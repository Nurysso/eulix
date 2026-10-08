// ─────────────────────────────────────────────────────────────────────────────
// Parse outcome accounting
//
//   clean   : tree-sitter produced a tree with no ERROR / MISSING nodes
//   partial : tree-sitter produced a tree, but it contains ERROR / MISSING nodes
//             (syntax it could not fit). The file IS still extracted and
//             written to the knowledge base, on a best-effort basis.
//   failed  : we never got a usable tree (I/O error, bad UTF-8, unsupported
//             language, parser returned Err, ...). The file is NOT in the KB.
// ─────────────────────────────────────────────────────────────────────────────

use rustc_hash::FxHashMap;

#[derive(Debug, Clone)]
pub struct PartialFile {
    pub path: String,
    pub language: String,
    pub error_nodes: usize,
    pub missing_nodes: usize,
    pub first_error_line: Option<usize>,
}

impl PartialFile {
    pub fn total(&self) -> usize {
        self.error_nodes + self.missing_nodes
    }
}

#[derive(Debug, Clone)]
pub struct FailedFile {
    pub path: String,
    pub language: String,
    pub reason: String,
}

#[derive(Debug, Default, Clone, Copy)]
pub struct LangCounts {
    pub clean: usize,
    pub partial: usize,
    pub failed: usize,
}

#[derive(Debug, Default)]
pub struct ParseStats {
    pub clean: usize,
    pub partial: Vec<PartialFile>,
    pub failed: Vec<FailedFile>,
    pub by_lang: FxHashMap<String, LangCounts>,
}

impl ParseStats {
    pub fn total(&self) -> usize {
        self.clean + self.partial.len() + self.failed.len()
    }
}

pub fn lang_counts<'a>(
    map: &'a mut FxHashMap<String, LangCounts>,
    lang: &str,
) -> &'a mut LangCounts {
    map.entry(lang.to_string()).or_default()
}
