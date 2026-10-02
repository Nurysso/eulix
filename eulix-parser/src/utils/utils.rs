#![allow(clippy::module_inception)]
use std::path::{Path, PathBuf};

/// "test1/kb.json" -> "test1"
/// "test1/data/kb" -> "test1/data"
/// "kb.json"       -> "."   (no directory part, so the current dir)
pub fn output_dir(output: impl AsRef<Path>) -> PathBuf {
    let path = output.as_ref();
    match path.parent() {
        Some(p) if !p.as_os_str().is_empty() => p.to_path_buf(),
        _ => PathBuf::from("."),
    }
}
