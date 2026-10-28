// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
//! PRISM v2 — Polyglot Resolution via Inverted Symbol Map, version 2.
//!
//! v2 is a *scope-aware* call-graph resolver. Compared with v1, it trades a
//! small amount of extra bookkeeping for a large gain in precision:
//!
//!   * v1 resolves an unresolved short name with a single global
//!     "first definition wins" lookup. That is fast but wrong whenever two
//!     unrelated symbols share a short name (`__init__`, `run`, `handle`,
//!     `Get`...), which is the common case in real repos.
//!
//!   * v2 resolves the same short name through a *scope ladder*:
//!     same file  →  explicit import  →  included files (PHP)  →  same
//!     package/dir  →  project-wide
//!     Only if the caller is still ambiguous at every tighter tier does it
//!     fall back to the loose global search, and even then it refuses
//!     names that are too common (`max_ambiguous`).
//!
//! v2 also understands class hierarchies: `self.foo()` and
//! `BaseClass.foo()` can be resolved through the inheritance chain rather
//! than a name-only guess.
//!
//! PHP is resolved by its own rules, in the `php` submodule. PHP and
//! non-PHP symbols never resolve to each other.
//!
//! On a ~64k-file polyglot repo this runs in ~1.4s with ~3.1GB peak RSS,
//! producing ~773k nodes and ~1.5M edges. That is the target profile.

use crate::struc::kb_struct::{
    CallGraph, CallGraphEdge, CallGraphNode, CallerInfo, FileData, KnowledgeBase, Parameter,
};
use rayon::prelude::*;
use std::borrow::Cow;
use std::cmp::Reverse;
use std::collections::hash_map::Entry;
use std::collections::VecDeque;
use std::collections::{BTreeMap, HashMap};

mod php;
use crate::grammar_files::php::FALLBACK_TAG;
use crate::grammar_files::utils::NESTED_TAG;
use php::{include_closures, php_name, php_namespace, IncludeIndex, PhpHierarchy};

type Map<K, V> = HashMap<K, V>;

const ENTRY_TAG: &str = "entry-point";
/// Prefixes the parser adds to synthesized IDs (`func_foo`, `method_Bar.baz`).
/// Stripping them is what lets us compare against a caller's raw name.
const KNOWN_PREFIXES: [&str; 4] = ["func_", "method_", "class_", "struct_"];
/// Spellings of the implicit receiver across the languages we support.
const SELF_NAMES: [&str; 6] = ["self", "this", "cls", "_self", "self_", "myself"];
/// Cap on the BFS depth when walking the class hierarchy. Real hierarchies
/// are shallow; a cap here prevents a cyclic or adversarial graph from
/// blowing up the walk.
const MAX_CHAIN: usize = 64;
const EDGE_CALL: u8 = 0;
const EDGE_INHERIT: u8 = 1;

#[derive(Clone, Debug)]
pub struct CallGraphOptions {
    /// A bare name that matches more than this many project symbols (and could
    /// not be pinned down by file / import / package) is considered too generic
    /// (`Get`, `Close`, `String`...) and produces no edge. PHP method calls on
    /// a receiver of unknown class are stricter: they need the only method of
    /// that name in the project (see the `php` submodule).
    pub max_ambiguous: usize,
    /// If a name or qualifier comes from an import that does not map to a file
    /// in the project (stdlib, third-party), never fall back to a global name
    /// search. Turn off if your TS code uses path aliases (`@app/...`) that
    /// the module index cannot resolve.
    pub strict_imports: bool,
}

impl Default for CallGraphOptions {
    fn default() -> Self {
        Self {
            max_ambiguous: 8,
            strict_imports: true,
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    Function,
    Method,
    Class,
}

/// What the call site *looks like* syntactically. This is what the resolver
/// uses to decide which kinds of targets are acceptable.
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Want {
    /// `foo()`
    Call,
    /// `x.foo()` where x is an unknown receiver
    MethodCall,
    /// base class / static receiver
    Class,
}

/// What a lookup accepts: the kinds a call-site shape allows, ranked
/// (`Want`), or exactly one kind. PHP uses the latter, since its call syntax
/// already says whether it reaches a function or a method.
#[derive(Clone, Copy)]
enum Seek {
    Shape(Want),
    Only(Kind),
}

impl From<Want> for Seek {
    fn from(want: Want) -> Self {
        Seek::Shape(want)
    }
}

/// Borrowed view of everything the resolver needs about one node.
/// All `&'a str` fields point into the original `KnowledgeBase`, so the
/// resolver allocates essentially nothing per node. The one exception is the
/// name of a PHP node, owned because it is the lowercase qualified name
/// (`app\models\user`): PHP names are case-insensitive.
struct Node<'a> {
    id: &'a str,
    name: Cow<'a, str>,
    kind: Kind,
    file: u32,
    is_entry: bool,
    /// PHP fallback declaration, see `FALLBACK_TAG`.
    fallback: bool,
    /// PHP `static` method.
    php_static: bool,
    owner: Option<&'a str>,
    /// Declared in another function's body, see `NESTED_TAG`.
    nested: bool,
}

#[derive(Clone, Copy)]
struct RawEdge {
    from: u32,
    to: u32,
    line: u32,
    kind: u8,
    conditional: bool,
}

struct BaseRef<'a> {
    class: &'a str,
    base: &'a str,
    file: u32,
    line: u32,
}

/// `None` = the module is not part of the project (stdlib / third party).
/// `Some(files)` = this import/namespace resolves to those file indices.
type Target<'a> = Option<&'a [u32]>;

#[derive(Default)]
struct FileImports<'a> {
    /// `import { foo } from "x"` — `foo` may be called bare as `foo()`.
    names: Map<&'a str, Target<'a>>,
    /// `import * as ns from "x"` or `import x.y` — `ns.foo()` / `x.y.foo()`.
    namespaces: Map<&'a str, Target<'a>>,
    /// PHP `include`/`require` targets, sorted. Direct includes while the
    /// files are read, then replaced by the transitive closure.
    includes: Vec<u32>,
}

/// Everything a single parallel chunk extracts from its slice of files.
struct Extract<'a> {
    nodes: Vec<Node<'a>>,
    bases: Vec<BaseRef<'a>>,
    /// PHP class -> the class it `extends` and the traits it uses (`bases`
    /// holds both, with the interfaces, in no particular order).
    php_extends: Vec<BaseRef<'a>>,
    php_traits: Vec<BaseRef<'a>>,
    imports: Vec<FileImports<'a>>,
}

// ───────────────────────────── string helpers ─────────────────────────────

#[inline]
fn sep(c: char) -> bool {
    c == '.' || c == ':' || c == '/'
}

#[inline]
fn clean_path(p: &str) -> &str {
    p.trim_start_matches("./")
}

#[inline]
fn dir_of(p: &str) -> &str {
    clean_path(p).rsplit_once('/').map_or("", |(d, _)| d)
}

/// `func_newFileCache::eulix-cli/internal/x.go` -> `newFileCache`
/// `method_Cache.Get::a/b.go` / `func_Foo::bar::a/b.go` -> `Get` / `bar`
///
/// This is the "short name" we index by. It deliberately strips:
///   * the `func_` / `method_` / `class_` / `struct_` prefix
///   * everything after the last `::` (the file/scope suffix)
///   * a trailing `.name` segment (dotted qualifier)
#[inline]
fn bare_name(id: &str) -> &str {
    let name = id.rsplit_once("::").map_or(id, |(n, _)| n);
    let name = KNOWN_PREFIXES
        .iter()
        .find_map(|p| name.strip_prefix(p))
        .unwrap_or(name);
    let name = name.rsplit("::").next().unwrap_or(name);
    name.rsplit('.').next().unwrap_or(name)
}

/// `a.b.c` -> (Some("a.b"), "c");  `Foo::bar` -> (Some("Foo"), "bar")
///
/// The two separators overlap (`::` contains `:`), so we compare the last
/// index of each and take whichever is further right — that's the actual
/// last separator in the string.
#[inline]
fn split_callee(c: &str) -> (Option<&str>, &str) {
    match (c.rfind('.'), c.rfind("::")) {
        (None, None) => (None, c),
        (Some(d), None) => (Some(&c[..d]), &c[d + 1..]),
        (None, Some(p)) => (Some(&c[..p]), &c[p + 2..]),
        (Some(d), Some(p)) => {
            if d > p {
                (Some(&c[..d]), &c[d + 1..])
            } else {
                (Some(&c[..p]), &c[p + 2..])
            }
        }
    }
}

#[inline]
fn first_self_param(params: &[Parameter]) -> bool {
    params
        .first()
        .map(|p| SELF_NAMES.contains(&p.name.as_str()))
        .unwrap_or(false)
}

#[inline]
fn is_param(params: &[Parameter], callee: &str) -> bool {
    if split_callee(callee).0.is_some() {
        return false;
    }
    params.iter().any(|p| {
        let name = p
            .name
            .trim_start_matches("mut ")
            .trim_start_matches('&')
            .trim_start_matches('*')
            .trim_start_matches("...")
            .trim();
        !SELF_NAMES.contains(&name) && name == callee
    })
}

#[inline]
fn is_self_qualifier(q: &str) -> bool {
    SELF_NAMES.contains(&q) || q == "Self"
}

/// Number of leading path components `a` and `b` share (byte-aligned to `/`).
/// Used as a cheap "closeness" heuristic: a candidate in a nearby directory
/// is a better guess than one on the other side of the tree.
fn common_dir_prefix(a: &str, b: &str) -> usize {
    let (a, b) = (clean_path(a).as_bytes(), clean_path(b).as_bytes());
    let (mut last_slash, mut i) = (0, 0);
    while i < a.len().min(b.len()) && a[i] == b[i] {
        if a[i] == b'/' {
            last_slash = i + 1;
        }
        i += 1;
    }
    last_slash
}

// module index
//
// Maps every component-suffix of every file stem AND every directory to the
// files it denotes. Keys are borrowed slices of the file paths (no allocation).
//   src/a/b.py   -> "src/a/b", "a/b", "b"      (python / rust / js module)
//   pkg/retr/x.go-> "pkg/retr", "retr"         (go package = directory)
//
// The suffix trick is what makes the index polyglot: every language's module
// syntax is expressible as some suffix of a path, so we can look up "retr"
// and match `pkg/retr/x.go` without knowing we're in Go.
fn add_suffixes<'a>(map: &mut Map<&'a str, Vec<u32>>, s: &'a str, file: u32) {
    let mut cur = s;
    while !cur.is_empty() {
        let v = map.entry(cur).or_default();
        if v.last() != Some(&file) {
            v.push(file);
        }
        match cur.split_once('/') {
            Some((_, rest)) => cur = rest,
            None => break,
        }
    }
}

fn build_module_index<'a>(files: &[&'a str]) -> Map<&'a str, Vec<u32>> {
    let mut map: Map<&'a str, Vec<u32>> = Map::with_capacity(files.len() * 3);
    for (i, &path) in files.iter().enumerate() {
        let cleaned = clean_path(path);
        // File "stem": drop the extension, but only if the last `.` is not
        // part of a directory name (a common gotcha on paths like `v1.2/x`).
        let stem = match cleaned.rfind('.') {
            Some(d) if !cleaned[d..].contains('/') => &cleaned[..d],
            _ => cleaned,
        };
        add_suffixes(&mut map, stem, i as u32);
        if let Some(slash) = stem.rfind('/') {
            add_suffixes(&mut map, &stem[..slash], i as u32);
        }
    }
    map
}

fn resolve_module<'a>(module: &str, index: &'a Map<&'a str, Vec<u32>>) -> Target<'a> {
    let mut m = module.trim().trim_start_matches(['.', '/']);
    // Peel common alias prefixes until none match.
    for p in ["@/", "~/", "crate::", "self::", "super::"] {
        while let Some(r) = m.strip_prefix(p) {
            m = r;
        }
    }
    if m.is_empty() {
        return None;
    }
    // Dotted / `::`-separated modules (python, rust, java) become path-like.
    let norm: Cow<str> = if !m.contains('/') && (m.contains('.') || m.contains("::")) {
        Cow::Owned(m.replace("::", "/").replace('.', "/"))
    } else {
        Cow::Borrowed(m)
    };
    // `github.com/acme/proj/internal/x` must match dir `proj/internal/x` or
    // `internal/x`, but never a lone `x`, otherwise `context`, `errors`, ...
    // would match random project folders.
    let min_parts = norm.split('/').count().min(2);
    let mut cur: &str = &norm;
    loop {
        if let Some(v) = index.get(cur) {
            return Some(v.as_slice());
        }
        let (_, rest) = cur.split_once('/')?;
        if rest.split('/').count() < min_parts {
            return None;
        }
        cur = rest;
    }
}

fn build_file_imports<'a>(
    fd: &'a FileData,
    index: &'a Map<&'a str, Vec<u32>>,
    includes: &IncludeIndex,
    path: &str,
) -> FileImports<'a> {
    let mut out = FileImports::default();
    if fd.language == "php" {
        out.includes = php::includes_of(fd, includes, path);
        return out;
    }
    for imp in &fd.imports {
        let target = resolve_module(&imp.module, index);
        for item in &imp.items {
            // A named import can appear either as a bare call (`foo()`) or
            // as the head of a qualified call (`foo.bar()`), so we register
            // it in both maps. `or_insert` keeps the first registration, so
            // a name imported twice does not flip-flop.
            out.names.entry(item.as_str()).or_insert(target);
            out.namespaces.entry(item.as_str()).or_insert(target);
        }
        // `import "a/b/retrieval"` -> `retrieval.X()`;  `import a.b.c` -> `a.b.c.X()`
        let m = imp.module.trim_end_matches('/');
        if let Some(last) = m.rsplit(sep).find(|s| !s.is_empty()) {
            out.namespaces.entry(last).or_insert(target);
        }
        if !m.contains('/') {
            if let Some(first) = m.split(sep).find(|s| !s.is_empty()) {
                out.namespaces.entry(first).or_insert(target);
            }
        }
    }
    out
}

/// Collapse the edges of ONE file's callers: one edge per (caller, target),
/// first call-site line, `conditional` only if *every* call to that target is
/// conditional.
///
/// This is deliberately per-file (not global): it keeps the graph from
/// growing with the number of call sites inside the same function, which
/// matters a lot on machine-generated code. Per file rather than per
/// function because a file can declare one id twice (`#ifdef` branches,
/// PHP's `if (...) { function f() {} } else { function f() {} }`), and both
/// declarations are the same graph node.
fn flush_edges(out: &mut Vec<RawEdge>, buf: &mut Vec<RawEdge>) {
    buf.sort_unstable_by_key(|e| (e.from, e.to, e.line));
    let mut it = buf.drain(..);
    if let Some(mut cur) = it.next() {
        for e in it {
            if (e.from, e.to) == (cur.from, cur.to) {
                cur.conditional &= e.conditional;
            } else {
                out.push(cur);
                cur = e;
            }
        }
        out.push(cur);
    }
}

/// Rank how well a candidate of kind `k` fits a call site of shape `want`.
/// Lower is better. `None` means "this candidate cannot satisfy the call
/// site at all" and is dropped.
fn rank(want: Want, k: Kind, strict: bool) -> Option<u8> {
    use Kind::*;
    match (want, k) {
        (Want::Class, Class) => Some(0),
        (Want::Class, _) => None,
        (Want::Call, Function) => Some(0),
        (Want::Call, Class) => Some(1),
        (Want::Call, Method) => (!strict).then_some(2),
        (Want::MethodCall, Method) => Some(0),
        (Want::MethodCall, Function) => Some(1),
        (Want::MethodCall, Class) => Some(2),
    }
}

/// `rank` for a `Seek`: the one accepted kind ranks first, any other is dropped.
fn rank_seek(seek: Seek, k: Kind, strict: bool) -> Option<u8> {
    match seek {
        Seek::Shape(want) => rank(want, k, strict),
        Seek::Only(only) => (k == only).then_some(0),
    }
}

struct Resolver<'a> {
    nodes: &'a [Node<'a>],
    node_map: &'a Map<&'a str, u32>,
    /// bare name -> node indices, ascending (=> also ascending by file).
    by_name: &'a Map<&'a str, Vec<u32>>,
    files: &'a [&'a str],
    /// file idx -> dir id; non-decreasing because files are sorted by (dir, path)
    file_dir: &'a [u32],
    imports: &'a [FileImports<'a>],
    /// file idx -> written in PHP
    php: &'a [bool],
    /// `extends` and traits of PHP classes, for PHP's lookup order.
    php_hierarchy: PhpHierarchy,
    /// (class idx, method name, method idx), sorted — enables binary search
    /// for "does this class define this method".
    methods: &'a [(u32, &'a str, u32)],
    parents: Map<u32, Vec<u32>>,
    children: Map<u32, Vec<u32>>,
    max_ambiguous: usize,
    strict_imports: bool,
}

impl<'a> Resolver<'a> {
    /// Best candidate + how many were eligible to be picked.
    ///
    /// `closeness` is off in tiers where the candidate set is already
    /// constrained (same file, same package) — the tie-break would just be
    /// noise and costs a byte-scan per candidate.
    fn pick(
        &self,
        cands: impl Iterator<Item = u32>,
        want: impl Into<Seek>,
        strict: bool,
        file: u32,
        closeness: bool,
    ) -> (Option<u32>, usize) {
        let want = want.into();
        let mut best: Option<(u8, bool, Reverse<usize>, u32)> = None;
        let mut eligible = 0usize;
        for c in cands {
            let n = &self.nodes[c as usize];
            // PHP and the other languages never call into each other.
            if self.php[n.file as usize] != self.php[file as usize] {
                continue;
            }
            let Some(r) = rank_seek(want, n.kind, strict) else {
                continue;
            };
            eligible += 1;
            let close = if closeness {
                common_dir_prefix(self.files[file as usize], self.files[n.file as usize])
            } else {
                0
            };
            // Sort by (rank, fallback last, close desc, idx asc) — the last
            // term keeps the result deterministic when the rest ties.
            let key = (r, n.fallback, Reverse(close), c);
            if best.is_none_or(|b| key < b) {
                best = Some(key);
            }
        }
        (best.map(|k| k.3), eligible)
    }

    /// A function declared in `caller`'s body: only `caller` can call it.
    fn nested_in(&self, caller: &str, callee: &str) -> Option<u32> {
        let (scope, file) = caller.split_once("::")?;
        let &i = self
            .node_map
            .get(format!("{scope}.{callee}::{file}").as_str())?;
        self.nodes[i as usize].nested.then_some(i)
    }

    /// Restrict a name lookup to files belonging to a resolved import.
    /// `fs` is sorted, so the inner filter is a binary search per candidate.
    fn lookup_in_files(
        &self,
        name: &str,
        fs: &[u32],
        file: u32,
        want: impl Into<Seek>,
    ) -> Option<u32> {
        let cands = self.by_name.get(name)?;
        let it = cands
            .iter()
            .copied()
            .filter(|&c| fs.binary_search(&self.nodes[c as usize].file).is_ok());
        self.pick(it, want, false, file, true).0
    }

    /// Scope ladder; the first tier that yields a candidate wins:
    /// same file -> explicit import -> included files (PHP) -> same package/dir
    /// -> project-wide (strict). A PHP fallback found in the same dir waits
    /// for the project-wide tier, where a real definition beats it.
    ///
    /// `by_name[name]` is globally sorted by file idx, and file idx is
    /// itself sorted by (dir, path). That lets each tier slice the candidate
    /// list with `partition_point` instead of filtering the whole thing.
    fn lookup(&self, name: &str, file: u32, want: impl Into<Seek>) -> Option<u32> {
        let want = want.into();
        let cands = self.by_name.get(name)?.as_slice();

        // Tier 1: same file.
        let lo = cands.partition_point(|&c| self.nodes[c as usize].file < file);
        let hi = lo + cands[lo..].partition_point(|&c| self.nodes[c as usize].file <= file);
        if let (Some(x), _) = self.pick(cands[lo..hi].iter().copied(), want, false, file, false) {
            return Some(x);
        }

        // Tier 2: explicit import. `Some(None)` means the import resolved
        // to a file outside the project — under `strict_imports` we refuse
        // to fall through to tiers 3/4, since that would just be guessing.
        match self.imports[file as usize].names.get(name) {
            Some(Some(fs)) if let Some(x) = self.lookup_in_files(name, fs, file, want) => {
                return Some(x);
            }
            Some(None) if self.strict_imports => return None,
            _ => {}
        }

        // Tier 2b: PHP files pulled in by include/require, transitively.
        let included = &self.imports[file as usize].includes;
        if !included.is_empty() {
            if let Some(x) = self.lookup_in_files(name, included, file, want) {
                return Some(x);
            }
        }

        // Tier 3: same package/dir. Same partition_point trick, on dir id.
        let d = self.file_dir[file as usize];
        let dir_of_c = |c: u32| self.file_dir[self.nodes[c as usize].file as usize];
        let lo = cands.partition_point(|&c| dir_of_c(c) < d);
        let hi = lo + cands[lo..].partition_point(|&c| dir_of_c(c) <= d);
        // A PHP fallback here only wins if tier 4 has no real definition
        // (WordPress' `wp-admin/includes/noop.php` stubs vs `l10n.php`).
        let mut fallback = None;
        if let (Some(x), _) = self.pick(cands[lo..hi].iter().copied(), want, false, file, false) {
            if !self.nodes[x as usize].fallback {
                return Some(x);
            }
            fallback = Some(x);
        }

        // Tier 4: project-wide, but only if the name is not too generic.
        // Fallbacks don't make a name generic: ten theme copies of
        // `wp_body_open()` under `function_exists` leave core's one definition.
        let is_fallback = |c: &u32| self.nodes[*c as usize].fallback;
        let real = cands.iter().copied().filter(|c| !is_fallback(c));
        if let (Some(x), n) = self.pick(real, want, true, file, true) {
            // Too generic: keep the fallback tier 3 found, if any.
            return if n <= self.max_ambiguous.max(1) {
                Some(x)
            } else {
                fallback
            };
        }
        // No real definition: the fallback nearby, else one elsewhere.
        if fallback.is_some() {
            return fallback;
        }
        let fallbacks = cands.iter().copied().filter(is_fallback);
        match self.pick(fallbacks, want, true, file, true) {
            (Some(x), n) if n <= self.max_ambiguous.max(1) => Some(x),
            _ => None,
        }
    }

    /// Binary search for `class.name` in the sorted methods vector.
    #[inline]
    fn own_method(&self, class: u32, name: &str) -> Option<u32> {
        let i = self
            .methods
            .partition_point(|&(c, n, _)| (c, n) < (class, name));
        match self.methods.get(i) {
            Some(&(c, n, m)) if c == class && n == name => Some(m),
            _ => None,
        }
    }

    /// BFS through `adj` (either parents or children) looking for the first
    /// class that defines `name`. `visited` also acts as a cycle guard;
    /// `MAX_CHAIN` bounds adversarial/deep hierarchies.
    fn walk(&self, start: u32, name: &str, adj: &Map<u32, Vec<u32>>) -> Option<u32> {
        let mut queue: VecDeque<u32> = adj.get(&start)?.iter().copied().collect();
        let mut visited: Vec<u32> = vec![start];
        while let Some(c) = queue.pop_front() {
            if visited.contains(&c) {
                continue;
            }
            visited.push(c);
            if visited.len() > MAX_CHAIN {
                break;
            }
            if let Some(m) = self.own_method(c, name) {
                return Some(m);
            }
            if let Some(next) = adj.get(&c) {
                queue.extend(next.iter().copied());
            }
        }
        None
    }

    /// own methods -> ancestors (full BFS) -> descendants (overrides)
    ///
    /// Ancestors first because inherited methods are the common case;
    /// descendants second so an override in a subclass can also be found
    /// when the call site is on the base (e.g. Python's `super().foo()`).
    fn method_in_hierarchy(&self, class: u32, name: &str) -> Option<u32> {
        self.own_method(class, name)
            .or_else(|| self.walk(class, name, &self.parents))
            .or_else(|| self.walk(class, name, &self.children))
    }

    fn resolve_call(
        &self,
        callee: &str,
        file: u32,
        class: Option<u32>,
        has_self: bool,
    ) -> Option<u32> {
        // Exact full-ID match always wins — no ambiguity possible.
        if let Some(&i) = self.node_map.get(callee) {
            return Some(i);
        }
        if callee.is_empty() || callee.contains('/') {
            return None;
        }
        let (qual, name) = split_callee(callee);
        if name.is_empty() {
            return None;
        }

        // Unqualified: `foo()`
        let Some(q) = qual else {
            // If the enclosing method has a self param, try the class chain
            // first — `foo()` inside a method could be a sibling method.
            if has_self {
                if let Some(m) = class.and_then(|c| self.method_in_hierarchy(c, name)) {
                    return Some(m);
                }
            }
            return self.lookup(name, file, Want::Call);
        };

        // `self.foo()` / `this.bar()` / `cls.baz()`
        if is_self_qualifier(q) {
            if let Some(m) = class.and_then(|c| self.method_in_hierarchy(c, name)) {
                return Some(m);
            }
            return self.lookup(name, file, Want::MethodCall);
        }

        // pkg.Func() / module.func(): look up the head of the qualifier in
        // the file's namespace imports and, if it maps to project files,
        // resolve `name` inside those files only.
        let root = q.split(sep).next().unwrap_or(q);
        match self.imports[file as usize].namespaces.get(root) {
            Some(Some(fs)) if let Some(x) = self.lookup_in_files(name, fs, file, Want::Call) => {
                return Some(x);
            }
            Some(None) if self.strict_imports => {
                return None; // fmt.Println, os.path.join, ...
            }
            _ => {}
        }

        // Class.staticMethod() / Class::method(): resolve the qualifier as
        // a class first, then search that class's method hierarchy.
        let q_last = q.rsplit(sep).next().unwrap_or(q);
        if let Some(cls) = self.lookup(q_last, file, Want::Class) {
            if let Some(m) = self.method_in_hierarchy(cls, name) {
                return Some(m);
            }
        }
        self.lookup(name, file, Want::MethodCall)
    }

    fn resolve_base(&self, base: &str, file: u32) -> Option<u32> {
        if let Some(&i) = self.node_map.get(base) {
            return Some(i);
        }
        // `Base<T>` / `Base[]` / `Base(args)` -> `Base`. Generic args and
        // array suffixes are irrelevant to *which class* the base refers to.
        let base = base.split(['<', '[', '(']).next().unwrap_or(base).trim();
        let (qual, name) = split_callee(base);
        if let Some(q) = qual {
            let root = q.split(sep).next().unwrap_or(q);
            if let Some(Some(fs)) = self.imports[file as usize].namespaces.get(root) {
                if let Some(x) = self.lookup_in_files(name, fs, file, Want::Class) {
                    return Some(x);
                }
            }
        }
        self.lookup(name, file, Want::Class)
    }
}

// v2 exists because v1's global-first-match symbol index gets calls
// wrong whenever two unrelated classes define a method with the same
// short name common enough in real codebases (`__init__`, `run`,
// `handle`) that a class-aware, scope-aware resolver earns its extra
// cost for callers who need accuracy over raw speed.
pub fn build_call_graph_v2(structure: &BTreeMap<String, FileData>) -> CallGraph {
    build_with(structure, &CallGraphOptions::default())
}

pub fn build_with(structure: &BTreeMap<String, FileData>, opts: &CallGraphOptions) -> CallGraph {
    if structure.is_empty() {
        return CallGraph {
            nodes: Vec::new(),
            edges: Vec::new(),
        };
    }

    // 0. Deterministic file order, grouped by directory.
    //
    // Two invariants come out of this:
    //   * `paths` is sorted by (dir, path), which means file indices are
    //     grouped by directory. `file_dir` is therefore non-decreasing,
    //     which is what the partition_point tiers in `lookup` rely on.
    //   * "First wins" on duplicate IDs is deterministic across runs.
    let mut files: Vec<(&str, &FileData)> =
        structure.iter().map(|(k, v)| (k.as_str(), v)).collect();
    files.sort_unstable_by(|a, b| (dir_of(a.0), a.0).cmp(&(dir_of(b.0), b.0)));
    let paths: Vec<&str> = files.iter().map(|f| f.0).collect();
    let mut file_dir: Vec<u32> = Vec::with_capacity(paths.len());
    {
        let (mut prev, mut id) = (None::<&str>, 0u32);
        for p in &paths {
            let d = dir_of(p);
            if matches!(prev, Some(pd) if pd != d) {
                id += 1;
            }
            prev = Some(d);
            file_dir.push(id);
        }
    }
    // Chunking matters here: one Rayon task per file would spend most of
    // its time on scheduling. `threads * 8` gives the pool enough work to
    // stay fed while keeping per-task `Vec`s small enough for cache.
    let threads = rayon::current_num_threads().max(1);
    let chunk = (files.len() / (threads * 8)).clamp(16, 2048);
    let module_index = build_module_index(&paths);
    let include_index = if files.iter().any(|f| f.1.language == "php") {
        IncludeIndex::new(&paths)
    } else {
        IncludeIndex::default()
    };

    // 1. Nodes, class->base refs and resolved imports (parallel, order-preserving).
    let extracts: Vec<Extract> = files
        .par_chunks(chunk)
        .enumerate()
        .map(|(ci, slice)| {
            let mut ex = Extract {
                nodes: Vec::with_capacity(slice.len() * 8),
                bases: Vec::new(),
                php_extends: Vec::new(),
                php_traits: Vec::new(),
                imports: Vec::with_capacity(slice.len()),
            };
            for (j, &(path, fd)) in slice.iter().enumerate() {
                let f = (ci * chunk + j) as u32;
                ex.imports
                    .push(build_file_imports(fd, &module_index, &include_index, path));
                let php = fd.language == "php";
                for func in &fd.functions {
                    let nested = func.tags.iter().any(|t| t.as_str() == NESTED_TAG);
                    ex.nodes.push(Node {
                        id: &func.id,
                        name: if php {
                            php_name(&func.name, &func.lang_info)
                        } else {
                            Cow::Borrowed(bare_name(&func.id))
                        },
                        kind: if func.id.starts_with("method_") && !nested {
                            Kind::Method
                        } else {
                            Kind::Function
                        },
                        file: f,
                        is_entry: func.tags.iter().any(|t| t.as_str() == ENTRY_TAG),
                        fallback: func.tags.iter().any(|t| t.as_str() == FALLBACK_TAG),
                        php_static: false,
                        owner: None,
                        nested,
                    });
                }
                for class in &fd.classes {
                    ex.nodes.push(Node {
                        id: &class.id,
                        name: if php {
                            php_name(&class.name, &class.lang_info)
                        } else {
                            Cow::Borrowed(bare_name(&class.id))
                        },
                        kind: Kind::Class,
                        file: f,
                        is_entry: false,
                        fallback: false,
                        php_static: false,
                        owner: None,
                        nested: false,
                    });
                    for base in &class.bases {
                        ex.bases.push(BaseRef {
                            class: &class.id,
                            base: base.as_str(),
                            file: f,
                            line: class.line_start as u32,
                        });
                    }
                    if let Some(info) = class.lang_info.php.as_deref().filter(|_| php) {
                        let line = class.line_start as u32;
                        for b in info.extends.iter().take(1) {
                            ex.php_extends.push(BaseRef {
                                class: &class.id,
                                base: b.as_str(),
                                file: f,
                                line,
                            });
                        }
                        for b in &info.traits {
                            ex.php_traits.push(BaseRef {
                                class: &class.id,
                                base: b.as_str(),
                                file: f,
                                line,
                            });
                        }
                    }
                    for m in &class.methods {
                        ex.nodes.push(Node {
                            id: &m.id,
                            // `bare_name` of a `method_{Class}_{name}` id is
                            // `{Class}_{name}`, which no call site spells.
                            name: if php {
                                Cow::Owned(m.name.to_ascii_lowercase())
                            } else {
                                Cow::Borrowed(bare_name(&m.id))
                            },
                            kind: Kind::Method,
                            file: f,
                            is_entry: false,
                            fallback: false,
                            php_static: m.lang_info.php.as_ref().is_some_and(|p| p.is_static),
                            owner: Some(&class.id),
                            nested: false,
                        });
                    }
                }
            }
            ex
        })
        .collect();

    // 2. Sequential merge: first node (in sorted file order) wins a duplicate id.
    //
    // The merge is single-threaded because it populates a global map. It
    // is still fast — all the expensive per-file work already happened in
    // step 1. If two parser outputs disagree on an ID, the earlier file
    // (per sort order) wins, so the result is deterministic.
    let mut nodes: Vec<Node> = Vec::new();
    let mut node_map: Map<&str, u32> = Map::new();
    let mut base_refs: Vec<BaseRef> = Vec::new();
    let mut php_extends: Vec<BaseRef> = Vec::new();
    let mut php_traits: Vec<BaseRef> = Vec::new();
    let mut imports: Vec<FileImports> = Vec::with_capacity(files.len());
    for ex in extracts {
        for n in ex.nodes {
            if let Entry::Vacant(v) = node_map.entry(n.id) {
                v.insert(nodes.len() as u32);
                nodes.push(n);
            }
        }
        base_refs.extend(ex.bases);
        php_extends.extend(ex.php_extends);
        php_traits.extend(ex.php_traits);
        imports.extend(ex.imports);
    }
    if imports.iter().any(|i| !i.includes.is_empty()) {
        let direct: Vec<Vec<u32>> = imports
            .iter_mut()
            .map(|i| std::mem::take(&mut i.includes))
            .collect();
        for (i, closure) in imports.iter_mut().zip(include_closures(&direct)) {
            i.includes = closure;
        }
    }

    let php: Vec<bool> = files.iter().map(|f| f.1.language == "php").collect();
    let mut by_name: Map<&str, Vec<u32>> = Map::with_capacity(nodes.len());
    for (i, n) in nodes.iter().enumerate().filter(|(_, n)| !n.nested) {
        by_name.entry(n.name.as_ref()).or_default().push(i as u32);
    }
    // Class-method index, sorted for binary search by (class, name).
    let mut methods: Vec<(u32, &str, u32)> = nodes
        .iter()
        .enumerate()
        .filter_map(|(i, n)| {
            let owner = *node_map.get(n.owner?)?;
            Some((owner, n.name.as_ref(), i as u32))
        })
        .collect();
    methods.sort_unstable();

    let mut r = Resolver {
        nodes: &nodes,
        node_map: &node_map,
        by_name: &by_name,
        files: &paths,
        file_dir: &file_dir,
        imports: &imports,
        php: &php,
        php_hierarchy: PhpHierarchy::default(),
        methods: &methods,
        parents: Map::new(),
        children: Map::new(),
        max_ambiguous: opts.max_ambiguous,
        strict_imports: opts.strict_imports,
    };

    // 3. Inheritance edges, then the class hierarchy used by method lookup.
    //
    // Inheritance has to be resolved and materialised before calls, because
    // `resolve_call` walks `parents`/`children` to find inherited methods.
    // The edges are also sorted and deduped by (from, to) before being
    // stored — a class inheriting the same base twice (impossible in
    // valid source, possible in sloppy parser output) becomes one edge.
    let inherit: Vec<RawEdge> = {
        let mut v: Vec<RawEdge> = base_refs
            .par_iter()
            .filter_map(|b| {
                let child = *r.node_map.get(b.class)?;
                let parent = if r.php[b.file as usize] {
                    r.php_class(b.base, b.file)?
                } else {
                    r.resolve_base(b.base, b.file)?
                };
                (child != parent).then_some(RawEdge {
                    from: child,
                    to: parent,
                    line: b.line,
                    kind: EDGE_INHERIT,
                    conditional: false,
                })
            })
            .collect();
        v.sort_unstable_by_key(|e| (e.from, e.to, e.line));
        v.dedup_by_key(|e| (e.from, e.to));
        v
    };
    for e in &inherit {
        r.parents.entry(e.from).or_default().push(e.to);
        r.children.entry(e.to).or_default().push(e.from);
    }
    let php_hierarchy = PhpHierarchy::new(&r, &php_extends, &php_traits);
    r.php_hierarchy = php_hierarchy;

    // 4. Call edges.
    //
    // Parallel across chunks again. Each chunk writes into its own `out`
    // and `buf`, so no locks are needed. `flush_edges` collapses multiple
    // calls to the same target from one caller into a single edge, once per
    // file.
    let call_edges: Vec<RawEdge> = files
        .par_chunks(chunk)
        .enumerate()
        .flat_map_iter(|(ci, slice)| {
            let mut out: Vec<RawEdge> = Vec::new();
            let mut buf: Vec<RawEdge> = Vec::new();
            for (j, &(_, fd)) in slice.iter().enumerate() {
                let f = (ci * chunk + j) as u32;
                let php = r.php[f as usize];
                for func in &fd.functions {
                    let Some(&from) = r.node_map.get(func.id.as_str()) else {
                        continue;
                    };
                    for call in &func.calls {
                        if !php && is_param(&func.params, &call.callee) {
                            continue;
                        }
                        let to = if php {
                            r.resolve_php_call(
                                &call.callee,
                                f,
                                None,
                                php_namespace(&func.lang_info),
                            )
                        } else {
                            r.nested_in(&func.id, &call.callee)
                                .or_else(|| r.resolve_call(&call.callee, f, None, false))
                        };
                        if let Some(to) = to {
                            buf.push(RawEdge {
                                from,
                                to,
                                line: call.line as u32,
                                kind: EDGE_CALL,
                                conditional: call.is_conditional,
                            });
                        }
                    }
                }
                for class in &fd.classes {
                    let Some(&cidx) = r.node_map.get(class.id.as_str()) else {
                        continue;
                    };
                    for m in &class.methods {
                        let Some(&from) = r.node_map.get(m.id.as_str()) else {
                            continue;
                        };
                        // `has_self` enables the "is this a sibling method?"
                        // shortcut in `resolve_call`.
                        let has_self = first_self_param(&m.params);
                        for call in &m.calls {
                            if !php && is_param(&m.params, &call.callee) {
                                continue;
                            }
                            let to = if php {
                                let ns = php_namespace(&m.lang_info);
                                r.resolve_php_call(&call.callee, f, Some(cidx), ns)
                            } else {
                                r.nested_in(&m.id, &call.callee).or_else(|| {
                                    r.resolve_call(&call.callee, f, Some(cidx), has_self)
                                })
                            };
                            if let Some(to) = to {
                                buf.push(RawEdge {
                                    from,
                                    to,
                                    line: call.line as u32,
                                    kind: EDGE_CALL,
                                    conditional: call.is_conditional,
                                });
                            }
                        }
                    }
                }
                flush_edges(&mut out, &mut buf);
            }
            out.into_iter()
        })
        .collect();

    // 5. Materialise output.
    //
    // `call_count_estimate` is the number of *resolved* call edges that
    // point at the node. It ignores inheritance edges on purpose — for
    // "hot spot" queries what matters is callers, not subclasses.
    let mut in_calls = vec![0u32; nodes.len()];
    for e in &call_edges {
        in_calls[e.to as usize] += 1;
    }
    let out_nodes: Vec<CallGraphNode> = nodes
        .par_iter()
        .enumerate()
        .map(|(i, n)| CallGraphNode {
            id: n.id.to_string(),
            node_type: match n.kind {
                Kind::Function => "function",
                Kind::Method => "method",
                Kind::Class => "class",
            }
            .to_string(),
            file: paths[n.file as usize].to_string(),
            is_entry_point: n.is_entry,
            call_count_estimate: in_calls[i] as usize,
        })
        .collect();
    let out_edges: Vec<CallGraphEdge> = inherit
        .par_iter()
        .chain(call_edges.par_iter())
        .map(|e| CallGraphEdge {
            from: nodes[e.from as usize].id.to_string(),
            to: nodes[e.to as usize].id.to_string(),
            edge_type: if e.kind == EDGE_INHERIT {
                "inheritance"
            } else {
                "call"
            }
            .to_string(),
            conditional: e.conditional,
            call_site_line: e.line as usize,
        })
        .collect();

    CallGraph {
        nodes: out_nodes,
        edges: out_edges,
    }
}

pub fn populate_called_by_from_graph(kb: &mut KnowledgeBase) {
    // Reverse edges are built from the *materialised* graph rather than
    // re-running resolution, so this can run in a separate pass after the
    // graph has been written to disk and reloaded.
    let g = &kb.call_graph;
    let file_of: HashMap<&str, &str> = g
        .nodes
        .iter()
        .map(|n| (n.id.as_str(), n.file.as_str()))
        .collect();
    let mut rev: HashMap<&str, Vec<CallerInfo>> = HashMap::new();
    for e in g.edges.iter().filter(|e| e.edge_type == "call") {
        rev.entry(e.to.as_str()).or_default().push(CallerInfo {
            function: e.from.clone(),
            file: file_of
                .get(e.from.as_str())
                .map_or_else(String::new, |f| f.to_string()),
            line: e.call_site_line,
        });
    }
    for fd in kb.structure.values_mut() {
        for f in &mut fd.functions {
            f.called_by = rev.remove(f.id.as_str()).unwrap_or_default();
        }
        for c in &mut fd.classes {
            for m in &mut c.methods {
                m.called_by = rev.remove(m.id.as_str()).unwrap_or_default();
            }
        }
    }
}

#[cfg(test)]
mod tests {
    // #![expect(clippy::expect_used)]
    // #![expect(clippy::unwrap_used)]

    use super::*;

    mod first_self_param_tests {
        use super::*;

        fn p(name: &str) -> Parameter {
            Parameter {
                name: name.to_string(),
                type_annotation: String::new(),
                default_value: None,
            }
        }

        #[test]
        fn recognizes_all_known_receiver_spellings() {
            for name in ["self", "this", "cls", "_self", "self_", "myself"] {
                assert!(first_self_param(&[p(name)]), "{name} should be recognized");
            }
        }

        #[test]
        fn empty_params_is_false() {
            assert!(!first_self_param(&[]));
        }

        #[test]
        fn only_the_first_param_is_checked() {
            // a receiver-like name in second position must NOT count
            assert!(!first_self_param(&[p("other"), p("self")]));
        }

        #[test]
        fn case_sensitivity_gap() {
            assert!(!first_self_param(&[p("Self")]));
            assert!(!first_self_param(&[p("This")]));
            assert!(!first_self_param(&[p("Cls")]));
        }

        #[test]
        fn near_miss_spellings_rejected() {
            for name in ["self1", "SELF", "s_elf", " self", "self ", "__self__"] {
                assert!(!first_self_param(&[p(name)]), "{name} should NOT match");
            }
        }
    }

    #[test]
    fn caller_parameter_shadowing_global_function_produces_no_edge() {
        use crate::grammar_files::parse_source;
        use std::path::Path;

        let cases = [
            (
                "app.py",
                "def helper():\n    return 1\n\ndef dynamic(first, helper):\n    return helper()\n\ndef direct():\n    return helper()\n",
            ),
            (
                "app.ts",
                "export function helper(): number { return 1; }\nexport function dynamic(first: number, helper: () => number): number { return helper(); }\nexport function direct(): number { return helper(); }\n",
            ),
            (
                "app.rs",
                "pub fn helper() -> i32 { 1 }\npub fn dynamic(_first: i32, helper: fn() -> i32) -> i32 { helper() }\npub fn direct() -> i32 { helper() }\n",
            ),
        ];

        for (file, src) in cases {
            let (_, fd) = parse_source(Path::new(file), src).unwrap();
            let mut structure = BTreeMap::new();
            structure.insert(file.to_string(), fd);
            let cg = build_call_graph_v2(&structure);

            let dynamic_id = format!("func_dynamic::{file}");
            let direct_id = format!("func_direct::{file}");
            let helper_id = format!("func_helper::{file}");

            assert!(
                cg.edges
                    .iter()
                    .any(|e| e.from == direct_id && e.to == helper_id),
                "expected direct -> helper in {file}, got {:?}",
                cg.edges
            );
            assert!(
                !cg.edges
                    .iter()
                    .any(|e| e.from == dynamic_id && e.to == helper_id),
                "shadowed param in {file} must not resolve to global helper: {:?}",
                cg.edges
            );
        }
    }
}
