// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use indicatif::{ProgressBar, ProgressStyle};

use super::acc::Acc;
use super::file::{collect_source_files_and_hash, language_label, parse_file, rel_path};
use crate::cache::ParseCache;
use crate::spill::SpillIndex;
use crate::stats::ParseStats;
use libeulix::struc::kb_struct::{
    CallGraph, DependencyGraph, ExternalDependency, Indices, KnowledgeBase, Metadata, PatternInfo,
};
use rayon::prelude::*;
use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};

#[allow(clippy::too_many_arguments)]
pub fn parse_directory(
    dir: &str,
    languages: &str,
    euignore_path: Option<&str>,
    verbose: bool,
    version: &str,
    git_hash: &str,
    write_dir: &Path,
    engine: &crate::engine::Engine,
) -> Result<(KnowledgeBase, ParseStats, SpillIndex), Box<dyn std::error::Error>> {
    let path = PathBuf::from(dir);
    let euignore = euignore_path.map(PathBuf::from).or_else(|| {
        let default_path = path.join(".euignore");
        default_path.exists().then_some(default_path)
    });
    if verbose {
        if let Some(ref p) = euignore {
            println!("   [!] Using .euignore: {:?}", p);
        }
    }

    let (files, project_hash) = collect_source_files_and_hash(
        &path,
        languages,
        euignore.as_deref(),
        verbose,
        write_dir,
        engine.threads(),
    )?;

    if verbose {
        println!("      • Discovered {} source files", files.len());
    }

    let cache = ParseCache::open(write_dir.join("parse_cache"), version, git_hash)?;
    let spill = cache.spill("emit")?;
    let slim_spill = cache.spill("slim")?;

    let vec_memory_bytes = files.capacity() * std::mem::size_of::<PathBuf>();
    if verbose {
        println!(
            "      • Memory allocated for the PathBuf vector: ~{} bytes",
            vec_memory_bytes
        );
    }

    let pb = if verbose {
        let pb = ProgressBar::new(files.len() as u64);
        let style = ProgressStyle::default_bar()
            .template(
                "{spinner:.green} [{elapsed_precise}] [{bar:40.cyan/blue}] {pos}/{len} ({eta})",
            )?
            .progress_chars("#>-");
        pb.set_style(style);
        Some(pb)
    } else {
        None
    };

    #[cfg(target_os = "linux")]
    if files.len() > 1 {
        let prefetch_limit = files.len().min(1000);
        for p in &files[0..prefetch_limit] {
            use crate::os_io;
            os_io::prefetch(p);
        }
    }

    // Thread count comes from the engine's pool, not the global one.
    let num_threads = engine.threads().max(1);
    let chunk_size = (files.len() / (num_threads * 8)).clamp(16, 200);

    // The whole pipeline runs inside `engine.pool().install(...)` so that
    // rayon drives the work on the engine's threads, not on a global pool
    // that may not exist.
    let acc = engine.pool().install(|| {
        files
            .par_chunks(chunk_size)
            .fold(Acc::default, |mut acc: Acc, chunk: &[PathBuf]| {
                for file_path in chunk {
                    match parse_file(file_path, &path, &spill, &slim_spill, &cache) {
                        Ok((rel, file_data, cached, reused)) => {
                            if let Some(ref pb) = pb {
                                pb.inc(1);
                            }
                            acc.reused += reused as usize;
                            acc.record_ok(rel, file_data, cached);
                        }
                        Err(e) => {
                            let rel = rel_path(file_path, &path);
                            let reason = e.to_string();
                            if let Some(ref pb) = pb {
                                pb.println(format!("   ✗ Failed: {rel} - {reason}"));
                                pb.inc(1);
                            }
                            acc.record_failed(rel, language_label(file_path), reason);
                        }
                    }
                }
                acc
            })
            .reduce(Acc::default, Acc::merge)
    });

    spill.flush()?;
    slim_spill.flush()?;

    let Acc {
        structure,
        loc: total_loc,
        functions: total_functions,
        classes: total_classes,
        methods: total_methods,
        languages: languages_set,
        stats: mut final_stats,
        mut index,
        raw_imports,
        cache: cached,
        reused,
    } = acc;
    cache.commit(cached.into_iter().collect())?;

    // Aggregate raw import tuples into ExternalDependency — mirrors the
    // logic in dependencies.rs but runs here so we don't need kb.structure
    // to be populated during analyze_and_build.
    let external_dependencies = {
        let mut deps_map: BTreeMap<String, (BTreeSet<String>, String)> = BTreeMap::new();
        for (module, filepath, import_type) in raw_imports {
            let entry = deps_map
                .entry(module)
                .or_insert_with(|| (BTreeSet::new(), import_type));
            entry.0.insert(filepath);
        }
        deps_map
            .into_iter()
            .map(|(name, (files, import_type))| ExternalDependency {
                name,
                version: None,
                source: import_type,
                import_count: files.len(),
                used_by: files.into_iter().collect(),
            })
            .collect::<Vec<_>>()
    };

    index.sort_unstable_by(|a, b| a.0.cmp(&b.0));

    // Chunk/merge order is not meaningful; make output deterministic.
    final_stats
        .partial
        .sort_unstable_by(|a, b| a.path.cmp(&b.path));
    final_stats
        .failed
        .sort_unstable_by(|a, b| a.path.cmp(&b.path));

    let project_name = path
        .canonicalize()
        .ok()
        .and_then(|p| p.file_name().map(|n| n.to_string_lossy().into_owned()))
        .or_else(|| {
            std::env::current_dir()
                .ok()
                .and_then(|p| p.file_name().map(|n| n.to_string_lossy().into_owned()))
        })
        .unwrap_or_else(|| "unknown".to_string());

    let metadata = Metadata {
        version: version.to_string(),
        git_hash: git_hash.to_string(),
        project_name,
        project_hash: project_hash.to_string(),
        parsed_at: chrono::Utc::now().format("%Y.%m.%d.%H%M%S").to_string(),
        languages: languages_set.into_iter().collect(),
        total_files: index.len(),
        total_loc,
        total_functions,
        total_classes,
        total_methods,
    };

    let kb = KnowledgeBase {
        metadata,
        structure: structure.into_iter().collect(),
        call_graph: CallGraph::default(),
        dependency_graph: DependencyGraph::default(),
        indices: Indices::default(),
        entry_points: vec![],
        external_dependencies,
        patterns: PatternInfo::default(),
    };

    if let Some(pb) = pb {
        pb.finish_with_message("Parse complete!");
    }
    if verbose {
        println!("      • Reused {} cached files", reused);
    }

    Ok((
        kb,
        final_stats,
        SpillIndex {
            path: spill.path().to_path_buf(),
            entries: index,
        },
    ))
}
