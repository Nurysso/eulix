use anyhow::Result;
use serde::{Deserialize, Serialize};
use std::fs::File;
use std::io::BufWriter;
use std::path::Path;

use crate::chunker::{ChunkMetadata, ChunkType};

/// Combined embedding index with both vectors and searchable metadata
#[derive(Debug, Serialize, Deserialize)]
pub struct EmbeddingIndex {
    pub model: String,
    pub dimension: usize,
    pub total_chunks: usize,
    pub embeddings: Vec<EmbeddingEntry>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct EmbeddingEntry {
    pub id: String,
    pub chunk_type: ChunkType,
    pub content: String,
    pub embedding: Vec<f32>,
    pub metadata: ChunkMetadata,
}

impl EmbeddingIndex {
    /// Create a new empty index
    pub fn new(model: String, dimension: usize) -> Self {
        Self {
            model,
            dimension,
            total_chunks: 0,
            embeddings: Vec::new(),
        }
    }

    /// Add an embedding entry

pub fn add_entry(&mut self, entry: EmbeddingEntry) -> Result<()> {
    // Validate and auto-correct dimension
    let entry_dim = entry.embedding.len();

    if self.embeddings.is_empty() {
        // First entry - update dimension if different from config
        if entry_dim != self.dimension {
            println!("      Auto-correcting dimension from {} to {} based on actual embeddings",
                     self.dimension, entry_dim);
            self.dimension = entry_dim;
        }
    } else {
        // Subsequent entries - validate dimension matches
        if entry_dim != self.dimension {
            return Err(anyhow::anyhow!(
                "Embedding dimension mismatch: expected {}, got {}. Entry ID: {}",
                self.dimension,
                entry_dim,
                entry.id
            ));
        }
    }

    self.embeddings.push(entry);
    self.total_chunks += 1;
    Ok(())
}

    /// Save to JSON file
    pub fn save(&self, path: &Path) -> Result<()> {
        let file = File::create(path)?;
        let writer = BufWriter::new(file);
        serde_json::to_writer_pretty(writer, self)?;
        Ok(())
    }

    /// Load from JSON file
    pub fn load(path: &Path) -> Result<Self> {
        let file = File::open(path)?;
        let reader = std::io::BufReader::new(file);
        let index = serde_json::from_reader(reader)?;
        Ok(index)
    }
/// Save embeddings to binary format
pub fn save_binary(&self, path: &Path) -> Result<()> {
    use std::io::Write;

    let mut file = File::create(path)?;

    // Write magic bytes "EULX"
    file.write_all(b"EULX")?;

    // Write version 2 (includes model name)
    let version: u32 = 2;
    file.write_all(&version.to_le_bytes())?;

    // Write model name length and model name
    let model_bytes = self.model.as_bytes();
    file.write_all(&(model_bytes.len() as u32).to_le_bytes())?;
    file.write_all(model_bytes)?;

    // Write count
    file.write_all(&(self.embeddings.len() as u32).to_le_bytes())?;

    // Get actual dimension from first embedding
    let actual_dimension = if let Some(first) = self.embeddings.first() {
        first.embedding.len()
    } else {
        self.dimension
    };

    // Validate all embeddings have the same dimension
    for (i, entry) in self.embeddings.iter().enumerate() {
        if entry.embedding.len() != actual_dimension {
            return Err(anyhow::anyhow!(
                "Embedding {} has dimension {} but expected {}. All embeddings must have the same dimension.",
                i, entry.embedding.len(), actual_dimension
            ));
        }
    }

    // Write actual dimension
    file.write_all(&(actual_dimension as u32).to_le_bytes())?;

    // Write embeddings only (no IDs, no metadata - just vectors)
    for entry in &self.embeddings {
        for &value in &entry.embedding {
            file.write_all(&value.to_le_bytes())?;
        }
    }

    Ok(())
}

pub fn load_binary(path: &Path) -> Result<Self> {
    use std::io::Read;

    let mut file = File::open(path)?;

    // Read and validate magic bytes
    let mut magic = [0u8; 4];
    file.read_exact(&mut magic)?;
    if &magic != b"EULX" {
        return Err(anyhow::anyhow!("Invalid magic bytes: expected EULX"));
    }

    // Read version
    let mut version_bytes = [0u8; 4];
    file.read_exact(&mut version_bytes)?;
    let version = u32::from_le_bytes(version_bytes);

    let model = match version {
        2 => {
            //  Read model name
            let mut model_len_bytes = [0u8; 4];
            file.read_exact(&mut model_len_bytes)?;
            let model_len = u32::from_le_bytes(model_len_bytes) as usize;

            let mut model_bytes = vec![0u8; model_len];
            file.read_exact(&mut model_bytes)?;
            String::from_utf8(model_bytes)
                .map_err(|e| anyhow::anyhow!("Invalid UTF-8 in model name: {}", e))?
        }
        1 => {
            // No model name stored, use placeholder
            "unknown-model (v2 format)".to_string()
        }
        _ => {
            return Err(anyhow::anyhow!("Unsupported binary version: {}. Expected 2 or 3", version));
        }
    };

    // Read count
    let mut count_bytes = [0u8; 4];
    file.read_exact(&mut count_bytes)?;
    let count = u32::from_le_bytes(count_bytes) as usize;

    // Read dimension
    let mut dimension_bytes = [0u8; 4];
    file.read_exact(&mut dimension_bytes)?;
    let dimension = u32::from_le_bytes(dimension_bytes) as usize;

    // Read embeddings
    let mut embeddings = Vec::with_capacity(count);
    for i in 0..count {
        let mut embedding = Vec::with_capacity(dimension);
        for _ in 0..dimension {
            let mut value_bytes = [0u8; 4];
            file.read_exact(&mut value_bytes)?;
            embedding.push(f32::from_le_bytes(value_bytes));
        }

        embeddings.push(EmbeddingEntry {
            id: format!("embedding_{}", i), // Placeholder ID
            chunk_type: ChunkType::Other,
            content: String::new(),
            embedding,
            metadata: ChunkMetadata {
                file_path: None,
                language: None,
                line_start: None,
                line_end: None,
                name: String::new(),
                complexity: None,
            },
        });
    }

    Ok(Self {
        model,
        dimension,
        total_chunks: embeddings.len(),
        embeddings,
    })
}

    /// Find the top-k most similar chunks to a query embedding
    pub fn search(&self, query_embedding: &[f32], top_k: usize) -> Vec<SearchResult> {
        let mut results: Vec<SearchResult> = self.embeddings
            .iter()
            .map(|entry| {
                let similarity = cosine_similarity(query_embedding, &entry.embedding);
                SearchResult {
                    id: entry.id.clone(),
                    chunk_type: entry.chunk_type.clone(),
                    content: entry.content.clone(),
                    metadata: entry.metadata.clone(),
                    similarity,
                }
            })
            .collect();

        results.sort_by(|a, b| b.similarity.partial_cmp(&a.similarity).unwrap());
        results.truncate(top_k);
        results
    }

    /// Search with filters
    pub fn search_filtered(
        &self,
        query_embedding: &[f32],
        top_k: usize,
        filters: SearchFilters,
    ) -> Vec<SearchResult> {
        let mut results: Vec<SearchResult> = self.embeddings
            .iter()
            .filter(|entry| {
                // Apply chunk type filter
                if let Some(ref types) = filters.chunk_types {
                    if !types.contains(&entry.chunk_type) {
                        return false;
                    }
                }

                // Apply language filter
                if let Some(ref langs) = filters.languages {
                    if let Some(ref lang) = entry.metadata.language {
                        if !langs.contains(lang) {
                            return false;
                        }
                    } else {
                        return false;
                    }
                }

                // Apply file path filter
                if let Some(ref paths) = filters.file_paths {
                    if let Some(ref path) = entry.metadata.file_path {
                        if !paths.iter().any(|p| path.contains(p)) {
                            return false;
                        }
                    } else {
                        return false;
                    }
                }

                true
            })
            .map(|entry| {
                let similarity = cosine_similarity(query_embedding, &entry.embedding);
                SearchResult {
                    id: entry.id.clone(),
                    chunk_type: entry.chunk_type.clone(),
                    content: entry.content.clone(),
                    metadata: entry.metadata.clone(),
                    similarity,
                }
            })
            .collect();

        results.sort_by(|a, b| b.similarity.partial_cmp(&a.similarity).unwrap());
        results.truncate(top_k);
        results
    }

    /// Get statistics about the index
    pub fn stats(&self) -> IndexStats {
        let mut chunk_type_counts = std::collections::HashMap::new();
        let mut language_counts = std::collections::HashMap::new();

        for entry in &self.embeddings {
            *chunk_type_counts.entry(format!("{:?}", entry.chunk_type)).or_insert(0) += 1;
            if let Some(ref lang) = entry.metadata.language {
                *language_counts.entry(lang.clone()).or_insert(0) += 1;
            }
        }

        IndexStats {
            total_chunks: self.total_chunks,
            dimension: self.dimension,
            model: self.model.clone(),
            chunk_types: chunk_type_counts,
            languages: language_counts,
        }
    }
}

#[derive(Debug, Clone)]
pub struct SearchResult {
    pub id: String,
    pub chunk_type: ChunkType,
    pub content: String,
    pub metadata: ChunkMetadata,
    pub similarity: f32,
}

#[derive(Debug, Default)]
pub struct SearchFilters {
    pub chunk_types: Option<Vec<ChunkType>>,
    pub languages: Option<Vec<String>>,
    pub file_paths: Option<Vec<String>>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct IndexStats {
    pub total_chunks: usize,
    pub dimension: usize,
    pub model: String,
    pub chunk_types: std::collections::HashMap<String, usize>,
    pub languages: std::collections::HashMap<String, usize>,
}

fn cosine_similarity(a: &[f32], b: &[f32]) -> f32 {
    let dot_product: f32 = a.iter().zip(b.iter()).map(|(x, y)| x * y).sum();
    let magnitude_a: f32 = a.iter().map(|x| x * x).sum::<f32>().sqrt();
    let magnitude_b: f32 = b.iter().map(|x| x * x).sum::<f32>().sqrt();

    if magnitude_a == 0.0 || magnitude_b == 0.0 {
        0.0
    } else {
        dot_product / (magnitude_a * magnitude_b)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_cosine_similarity() {
        let a = vec![1.0, 0.0, 0.0];
        let b = vec![1.0, 0.0, 0.0];
        assert!((cosine_similarity(&a, &b) - 1.0).abs() < 1e-6);

        let c = vec![1.0, 0.0, 0.0];
        let d = vec![0.0, 1.0, 0.0];
        assert!(cosine_similarity(&c, &d).abs() < 1e-6);
    }

    #[test]
    fn test_index_creation() {
        let index = EmbeddingIndex::new("test-model".to_string(), 384);
        assert_eq!(index.total_chunks, 0);
        assert_eq!(index.dimension, 384);
    }
}
