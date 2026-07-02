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
use crate::struc::kb_struct::EntryPointsRef;
use crate::struc::kb_struct::ExternalDepsRef;
use crate::struc::kb_struct::FileData;
use crate::struc::kb_struct::IndexDataRef;
use crate::struc::kb_struct::Indices;
use crate::struc::kb_struct::KnowledgeBase;
use crate::struc::kb_struct::KnowledgeBaseSimplifiedRef;
use crate::struc::kb_struct::Metadata;
use crate::struc::kb_struct::PatternInfo;
use crate::struc::kb_struct::PatternsRef;
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

#[cfg(target_os = "linux")]
mod os_io {
    use std::fs::File;
    use std::os::unix::io::AsRawFd;
    use std::path::Path;

    const MIN_PREFETCH_SIZE: u64 = 1024 * 1024; // 1MB
    const MIN_CACHE_EVICT_SIZE: u64 = 10 * 1024 * 1024; // 10MB

    pub fn prefetch(path: &Path) {
        if let Ok(metadata) = std::fs::metadata(path) {
            if metadata.len() > MIN_PREFETCH_SIZE {
                if let Ok(f) = std::fs::File::open(path) {
                    let fd = f.as_raw_fd();
                    let size = metadata.len().min(16 * 1024 * 1024);
                    unsafe {
                        libc::readahead(fd, 0, size as usize);
                    }
                }
            }
        }
    }

    pub fn hint_read_sequential(f: &File) {
        let fd = f.as_raw_fd();
        unsafe {
            libc::posix_fadvise(fd, 0, 0, libc::POSIX_FADV_SEQUENTIAL);
        }
    }

    // Evict large files from cache after parsing to free memory
    pub fn done_with_file(f: &File) {
        if let Ok(metadata) = f.metadata() {
            if metadata.len() > MIN_CACHE_EVICT_SIZE {
                let fd = f.as_raw_fd();
                unsafe {
                    libc::posix_fadvise(fd, 0, 0, libc::POSIX_FADV_DONTNEED);
                }
            }
        }
    }

    // No-op for writes - let kernel handle it
    pub fn flush_output(_f: &File) {}

    pub fn hint_write_sequential(_f: &File) {
        // Intentionally empty - writes don't need read hints
    }

    pub fn flush_and_drop(f: &std::fs::File) {
        if let Ok(metadata) = f.metadata() {
            if metadata.len() > MIN_CACHE_EVICT_SIZE {
                use std::os::unix::io::AsRawFd;
                unsafe {
                    libc::posix_fadvise(f.as_raw_fd(), 0, 0, libc::POSIX_FADV_DONTNEED);
                }
            }
        }
    }
}

#[cfg(target_os = "macos")]
mod os_io {
    use std::fs::File;
    use std::os::unix::io::AsRawFd;
    use std::path::Path;

    pub fn prefetch(_path: &Path) {}

    pub fn hint_read_sequential(f: &File) {
        let fd = f.as_raw_fd();
        unsafe {
            libc::fcntl(fd, libc::F_RDAHEAD, 1);
            libc::fcntl(fd, libc::F_NOCACHE, 1);
        }
    }

    pub fn done_with_file(_f: &File) {}

    pub fn flush_output(_f: &File) {}

    pub fn hint_write_sequential(_f: &File) {}

    pub fn flush_and_drop(_f: &std::fs::File) {}
}

#[cfg(target_os = "windows")]
mod os_io {
    use std::fs::File;
    use std::path::Path;

    pub fn prefetch(_path: &Path) {}

    pub fn hint_read_sequential(_f: &File) {}

    pub fn done_with_file(_f: &File) {}

    pub fn flush_output(_f: &File) {}

    pub fn hint_write_sequential(_f: &File) {}

    pub fn flush_and_drop(_f: &std::fs::File) {}
}

#[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
mod os_io {
    use std::fs::File;
    use std::path::Path;

    pub fn prefetch(_path: &Path) {}
    pub fn hint_read_sequential(_f: &File) {}
    pub fn done_with_file(_f: &File) {}
    pub fn flush_output(_f: &File) {}
    pub fn hint_write_sequential(_f: &File) {}
    pub fn flush_and_drop(_f: &std::fs::File) {}
}

// Opens a source file with the OS-optimal flags for sequential one-pass reads.
fn open_source_file(path: &Path) -> std::io::Result<std::fs::File> {
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::fs::OpenOptionsExt;
        std::fs::OpenOptions::new()
            .read(true)
            .custom_flags(0x0800_0000) // FILE_FLAG_SEQUENTIAL_SCAN
            .open(path)
    }
    #[cfg(not(target_os = "windows"))]
    {
        let f = std::fs::File::open(path)?;
        os_io::hint_read_sequential(&f);
        Ok(f)
    }
}

#[cfg(target_os = "linux")]
fn write_json_file(path: &Path, json: &str) -> std::io::Result<()> {
    let f = std::fs::File::create(path)?;
    let len = json.len();
    let buffer_size = if len > 100 * 1024 * 1024 {
        8 * 1024 * 1024
    } else if len > 10 * 1024 * 1024 {
        2 * 1024 * 1024
    } else {
        512 * 1024
    };
    let mut w = BufWriter::with_capacity(buffer_size, f);
    w.write_all(json.as_bytes())?;
    w.flush()?;
    Ok(())
}

#[cfg(not(target_os = "linux"))]
fn write_json_file(path: &Path, json: &str) -> std::io::Result<()> {
    let f = std::fs::File::create(path)?;
    let len = json.len();

    let buffer_size = if len > 100 * 1024 * 1024 {
        8 * 1024 * 1024
    } else if len > 10 * 1024 * 1024 {
        2 * 1024 * 1024
    } else {
        512 * 1024
    };

    let mut w = BufWriter::with_capacity(buffer_size, f);
    w.write_all(json.as_bytes())?;
    w.flush()?;
    Ok(())
}

fn default_thread_count() -> usize {
    #[cfg(feature = "num_cpus")]
    {
        num_cpus::get_physical().max(1)
    }
    #[cfg(not(feature = "num_cpus"))]
    {
        4
    }
}
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

fn validate_prism_input(val: &str) -> Result<u8, String> {
    let n: u8 = val
        .parse()
        .map_err(|_| "Value must be a number".to_string())?;
    if n == 1 || n == 2 {
        Ok(n)
    } else {
        Err("can only accept 1 or 2".to_string())
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

    // switches prism algorithm version check docs to see what each version does
    #[arg(short, long, value_parser = validate_prism_input)]
    prism: u8,
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let mut args = Args::parse();
    let version = env!("CARGO_PKG_VERSION");
    let bin_hash = "miku"; // temperoraly placeholder till I figure out how to store git hash.
                           // let bin_hash = var("VERGEN_GIT_SHA")?;
                           // let bin_hash = env!("VERGEN_GIT_SHA");
                           // Set thread pool size
    if args.threads == 0 {
        args.threads = default_thread_count();
    }
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
        println!("PRISM Version:   {}", args.prism);
        if let Some(ref ignore) = args.euignore {
            println!("[x] Ignore File:     {}", ignore);
        }
        println!();
        println!("{}", "═".repeat(64));
    }

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
        if args.verbose {
            println!("\n PHASE 2: BUILDING CALL GRAPH & INDICES");
            println!("{}", "─".repeat(64));
            println!("   Analyzing relationships and dependencies...");
        }
        let analyze_start = Instant::now();
        let file_count = kb.structure.len();
        if file_count > 10000 && args.verbose {
            println!("   [!]  Large codebase detected ({} files)", file_count);
            println!("    Consider using --no-analyze for faster results");
        }

        kb = Analyzer::analyze_and_build(kb, args.verbose, args.prism);

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

        if args.verbose {
            println!("\n PHASE 3: GENERATING SUMMARY");
            println!("{}", "─".repeat(64));
        }
        let summary_start = Instant::now();
        let summary = Analyzer::generate_summary(&kb);
        const TOP_K: usize = 20;
        let metrics = Analyzer::generate_metrics(&kb, TOP_K);

        if args.verbose {
            println!(
                " Summary + metrics generated in {:.2}s",
                summary_start.elapsed().as_secs_f64()
            );
            println!("{}", "═".repeat(64));
            println!("\n PHASE 4: WRITING OUTPUT FILES");
            println!("{}", "─".repeat(64));
        }

        let output_path = Path::new(&args.output);
        let output_dir = output_path.parent().unwrap_or_else(|| Path::new("."));
        fs::create_dir_all(output_dir)?;

        let base_name = output_path
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("kb");
        let index_path = output_dir.join(format!("{}_index.json", base_name));
        let summary_path = output_dir.join(format!("{}_summary.json", base_name));
        let callgraph_path = output_dir.join(format!("{}_call_graph.json", base_name));
        let metrics_path = output_dir.join(format!("{}_metrics.json", base_name));
        let ep_path = output_dir.join(format!("{}_entry_points.json", base_name));
        let deps_path = output_dir.join(format!("{}_external_deps.json", base_name));
        let patterns_path = output_dir.join(format!("{}_patterns.json", base_name));

        let kb_ref = KnowledgeBaseSimplifiedRef {
            metadata: &kb.metadata,
            structure: &kb.structure,
        };
        let index_ref = IndexDataRef {
            indices: &kb.indices,
        };
        let cg_ref = CallGraphRef {
            nodes: &kb.call_graph.nodes,
            edges: &kb.call_graph.edges,
        };
        let ep_ref = EntryPointsRef {
            entry_points: &kb.entry_points,
        };
        let deps_ref = ExternalDepsRef {
            external_dependencies: &kb.external_dependencies,
        };
        let pat_ref = PatternsRef {
            patterns: &kb.patterns,
        };

        // Serialize all four in parallel — each worker borrows its own ref
        let (
            (kb_json, index_json),
            ((summary_json, callgraph_json), (metrics_json, (ep_json, (deps_json, patterns_json)))),
        ) = rayon::join(
            || {
                rayon::join(
                    || sonic_rs::to_string(&kb_ref).expect("serialize kb"),
                    || sonic_rs::to_string(&index_ref).expect("serialize indices"),
                )
            },
            || {
                rayon::join(
                    || {
                        rayon::join(
                            || sonic_rs::to_string_pretty(&summary).expect("serialize summary"),
                            || sonic_rs::to_string(&cg_ref).expect("serialize call_graph"),
                        )
                    },
                    || {
                        rayon::join(
                            || sonic_rs::to_string_pretty(&metrics).expect("serialize metrics"),
                            || {
                                rayon::join(
                                    || {
                                        sonic_rs::to_string(&ep_ref)
                                            .expect("serialize entry_points")
                                    },
                                    || {
                                        rayon::join(
                                            || {
                                                sonic_rs::to_string(&deps_ref)
                                                    .expect("serialize external_deps")
                                            },
                                            || {
                                                sonic_rs::to_string(&pat_ref)
                                                    .expect("serialize patterns")
                                            },
                                        )
                                    },
                                )
                            },
                        )
                    },
                )
            },
        );

        // Write all four files in parallel
        let files: Vec<(&Path, &str, &str)> = vec![
            (output_path, "knowledge base", kb_json.as_str()),
            (&index_path, "index", index_json.as_str()),
            (&summary_path, "summary", summary_json.as_str()),
            (&callgraph_path, "call graph", callgraph_json.as_str()),
            (&metrics_path, "metrics", metrics_json.as_str()),
            (&ep_path, "entry points", ep_json.as_str()),
            (&deps_path, "external deps", deps_json.as_str()),
            (&patterns_path, "patterns", patterns_json.as_str()),
        ];

        let write_errors: Vec<String> = files
            .iter()
            .filter_map(|(path, name, json)| {
                if args.verbose {
                    println!("   Writing {}...", name);
                }
                write_json_file(path, json)
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
        write_json_file(output_path, &kb_json)?;

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

    let pb = if verbose {
        let pb = ProgressBar::new(files.len() as u64);
        pb.set_style(
            ProgressStyle::default_bar()
                .template(
                    "{spinner:.green} [{elapsed_precise}] [{bar:40.cyan/blue}] {pos}/{len} ({eta})",
                )
                .unwrap()
                .progress_chars("#>-"),
        );
        Some(pb)
    } else {
        None
    };

    // Thread-safe stats collection
    let stats = Arc::new(Mutex::new(ParseStats::new()));

    // Prefetch large files to overlap I/O with parsing
    #[cfg(target_os = "linux")]
    if files.len() > 1 {
        // Prefetch in chunks to avoid overwhelming the I/O subsystem
        let prefetch_limit = files.len().min(1000);
        let prefetch_files = &files[0..prefetch_limit];
        for p in prefetch_files {
            os_io::prefetch(p);
        }
    }

    let chunk_size = if files.len() > 10000 { 100 } else { 50 };

    // Parse files in parallel using Rayon
    let (structure, total_loc, total_functions, total_classes, total_methods, languages_set) = {
        type Acc = (
            HashMap<String, FileData>,
            usize,
            usize,
            usize,
            usize,
            std::collections::HashSet<String>,
        );

        files
            .par_chunks(chunk_size)
            .fold(
                || {
                    (
                        HashMap::new(),
                        0usize,
                        0usize,
                        0usize,
                        0usize,
                        std::collections::HashSet::new(),
                    )
                },
                |mut acc: Acc, chunk: &[PathBuf]| {
                    for file_path in chunk {
                        let relative_path = file_path
                            .strip_prefix(&path)
                            .unwrap_or(file_path)
                            .to_string_lossy()
                            .to_string();

                        match parse_file(file_path, &path) {
                            Ok((rel, file_data)) => {
                                if let Some(ref pb) = pb {
                                    pb.inc(1);
                                    pb.set_message(format!("Parsed: {}", relative_path));
                                }
                                stats.lock().unwrap().parsed.push(relative_path.clone());

                                acc.1 += file_data.loc;
                                acc.2 += file_data.functions.len();
                                acc.3 += file_data.classes.len();
                                acc.4 += file_data
                                    .classes
                                    .iter()
                                    .map(|c| c.methods.len())
                                    .sum::<usize>();
                                acc.5.insert(file_data.language.clone());
                                acc.0.insert(rel, file_data);
                            }
                            Err(e) => {
                                if let Some(ref pb) = pb {
                                    pb.println(format!("   ✗ Failed: {} - {}", relative_path, e));
                                    pb.inc(1);
                                }
                                stats
                                    .lock()
                                    .unwrap()
                                    .failed
                                    .push((relative_path, e.to_string()));
                            }
                        }
                    }
                    acc
                },
            )
            .reduce(
                || (HashMap::new(), 0, 0, 0, 0, std::collections::HashSet::new()),
                |mut a, b| {
                    for (k, v) in b.0 {
                        a.0.insert(k, v);
                    }
                    a.1 += b.1;
                    a.2 += b.2;
                    a.3 += b.3;
                    a.4 += b.4;
                    a.5.extend(b.5);
                    a
                },
            )
    };

    let final_stats = Arc::try_unwrap(stats).unwrap().into_inner().unwrap();

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

    let walker = if let Some(ignore_path) = euignore_path {
        FileWalker::new(root.to_path_buf()).with_euignore(ignore_path.to_path_buf())
    } else {
        FileWalker::new(root.to_path_buf())
    };

    for lang in &lang_filters {
        if *lang == Language::Cpp {
            for ext in &["cpp", "cc", "cxx", "hpp", "hxx"] {
                match walker.walk_files(|path| {
                    path.extension()
                        .and_then(|e| e.to_str())
                        .map(|e| e == *ext)
                        .unwrap_or(false)
                }) {
                    Ok(files) => {
                        if verbose && !files.is_empty() {
                            println!("      • Found {} .{} files", files.len(), ext);
                        }
                        all_files.extend(files);
                    }
                    Err(e) => {
                        if verbose {
                            eprintln!("        Failed to collect .{} files: {}", ext, e);
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
                all_files.extend(files);
            }
            Err(e) => {
                if verbose {
                    eprintln!("        Failed to collect .{} files: {}", extension, e);
                }
            }
        }
    }

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

    // Open with hints
    let file = open_source_file(file_path)?;

    // For very large files, use mmap
    let _contents = if let Ok(metadata) = file.metadata() {
        if metadata.len() > 10 * 1024 * 1024 {
            use memmap2::Mmap;
            let mmap = unsafe { Mmap::map(&file)? };
            std::str::from_utf8(&mmap)?.to_string()
        } else {
            std::fs::read_to_string(file_path)?
        }
    } else {
        std::fs::read_to_string(file_path)?
    };

    // Parse the file
    let result = match lang {
        Language::Python => {
            let (_, fd) = python::parse_file(file_path)?;
            fd
        }
        Language::JavaScript => {
            return Err("JavaScript parsing not yet implemented".into());
        }
        Language::TypeScript => {
            let (_, fd) = typescript::parse_file(file_path)?;
            fd
        }
        Language::Go => {
            let (_, fd) = go::parse_file(file_path)?;
            fd
        }
        Language::C => {
            let (_, fd) = c::parse_file(file_path)?;
            fd
        }
        Language::Cpp => {
            let (_, fd) = cpp::parse_file(file_path)?;
            fd
        }
        Language::Rust => {
            let (_, fd) = rust_parser::parse_file(file_path)?;
            fd
        }
        _ => return Err(format!("Unsupported language: {:?}", lang).into()),
    };

    // Optionally hint that we're done with the file
    #[cfg(target_os = "linux")]
    os_io::done_with_file(&file);

    Ok((relative_path, result))
}

// MAYBE in future we can add hash
// fn compute_crc32(path: &Path) -> Option<u32> {
//     let bytes = fs::read(path).ok()?;
//     let mut hasher = Hasher::new();
//     hasher.update(&bytes);
//     Some(hasher.finalize())
// }
