// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use indicatif::{ProgressBar, ProgressStyle};

use super::acc::Acc;
use super::file::{collect_source_files_and_hash, language_label, parse_file, rel_path};
use crate::spill::{Entry, Spill, SpillGuard, SpillIndex};
use crate::stats::ParseStats;
use libeulix::struc::kb_struct::{
    CallGraph, DependencyGraph, ExternalDependency, Indices, KnowledgeBase, Metadata, PatternInfo,
};
use rayon::prelude::*;
use rustc_hash::FxHashSet;
use std::collections::{HashMap, HashSet};
use std::fs;
use std::path::{Path, PathBuf};

#[derive(serde::Deserialize)]
struct SpilledClass {
    #[serde(default)]
    methods: Vec<serde::de::IgnoredAny>,
}

#[derive(serde::Deserialize)]
struct SpilledFunction {
    #[serde(default)]
    name: String,
}

#[derive(serde::Deserialize)]
struct SpilledFile {
    language: String,
    loc: usize,
    #[serde(default)]
    functions: Vec<SpilledFunction>,
    #[serde(default)]
    classes: Vec<SpilledClass>,
}

#[allow(clippy::too_many_arguments)]
pub fn parse_directory(
    dir: &str,
    languages: &str,
    euignore_path: Option<&str>,
    verbose: bool,
    version: &str,
    git_hash: &str,
    write_dir: &Path,
    resume: bool,
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

    let (files, project_hash) =
        collect_source_files_and_hash(&path, languages, euignore.as_deref(), verbose, write_dir)?;

    if verbose {
        println!("      • Discovered {} source files", files.len());
    }

    // Spill setup: fresh or resumed
    fs::create_dir_all(write_dir)?;
    let spill_path = write_dir.join(".kb_spill.tmp");

    let (spill, already_done): (Spill, Vec<Entry>) = if resume {
        let (s, prior) = Spill::open_or_resume(spill_path.clone())?;
        if verbose {
            if prior.is_empty() {
                println!("   ↻ Resume requested, but no usable spill found — starting fresh");
            } else {
                println!("   ↻ Resuming: {} files already in spill", prior.len());
            }
        }
        (s, prior)
    } else {
        // No resume: blow away any stale spill and start clean.
        let _ = fs::remove_file(&spill_path);
        (Spill::create(spill_path.clone())?, Vec::new())
    };

    // Files whose path is already in the valid prefix of the spill are skipped.
    let discovered: FxHashSet<String> = files.iter().map(|p| rel_path(p, &path)).collect();
    let already_done: Vec<Entry> = already_done
        .into_iter()
        .filter(|e| discovered.contains(&e.path))
        .collect();
    let done: FxHashSet<String> = already_done.iter().map(|e| e.path.clone()).collect();
    let to_parse: Vec<PathBuf> = files
        .into_iter()
        .filter(|p| !done.contains(&rel_path(p, &path)))
        .collect();
    if verbose {
        println!(
            "      • Number of source files to process: {} ({} skipped via resume)",
            to_parse.len(),
            done.len()
        );
    }
    let vec_memory_bytes = to_parse.capacity() * std::mem::size_of::<PathBuf>();
    if verbose {
        println!(
            "      • Memory allocated for the PathBuf vector: ~{} bytes",
            vec_memory_bytes
        );
    }

    //  Progress bar reflects only the work we're actually doing
    let pb = if verbose {
        let pb = ProgressBar::new(to_parse.len() as u64);
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
    if to_parse.len() > 1 {
        let prefetch_limit = to_parse.len().min(1000);
        for p in &to_parse[0..prefetch_limit] {
            use crate::os_io;
            os_io::prefetch(p);
        }
    }

    // Thread count comes from the engine's pool, not the global one.
    let num_threads = engine.threads().max(1);
    let chunk_size = (to_parse.len() / (num_threads * 8)).clamp(16, 200);

    // The whole pipeline runs inside `engine.pool().install(...)` so that
    // rayon drives the work on the engine's threads, not on a global pool
    // that may not exist.
    let acc = engine.pool().install(|| {
        to_parse
            .par_chunks(chunk_size)
            .fold(Acc::default, |mut acc: Acc, chunk: &[PathBuf]| {
                for file_path in chunk {
                    match parse_file(file_path, &path, &spill) {
                        Ok((rel, file_data, loc, raw_imports)) => {
                            if let Some(ref pb) = pb {
                                pb.inc(1);
                            }
                            acc.record_ok(rel, file_data, loc, raw_imports);
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

    let Acc {
        structure,
        loc: mut total_loc,
        functions: mut total_functions,
        classes: mut total_classes,
        methods: mut total_methods,
        languages: mut languages_set,
        stats: mut final_stats,
        mut index,
        raw_imports,
    } = acc;

    // Aggregate raw import tuples into ExternalDependency — mirrors the
    // logic in dependencies.rs but runs here so we don't need kb.structure
    // to be populated during analyze_and_build.
    let external_dependencies = {
        let mut deps_map: HashMap<String, (HashSet<String>, String)> = HashMap::new();
        for (module, filepath, import_type) in raw_imports {
            let entry = deps_map
                .entry(module)
                .or_insert_with(|| (HashSet::new(), import_type));
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

    for entry in &already_done {
        let payload = spill.read_payload(entry)?;
        let sf: SpilledFile = serde_json::from_slice(&payload)?;
        total_loc += sf.loc;
        // Mirrors Acc::record_ok: a PHP file's `{main}` holds its top-level
        // code and isn't a declared function. The spill doesn't keep tags,
        // so match the synthetic name instead.
        total_functions += sf.functions.iter().filter(|f| f.name != "{main}").count();
        total_classes += sf.classes.len();
        total_methods += sf.classes.iter().map(|c| c.methods.len()).sum::<usize>();
        if !languages_set.contains(sf.language.as_str()) {
            languages_set.insert(sf.language.clone());
        }
        // `final_stats` stays limited to this run: the spill doesn't keep
        // the syntax health, so a resumed file can't be called clean.
    }

    // Merge prior entries (from resume) with newly written ones
    // Prior entries have offsets into the *same* spill file; nothing else to do.
    let mut merged_index: Vec<(String, u64, u32)> = already_done
        .iter()
        .map(|e| (e.path.clone(), e.offset, e.len))
        .chain(index.drain(..))
        .collect();
    merged_index.sort_unstable_by(|a, b| a.0.cmp(&b.0));

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
        total_files: merged_index.len(),
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

    // Guard lives in the returned SpillIndex, so it fires only after the
    // caller is done with the spill (write_kb_from_spill, etc.).
    let guard = SpillGuard::new(spill.path().to_path_buf());

    Ok((
        kb,
        final_stats,
        SpillIndex {
            path: spill.path().to_path_buf(),
            entries: merged_index,
            _guard: Some(guard),
        },
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engine::Engine;
    use tempfile::TempDir;

    #[test]
    fn resume_rehydrates_metadata_counts_and_languages_from_spill() {
        let repo = TempDir::new().unwrap();
        let out = TempDir::new().unwrap();
        fs::write(repo.path().join("a.py"), "def foo():\n    return 1\n").unwrap();
        fs::write(
            repo.path().join("b.py"),
            "class Bar:\n    def m1(self):\n        return 2\n    def m2(self):\n        return 3\n",
        )
        .unwrap();

        // `{main}` holds the top-level code; only `hello` is a declared function.
        fs::write(
            repo.path().join("d.php"),
            "<?php\nfunction hello() { return 1; }\nhello();\n",
        )
        .unwrap();

        let engine = Engine::with_threads(2).unwrap();
        let root_str = repo.path().to_string_lossy().to_string();

        // First run: keep the spill file alive across the drop by calling .keep() on the guard.
        let (kb_fresh, stats_fresh, mut spill_fresh) = parse_directory(
            &root_str,
            "all",
            None,
            false,
            "0.8.1",
            "test",
            out.path(),
            false,
            &engine,
        )
        .unwrap();
        if let Some(g) = spill_fresh._guard.take() {
            let _ = g.keep();
        }

        // Add one more file and resume from the existing spill.
        fs::write(repo.path().join("c.rs"), "pub fn baz() {}\n").unwrap();
        let (kb_resumed, stats_resumed, _spill_resumed) = parse_directory(
            &root_str,
            "all",
            None,
            false,
            "0.8.1",
            "test",
            out.path(),
            true,
            &engine,
        )
        .unwrap();

        assert_eq!(kb_fresh.metadata.total_files, 3);
        assert_eq!(kb_fresh.metadata.total_functions, 2);
        assert_eq!(kb_fresh.metadata.total_classes, 1);
        assert_eq!(kb_fresh.metadata.total_methods, 2);
        assert_eq!(stats_fresh.total(), 3);

        assert_eq!(kb_resumed.metadata.total_files, 4);
        assert_eq!(
            kb_resumed.metadata.total_loc,
            kb_fresh.metadata.total_loc + 1
        );
        assert_eq!(kb_resumed.metadata.total_functions, 3);
        assert_eq!(kb_resumed.metadata.total_classes, 1);
        assert_eq!(kb_resumed.metadata.total_methods, 2);
        assert!(kb_resumed
            .metadata
            .languages
            .contains(&"python".to_string()));
        assert!(kb_resumed.metadata.languages.contains(&"rust".to_string()));
        // Only c.rs was parsed in the resumed run.
        assert_eq!(stats_resumed.total(), 1);
    }

    #[test]
    fn resume_drops_spilled_files_that_no_longer_exist() {
        let repo = TempDir::new().unwrap();
        let out = TempDir::new().unwrap();
        fs::write(repo.path().join("a.py"), "def foo():\n    return 1\n").unwrap();
        fs::write(repo.path().join("b.py"), "def bar():\n    return 2\n").unwrap();

        let engine = Engine::with_threads(2).unwrap();
        let root_str = repo.path().to_string_lossy().to_string();
        let parse = |resume: bool| {
            parse_directory(
                &root_str,
                "all",
                None,
                false,
                "0.8.1",
                "test",
                out.path(),
                resume,
                &engine,
            )
            .unwrap()
        };

        let (_, _, mut spill_fresh) = parse(false);
        if let Some(g) = spill_fresh._guard.take() {
            let _ = g.keep();
        }

        fs::remove_file(repo.path().join("a.py")).unwrap();
        let (kb_resumed, _, spill_resumed) = parse(true);

        let paths: Vec<&str> = spill_resumed
            .entries
            .iter()
            .map(|(p, _, _)| p.as_str())
            .collect();
        assert_eq!(paths, vec!["b.py"]);
        assert_eq!(kb_resumed.metadata.total_files, 1);
        assert_eq!(kb_resumed.metadata.total_functions, 1);
    }
}
