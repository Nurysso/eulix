//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use anyhow::{Context, Result};
use rayon::prelude::*;
use std::path::PathBuf;

use crate::chunker::Chunk;
use crate::context::VectorStore;
use crate::onnx_backend::{DeviceType, OnnxBackend};

/// Embedding backend types
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EmbeddingBackend {
    /// ONNX with CUDA (NVIDIA)
    OnnxCuda,
    /// ONNX with ROCm (AMD)
    OnnxRocm,
    /// ONNX with CPU
    OnnxCpu,
    /// Dummy embeddings (testing only)
    Dummy,
}

impl std::str::FromStr for EmbeddingBackend {
    type Err = anyhow::Error;

    fn from_str(s: &str) -> Result<Self> {
        match s.to_lowercase().as_str() {
            "onnx-cuda" | "cuda" => Ok(Self::OnnxCuda),
            "onnx-rocm" | "rocm" => Ok(Self::OnnxRocm),
            "onnx-cpu" | "cpu" => Ok(Self::OnnxCpu),
            "onnx" | "auto" => Ok(Self::auto_detect()),
            "dummy" | "test" => Ok(Self::Dummy),
            _ => Err(anyhow::anyhow!(
                "Unknown backend: {}. Options: auto, cuda, rocm, cpu, dummy",
                s
            )),
        }
    }
}

impl EmbeddingBackend {
    pub fn auto_detect() -> Self {
        println!("  Auto-detecting GPU backend...");

        if Self::is_cuda_available() {
            println!("  ✓ NVIDIA GPU detected - using CUDA acceleration");
            return Self::OnnxCuda;
        }

        if Self::is_rocm_available() {
            println!("  ✓ AMD GPU detected - using ROCm acceleration");
            return Self::OnnxRocm;
        }

        println!("  ℹ No GPU detected - using CPU backend");
        println!("    For faster embeddings, consider installing CUDA or ROCm");
        Self::OnnxCpu
    }

    fn is_cuda_available() -> bool {
        if std::env::var("CUDA_PATH").is_ok() || std::env::var("CUDA_HOME").is_ok() {
            return true;
        }
        let cuda_paths = [
            "/usr/local/cuda",
            "/usr/local/cuda-12",
            "/usr/local/cuda-11",
            "/opt/cuda",
            "C:\\Program Files\\NVIDIA GPU Computing Toolkit\\CUDA",
        ];
        for path in &cuda_paths {
            if std::path::Path::new(path).exists() {
                return true;
            }
        }
        if let Ok(output) = std::process::Command::new("nvidia-smi").output() {
            return output.status.success();
        }
        false
    }

    fn is_rocm_available() -> bool {
        if std::env::var("ROCM_PATH").is_ok() || std::env::var("ROCM_HOME").is_ok() {
            return true;
        }
        let rocm_paths = ["/opt/rocm", "/opt/rocm-5", "/opt/rocm-6"];
        for path in &rocm_paths {
            if std::path::Path::new(path).exists() {
                return true;
            }
        }
        if let Ok(output) = std::process::Command::new("rocm-smi").output() {
            return output.status.success();
        }
        false
    }

    pub fn description(&self) -> &str {
        match self {
            Self::OnnxCuda => "ONNX Runtime with CUDA (NVIDIA GPU)",
            Self::OnnxRocm => "ONNX Runtime with ROCm (AMD GPU)",
            Self::OnnxCpu => "ONNX Runtime with CPU",
            Self::Dummy => "Dummy embeddings (testing)",
        }
    }
}

pub struct EmbedderConfig {
    pub backend: EmbeddingBackend,
    pub model_name: String,
    pub model_path: Option<PathBuf>,
    pub dimension: usize,
    pub batch_size: usize,
    pub normalize: bool,
}

impl Default for EmbedderConfig {
    fn default() -> Self {
        let backend = EmbeddingBackend::auto_detect();
        let batch_size = match backend {
            EmbeddingBackend::OnnxCuda => 128,
            // ROCm has higher per-batch overhead than CUDA; smaller batches
            // reduce padding waste and keep latency tighter.
            EmbeddingBackend::OnnxRocm => 32,
            EmbeddingBackend::OnnxCpu => 32,
            EmbeddingBackend::Dummy => 32,
        };
        Self {
            backend,
            model_name: "sentence-transformers/all-MiniLM-L6-v2".to_string(),
            model_path: None,
            dimension: 384,
            batch_size,
            normalize: true,
        }
    }
}

pub struct EmbeddingGenerator {
    config: EmbedderConfig,
    backend_impl: Box<dyn EmbeddingBackendTrait + Send + Sync>,
}

impl EmbeddingGenerator {
    pub fn new(model_name: &str) -> Result<Self> {
        let config = EmbedderConfig {
            model_name: model_name.to_string(),
            ..Default::default()
        };
        Self::with_config(config)
    }

    pub fn with_config(config: EmbedderConfig) -> Result<Self> {
        println!("     Initializing embedding generator:");
        println!("     Backend: {}", config.backend.description());
        println!("     Model: {}", config.model_name);
        println!("     Dimension: {}", config.dimension);

        let backend_impl: Box<dyn EmbeddingBackendTrait + Send + Sync> = match config.backend {
            EmbeddingBackend::OnnxCuda => Self::try_create_onnx_backend(&config, DeviceType::Cuda)?,
            EmbeddingBackend::OnnxRocm => Self::try_create_onnx_backend(&config, DeviceType::Rocm)?,
            EmbeddingBackend::OnnxCpu => Self::try_create_onnx_backend(&config, DeviceType::Cpu)?,
            EmbeddingBackend::Dummy => Box::new(DummyBackend::new(&config)),
        };

        println!("  ✓ Embedding generator ready!");

        Ok(Self {
            config,
            backend_impl,
        })
    }

    fn try_create_onnx_backend(
        config: &EmbedderConfig,
        device_type: DeviceType,
    ) -> Result<Box<dyn EmbeddingBackendTrait + Send + Sync>> {
        match OnnxBackend::new(config, device_type) {
            Ok(backend) => Ok(Box::new(backend)),
            Err(e) => {
                eprintln!("\n:(  Failed to initialize ONNX backend: {}", e);
                eprintln!("    Common issues:");
                eprintln!("    - No internet connection for model download");
                eprintln!("    - HuggingFace Hub API issues");
                eprintln!("    - Missing ONNX model files");
                eprintln!("    - GPU driver issues");
                eprintln!("\n    Solutions:");
                eprintln!("    1. Check internet connection");
                eprintln!("    2. Set HF_HOME environment variable");
                eprintln!("    3. Download ONNX model manually and use --model-path");
                eprintln!("    4. Try CPU backend: --backend cpu");
                eprintln!("    5. Use dummy backend: --backend dummy");
                eprintln!("\n    Falling back to dummy embeddings for now...\n");
                Ok(Box::new(DummyBackend::new(config)))
            }
        }
    }

    /// Single-embed sequential path. Faster than batched inference on a single
    /// ROCm/CUDA session because each call uses the exact sequence length with
    /// zero padding. Batching only wins with a session pool (multiple concurrent
    /// ONNX sessions), which is a TODO for a future refactor.
    pub fn generate_vectors(&self, chunks: Vec<Chunk>) -> Result<VectorStore> {
        let total = chunks.len();
        let mut store = VectorStore::new();

        println!(" Processing {} chunks...", total);
        let start = std::time::Instant::now();

        for (i, chunk) in chunks.iter().enumerate() {
            if i > 0 && i % 100 == 0 {
                let elapsed = start.elapsed().as_secs_f32();
                let rate = i as f32 / elapsed;
                let eta = ((total - i) as f32 / rate).round();
                println!(
                    "     Progress: {}/{} ({:.1} chunks/sec, ETA: {:.0}s)",
                    i, total, rate, eta
                );
            }

            let embedding = self
                .backend_impl
                .generate_embedding(&chunk.content)
                .with_context(|| format!("Failed to embed chunk: {}", chunk.id))?;

            store.add(chunk.id.clone(), embedding);
        }

        let elapsed = start.elapsed();
        println!(
            "  ✓ Completed all embeddings in {:.2}s",
            elapsed.as_secs_f32()
        );
        println!(
            "     Average: {:.1} chunks/sec",
            total as f32 / elapsed.as_secs_f32()
        );

        Ok(store)
    }

    /// FIX 2: parallel path now propagates errors instead of panicking via .expect().
    /// Each thread returns a Result; failures are collected and the first one surfaced.
    /// Note: inference still serializes on the session Mutex — true parallelism here
    /// requires a session pool, which is a larger refactor.
    pub fn generate_vectors_parallel(&self, chunks: Vec<Chunk>) -> Result<VectorStore> {
        let total = chunks.len();

        println!(" Processing {} chunks in parallel...", total);
        let start = std::time::Instant::now();

        let results: Vec<Result<(String, Vec<f32>)>> = chunks
            .into_par_iter()
            .map(|chunk| {
                let embedding = self
                    .backend_impl
                    .generate_embedding(&chunk.content)
                    .with_context(|| format!("Failed to embed chunk: {}", chunk.id))?;
                Ok((chunk.id, embedding))
            })
            .collect();

        // Surface the first error, if any.
        let pairs: Vec<(String, Vec<f32>)> = results.into_iter().collect::<Result<Vec<_>>>()?;

        let elapsed = start.elapsed();
        println!(
            "  ✓ Completed all embeddings in {:.2}s",
            elapsed.as_secs_f32()
        );
        println!(
            "     Average: {:.1} chunks/sec",
            total as f32 / elapsed.as_secs_f32()
        );

        let mut store = VectorStore::new();
        for (id, vector) in pairs {
            store.add(id, vector);
        }

        Ok(store)
    }

    pub fn dimension(&self) -> usize {
        self.config.dimension
    }

    pub fn backend(&self) -> EmbeddingBackend {
        self.config.backend
    }

    pub fn model_name(&self) -> &str {
        &self.config.model_name
    }
}

trait EmbeddingBackendTrait {
    fn generate_embedding(&self, text: &str) -> Result<Vec<f32>>;
    /// Batch variant — default impl falls back to calling generate_embedding per item.
    /// Real backends should override this with a true batched call.
    fn generate_embeddings_batch(&self, texts: &[&str]) -> Result<Vec<Vec<f32>>> {
        texts.iter().map(|t| self.generate_embedding(t)).collect()
    }
    fn dimension(&self) -> usize;
}

impl EmbeddingBackendTrait for OnnxBackend {
    fn generate_embedding(&self, text: &str) -> Result<Vec<f32>> {
        self.generate_embedding(text)
    }

    /// Delegates to the real batched ONNX call in onnx_backend.rs.
    fn generate_embeddings_batch(&self, texts: &[&str]) -> Result<Vec<Vec<f32>>> {
        self.generate_embeddings_batch(texts)
    }

    fn dimension(&self) -> usize {
        self.dimension()
    }
}

// Dummy Backend
struct DummyBackend {
    dimension: usize,
    normalize: bool,
}

impl DummyBackend {
    fn new(config: &EmbedderConfig) -> Self {
        println!("     :(  Using dummy embeddings (for testing only)");
        println!("        These are hash-based, not semantically meaningful");
        println!("        Use for testing pipeline, not production!");
        Self {
            dimension: config.dimension,
            normalize: config.normalize,
        }
    }
}

impl EmbeddingBackendTrait for DummyBackend {
    fn generate_embedding(&self, text: &str) -> Result<Vec<f32>> {
        Ok(dummy_embedding(text, self.dimension, self.normalize))
    }

    fn dimension(&self) -> usize {
        self.dimension
    }
}

/// FIX 3: replaced DefaultHasher (non-deterministic across Rust versions/processes)
/// with a simple seeded LCG so dummy vectors are stable for regression testing.
fn dummy_embedding(text: &str, dimension: usize, normalize: bool) -> Vec<f32> {
    // Stable FNV-1a seed from the input text.
    let mut seed: u64 = 0xcbf29ce484222325;
    for byte in text.bytes() {
        seed ^= byte as u64;
        seed = seed.wrapping_mul(0x100000001b3);
    }

    let mut embedding = Vec::with_capacity(dimension);
    for _ in 0..dimension {
        // Park-Miller LCG — cheap, portable, stable.
        seed = seed
            .wrapping_mul(6364136223846793005)
            .wrapping_add(1442695040888963407);
        let normalized = (seed as f32 / u64::MAX as f32) * 2.0 - 1.0;
        embedding.push(normalized);
    }

    if normalize {
        normalize_vector(&mut embedding);
    }

    embedding
}

fn normalize_vector(vec: &mut [f32]) {
    let magnitude: f32 = vec.iter().map(|x| x * x).sum::<f32>().sqrt();
    if magnitude > 1e-12 {
        vec.iter_mut().for_each(|x| *x /= magnitude);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_dummy_backend() {
        let config = EmbedderConfig::default();
        let backend = DummyBackend::new(&config);
        let embedding = backend.generate_embedding("test").unwrap();
        assert_eq!(embedding.len(), 384);
    }

    #[test]
    fn test_dummy_determinism() {
        // Vectors must be identical across calls — the whole point of the FNV fix.
        let config = EmbedderConfig::default();
        let backend = DummyBackend::new(&config);
        let a = backend.generate_embedding("hello world").unwrap();
        let b = backend.generate_embedding("hello world").unwrap();
        assert_eq!(a, b);
    }

    #[test]
    fn test_dummy_distinct() {
        let config = EmbedderConfig::default();
        let backend = DummyBackend::new(&config);
        let a = backend.generate_embedding("foo").unwrap();
        let b = backend.generate_embedding("bar").unwrap();
        assert_ne!(a, b);
    }

    #[test]
    fn test_normalization() {
        let mut vec = vec![3.0, 4.0];
        normalize_vector(&mut vec);
        let magnitude: f32 = vec.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!((magnitude - 1.0).abs() < 1e-6);
    }

    #[test]
    fn test_backend_parsing() {
        assert!(matches!(
            "dummy".parse::<EmbeddingBackend>().unwrap(),
            EmbeddingBackend::Dummy
        ));
        assert!(matches!(
            "cpu".parse::<EmbeddingBackend>().unwrap(),
            EmbeddingBackend::OnnxCpu
        ));
    }
}
