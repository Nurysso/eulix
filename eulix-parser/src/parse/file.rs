// This module is the bridge between walking the repo and running the
// language-specific parsers.

use rustc_hash::FxHashSet;
use std::io::Read;
use std::path::{Path, PathBuf};

use crate::os_io;
use crate::parse::slim::{slim, strip_emitted};
use crate::spill::{ParsedFileResult, Spill};

use libeulix::grammar_files;
use libeulix::grammar_files::language::Language;
use libeulix::struc::kb_struct::FileDataSimpleView;
use libeulix::utils::file_walker::FileWalker;

#[cfg(target_os = "linux")]

pub fn parse_file(
    file_path: &Path,
    root: &Path,
    spill: &Spill,
) -> Result<ParsedFileResult, Box<dyn std::error::Error>> {
    let relative_path = file_path
        .strip_prefix(root)
        .unwrap_or(file_path)
        .to_string_lossy()
        .to_string();

    // Open once, for the OS-level read hints. Read the bytes through this
    // handle so the fadvise / sequential-scan flags actually apply.
    let file = open_source_file(file_path)?;
    let mut source = String::new();
    {
        let mut reader = std::io::BufReader::new(&file);
        reader.read_to_string(&mut source)?;
    }

    // tree-sitter Tree lives and dies inside this call.
    let (_, mut result) = grammar_files::parse_source(file_path, &source)?;

    // Spill the rich view first. Phase 2 reads this back, so this has to
    // happen before we slim or strip anything.
    let loc = {
        let mut frag = Vec::with_capacity(8 * 1024);
        serde_json::to_writer(&mut frag, &FileDataSimpleView(&result))?;
        spill.append(&relative_path, &frag)?
    };

    // Release the descriptor now, before we spend time shrinking in memory.
    #[cfg(target_os = "linux")]
    os_io::done_with_file(&file);
    drop(file);

    slim(&mut result);
    strip_emitted(&mut result);

    Ok((relative_path, result, loc))
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
        println!("    Searching for files...");
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
            path.extension()
                .and_then(|e| e.to_str())
                .map(|e| ext_set.contains(e))
                .unwrap_or(false)
        },
        write_dir,
    )?;

    if verbose {
        println!("      • Found {} parseable files", all_files.len());
    }

    // Sort + dedup keeps output stable. The walker shouldn't produce dupes,
    // but this is cheap insurance.
    all_files.sort_unstable();
    all_files.dedup();
    Ok((all_files, project_hash))
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
