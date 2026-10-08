use indicatif::{ProgressBar, ProgressStyle};

use super::acc::Acc;
use super::file::{collect_source_files_and_hash, language_label, parse_file, rel_path};
use crate::spill::{Entry, Spill, SpillGuard, SpillIndex};
use crate::stats::ParseStats;
use crate::struc::kb_struct::{
    CallGraph, DependencyGraph, Indices, KnowledgeBase, Metadata, PatternInfo,
};
use rayon::prelude::*;
use rustc_hash::FxHashSet;
use std::fs;
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
    resume: bool,
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
        println!("    Discovered {} source files", files.len());
        println!();
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
    let done: FxHashSet<String> = already_done.iter().map(|e| e.path.clone()).collect();
    let to_parse: Vec<PathBuf> = files
        .into_iter()
        .filter(|p| !done.contains(&rel_path(p, &path)))
        .collect();

    println!(
        "      • Number of source files to process: {} ({} skipped via resume)",
        to_parse.len(),
        done.len()
    );
    let vec_memory_bytes = to_parse.capacity() * std::mem::size_of::<PathBuf>();
    println!(
        "      • Memory allocated for the PathBuf vector: ~{} bytes",
        vec_memory_bytes
    );

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

    let num_threads = rayon::current_num_threads().max(1);
    let chunk_size = (to_parse.len() / (num_threads * 8)).clamp(16, 200);

    let acc = to_parse
        .par_chunks(chunk_size)
        .fold(Acc::default, |mut acc: Acc, chunk: &[PathBuf]| {
            for file_path in chunk {
                match parse_file(file_path, &path, &spill) {
                    Ok((rel, file_data, loc)) => {
                        if let Some(ref pb) = pb {
                            pb.inc(1);
                        }
                        acc.record_ok(rel, file_data, loc);
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
        .reduce(Acc::default, Acc::merge);

    spill.flush()?;

    let Acc {
        structure,
        loc: total_loc,
        functions: total_functions,
        classes: total_classes,
        methods: total_methods,
        languages: languages_set,
        stats: mut final_stats,
        mut index,
    } = acc;

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
        total_files: structure.len(),
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
        external_dependencies: vec![],
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
