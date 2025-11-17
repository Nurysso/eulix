use anyhow::Result;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs::File;
use std::io::BufWriter;
use std::path::Path;

use crate::chunker::{Chunk, ChunkType};
use crate::kb_loader::KnowledgeBase;

/// Lightweight context index for LLM queries (no embeddings stored)
#[derive(Debug, Serialize, Deserialize)]
pub struct ContextIndex {
    pub metadata: ContextMetadata,
    pub chunks: Vec<ContextChunk>,
    pub relationships: Vec<Relationship>,
    pub tags: HashMap<String, Vec<String>>, 
    pub call_graph_summary: CallGraphSummary,
    pub entry_points: Vec<EntryPointInfo>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ContextMetadata {
    pub project_name: String,
    pub total_chunks: usize,
    pub chunk_types: HashMap<String, usize>,
    pub created_at: String,
    pub embedding_dimension: usize,
    pub languages: Vec<String>,
    pub architecture_style: Option<String>,
}

/// Lightweight chunk representation (no embedding vector)
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ContextChunk {
    pub id: String,
    pub chunk_type: ChunkType,
    pub content: String,
    pub metadata: ContextChunkMetadata,
    pub tags: Vec<String>,
    pub importance_score: f32,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ContextChunkMetadata {
    pub file_path: Option<String>,
    pub language: Option<String>,
    pub line_start: Option<usize>,
    pub line_end: Option<usize>,
    pub name: String,
    pub complexity: Option<usize>,
    pub is_entry_point: bool,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Relationship {
    pub from: String,
    pub to: String,
    pub rel_type: RelationType,
    pub conditional: bool,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(rename_all = "snake_case")]
pub enum RelationType {
    Calls,
    CalledBy,
    Imports,
    Inherits,
    Contains,
    Uses,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CallGraphSummary {
    pub total_nodes: usize,
    pub total_edges: usize,
    pub entry_points_count: usize,
    pub max_depth: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct EntryPointInfo {
    pub id: String,
    pub entry_type: String,
    pub function_name: String,
    pub file: String,
    pub path: Option<String>,
}

impl ContextIndex {
    pub fn from_kb_and_chunks(kb: &KnowledgeBase, chunks: Vec<Chunk>, embedding_dimension: usize) -> Self {
        let mut chunk_types = HashMap::new();
        let mut tags = HashMap::new();

        let context_chunks: Vec<ContextChunk> = chunks
            .into_iter()
            .map(|chunk| {
                // Count chunk types
                let type_name = format!("{:?}", chunk.chunk_type);
                *chunk_types.entry(type_name).or_insert(0) += 1;

                // Index tags
                for tag in &chunk.tags {
                    tags.entry(tag.clone())
                        .or_insert_with(Vec::new)
                        .push(chunk.id.clone());
                }

                // Check if this is an entry point
                let is_entry_point = kb.entry_points.iter().any(|ep| ep.function == chunk.id);

                ContextChunk {
                    id: chunk.id,
                    chunk_type: chunk.chunk_type,
                    content: chunk.content,
                    metadata: ContextChunkMetadata {
                        file_path: chunk.metadata.file_path,
                        language: chunk.metadata.language,
                        line_start: chunk.metadata.line_start,
                        line_end: chunk.metadata.line_end,
                        name: chunk.metadata.name,
                        complexity: chunk.metadata.complexity,
                        is_entry_point,
                    },
                    tags: chunk.tags,
                    importance_score: chunk.importance_score,
                }
            })
            .collect();

        let relationships = Self::extract_relationships(kb);
        let call_graph_summary = Self::create_call_graph_summary(kb);
        let entry_points = Self::extract_entry_points(kb);

        Self {
            metadata: ContextMetadata {
                project_name: kb.metadata.project_name.clone(),
                total_chunks: context_chunks.len(),
                chunk_types,
                created_at: chrono::Utc::now().to_rfc3339(),
                embedding_dimension,
                languages: kb.metadata.languages.clone(),
                architecture_style: kb.patterns.architecture_style.clone(),
            },
            chunks: context_chunks,
            relationships,
            tags,
            call_graph_summary,
            entry_points,
        }
    }

    fn extract_relationships(kb: &KnowledgeBase) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // Extract from call graph
        for edge in &kb.call_graph.edges {
            let rel_type = match edge.edge_type.as_str() {
                "calls" => RelationType::Calls,
                "inherits" => RelationType::Inherits,
                "uses" => RelationType::Uses,
                _ => RelationType::Uses,
            };

            relationships.push(Relationship {
                from: edge.from.clone(),
                to: edge.to.clone(),
                rel_type,
                conditional: edge.conditional,
            });
        }

        // Extract from dependency graph
        for edge in &kb.dependency_graph.edges {
            let rel_type = match edge.edge_type.as_str() {
                "imports" => RelationType::Imports,
                _ => RelationType::Uses,
            };

            relationships.push(Relationship {
                from: edge.from.clone(),
                to: edge.to.clone(),
                rel_type,
                conditional: false,
            });
        }

        relationships
    }

    fn create_call_graph_summary(kb: &KnowledgeBase) -> CallGraphSummary {
        CallGraphSummary {
            total_nodes: kb.call_graph.nodes.len(),
            total_edges: kb.call_graph.edges.len(),
            entry_points_count: kb.entry_points.len(),
            max_depth: Self::calculate_max_call_depth(kb),
        }
    }

    fn calculate_max_call_depth(kb: &KnowledgeBase) -> usize {
        // Simple BFS to find max depth from entry points
        use std::collections::{HashMap, VecDeque};

        let mut max_depth = 0;
        let adjacency: HashMap<String, Vec<String>> = kb.call_graph.edges
            .iter()
            .fold(HashMap::new(), |mut acc, edge| {
                acc.entry(edge.from.clone())
                    .or_insert_with(Vec::new)
                    .push(edge.to.clone());
                acc
            });

        for entry_point in &kb.entry_points {
            let mut queue = VecDeque::new();
            let mut visited = HashMap::new();

            queue.push_back((entry_point.function.clone(), 0));
            visited.insert(entry_point.function.clone(), 0);

            while let Some((node, depth)) = queue.pop_front() {
                max_depth = max_depth.max(depth);

                if let Some(neighbors) = adjacency.get(&node) {
                    for neighbor in neighbors {
                        if !visited.contains_key(neighbor) {
                            visited.insert(neighbor.clone(), depth + 1);
                            queue.push_back((neighbor.clone(), depth + 1));
                        }
                    }
                }
            }
        }

        max_depth
    }

    fn extract_entry_points(kb: &KnowledgeBase) -> Vec<EntryPointInfo> {
        kb.entry_points
            .iter()
            .map(|ep| EntryPointInfo {
                id: ep.function.clone(),
                entry_type: ep.entry_type.clone(),
                function_name: ep.handler.clone(),
                file: ep.file.clone(),
                path: ep.path.clone(),
            })
            .collect()
    }

    pub fn save(&self, path: &Path) -> Result<()> {
        let file = File::create(path)?;
        let writer = BufWriter::new(file);
        serde_json::to_writer_pretty(writer, self)?;
        Ok(())
    }

    pub fn load(path: &Path) -> Result<Self> {
        let file = File::open(path)?;
        let reader = std::io::BufReader::new(file);
        let index = serde_json::from_reader(reader)?;
        Ok(index)
    }

    /// Get chunks by tag (useful for filtering context)
    pub fn get_by_tag(&self, tag: &str) -> Vec<&ContextChunk> {
        if let Some(chunk_ids) = self.tags.get(tag) {
            chunk_ids
                .iter()
                .filter_map(|id| self.chunks.iter().find(|c| &c.id == id))
                .collect()
        } else {
            Vec::new()
        }
    }

    /// Get related chunks (via relationships)
    pub fn get_related(&self, chunk_id: &str) -> Vec<&ContextChunk> {
        let related_ids: Vec<String> = self.relationships
            .iter()
            .filter(|r| r.from == chunk_id)
            .map(|r| r.to.clone())
            .collect();

        self.chunks
            .iter()
            .filter(|c| related_ids.contains(&c.id))
            .collect()
    }

    /// Get chunks that reference this chunk (incoming relationships)
    pub fn get_referencing_chunks(&self, chunk_id: &str) -> Vec<&ContextChunk> {
        let referencing_ids: Vec<String> = self.relationships
            .iter()
            .filter(|r| r.to == chunk_id)
            .map(|r| r.from.clone())
            .collect();

        self.chunks
            .iter()
            .filter(|c| referencing_ids.contains(&c.id))
            .collect()
    }

    /// Get entry point chunks
    pub fn get_entry_points(&self) -> Vec<&ContextChunk> {
        self.chunks
            .iter()
            .filter(|c| c.metadata.is_entry_point)
            .collect()
    }

    /// Build context window for a query
    /// Returns the most relevant chunks up to max_tokens
    pub fn build_context_window(
        &self,
        relevant_chunk_ids: &[String],
        max_tokens: usize,
        include_related: bool,
    ) -> String {
        let mut context = String::new();
        let mut token_count = 0;
        const AVG_CHARS_PER_TOKEN: usize = 4;
        let mut added_ids = std::collections::HashSet::new();

        // Add relevant chunks
        for chunk_id in relevant_chunk_ids {
            if let Some(chunk) = self.chunks.iter().find(|c| &c.id == chunk_id) {
                if added_ids.contains(chunk_id) {
                    continue;
                }

                let chunk_text = self.format_chunk_for_context(chunk);
                let chunk_tokens = chunk_text.len() / AVG_CHARS_PER_TOKEN;

                if token_count + chunk_tokens > max_tokens {
                    break;
                }

                context.push_str(&chunk_text);
                context.push_str("\n\n---\n\n");
                token_count += chunk_tokens;
                added_ids.insert(chunk_id.clone());

                // Include related chunks if requested
                if include_related {
                    for related in self.get_related(chunk_id) {
                        if added_ids.contains(&related.id) {
                            continue;
                        }

                        let related_text = self.format_chunk_for_context(related);
                        let related_tokens = related_text.len() / AVG_CHARS_PER_TOKEN;

                        if token_count + related_tokens > max_tokens {
                            break;
                        }

                        context.push_str(&related_text);
                        context.push_str("\n\n---\n\n");
                        token_count += related_tokens;
                        added_ids.insert(related.id.clone());
                    }
                }
            }
        }

        context
    }

    fn format_chunk_for_context(&self, chunk: &ContextChunk) -> String {
        format!(
            "// File: {}\n// Type: {:?}\n// Name: {}\n{}\n\n{}",
            chunk.metadata.file_path.as_ref().unwrap_or(&"unknown".to_string()),
            chunk.chunk_type,
            chunk.metadata.name,
            if chunk.metadata.is_entry_point { "// [ENTRY POINT]" } else { "" },
            chunk.content
        )
    }

    /// Get statistics about the index
    pub fn stats(&self) -> IndexStats {
        IndexStats {
            total_chunks: self.chunks.len(),
            chunk_types: self.metadata.chunk_types.clone(),
            total_relationships: self.relationships.len(),
            entry_points: self.entry_points.len(),
            languages: self.metadata.languages.clone(),
            call_graph_depth: self.call_graph_summary.max_depth,
        }
    }
}

#[derive(Debug, Serialize, Deserialize)]
pub struct IndexStats {
    pub total_chunks: usize,
    pub chunk_types: HashMap<String, usize>,
    pub total_relationships: usize,
    pub entry_points: usize,
    pub languages: Vec<String>,
    pub call_graph_depth: usize,
}

/// Binary vector storage for efficient similarity search
#[derive(Debug)]
pub struct VectorStore {
    pub vectors: HashMap<String, Vec<f32>>,
}

impl Default for VectorStore {
    fn default() -> Self {
        Self::new()
    }
}

impl VectorStore {
    pub fn new() -> Self {
        Self {
            vectors: HashMap::new(),
        }
    }

    /// Get the number of vectors in the store
    pub fn len(&self) -> usize {
        self.vectors.len()
    }

    /// Check if the store is empty
    pub fn is_empty(&self) -> bool {
        self.vectors.is_empty()
    }

    /// Calculate the approximate size in megabytes
    pub fn size_mb(&self) -> f64 {
        let vector_count = self.vectors.len();
        if vector_count == 0 {
            return 0.0;
        }

        // Get dimension from first vector
        let dimension = self.vectors.values()
            .next()
            .map(|v| v.len())
            .unwrap_or(0);

        // Each f32 is 4 bytes
        let bytes_per_vector = dimension * 4;

        // ID strings (approximate average of 50 bytes per ID)
        let id_bytes: usize = self.vectors.keys()
            .map(|id| id.len())
            .sum();

        let total_bytes = (vector_count * bytes_per_vector) + id_bytes;
        total_bytes as f64 / (1024.0 * 1024.0)
    }

    /// Add a vector to the store
    pub fn add(&mut self, id: String, vector: Vec<f32>) {
        self.vectors.insert(id, vector);
    }

    /// Get a vector by ID
    pub fn get(&self, id: &str) -> Option<&Vec<f32>> {
        self.vectors.get(id)
    }

    /// Save to binary format
    pub fn save_binary(&self, path: &Path) -> Result<()> {
        use std::io::Write;

        let mut file = std::fs::File::create(path)?;

        // Write header: [version: u32, count: u64, dimension: u32]
        let version: u32 = 1;
        let count = self.vectors.len() as u64;
        let dimension = self.vectors.values()
            .next()
            .map(|v| v.len() as u32)
            .unwrap_or(0);

        file.write_all(&version.to_le_bytes())?;
        file.write_all(&count.to_le_bytes())?;
        file.write_all(&dimension.to_le_bytes())?;

        // Write each vector with its ID
        for (id, vector) in &self.vectors {
            // Write ID length and ID
            let id_bytes = id.as_bytes();
            file.write_all(&(id_bytes.len() as u32).to_le_bytes())?;
            file.write_all(id_bytes)?;

            // Write vector data
            for &value in vector {
                file.write_all(&value.to_le_bytes())?;
            }
        }

        Ok(())
    }

    /// Load from binary format
    pub fn load_binary(path: &Path) -> Result<Self> {
        use std::io::Read;

        let mut file = std::fs::File::open(path)?;
        let mut store = VectorStore::new();

        // Read header
        let mut version_bytes = [0u8; 4];
        let mut count_bytes = [0u8; 8];
        let mut dimension_bytes = [0u8; 4];

        file.read_exact(&mut version_bytes)?;
        file.read_exact(&mut count_bytes)?;
        file.read_exact(&mut dimension_bytes)?;

        let _version = u32::from_le_bytes(version_bytes);
        let count = u64::from_le_bytes(count_bytes);
        let dimension = u32::from_le_bytes(dimension_bytes) as usize;

        // Read vectors
        for _ in 0..count {
            // Read ID
            let mut id_len_bytes = [0u8; 4];
            file.read_exact(&mut id_len_bytes)?;
            let id_len = u32::from_le_bytes(id_len_bytes) as usize;

            let mut id_bytes = vec![0u8; id_len];
            file.read_exact(&mut id_bytes)?;
            let id = String::from_utf8(id_bytes)?;

            // Read vector
            let mut vector = Vec::with_capacity(dimension);
            for _ in 0..dimension {
                let mut value_bytes = [0u8; 4];
                file.read_exact(&mut value_bytes)?;
                vector.push(f32::from_le_bytes(value_bytes));
            }

            store.add(id, vector);
        }

        Ok(store)
    }

    /// Search for top-k similar vectors
    pub fn search(&self, query_vector: &[f32], top_k: usize) -> Vec<SearchResult> {
        let mut results: Vec<SearchResult> = self.vectors
            .iter()
            .map(|(id, vector)| {
                let similarity = cosine_similarity(query_vector, vector);
                SearchResult {
                    id: id.clone(),
                    similarity,
                }
            })
            .collect();

        results.sort_by(|a, b| b.similarity.partial_cmp(&a.similarity).unwrap());
        results.truncate(top_k);
        results
    }
}

#[derive(Debug, Clone)]
pub struct SearchResult {
    pub id: String,
    pub similarity: f32,
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
