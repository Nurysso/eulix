use anyhow::{anyhow, Result};
use ndarray::{Array2, Axis};
use ort::session::builder::GraphOptimizationLevel;
use ort::session::Session;
use ort::value::Value;
use tokenizers::Tokenizer;
use std::path::PathBuf;
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};  // ADD THIS

use crate::embedder::EmbedderConfig;

#[derive(Debug, Clone, Copy)]
pub enum DeviceType {
    Cuda,
    Rocm,
    Cpu,
}

#[derive(Debug, Clone, Copy)]
enum ModelType {
    // Bert,
    // Sentence,
    Standard,
    MPNet,
}

pub struct OnnxBackend {
    session: Mutex<Session>,
    tokenizer: Tokenizer,
    dimension: AtomicUsize,  // CHANGED: was usize, now AtomicUsize
    normalize: bool,
    model_type: ModelType,
}

impl OnnxBackend {
    pub fn new(config: &EmbedderConfig, device_type: DeviceType) -> Result<Self> {
        println!("     Loading ONNX model...");

        let model_type = Self::detect_model_type(&config.model_name);
        println!("     Detected model type: {:?}", model_type);

        // Start with config dimension, but we'll update it on first inference
        let dimension = config.dimension;
        println!("     Initial dimension (from config): {}", dimension);

        let model_path = Self::download_model(&config.model_name)?;
        let model_bytes = std::fs::read(&model_path)
            .map_err(|e| anyhow!("Failed to read model file: {}", e))?;

        println!("     Configuring execution providers for {:?}...", device_type);

        let session = match device_type {
            DeviceType::Cuda => {
                println!("     Initializing CUDA execution provider...");
                Session::builder()
                    .map_err(|e| anyhow!("Failed to create session builder: {:?}", e))?
                    .with_optimization_level(GraphOptimizationLevel::Level3)
                    .map_err(|e| anyhow!("Failed to set optimization level: {:?}", e))?
                    .with_intra_threads(4)
                    .map_err(|e| anyhow!("Failed to set intra threads: {:?}", e))?
                    .with_execution_providers([
                        ort::execution_providers::CUDAExecutionProvider::default()
                            .build()
                    ])
                    .map_err(|e| anyhow!("Failed to set CUDA execution provider: {:?}", e))?
                    .commit_from_memory(&model_bytes)
                    .map_err(|e| anyhow!("Failed to load model: {:?}", e))?
            }
            DeviceType::Rocm => {
                println!("     Initializing ROCm execution provider...");
                Session::builder()
                    .map_err(|e| anyhow!("Failed to create session builder: {:?}", e))?
                    .with_optimization_level(GraphOptimizationLevel::Level3)
                    .map_err(|e| anyhow!("Failed to set optimization level: {:?}", e))?
                    .with_intra_threads(4)
                    .map_err(|e| anyhow!("Failed to set intra threads: {:?}", e))?
                    .with_execution_providers([
                        ort::execution_providers::ROCmExecutionProvider::default()
                            .build()
                    ])
                    .map_err(|e| anyhow!("Failed to set ROCm execution provider: {:?}", e))?
                    .commit_from_memory(&model_bytes)
                    .map_err(|e| anyhow!("Failed to load model: {:?}", e))?
            }
            DeviceType::Cpu => {
                println!("     Initializing CPU execution provider...");
                Session::builder()
                    .map_err(|e| anyhow!("Failed to create session builder: {:?}", e))?
                    .with_optimization_level(GraphOptimizationLevel::Level3)
                    .map_err(|e| anyhow!("Failed to set optimization level: {:?}", e))?
                    .with_intra_threads(num_cpus::get())
                    .map_err(|e| anyhow!("Failed to set intra threads: {:?}", e))?
                    .commit_from_memory(&model_bytes)
                    .map_err(|e| anyhow!("Failed to load model: {:?}", e))?
            }
        };

        println!("     Device initialized: {:?}", device_type);

        let tokenizer_path = if let Some(ref local_path) = config.model_path {
            println!("     Using local tokenizer from: {:?}", local_path);
            local_path.join("tokenizer.json")
        } else {
            println!("     Downloading tokenizer from HuggingFace Hub...");
            let api = hf_hub::api::sync::Api::new()
                .map_err(|e| anyhow!("Failed to initialize HuggingFace API: {}. Try setting HF_HOME env variable", e))?;

            let repo_api = api.model(config.model_name.clone());
            repo_api.get("tokenizer.json")
                .map_err(|e| anyhow!("Failed to download tokenizer.json: {}", e))?
        };

        println!("     Loading tokenizer...");
        let tokenizer = Tokenizer::from_file(tokenizer_path)
            .map_err(|e| anyhow!("Failed to load tokenizer: {}", e))?;

        println!("     ONNX model loaded successfully!");

        Ok(Self {
            session: Mutex::new(session),
            tokenizer,
            dimension: AtomicUsize::new(dimension),  // CHANGED: wrap in AtomicUsize
            normalize: config.normalize,
            model_type,
        })
    }

    fn detect_model_type(model_name: &str) -> ModelType {
        let name_lower = model_name.to_lowercase();

        if name_lower.contains("mpnet") {
            ModelType::MPNet
        } else {
            ModelType::Standard
        }
        // } else if name_lower.contains("minilm") || name_lower.contains("all-minilm") {
            // ModelType::Sentence
        // } else {
            // ModelType::Bert
        // }
    }

    fn download_model(model_name: &str) -> Result<PathBuf> {
        println!("     Downloading ONNX model from HuggingFace Hub...");

        let api = hf_hub::api::sync::Api::new()
            .map_err(|e| anyhow!("Failed to initialize HuggingFace API: {}", e))?;

        let repo_api = api.model(model_name.to_string());

        let model_path = repo_api.get("onnx/model.onnx")
            .or_else(|_| repo_api.get("model.onnx"))
            .map_err(|e| anyhow!("Failed to download ONNX model: {}. Make sure the model has an ONNX version available.", e))?;

        println!("     Model downloaded successfully");
        Ok(model_path)
    }

    pub fn generate_embedding(&self, text: &str) -> Result<Vec<f32>> {
        const MAX_TOKENS: usize = 512;

        let encoding = self
            .tokenizer
            .encode(text, true)
            .map_err(|e| anyhow!("Tokenization failed: {}", e))?;

        let mut input_ids = encoding.get_ids().to_vec();
        let mut attention_mask = encoding.get_attention_mask().to_vec();
        let mut token_type_ids = encoding.get_type_ids().to_vec();

        if input_ids.len() > MAX_TOKENS {
            input_ids.truncate(MAX_TOKENS);
            attention_mask.truncate(MAX_TOKENS);
            token_type_ids.truncate(MAX_TOKENS);
        }

        let seq_len = input_ids.len();

        let input_ids_i64: Vec<i64> = input_ids.iter().map(|&x| x as i64).collect();
        let attention_mask_i64: Vec<i64> = attention_mask.iter().map(|&x| x as i64).collect();
        let token_type_ids_i64: Vec<i64> = token_type_ids.iter().map(|&x| x as i64).collect();

        let input_ids_value = Value::from_array(([1, seq_len], input_ids_i64))
            .map_err(|e| anyhow!("Failed to create input_ids tensor: {:?}", e))?;

        let attention_mask_value = Value::from_array(([1, seq_len], attention_mask_i64))
            .map_err(|e| anyhow!("Failed to create attention_mask tensor: {:?}", e))?;

        let mut session_guard = self.session.lock()
            .map_err(|e| anyhow!("Failed to lock session: {}", e))?;

        let outputs = match self.model_type {
            ModelType::MPNet => {
                // MPNet: only needs input_ids and attention_mask
                let inputs = ort::inputs![
                    "input_ids" => input_ids_value,
                    "attention_mask" => attention_mask_value,
                ];
                session_guard.run(inputs)
                    .map_err(|e| anyhow!("Failed to run inference: {:?}", e))?
            }
            ModelType::Standard => {
                // Standard BERT-like models: need token_type_ids too
                let token_type_ids_value = Value::from_array(([1, seq_len], token_type_ids_i64))
                    .map_err(|e| anyhow!("Failed to create token_type_ids tensor: {:?}", e))?;

                let inputs = ort::inputs![
                    "input_ids" => input_ids_value,
                    "attention_mask" => attention_mask_value,
                    "token_type_ids" => token_type_ids_value,
                ];
                session_guard.run(inputs)
                    .map_err(|e| anyhow!("Failed to run inference: {:?}", e))?
            }
        };

        let output_name = "last_hidden_state";

        let Ok((output_shape, embeddings_data)) = outputs
            .get(output_name)
            .ok_or_else(|| {
                let available: Vec<String> = outputs
                    .iter()
                    .map(|(name, _)| name.to_string())
                    .collect();
                anyhow!(
                    "No output named '{}'. Available outputs: {:?}",
                    output_name,
                    available
                )
            })?
            .try_extract_tensor::<f32>() else { todo!() };


            // Get actual dimension from model output
        let actual_hidden_dim = if output_shape.len() == 3 {
            output_shape[2] as usize
        } else {
            return Err(anyhow!(
                "Unexpected output shape dimensions: {:?}. Expected [batch, seq_len, hidden_dim]",
                output_shape
            ));
        };

        // Update stored dimension if this is the first time we see the real value
        let stored_dim = self.dimension.load(Ordering::Relaxed);
        if actual_hidden_dim != stored_dim {
            println!(
                "     ✓ Actual model dimension: {}d (config estimated: {}d)",
                actual_hidden_dim, stored_dim
            );
            self.dimension.store(actual_hidden_dim, Ordering::Relaxed);
        }

        let expected_elements = seq_len * actual_hidden_dim;

        if embeddings_data.len() != expected_elements {
            return Err(anyhow!(
                "Unexpected embedding shape. Expected {} elements ({}x{}), got {}. Output shape: {:?}",
                expected_elements,
                seq_len,
                actual_hidden_dim,
                embeddings_data.len(),
                output_shape
            ));
        }

        let embeddings = Array2::from_shape_vec((seq_len, actual_hidden_dim), embeddings_data.to_vec())
            .map_err(|e| anyhow!("Failed to reshape embeddings: {}", e))?;

        let attention_mask_f32: Vec<f32> = attention_mask.iter().map(|&x| x as f32).collect();
        let attention_mask_array = Array2::from_shape_vec((seq_len, 1), attention_mask_f32)
            .map_err(|e| anyhow!("Failed to create attention mask array: {}", e))?;

        let attention_expanded = attention_mask_array
            .broadcast((seq_len, actual_hidden_dim))
            .ok_or_else(|| anyhow!("Failed to broadcast attention mask"))?;

        let masked_embeddings = &embeddings * &attention_expanded;
        let sum_embeddings = masked_embeddings.sum_axis(Axis(0));
        let sum_mask = attention_expanded.sum_axis(Axis(0));

        let mut embedding: Vec<f32> = sum_embeddings
            .iter()
            .zip(sum_mask.iter())
            .map(|(sum, mask)| if *mask > 0.0 { sum / mask } else { 0.0 })
            .collect();

        assert_eq!(embedding.len(), actual_hidden_dim, "Embedding size mismatch");

        if self.normalize {
            Self::normalize_vector(&mut embedding);
        }

        Ok(embedding)
    }

    pub fn generate_embeddings_batch(&self, texts: &[&str]) -> Result<Vec<Vec<f32>>> {
        if texts.is_empty() {
            return Ok(Vec::new());
        }

        const MAX_TOKENS: usize = 512;
        let batch_size = texts.len();

        // Tokenize all texts
        let encodings: Vec<_> = texts
            .iter()
            .map(|text| {
                self.tokenizer
                    .encode(*text, true)
                    .map_err(|e| anyhow!("Tokenization failed: {}", e))
            })
            .collect::<Result<Vec<_>>>()?;

        // Find max sequence length in batch (for padding)
        let max_seq_len = encodings
            .iter()
            .map(|enc| enc.get_ids().len().min(MAX_TOKENS))
            .max()
            .unwrap_or(0);

        // Prepare batched tensors with padding
        let mut batch_input_ids = Vec::with_capacity(batch_size * max_seq_len);
        let mut batch_attention_mask = Vec::with_capacity(batch_size * max_seq_len);
        let mut batch_token_type_ids = Vec::with_capacity(batch_size * max_seq_len);

        for encoding in &encodings {
            let mut input_ids = encoding.get_ids().to_vec();
            let mut attention_mask = encoding.get_attention_mask().to_vec();
            let mut token_type_ids = encoding.get_type_ids().to_vec();

            // Truncate if needed
            if input_ids.len() > MAX_TOKENS {
                input_ids.truncate(MAX_TOKENS);
                attention_mask.truncate(MAX_TOKENS);
                token_type_ids.truncate(MAX_TOKENS);
            }

            let _seq_len = input_ids.len();

            // Pad to max_seq_len
            input_ids.resize(max_seq_len, 0);
            attention_mask.resize(max_seq_len, 0);
            token_type_ids.resize(max_seq_len, 0);

            // Add to batch
            batch_input_ids.extend(input_ids.iter().map(|&x| x as i64));
            batch_attention_mask.extend(attention_mask.iter().map(|&x| x as i64));
            batch_token_type_ids.extend(token_type_ids.iter().map(|&x| x as i64));
        }

        // Create tensors
        let input_ids_value = Value::from_array(([batch_size, max_seq_len], batch_input_ids))
            .map_err(|e| anyhow!("Failed to create input_ids tensor: {:?}", e))?;

        let attention_mask_value = Value::from_array(([batch_size, max_seq_len], batch_attention_mask.clone()))
            .map_err(|e| anyhow!("Failed to create attention_mask tensor: {:?}", e))?;

        let mut session_guard = self.session.lock()
            .map_err(|e| anyhow!("Failed to lock session: {}", e))?;

        // Run inference
        let outputs = match self.model_type {
            ModelType::MPNet => {
                let inputs = ort::inputs![
                    "input_ids" => input_ids_value,
                    "attention_mask" => attention_mask_value,
                ];
                session_guard.run(inputs)
                    .map_err(|e| anyhow!("Failed to run inference: {:?}", e))?
            }
            ModelType::Standard => {
                let token_type_ids_value = Value::from_array(([batch_size, max_seq_len], batch_token_type_ids))
                    .map_err(|e| anyhow!("Failed to create token_type_ids tensor: {:?}", e))?;

                let inputs = ort::inputs![
                    "input_ids" => input_ids_value,
                    "attention_mask" => attention_mask_value,
                    "token_type_ids" => token_type_ids_value,
                ];
                session_guard.run(inputs)
                    .map_err(|e| anyhow!("Failed to run inference: {:?}", e))?
            }
        };

        let output_name = "last_hidden_state";
        let Ok((output_shape, embeddings_data)) = outputs
            .get(output_name)
            .ok_or_else(|| anyhow!("No output named '{}'", output_name))?
            .try_extract_tensor::<f32>() else {
                return Err(anyhow!("Failed to extract tensor"));
            };

        // Get actual dimension from model output
        let actual_hidden_dim = if output_shape.len() == 3 {
            output_shape[2] as usize
        } else {
            return Err(anyhow!("Unexpected output shape: {:?}", output_shape));
        };

        // Update stored dimension if needed
        let stored_dim = self.dimension.load(Ordering::Relaxed);
        if actual_hidden_dim != stored_dim {
            println!(
                "     ✓ Actual model dimension: {}d (config estimated: {}d)",
                actual_hidden_dim, stored_dim
            );
            self.dimension.store(actual_hidden_dim, Ordering::Relaxed);
        }

        // Process each item in the batch
        let mut result = Vec::with_capacity(batch_size);

        for i in 0..batch_size {
            let start_idx = i * max_seq_len * actual_hidden_dim;
            let end_idx = start_idx + (max_seq_len * actual_hidden_dim);
            let item_embeddings = &embeddings_data[start_idx..end_idx];

            // Reshape to [seq_len, hidden_dim]
            let embeddings = Array2::from_shape_vec((max_seq_len, actual_hidden_dim), item_embeddings.to_vec())
                .map_err(|e| anyhow!("Failed to reshape embeddings: {}", e))?;

            // Get attention mask for this item
            let attention_start = i * max_seq_len;
            let attention_end = attention_start + max_seq_len;
            let attention_mask_f32: Vec<f32> = batch_attention_mask[attention_start..attention_end]
                .iter()
                .map(|&x| x as f32)
                .collect();

            let attention_mask_array = Array2::from_shape_vec((max_seq_len, 1), attention_mask_f32)
                .map_err(|e| anyhow!("Failed to create attention mask array: {}", e))?;

            let attention_expanded = attention_mask_array
                .broadcast((max_seq_len, actual_hidden_dim))
                .ok_or_else(|| anyhow!("Failed to broadcast attention mask"))?;

            // Mean pooling
            let masked_embeddings = &embeddings * &attention_expanded;
            let sum_embeddings = masked_embeddings.sum_axis(Axis(0));
            let sum_mask = attention_expanded.sum_axis(Axis(0));

            let mut embedding: Vec<f32> = sum_embeddings
                .iter()
                .zip(sum_mask.iter())
                .map(|(sum, mask)| if *mask > 0.0 { sum / mask } else { 0.0 })
                .collect();

            if self.normalize {
                Self::normalize_vector(&mut embedding);
            }

            result.push(embedding);
        }

        Ok(result)
    }

    fn normalize_vector(vec: &mut [f32]) {
        let magnitude: f32 = vec.iter().map(|x| x * x).sum::<f32>().sqrt();
        if magnitude > 1e-12 {
            vec.iter_mut().for_each(|x| *x /= magnitude);
        }
    }

    pub fn dimension(&self) -> usize {
        self.dimension.load(Ordering::Relaxed)  // CHANGED: load from atomic
    }
}
