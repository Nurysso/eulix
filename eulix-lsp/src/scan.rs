// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Workspace discovery and parallel parsing.
//!
//! We walk the tree ourselves instead of calling
//! `libeulix::utils::file_walker::FileWalker`, and it's worth saying why.
//! `FileWalker::walk_files` and `walk_and_project_hash` both call `println!`
//! unconditionally (`utils/file_walker.rs:193` and `:228`). In a language
//! server, stdout *is* the JSON-RPC channel. One progress line gets written
//! straight into the middle of a response frame and desyncs every message
//! after it. `libeulix`'s own `lib.rs` advertises stdout purity; these two
//! functions break it.
//!
//! The ignore rules below mirror the ones `FileWalker` applies, so the
//! workspace contents match what the CLI would have seen.

use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::Arc;

use ignore::WalkBuilder;

use crate::config::Config;
use crate::syntax::{self, ParsedFile};

// Collect indexable source files under `root`.
//
// Returns `(paths, truncated)`. `truncated` is set when the cap was hit, so
// the caller can tell the user the index is partial instead of implying the
// whole workspace is covered.
pub fn collect_source_files(root: &Path, cfg: &Config) -> (Vec<PathBuf>, bool) {
    let mut builder = WalkBuilder::new(root);
    // `.euignore` is eulix's own ignore file, so honour it.
    builder.add_custom_ignore_filename(".euignore");
    builder.git_ignore(true);
    builder.git_global(true);
    builder.git_exclude(true);
    builder.ignore(true);
    builder.parents(true);
    builder.hidden(false);
    builder.follow_links(false);
    builder.threads(cfg.scan_threads);

    let ignored: Vec<String> = cfg.ignore_dirs.clone();
    builder.filter_entry(move |entry| {
        if !entry.file_type().is_some_and(|t| t.is_dir()) {
            return true;
        }
        let Some(name) = entry.file_name().to_str() else {
            return true;
        };
        !ignored.iter().any(|d| d == name) && !name.ends_with(".egg-info")
    });

    let mut out = Vec::new();
    let mut truncated = false;

    for entry in builder.build().flatten() {
        if !entry.file_type().is_some_and(|t| t.is_file()) {
            continue;
        }
        let path = entry.path();
        if !syntax::is_supported(path) {
            continue;
        }
        // Skip oversized files here so the parse workers never see them.
        if std::fs::metadata(path)
            .map(|m| m.len() as usize)
            .unwrap_or(0)
            > cfg.max_file_bytes
        {
            continue;
        }
        if out.len() >= cfg.max_scan_files {
            truncated = true;
            break;
        }
        out.push(path.to_path_buf());
    }

    out.sort();
    (out, truncated)
}

// Parse every path in parallel on the blocking pool.
//
// `cancel` is polled between files so a superseded scan or a shutdown stops
// promptly instead of running to completion unseen. Files that fail to parse
// are skipped, not fatal: one unreadable file must not abort indexing of a
// whole repository.
pub async fn parse_all(
    paths: Vec<PathBuf>,
    cfg: Config,
    cancel: Arc<AtomicBool>,
    on_progress: impl Fn(usize) + Send + Sync + 'static,
) -> (Vec<(PathBuf, Arc<ParsedFile>)>, Vec<(PathBuf, String)>) {
    let total = paths.len();
    if total == 0 {
        return (Vec::new(), Vec::new());
    }

    let threads = cfg.scan_threads.max(1).min(total);
    // Chunk round-robin rather than contiguously: it keeps any single chunk
    // from being all huge files, which is what would strand one thread while
    // the others finish.
    let mut chunks: Vec<Vec<PathBuf>> = (0..threads).map(|_| Vec::new()).collect();
    for (i, p) in paths.into_iter().enumerate() {
        chunks[i % threads].push(p);
    }

    let done = Arc::new(AtomicUsize::new(0));
    let on_progress = Arc::new(on_progress);

    let mut handles = Vec::with_capacity(threads);
    for chunk in chunks {
        if chunk.is_empty() {
            continue;
        }
        let done = done.clone();
        let on_progress = on_progress.clone();
        let cancel = cancel.clone();
        let cfg = cfg.clone();
        handles.push(tokio::task::spawn_blocking(move || {
            let mut ok = Vec::new();
            let mut failed = Vec::new();
            for path in chunk {
                if cancel.load(Ordering::Relaxed) {
                    break;
                }
                match read_and_parse(&path, &cfg) {
                    Ok(parsed) => ok.push((path, Arc::new(parsed))),
                    Err(e) => failed.push((path, e)),
                }
                let n = done.fetch_add(1, Ordering::Relaxed) + 1;
                // Report every 64 files so progress updates don't dominate
                // the scan itself.
                if n % 64 == 0 || n == total {
                    on_progress(n);
                }
            }
            (ok, failed)
        }));
    }

    let mut ok = Vec::new();
    let mut failed = Vec::new();
    for h in handles {
        match h.await {
            Ok((o, f)) => {
                ok.extend(o);
                failed.extend(f);
            }
            Err(e) => tracing::error!(error = %e, "scan worker panicked"),
        }
    }
    (ok, failed)
}

fn read_and_parse(path: &Path, cfg: &Config) -> Result<ParsedFile, String> {
    let meta = std::fs::metadata(path).map_err(|e| format!("stat: {e}"))?;
    if meta.len() as usize > cfg.max_file_bytes {
        return Err("too large".into());
    }
    let bytes = std::fs::read(path).map_err(|e| format!("read: {e}"))?;
    let text = String::from_utf8_lossy(&bytes);
    syntax::parse(path, &text).map_err(|e| e.0)
}

// Read and parse a single file, for the open/save path.
pub fn parse_file(path: &Path, text: &str, cfg: &Config) -> Result<ParsedFile, String> {
    if text.len() > cfg.max_file_bytes {
        return Err(format!(
            "file is {} bytes, above the {} byte limit",
            text.len(),
            cfg.max_file_bytes
        ));
    }
    syntax::parse(path, text).map_err(|e| e.0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    fn cfg() -> Config {
        Config::default()
    }

    #[test]
    fn collects_supported_files_and_skips_the_rest() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        fs::write(root.join("a.rs"), "fn a() {}").unwrap();
        fs::write(root.join("b.py"), "def b(): pass").unwrap();
        fs::write(root.join("c.md"), "# not code").unwrap();
        fs::create_dir(root.join("target")).unwrap();
        fs::write(root.join("target/dep.rs"), "fn d() {}").unwrap();

        let (files, truncated) = collect_source_files(root, &cfg());
        let names: Vec<_> = files
            .iter()
            .map(|p| p.file_name().unwrap().to_string_lossy().to_string())
            .collect();
        assert!(names.contains(&"a.rs".to_string()));
        assert!(names.contains(&"b.py".to_string()));
        assert!(!names.contains(&"c.md".to_string()));
        assert!(
            !names.contains(&"dep.rs".to_string()),
            "target/ should be skipped"
        );
        assert!(!truncated);
    }

    #[test]
    fn reports_truncation_rather_than_silently_capping() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        for i in 0..10 {
            fs::write(root.join(format!("f{i}.rs")), "fn f() {}").unwrap();
        }
        let mut c = cfg();
        c.max_scan_files = 3;
        let (files, truncated) = collect_source_files(root, &c);
        assert_eq!(files.len(), 3);
        assert!(
            truncated,
            "caller must be able to tell the index is partial"
        );
    }

    #[tokio::test]
    async fn parses_in_parallel_and_reports_failures_without_aborting() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        fs::write(root.join("good.rs"), "fn alpha() {}").unwrap();
        fs::write(root.join("bad.jsx"), "export default function App() {}").unwrap();

        let paths = vec![root.join("good.rs"), root.join("bad.jsx")];
        let cancel = Arc::new(AtomicBool::new(false));
        let (ok, failed) = parse_all(paths, cfg(), cancel, |_| {}).await;

        assert_eq!(ok.len(), 1, "one good file must still index");
        assert_eq!(failed.len(), 1, "one bad file must not abort the batch");
    }

    #[tokio::test]
    async fn cancellation_stops_early() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        for i in 0..200 {
            fs::write(root.join(format!("f{i}.rs")), "fn f() {}").unwrap();
        }
        let mut paths: Vec<PathBuf> = (0..200).map(|i| root.join(format!("f{i}.rs"))).collect();
        paths.sort();
        let cancel = Arc::new(AtomicBool::new(true)); // already cancelled
        let (ok, _) = parse_all(paths, cfg(), cancel, |_| {}).await;
        assert!(ok.is_empty(), "a pre-cancelled scan must do no work");
    }

    #[test]
    fn euignore_is_honoured() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        fs::create_dir(root.join("gen")).unwrap();
        fs::write(root.join("gen/out.rs"), "fn g() {}").unwrap();
        fs::write(root.join("keep.rs"), "fn k() {}").unwrap();
        fs::write(root.join(".euignore"), "gen/\n").unwrap();

        let (files, _) = collect_source_files(root, &cfg());
        let names: Vec<_> = files
            .iter()
            .map(|p| p.file_name().unwrap().to_string_lossy().to_string())
            .collect();
        assert!(names.contains(&"keep.rs".to_string()));
        assert!(
            !names.contains(&"out.rs".to_string()),
            ".euignore not honoured"
        );
    }
}
