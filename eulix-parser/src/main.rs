//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

//! # Eulix Parser
//!
//! A fast, multi-threaded source code parser that builds a structured
//! knowledge base from large codebases.
//!
//! ## Supported Languages
//!
//! | Language   | Extensions              |
//! |------------|-------------------------|
//! | C          | `.c`                    |
//! | C++        | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hxx` |
//! | Python     | `.py`                   |
//! | Rust       | `.rs`                   |
//! | TypeScript | `.ts`                   |
//! | Go         | `.go`                   |
//!
//! ## Pipeline
//!
//! The parser runs in four sequential phases:
//!
//! 1. **File Discovery & Parsing** — Walks the project root, filters by
//!    language, respects `.euignore` exclusion rules, and parses all
//!    source files in parallel via Rayon. Extracts functions, classes,
//!    methods, imports, and LOC per file.
//!
//! 2. **Analysis** — Builds a call graph (nodes = callables, edges =
//!    call relationships) and reverse call graph, resolves cross-file
//!    call locations using PRISM aproximate precission analysis, detects patterns,
//!    identifies entry points, and enumerates external dependencies.
//!
//! 3. **Summary Generation** — Produces a high-level summary of the
//!    knowledge base (language breakdown, top-level metrics, dependency
//!    overview).
//!
//! 4. **Output** — Writes four JSON artifacts to the output directory:
//!    - `kb.json`            — full knowledge base (files + graph)
//!    - `kb_index.json`      — symbol and file indices
//!    - `kb_summary.json`    — human-readable project summary
//!    - `kb_call_graph.json` — serialized call graph (nodes + edges)
//!
//! ## Usage
//!
//! ```bash
//! eulix_parser --root ./my_project --output out/kb.json --threads 12
//! eulix_parser --root . --languages rust,python --no-analyze
//! eulix_parser --root . --euignore ./.euignore --verbose
//! ```
//!
//! ## Performance
//!
//! Parsing is parallelised across all available threads with Rayon.
//! On a 12-thread run against ~37k files (26M LOC), typical wall time
//! is ~46s for parsing and ~5s for analysis.
//! For codebases exceeding ~10k files, `--no-analyze` is recommended
//! if only the raw parse output is needed.

use clap::Parser;
use indicatif::{ProgressBar, ProgressStyle};
use mimalloc::MiMalloc;
use rayon::prelude::*;
use std::collections::HashMap;
use std::fs;
use std::io::{BufWriter, Write};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Instant;

mod parser;
mod struc;
mod utils;

use crate::struc::kb_struct::CallGraph;
use crate::struc::kb_struct::CallGraphRef;
use crate::struc::kb_struct::DependencyGraph;
use crate::struc::kb_struct::FileData;
use crate::struc::kb_struct::IndexDataRef;
use crate::struc::kb_struct::Indices;
use crate::struc::kb_struct::KnowledgeBase;
use crate::struc::kb_struct::KnowledgeBaseSimplifiedRef;
use crate::struc::kb_struct::Metadata;
use crate::struc::kb_struct::PatternInfo;
use parser::analyze::Analyzer;
use parser::c;
use parser::cpp;
use parser::go;
use parser::language::Language;
use parser::python;
use parser::rust as rust_parser;
use parser::typescript;
use utils::file_walker::FileWalker;

#[global_allocator]
static GLOBAL: MiMalloc = MiMalloc;

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

#[derive(Parser, Debug)]
#[command(
    name = "eulix_parser",
    version = env!("CARGO_PKG_VERSION"),
    // disable_version_flag = true,
    about = "Fast multi-language code parser"
)]

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
    let version = env!("CARGO_PKG_VERSION");
    let bin_hash = "miku"; // temperoraly placeholder till I figure out how to store git hash.
                           // let bin_hash = var("VERGEN_GIT_SHA")?;
                           // let bin_hash = env!("VERGEN_GIT_SHA");
                           // Set thread pool size
    rayon::ThreadPoolBuilder::new()
        .num_threads(args.threads)
        .build_global()
        .unwrap();

    let start_time = Instant::now();

    if args.verbose {
        println!("╔════════════════════════════════════════════════════════════════╗");
        println!("║{:^64}║", format!("EULIX PARSER - v{}", version));
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
    let (mut kb, stats) = parse_directory(
        &args.root,
        &args.languages,
        args.euignore.as_deref(),
        args.verbose,
        version,
        &bin_hash,
    )?;

    // Store metadata before it might be moved
    let metadata = kb.metadata.clone();

    if args.verbose {
        println!("\n{}", "─".repeat(64));
        println!("Parsing Complete!");
        println!(
            "     Time:         {:.2}s",
            parse_start.elapsed().as_secs_f64()
        );
        println!("     Parsed:       {} files", stats.parsed.len());
        println!("     Skipped:      {} files", stats.skipped.len());
        println!("     Failed:       {} files", stats.failed.len());
        println!("{}", "═".repeat(64));
    }

    if !args.no_analyze {
        // Phase 2: Analyze and build indices
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
            println!(
                "  Time:         {:.2}s",
                analyze_start.elapsed().as_secs_f64()
            );
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
            println!(
                " Summary generated in {:.2}s",
                summary_start.elapsed().as_secs_f64()
            );
            println!("{}", "═".repeat(64));
        }
        // Phase 4: Write outputs
        if args.verbose {
            println!("\n PHASE 4: WRITING OUTPUT FILES");
            println!("{}", "─".repeat(64));
        }

        let output_path = Path::new(&args.output);
        let output_dir = if let Some(parent) = output_path.parent() {
            parent
        } else {
            Path::new(".")
        };
        fs::create_dir_all(output_dir)?;

        let base_name = output_path
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("kb");

        let index_path = output_dir.join(format!("{}_index.json", base_name));
        let summary_path = output_dir.join(format!("{}_summary.json", base_name));
        let callgraph_path = output_dir.join(format!("{}_call_graph.json", base_name));

        // Zero-copy views — no clone, no extra allocation
        let kb_ref = KnowledgeBaseSimplifiedRef {
            metadata: &kb.metadata,
            structure: &kb.structure,
        };
        let index_ref = IndexDataRef {
            indices: &kb.indices,
            entry_points: &kb.entry_points,
            external_dependencies: &kb.external_dependencies,
            patterns: &kb.patterns,
        };
        let cg_ref = CallGraphRef {
            nodes: &kb.call_graph.nodes,
            edges: &kb.call_graph.edges,
        };

        // Serialize all four in parallel — each worker borrows its own ref
        let ((kb_json, index_json), (summary_json, callgraph_json)) = rayon::join(
            || {
                rayon::join(
                    || sonic_rs::to_string(&kb_ref).expect("Failed to serialize kb"),
                    || sonic_rs::to_string(&index_ref).expect("Failed to serialize indices"),
                )
            },
            || {
                rayon::join(
                    || sonic_rs::to_string_pretty(&summary).expect("Failed to serialize summary"),
                    || sonic_rs::to_string(&cg_ref).expect("Failed to serialize call_graph"),
                )
            },
        );

        // Write all four files in parallel
        let files: Vec<(&Path, &str, &str)> = vec![
            (output_path, "knowledge base", kb_json.as_str()),
            (&index_path, "index", index_json.as_str()),
            (&summary_path, "summary", summary_json.as_str()),
            (&callgraph_path, "call graph", callgraph_json.as_str()),
        ];

        let write_errors: Vec<String> = files
            .par_iter()
            .filter_map(|(path, name, json)| {
                let result = fs::File::create(path).and_then(|f| {
                    let mut w = BufWriter::new(f);
                    w.write_all(json.as_bytes())
                });
                result
                    .err()
                    .map(|e| format!("{} ({}): {}", path.display(), name, e))
            })
            .collect();

        if !write_errors.is_empty() {
            for e in &write_errors {
                eprintln!("   ✗ Failed to write {}", e);
            }
        }

        if args.verbose {
            for (path, name, _) in &files {
                if path.exists() {
                    let size = fs::metadata(path)?.len();
                    println!(
                        "   ✓ {}: {} ({:.2} KB)",
                        name,
                        path.display(),
                        size as f64 / 1024.0
                    );
                }
            }
            println!("{}", "═".repeat(64));
            print_final_summary(&metadata, &stats, start_time.elapsed().as_secs_f64());
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
        // Write only kb.json without analysis
        if args.verbose {
            println!("\n WRITING OUTPUT (ANALYSIS SKIPPED)");
            println!("{}", "─".repeat(64));
        }

        let output_path = Path::new(&args.output);
        if let Some(parent) = output_path.parent() {
            fs::create_dir_all(parent)?;
        }

        // Write simplified KnowledgeBase
        let kb_simplified = KnowledgeBaseSimplifiedRef {
            metadata: &kb.metadata,
            structure: &kb.structure,
        };

        let kb_json = serde_json::to_string(&kb_simplified)?;
        fs::write(output_path, kb_json)?;

        if args.verbose {
            let size = fs::metadata(output_path)?.len();
            println!("   ✓ {} ({:.2} KB)", args.output, size as f64 / 1024.0);
            println!("{}", "═".repeat(64));
            print_final_summary(&metadata, &stats, start_time.elapsed().as_secs_f64());
        } else {
            println!(
                "✓ Parsed {} files ({} LOC) in {:.2}s → {} (no analysis)",
                metadata.total_files,
                metadata.total_loc,
                start_time.elapsed().as_secs_f64(),
                args.output
            );
        }
    }

    Ok(())
}

fn print_final_summary(metadata: &Metadata, stats: &ParseStats, total_time: f64) {
    println!("EXECUTION TIME");
    println!("   Total:                  {:.2}s", total_time);
    println!();

    println!("CODE METRICS");
    println!("   Files Processed:        {}", metadata.total_files);
    println!("   Total Lines of Code:    {}", metadata.total_loc);
    println!("   Functions:              {}", metadata.total_functions);
    println!("   Classes:                {}", metadata.total_classes);
    println!("   Methods:                {}", metadata.total_methods);
    println!();

    println!("LANGUAGES DETECTED");
    for lang in &metadata.languages {
        println!("   • {}", lang);
    }
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
    version: &str,
    git_hash: &str,
) -> Result<(KnowledgeBase, ParseStats), Box<dyn std::error::Error>> {
    let path = PathBuf::from(dir);

    // Determine euignore path
    let euignore = euignore_path.map(PathBuf::from).or_else(|| {
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
    let files = collect_source_files(&path, languages, euignore.as_deref(), verbose)?;

    if verbose {
        println!("    Discovered {} source files", files.len());
        println!();
    }

    let pb =
        if verbose {
            let pb = ProgressBar::new(files.len() as u64);
            pb.set_style(ProgressStyle::default_bar()
        .template("{spinner:.green} [{elapsed_precise}] [{bar:40.cyan/blue}] {pos}/{len} ({eta})")
        .unwrap()
        .progress_chars("#>-"));
            Some(pb)
        } else {
            None
        };

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
                    if let Some(ref pb) = pb {
                        pb.inc(1);
                        pb.set_message(format!("Parsed: {}", relative_path));
                    }
                    stats.lock().unwrap().parsed.push(relative_path.clone());
                    Some(result)
                }
                Err(e) => {
                    let error_msg = e.to_string();
                    if let Some(ref pb) = pb {
                        pb.println(format!("   ✗ Failed: {} - {}", relative_path, e));
                        pb.inc(1);
                    }
                    stats
                        .lock()
                        .unwrap()
                        .failed
                        .push((relative_path, error_msg));
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
        version: version.to_string(),
        git_hash: git_hash.to_string(),
        parsed_at: chrono::Utc::now().format("%Y.%m.%d.%H%M%S").to_string(),
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
    if let Some(pb) = pb {
        pb.finish_with_message("Parse complete!");
    }

    Ok((kb, final_stats))
}

fn collect_source_files(
    root: &Path,
    languages: &str,
    euignore_path: Option<&Path>,
    verbose: bool,
) -> Result<Vec<PathBuf>, Box<dyn std::error::Error>> {
    let mut all_files = Vec::new();

    // Parse language filter
    let lang_filters: Vec<Language> = if languages == "all" {
        vec![
            Language::C,
            Language::Cpp,
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
                "c" => Some(Language::C),
                "cpp" | "c++" | "cxx" => Some(Language::Cpp),
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

    // Use FileWalker for all languages; thread through the custom euignore path if provided
    let walker = if let Some(ignore_path) = euignore_path {
        FileWalker::new(root.to_path_buf()).with_euignore(ignore_path.to_path_buf())
    } else {
        FileWalker::new(root.to_path_buf())
    };

    for lang in &lang_filters {
        // C++ uses multiple extensions — handle separately
        if *lang == Language::Cpp {
            let cpp_exts = ["cpp", "cc", "cxx", "hpp", "hxx"];
            for ext in &cpp_exts {
                let ext_str = *ext;
                match walker.walk_files(|path| {
                    path.extension()
                        .and_then(|e| e.to_str())
                        .map(|e| e == ext_str)
                        .unwrap_or(false)
                }) {
                    Ok(files) => {
                        if verbose && !files.is_empty() {
                            println!("      • Found {} .{} files", files.len(), ext_str);
                        }
                        all_files.extend(files);
                    }
                    Err(e) => {
                        if verbose {
                            eprintln!("        Failed to collect .{} files: {}", ext_str, e);
                        }
                    }
                }
            }
            continue;
        }

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
            }
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
    // let checksum = compute_crc32(file_path);

    match lang {
        Language::Python => {
            let (_, file_data) = python::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        Language::JavaScript => Err("JavaScript parsing not yet implemented".into()),
        Language::TypeScript => {
            let (_, file_data) = typescript::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        Language::Go => {
            let (_, file_data) = go::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        Language::C => {
            let (_, file_data) = c::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        Language::Cpp => {
            let (_, file_data) = cpp::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        Language::Rust => {
            let (_, file_data) = rust_parser::parse_file(file_path)?;
            Ok((relative_path, file_data))
        }
        _ => Err(format!("Unsupported language: {:?}", lang).into()),
    }
}

// MAYBE in future we can add hash
// fn compute_crc32(path: &Path) -> Option<u32> {
//     let bytes = fs::read(path).ok()?;
//     let mut hasher = Hasher::new();
//     hasher.update(&bytes);
//     Some(hasher.finalize())
// }
