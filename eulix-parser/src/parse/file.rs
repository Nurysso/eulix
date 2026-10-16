// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// This module is the bridge between walking the repo and running the
// language-specific parsers.

use rustc_hash::FxHashSet;
use std::io::Read;
use std::path::{Path, PathBuf};

use crate::os_io;
use crate::parse::slim::{slim, strip_emitted};
use crate::spill::{ParsedFileResult, Spill};
use crate::views::FileDataSimpleView;

use libeulix::grammar_files;
use libeulix::grammar_files::language::Language;
use libeulix::grammar_files::php;
use libeulix::utils::file_walker::FileWalker;

pub fn parse_file(
    file_path: &Path,
    root: &Path,
    spill: &Spill,
) -> Result<ParsedFileResult, Box<dyn std::error::Error>> {
    // ) -> Result<(String, FileData, SpillLoc, Vec<(String, String)>), Box<dyn std::error::Error>> {
    let relative_path = file_path
        .strip_prefix(root)
        .unwrap_or(file_path)
        .to_string_lossy()
        .to_string();

    // Open once, for the OS-level read hints. Read the bytes through this
    // handle so the fadvise / sequential-scan flags actually apply.
    let file = open_source_file(file_path)?;
    let mut bytes = Vec::new();
    {
        let mut reader = std::io::BufReader::new(&file);
        reader.read_to_end(&mut bytes)?;
    }

    // Older code often has Latin-1 comments or strings. Replacing the
    // invalid bytes keeps the rest of the file instead of failing it, and
    // the file is reported as partial so the loss still shows up.
    let (source, invalid_line) = match String::from_utf8(bytes) {
        Ok(source) => (source, None),
        Err(e) => {
            let valid = e.utf8_error().valid_up_to();
            let bytes = e.into_bytes();
            let line = 1 + bytes[..valid].iter().filter(|&&b| b == b'\n').count();
            (String::from_utf8_lossy(&bytes).into_owned(), Some(line))
        }
    };

    // tree-sitter Tree lives and dies inside this call.
    let (_, mut result) = grammar_files::parse_source(Path::new(&relative_path), &source)?;
    if let Some(line) = invalid_line {
        let syntax = &mut result.syntax;
        syntax.error_nodes += 1;
        syntax.first_error_line = Some(syntax.first_error_line.map_or(line, |l| l.min(line)));
    }

    // Spill the rich view first. Phase 2 reads this back, so this has to
    // happen before we slim or strip anything.
    let loc = {
        let mut frag = Vec::with_capacity(8 * 1024);
        serde_json::to_writer(&mut frag, &FileDataSimpleView(&result))?;
        spill.append(&relative_path, &frag)?
    };

    // Extract imports BEFORE slim() clears them
    let raw_imports: Vec<(String, String, String)> = result
        .imports
        .iter()
        .filter(|i| {
            !matches!(
                i.import_type.as_str(),
                "internal" | "cgo" | "include" | "include_dynamic"
            )
        })
        .map(|i| {
            (
                i.module.clone(),
                relative_path.clone(),
                i.import_type.clone(),
            )
        })
        .collect();

    // Release the descriptor now, before we spend time shrinking in memory.
    #[cfg(target_os = "linux")]
    os_io::done_with_file(&file);
    drop(file);

    slim(&mut result);
    strip_emitted(&mut result);

    Ok((relative_path, result, loc, raw_imports))
}

pub fn collect_source_files_and_hash(
    root: &Path,
    languages: &str,
    euignore_path: Option<&Path>,
    verbose: bool,
    write_dir: &Path,
) -> Result<(Vec<PathBuf>, String), Box<dyn std::error::Error>> {
    // Turn the comma-separated CLI value into Language enums.
    // `all` is just a shortcut; otherwise we parse each token and ignore
    // anything we don't recognize (with a warning if verbose).
    let lang_filters: Vec<Language> = if languages == "all" {
        vec![
            Language::C,
            Language::Cpp,
            Language::Python,
            Language::JavaScript,
            Language::TypeScript,
            Language::Go,
            Language::Rust,
            Language::Java,
            Language::Php,
        ]
    } else {
        languages
            .split(',')
            .map(|s| s.trim())
            .filter_map(|lang_str| match lang_str.to_lowercase().as_str() {
                "c" | "h" => Some(Language::C),
                "cpp" | "c++" | "cxx" | "hpp" => Some(Language::Cpp),
                "python" | "py" => Some(Language::Python),
                "javascript" | "js" => Some(Language::JavaScript),
                "java" => Some(Language::Java),
                "typescript" | "ts" => Some(Language::TypeScript),
                "go" | "golang" => Some(Language::Go),
                "rust" | "rs" => Some(Language::Rust),
                "php" => Some(Language::Php),
                _ => {
                    if verbose {
                        eprintln!("     Unknown language filter '{}'", lang_str);
                    }
                    None
                }
            })
            .collect()
    };

    if verbose {
        println!("      • Searching for files...");
    }

    // Collect extensions once so the walker predicate is just a hash lookup.
    let mut ext_set: FxHashSet<&'static str> = FxHashSet::default();
    for lang in &lang_filters {
        for ext in lang.extensions() {
            ext_set.insert(ext);
        }
    }

    let walker = if let Some(ignore_path) = euignore_path {
        FileWalker::new(root.to_path_buf()).with_euignore(ignore_path.to_path_buf())
    } else {
        FileWalker::new(root.to_path_buf())
    };

    // Walk the tree, keep files whose extension matches, and compute the
    // project hash while we're at it.
    let (mut all_files, project_hash) = walker.walk_and_project_hash(
        |path| {
            let ext = path
                .extension()
                .and_then(|e| e.to_str())
                .map(str::to_ascii_lowercase);
            match ext.as_deref() {
                Some("inc") => ext_set.contains("inc") && holds_php(path),
                Some(e) => ext_set.contains(e),
                None => false,
            }
        },
        write_dir,
    )?;

    // Sort + dedup keeps output stable. The walker shouldn't produce dupes,
    // but this is cheap insurance.
    all_files.sort_unstable();
    all_files.dedup();
    Ok((all_files, project_hash))
}

/// Whether a `.inc` file holds PHP code, judged by a PHP opening tag the way
/// libeulix judges it. C, C++ and assembler projects use the extension too.
fn holds_php(path: &Path) -> bool {
    std::fs::read(path).is_ok_and(|bytes| php::has_open_tag(&String::from_utf8_lossy(&bytes)))
}

pub fn open_source_file(path: &Path) -> std::io::Result<std::fs::File> {
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::fs::OpenOptionsExt;
        // Windows: pass FILE_FLAG_SEQUENTIAL_SCAN manually because std
        // doesn't expose it directly.
        std::fs::OpenOptions::new()
            .read(true)
            .custom_flags(0x0800_0000)
            .open(path)
    }
    #[cfg(not(target_os = "windows"))]
    {
        let f = std::fs::File::open(path)?;
        os_io::hint_read_sequential(&f);
        Ok(f)
    }
}

pub fn rel_path(file: &Path, root: &Path) -> String {
    // Relative path as a String. If the file isn't under root, fall back
    // to the original path instead of failing.
    file.strip_prefix(root)
        .unwrap_or(file)
        .to_string_lossy()
        .into_owned()
}

pub fn language_label(file: &Path) -> String {
    // Debug format lowercased, e.g. "python", "rust", etc.
    format!("{:?}", Language::detect(file)).to_lowercase()
}

#[cfg(test)]
mod tests {
    use crate::analysis_pipeline::analyze_and_build;
    use crate::engine::Engine;
    use crate::parse::directory::parse_directory;
    use crate::stats::ParseStats;
    use libeulix::struc::kb_struct::KnowledgeBase;
    use std::fs;
    use tempfile::TempDir;

    #[test]
    fn symbol_ids_use_relative_paths_and_call_graph_resolves() {
        let repo = TempDir::new().unwrap();
        let out = TempDir::new().unwrap();
        let src_dir = repo.path().join("src");
        fs::create_dir_all(&src_dir).unwrap();
        fs::write(src_dir.join("a.py"), "def caller():\n    target()\n").unwrap();
        fs::write(src_dir.join("b.py"), "def target():\n    return 1\n").unwrap();

        let engine = Engine::with_threads(2).unwrap();
        let root_str = repo.path().to_string_lossy().to_string();
        let (kb, _stats, _spill) = parse_directory(
            &root_str,
            "all",
            None,
            false,
            "0.8.1",
            "test",
            out.path(),
            false,
            &engine,
        )
        .unwrap();

        let fd_a = kb.structure.get("src/a.py").expect("src/a.py in structure");
        assert_eq!(fd_a.functions[0].id, "func_caller::src/a.py");
        let fd_b = kb.structure.get("src/b.py").expect("src/b.py in structure");
        assert_eq!(fd_b.functions[0].id, "func_target::src/b.py");

        let kb = analyze_and_build(kb, false, 2);
        assert!(
            kb.call_graph.edges.iter().any(|e| {
                e.edge_type == "call"
                    && e.from == "func_caller::src/a.py"
                    && e.to == "func_target::src/b.py"
            }),
            "expected call edge func_caller::src/a.py -> func_target::src/b.py, got {:?}",
            kb.call_graph.edges
        );
    }

    fn parse(repo: &TempDir, out: &TempDir) -> (KnowledgeBase, ParseStats) {
        let engine = Engine::with_threads(2).unwrap();
        let root_str = repo.path().to_string_lossy().to_string();
        let (kb, stats, _spill) = parse_directory(
            &root_str,
            "all",
            None,
            false,
            "0.8.1",
            "test",
            out.path(),
            false,
            &engine,
        )
        .unwrap();
        (kb, stats)
    }

    #[test]
    fn uppercase_extensions_are_parsed() {
        let repo = TempDir::new().unwrap();
        let out = TempDir::new().unwrap();
        fs::write(repo.path().join("UPPER.PY"), "def upper():\n    return 1\n").unwrap();
        fs::write(repo.path().join("Lower.Rs"), "pub fn lower() {}\n").unwrap();

        let (kb, stats) = parse(&repo, &out);

        assert_eq!(kb.structure["UPPER.PY"].functions[0].name, "upper");
        assert_eq!(kb.structure["Lower.Rs"].functions[0].name, "lower");
        assert_eq!(stats.clean, 2);
    }

    #[test]
    fn non_utf8_bytes_are_replaced_and_reported_as_partial() {
        let repo = TempDir::new().unwrap();
        let out = TempDir::new().unwrap();
        // 0xE9 is "é" in Latin-1 and not valid UTF-8 on its own.
        fs::write(
            repo.path().join("latin1.py"),
            b"def first():\n    return 1\n\n# caf\xe9\ndef second():\n    return 2\n",
        )
        .unwrap();

        let (kb, stats) = parse(&repo, &out);

        let names: Vec<&str> = kb.structure["latin1.py"]
            .functions
            .iter()
            .map(|f| f.name.as_str())
            .collect();
        assert_eq!(names, vec!["first", "second"]);
        assert_eq!(stats.clean, 0);
        assert!(stats.failed.is_empty());
        assert_eq!(stats.partial.len(), 1);
        assert_eq!(stats.partial[0].path, "latin1.py");
        assert_eq!(stats.partial[0].first_error_line, Some(4));
    }
}
