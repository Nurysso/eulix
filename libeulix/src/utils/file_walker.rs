// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use anyhow::{Context, Result};
use ignore::{WalkBuilder, WalkState};
use serde::Serialize;
use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc;
use std::sync::Arc;
use std::time::UNIX_EPOCH;
use xxhash_rust::xxh3::{xxh3_64, Xxh3};

pub type Failed = Vec<(PathBuf, anyhow::Error)>;

pub struct WalkedFile {
    pub path: PathBuf,
    pub rel: PathBuf,
    pub meta_hash: u64,
    pub size: i64,
    pub mod_time: i64, // unix nanos, same as Go's UnixNano()
}

/// Statistics gathered during a single tree walk.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct WalkStats {
    /// Every regular file encountered (after ignore rules).
    pub scanned: usize,
    /// Files that passed the caller's filter.
    pub matched: usize,
}

impl std::fmt::Display for WalkStats {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "Scanned {} total file(s), matched {} file(s).",
            self.scanned, self.matched
        )
    }
}

pub struct FileWalker {
    /// Root of directory to be walked
    root: PathBuf,
    /// Optional path to a custom .euignore file (overrides the default ``<root>``/.euignore)
    euignore_path: Option<PathBuf>,
    threads: usize,
}

#[derive(Serialize)]
struct FileEntry {
    hash: String,
    size: i64,
    mod_time: i64,
}

#[derive(Serialize)]
struct DirEntry {
    hash: String,
    mod_time: i64,
    files: Vec<String>,
    dirs: Vec<String>,
}

#[derive(Serialize)]
struct Checksum {
    project_path: String,
    total_files: usize,
    hash: String,
    files: BTreeMap<String, FileEntry>,
    dirs: BTreeMap<String, DirEntry>, // left empty, Go does a full walk
    last_analyzed: String,            // RFC3339, parses into Go time.Time
    analysis_version: String,
}

impl FileWalker {
    pub fn new(root: PathBuf) -> Self {
        Self {
            root,
            euignore_path: None,
            threads: std::thread::available_parallelism()
                .map(|n| n.get())
                .unwrap_or(4),
        }
    }

    pub fn with_threads(mut self, n: usize) -> Self {
        self.threads = n;
        self
    }

    pub fn with_euignore(mut self, path: PathBuf) -> Self {
        self.euignore_path = Some(path);
        self
    }

    fn build_walker(&self) -> WalkBuilder {
        let mut builder = WalkBuilder::new(&self.root);
        if let Some(ref custom_ignore) = self.euignore_path {
            builder.add_ignore(custom_ignore);
        }
        builder.add_custom_ignore_filename(".euignore");
        builder.git_ignore(false);
        builder.git_global(false);
        builder.git_exclude(false);
        builder.threads(self.threads);

        let ignored_dirs = [
            ".git",
            ".eulix",
            "__pycache__",
            ".venv",
            "venv",
            "env",
            ".env",
            "node_modules",
            ".pytest_cache",
            ".mypy_cache",
            ".tox",
            "dist",
            "build",
            ".eggs",
            ".ipynb_checkpoints",
            "target",
        ];
        builder.filter_entry(move |entry| {
            let path = entry.path();
            let name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");
            let is_dir = entry.file_type().map(|ft| ft.is_dir()).unwrap_or(false);
            if is_dir {
                if ignored_dirs.contains(&name) {
                    return false;
                }
                if name.ends_with(".egg-info") {
                    return false;
                }
            }
            true
        });
        builder
    }

    // ========== Core walker ==========

    /// Single parallel traversal. `filter` is applied per entry; `process`
    /// runs on the worker thread for every path that passes the filter.
    /// Collects `WalkStats` for free. Paths whose `process` fails go to the
    /// returned `Failed` list instead of aborting the walk.
    fn walk_raw<F, T, P>(&self, filter: F, process: P) -> Result<(Vec<T>, Failed, WalkStats)>
    where
        F: Fn(&Path) -> bool + Sync,
        T: Send + 'static,
        P: Fn(&Path) -> Result<T> + Sync,
    {
        let builder = self.build_walker();
        let (tx, rx) = mpsc::channel::<std::result::Result<T, (PathBuf, anyhow::Error)>>();
        let total_scanned = Arc::new(AtomicUsize::new(0));
        let total_matched = Arc::new(AtomicUsize::new(0));

        builder.build_parallel().run(|| {
            let tx = tx.clone();
            let filter = &filter;
            let process = &process;
            let scanned = Arc::clone(&total_scanned);
            let matched = Arc::clone(&total_matched);
            Box::new(move |entry| {
                let entry = match entry {
                    Ok(e) => e,
                    Err(_) => return WalkState::Continue,
                };
                if entry.file_type().map(|ft| ft.is_file()).unwrap_or(false) {
                    scanned.fetch_add(1, Ordering::Relaxed);
                    let path = entry.path();
                    if filter(path) {
                        matched.fetch_add(1, Ordering::Relaxed);
                        let _ = tx.send(process(path).map_err(|e| (path.to_path_buf(), e)));
                    }
                }
                WalkState::Continue
            })
        });
        drop(tx);

        let mut files = Vec::with_capacity(30_000);
        let mut failed = Vec::new();
        for res in rx {
            match res {
                Ok(f) => files.push(f),
                Err(e) => failed.push(e),
            }
        }

        Ok((
            files,
            failed,
            WalkStats {
                scanned: total_scanned.load(Ordering::Relaxed),
                matched: total_matched.load(Ordering::Relaxed),
            },
        ))
    }

    // =========== Public: paths only (no reads, no hashes) ==========

    /// Walk the tree and return matching paths **without reading or hashing**
    /// any file. Use this when you only need the file list; it is much faster
    /// than `walk_files` used to be.
    pub fn walk_paths<F>(&self, filter: F) -> Result<(Vec<PathBuf>, WalkStats)>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        let (paths, _, stats) = self.walk_raw(filter, |p| Ok(p.to_path_buf()))?;
        Ok((paths, stats))
    }

    /// Backwards-compatible convenience. Returns just the paths.
    /// (Now implemented via `walk_paths`, so no more wasted hashing.)
    pub fn walk_files<F>(&self, filter: F) -> Result<Vec<PathBuf>>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        Ok(self.walk_paths(filter)?.0)
    }

    // =========== Public: paths + hashes ==========

    /// Walk + stat + hash the metadata. Internal building block for `project_hash`.
    fn walk_and_hash<F>(&self, filter: F) -> Result<(Vec<WalkedFile>, Failed, WalkStats)>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        let root = self.root.clone();
        self.walk_raw(filter, move |path| {
            let path = path.to_path_buf();
            let meta = fs::metadata(&path).with_context(|| {
                format!("failed to stat {} while walking project", path.display())
            })?;
            let rel = path.strip_prefix(&root).unwrap_or(&path).to_path_buf();
            let size = meta.len() as i64;
            let mod_time = meta
                .modified()
                .ok()
                .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
                .map(|d| d.as_nanos() as i64)
                .unwrap_or(0);
            Ok(WalkedFile {
                meta_hash: xxh3_64(&[size.to_le_bytes(), mod_time.to_le_bytes()].concat()),
                size,
                mod_time,
                path,
                rel,
            })
        })
    }

    fn hash_files(files: &mut [WalkedFile]) -> String {
        files.sort_unstable_by(|a, b| a.rel.cmp(&b.rel));
        let mut hasher = Xxh3::new();
        for f in files.iter() {
            hasher.update(f.rel.to_string_lossy().as_bytes());
            hasher.update(b"\0");
            hasher.update(&f.meta_hash.to_le_bytes());
        }
        format!("{:016x}", hasher.digest())
    }

    /// Discover matching source files AND compute the project hash
    /// in a single walk. Returns (matching source files, project hash).
    #[allow(dead_code)]
    pub fn project_hash(&self) -> Result<String> {
        Ok(self.project_hash_with_stats()?.0)
    }

    #[allow(dead_code)]
    pub fn project_hash_with_stats(&self) -> Result<(String, WalkStats)> {
        let (mut files, _, stats) = self.walk_and_hash(|_| true)?;
        Ok((Self::hash_files(&mut files), stats))
    }

    /// Discover matching source files AND compute the project hash in a
    /// single walk. Existing signature preserved.
    pub fn walk_and_project_hash<F>(
        &self,
        filter: F,
        write_dir: &Path,
    ) -> Result<(Vec<PathBuf>, String)>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        let (paths, hash, _stats) = self.walk_and_project_hash_with_stats(filter, write_dir)?;
        Ok((paths, hash))
    }

    /// Same as `walk_and_project_hash`, but also returns walk statistics.
    pub fn walk_and_project_hash_with_stats<F>(
        &self,
        filter: F,
        write_dir: &Path,
    ) -> Result<(Vec<PathBuf>, String, WalkStats)>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        let (mut files, failed, stats) = self.walk_and_hash(filter)?;
        let hash = Self::hash_files(&mut files);
        self.write_checksum(&files, &hash, write_dir)?;
        // Failed paths still go to the parser so it records them as failed.
        let paths = files
            .into_iter()
            .map(|f| f.path)
            .chain(failed.into_iter().map(|(p, _)| p))
            .collect();
        Ok((paths, hash, stats))
    }

    fn write_checksum(
        &self,
        files: &[WalkedFile],
        root_hash: &str,
        write_dir: &Path,
    ) -> Result<()> {
        let mut map = BTreeMap::new();
        for f in files {
            map.insert(
                f.rel.to_string_lossy().to_string(),
                FileEntry {
                    hash: format!("{:016x}", f.meta_hash),
                    size: f.size,
                    mod_time: f.mod_time,
                },
            );
        }

        let checksum = Checksum {
            project_path: self.root.to_string_lossy().to_string(),
            total_files: map.len(),
            hash: root_hash.to_string(),
            files: map,
            dirs: BTreeMap::new(),
            last_analyzed: chrono::Utc::now().to_rfc3339(),
            analysis_version: env!("CARGO_PKG_VERSION").to_string(),
        };

        fs::create_dir_all(write_dir).context("failed to create .eulix directory")?;
        let json = serde_json::to_vec(&checksum)?;
        let compressed = zstd::encode_all(&json[..], 3)?;
        fs::write(write_dir.join("checksum.json.zst"), compressed)
            .context("failed to write checksum file")?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use tempfile::TempDir;

    #[test]
    fn test_euignore_filtering() -> Result<()> {
        let temp_dir = TempDir::new()?;
        let root = temp_dir.path();
        fs::create_dir_all(root.join("src"))?;
        fs::create_dir_all(root.join("tests"))?;
        fs::create_dir_all(root.join("docs"))?;
        fs::write(root.join("src/main.py"), "# main")?;
        fs::write(root.join("tests/test_main.py"), "# test")?;
        fs::write(root.join("docs/guide.py"), "# docs")?;
        fs::write(root.join(".euignore"), "tests/\ndocs/\n")?;

        let walker = FileWalker::new(root.to_path_buf());
        let files = walker.walk_files(|p| p.extension().and_then(|e| e.to_str()) == Some("py"))?;

        assert_eq!(files.len(), 1);
        assert!(files[0].ends_with("src/main.py"));
        Ok(())
    }

    #[test]
    fn test_walk_and_project_hash() -> Result<()> {
        let temp_dir = TempDir::new()?;
        let root = temp_dir.path();

        fs::create_dir_all(root.join("src"))?;
        fs::write(root.join("src/lib.rs"), "pub fn hello() {}")?;

        let write_dir = temp_dir.path().join(".cache");
        fs::create_dir_all(&write_dir)?;

        let walker = FileWalker::new(root.to_path_buf());
        let (files, hash) = walker.walk_and_project_hash(
            |p| p.extension().and_then(|e| e.to_str()) == Some("rs"),
            &write_dir,
        )?;

        assert_eq!(files.len(), 1);
        assert!(files[0].ends_with("src/lib.rs"));
        assert!(!hash.is_empty());
        assert_eq!(hash.len(), 16);
        Ok(())
    }

    #[test]
    fn test_failed_files_do_not_abort_walk() -> Result<()> {
        let temp_dir = TempDir::new()?;
        let root = temp_dir.path();
        fs::write(root.join("ok.rs"), "fn ok() {}")?;
        fs::write(root.join("bad.rs"), "fn bad() {}")?;

        let walker = FileWalker::new(root.to_path_buf());
        let (ok, failed, stats) = walker.walk_raw(
            |_| true,
            |p| {
                if p.ends_with("bad.rs") {
                    anyhow::bail!("unreadable")
                }
                Ok(p.to_path_buf())
            },
        )?;
        assert_eq!(stats.matched, 2);
        assert!(ok.len() == 1 && ok[0].ends_with("ok.rs"));
        assert!(failed.len() == 1 && failed[0].0.ends_with("bad.rs"));

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(root.join("bad.rs"), fs::Permissions::from_mode(0o000))?;
            let (files, _) = walker.walk_and_project_hash(|_| true, &root.join(".cache"))?;
            assert!(files.iter().any(|p| p.ends_with("bad.rs")));
        }
        Ok(())
    }

    #[test]
    fn test_with_threads_limits_workers() -> Result<()> {
        let temp_dir = TempDir::new()?;
        let root = temp_dir.path();
        for i in 0..200 {
            fs::create_dir_all(root.join(format!("d{i}")))?;
            fs::write(root.join(format!("d{i}/f.rs")), "")?;
        }

        let walker = FileWalker::new(root.to_path_buf()).with_threads(2);
        let (ids, _, _) = walker.walk_raw(|_| true, |_| Ok(std::thread::current().id()))?;
        let distinct: std::collections::HashSet<_> = ids.into_iter().collect();
        assert_eq!(walker.threads, 2);
        assert!(distinct.len() <= 2, "used {} threads", distinct.len());
        Ok(())
    }

    #[test]
    fn test_project_hash_standalone() -> Result<()> {
        let temp_dir = TempDir::new()?;
        let root = temp_dir.path();
        fs::write(root.join("Cargo.toml"), "[package]")?;

        let walker = FileWalker::new(root.to_path_buf());
        let hash = walker.project_hash()?;

        assert!(!hash.is_empty());
        assert_eq!(hash.len(), 16);
        Ok(())
    }
}
