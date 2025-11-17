// eulix_embed/src/main.rs
use anyhow::{Context, Result};
use std::path::Path;

// Module declarations
mod candle_backend;
mod chunker;
mod context;
mod embedder;
mod index;
mod kb_loader;

// Import types we need
use chunker::{chunk_knowledge_base, Chunk, ChunkMetadata, ChunkType};
use context::{ContextIndex, VectorStore};
use embedder::EmbeddingGenerator;
use index::{EmbeddingEntry, EmbeddingIndex};
use kb_loader::load_knowledge_base;

pub struct EmbeddingPipeline {
    generator: EmbeddingGenerator,
    max_chunk_size: usize,
}

impl EmbeddingPipeline {
    pub fn new(model_name: &str) -> Result<Self> {
        let generator = EmbeddingGenerator::new(model_name)?;
        Ok(Self {
            generator,
            max_chunk_size: 2000,
        })
    }

    pub fn with_max_chunk_size(mut self, size: usize) -> Self {
        self.max_chunk_size = size;
        self
    }

    pub fn process(
        &self,
        kb_path: &Path,
        output_dir: &Path,
    ) -> Result<EmbeddingPipelineOutput> {
        println!("\n🚀 Starting Embedding Pipeline");
        println!("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n");

        println!("📖 Step 1: Loading knowledge base...");
        let kb = load_knowledge_base(kb_path)
            .context("Failed to load knowledge base")?;

        // Calculate total items from the new structure
        let total_functions: usize = kb.structure.values()
            .map(|f| f.functions.len())
            .sum();
        let total_classes: usize = kb.structure.values()
            .map(|f| f.classes.len())
            .sum();
        let total_methods: usize = kb.structure.values()
            .flat_map(|f| &f.classes)
            .map(|c| c.methods.len())
            .sum();

        println!("   ✓ Loaded knowledge base:");
        println!("     - {} files", kb.structure.len());
        println!("     - {} functions", total_functions);
        println!("     - {} classes", total_classes);
        println!("     - {} methods", total_methods);
        println!("     - {} entry points", kb.entry_points.len());

        println!("\n✂️  Step 2: Processing chunks...");
        let chunks = chunk_knowledge_base(&kb, self.max_chunk_size);
        println!("   ✓ Processed {} chunks", chunks.len());

        // Show chunk type breakdown
        let mut chunk_type_counts = std::collections::HashMap::new();
        for chunk in &chunks {
            *chunk_type_counts.entry(format!("{:?}", chunk.chunk_type)).or_insert(0) += 1;
        }
        println!("   ✓ Chunk breakdown:");
        for (chunk_type, count) in &chunk_type_counts {
            println!("     - {}: {}", chunk_type, count);
        }

        println!("\n🧮 Step 3: Generating embeddings...");
        let vector_store = self.generator.generate_vectors(chunks.clone())?;
        println!("   ✓ Generated {} embeddings", vector_store.len());
        println!("   ✓ Vector store size: {:.2} MB", vector_store.size_mb());

        println!("\n📊 Step 4: Building embedding index...");
        let mut embedding_index = EmbeddingIndex::new(
            self.generator.model_name().to_string(),
            self.generator.dimension(),
        );

        for chunk in chunks.clone() {
            if let Some(embedding) = vector_store.get(&chunk.id) {
                embedding_index.add_entry(EmbeddingEntry {
                    id: chunk.id.clone(),
                    chunk_type: chunk.chunk_type.clone(),
                    content: chunk.content.clone(),
                    embedding: embedding.clone(),
                    metadata: chunk.metadata.clone(),
                });
            }
        }
        println!("   ✓ Built index with {} entries", embedding_index.total_chunks);

        println!("\n📝 Step 5: Creating context index...");
        let context_index = ContextIndex::from_kb_and_chunks(&kb, chunks, self.generator.dimension());
        println!("   ✓ Context index created");
        println!("   ✓ Total tags: {}", context_index.tags.len());
        println!("   ✓ Total relationships: {}", context_index.relationships.len());

        println!("\n💾 Step 6: Saving outputs...");
        std::fs::create_dir_all(output_dir)?;

        let embeddings_json = output_dir.join("embeddings.json");
        embedding_index.save(&embeddings_json)?;
        println!("   ✓ Saved embeddings.json");

        let embeddings_bin = output_dir.join("embeddings.bin");
        embedding_index.save_binary(&embeddings_bin)?;
        println!("   ✓ Saved embeddings.bin");

        let vectors_bin = output_dir.join("vectors.bin");
        vector_store.save_binary(&vectors_bin)?;
        println!("   ✓ Saved vectors.bin");

        let context_json = output_dir.join("context.json");
        context_index.save(&context_json)?;
        println!("   ✓ Saved context.json");

        println!("\n📈 Pipeline Statistics:");
        println!("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━");
        let stats = embedding_index.stats();
        println!("   Model: {}", stats.model);
        println!("   Dimension: {}", stats.dimension);
        println!("   Total chunks: {}", stats.total_chunks);
        println!("\n   Chunk types:");
        for (chunk_type, count) in &stats.chunk_types {
            println!("      {}: {}", chunk_type, count);
        }
        println!("\n   Languages:");
        for (lang, count) in &stats.languages {
            println!("      {}: {}", lang, count);
        }

        // Context index stats
        let context_stats = context_index.stats();
        println!("\n   Context Index:");
        println!("      Relationships: {}", context_stats.total_relationships);
        println!("      Entry points: {}", context_stats.entry_points);
        println!("      Call graph depth: {}", context_stats.call_graph_depth);

        println!("\n✅ Pipeline completed successfully!\n");

        Ok(EmbeddingPipelineOutput {
            embedding_index,
            vector_store,
            context_index,
        })
    }
}

pub struct EmbeddingPipelineOutput {
    pub embedding_index: EmbeddingIndex,
    pub vector_store: VectorStore,
    pub context_index: ContextIndex,
}

pub struct QueryEngine {
    embedding_index: EmbeddingIndex,
    context_index: ContextIndex,
    generator: EmbeddingGenerator,
}

impl QueryEngine {
    pub fn load(output_dir: &Path, model_name: &str) -> Result<Self> {
        println!("🔍 Loading query engine...");

        let embedding_index = EmbeddingIndex::load(&output_dir.join("embeddings.json"))?;
        let context_index = ContextIndex::load(&output_dir.join("context.json"))?;
        let generator = EmbeddingGenerator::new(model_name)?;

        println!("   ✓ Query engine ready!");

        Ok(Self {
            embedding_index,
            context_index,
            generator,
        })
    }

    pub fn query(&self, query: &str, top_k: usize) -> Result<QueryResult> {
        // Create a temporary chunk for the query
        let query_chunk = Chunk {
            id: "query".to_string(),
            chunk_type: ChunkType::Other,
            content: query.to_string(),
            metadata: ChunkMetadata {
                file_path: None,
                language: None,
                line_start: None,
                line_end: None,
                name: "query".to_string(),
                complexity: None,
            },
            tags: vec![],
            importance_score: 0.0,
        };

        let query_embedding = self.generator.generate_vectors(vec![query_chunk])?;

        let query_vec = query_embedding.get("query")
            .context("Failed to get query embedding")?;

        let search_results = self.embedding_index.search(query_vec, top_k);

        let chunk_ids: Vec<String> = search_results
            .iter()
            .map(|r| r.id.clone())
            .collect();

        let context = self.context_index.build_context_window(
            &chunk_ids,
            4000,
            true,
        );

        Ok(QueryResult {
            query: query.to_string(),
            results: search_results,
            context,
        })
    }
}

pub struct QueryResult {
    pub query: String,
    pub results: Vec<index::SearchResult>,
    pub context: String,
}

fn print_help() {
    println!("Eulix Embed - Knowledge Base Embedding Generator\n");
    println!("USAGE:");
    println!("    eulix_embed [OPTIONS]\n");
    println!("OPTIONS:");
    println!("    -k, --kb-path <PATH>     Path to knowledge base JSON file");
    println!("                             (default: knowledge_base.json)");
    println!();
    println!("    -o, --output <DIR>       Output directory for embeddings");
    println!("                             (default: ./embeddings)");
    println!();
    println!("    -m, --model <NAME>       HuggingFace model name for embeddings");
    println!("                             (default: sentence-transformers/all-MiniLM-L6-v2)");
    println!();
    println!("    -h, --help               Show this help message\n");
    println!("EXAMPLES:");
    println!("    # Use default settings");
    println!("    eulix_embed\n");
    println!("    # Specify custom knowledge base");
    println!("    eulix_embed --kb-path my_kb.json\n");
    println!("    # Use different output directory");
    println!("    eulix_embed -k my_kb.json -o ./my_embeddings\n");
    println!("    # Use a different embedding model");
    println!("    eulix_embed -m BAAI/bge-small-en-v1.5\n");
    println!("SUPPORTED MODELS:");
    println!("    - sentence-transformers/all-MiniLM-L6-v2 (384d, fast)");
    println!("    - BAAI/bge-small-en-v1.5 (384d, good quality)");
    println!("    - BAAI/bge-base-en-v1.5 (768d, better quality)");
    println!("    - sentence-transformers/all-mpnet-base-v2 (768d, high quality)");
}

fn main() -> Result<()> {
    let args: Vec<String> = std::env::args().collect();

    // Show help if no arguments or --help flag
    if args.len() == 1 || args.contains(&"--help".to_string()) || args.contains(&"-h".to_string()) {
        print_help();
        std::process::exit(0);
    }

    let mut kb_path = "knowledge_base.json".to_string();
    let mut output_dir = "./embeddings".to_string();
    let mut model = "sentence-transformers/all-MiniLM-L6-v2".to_string();

    // Parse arguments
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--kb-path" | "-k" => {
                if i + 1 < args.len() {
                    kb_path = args[i + 1].clone();
                    i += 2;
                } else {
                    eprintln!("Error: {} requires a value\n", args[i]);
                    print_help();
                    std::process::exit(1);
                }
            }
            "--output" | "-o" => {
                if i + 1 < args.len() {
                    output_dir = args[i + 1].clone();
                    i += 2;
                } else {
                    eprintln!("Error: {} requires a value\n", args[i]);
                    print_help();
                    std::process::exit(1);
                }
            }
            "--model" | "-m" => {
                if i + 1 < args.len() {
                    model = args[i + 1].clone();
                    i += 2;
                } else {
                    eprintln!("Error: {} requires a value\n", args[i]);
                    print_help();
                    std::process::exit(1);
                }
            }
            "--help" | "-h" => {
                print_help();
                std::process::exit(0);
            }
            _ => {
                eprintln!("Error: Unknown argument '{}'\n", args[i]);
                print_help();
                std::process::exit(1);
            }
        }
    }

    println!("╔════════════════════════════════════════╗");
    println!("║  Eulix Embed - Embedding Generator    ║");
    println!("╚════════════════════════════════════════╝\n");

    println!("Configuration:");
    println!("  KB Path:    {}", kb_path);

    // Show absolute path for debugging
    let abs_path = std::fs::canonicalize(&kb_path)
        .unwrap_or_else(|_| Path::new(&kb_path).to_path_buf());
    println!("  Absolute:   {:?}", abs_path);

    println!("  Output Dir: {}", output_dir);
    println!("  Model:      {}\n", model);

    // Check if KB file exists
    if !Path::new(&kb_path).exists() {
        eprintln!("❌ Error: Knowledge base file not found: {}", kb_path);
        eprintln!("   Current directory: {:?}", std::env::current_dir().unwrap());
        eprintln!("\n💡 Tip: Create a knowledge base file or specify the correct path using --kb-path");
        std::process::exit(1);
    }

    let pipeline = EmbeddingPipeline::new(&model)?;
    pipeline.process(Path::new(&kb_path), Path::new(&output_dir))?;

    Ok(())
}
