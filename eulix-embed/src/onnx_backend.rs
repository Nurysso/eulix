use anyhow::{anyhow, Result};
use ndarray::{Array2, Axis};
use ort::session::builder::GraphOptimizationLevel;
use ort::session::Session;
use ort::value::Value;
use tokenizers::Tokenizer;
use std::path::PathBuf;
use std::sync::Mutex;

use crate::embedder::EmbedderConfig;

#[derive(Debug, Clone, Copy)]
pub enum DeviceType {
    Cuda,
    Rocm,
    Cpu,
}

#[derive(Debug, Clone, Copy)]
enum ModelType {
    Bert,      // Uses last_hidden_state, requires token_type_ids
    Sentence,  // Uses token_embeddings, requires token_type_ids
    MPNet,     // Uses last_hidden_state, NO token_type_ids
}

pub struct OnnxBackend {
    session: Mutex<Session>,
    tokenizer: Tokenizer,
    dimension: usize,
    normalize: bool,
    model_type: ModelType,
}

impl OnnxBackend {
    pub fn new(config: &EmbedderConfig, device_type: DeviceType) -> Result<Self> {
        println!("     Loading ONNX model (this may take a moment)...");

        // Detect model type from model name
        let model_type = Self::detect_model_type(&config.model_name);
        println!("     Detected model type: {:?}", model_type);

        // Auto-detect dimension if not explicitly set or if it seems wrong
        let dimension = if config.dimension == 384 {
            let detected_dim = Self::detect_dimension(&config.model_name);
            if detected_dim != 384 {
                println!("     Auto-detected dimension: {} (overriding config: {})", detected_dim, config.dimension);
                detected_dim
            } else {
                config.dimension
            }
        } else {
            config.dimension
        };

        // Download model first
        let model_path = Self::download_model(&config.model_name)?;

        // Read model file into memory
        let model_bytes = std::fs::read(&model_path)
            .map_err(|e| anyhow!("Failed to read model file: {}", e))?;

        println!("     Configuring execution providers for {:?}...", device_type);

        // Configure execution provider based on device type
        let session = match device_type {
            DeviceType::Cuda => {
                println!("     Initializing CUDA execution provider...");
                println!("     Note: First run may be slow due to kernel compilation");
                Session::builder()
                    .map_err(|e| anyhow!("Failed to create session builder: {:?}", e))?
                    .with_optimization_level(GraphOptimizationLevel::Level3)
                    .map_err(|e| anyhow!("Failed to set optimization level: {:?}", e))?
                    .with_intra_threads(4)
                    .map_err(|e| anyhow!("Failed to set intra threads: {:?}", e))?
                    .commit_from_memory(&model_bytes)
                    .map_err(|e| anyhow!("Failed to load model: {:?}", e))?
            }
            DeviceType::Rocm => {
                println!("     Initializing ROCm execution provider...");
                println!("     Note: First run may be slow due to kernel compilation");
                Session::builder()
                    .map_err(|e| anyhow!("Failed to create session builder: {:?}", e))?
                    .with_optimization_level(GraphOptimizationLevel::Level3)
                    .map_err(|e| anyhow!("Failed to set optimization level: {:?}", e))?
                    .with_intra_threads(4)
                    .map_err(|e| anyhow!("Failed to set intra threads: {:?}", e))?
                    .commit_from_memory(&model_bytes)
                    .map_err(|e| anyhow!("Failed to load model: {:?}", e))?
            }
            DeviceType::Cpu => {
                println!("     Initializing CPU execution provider...");
                Session::builder()
                    .map_err(|e| anyhow!("Failed to create session builder: {:?}", e))?
                    .with_optimization_level(GraphOptimizationLevel::Level3)
                    .map_err(|e| anyhow!("Failed to set optimization level: {:?}", e))?
                    .with_intra_threads(4)
                    .map_err(|e| anyhow!("Failed to set intra threads: {:?}", e))?
                    .commit_from_memory(&model_bytes)
                    .map_err(|e| anyhow!("Failed to load model: {:?}", e))?
            }
        };

        println!("     Device initialized: {:?}", device_type);

        // Load tokenizer
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

        println!("     ✓ ONNX model loaded successfully!");

        Ok(Self {
            session: Mutex::new(session),
            tokenizer,
            dimension,
            normalize: config.normalize,
            model_type,
        })
    }

    fn detect_model_type(model_name: &str) -> ModelType {
        let name_lower = model_name.to_lowercase();

        if name_lower.contains("mpnet") {
            ModelType::MPNet
        } else if name_lower.contains("minilm") || name_lower.contains("all-minilm") {
            ModelType::Sentence
        } else {
            // Default to BERT-style (BGE models, etc.)
            ModelType::Bert
        }
    }

    fn detect_dimension(model_name: &str) -> usize {
        let name_lower = model_name.to_lowercase();

        // Explicitly check for known base/large models (768d)
        if name_lower.contains("base") || name_lower.contains("mpnet") {
            768
        } else if name_lower.contains("large") {
            1024
        } else {
            // Default to 384 for small models
            384
        }
    }

    fn download_model(model_name: &str) -> Result<PathBuf> {
        println!("     Downloading ONNX model from HuggingFace Hub...");

        let api = hf_hub::api::sync::Api::new()
            .map_err(|e| anyhow!("Failed to initialize HuggingFace API: {}", e))?;

        let repo_api = api.model(model_name.to_string());

        // Try to get ONNX model
        let model_path = repo_api.get("onnx/model.onnx")
            .or_else(|_| repo_api.get("model.onnx"))
            .map_err(|e| anyhow!("Failed to download ONNX model: {}. Make sure the model has an ONNX version available.", e))?;

        println!("     ✓ Model downloaded successfully");
        Ok(model_path)
    }

    pub fn generate_embedding(&self, text: &str) -> Result<Vec<f32>> {
        // Tokenize with truncation
        const MAX_TOKENS: usize = 512;

        let encoding = self
            .tokenizer
            .encode(text, true)
            .map_err(|e| anyhow!("Tokenization failed: {}", e))?;

        let mut input_ids = encoding.get_ids().to_vec();
        let mut attention_mask = encoding.get_attention_mask().to_vec();
        let mut token_type_ids = encoding.get_type_ids().to_vec();

        // Truncate if necessary
        if input_ids.len() > MAX_TOKENS {
            input_ids.truncate(MAX_TOKENS);
            attention_mask.truncate(MAX_TOKENS);
            token_type_ids.truncate(MAX_TOKENS);
        }

        let seq_len = input_ids.len();

        // Convert to i64 for ONNX
        let input_ids_i64: Vec<i64> = input_ids.iter().map(|&x| x as i64).collect();
        let attention_mask_i64: Vec<i64> = attention_mask.iter().map(|&x| x as i64).collect();
        let token_type_ids_i64: Vec<i64> = token_type_ids.iter().map(|&x| x as i64).collect();

        // Create ONNX tensors
        let input_ids_value = Value::from_array(([1, seq_len], input_ids_i64))
            .map_err(|e| anyhow!("Failed to create input_ids tensor: {:?}", e))?;

        let attention_mask_value = Value::from_array(([1, seq_len], attention_mask_i64))
            .map_err(|e| anyhow!("Failed to create attention_mask tensor: {:?}", e))?;

        // Run inference based on model type
        let mut session_guard = self.session.lock()
            .map_err(|e| anyhow!("Failed to lock session: {}", e))?;

        let outputs = match self.model_type {
            ModelType::MPNet => {
                // MPNet doesn't use token_type_ids
                let inputs = ort::inputs![
                    "input_ids" => input_ids_value,
                    "attention_mask" => attention_mask_value,
                ];
                session_guard.run(inputs)
                    .map_err(|e| anyhow!("Failed to run inference: {:?}", e))?
            }
            ModelType::Bert | ModelType::Sentence => {
                // BERT and Sentence models use token_type_ids
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

        // Extract embeddings based on model type
        let output_name = match self.model_type {
            ModelType::Sentence => "token_embeddings",
            ModelType::Bert | ModelType::MPNet => "last_hidden_state",
        };

        // Try to extract the output
        let (output_shape, embeddings_data) = outputs
            .get(output_name)
            .ok_or_else(|| {
                // List available outputs for debugging
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
            .try_extract_tensor::<f32>()
            .map_err(|e| anyhow!("Failed to extract embeddings from '{}': {:?}", output_name, e))?;

        // The output shape should be [batch_size, seq_len, hidden_dim]
        // Parse the actual dimension from output shape
        let actual_hidden_dim = if output_shape.len() == 3 {
            output_shape[2] as usize
        } else {
            return Err(anyhow!(
                "Unexpected output shape dimensions: {:?}. Expected [batch, seq_len, hidden_dim]",
                output_shape
            ));
        };

        // Update dimension if different from expected
        let working_dimension = if actual_hidden_dim != self.dimension {
            println!(
                "     Note: Model outputs {}d embeddings (expected {}d), using actual dimension",
                actual_hidden_dim, self.dimension
            );
            actual_hidden_dim
        } else {
            self.dimension
        };

        // batch_size = 1, so total elements = seq_len * hidden_dim
        let expected_elements = seq_len * working_dimension;

        if embeddings_data.len() != expected_elements {
            return Err(anyhow!(
                "Unexpected embedding shape. Expected {} elements ({}x{}), got {}. Output shape: {:?}",
                expected_elements,
                seq_len,
                working_dimension,
                embeddings_data.len(),
                output_shape
            ));
        }

        // Convert to ndarray for processing
        let embeddings = Array2::from_shape_vec((seq_len, working_dimension), embeddings_data.to_vec())
            .map_err(|e| anyhow!("Failed to reshape embeddings: {}", e))?;

        // Mean pooling with attention mask
        let attention_mask_f32: Vec<f32> = attention_mask.iter().map(|&x| x as f32).collect();
        let attention_mask_array = Array2::from_shape_vec((seq_len, 1), attention_mask_f32)
            .map_err(|e| anyhow!("Failed to create attention mask array: {}", e))?;

        // Broadcast attention mask to match embeddings shape
        let attention_expanded = attention_mask_array
            .broadcast((seq_len, working_dimension))
            .ok_or_else(|| anyhow!("Failed to broadcast attention mask"))?;

        // Apply attention mask and compute mean
        let masked_embeddings = &embeddings * &attention_expanded;
        let sum_embeddings = masked_embeddings.sum_axis(Axis(0));
        let sum_mask = attention_expanded.sum_axis(Axis(0));

        // Compute mean embedding
        let mut embedding: Vec<f32> = sum_embeddings
            .iter()
            .zip(sum_mask.iter())
            .map(|(sum, mask)| if *mask > 0.0 { sum / mask } else { 0.0 })
            .collect();

        // Note: We don't truncate anymore since we're using the actual dimension
        assert_eq!(embedding.len(), working_dimension, "Embedding size mismatch");

        // Normalize if requested
        if self.normalize {
            Self::normalize_vector(&mut embedding);
        }

        Ok(embedding)
    }

    fn normalize_vector(vec: &mut [f32]) {
        let magnitude: f32 = vec.iter().map(|x| x * x).sum::<f32>().sqrt();
        if magnitude > 1e-12 {
            vec.iter_mut().for_each(|x| *x /= magnitude);
        }
    }

    pub fn dimension(&self) -> usize {
        self.dimension
    }
}
