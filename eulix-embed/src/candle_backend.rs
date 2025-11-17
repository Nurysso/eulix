#[cfg(feature = "candle-cpu")]
use anyhow::{anyhow, Result};

#[cfg(feature = "candle-cpu")]
use candle_core::{DType, Device, Tensor};

#[cfg(feature = "candle-cpu")]
use candle_nn::VarBuilder;

#[cfg(feature = "candle-cpu")]
use candle_transformers::models::bert::{BertModel, Config};

#[cfg(feature = "candle-cpu")]
use hf_hub::api::sync::Api;

#[cfg(feature = "candle-cpu")]
use tokenizers::Tokenizer;

use crate::embedder::EmbedderConfig;

#[derive(Debug, Clone, Copy)]
pub enum DeviceType {
    Cuda,
    Rocm,
    Cpu,
}

#[cfg(feature = "candle-cpu")]
pub struct CandleBackend {
    model: BertModel,
    tokenizer: Tokenizer,
    device: Device,
    dimension: usize,
    normalize: bool,
}

#[cfg(feature = "candle-cpu")]
impl CandleBackend {
    pub fn new(config: &EmbedderConfig, device_type: DeviceType) -> Result<Self> {
        println!("     Loading model (this may take a moment)...");

        let device = match device_type {
            DeviceType::Cuda => {
                #[cfg(feature = "cuda")]
                {
                    Device::new_cuda(0)?
                }
                #[cfg(not(feature = "cuda"))]
                {
                    return Err(anyhow!("CUDA support not compiled. Use --features cuda"));
                }
            }
            DeviceType::Rocm => {
                #[cfg(feature = "rocm")]
                {
                    Device::new_cuda(0)?
                }
                #[cfg(not(feature = "rocm"))]
                {
                    return Err(anyhow!("ROCm support not compiled. Use --features rocm"));
                }
            }
            DeviceType::Cpu => Device::Cpu,
        };

        println!("     Device initialized: {:?}", device);

        let (config_path, tokenizer_path, weights_path) = if let Some(ref local_path) = config.model_path {
            println!("     Using local model from: {:?}", local_path);
            (
                local_path.join("config.json"),
                local_path.join("tokenizer.json"),
                local_path.join("model.safetensors"),
            )
        } else {
            println!("     Downloading model files from HuggingFace Hub...");

            let api = Api::new()
                .map_err(|e| anyhow!("Failed to initialize HuggingFace API: {}. Try setting HF_HOME env variable or using --model-path for local models", e))?;

            let repo_api = api.model(config.model_name.clone());

            let config_path = repo_api.get("config.json")
                .map_err(|e| anyhow!("Failed to download config.json: {}. Check internet connection and model name", e))?;

            let tokenizer_path = repo_api.get("tokenizer.json")
                .map_err(|e| anyhow!("Failed to download tokenizer.json: {}", e))?;

            let weights_path = if let Ok(path) = repo_api.get("model.safetensors") {
                path
            } else if let Ok(path) = repo_api.get("pytorch_model.bin") {
                path
            } else {
                repo_api.get("rust_model.ot")
                    .map_err(|e| anyhow!("Failed to download model weights: {}", e))?
            };

            println!("     ✓ Model files downloaded to cache");
            (config_path, tokenizer_path, weights_path)
        };

        println!("     Loading model configuration...");
        let model_config = std::fs::read_to_string(&config_path)
            .map_err(|e| anyhow!("Failed to read config: {}", e))?;
        let model_config: Config = serde_json::from_str(&model_config)
            .map_err(|e| anyhow!("Failed to parse config: {}", e))?;

        println!("     Loading model weights...");
        let vb = if weights_path.to_str().unwrap().ends_with(".safetensors") {
            unsafe {
                VarBuilder::from_mmaped_safetensors(
                    &[weights_path],
                    DType::F32,
                    &device,
                ).map_err(|e| anyhow!("Failed to load safetensors: {}", e))?
            }
        } else {
            VarBuilder::from_pth(&weights_path, DType::F32, &device)
                .map_err(|e| anyhow!("Failed to load weights: {}", e))?
        };

        println!("     Building model...");
        let model = BertModel::load(vb, &model_config)
            .map_err(|e| anyhow!("Failed to build model: {}", e))?;

        println!("     Loading tokenizer...");
        let tokenizer = Tokenizer::from_file(tokenizer_path)
            .map_err(|e| anyhow!("Failed to load tokenizer: {}", e))?;

        println!("     ✓ Model loaded successfully!");

        Ok(Self {
            model,
            tokenizer,
            device,
            dimension: config.dimension,
            normalize: config.normalize,
        })
    }

    pub fn generate_embedding(&self, text: &str) -> Result<Vec<f32>> {
        // Truncate text if needed - BERT models typically have 512 token limit
        // Reserve 2 tokens for [CLS] and [SEP]
        const MAX_TOKENS: usize = 510;

        let encoding = self
            .tokenizer
            .encode(text, true)
            .map_err(|e| anyhow!("Tokenization failed: {}", e))?;

        let mut tokens = encoding.get_ids().to_vec();
        let mut token_type_ids = encoding.get_type_ids().to_vec();
        let mut attention_mask = encoding.get_attention_mask().to_vec();

        // Truncate if exceeds max length
        if tokens.len() > MAX_TOKENS {
            tokens.truncate(MAX_TOKENS);
            token_type_ids.truncate(MAX_TOKENS);
            attention_mask.truncate(MAX_TOKENS);
        }

        let token_ids = Tensor::new(&tokens[..], &self.device)?.unsqueeze(0)?;
        let token_type_ids = Tensor::new(&token_type_ids[..], &self.device)?.unsqueeze(0)?;
        let attention_mask_tensor =
            Tensor::new(&attention_mask[..], &self.device)?.unsqueeze(0)?;

        // Forward pass with attention mask
        let embeddings = self
            .model
            .forward(&token_ids, &token_type_ids, Some(&attention_mask_tensor))?;

        let attention_mask_tensor = attention_mask_tensor.to_dtype(DType::F32)?;
        let attention_mask_expanded =
            attention_mask_tensor.unsqueeze(2)?.broadcast_as(embeddings.shape())?;

        let masked_embeddings = (embeddings * attention_mask_expanded)?;
        let sum_embeddings = masked_embeddings.sum(1)?;
        let sum_mask = attention_mask_tensor.sum(1)?;
        let mean_embeddings = sum_embeddings.broadcast_div(&sum_mask)?;

        let mut embedding = mean_embeddings.squeeze(0)?.to_vec1::<f32>()?;

        if embedding.len() > self.dimension {
            embedding.truncate(self.dimension);
        }

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

#[cfg(not(feature = "candle-cpu"))]
pub struct CandleBackend;

#[cfg(not(feature = "candle-cpu"))]
impl CandleBackend {
    pub fn new(_config: &EmbedderConfig, _device_type: DeviceType) -> Result<Self> {
        Err(anyhow::anyhow!(
            "Candle backend not compiled. Build with --features candle-cpu"
        ))
    }

    pub fn generate_embedding(&self, _text: &str) -> Result<Vec<f32>> {
        Err(anyhow::anyhow!("Candle backend not available"))
    }

    pub fn dimension(&self) -> usize {
        0
    }
}
