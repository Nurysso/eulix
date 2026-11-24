// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
// Maintainer: Dawood (Nurysso) <nurysso [at] proton.me>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
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
//! | PHP        | `.php`, `.phtml`, `.inc`              |
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

mod analysis_pipeline;
mod cache;
mod engine;
mod memory;
#[cfg(not(target_os = "windows"))]
mod os_io;
mod output;
mod parse;
mod report;
mod spill;
mod stats;
mod views;

use clap::Parser;
use std::fs;
use std::path::Path;
use std::time::Instant;

use crate::analysis_pipeline::analyze_and_build;
use crate::engine::Engine;
use crate::memory::{default_thread_count, max_rss_mb};
use crate::output::{output_dir, write_checksum_copy, write_fb_output, write_kb_fb};
use crate::parse::directory::parse_directory;
use crate::report::{field_report, print_final_summary, write_parse_report};
use crate::views::CallGraphRef;
use libeulix::fb_io;
use libeulix::utils::file_walker::ChecksumFormat;

use libeulix::analyze;

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

    /// Also write a JSON copy of every FlatBuffers output, read back from the
    /// binary file, for inspecting changes. Off by default; when off, stale
    /// copies from an earlier run are removed.
    #[arg(long)]
    emit_json: bool,

    /// Checksum baseline eulix-cli uses to spot changes: fb (checksum.fb.zst),
    /// json (checksum.json.zst) or none (no hashing, no file). The baseline
    /// of the other format is removed.
    #[arg(long, default_value = "fb")]
    checksum: ChecksumFormat,
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

pub fn main() {
    let code = match run() {
        Ok(()) => 0,
        Err(e) => {
            eprintln!("Error: {e:?}");
            1
        }
    };
    let _ = std::io::Write::flush(&mut std::io::stdout());
    // rpmalloc's exit-time finalizer unmaps heaps while detached rayon workers
    // are still finalizing theirs (SIGSEGV on large runs); the OS reclaims it all anyway.
    #[cfg(unix)]
    #[allow(unsafe_code)]
    unsafe {
        libc::_exit(code)
    }
    #[cfg(not(unix))]
    std::process::exit(code)
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
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

    // One thread pool, owned by the engine. Every parallel operation in
    // this run goes through `engine.pool()` — no global pool, no surprise
    // contention with other rayon users.
    let engine = Engine::with_threads(args.threads)?;

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
        &engine,
        args.emit_json,
        args.checksum,
    )?;
    write_checksum_copy(
        &write_dir,
        args.emit_json && args.checksum == ChecksumFormat::FlatBuffers,
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

        // Run on the engine's pool too, so --threads also bounds Phase 2
        // instead of rayon's global pool sized to every core.
        kb = engine
            .pool()
            .install(|| analyze_and_build(kb, args.verbose, args.prism, Path::new(&args.root)));
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
        // The knowledge base is <stem>.fb next to --output, whatever its extension.
        let kb_path = output_path.with_extension("fb");
        let output_dir = &write_dir;
        fs::create_dir_all(output_dir)?;

        let base_name = output_path
            .file_stem()
            .and_then(|s| s.to_str())
            .unwrap_or("kb");
        let index_path = output_dir.join(format!("{}_index.fb", base_name));
        let summary_path = output_dir.join(format!("{}_summary.fb", base_name));
        let callgraph_path = output_dir.join(format!("{}_call_graph.fb", base_name));
        let metrics_path = output_dir.join(format!("{}_metrics.fb", base_name));
        let ep_path = output_dir.join(format!("{}_entry_points.fb", base_name));
        let deps_path = output_dir.join(format!("{}_external_deps.fb", base_name));
        let patterns_path = output_dir.join(format!("{}_patterns.fb", base_name));

        let indices = analyze::indices::generate_indices_view(&kb);
        let cg_ref = CallGraphRef::new(&kb, &indices);

        if args.verbose {
            println!("   Writing knowledge base...");
        }
        write_kb_fb(&kb_path, &kb.metadata, &spill, args.emit_json)?;

        if args.verbose {
            println!("   Writing index...");
        }
        let index_bytes = fb_io::encode_indices(&indices)?;
        write_fb_output(
            &index_path,
            index_bytes,
            fb_io::indices_json,
            args.emit_json,
        )?;

        if args.verbose {
            println!("   Writing summary...");
        }
        write_fb_output(
            &summary_path,
            fb_io::encode_summary(&summary),
            fb_io::summary_json,
            args.emit_json,
        )?;

        if args.verbose {
            println!("   Writing call graph...");
        }
        let cg_bytes = fb_io::encode_call_graph(
            cg_ref.nodes,
            cg_ref.edges,
            &cg_ref.tags,
            &cg_ref.file_categories,
        )?;
        write_fb_output(
            &callgraph_path,
            cg_bytes,
            fb_io::call_graph_json,
            args.emit_json,
        )?;

        if args.verbose {
            println!("   Writing metrics...");
        }
        write_fb_output(
            &metrics_path,
            fb_io::encode_metrics(&metrics),
            fb_io::metrics_json,
            args.emit_json,
        )?;

        if args.verbose {
            println!("   Writing entry points...");
        }
        write_fb_output(
            &ep_path,
            fb_io::encode_entry_points(&kb.entry_points),
            fb_io::entry_points_json,
            args.emit_json,
        )?;

        if args.verbose {
            println!("   Writing external deps...");
        }
        write_fb_output(
            &deps_path,
            fb_io::encode_external_deps(&kb.external_dependencies),
            fb_io::external_deps_json,
            args.emit_json,
        )?;

        if args.verbose {
            println!("   Writing patterns...");
        }
        write_fb_output(
            &patterns_path,
            fb_io::encode_patterns(&kb.patterns),
            fb_io::patterns_json,
            args.emit_json,
        )?;

        if args.verbose {
            let files_to_check = [
                (kb_path.as_path(), "knowledge base"),
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
                kb_path.display()
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
        let kb_path = output_path.with_extension("fb");

        write_kb_fb(&kb_path, &kb.metadata, &spill, args.emit_json)?;
        let rss_after_write = max_rss_mb();

        if args.verbose {
            let size = fs::metadata(&kb_path)?.len();
            println!(
                "   ✓ {} ({:.2} KB)",
                kb_path.display(),
                size as f64 / 1024.0
            );
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
                kb_path.display(),
            );
            println!("Peak RSS: {:.1} MB", rss_after_write);
        }
    }

    Ok(())
}
