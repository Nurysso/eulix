use anyhow::{Context, Result};
use std::path::Path;
use std::time::Instant;

// Module declarations
mod onnx_backend;
mod chunker;
mod context;
mod embedder;
mod index;
mod kb_loader;

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
        let total_start = Instant::now();

        println!("\n{}", "=".repeat(70));
        println!("  EULIX EMBED - EMBEDDING PIPELINE");
        println!("{}\n", "=".repeat(70));

        // Step 1: Load KB
        println!("STEP 1: Loading Knowledge Base");
        println!("{}", "-".repeat(70));
        let step_start = Instant::now();

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

        println!("  [OK] Knowledge base loaded successfully");
        println!("       Files:        {}", kb.structure.len());
        println!("       Functions:    {}", total_functions);
        println!("       Classes:      {}", total_classes);
        println!("       Methods:      {}", total_methods);
        println!("       Entry Points: {}", kb.entry_points.len());
        println!("       Time:         {:.2}s", step_start.elapsed().as_secs_f64());
        println!();

        // Step 2: Chunk processing
        println!("STEP 2: Processing Code Chunks");
        println!("{}", "-".repeat(70));
        let step_start = Instant::now();

        let chunks = chunk_knowledge_base(&kb, self.max_chunk_size);

        // Show chunk type breakdown
        let mut chunk_type_counts = std::collections::HashMap::new();
        for chunk in &chunks {
            *chunk_type_counts.entry(format!("{:?}", chunk.chunk_type)).or_insert(0) += 1;
        }

        println!("  [OK] Chunking completed");
        println!("       Total Chunks: {}", chunks.len());
        println!("       Max Size:     {} chars", self.max_chunk_size);
        println!();
        println!("       Chunk Breakdown:");
        for (chunk_type, count) in &chunk_type_counts {
            println!("         {:20} {}", format!("{}:", chunk_type), count);
        }
        println!("       Time:         {:.2}s", step_start.elapsed().as_secs_f64());
        println!();

        // Step 3: Generate embeddings
        println!("STEP 3: Generating Embeddings");
        println!("{}", "-".repeat(70));
        let step_start = Instant::now();

        let vector_store = self.generator.generate_vectors(chunks.clone())?;

        println!("  [OK] Embeddings generated");
        println!("       Total Vectors:  {}", vector_store.len());
        println!("       Vector Size:    {:.2} MB", vector_store.size_mb());
        println!("       Model:          {}", self.generator.model_name());
        println!("       Dimension:      {}", self.generator.dimension());
        println!("       Time:           {:.2}s", step_start.elapsed().as_secs_f64());
        println!();

        // Step 4: Build index
        println!("STEP 4: Building Embedding Index");
        println!("{}", "-".repeat(70));
        let step_start = Instant::now();

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

        println!("  [OK] Index built successfully");
        println!("       Total Entries:  {}", embedding_index.total_chunks);
        println!("       Time:           {:.2}s", step_start.elapsed().as_secs_f64());
        println!();

        // Step 5: Create context index
        println!("STEP 5: Creating Context Index");
        println!("{}", "-".repeat(70));
        let step_start = Instant::now();

        let context_index = ContextIndex::from_kb_and_chunks(&kb, chunks, self.generator.dimension());

        println!("  [OK] Context index created");
        println!("       Tags:           {}", context_index.tags.len());
        println!("       Relationships:  {}", context_index.relationships.len());
        println!("       Time:           {:.2}s", step_start.elapsed().as_secs_f64());
        println!();

        // Step 6: Save outputs
        println!("STEP 6: Writing Output Files");
        println!("{}", "-".repeat(70));
        let step_start = Instant::now();

        std::fs::create_dir_all(output_dir)?;

        let embeddings_json = output_dir.join("embeddings.json");
        embedding_index.save(&embeddings_json)?;
        let json_size = std::fs::metadata(&embeddings_json)?.len();
        println!("  [OK] embeddings.json ({:.2} MB)", json_size as f64 / 1_048_576.0);

        let embeddings_bin = output_dir.join("embeddings.bin");
        embedding_index.save_binary(&embeddings_bin)?;
        let bin_size = std::fs::metadata(&embeddings_bin)?.len();
        println!("  [OK] embeddings.bin  ({:.2} MB)", bin_size as f64 / 1_048_576.0);

        let vectors_bin = output_dir.join("vectors.bin");
        vector_store.save_binary(&vectors_bin)?;
        let vec_size = std::fs::metadata(&vectors_bin)?.len();
        println!("  [OK] vectors.bin     ({:.2} MB)", vec_size as f64 / 1_048_576.0);

        let context_json = output_dir.join("context.json");
        context_index.save(&context_json)?;
        let ctx_size = std::fs::metadata(&context_json)?.len();
        println!("  [OK] context.json    ({:.2} MB)", ctx_size as f64 / 1_048_576.0);

        println!();
        println!("       Total Size:     {:.2} MB",
            (json_size + bin_size + vec_size + ctx_size) as f64 / 1_048_576.0);
        println!("       Time:           {:.2}s", step_start.elapsed().as_secs_f64());
        println!();

        // Final summary
        print_pipeline_summary(&embedding_index, &context_index, total_start.elapsed().as_secs_f64());

        Ok(EmbeddingPipelineOutput {
            embedding_index,
            vector_store,
            context_index,
        })
    }
}

fn print_pipeline_summary(
    embedding_index: &EmbeddingIndex,
    context_index: &ContextIndex,
    total_time: f64,
) {
    println!("{}", "=".repeat(70));
    println!("  PIPELINE SUMMARY");
    println!("{}", "=".repeat(70));
    println!();

    let stats = embedding_index.stats();

    println!("EMBEDDING STATISTICS");
    println!("{}", "-".repeat(70));
    println!("  Model:              {}", stats.model);
    println!("  Dimension:          {}", stats.dimension);
    println!("  Total Chunks:       {}", stats.total_chunks);
    println!();

    if !stats.chunk_types.is_empty() {
        println!("  Chunk Type Distribution:");
        let mut sorted_types: Vec<_> = stats.chunk_types.iter().collect();
        sorted_types.sort_by_key(|(_, count)| std::cmp::Reverse(*count));
        for (chunk_type, count) in sorted_types {
            let percentage = (*count as f64 / stats.total_chunks as f64) * 100.0;
            println!("    {:20} {:6} ({:5.1}%)",
                format!("{}:", chunk_type), count, percentage);
        }
        println!();
    }

    if !stats.languages.is_empty() {
        println!("  Language Distribution:");
        let mut sorted_langs: Vec<_> = stats.languages.iter().collect();
        sorted_langs.sort_by_key(|(_, count)| std::cmp::Reverse(*count));
        for (lang, count) in sorted_langs {
            let percentage = (*count as f64 / stats.total_chunks as f64) * 100.0;
            println!("    {:20} {:6} ({:5.1}%)",
                format!("{}:", lang), count, percentage);
        }
        println!();
    }

    let context_stats = context_index.stats();
    println!("CONTEXT INDEX STATISTICS");
    println!("{}", "-".repeat(70));
    println!("  Relationships:      {}", context_stats.total_relationships);
    println!("  Entry Points:       {}", context_stats.entry_points);
    println!("  Call Graph Depth:   {}", context_stats.call_graph_depth);
    println!();

    println!("EXECUTION TIME");
    println!("{}", "-".repeat(70));
    println!("  Total Time:         {:.2}s", total_time);
    println!();

    println!("{}", "=".repeat(70));
    println!("  PIPELINE COMPLETED SUCCESSFULLY");
    println!("{}", "=".repeat(70));
    println!();
}

pub struct EmbeddingPipelineOutput {
    pub embedding_index: EmbeddingIndex,
    pub vector_store: VectorStore,
    pub context_index: ContextIndex,
}

// Query embedding functionality
pub struct QueryEmbedder {
    generator: EmbeddingGenerator,
}

impl QueryEmbedder {
    pub fn new(model_name: &str) -> Result<Self> {
        let generator = EmbeddingGenerator::new(model_name)?;
        Ok(Self { generator })
    }

    /// Generate embedding for a query string
    /// Returns a vector of f32 values
    pub fn embed_query(&self, query: &str) -> Result<Vec<f32>> {
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

        let vector_store = self.generator.generate_vectors(vec![query_chunk])?;

        let embedding = vector_store.get("query")
            .context("Failed to get query embedding")?
            .clone();

        Ok(embedding)
    }

    pub fn dimension(&self) -> usize {
        self.generator.dimension()
    }

    pub fn model_name(&self) -> &str {
        self.generator.model_name()
    }
}

fn print_help() {
    println!("Eulix Embed - Knowledge Base Embedding Generator\n");
    println!("USAGE:");
    println!("    eulix_embed [COMMAND] [OPTIONS]\n");
    println!("COMMANDS:");
    println!("    embed              Generate embeddings for knowledge base (default)");
    println!("    query              Generate embedding for a query string\n");
    println!("EMBED OPTIONS:");
    println!("    -k, --kb-path <PATH>     Path to knowledge base JSON file");
    println!("    -o, --output <DIR>       Output directory for embeddings");
    println!("    -m, --model <NAME>       HuggingFace model name or local path\n");
    println!("QUERY OPTIONS:");
    println!("    -q, --query <TEXT>       Query text to embed");
    println!("    -m, --model <NAME>       HuggingFace model name or local path");
    println!("    -f, --format <FORMAT>    Output format: json (default) or binary\n");
    println!("GENERAL OPTIONS:");
    println!("    -h, --help               Show this help message");
    println!("    -v, --version            Show version\n");
    println!("SUPPORTED MODELS:");
    println!("    - sentence-transformers/all-MiniLM-L6-v2 (fast, good for development)");
    println!("    - BAAI/bge-small-en-v1.5 (better quality)");
    println!("    - BAAI/bge-base-en-v1.5 (high quality)\n");
    println!("EXAMPLES:");
    println!("    # Generate embeddings");
    println!("    eulix_embed embed -k kb.json -o ./embeddings\n");
    println!("    # Embed a query (JSON output)");
    println!("    eulix_embed query -q \"how does login work\" -m BAAI/bge-small-en-v1.5\n");
    println!("    # Embed a query (binary output)");
    println!("    eulix_embed query -q \"authentication flow\" -f binary > query.bin");
}

fn main() -> Result<()> {
    let args: Vec<String> = std::env::args().collect();

    // Show help if no arguments or --help flag
    if args.len() == 1 || args.contains(&"--help".to_string()) || args.contains(&"-h".to_string()) {
        print_help();
        std::process::exit(0);
    }

    if args.contains(&"--version".to_string()) || args.contains(&"-v".to_string()) {
        println!("0.1.3");
        std::process::exit(0);
    }

    // Determine command (default to "embed" for backward compatibility)
    let command = if args.len() > 1 && !args[1].starts_with('-') {
        args[1].as_str()
    } else {
        "embed"
    };

    match command {
        "query" => run_query_command(&args),
        "embed" => run_embed_command(&args),
        "compare" => {
    if args.len() < 4 {
        eprintln!("Usage: {} compare <json_index.json> <index.bin>", args[0]);
        std::process::exit(1);
    }

    let json_path = std::path::Path::new(&args[2]);
    let bin_path  = std::path::Path::new(&args[3]);

    compare_indices(json_path, bin_path)
}

        _ => {
            eprintln!("Error: Unknown command '{}'\n", command);
            print_help();
            std::process::exit(1);
        }
    }
}
fn compare_indices(json_path: &Path, bin_path: &Path) -> Result<()> {
    println!("Comparing index files...\n");

    // Check files exist
    if !json_path.exists() {
        anyhow::bail!("JSON file not found: {}", json_path.display());
    }
    if !bin_path.exists() {
        anyhow::bail!("Binary file not found: {}", bin_path.display());
    }

    // Load JSON
    println!("Loading JSON: {}", json_path.display());
    let json_index = EmbeddingIndex::load(json_path)?;
    println!("✓ Loaded {} embeddings\n", json_index.total_chunks);

    // Load Binary
    println!("Loading Binary: {}", bin_path.display());
    let bin_index = EmbeddingIndex::load_binary(bin_path)?;
    println!("✓ Loaded {} embeddings\n", bin_index.total_chunks);

    println!("{:=<70}", "");
    println!("{:^70}", "COMPARISON RESULTS");
    println!("{:=<70}\n", "");

    // Compare metadata
    let mut issues = Vec::new();

    println!("Model:");
    println!("  JSON:   '{}'", json_index.model);
    println!("  Binary: '{}'", bin_index.model);
    if json_index.model == bin_index.model {
        println!("✓ Match\n");
    } else {
        println!("x MISMATCH!\n");
        issues.push("Model names don't match");
    }

    println!("Dimension:");
    println!("  JSON:   {}", json_index.dimension);
    println!("  Binary: {}", bin_index.dimension);
    if json_index.dimension == bin_index.dimension {
        println!("✓ Match\n");
    } else {
        println!("x CRITICAL MISMATCH!\n");
        issues.push("Dimensions don't match - this is a critical error!");
    }

    println!("Total Chunks:");
    println!("  JSON:   {}", json_index.total_chunks);
    println!("  Binary: {}", bin_index.total_chunks);
    if json_index.total_chunks == bin_index.total_chunks {
        println!("✓ Match\n");
    } else {
        println!("x MISMATCH!\n");
        issues.push("Different number of embeddings");
    }

    // Compare first embedding if both exist
    if !json_index.embeddings.is_empty() && !bin_index.embeddings.is_empty() {
        println!("{:-<70}", "");
        println!("First Embedding Comparison:\n");

        let json_first = &json_index.embeddings[0];
        let bin_first = &bin_index.embeddings[0];

        println!("  ID:");
        println!("    JSON:   '{}'", json_first.id);
        println!("    Binary: '{}'", bin_first.id);

        println!("\n  Vector Dimensions:");
        println!("    JSON:   {} values", json_first.embedding.len());
        println!("    Binary: {} values", bin_first.embedding.len());

        if json_first.embedding.len() == bin_first.embedding.len() {
            println!("    ✓ Same length");

            // Show first 5 and last 5 values
            println!("\n  First 5 values:");
            println!("    {:>10} {:>15} {:>15} {:>10}", "Index", "JSON", "Binary", "Diff");
            println!("    {:-<10} {:-<15} {:-<15} {:-<10}", "", "", "", "");

            for i in 0..5.min(json_first.embedding.len()) {
                let j = json_first.embedding[i];
                let b = bin_first.embedding[i];
                let diff = (j - b).abs();
                println!("    {:>10} {:>15.6} {:>15.6} {:>10.2e}", i, j, b, diff);
            }

            if json_first.embedding.len() > 10 {
                println!("    ...");
                println!("\n  Last 5 values:");
                println!("    {:>10} {:>15} {:>15} {:>10}", "Index", "JSON", "Binary", "Diff");
                println!("    {:-<10} {:-<15} {:-<15} {:-<10}", "", "", "", "");

                let len = json_first.embedding.len();
                for i in (len - 5)..len {
                    let j = json_first.embedding[i];
                    let b = bin_first.embedding[i];
                    let diff = (j - b).abs();
                    println!("    {:>10} {:>15.6} {:>15.6} {:>10.2e}", i, j, b, diff);
                }
            }

            // Check if vectors match
            let max_diff = json_first.embedding.iter()
                .zip(bin_first.embedding.iter())
                .map(|(a, b)| (a - b).abs())
                .fold(0.0f32, |a, b| a.max(b));

            println!("\n  Maximum difference: {:.2e}", max_diff);

            if max_diff < 1e-6 {
                println!("✓ Vectors match (within tolerance)\n");
            } else {
                println!("x Vectors don't match!\n");
                issues.push("Embedding values are different");
            }
        } else {
            println!("x Different lengths!\n");
            issues.push("First embedding has different dimensions");
        }
    }

    println!("{:=<70}\n", "");

    // Summary
    if issues.is_empty() {
        println!("✓ SUCCESS: All checks passed!");
        println!("\nBoth index files are consistent and can be used interchangeably.");
    } else {
        println!("x ISSUES FOUND:\n");
        for (i, issue) in issues.iter().enumerate() {
            println!("  {}. {}", i + 1, issue);
        }

        println!("\n RECOMMENDED FIX:");
        println!("   1. Delete the corrupted binary file:");
        println!("      rm {}", bin_path.display());
        println!("   2. The binary will be regenerated from JSON on next load");
        println!("   3. Or regenerate both files fresh from your source data");

        anyhow::bail!("Index files are inconsistent");
    }

    Ok(())
}
fn run_query_command(args: &[String]) -> Result<()> {
    let mut query = String::new();
    let mut model = "sentence-transformers/all-MiniLM-L6-v2".to_string();
    let mut format = "json".to_string();

    // Parse arguments
    let mut i = 2; // Skip program name and "query" command
    while i < args.len() {
        match args[i].as_str() {
            "--query" | "-q" => {
                if i + 1 < args.len() {
                    query = args[i + 1].clone();
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
            "--format" | "-f" => {
                if i + 1 < args.len() {
                    format = args[i + 1].clone();
                    i += 2;
                } else {
                    eprintln!("Error: {} requires a value\n", args[i]);
                    print_help();
                    std::process::exit(1);
                }
            }
            _ => {
                eprintln!("Error: Unknown argument '{}'\n", args[i]);
                print_help();
                std::process::exit(1);
            }
        }
    }

    if query.is_empty() {
        eprintln!("Error: --query is required\n");
        print_help();
        std::process::exit(1);
    }

    eprintln!("Initializing embedding model: {}", model);
    let embedder = QueryEmbedder::new(&model)?;

    eprintln!("Generating embedding for query...");
    let embedding = embedder.embed_query(&query)?;

    match format.as_str() {
        "json" => {
            let output = serde_json::json!({
                "query": query,
                "model": embedder.model_name(),
                "dimension": embedder.dimension(),
                "embedding": embedding,
            });
            println!("{}", serde_json::to_string_pretty(&output)?);
        }
        "binary" => {
            // Write dimension first (4 bytes)
            let dim = embedding.len() as u32;
            let dim_bytes = dim.to_le_bytes();
            std::io::Write::write_all(&mut std::io::stdout(), &dim_bytes)?;

            // Write each float32 (4 bytes each)
            for value in embedding {
                let bytes = value.to_le_bytes();
                std::io::Write::write_all(&mut std::io::stdout(), &bytes)?;
            }
        }
        _ => {
            eprintln!("Error: Unknown format '{}'. Use 'json' or 'binary'\n", format);
            std::process::exit(1);
        }
    }

    Ok(())
}

fn run_embed_command(args: &[String]) -> Result<()> {
    let mut kb_path = "knowledge_base.json".to_string();
    let mut output_dir = "./embeddings".to_string();
    let mut model = "sentence-transformers/all-MiniLM-L6-v2".to_string();

    // Parse arguments (skip "embed" command if present)
    let start_idx = if args.len() > 1 && args[1] == "embed" { 2 } else { 1 };
    let mut i = start_idx;

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
            _ => {
                eprintln!("Error: Unknown argument '{}'\n", args[i]);
                print_help();
                std::process::exit(1);
            }
        }
    }

    println!();
    println!("{}", "=".repeat(70));
    println!("  EULIX EMBED - EMBEDDING GENERATOR");
    println!("{}", "=".repeat(70));
    println!();
    println!("CONFIGURATION");
    println!("{}", "-".repeat(70));
    println!("  KB Path:         {}", kb_path);

    let abs_path = std::fs::canonicalize(&kb_path)
        .unwrap_or_else(|_| Path::new(&kb_path).to_path_buf());
    println!("  Absolute Path:   {:?}", abs_path);

    println!("  Output Dir:      {}", output_dir);
    println!("  Model:           {}", model);
    println!();

    if !Path::new(&kb_path).exists() {
        println!("{}", "=".repeat(70));
        eprintln!("[ERROR] Knowledge base file not found: {}", kb_path);
        eprintln!("        Current directory: {:?}", std::env::current_dir().unwrap());
        eprintln!();
        eprintln!("[TIP]   Create a knowledge base file or specify the correct path");
        eprintln!("        using --kb-path option");
        println!("{}", "=".repeat(70));
        std::process::exit(1);
    }

    let pipeline = EmbeddingPipeline::new(&model)?;
    pipeline.process(Path::new(&kb_path), Path::new(&output_dir))?;

    Ok(())
}
