//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use std::path::Path;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Language {
    C,
    Cpp,
    Go,
    Java,
    JavaScript,
    Jsx,
    TypeScript,
    Tsx,
    Python,
    Rust,
    Unknown,
}
/// Single source of truth for extension/filename -> language mapping.
struct LangSpec {
    lang: Language,
    extensions: &'static [&'static str],
    filenames: &'static [&'static str],
}

static LANG_TABLE: &[LangSpec] = &[
    LangSpec {
        lang: Language::Python,
        extensions: &["py", "pyw", "pyi"],
        filenames: &[],
    },
    LangSpec {
        lang: Language::JavaScript,
        extensions: &["js", "mjs", "cjs"],
        filenames: &[],
    },
    LangSpec {
        lang: Language::Jsx,
        extensions: &["jsx"],
        filenames: &[],
    },
    LangSpec {
        lang: Language::TypeScript,
        extensions: &["ts", "mts", "cts"],
        filenames: &[],
    },
    LangSpec {
        lang: Language::Tsx,
        extensions: &["tsx"],
        filenames: &[],
    },
    LangSpec {
        lang: Language::Go,
        extensions: &["go"],
        filenames: &["go.mod", "go.sum"],
    },
    LangSpec {
        lang: Language::Rust,
        extensions: &["rs"],
        filenames: &["Cargo.toml", "Cargo.lock"],
    },
    LangSpec {
        lang: Language::Java,
        extensions: &["java"],
        filenames: &[],
    },
    LangSpec {
        lang: Language::C,
        extensions: &["c", "h"],
        filenames: &["Makefile", "GNUmakefile"],
    },
    LangSpec {
        lang: Language::Cpp,
        extensions: &["cpp", "cc", "cxx", "hpp", "hxx", "h++", "c++"],
        filenames: &[],
    },
];

impl Language {
    /// Extension/filename only no I/O, no allocation on the hit path.
    pub fn detect(path: &Path) -> Self {
        if let Some(ext) = path.extension().and_then(|e| e.to_str()) {
            if let Some(lang) = Self::from_extension(ext) {
                return lang;
            }
        }

        if let Some(filename) = path.file_name().and_then(|f| f.to_str()) {
            if let Some(lang) = Self::from_filename(filename) {
                return lang;
            }
        }

        Language::Unknown
    }

    pub fn extensions(&self) -> &'static [&'static str] {
        LANG_TABLE
            .iter()
            .find(|spec| spec.lang == *self)
            .map(|spec| spec.extensions)
            .unwrap_or(&[])
    }

    fn from_extension(ext: &str) -> Option<Self> {
        // Extensions in the table are already lowercase; most real-world
        // paths are too, so try a zero-cost exact match first and only
        // pay for to_lowercase() on the rare mixed-case extension.
        if let Some(lang) = LANG_TABLE
            .iter()
            .find(|spec| spec.extensions.contains(&ext))
            .map(|spec| spec.lang)
        {
            return Some(lang);
        }
        if ext.chars().any(|c| c.is_ascii_uppercase()) {
            let lower = ext.to_ascii_lowercase();
            return LANG_TABLE
                .iter()
                .find(|spec| spec.extensions.contains(&lower.as_str()))
                .map(|spec| spec.lang);
        }
        None
    }

    fn from_filename(filename: &str) -> Option<Self> {
        LANG_TABLE
            .iter()
            .find(|spec| spec.filenames.contains(&filename))
            .map(|spec| spec.lang)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;

    #[test]
    fn test_extension_detection() {
        // Python
        assert_eq!(Language::from_extension("py"), Some(Language::Python));
        assert_eq!(Language::from_extension("pyw"), Some(Language::Python));
        assert_eq!(Language::from_extension("pyi"), Some(Language::Python));

        // JavaScript & JSX
        assert_eq!(Language::from_extension("js"), Some(Language::JavaScript));
        assert_eq!(Language::from_extension("mjs"), Some(Language::JavaScript));
        assert_eq!(Language::from_extension("cjs"), Some(Language::JavaScript));
        assert_eq!(Language::from_extension("jsx"), Some(Language::Jsx));

        // TypeScript & TSX
        assert_eq!(Language::from_extension("ts"), Some(Language::TypeScript));
        assert_eq!(Language::from_extension("mts"), Some(Language::TypeScript));
        assert_eq!(Language::from_extension("cts"), Some(Language::TypeScript));
        assert_eq!(Language::from_extension("tsx"), Some(Language::Tsx));

        // Systems & General
        assert_eq!(Language::from_extension("go"), Some(Language::Go));
        assert_eq!(Language::from_extension("rs"), Some(Language::Rust));
        assert_eq!(Language::from_extension("java"), Some(Language::Java));
        assert_eq!(Language::from_extension("c"), Some(Language::C));
        assert_eq!(Language::from_extension("h"), Some(Language::C));

        // C++
        assert_eq!(Language::from_extension("cpp"), Some(Language::Cpp));
        assert_eq!(Language::from_extension("cc"), Some(Language::Cpp));
        assert_eq!(Language::from_extension("cxx"), Some(Language::Cpp));
        assert_eq!(Language::from_extension("hpp"), Some(Language::Cpp));
        assert_eq!(Language::from_extension("hxx"), Some(Language::Cpp));
        assert_eq!(Language::from_extension("h++"), Some(Language::Cpp));
        assert_eq!(Language::from_extension("c++"), Some(Language::Cpp));

        // Unmatched extension
        assert_eq!(Language::from_extension("txt"), None);
    }

    #[test]
    fn test_case_insensitive_extension() {
        assert_eq!(Language::from_extension("PY"), Some(Language::Python));
        assert_eq!(Language::from_extension("Rs"), Some(Language::Rust));
        assert_eq!(Language::from_extension("CPP"), Some(Language::Cpp));
    }

    #[test]
    fn test_filename_detection() {
        assert_eq!(Language::from_filename("Makefile"), Some(Language::C));
        assert_eq!(Language::from_filename("GNUmakefile"), Some(Language::C));
        assert_eq!(Language::from_filename("go.mod"), Some(Language::Go));
        assert_eq!(Language::from_filename("go.sum"), Some(Language::Go));
        assert_eq!(Language::from_filename("Cargo.toml"), Some(Language::Rust));
        assert_eq!(Language::from_filename("Cargo.lock"), Some(Language::Rust));

        // Unmatched filename
        assert_eq!(Language::from_filename("random.txt"), None);
    }

    #[test]
    fn test_path_detect() {
        // Standard path resolution by extension
        assert_eq!(Language::detect(Path::new("src/main.rs")), Language::Rust);
        assert_eq!(
            Language::detect(Path::new("scripts/app.tsx")),
            Language::Tsx
        );

        // Path resolution by exact filename (no extension match)
        assert_eq!(Language::detect(Path::new("Cargo.toml")), Language::Rust);
        assert_eq!(Language::detect(Path::new("project/Makefile")), Language::C);

        // Unknown path/extension
        assert_eq!(Language::detect(Path::new("README.md")), Language::Unknown);
        assert_eq!(
            Language::detect(Path::new("no_extension")),
            Language::Unknown
        );
    }

    #[test]
    fn test_language_extensions_getter() {
        assert_eq!(Language::Python.extensions(), &["py", "pyw", "pyi"]);
        assert_eq!(Language::Go.extensions(), &["go"]);
        assert_eq!(Language::Unknown.extensions(), &[] as &[&str]);
    }
}
