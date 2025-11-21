use anyhow::{anyhow, Context, Result};
use rayon::prelude::*;
use std::path::PathBuf;

use crate::chunker::Chunk;
use crate::context::VectorStore;
use crate::onnx_backend::{OnnxBackend, DeviceType};

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
            _ => Err(anyhow!("Unknown backend: {}. Options: auto, cuda, rocm, cpu, dummy", s)),
        }
    }
}

impl EmbeddingBackend {
    /// Auto-detect the best available backend
    pub fn auto_detect() -> Self {
        println!("  Auto-detecting GPU backend...");

        // Check for CUDA
        if Self::is_cuda_available() {
            println!("  ✓ NVIDIA GPU detected - using CUDA acceleration");
            return Self::OnnxCuda;
        }

        // Check for ROCm
        if Self::is_rocm_available() {
            println!("  ✓ AMD GPU detected - using ROCm acceleration");
            return Self::OnnxRocm;
        }

        println!("  ℹ No GPU detected - using CPU backend");
        println!("    For faster embeddings, consider installing CUDA or ROCm");
        Self::OnnxCpu
    }

    fn is_cuda_available() -> bool {
        // Check multiple indicators for CUDA availability
        if std::env::var("CUDA_PATH").is_ok() || std::env::var("CUDA_HOME").is_ok() {
            return true;
        }

        // Common CUDA installation paths
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

        // Check if nvidia-smi is available
        if let Ok(output) = std::process::Command::new("nvidia-smi").output() {
            return output.status.success();
        }

        false
    }

    fn is_rocm_available() -> bool {
        // Check for ROCm installation
        if std::env::var("ROCM_PATH").is_ok() || std::env::var("ROCM_HOME").is_ok() {
            return true;
        }

        // Common ROCm installation paths
        let rocm_paths = [
            "/opt/rocm",
            "/opt/rocm-5",
            "/opt/rocm-6",
        ];

        for path in &rocm_paths {
            if std::path::Path::new(path).exists() {
                return true;
            }
        }

        // Check if rocm-smi is available
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

/// Configuration for the embedding generator
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
        // Use larger batch size for GPU backends
        let batch_size = match backend {
            EmbeddingBackend::OnnxCuda | EmbeddingBackend::OnnxRocm => 128,
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
    /// Create a new embedding generator with auto-detected backend
    pub fn new(model_name: &str) -> Result<Self> {
        let config = EmbedderConfig {
            model_name: model_name.to_string(),
            ..Default::default()
        };
        Self::with_config(config)
    }

    /// Create with explicit configuration
    pub fn with_config(config: EmbedderConfig) -> Result<Self> {
        println!("     Initializing embedding generator:");
        println!("     Backend: {}", config.backend.description());
        println!("     Model: {}", config.model_name);
        println!("     Dimension: {}", config.dimension);

        let backend_impl: Box<dyn EmbeddingBackendTrait + Send + Sync> = match config.backend {
            EmbeddingBackend::OnnxCuda => {
                Self::try_create_onnx_backend(&config, DeviceType::Cuda)?
            }
            EmbeddingBackend::OnnxRocm => {
                Self::try_create_onnx_backend(&config, DeviceType::Rocm)?
            }
            EmbeddingBackend::OnnxCpu => {
                Self::try_create_onnx_backend(&config, DeviceType::Cpu)?
            }
            EmbeddingBackend::Dummy => {
                Box::new(DummyBackend::new(&config))
            }
        };

        println!("  ✓ Embedding generator ready!");

        Ok(Self {
            config,
            backend_impl,
        })
    }

    /// Try to create ONNX backend with fallback to dummy
    fn try_create_onnx_backend(
        config: &EmbedderConfig,
        device_type: DeviceType
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

    pub fn generate_vectors(&self, chunks: Vec<Chunk>) -> Result<VectorStore> {
        let total = chunks.len();
        let mut store = VectorStore::new();

        println!(" Processing {} chunks in batches...", total);
        let start = std::time::Instant::now();

        let batch_size = self.config.batch_size;

        for (batch_idx, chunk_batch) in chunks.chunks(batch_size).enumerate() {
            let batch_start = batch_idx * batch_size;

            if batch_start % 100 == 0 && batch_start > 0 {
                let elapsed = start.elapsed().as_secs_f32();
                let rate = batch_start as f32 / elapsed;
                let eta = ((total - batch_start) as f32 / rate).round();
                println!("     Progress: {}/{} ({:.1} chunks/sec, ETA: {:.0}s)",
                         batch_start, total, rate, eta);
            }

            for chunk in chunk_batch {
                let embedding = self.backend_impl
                    .generate_embedding(&chunk.content)
                    .context(format!("Failed to generate embedding for chunk: {}", chunk.id))?;

                store.add(chunk.id.clone(), embedding);
            }
        }

        let elapsed = start.elapsed();
        println!("  ✓ Completed all embeddings in {:.2}s", elapsed.as_secs_f32());
        println!("     Average: {:.1} chunks/sec", total as f32 / elapsed.as_secs_f32());

        Ok(store)
    }

    /// Parallel processing version (for CPU/multi-GPU scenarios)
    pub fn generate_vectors_parallel(&self, chunks: Vec<Chunk>) -> Result<VectorStore> {
        let total = chunks.len();
        let mut store = VectorStore::new();

        println!(" Processing {} chunks in parallel...", total);
        let start = std::time::Instant::now();

        let results: Vec<(String, Vec<f32>)> = chunks
            .into_par_iter()
            .enumerate()
            .map(|(idx, chunk)| {
                if idx % 100 == 0 && idx > 0 {
                    let elapsed = start.elapsed().as_secs_f32();
                    let rate = idx as f32 / elapsed;
                    println!("     Progress: {}/{} ({:.1} chunks/sec)", idx, total, rate);
                }

                let embedding = self.backend_impl
                    .generate_embedding(&chunk.content)
                    .expect("Failed to generate embedding");

                (chunk.id, embedding)
            })
            .collect();

        let elapsed = start.elapsed();
        println!("  ✓ Completed all embeddings in {:.2}s", elapsed.as_secs_f32());
        println!("     Average: {:.1} chunks/sec", total as f32 / elapsed.as_secs_f32());

        for (id, vector) in results {
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

/// Trait for different embedding backends
trait EmbeddingBackendTrait {
    fn generate_embedding(&self, text: &str) -> Result<Vec<f32>>;
    fn dimension(&self) -> usize;
}

impl EmbeddingBackendTrait for OnnxBackend {
    fn generate_embedding(&self, text: &str) -> Result<Vec<f32>> {
        self.generate_embedding(text)
    }

    fn dimension(&self) -> usize {
        self.dimension()
    }
}

// Dummy Backend (for testing)
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

// Helper Functions
fn dummy_embedding(text: &str, dimension: usize, normalize: bool) -> Vec<f32> {
    use std::collections::hash_map::DefaultHasher;
    use std::hash::{Hash, Hasher};

    let mut hasher = DefaultHasher::new();
    text.hash(&mut hasher);
    let hash = hasher.finish();

    let mut embedding = Vec::with_capacity(dimension);
    let mut seed = hash;

    for _ in 0..dimension {
        seed = seed.wrapping_mul(1664525).wrapping_add(1013904223);
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
    fn test_normalization() {
        let mut vec = vec![3.0, 4.0];
        normalize_vector(&mut vec);

        let magnitude: f32 = vec.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!((magnitude - 1.0).abs() < 1e-6);
    }

    #[test]
    fn test_backend_parsing() {
        assert!(matches!("dummy".parse::<EmbeddingBackend>().unwrap(), EmbeddingBackend::Dummy));
        assert!(matches!("cpu".parse::<EmbeddingBackend>().unwrap(), EmbeddingBackend::OnnxCpu));
    }
}
