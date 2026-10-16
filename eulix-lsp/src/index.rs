// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Incremental workspace index.
//!
//! What this is responsible for:
//!   * name to definitions, so definition/hover/completion are a hash lookup
//!     instead of a scan over every function in the workspace;
//!   * call site to reverse index, so "find references" doesn't have to walk
//!     the whole workspace on every keystroke;
//!   * deterministic resolution, which libeulix explicitly does not provide.
//!     Its own test suite has one called
//!     `anambigous_short_name_resolves_to_some_candidate_`: an ambiguous
//!     short name resolves to whichever candidate an unordered `HashMap`
//!     happened to yield first.
//!
//! Cost model: indexing a file is O(symbols in that file). Re-indexing after
//! an edit touches only that file, so typing in a 10k-file workspace stays
//! flat.

use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};

use crate::syntax::{DefKind, ParsedFile};

mod php;
use php::PhpIndex;

// A single definition site.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Def {
    pub name: String,
    pub kind: DefKind,
    pub path: PathBuf,
    // 1-based, inclusive.
    pub line_start: usize,
    pub line_end: usize,
    // Enclosing class for methods.
    pub container: Option<String>,
}

// Where a call to `name` occurs.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord)]
pub struct CallRef {
    pub path: PathBuf,
    // 1-based.
    pub line: usize,
}

#[derive(Debug, Default)]
pub struct Index {
    php: PhpIndex,
    // name -> path -> definitions in that path
    defs: HashMap<String, HashMap<PathBuf, Vec<Def>>>,
    // path -> names it defines, so removal is O(that file) not O(workspace)
    defined_names: HashMap<PathBuf, HashSet<String>>,
    // callee name -> call sites
    calls: HashMap<String, Vec<CallRef>>,
    // path -> callee names it calls, for the same reason as defined_names
    called_names: HashMap<PathBuf, HashSet<String>>,
}

impl Index {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn def_count(&self) -> usize {
        self.defs
            .values()
            .map(|m| m.values().map(Vec::len).sum::<usize>())
            .sum()
    }

    // Drop everything contributed by `path`. Safe to call for a path we've
    // never seen.
    pub fn remove_file(&mut self, path: &Path) {
        self.php.remove(path);
        if let Some(names) = self.defined_names.remove(path) {
            for name in names {
                if let Some(by_path) = self.defs.get_mut(&name) {
                    by_path.remove(path);
                    if by_path.is_empty() {
                        self.defs.remove(&name);
                    }
                }
            }
        }
        if let Some(names) = self.called_names.remove(path) {
            for name in names {
                if let Some(sites) = self.calls.get_mut(&name) {
                    sites.retain(|c| c.path != path);
                    if sites.is_empty() {
                        self.calls.remove(&name);
                    }
                }
            }
        }
    }

    // Replace this path's contribution with what `parsed` has now. Calling
    // this twice with the same input is the same as calling it once.
    pub fn index_file(&mut self, path: &Path, parsed: &ParsedFile) {
        self.remove_file(path);
        if parsed.language == "php" {
            self.php.index(path, parsed);
        }
        let mut defined: HashSet<String> = HashSet::new();
        let mut called: HashSet<String> = HashSet::new();

        let mut add_def = |name: &str,
                           kind: DefKind,
                           line_start: usize,
                           line_end: usize,
                           container: Option<String>,
                           defined: &mut HashSet<String>| {
            if name.is_empty() {
                return;
            }
            self.defs
                .entry(name.to_string())
                .or_default()
                .entry(path.to_path_buf())
                .or_default()
                .push(Def {
                    name: name.to_string(),
                    kind,
                    path: path.to_path_buf(),
                    line_start,
                    line_end,
                    container,
                });
            defined.insert(name.to_string());
        };

        for f in &parsed.functions {
            add_def(
                &f.name,
                DefKind::Function,
                f.line_start,
                f.line_end,
                None,
                &mut defined,
            );
        }
        for c in &parsed.classes {
            add_def(
                &c.name,
                DefKind::Class,
                c.line_start,
                c.line_end,
                None,
                &mut defined,
            );
            for m in &c.methods {
                add_def(
                    &m.name,
                    DefKind::Method,
                    m.line_start,
                    m.line_end,
                    Some(c.name.clone()),
                    &mut defined,
                );
            }
        }

        for call in parsed
            .functions
            .iter()
            .flat_map(|f| f.calls.iter().map(move |c| (c.callee.as_str(), c.line)))
            .chain(
                parsed
                    .classes
                    .iter()
                    .flat_map(|c| c.methods.iter())
                    .flat_map(|m| m.calls.iter().map(move |c| (c.callee.as_str(), c.line))),
            )
        {
            let (callee, line) = call;
            if callee.is_empty() {
                continue;
            }
            let entry = CallRef {
                path: path.to_path_buf(),
                line,
            };
            let sites = self.calls.entry(callee.to_string()).or_default();
            // Guard against duplicates from overlapping call ranges without
            // sorting the whole list on every insert.
            if !sites.contains(&entry) {
                sites.push(entry);
            }
            called.insert(callee.to_string());
        }

        if !defined.is_empty() {
            self.defined_names.insert(path.to_path_buf(), defined);
        }
        if !called.is_empty() {
            self.called_names.insert(path.to_path_buf(), called);
        }
    }

    // All definitions of `name`, in a deterministic order.
    pub fn definitions(&self, name: &str) -> Vec<Def> {
        let Some(by_path) = self.defs.get(name) else {
            return Vec::new();
        };
        let mut out: Vec<Def> = by_path.values().flatten().cloned().collect();
        out.sort_by(|a, b| {
            a.path
                .cmp(&b.path)
                .then(a.line_start.cmp(&b.line_start))
                .then_with(|| a.name.cmp(&b.name))
        });
        out
    }

    pub fn definition_count(&self, name: &str) -> usize {
        self.defs
            .get(name)
            .map(|m| m.values().map(Vec::len).sum())
            .unwrap_or(0)
    }

    // Best definition of `name` as seen from `from_path`.
    //
    // The ranking is total, so results are stable across runs. That's the
    // whole point; a first-hit scan over a `HashMap` is not. Order:
    //   1. same file
    //   2. same directory
    //   3. shortest path relative to the workspace root
    //   4. lexicographic path, then line
    //
    // Returns None only when the name is genuinely unknown. If you want to
    // offer the user a choice instead of picking one, use `definitions`.
    pub fn resolve(&self, name: &str, from_path: &Path, root: Option<&Path>) -> Option<Def> {
        let mut best: Option<Def> = None;
        let mut best_key: Option<(u8, usize, PathBuf, usize)> = None;

        for def in self.definitions(name) {
            let rank = proximity_rank(&def.path, from_path, root);
            let depth = root
                .and_then(|r| def.path.strip_prefix(r).ok())
                .map(|p| p.components().count())
                .unwrap_or(usize::MAX);
            let key = (rank, depth, def.path.clone(), def.line_start);
            if best_key.as_ref().is_none_or(|b| key < *b) {
                best_key = Some(key);
                best = Some(def);
            }
        }
        best
    }

    // Every call site naming `name`, workspace wide, deterministically ordered.
    pub fn call_sites(&self, name: &str) -> Vec<CallRef> {
        let mut sites = self.calls.get(name).cloned().unwrap_or_default();
        sites.sort();
        sites.dedup();
        sites
    }

    pub fn is_php(&self, path: &Path) -> bool {
        self.php.contains(path)
    }

    pub fn php_symbol_at(
        &self,
        path: &Path,
        line: usize,
        name: &str,
        at_call: bool,
    ) -> Option<Def> {
        self.php.symbol_at(path, line, name, at_call)
    }

    pub fn php_call_sites(&self, target: &Def) -> Vec<CallRef> {
        self.php.sites_to(target)
    }

    pub fn resolve_call(
        &self,
        name: &str,
        path: &Path,
        line: usize,
        root: Option<&Path>,
    ) -> Option<Def> {
        if self.is_php(path) {
            self.php.resolve_call(path, line, name)
        } else {
            self.resolve(name, path, root)
        }
    }

    // `prefix`-matched definitions for completion, ranked and capped.
    pub fn completions(
        &self,
        prefix: &str,
        from_path: &Path,
        root: Option<&Path>,
        limit: usize,
    ) -> Vec<Def> {
        let mut out: Vec<(u8, usize, PathBuf, usize, Def)> = Vec::new();
        for (name, by_path) in &self.defs {
            if !name.starts_with(prefix) {
                continue;
            }
            for defs in by_path.values() {
                for def in defs {
                    let rank = proximity_rank(&def.path, from_path, root);
                    let depth = root
                        .and_then(|r| def.path.strip_prefix(r).ok())
                        .map(|p| p.components().count())
                        .unwrap_or(usize::MAX);
                    out.push((rank, depth, def.path.clone(), def.line_start, def.clone()));
                }
            }
        }
        out.sort_by(|a, b| (a.0, a.1, &a.2, a.3, &a.4.name).cmp(&(b.0, b.1, &b.2, b.3, &b.4.name)));
        // One entry per distinct name, using the best-ranked occurrence as
        // the representative.
        let mut seen = HashSet::new();
        out.into_iter()
            .filter_map(|(_, _, _, _, d)| seen.insert(d.name.clone()).then_some(d))
            .take(limit)
            .collect()
    }
}

fn proximity_rank(candidate: &Path, from: &Path, _root: Option<&Path>) -> u8 {
    if candidate == from {
        0
    } else if candidate.parent() == from.parent() {
        1
    } else {
        2
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::syntax::{CallSite, Func, Klass, SyntaxReport};

    fn call(name: &str, line: usize) -> CallSite {
        CallSite {
            callee: name.to_string(),
            qualifier: None,
            line,
            is_conditional: false,
            context: "unconditional".to_string(),
        }
    }

    fn func(name: &str, start: usize, end: usize) -> Func {
        Func {
            id: format!("func_{name}"),
            name: name.to_string(),
            line_start: start,
            line_end: end,
            ..Default::default()
        }
    }

    fn parsed_with(functions: Vec<Func>, classes: Vec<Klass>) -> ParsedFile {
        ParsedFile {
            functions,
            classes,
            syntax: SyntaxReport::default(),
            ..Default::default()
        }
    }

    #[test]
    fn index_and_resolve_roundtrip() {
        let mut ix = Index::new();
        ix.index_file(
            Path::new("/w/a.rs"),
            &parsed_with(vec![func("foo", 1, 3)], vec![]),
        );
        let d = ix
            .resolve("foo", Path::new("/w/a.rs"), Some(Path::new("/w")))
            .unwrap();
        assert_eq!(d.line_start, 1);
        assert_eq!(ix.definitions("foo").len(), 1);
    }

    #[test]
    fn reindexing_a_file_replaces_rather_than_duplicates() {
        let mut ix = Index::new();
        let p = Path::new("/w/a.rs");
        for _ in 0..5 {
            ix.index_file(p, &parsed_with(vec![func("foo", 1, 3)], vec![]));
        }
        assert_eq!(ix.def_count(), 1, "stale definitions accumulated");
    }

    #[test]
    fn removing_a_file_clears_its_defs_and_call_sites() {
        let mut ix = Index::new();
        let a = Path::new("/w/a.rs");
        let b = Path::new("/w/b.rs");
        ix.index_file(
            a,
            &parsed_with(
                vec![Func {
                    calls: vec![call("target", 5)],
                    ..func("caller", 1, 9)
                }],
                vec![],
            ),
        );
        ix.index_file(b, &parsed_with(vec![func("target", 2, 4)], vec![]));

        assert_eq!(ix.call_sites("target").len(), 1);
        ix.remove_file(a);
        assert!(
            ix.call_sites("target").is_empty(),
            "call sites outlived their file"
        );
        assert_eq!(ix.definition_count("target"), 1);
        assert!(
            ix.resolve("target", a, None).is_some(),
            "b's def should survive"
        );
    }

    #[test]
    fn resolution_prefers_same_file_then_same_directory_then_stable_order() {
        let mut ix = Index::new();
        let same = Path::new("/w/pkg/a.rs");
        let sibling = Path::new("/w/pkg/b.rs");
        let far = Path::new("/w/other/z.rs");
        ix.index_file(same, &parsed_with(vec![func("run", 1, 2)], vec![]));
        ix.index_file(sibling, &parsed_with(vec![func("run", 1, 2)], vec![]));
        ix.index_file(far, &parsed_with(vec![func("run", 1, 2)], vec![]));

        assert_eq!(
            ix.resolve("run", same, Some(Path::new("/w"))).unwrap().path,
            same
        );
        assert_eq!(
            ix.resolve("run", sibling, Some(Path::new("/w")))
                .unwrap()
                .path,
            sibling
        );
        // From an unrelated file, the winner has to be the same every time,
        // not just "one of them."
        let probe = Path::new("/w/pkg/c.rs");
        let first = ix.resolve("run", probe, Some(Path::new("/w"))).unwrap();
        for _ in 0..50 {
            assert_eq!(
                ix.resolve("run", probe, Some(Path::new("/w"))).unwrap(),
                first
            );
        }
    }

    #[test]
    fn call_sites_are_deduplicated_and_sorted() {
        let mut ix = Index::new();
        let mut f = func("caller", 1, 20);
        f.calls = vec![call("dup", 7), call("dup", 7), call("dup", 3)];
        ix.index_file(Path::new("/w/a.rs"), &parsed_with(vec![f], vec![]));
        let sites = ix.call_sites("dup");
        assert_eq!(sites.len(), 2, "identical call site recorded twice");
        assert!(sites[0].line < sites[1].line, "call sites must be ordered");
    }

    #[test]
    fn methods_are_indexed_with_their_container() {
        let mut ix = Index::new();
        let cls = Klass {
            name: "Server".to_string(),
            methods: vec![func("serve", 4, 9)],
            ..Default::default()
        };
        ix.index_file(Path::new("/w/a.rs"), &parsed_with(vec![], vec![cls]));
        let d = ix.resolve("serve", Path::new("/w/a.rs"), None).unwrap();
        assert_eq!(d.kind, DefKind::Method);
        assert_eq!(d.container.as_deref(), Some("Server"));
    }

    #[test]
    fn completions_are_prefix_matched_ranked_and_capped() {
        let mut ix = Index::new();
        ix.index_file(
            Path::new("/w/a.rs"),
            &parsed_with(vec![func("run", 1, 2)], vec![]),
        );
        ix.index_file(
            Path::new("/w/z.rs"),
            &parsed_with(vec![func("runaway", 1, 2)], vec![]),
        );
        ix.index_file(
            Path::new("/w/y.rs"),
            &parsed_with(vec![func("other", 1, 2)], vec![]),
        );

        let out = ix.completions("run", Path::new("/w/a.rs"), Some(Path::new("/w")), 10);
        let names: Vec<_> = out.iter().map(|d| d.name.as_str()).collect();
        assert_eq!(names, vec!["run", "runaway"]);
        assert_eq!(
            ix.completions("run", Path::new("/w/a.rs"), None, 1).len(),
            1
        );
    }

    #[test]
    fn unknown_names_resolve_to_none() {
        let ix = Index::new();
        assert!(ix.resolve("nope", Path::new("/a"), None).is_none());
        assert!(ix.definitions("nope").is_empty());
        assert!(ix.call_sites("nope").is_empty());
    }
}
