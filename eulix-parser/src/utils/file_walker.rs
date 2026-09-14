//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use anyhow::{Context, Result};
use ignore::{WalkBuilder, WalkState};
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc;
use std::sync::Arc;
use xxhash_rust::xxh3::{xxh3_64, Xxh3};

pub struct FileWalker {
    root: PathBuf,
    /// Optional path to a custom .euignore file (overrides the default <root>/.euignore)
    euignore_path: Option<PathBuf>,
}

pub struct WalkedFile {
    pub path: PathBuf,
    pub rel: PathBuf,
    pub content_hash: u64,
}

impl FileWalker {
    pub fn new(root: PathBuf) -> Self {
        Self {
            root,
            euignore_path: None,
        }
    }

    /// Use a custom .euignore path (from --euignore CLI flag) instead of the default
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
        builder.threads(
            std::thread::available_parallelism()
                .map(|n| n.get())
                .unwrap_or(4),
        );

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

    /// Single traversal: applies `filter` per entry, reads+hashes only the
    /// files that pass, and reports total files scanned. Both `walk_files`
    /// and `project_hash` build on this so the tree is only ever walked once.
    fn walk_and_hash<F>(&self, filter: F) -> Result<(Vec<WalkedFile>, usize)>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        let builder = self.build_walker();
        let root = self.root.clone();
        let (tx, rx) = mpsc::channel::<Result<WalkedFile>>();
        let total_scanned = Arc::new(AtomicUsize::new(0));

        builder.build_parallel().run(|| {
            let tx = tx.clone();
            let filter = &filter;
            let root = root.clone();
            let counter = Arc::clone(&total_scanned);
            Box::new(move |entry| {
                let entry = match entry {
                    Ok(e) => e,
                    Err(_) => return WalkState::Continue,
                };
                if entry.file_type().map(|ft| ft.is_file()).unwrap_or(false) {
                    counter.fetch_add(1, Ordering::Relaxed);
                    let path = entry.path();
                    if filter(path) {
                        let path = path.to_path_buf();
                        let result = fs::read(&path)
                            .with_context(|| {
                                format!("failed to read {} while walking project", path.display())
                            })
                            .map(|contents| {
                                let rel = path.strip_prefix(&root).unwrap_or(&path).to_path_buf();
                                WalkedFile {
                                    content_hash: xxh3_64(&contents),
                                    path,
                                    rel,
                                }
                            });
                        let _ = tx.send(result);
                    }
                }
                WalkState::Continue
            })
        });
        drop(tx);

        let mut files = Vec::with_capacity(30_000);
        for res in rx {
            files.push(res?);
        }
        let scanned = total_scanned.load(Ordering::Relaxed);
        Ok((files, scanned))
    }

    #[allow(dead_code)]
    pub fn walk_files<F>(&self, filter: F) -> Result<Vec<PathBuf>>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        let (files, scanned) = self.walk_and_hash(filter)?;
        println!(
            "      • Walker stats: Scanned {} total files, found {} matching source files.",
            scanned,
            files.len()
        );
        Ok(files.into_iter().map(|f| f.path).collect())
    }

    #[allow(dead_code)]
    pub fn project_hash(&self) -> Result<String> {
        let (mut files, _scanned) = self.walk_and_hash(|_| true)?;
        files.sort_unstable_by(|a, b| a.rel.cmp(&b.rel));

        let mut hasher = Xxh3::new();
        for f in &files {
            hasher.update(f.rel.to_string_lossy().as_bytes());
            hasher.update(b"\0");
            hasher.update(&f.content_hash.to_le_bytes());
        }
        Ok(format!("{:016x}", hasher.digest()))
    }

    /// Discover matching source files AND compute the project hash
    /// in a single walk. Returns (matching source files, project hash).
    pub fn walk_and_project_hash<F>(&self, filter: F) -> Result<(Vec<PathBuf>, String)>
    where
        F: Fn(&Path) -> bool + Sync,
    {
        let (mut files, scanned) = self.walk_and_hash(filter)?;
        println!(
            "      • Walker stats: Scanned {} total files, found {} matching source files.",
            scanned,
            files.len()
        );
        files.sort_unstable_by(|a, b| a.rel.cmp(&b.rel));

        let mut hasher = Xxh3::new();
        for f in &files {
            hasher.update(f.rel.to_string_lossy().as_bytes());
            hasher.update(b"\0");
            hasher.update(&f.content_hash.to_le_bytes());
        }
        let hash = format!("{:016x}", hasher.digest());
        let paths = files.into_iter().map(|f| f.path).collect();
        Ok((paths, hash))
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

        let walker = FileWalker::new(root.to_path_buf());
        let (files, hash) = walker
            .walk_and_project_hash(|p| p.extension().and_then(|e| e.to_str()) == Some("rs"))?;

        assert_eq!(files.len(), 1);
        assert!(files[0].ends_with("src/lib.rs"));
        assert!(!hash.is_empty());
        assert_eq!(hash.len(), 16); // Hex-encoded u64 xxh3 digest
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
