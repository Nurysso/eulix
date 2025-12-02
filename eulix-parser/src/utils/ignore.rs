use std::fs;
use std::path::{Path, PathBuf};

/// Manages .euignore patterns (similar to .gitignore)
pub struct IgnoreFilter {
    patterns: Vec<IgnorePattern>,
    base_path: PathBuf,
}

#[derive(Debug, Clone)]
struct IgnorePattern {
    pattern: String,
    is_directory: bool,      // Ends with /
    is_anchored: bool,       // Starts with /
    is_negation: bool,       // Starts with !
}

impl IgnorePattern {
    fn from_str(s: &str) -> Self {
        let mut pattern = s.to_string();
        let is_negation = pattern.starts_with('!');
        if is_negation {
            pattern = pattern[1..].to_string();
        }

        let is_anchored = pattern.starts_with('/');
        if is_anchored {
            pattern = pattern[1..].to_string();
        }

        let is_directory = pattern.ends_with('/');
        if is_directory {
            pattern = pattern[..pattern.len()-1].to_string();
        }

        Self {
            pattern,
            is_directory,
            is_anchored,
            is_negation,
        }
    }

    fn matches(&self, path_str: &str, is_dir: bool) -> bool {
        // If pattern is for directories only, skip non-directories
        if self.is_directory && !is_dir {
            return false;
        }

        if self.is_anchored {
            // Anchored patterns match from root
            if self.is_directory {
                // For directory patterns, check if path starts with pattern
                path_str.starts_with(&self.pattern)
                    || path_str == self.pattern
            } else {
                // Exact match or as a component
                path_str == self.pattern
                    || path_str.starts_with(&format!("{}/", self.pattern))
            }
        } else {
            // Non-anchored patterns match anywhere
            let components: Vec<&str> = path_str.split('/').collect();

            if self.is_directory {
                // Match directory name anywhere in path
                components.iter().any(|&comp| comp == self.pattern)
            } else {
                // Match component or full path
                components.contains(&self.pattern.as_str())
                    || path_str.ends_with(&self.pattern)
            }
        }
    }
}
#[allow(dead_code)]
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
    fn load_patterns(ignore_path: &Path) -> Vec<IgnorePattern> {
        let mut patterns = Vec::new();

        // Default patterns (always ignored)
        let defaults = vec![
            ".git/",
            ".eulix/",
            "node_modules/",
            "__pycache__/",
            ".venv/",
            "venv/",
            "target/",
            "dist/",
            "build/",
            "*.pyc",
            "*.pyo",
            "*.so",
            "*.dylib",
            "*.exe",
            "*.log",
            ".DS_Store",
        ];

        for pattern_str in defaults {
            patterns.push(IgnorePattern::from_str(pattern_str));
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

                    patterns.push(IgnorePattern::from_str(line));
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
            Err(_) => path,
        };

        let path_str = rel_path.to_string_lossy().replace('\\', "/");
        let is_dir = path.is_dir();

        // Check against all patterns
        let mut ignored = false;
        for pattern in &self.patterns {
            if pattern.matches(&path_str, is_dir) {
                if pattern.is_negation {
                    ignored = false;
                } else {
                    ignored = true;
                }
            }
        }

        ignored
    }

    /// Check if directory should be ignored (including subdirectories)
    pub fn should_ignore_dir(&self, dir_path: &Path) -> bool {
        if self.should_ignore(dir_path) {
            return true;
        }

        // Check if any parent directory is ignored
        let mut current = dir_path;
        while let Some(parent) = current.parent() {
            if parent == self.base_path {
                break;
            }

            if self.should_ignore(parent) {
                return true;
            }
            current = parent;
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
    }

    #[test]
    fn test_pattern_parsing() {
        let pattern = IgnorePattern::from_str("/docs/");
        assert!(pattern.is_anchored);
        assert!(pattern.is_directory);
        assert_eq!(pattern.pattern, "docs");

        let pattern2 = IgnorePattern::from_str("test/");
        assert!(!pattern2.is_anchored);
        assert!(pattern2.is_directory);
        assert_eq!(pattern2.pattern, "test");
    }
}
