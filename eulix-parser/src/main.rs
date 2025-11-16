use clap::Parser;
use rayon::prelude::*;
use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::time::Instant;

mod kb;
mod parser;
mod utils;

use kb::types::*;
use parser::analyze::Analyzer;
use parser::language::Language;
use parser::python;
use utils::file_walker::FileWalker;

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
        println!("Parsing codebase: {}", args.root);
        println!("   Threads: {}", args.threads);
        println!("   Output: {}", args.output);
        println!("   Languages: {}", args.languages);
        println!("   Skip analysis: {}", args.no_analyze);
        println!();
    }

    // Phase 1: Parse all files
    if args.verbose {
        println!("Phase 1: Parsing files...");
    }
    let parse_start = Instant::now();
    let mut kb = parse_directory(&args.root, &args.languages, args.euignore.as_deref(), args.verbose)?;
    if args.verbose {
        println!(
            "   ✓ Parsed {} files in {:.2}s",
            kb.metadata.total_files,
            parse_start.elapsed().as_secs_f64()
        );
    }

    if !args.no_analyze {
        // Phase 2: Analyze and build indices (parallel where possible)
        if args.verbose {
            println!("Phase 2: Building call graph and indices...");
            println!("   (This may take a while for large codebases...)");
        }
        let analyze_start = Instant::now();

        // Check if codebase is too large for full analysis
        let file_count = kb.structure.len();
        if file_count > 10000 && args.verbose {
            println!("   ⚠️ Large codebase detected ({} files)", file_count);
            println!("   Consider using --no-analyze for faster results");
        }

        kb = Analyzer::analyze_and_build(kb, args.verbose);
        if args.verbose {
            println!(
                "   ✓ Built call graph with {} nodes, {} edges in {:.2}s",
                kb.call_graph.nodes.len(),
                kb.call_graph.edges.len(),
                analyze_start.elapsed().as_secs_f64()
            );
        }

        // Phase 3: Generate summary
        if args.verbose {
            println!(" Phase 3: Generating summary...");
        }
        let summary_start = Instant::now();
        let summary = Analyzer::generate_summary(&kb);
        if args.verbose {
            println!(
                "   ✓ Generated summary in {:.2}s",
                summary_start.elapsed().as_secs_f64()
            );
        }

        // Phase 4: Write outputs
        if args.verbose {
            println!(" Phase 4: Writing output files...");
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
            println!(
                "   ✓ Wrote {} ({} bytes)",
                args.output,
                fs::metadata(output_path)?.len()
            );
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
            println!("   ✓ Wrote {}", index_path.display());
        }

        // Write summary.json
        let summary_path = output_dir.join(format!("{}_summary.json", base_name));
        let summary_json = serde_json::to_string_pretty(&summary)?;
        fs::write(&summary_path, summary_json)?;
        if args.verbose {
            println!("   ✓ Wrote {}", summary_path.display());
        }

        // Write call_graph.json
        let callgraph_path = output_dir.join(format!("{}_call_graph.json", base_name));
        let callgraph_json = serde_json::to_string_pretty(&kb.call_graph)?;
        fs::write(&callgraph_path, callgraph_json)?;
        if args.verbose {
            println!("   ✓ Wrote {}", callgraph_path.display());
        }

        if args.verbose {
            println!(
                "\n✨ Complete! Total time: {:.2}s",
                start_time.elapsed().as_secs_f64()
            );
            println!("\nStatistics:");
            println!("   Files: {}", kb.metadata.total_files);
            println!("   Lines of code: {}", kb.metadata.total_loc);
            println!("   Functions: {}", kb.metadata.total_functions);
            println!("   Classes: {}", kb.metadata.total_classes);
            println!("   Methods: {}", kb.metadata.total_methods);
            println!("   Call graph edges: {}", kb.call_graph.edges.len());
            println!("   Entry points: {}", kb.entry_points.len());
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
            println!("Writing output (analysis skipped)...");
        }

        let output_path = Path::new(&args.output);
        if let Some(parent) = output_path.parent() {
            fs::create_dir_all(parent)?;
        }

        let kb_json = serde_json::to_string_pretty(&kb)?;
        fs::write(output_path, kb_json)?;

        if args.verbose {
            println!(
                "   ✓ Wrote {} ({} bytes)",
                args.output,
                fs::metadata(output_path)?.len()
            );
            println!(
                "\n✨ Complete! Total time: {:.2}s",
                start_time.elapsed().as_secs_f64()
            );
            println!("\nStatistics:");
            println!("   Files: {}", kb.metadata.total_files);
            println!("   Lines of code: {}", kb.metadata.total_loc);
            println!("   Functions: {}", kb.metadata.total_functions);
            println!("   Classes: {}", kb.metadata.total_classes);
            println!("   Methods: {}", kb.metadata.total_methods);
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

fn parse_directory(
    dir: &str,
    languages: &str,
    euignore_path: Option<&str>,
    verbose: bool,
) -> Result<KnowledgeBase, Box<dyn std::error::Error>> {
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
        println!("   Using .euignore: {:?}", euignore.as_ref().unwrap());
    }

    // Collect all source files based on language filter
    let files = collect_source_files(&path, euignore.as_deref(), languages, verbose)?;

    if verbose {
        println!("   Found {} source files", files.len());
    }

    // Parse files in parallel using Rayon
    let results: Vec<_> = files
        .par_iter()
        .filter_map(|file_path| match parse_file(file_path, &path) {
            Ok(result) => Some(result),
            Err(e) => {
                if verbose {
                    eprintln!("   Warning: Failed to parse {}: {}", file_path.display(), e);
                }
                None
            }
        })
        .collect();

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

    Ok(KnowledgeBase {
        metadata,
        structure,
        call_graph: CallGraph::default(),
        dependency_graph: DependencyGraph::default(),
        indices: Indices::default(),
        entry_points: vec![],
        external_dependencies: vec![],
        patterns: PatternInfo::default(),
    })
}

fn collect_source_files(
    root: &Path,
    euignore_path: Option<&Path>,
    languages: &str,
    verbose: bool,
) -> Result<Vec<PathBuf>, Box<dyn std::error::Error>> {
    let mut all_files = Vec::new();

    // Parse language filter
    let lang_filters: Vec<Language> = if languages == "all" {
        vec![
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
                "python" | "py" => Some(Language::Python),
                "javascript" | "js" => Some(Language::JavaScript),
                "typescript" | "ts" => Some(Language::TypeScript),
                "go" | "golang" => Some(Language::Go),
                "rust" | "rs" => Some(Language::Rust),
                _ => {
                    if verbose {
                        eprintln!("   Warning: Unknown language filter '{}'", lang_str);
                    }
                    None
                }
            })
            .collect()
    };

    // Use FileWalker for each supported language
    for lang in &lang_filters {
        match lang {
            Language::Python => {
                let walker = FileWalker::new(root.to_path_buf(), euignore_path.map(|p| p.to_path_buf()));
                match walker.find_python_files() {
                    Ok(files) => all_files.extend(files),
                    Err(e) => {
                        if verbose {
                            eprintln!("   Warning: Failed to collect Python files: {}", e);
                        }
                    }
                }
            }
            _ => {
                // For other languages, use generic file collection
                // TODO: Implement language-specific walkers
                if verbose {
                    println!("   Note: Using generic file walker for {:?}", lang);
                }
                collect_files_by_extension(root, root, &mut all_files, euignore_path, lang)?;
            }
        }
    }

    // Remove duplicates (in case of overlap)
    all_files.sort();
    all_files.dedup();

    Ok(all_files)
}

fn collect_files_by_extension(
    root: &Path,
    current: &Path,
    files: &mut Vec<PathBuf>,
    euignore_path: Option<&Path>,
    language: &Language,
) -> Result<(), Box<dyn std::error::Error>> {
    if !current.is_dir() {
        return Ok(());
    }

    // Create ignore filter if euignore exists
    let ignore_filter = euignore_path.and_then(|p| {
        if p.exists() {
            Some(utils::ignore::IgnoreFilter::new(root))
        } else {
            None
        }
    });

    for entry in fs::read_dir(current)? {
        let entry = entry?;
        let path = entry.path();

        // Check if path should be ignored
        if let Some(filter) = &ignore_filter {
            if filter.should_ignore(&path) {
                continue;
            }
        }

        if path.is_dir() {
            collect_files_by_extension(root, &path, files, euignore_path, language)?;
        } else if path.is_file() {
            let detected_lang = Language::detect(&path);
            if detected_lang == *language {
                files.push(path);
            }
        }
    }

    Ok(())
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
            Err("Go parsing not yet implemented".into())
        }
        Language::Rust => {
            Err("Rust parsing not yet implemented".into())
        }
        _ => Err(format!("Unsupported language: {:?}", lang).into()),
    }
}
