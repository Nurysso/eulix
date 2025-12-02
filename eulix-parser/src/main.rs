use clap::Parser;
use rayon::prelude::*;
use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Instant;

mod kb;
mod parser;
mod utils;

use kb::types::*;
use parser::analyze::Analyzer;
use parser::language::Language;
use parser::python;
use parser::go;
use parser::c;
use utils::file_walker::FileWalker;

#[derive(Debug, Clone)]
struct ParseStats {
    parsed: Vec<String>,
    skipped: Vec<String>,
    failed: Vec<(String, String)>,
}

impl ParseStats {
    fn new() -> Self {
        Self {
            parsed: Vec::new(),
            skipped: Vec::new(),
            failed: Vec::new(),
        }
    }
}

/// Fast multi-language code parser
#[derive(Parser, Debug)]
#[command(name = "eulix_parser")]
#[command(about = "Fast multi-language code parser", long_about = None)]
struct Args {
    /// Project root directory
    #[arg(short, long)]
    root: String,

    /// Output file for knowledge base
    #[arg(short, long, default_value = "knowledge_base.json")]
    output: String,

    /// Number of threads for parallel parsing
    #[arg(short, long, default_value_t = 4)]
    threads: usize,

    /// Verbose output
    #[arg(short, long)]
    verbose: bool,

    /// Languages to parse (comma-separated, or "all")
    #[arg(short, long, default_value = "all")]
    languages: String,

    /// Skip analysis phase (faster, only parse files)
    #[arg(long)]
    no_analyze: bool,

    /// Path to custom .euignore file (defaults to <root>/.euignore)
    #[arg(long)]
    euignore: Option<String>,
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args = Args::parse();

    // Set thread pool size
    rayon::ThreadPoolBuilder::new()
        .num_threads(args.threads)
        .build_global()
        .unwrap();

    let start_time = Instant::now();

    if args.verbose {
        println!("╔════════════════════════════════════════════════════════════════╗");
        println!("║             EULIX PARSER - Code Analysis Tool                  ║");
        println!("╚════════════════════════════════════════════════════════════════╝");
        println!();
        println!("Project Root:    {}", args.root);
        println!("Threads:         {}", args.threads);
        println!("Output:          {}", args.output);
        println!("Languages:       {}", args.languages);
        println!("Skip Analysis:   {}", args.no_analyze);
        if let Some(ref ignore) = args.euignore {
            println!("[x] Ignore File:     {}", ignore);
        }
        println!();
        println!("{}", "═".repeat(64));
    }

    // Phase 1: Parse all files
    if args.verbose {
        println!("\n PHASE 1: FILE DISCOVERY & PARSING");
        println!("{}", "─".repeat(64));
    }
    let parse_start = Instant::now();
    let (mut kb, stats) = parse_directory(&args.root, &args.languages, args.euignore.as_deref(), args.verbose)?;

    if args.verbose {
        println!("\n{}", "─".repeat(64));
        println!("Parsing Complete!");
        println!("     Time:         {:.2}s", parse_start.elapsed().as_secs_f64());
        println!("     Parsed:       {} files", stats.parsed.len());
        println!("     Skipped:      {} files", stats.skipped.len());
        println!("     Failed:       {} files", stats.failed.len());
        println!("{}", "═".repeat(64));
    }

    if !args.no_analyze {
        // Phase 2: Analyze and build indices (parallel where possible)
        if args.verbose {
            println!("\n PHASE 2: BUILDING CALL GRAPH & INDICES");
            println!("{}", "─".repeat(64));
            println!("   Analyzing relationships and dependencies...");
        }
        let analyze_start = Instant::now();

        // Check if codebase is too large for full analysis
        let file_count = kb.structure.len();
        if file_count > 10000 && args.verbose {
            println!("   [!]  Large codebase detected ({} files)", file_count);
            println!("    Consider using --no-analyze for faster results");
        }

        kb = Analyzer::analyze_and_build(kb, args.verbose);

        if args.verbose {
            println!("\n{}", "─".repeat(64));
            println!(" Analysis Complete!");
            println!("  Time:         {:.2}s", analyze_start.elapsed().as_secs_f64());
            println!("  Graph Nodes:  {}", kb.call_graph.nodes.len());
            println!("  Graph Edges:  {}", kb.call_graph.edges.len());
            println!("{}", "═".repeat(64));
        }

        // Phase 3: Generate summary
        if args.verbose {
            println!("\n PHASE 3: GENERATING SUMMARY");
            println!("{}", "─".repeat(64));
        }
        let summary_start = Instant::now();
        let summary = Analyzer::generate_summary(&kb);

        if args.verbose {
            println!(" Summary generated in {:.2}s", summary_start.elapsed().as_secs_f64());
            println!("{}", "═".repeat(64));
        }

        // Phase 4: Write outputs
        if args.verbose {
            println!("\n PHASE 4: WRITING OUTPUT FILES");
            println!("{}", "─".repeat(64));
        }

        // Determine output directory and file
        let output_path = Path::new(&args.output);
        let output_dir = if let Some(parent) = output_path.parent() {
            parent
        } else {
            Path::new(".")
        };
        fs::create_dir_all(output_dir)?;

        // Write main kb file
        let kb_json = serde_json::to_string_pretty(&kb)?;
        fs::write(output_path, kb_json)?;
        if args.verbose {
            let size = fs::metadata(output_path)?.len();
            println!("   ✓ {} ({:.2} KB)", args.output, size as f64 / 1024.0);
        }

        // Write additional analysis files in the same directory
        let base_name = output_path
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("kb");

        // Write index.json
        let index_path = output_dir.join(format!("{}_index.json", base_name));
        let index_json = serde_json::to_string_pretty(&kb.indices)?;
        fs::write(&index_path, index_json)?;
        if args.verbose {
            let size = fs::metadata(&index_path)?.len();
            println!("   ✓ {}_index.json ({:.2} KB)", base_name, size as f64 / 1024.0);
        }

        // Write summary.json
        let summary_path = output_dir.join(format!("{}_summary.json", base_name));
        let summary_json = serde_json::to_string_pretty(&summary)?;
        fs::write(&summary_path, summary_json)?;
        if args.verbose {
            let size = fs::metadata(&summary_path)?.len();
            println!("   ✓ {}_summary.json ({:.2} KB)", base_name, size as f64 / 1024.0);
        }

        // Write call_graph.json
        let callgraph_path = output_dir.join(format!("{}_call_graph.json", base_name));
        let callgraph_json = serde_json::to_string_pretty(&kb.call_graph)?;
        fs::write(&callgraph_path, callgraph_json)?;
        if args.verbose {
            let size = fs::metadata(&callgraph_path)?.len();
            println!("   ✓ {}_call_graph.json ({:.2} KB)", base_name, size as f64 / 1024.0);
        }

        if args.verbose {
            println!("{}", "═".repeat(64));
            print_final_summary(&kb, &stats, start_time.elapsed().as_secs_f64());
        } else {
            println!(
                "✓ Parsed {} files ({} LOC) in {:.2}s → {}",
                kb.metadata.total_files,
                kb.metadata.total_loc,
                start_time.elapsed().as_secs_f64(),
                args.output
            );
        }
    } else {
        // Only write basic kb.json without analysis
        if args.verbose {
            println!("\n WRITING OUTPUT (ANALYSIS SKIPPED)");
            println!("{}", "─".repeat(64));
        }

        let output_path = Path::new(&args.output);
        if let Some(parent) = output_path.parent() {
            fs::create_dir_all(parent)?;
        }

        let kb_json = serde_json::to_string_pretty(&kb)?;
        fs::write(output_path, kb_json)?;

        if args.verbose {
            let size = fs::metadata(output_path)?.len();
            println!("   ✓ {} ({:.2} KB)", args.output, size as f64 / 1024.0);
            println!("{}", "═".repeat(64));
            print_final_summary(&kb, &stats, start_time.elapsed().as_secs_f64());
        } else {
            println!(
                "✓ Parsed {} files ({} LOC) in {:.2}s → {} (no analysis)",
                kb.metadata.total_files,
                kb.metadata.total_loc,
                start_time.elapsed().as_secs_f64(),
                args.output
            );
        }
    }

    Ok(())
}

fn print_final_summary(kb: &KnowledgeBase, stats: &ParseStats, total_time: f64) {
    println!("EXECUTION TIME");
    println!("   Total:                  {:.2}s", total_time);
    println!();

    println!("CODE METRICS");
    println!("   Files Processed:        {}", kb.metadata.total_files);
    println!("   Total Lines of Code:    {}", kb.metadata.total_loc);
    println!("   Functions:              {}", kb.metadata.total_functions);
    println!("   Classes:                {}", kb.metadata.total_classes);
    println!("   Methods:                {}", kb.metadata.total_methods);
    println!();

    println!("LANGUAGES DETECTED");
    for lang in &kb.metadata.languages {
        println!("   • {}", lang);
    }
    println!();

    println!(" ANALYSIS RESULTS");
    println!("   Call Graph Nodes:       {}", kb.call_graph.nodes.len());
    println!("   Call Graph Edges:       {}", kb.call_graph.edges.len());
    println!("   Entry Points:           {}", kb.entry_points.len());
    println!("   External Dependencies:  {}", kb.external_dependencies.len());
    println!();

    if !stats.failed.is_empty() {
        println!();
        println!("[!]  FAILED FILES:");
        for (file, reason) in &stats.failed {
            println!("   • {} - {}", file, reason);
        }
    }

    println!(" PARSING STATISTICS");
    println!("   ✓ Successfully Parsed:  {} files", stats.parsed.len());
    println!("   ⊘ Skipped:              {} files", stats.skipped.len());
    println!("   ✗ Failed:               {} files", stats.failed.len());
    println!(" Analysis complete!");
}

fn parse_directory(
    dir: &str,
    languages: &str,
    euignore_path: Option<&str>,
    verbose: bool,
) -> Result<(KnowledgeBase, ParseStats), Box<dyn std::error::Error>> {
    let path = PathBuf::from(dir);

    // Determine euignore path
    let euignore = euignore_path
        .map(PathBuf::from)
        .or_else(|| {
            let default_path = path.join(".euignore");
            if default_path.exists() {
                Some(default_path)
            } else {
                None
            }
        });

    if verbose && euignore.is_some() {
        println!("   [!] Using .euignore: {:?}", euignore.as_ref().unwrap());
    }

    // Collect all source files based on language filter
    let files = collect_source_files(&path, languages, verbose)?;

    if verbose {
        println!("    Discovered {} source files", files.len());
        println!();
    }

    // Thread-safe stats collection
    let stats = Arc::new(Mutex::new(ParseStats::new()));

    // Parse files in parallel using Rayon
    let results: Vec<_> = files
        .par_iter()
        .filter_map(|file_path| {
            let relative_path = file_path
                .strip_prefix(&path)
                .unwrap_or(file_path)
                .to_string_lossy()
                .to_string();

            match parse_file(file_path, &path) {
                Ok(result) => {
                    if verbose {
                        println!("   ✓ Parsed:  {}", relative_path);
                    }
                    stats.lock().unwrap().parsed.push(relative_path.clone());
                    Some(result)
                }
                Err(e) => {
                    let error_msg = e.to_string();
                    if verbose {
                        println!("   ✗ Failed:  {} - {}", relative_path, error_msg);
                    }
                    stats.lock().unwrap().failed.push((relative_path, error_msg));
                    None
                }
            }
        })
        .collect();

    let final_stats = Arc::try_unwrap(stats).unwrap().into_inner().unwrap();

    // Build knowledge base structure
    let mut structure = HashMap::new();
    let mut total_loc = 0;
    let mut total_functions = 0;
    let mut total_classes = 0;
    let mut total_methods = 0;
    let mut languages_set = std::collections::HashSet::new();

    for (relative_path, file_data) in results {
        total_loc += file_data.loc;
        total_functions += file_data.functions.len();
        total_classes += file_data.classes.len();
        total_methods += file_data
            .classes
            .iter()
            .map(|c| c.methods.len())
            .sum::<usize>();
        languages_set.insert(file_data.language.clone());
        structure.insert(relative_path, file_data);
    }

    // Create metadata
    let project_name = path
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("unknown")
        .to_string();

    let metadata = Metadata {
        project_name,
        version: "1.0".to_string(),
        parsed_at: chrono::Utc::now().to_rfc3339(),
        languages: languages_set.into_iter().collect(),
        total_files: structure.len(),
        total_loc,
        total_functions,
        total_classes,
        total_methods,
    };

    let kb = KnowledgeBase {
        metadata,
        structure,
        call_graph: CallGraph::default(),
        dependency_graph: DependencyGraph::default(),
        indices: Indices::default(),
        entry_points: vec![],
        external_dependencies: vec![],
        patterns: PatternInfo::default(),
    };

    Ok((kb, final_stats))
}

#[allow(dead_code)]
fn collect_source_files(
    root: &Path,
    // euignore_path: Option<&Path>,
    languages: &str,
    verbose: bool,
) -> Result<Vec<PathBuf>, Box<dyn std::error::Error>> {
    let mut all_files = Vec::new();

    // Parse language filter
    let lang_filters: Vec<Language> = if languages == "all" {
        vec![
            Language::C,
            Language::Python,
            Language::JavaScript,
            Language::TypeScript,
            Language::Go,
            Language::Rust,
        ]
    } else {
        languages
            .split(',')
            .map(|s| s.trim())
            .filter_map(|lang_str| match lang_str.to_lowercase().as_str() {
                "c" | "C" => Some(Language::C),
                "python" | "py" => Some(Language::Python),
                "javascript" | "js" => Some(Language::JavaScript),
                "typescript" | "ts" => Some(Language::TypeScript),
                "go" | "golang" => Some(Language::Go),
                "rust" | "rs" => Some(Language::Rust),
                _ => {
                    if verbose {
                        eprintln!("     Unknown language filter '{}'", lang_str);
                    }
                    None
                }
            })
            .collect()
    };

    if verbose {
        println!("    Searching for files...");
    }

    // Use FileWalker for all languages
    let walker = FileWalker::new(root.to_path_buf());

    for lang in &lang_filters {
        let extension = match lang {
            Language::C => "c",
            Language::Python => "py",
            Language::JavaScript => "js",
            Language::TypeScript => "ts",
            Language::Go => "go",
            Language::Rust => "rs",
            _ => continue,
        };

        match walker.walk_files(|path| {
            path.extension()
                .and_then(|ext| ext.to_str())
                .map(|ext| ext == extension)
                .unwrap_or(false)
        }) {
            Ok(files) => {
                if verbose && !files.is_empty() {
                    println!("      • Found {} .{} files", files.len(), extension);
                }
                all_files.extend(files)
            },
            Err(e) => {
                if verbose {
                    eprintln!("        Failed to collect .{} files: {}", extension, e);
                }
            }
        }
    }

    // Remove duplicates (in case of overlap)
    all_files.sort();
    all_files.dedup();

    Ok(all_files)
}

fn parse_file(
    file_path: &Path,
    root: &Path,
) -> Result<(String, FileData), Box<dyn std::error::Error>> {
    let lang = Language::detect(file_path);

    let relative_path = file_path
        .strip_prefix(root)
        .unwrap_or(file_path)
        .to_string_lossy()
        .to_string();

    match lang {
        Language::Python => {
            let (_, file_data) = python::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        Language::JavaScript => {
            Err("JavaScript parsing not yet implemented".into())
        }
        Language::TypeScript => {
            Err("TypeScript parsing not yet implemented".into())
        }
        Language::Go => {
            let (_, file_data) = go::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        Language::C => {
            let (_, file_data) = c::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        Language::Rust => {
            Err("Rust parsing not yet implemented".into())
        }
        _ => Err(format!("Unsupported language: {:?}", lang).into()),
    }
}
