// This module is the bridge between walking the repo and running the
// language-specific parsers.

use rustc_hash::FxHashSet;
use std::path::{Path, PathBuf};

use crate::grammar_files::c;
use crate::grammar_files::cpp;
use crate::grammar_files::go;
use crate::grammar_files::java;
use crate::grammar_files::javascript;
use crate::grammar_files::language::Language;
use crate::grammar_files::python;
use crate::grammar_files::rust as rust_parser;
use crate::grammar_files::typescript;
use crate::parse::slim::{slim, strip_emitted};
use crate::spill::{ParsedFileResult, Spill};
use crate::struc::kb_struct::FileDataSimpleView;
use crate::utils::file_walker::FileWalker;

#[cfg(target_os = "linux")]
use crate::os_io;

pub fn parse_file(
    file_path: &Path,
    root: &Path,
    spill: &Spill,
) -> Result<ParsedFileResult, Box<dyn std::error::Error>> {
    // Figure out what parser to use from the extension.
    let lang = Language::detect(file_path);

    // Keep paths relative to the project root when possible. If the file
    // isn't under root, just fall back to the full path instead of blowing up.
    let relative_path = file_path
        .strip_prefix(root)
        .unwrap_or(file_path)
        .to_string_lossy()
        .to_string();

    let file = open_source_file(file_path)?;

    // The tree-sitter `Tree` is local to each language module's parse().
    // It's already gone by the time this match returns `syntax::inspect`
    // hands back owned data, so the tree can't escape. Nothing to drop here.
    // In Phase 1, the memory cost is really the returned FileData plus
    // whatever the caller keeps around.
    let mut result = match lang {
        Language::Python => python::parse_file(file_path)?.1,
        Language::JavaScript => javascript::parse_file(file_path)?.1,
        Language::TypeScript => typescript::parse_file(file_path)?.1,
        Language::Go => go::parse_file(file_path)?.1,
        Language::C => c::parse_file(file_path)?.1,
        Language::Cpp => cpp::parse_file(file_path)?.1,
        Language::Rust => rust_parser::parse_file(file_path)?.1,
        Language::Java => java::parse_file(file_path)?.1,
        _ => return Err(format!("Unsupported language: {:?}", lang).into()),
    };

    // Spill the rich view first. Phase 2 reads this back, so this has to
    //    happen before we slim or strip anything.
    let loc = {
        // 8 KiB is just a starting point; serde_json will grow it if needed.
        let mut frag = Vec::with_capacity(8 * 1024);
        serde_json::to_writer(&mut frag, &FileDataSimpleView(&result))?;
        spill.append(&relative_path, &frag)?
        // `frag` is dropped here, before we spend time slimming.
    };

    // We're done reading the file. Release the descriptor / mmap now,
    //    not at function exit, so it doesn't overlap with the JSON buffer
    //    that just came back from `spill.append`.
    #[cfg(target_os = "linux")]
    os_io::done_with_file(&file);
    drop(file);

    // Shrink the in-memory view in place. No need to build a second
    //    FileData just to throw most of it away.
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
