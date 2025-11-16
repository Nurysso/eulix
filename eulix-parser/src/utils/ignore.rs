// utils/ignore.rs

use glob::Pattern;
use std::fs;
use std::path::{Path, PathBuf};

/// Manages .euignore patterns (similar to .gitignore)
pub struct IgnoreFilter {
    patterns: Vec<Pattern>,
    base_path: PathBuf,
}

impl IgnoreFilter {
    /// Load .euignore from project root
    pub fn new(project_root: &Path) -> Self {
        let ignore_path = project_root.join(".euignore");
        let patterns = Self::load_patterns(&ignore_path);

        Self {
            patterns,
            base_path: project_root.to_path_buf(),
        }
    }

    /// Load patterns from .euignore file
    fn load_patterns(ignore_path: &Path) -> Vec<Pattern> {
        let mut patterns = Vec::new();

        // Default patterns (always ignored)
        let defaults = vec![
            ".git",
            ".eulix",
            "node_modules",
            "__pycache__",
            ".venv",
            "venv",
            "target",
            "dist",
            "build",
            "*.pyc",
            "*.pyo",
            "*.so",
            "*.dylib",
            "*.exe",
            "*.log",
            ".DS_Store",
        ];

        for pattern_str in defaults {
            if let Ok(pattern) = Pattern::new(pattern_str) {
                patterns.push(pattern);
            }
        }

        // Load user-defined patterns
        if ignore_path.exists() {
            if let Ok(content) = fs::read_to_string(ignore_path) {
                for line in content.lines() {
                    let line = line.trim();

                    // Skip empty lines and comments
                    if line.is_empty() || line.starts_with('#') {
                        continue;
                    }

                    if let Ok(pattern) = Pattern::new(line) {
                        patterns.push(pattern);
                    }
                }
            }
        }

        patterns
    }

    /// Check if a path should be ignored
    pub fn should_ignore(&self, path: &Path) -> bool {
        // Get relative path from base
        let rel_path = match path.strip_prefix(&self.base_path) {
            Ok(p) => p,
            Err(_) => path, // Fallback to absolute if strip fails
        };

        let path_str = rel_path.to_string_lossy();

        // Check against all patterns
        for pattern in &self.patterns {
            // Check full path
            if pattern.matches(&path_str) {
                return true;
            }

            // Check each component (for directory patterns)
            for component in rel_path.components() {
                if let Some(comp_str) = component.as_os_str().to_str() {
                    if pattern.matches(comp_str) {
                        return true;
                    }
                }
            }
        }

        false
    }

    /// Check if directory should be ignored (including subdirectories)
    pub fn should_ignore_dir(&self, dir_path: &Path) -> bool {
        if self.should_ignore(dir_path) {
            return true;
        }

        // Check if any parent directory is ignored
        let mut current = dir_path;
        while let Some(parent) = current.parent() {
            if self.should_ignore(parent) {
                return true;
            }
            current = parent;

            // Stop at base path
            if current == self.base_path {
                break;
            }
        }

        false
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    #[test]
    fn test_default_ignores() {
        let base = PathBuf::from("/tmp/test-project");
        let filter = IgnoreFilter::new(&base);

        assert!(filter.should_ignore(&base.join("node_modules")));
        assert!(filter.should_ignore(&base.join("__pycache__")));
        assert!(filter.should_ignore(&base.join(".git")));
        assert!(filter.should_ignore(&base.join("file.pyc")));

        assert!(!filter.should_ignore(&base.join("src/main.py")));
    }

    #[test]
    fn test_nested_ignores() {
        let base = PathBuf::from("/tmp/test-project");
        let filter = IgnoreFilter::new(&base);

        assert!(filter.should_ignore(&base.join("src/node_modules/package")));
        assert!(filter.should_ignore(&base.join("deep/nested/.git/objects")));
    }
}
