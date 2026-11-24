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

#[cfg(feature = "flatbuffers")]
use crate::fb_io;

/// Name of the FlatBuffers checksum baseline in the output directory.
pub const CHECKSUM_FILE: &str = "checksum.fb.zst";
/// Name of the JSON checksum baseline in the output directory.
pub const CHECKSUM_JSON_FILE: &str = "checksum.json.zst";

/// What `walk_and_project_hash*` leaves behind as the checksum baseline.
/// Whatever the choice, the baseline file of the other format is removed so
/// no reader finds one that no longer matches the tree. The default is
/// `FlatBuffers`, or `Json` when the `flatbuffers` feature is off.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ChecksumFormat {
    /// `checksum.fb.zst`: a zstd-compressed FlatBuffer (schema/checksum.fbs),
    /// the file eulix-cli reads. Needs the `flatbuffers` feature.
    #[cfg(feature = "flatbuffers")]
    FlatBuffers,
    /// `checksum.json.zst`: zstd-compressed JSON, the layout eulix wrote
    /// before the FlatBuffers move.
    Json,
    /// No baseline and no hashing: files are not even stat'ed, and the
    /// returned project hash is empty.
    None,
}

impl Default for ChecksumFormat {
    fn default() -> Self {
        #[cfg(feature = "flatbuffers")]
        return Self::FlatBuffers;
        #[cfg(not(feature = "flatbuffers"))]
        return Self::Json;
    }
}

impl ChecksumFormat {
    /// The baseline's file name, or `None` when nothing is written.
    pub fn file_name(self) -> Option<&'static str> {
        match self {
            #[cfg(feature = "flatbuffers")]
            Self::FlatBuffers => Some(CHECKSUM_FILE),
            Self::Json => Some(CHECKSUM_JSON_FILE),
            Self::None => None,
        }
    }
}

impl std::str::FromStr for ChecksumFormat {
    type Err = String;

    fn from_str(s: &str) -> std::result::Result<Self, Self::Err> {
        match s.to_ascii_lowercase().as_str() {
            #[cfg(feature = "flatbuffers")]
            "fb" | "flatbuffers" => Ok(Self::FlatBuffers),
            #[cfg(not(feature = "flatbuffers"))]
            "fb" | "flatbuffers" => Err("checksum format 'fb' needs libeulix's flatbuffers feature".into()),
            "json" => Ok(Self::Json),
            "none" | "off" => Ok(Self::None),
            other => Err(format!(
                "unknown checksum format '{other}' (expected fb, json or none)"
            )),
        }
    }
}

/// `checksum.json.zst`, field for field as eulix wrote it before FlatBuffers.
#[derive(Serialize)]
struct JsonChecksum<'a> {
    project_path: &'a str,
    total_files: usize,
    hash: &'a str,
    files: BTreeMap<&'a str, JsonFileEntry<'a>>,
    dirs: BTreeMap<String, JsonDirEntry>, // left empty, Go does a full walk
    last_analyzed: &'a str,               // RFC3339, parses into Go time.Time
    analysis_version: &'a str,
}

#[derive(Serialize)]
struct JsonFileEntry<'a> {
    hash: &'a str,
    size: i64,
    mod_time: i64,
}

#[derive(Serialize)]
struct JsonDirEntry {
    hash: String,
    mod_time: i64,
    files: Vec<String>,
    dirs: Vec<String>,
}

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
    /// Baseline written by `walk_and_project_hash*`.
    checksum: ChecksumFormat,
}

impl FileWalker {
    pub fn new(root: PathBuf) -> Self {
        Self {
            root,
            euignore_path: None,
            threads: std::thread::available_parallelism()
                .map(|n| n.get())
                .unwrap_or(4),
            checksum: ChecksumFormat::default(),
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

    /// Picks the checksum baseline `walk_and_project_hash*` writes
    /// (FlatBuffers by default). `ChecksumFormat::None` also skips hashing.
    pub fn with_checksum(mut self, format: ChecksumFormat) -> Self {
        self.checksum = format;
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
    /// With `ChecksumFormat::None` the project hash is empty and no file
    /// is stat'ed, so every match is returned.
    pub fn walk_and_project_hash_with_stats<F>(
        &self,
        filter: F,
        write_dir: &Path,
    ) -> Result<(Vec<PathBuf>, String, WalkStats)>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        if self.checksum == ChecksumFormat::None {
            remove_stale_baselines(write_dir, None)?;
            let (paths, stats) = self.walk_paths(filter)?;
            return Ok((paths, String::new(), stats));
        }
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

    /// Writes the baseline picked by `with_checksum` with every walked
    /// file's size and mtime, read back by eulix-cli. The file is replaced by rename, so a
    /// reader never sees a half-written baseline.
    fn write_checksum(
        &self,
        files: &[WalkedFile],
        root_hash: &str,
        write_dir: &Path,
    ) -> Result<()> {
        let Some(name) = self.checksum.file_name() else {
            return remove_stale_baselines(write_dir, None);
        };
        let mut rows: Vec<(String, String, i64, i64)> = files
            .iter()
            .map(|f| {
                (
                    slash_path(&f.rel),
                    format!("{:016x}", f.meta_hash),
                    f.size,
                    f.mod_time,
                )
            })
            .collect();
        // By path string, which the FlatBuffer's key lookup needs (not the
        // component order `hash_files` sorted `files` in).
        rows.sort_unstable_by(|a, b| a.0.cmp(&b.0));
        let project_path = self.root.to_string_lossy();
        let last_analyzed = chrono::Utc::now().to_rfc3339();
        let version = env!("CARGO_PKG_VERSION");

        let raw = match self.checksum {
            #[cfg(feature = "flatbuffers")]
            ChecksumFormat::FlatBuffers => {
                let entries: Vec<fb_io::ChecksumEntry<'_>> = rows
                    .iter()
                    .map(|(path, hash, size, mod_time)| fb_io::ChecksumEntry {
                        path,
                        hash,
                        size: *size,
                        mod_time: *mod_time,
                    })
                    .collect();
                fb_io::encode_checksum(&project_path, root_hash, &entries, &last_analyzed, version)?
            }
            ChecksumFormat::Json => {
                let files: BTreeMap<&str, JsonFileEntry<'_>> = rows
                    .iter()
                    .map(|(path, hash, size, mod_time)| {
                        let entry = JsonFileEntry {
                            hash,
                            size: *size,
                            mod_time: *mod_time,
                        };
                        (path.as_str(), entry)
                    })
                    .collect();
                serde_json::to_vec(&JsonChecksum {
                    project_path: &project_path,
                    total_files: files.len(),
                    hash: root_hash,
                    files,
                    dirs: BTreeMap::new(),
                    last_analyzed: &last_analyzed,
                    analysis_version: version,
                })?
            }
            ChecksumFormat::None => unreachable!("handled above"),
        };

        fs::create_dir_all(write_dir).context("failed to create .eulix directory")?;
        let compressed = zstd::encode_all(&raw[..], 3)?;
        let path = write_dir.join(name);
        let tmp = write_dir.join(format!("{name}.tmp"));
        fs::write(&tmp, compressed).context("failed to write checksum file")?;
        fs::rename(&tmp, &path).context("failed to replace checksum file")?;
        remove_stale_baselines(write_dir, Some(name))
    }
}

/// `rel` with `/` separators, so a baseline is the same on every platform
/// (eulix-cli keys files by `filepath.ToSlash`). Only Windows converts: on Unix
/// `\` is a legal file-name character.
fn slash_path(rel: &Path) -> String {
    let s = rel.to_string_lossy();
    if cfg!(windows) {
        s.replace('\\', "/")
    } else {
        s.into_owned()
    }
}

/// Removes every checksum baseline in `dir` except `keep`.
fn remove_stale_baselines(dir: &Path, keep: Option<&str>) -> Result<()> {
    for name in [CHECKSUM_FILE, CHECKSUM_JSON_FILE] {
        if Some(name) == keep {
            continue;
        }
        match fs::remove_file(dir.join(name)) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => {
                return Err(e).with_context(|| format!("failed to remove stale {name}"));
            }
            _ => {}
        }
    }
    Ok(())
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

    fn walk_with(
        format: ChecksumFormat,
        root: &Path,
        out: &Path,
    ) -> Result<(Vec<PathBuf>, String)> {
        FileWalker::new(root.to_path_buf())
            .with_checksum(format)
            .walk_and_project_hash(|p| p.extension().is_some_and(|e| e == "rs"), out)
    }

    #[cfg(feature = "flatbuffers")]
    #[test]
    fn test_checksum_formats() -> Result<()> {
        let temp_dir = TempDir::new()?;
        let root = temp_dir.path();
        fs::create_dir_all(root.join("a"))?;
        fs::write(root.join("a/b.rs"), "")?;
        fs::write(root.join("a.b.rs"), "")?;
        let out = root.join(".eulix");

        // FlatBuffers: entries in path-string order ("a.b.rs" < "a/b.rs").
        let (_, fb_hash) = walk_with(ChecksumFormat::FlatBuffers, root, &out)?;
        let buf = zstd::decode_all(&fs::read(out.join(CHECKSUM_FILE))?[..])?;
        let v = fb_io::checksum_json(&buf)?;
        assert_eq!(v["hash"], fb_hash.as_str());
        let paths: Vec<_> = v["files"]
            .as_array()
            .unwrap()
            .iter()
            .map(|f| f["path"].clone())
            .collect();
        assert_eq!(paths, ["a.b.rs", "a/b.rs"]);

        // JSON: the pre-FlatBuffers layout; the .fb baseline is gone.
        let (_, json_hash) = walk_with(ChecksumFormat::Json, root, &out)?;
        assert_eq!(json_hash, fb_hash);
        assert!(!out.join(CHECKSUM_FILE).exists());
        let raw = zstd::decode_all(&fs::read(out.join(CHECKSUM_JSON_FILE))?[..])?;
        let v: serde_json::Value = serde_json::from_slice(&raw)?;
        assert_eq!(v["hash"], json_hash.as_str());
        assert_eq!(v["total_files"], 2);
        assert_eq!(v["files"]["a/b.rs"]["size"], 0);
        assert!(v["files"]["a/b.rs"]["hash"].as_str().unwrap().len() == 16);
        assert_eq!(v["dirs"], serde_json::json!({}));
        assert!(v["last_analyzed"].is_string() && v["analysis_version"].is_string());

        // None: no hash, no baseline, still every match.
        let (files, none_hash) = walk_with(ChecksumFormat::None, root, &out)?;
        assert_eq!(files.len(), 2);
        assert!(none_hash.is_empty());
        assert!(!out.join(CHECKSUM_FILE).exists() && !out.join(CHECKSUM_JSON_FILE).exists());
        Ok(())
    }

    #[cfg(not(feature = "flatbuffers"))]
    #[test]
    fn test_checksum_without_flatbuffers() -> Result<()> {
        let temp_dir = TempDir::new()?;
        let root = temp_dir.path();
        fs::write(root.join("a.rs"), "")?;
        let out = root.join(".eulix");
        assert_eq!(ChecksumFormat::default(), ChecksumFormat::Json);
        assert!("fb".parse::<ChecksumFormat>().is_err());
        let (_, hash) = walk_with(ChecksumFormat::default(), root, &out)?;
        let raw = zstd::decode_all(&fs::read(out.join(CHECKSUM_JSON_FILE))?[..])?;
        let v: serde_json::Value = serde_json::from_slice(&raw)?;
        assert_eq!(v["hash"], hash.as_str());
        Ok(())
    }

    #[test]
    fn test_checksum_format_from_str() {
        #[cfg(feature = "flatbuffers")]
        {
            assert_eq!("fb".parse(), Ok(ChecksumFormat::FlatBuffers));
            assert_eq!("FlatBuffers".parse(), Ok(ChecksumFormat::FlatBuffers));
        }
        assert_eq!("json".parse(), Ok(ChecksumFormat::Json));
        assert_eq!("none".parse(), Ok(ChecksumFormat::None));
        assert!("xml".parse::<ChecksumFormat>().is_err());
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
