use std::fs;
use std::path::Path;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Language {
    Python,
    JavaScript,
    TypeScript,
    Go,
    Rust,
    C,
    Cpp,
    Unknown,
}

impl Language {
    /// Detect language from file path and optionally content
    pub fn detect(path: &Path) -> Self {
        // 1. Try extension first (fastest)
        if let Some(ext) = path.extension() {
            if let Some(ext_str) = ext.to_str() {
                if let Some(lang) = Self::from_extension(ext_str) {
                    return lang;
                }
            }
        }

        // 2. Try filename patterns
        if let Some(filename) = path.file_name() {
            if let Some(name_str) = filename.to_str() {
                if let Some(lang) = Self::from_filename(name_str) {
                    return lang;
                }
            }
        }

        // 3. Try reading shebang
        if let Ok(content) = fs::read_to_string(path) {
            if let Some(lang) = Self::from_shebang(&content) {
                return lang;
            }

            // 4. Last resort: content analysis
            return Self::from_content(&content);
        }

        Language::Unknown
    }

    /// Detect from file extension
    fn from_extension(ext: &str) -> Option<Self> {
        match ext.to_lowercase().as_str() {
            "py" | "pyw" | "pyi" => Some(Language::Python),
            "js" | "jsx" | "mjs" | "cjs" => Some(Language::JavaScript),
            "ts" | "tsx" => Some(Language::TypeScript),
            "go" => Some(Language::Go),
            "rs" => Some(Language::Rust),
            "c" | "h" => Some(Language::C),
            "cpp" | "cc" | "cxx" | "hpp" | "hxx" => Some(Language::Cpp),
            _ => None,
        }
    }

    /// Detect from filename patterns
    fn from_filename(filename: &str) -> Option<Self> {
        match filename {
            "Makefile" | "GNUmakefile" => Some(Language::C),
            "go.mod" | "go.sum" => Some(Language::Go),
            "Cargo.toml" | "Cargo.lock" => Some(Language::Rust),
            _ => None,
        }
    }

    /// Detect from shebang line
    fn from_shebang(content: &str) -> Option<Self> {
        let first_line = content.lines().next()?;

        if !first_line.starts_with("#!") {
            return None;
        }

        let shebang = first_line.to_lowercase();

        if shebang.contains("python") {
            Some(Language::Python)
        } else if shebang.contains("node") || shebang.contains("js") {
            Some(Language::JavaScript)
        } else {
            None
        }
    }

    /// Detect from content analysis (heuristic)
    fn from_content(content: &str) -> Self {
        let content_lower = content.to_lowercase();
        let lines: Vec<&str> = content.lines().take(50).collect(); // Check first 50 lines

        // Python indicators
        if lines.iter().any(|l| {
            l.contains("def ") ||
            l.contains("import ") ||
            l.contains("from ") ||
            l.trim_start().starts_with("class ")
        }) {
            return Language::Python;
        }

        // JavaScript/TypeScript indicators
        if lines.iter().any(|l| {
            l.contains("const ") ||
            l.contains("let ") ||
            l.contains("var ") ||
            l.contains("function ") ||
            l.contains("=>")
        }) {
            // Check for TypeScript-specific syntax
            if content_lower.contains("interface ") ||
               content_lower.contains(": string") ||
               content_lower.contains(": number") {
                return Language::TypeScript;
            }
            return Language::JavaScript;
        }

        // Go indicators
        if lines.iter().any(|l| {
            l.contains("package ") ||
            l.contains("func ") ||
            l.contains("import (")
        }) {
            return Language::Go;
        }

        // Rust indicators
        if lines.iter().any(|l| {
            l.contains("fn ") ||
            l.contains("let mut ") ||
            l.contains("impl ") ||
            l.contains("use ")
        }) {
            return Language::Rust;
        }

        // C/C++ indicators
        if lines.iter().any(|l| {
            l.contains("#include") ||
            l.contains("int main(") ||
            l.contains("void ")
        }) {
            if content_lower.contains("std::") ||
               content_lower.contains("namespace ") ||
               content_lower.contains("class ") {
                return Language::Cpp;
            }
            return Language::C;
        }

        Language::Unknown
    }

    /// Get tree-sitter language parser
    pub fn tree_sitter_language(&self) -> Option<tree_sitter::Language> {
        match self {
            Language::Python => Some(tree_sitter_python::language()),
            Language::JavaScript => Some(tree_sitter_javascript::language()),
            Language::TypeScript => Some(tree_sitter_typescript::language_typescript()),
            Language::Go => Some(tree_sitter_go::language()),
            Language::Rust => Some(tree_sitter_rust::language()),
            Language::C => Some(tree_sitter_c::language()),
            Language::Cpp => Some(tree_sitter_cpp::language()),
            Language::Unknown => None,
        }
    }

    /// Get file extensions for this language
    pub fn extensions(&self) -> &[&str] {
        match self {
            Language::Python => &["py", "pyw", "pyi"],
            Language::JavaScript => &["js", "jsx", "mjs", "cjs"],
            Language::TypeScript => &["ts", "tsx"],
            Language::Go => &["go"],
            Language::Rust => &["rs"],
            Language::C => &["c", "h"],
            Language::Cpp => &["cpp", "cc", "cxx", "hpp", "hxx"],
            Language::Unknown => &[],
        }
    }

    /// Display name for language
    pub fn display_name(&self) -> &str {
        match self {
            Language::Python => "Python",
            Language::JavaScript => "JavaScript",
            Language::TypeScript => "TypeScript",
            Language::Go => "Go",
            Language::Rust => "Rust",
            Language::C => "C",
            Language::Cpp => "C++",
            Language::Unknown => "Unknown",
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extension_detection() {
        assert_eq!(Language::from_extension("py"), Some(Language::Python));
        assert_eq!(Language::from_extension("js"), Some(Language::JavaScript));
        assert_eq!(Language::from_extension("ts"), Some(Language::TypeScript));
        assert_eq!(Language::from_extension("go"), Some(Language::Go));
        assert_eq!(Language::from_extension("rs"), Some(Language::Rust));
    }

    #[test]
    fn test_shebang_detection() {
        let python_content = "#!/usr/bin/env python3\nprint('hello')";
        assert_eq!(Language::from_shebang(python_content), Some(Language::Python));

        let node_content = "#!/usr/bin/env node\nconsole.log('hello')";
        assert_eq!(Language::from_shebang(node_content), Some(Language::JavaScript));
    }

    #[test]
    fn test_content_detection() {
        let python = "def hello():\n    print('world')";
        assert_eq!(Language::from_content(python), Language::Python);

        let js = "const hello = () => {\n  console.log('world');\n}";
        assert_eq!(Language::from_content(js), Language::JavaScript);

        let go = "package main\nfunc main() {}";
        assert_eq!(Language::from_content(go), Language::Go);
    }
}
