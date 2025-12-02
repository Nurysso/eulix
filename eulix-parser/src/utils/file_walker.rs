use anyhow::Result;
use ignore::WalkBuilder;
use std::path::{Path, PathBuf};

pub struct FileWalker {
    root: PathBuf,
}

impl FileWalker {
    pub fn new(root: PathBuf) -> Self {
        Self { root }
    }

    /// Generic walker that respects .euignore for any file extension
    pub fn walk_files<F>(&self, filter: F) -> Result<Vec<PathBuf>>
    where
        F: Fn(&Path) -> bool,
    {
        let mut builder = WalkBuilder::new(&self.root);

        // Only use .euignore, completely ignore .gitignore
        builder.add_custom_ignore_filename(".euignore");

        // Disable all gitignore support
        builder.git_ignore(false);
        builder.git_global(false);
        builder.git_exclude(false);

        // Standard ignored directories
        let ignored_dirs = [
            ".git", ".eulix", "__pycache__",
            ".venv", "venv", "env", ".env",
            "node_modules", ".pytest_cache",
            ".mypy_cache", ".tox", "dist", "build",
            ".eggs", ".ipynb_checkpoints", "target"
        ];

        builder.filter_entry(move |entry| {
            let path = entry.path();
            let name = path.file_name()
                .and_then(|n| n.to_str())
                .unwrap_or("");

            let is_dir = entry.file_type()
                .map(|ft| ft.is_dir())
                .unwrap_or(false);

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

        let files: Vec<PathBuf> = builder
            .build()
            .filter_map(|entry| entry.ok())
            .filter(|entry| {
                entry.file_type().map(|ft| ft.is_file()).unwrap_or(false)
            })
            .filter(|entry| filter(entry.path()))
            .map(|entry| entry.path().to_path_buf())
            .collect();

        Ok(files)
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

        // Create directory structure
        fs::create_dir_all(root.join("src"))?;
        fs::create_dir_all(root.join("tests"))?;
        fs::create_dir_all(root.join("docs"))?;

        // Create files
        fs::write(root.join("src/main.py"), "# main")?;
        fs::write(root.join("tests/test_main.py"), "# test")?;
        fs::write(root.join("docs/guide.py"), "# docs")?;

        // Create .euignore
        fs::write(
            root.join(".euignore"),
            "tests/\ndocs/\n"
        )?;

        let walker = FileWalker::new(root.to_path_buf());

        // Should only find src/main.py
        assert_eq!(files.len(), 1);
        assert!(files[0].ends_with("src/main.py"));

        Ok(())
    }
}
