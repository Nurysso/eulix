//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

//! # Eulix Parser
//!
//! A fast, multi-threaded source code parser that builds a structured
//! knowledge base from large codebases.
//!
//! Tho the name is *parser* its more of analysis enginge designed for RAGs
//!
//! ## Supported Languages
//!
//! | Language   | Extensions                            |
//! |------------|---------------------------------------|
//! | C          | `.c`, `.h`                            |
//! | C++        | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hxx` |
//! | Python     | `.py`                                 |
//! | Rust       | `.rs`                                 |
//! | TypeScript | `.ts`                                 |
//! | Go         | `.go`                                 |
//! | JavaScript | `.js`                                 |
//! |java        | `java`                                |
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
//!    call locations using PRISM approximate precision analysis, detects patterns,
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
//! On a 12-thread run against ~37k files (35M LOC), typical wall time
//! is ~46s for parsing and ~5s for analysis.
//!
//! ## Limitations
//! - The goal is build good ast and call graphs to be used in RAGs so a pre-compiler or IR is out of goals
//!   and cause of this macro expansion in c,cpp or other languages are limited.
//! - eulix_parser depends upon tree-sitter@latest so a newer version of language sysntax will result in partial
//!   parse and not a complete failure

#![allow(unused_crate_dependencies)]
// Deps that only the library target uses (tree-sitter and its grammars, ignore,
// regex, xxhash-rust, zstd, anyhow) are declared in Cargo.toml but never
// referenced here. The binary consumes them through `libeulix`. Silence the
// per-target lint; see `src/lib.rs` for the mirror-image case.


mod memory;
mod os_io;
mod output;
mod parse;
mod report;
mod spill;
mod stats;

use clap::Parser;
use std::fs;
use std::path::Path;
use std::time::Instant;

use crate::memory::{default_thread_count, max_rss_mb};
use crate::output::{write_json_streaming, write_kb_from_spill};
use crate::parse::directory::parse_directory;
use crate::report::{field_report, print_final_summary, write_parse_report};

use libeulix::{struc, analyze};
use libeulix::struc::kb_struct::{
    CallGraphRef, EntryPointsRef, ExternalDepsRef, IndexViewRef, PatternsRef,
};
use libeulix::utils::utils::output_dir;

#[global_allocator]
static ALLOC: rpmalloc::RpMalloc = rpmalloc::RpMalloc;

#[derive(Parser, Debug)]
#[command(
    name = "eulix_parser",
    version = env!("CARGO_PKG_VERSION"),
    about = "Fast multi-language repository analysis and parsing engine"
)]
pub struct Args {
    /// Root directory of the repository to analyze
    #[arg(short = 'r', long)]
    root: String,

    /// Output file path for the generated knowledge base JSON
    #[arg(short = 'o', long, default_value = "knowledge_base.json")]
    output: String,

    /// Number of parallel worker threads for parsing
    #[arg(short = 't', long, default_value_t = 4)]
    threads: usize,

    /// Enable verbose output and detailed phase logging
    #[arg(short = 'v', long)]
    verbose: bool,

    /// Target languages to parse (comma-separated list, or "all")
    #[arg(short = 'l', long, default_value = "all")]
    languages: String,

    /// Skip the relationship analysis phase (parse files only)
    #[arg(short = 'n', long)]
    no_analyze: bool,

    /// Path to a custom .euignore file (defaults to <root>/.euignore)
    #[arg(short = 'e', long)]
    euignore: Option<String>,

    /// PRISM relationship engine version (1 or 2; see documentation for details)
    #[arg(short = 'p', long, value_parser = validate_prism_input)]
    prism: u8,

    /// Write a TSV report listing every partial and failed file (useful for diffing runs)
    #[arg(short = 'P', long)]
    parse_report: Option<String>,

    /// Resume from a previous interrupted run by reusing the spill file
    /// (only valid with --no-analyze)
    #[arg(short = 'u', long)]
    resume: bool,
}

pub fn validate_prism_input(val: &str) -> Result<u8, String> {
    let n: u8 = val
        .parse()
        .map_err(|_| "Value must be a number".to_string())?;
    if n == 1 || n == 2 {
        Ok(n)
    } else {
        Err("can only accept 1 or 2".to_string())
    }
}

pub fn main() -> Result<(), Box<dyn std::error::Error>> {
    #[cfg(unix)]
    #[allow(unsafe_code)]
    unsafe {
        libc::signal(libc::SIGPIPE, libc::SIG_IGN);
    }
    let mut args = Args::parse();
    let version = env!("CARGO_PKG_VERSION");

    let bin_hash = option_env!("VERGEN_GIT_SHA").unwrap_or("unknown");

    if args.threads == 0 {
        args.threads = default_thread_count();
    }
    rayon::ThreadPoolBuilder::new()
        .num_threads(args.threads)
        .build_global()?;
    let write_dir = output_dir(&args.output);
    let start_time = Instant::now();

    let rss_start = max_rss_mb();

    if args.verbose {
        println!("╔════════════════════════════════════════════════════════════════╗");
        println!("║{:^64}║", format!("EULIX PARSER - v{}", version));
        println!("╚════════════════════════════════════════════════════════════════╝");
        println!();
        println!("Project Root:    {}", args.root);
        println!("Threads:         {}", args.threads);
        println!("Output:          {}", write_dir.display());
        println!("Languages:       {}", args.languages);
        println!("Skip Analysis:   {}", args.no_analyze);
        println!("PRISM Version:   {}", args.prism);
        if let Some(ref ignore) = args.euignore {
            println!("[x] Ignore File:     {}", ignore);
        }
        println!();
        println!("Peak RSS:    {:.1} MB", rss_start);
        println!("{}", "═".repeat(64));
    }
    if args.resume && !args.no_analyze {
        return Err(
            "--resume requires --no-analyze (analysis needs a full in-memory FileData map)".into(),
        );
    }

    if args.verbose {
        println!("\n PHASE 1: FILE DISCOVERY & PARSING");
        println!("{}", "─".repeat(64));
    }
    let parse_start = Instant::now();
    let (mut kb, stats, spill) = parse_directory(
        &args.root,
        &args.languages,
        args.euignore.as_deref(),
        args.verbose,
        version,
        bin_hash,
        &write_dir,
        args.resume,
    )?;
    if std::env::var_os("EULIX_FIELD_REPORT").is_some() {
        field_report(&kb);
    }
    let rss_after_parse = max_rss_mb();
    let metadata = kb.metadata.clone();

    if args.verbose {
        println!("\n{}", "─".repeat(64));
        println!("Parsing Complete!");
        println!(
            "     Time:         {:.2}s",
            parse_start.elapsed().as_secs_f64()
        );
        println!("     Peak RSS:     {:.1} MB", rss_after_parse);
        println!("{}", "═".repeat(64));
    }

    if let Some(report_path) = args.parse_report.as_deref() {
        write_parse_report(Path::new(report_path), &stats)?;
        if args.verbose {
            println!("   Parse report written: {}", report_path);
        }
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

        kb = analyze::analyze_and_build(kb, args.verbose, args.prism);
        let rss_after_analyze = max_rss_mb();

        if args.verbose {
            println!("\n{}", "─".repeat(64));
            println!(" Analysis Complete!");
            println!(
                "  Time:         {:.2}s",
                analyze_start.elapsed().as_secs_f64()
            );
            println!("  Graph Nodes:  {}", kb.call_graph.nodes.len());
            println!("  Graph Edges:  {}", kb.call_graph.edges.len());
            println!("  Peak RSS:     {:.1} MB", rss_after_analyze);
            println!("{}", "═".repeat(64));
        }

        if args.verbose {
            println!("\n PHASE 3: GENERATING SUMMARY");
            println!("{}", "─".repeat(64));
        }
        let summary_start = Instant::now();
        let summary = analyze::summary::generate_summary(&kb);
        const TOP_K: usize = 20;
        let metrics = analyze::metrics::generate_metrics(&kb, TOP_K);
        let rss_after_summary = max_rss_mb();

        if args.verbose {
            println!(
                " Summary + metrics generated in {:.2}s",
                summary_start.elapsed().as_secs_f64()
            );
            println!("PEak RSS: {:1.} MB", rss_after_summary);
            println!("{}", "═".repeat(64));
            println!("\n PHASE 4: WRITING OUTPUT FILES");
            println!("{}", "─".repeat(64));
        }

        let output_path = Path::new(&args.output);
        let output_dir = &write_dir;
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

        let indices = analyze::indices::generate_indices_view(&kb);
        let index_ref = IndexViewRef { indices: &indices };
        let cg_ref = CallGraphRef::new(&kb, &indices);
        let ep_ref = EntryPointsRef {
            entry_points: &kb.entry_points,
        };
        let deps_ref = ExternalDepsRef {
            external_dependencies: &kb.external_dependencies,
        };
        let pat_ref = PatternsRef {
            patterns: &kb.patterns,
        };

        if args.verbose {
            println!("   Writing knowledge base...");
        }
        write_kb_from_spill(output_path, &kb.metadata, &spill)?;

        if args.verbose {
            println!("   Writing index...");
        }
        write_json_streaming(&index_path, &index_ref, false)?;

        if args.verbose {
            println!("   Writing summary...");
        }
        write_json_streaming(&summary_path, &summary, true)?;

        if args.verbose {
            println!("   Writing call graph...");
        }
        write_json_streaming(&callgraph_path, &cg_ref, false)?;

        if args.verbose {
            println!("   Writing metrics...");
        }
        write_json_streaming(&metrics_path, &metrics, true)?;

        if args.verbose {
            println!("   Writing entry points...");
        }
        write_json_streaming(&ep_path, &ep_ref, true)?;

        if args.verbose {
            println!("   Writing external deps...");
        }
        write_json_streaming(&deps_path, &deps_ref, false)?;

        if args.verbose {
            println!("   Writing patterns...");
        }
        write_json_streaming(&patterns_path, &pat_ref, true)?;
        // let rss_after_write = max_rss_mb();

        if args.verbose {
            let files_to_check = [
                (output_path, "knowledge base"),
                (&index_path, "index"),
                (&summary_path, "summary"),
                (&callgraph_path, "call graph"),
                (&metrics_path, "metrics"),
                (&ep_path, "entry points"),
                (&deps_path, "external deps"),
                (&patterns_path, "patterns"),
            ];
            for (path, name) in &files_to_check {
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
                "✓ Parsed {} files ({} clean, {} partial, {} failed; {} LOC) in {:.2}s → {}",
                stats.total(),
                stats.clean,
                stats.partial.len(),
                stats.failed.len(),
                kb.metadata.total_loc,
                start_time.elapsed().as_secs_f64(),
                args.output
            );
        }
    } else {
        if args.verbose {
            println!("\n WRITING OUTPUT (ANALYSIS SKIPPED)");
            println!("{}", "─".repeat(64));
        }

        let output_path = Path::new(&args.output);
        if let Some(parent) = output_path.parent() {
            fs::create_dir_all(parent)?;
        }

        write_kb_from_spill(output_path, &kb.metadata, &spill)?;
        let rss_after_write = max_rss_mb();

        if args.verbose {
            let size = fs::metadata(output_path)?.len();
            println!("   ✓ {} ({:.2} KB)", args.output, size as f64 / 1024.0);
            println!("   Peak RSS:     {:.1} MB", rss_after_write);
            println!("{}", "═".repeat(64));
            print_final_summary(&metadata, &stats, start_time.elapsed().as_secs_f64());
        } else {
            println!(
                "✓ Parsed {} files ({} clean, {} partial, {} failed; {} LOC) in {:.2}s → {} (no analysis)",
                stats.total(),
                stats.clean,
                stats.partial.len(),
                stats.failed.len(),
                metadata.total_loc,
                start_time.elapsed().as_secs_f64(),
                args.output,
            );
            println!("Peak RSS: {:.1} MB", rss_after_write);
        }
    }

    Ok(())
}
