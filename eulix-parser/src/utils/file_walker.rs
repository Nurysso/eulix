use anyhow::Result;
use ignore::WalkBuilder;
use std::path::{PathBuf};

pub struct FileWalker {
    root: PathBuf,
    euignore: Option<PathBuf>,
}

impl FileWalker {
    pub fn new(root: PathBuf, euignore: Option<PathBuf>) -> Self {
        Self { root, euignore }
    }

    pub fn find_python_files(&self) -> Result<Vec<PathBuf>> {
        let mut builder = WalkBuilder::new(&self.root);

        // Respect .gitignore by default
        builder.git_ignore(true);
        builder.git_global(false);
        builder.git_exclude(false);

        // Add .euignore if provided
        if let Some(euignore_path) = &self.euignore {
            if euignore_path.exists() {
                builder.add_ignore(euignore_path);
            }
        }

        // Add standard ignores
        builder.filter_entry(|entry| {
            let path = entry.path();
            let name = path.file_name()
                .and_then(|n| n.to_str())
                .unwrap_or("");

            // Skip common directories
            if entry.file_type().map(|ft| ft.is_dir()).unwrap_or(false) {
                return !matches!(name,
                    ".git" | ".eulix" | "__pycache__" |
                    ".venv" | "venv" | "env" | ".env" |
                    "node_modules" | ".pytest_cache" |
                    ".mypy_cache" | ".tox" | "dist" | "build" |
                    ".eggs" | "*.egg-info" | ".ipynb_checkpoints"
                );
            }

            true
        });

        let python_files: Vec<PathBuf> = builder
            .build()
            .filter_map(|entry| entry.ok())
            .filter(|entry| {
                entry.file_type().map(|ft| ft.is_file()).unwrap_or(false)
            })
            .filter(|entry| {
                entry.path()
                    .extension()
                    .and_then(|ext| ext.to_str())
                    .map(|ext| ext == "py")
                    .unwrap_or(false)
            })
            .map(|entry| entry.path().to_path_buf())
            .collect();

        Ok(python_files)
    }
}
