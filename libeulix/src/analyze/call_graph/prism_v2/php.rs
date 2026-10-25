// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
//! PHP resolution for PRISM v2.
//!
//! PHP names its targets more precisely than the other languages: the parser
//! writes class names fully qualified, each call shape (`foo()`,
//! `$x->foo()`, `X::foo()`, `new X`) reaches only one kind of target, and the
//! files a script pulls in with `include`/`require` add a tier to the scope
//! ladder. This module holds what that takes: the include index, the names
//! PHP nodes are indexed by, and `Resolver::resolve_php_call`. `build_with`
//! routes PHP files here.

use super::{
    clean_path, common_dir_prefix, dir_of, split_callee, BaseRef, Kind, Map, Resolver, Seek,
    MAX_CHAIN,
};
use crate::struc::kb_struct::{FileData, LanguageSpecificInfo};
use rayon::prelude::*;
use std::borrow::Cow;
use std::collections::{HashSet, VecDeque};

/// Cap on the files reachable from one PHP file through `include`/`require`.
/// A bootstrap file that pulls in the whole project would otherwise give
/// every page a closure the size of the project.
const MAX_INCLUDES: usize = 2048;

/// Index name of a PHP function or class: its lowercase qualified name
/// (`app\\models\\user`), since PHP names are case-insensitive.
pub(super) fn php_name<'a>(name: &str, info: &LanguageSpecificInfo) -> Cow<'a, str> {
    let name = match info.php.as_ref().and_then(|p| p.namespace.as_deref()) {
        Some(ns) => format!("{}\\{}", ns, name),
        None => name.to_string(),
    };
    Cow::Owned(name.to_ascii_lowercase())
}

pub(super) fn php_namespace(info: &LanguageSpecificInfo) -> Option<&str> {
    info.php.as_ref()?.namespace.as_deref()
}

// Include index
//
// `include`/`require` name a file, not a module, so they get exact path
// lookups instead of the suffix-of-stem module index:
//   1. relative to the including file, then relative to the project root;
//   2. failing that (include_path, a document-root prefix, a constant or a
//      variable in front of the literal), the file whose path shares the most
//      trailing segments with the literal; ties go to the file closest to the
//      includer, and a tie that remains resolves to nothing.
#[derive(Default)]
pub(super) struct IncludeIndex<'a> {
    paths: &'a [&'a str],
    by_path: Map<&'a str, u32>,
    /// file name -> files
    by_name: Map<&'a str, Vec<u32>>,
}

/// Collapse `.` and `..`; `None` when the path climbs above the root.
fn normalize_path(p: &str) -> Option<String> {
    let mut out: Vec<&str> = Vec::new();
    for seg in p.split('/') {
        match seg {
            "" | "." => {}
            ".." => {
                out.pop()?;
            }
            s => out.push(s),
        }
    }
    Some(out.join("/"))
}

/// The literal path at the end of a dynamic include:
/// `$_SERVER['DOCUMENT_ROOT'] . '/lib/db.php'` -> `/lib/db.php`,
/// `"$dir/header.php"` -> `/header.php`. `None` if no file name survives
/// (`$name . '.php'`).
fn include_tail(expr: &str) -> Option<&str> {
    let lower = expr.to_ascii_lowercase();
    let end = [".php", ".inc", ".phtml"]
        .iter()
        .filter_map(|ext| lower.rfind(ext).map(|i| i + ext.len()))
        .max()?;
    let is_path = |c: char| c.is_alphanumeric() || matches!(c, '_' | '-' | '.' | '/');
    let mut start = expr[..end]
        .char_indices()
        .rev()
        .find(|&(_, c)| !is_path(c))
        .map_or(0, |(i, c)| i + c.len_utf8());
    // `$dir/x.php`: the leading identifier belongs to the variable.
    if expr[..start].ends_with('$') {
        start += expr[start..end]
            .find(|c: char| !(c.is_ascii_alphanumeric() || c == '_'))
            .unwrap_or(end - start);
    }
    let tail = &expr[start..end];
    let file = tail.rsplit('/').next().unwrap_or(tail);
    (!file.starts_with('.')).then_some(tail)
}

impl<'a> IncludeIndex<'a> {
    pub(super) fn new(paths: &'a [&'a str]) -> Self {
        let mut idx = IncludeIndex {
            paths,
            by_path: Map::with_capacity(paths.len()),
            by_name: Map::with_capacity(paths.len()),
        };
        for (i, &p) in paths.iter().enumerate() {
            let p = clean_path(p);
            idx.by_path.insert(p, i as u32);
            let name = p.rsplit('/').next().unwrap_or(p);
            idx.by_name.entry(name).or_default().push(i as u32);
        }
        idx
    }

    fn resolve(&self, from: &str, module: &str, dynamic: bool) -> Option<u32> {
        let module = module.replace('\\', "/");
        if !dynamic {
            let dir = dir_of(from);
            // An absolute path (`/var/www/lib/x.php`) does not depend on the
            // includer's directory.
            let relative = if dir.is_empty() || module.starts_with('/') {
                module.clone()
            } else {
                format!("{}/{}", dir, module)
            };
            for path in [normalize_path(&relative), normalize_path(&module)]
                .into_iter()
                .flatten()
            {
                if let Some(&f) = self.by_path.get(path.as_str()) {
                    return Some(f);
                }
            }
        }
        let tail = if dynamic {
            include_tail(&module)?
        } else {
            module.as_str()
        };
        self.by_suffix(from, tail)
    }

    fn by_suffix(&self, from: &str, tail: &str) -> Option<u32> {
        let tail = tail.rsplit("../").next().unwrap_or(tail);
        let segs: Vec<&str> = tail
            .split('/')
            .filter(|s| !s.is_empty() && *s != ".")
            .collect();
        let cands = self.by_name.get(*segs.last()?)?;
        let mut best: Option<((usize, usize), u32)> = None;
        let mut tied = false;
        for &c in cands {
            let path = clean_path(self.paths[c as usize]);
            let shared = path
                .rsplit('/')
                .zip(segs.iter().rev())
                .take_while(|(a, b)| a == *b)
                .count();
            let key = (shared, common_dir_prefix(from, path));
            match best {
                Some((b, _)) if key < b => {}
                Some((b, _)) if key == b => tied = true,
                _ => {
                    best = Some((key, c));
                    tied = false;
                }
            }
        }
        best.filter(|_| !tied).map(|(_, c)| c)
    }
}

/// Every file reachable from each file through its includes, nearest first,
/// capped at `MAX_INCLUDES`, then sorted for `lookup_in_files`.
pub(super) fn include_closures(direct: &[Vec<u32>]) -> Vec<Vec<u32>> {
    direct
        .par_iter()
        .enumerate()
        .map(|(f, first)| {
            if first.is_empty() {
                return Vec::new();
            }
            let mut seen: HashSet<u32> = HashSet::from([f as u32]);
            let mut queue: VecDeque<u32> = first.iter().copied().collect();
            let mut out = Vec::new();
            while let Some(x) = queue.pop_front() {
                if !seen.insert(x) {
                    continue;
                }
                out.push(x);
                if out.len() >= MAX_INCLUDES {
                    break;
                }
                queue.extend(direct[x as usize].iter().copied());
            }
            out.sort_unstable();
            out
        })
        .collect()
}

/// The files a PHP file includes, resolved and sorted. PHP `use` imports are
/// already applied by the parser, which writes class names fully qualified,
/// so includes are all that is left to resolve.
pub(super) fn includes_of(fd: &FileData, includes: &IncludeIndex, path: &str) -> Vec<u32> {
    let mut out: Vec<u32> = fd
        .imports
        .iter()
        .filter_map(|imp| {
            let dynamic = match imp.import_type.as_str() {
                "include" => false,
                "include_dynamic" => true,
                _ => return None,
            };
            includes.resolve(path, &imp.module, dynamic)
        })
        .collect();
    out.sort_unstable();
    out.dedup();
    out
}

/// The PHP side of the class hierarchy. PHP looks a method up in the class,
/// then in the traits it uses, then along the `extends` chain; interfaces
/// only declare. The generic `parents` map holds all of these as bases, in
/// no particular order.
#[derive(Default)]
pub(super) struct PhpHierarchy {
    /// class -> the class it `extends`
    parent: Map<u32, u32>,
    /// class or trait -> the traits it uses
    traits: Map<u32, Vec<u32>>,
    /// every class-like used as a trait
    is_trait: HashSet<u32>,
}

impl PhpHierarchy {
    /// Resolves the `extends` and trait references `build_with` collected.
    pub(super) fn new(r: &Resolver, extends: &[BaseRef], traits: &[BaseRef]) -> Self {
        let resolve = |b: &BaseRef| {
            let child = *r.node_map.get(b.class)?;
            let parent = r.php_class(b.base, b.file)?;
            (child != parent).then_some((child, parent))
        };
        let mut h = PhpHierarchy {
            parent: extends.iter().filter_map(resolve).collect(),
            ..Default::default()
        };
        for (class, t) in traits.iter().filter_map(resolve) {
            h.traits.entry(class).or_default().push(t);
            h.is_trait.insert(t);
        }
        h
    }
}

impl Resolver<'_> {
    /// PHP call sites, resolved by PHP's own rules. The parser writes class
    /// names fully qualified (`use` aliases and the current namespace already
    /// applied), and PHP nodes are indexed by lowercase qualified name.
    ///
    ///   `foo()`                    the namespace's `foo`, else the global one
    ///   `\App\f()`                 exactly that function
    ///   `new \App\User`            exactly that class
    ///   `\App\User::m()`, typed `\App\User.m()`, `self::`, `static::`,
    ///   `this.m()`                 `m` in that class's hierarchy
    ///   `parent::m()`              `m` from the `extends` class up
    ///   `$x.m()`, chains           the project's only method `m`, and never
    ///                              a static one from `->`
    ///
    /// A known class without `m` goes to its `__call` / `__callStatic`; a
    /// class the project does not declare (PHP built-ins, vendor code) gets
    /// no edge rather than a same-named guess.
    pub(super) fn resolve_php_call(
        &self,
        callee: &str,
        file: u32,
        class: Option<u32>,
        namespace: Option<&str>,
    ) -> Option<u32> {
        if let Some(name) = callee.strip_prefix("new ") {
            return self.php_class(name, file);
        }
        if let Some(name) = callee.strip_prefix("parent::") {
            let parent = *self.php_hierarchy.parent.get(&class?)?;
            // Upwards only: below the parent sits the caller itself.
            return self.php_upward(parent, &name.to_ascii_lowercase());
        }
        let (qual, name) = split_callee(callee);
        // `$this->$method()`, `$fn()`: the name is only known at runtime.
        if name.starts_with(['$', '{']) {
            return None;
        }
        let name = name.to_ascii_lowercase();

        let Some(q) = qual else {
            if let Some(qualified) = name.strip_prefix('\\') {
                return self.lookup(qualified, file, Seek::Only(Kind::Function));
            }
            // `B\foo` (no leading `\`): an unqualified `foo()` the parser saw
            // in namespace B, which is not the caller's own (`{main}` spans
            // every namespace of its file).
            let (local, global) = match name.rsplit_once('\\') {
                Some((_, short)) => (Some(name.clone()), short),
                None => (
                    namespace.map(|ns| format!("{}\\{}", ns.to_ascii_lowercase(), name)),
                    name.as_str(),
                ),
            };
            if let Some(f) = local.and_then(|l| self.lookup(&l, file, Seek::Only(Kind::Function))) {
                return Some(f);
            }
            return self.lookup(global, file, Seek::Only(Kind::Function));
        };

        let is_static = callee[q.len()..].starts_with("::");
        // `$this->m()` and `static::m()` dispatch at runtime, so an override
        // in a subclass may run; `X::m()` and `self::m()` only look upwards,
        // except in a trait, where `self` is the class using it.
        let in_trait = class.is_some_and(|c| self.php_hierarchy.is_trait.contains(&c));
        let dispatched = !is_static || q == "static" || q == "self" && in_trait;
        match q {
            "this" | "self" | "static" => match class {
                Some(c) => self.php_method(c, &name, is_static, dispatched),
                // `$this` in a template included from a method: the class is
                // not visible from here.
                None => self.php_method_by_name(&name, file, is_static),
            },
            q if q.starts_with('\\') && !q.contains([':', '.', '(', '$']) => {
                let c = self.php_class(q, file)?;
                self.php_method(c, &name, is_static, dispatched)
            }
            _ => self.php_method_by_name(&name, file, is_static),
        }
    }

    /// A method called on a receiver of unknown class, matched by name: the
    /// one PHP method of that name in the project, if there is exactly one.
    /// A name shared by unrelated classes is a coin toss (measured at 25-55%
    /// right on WordPress and Laravel), and still 61% when only one of them
    /// sits in the caller's file, includes or directory, so it gets no edge.
    /// Static methods count towards the ambiguity, but `$x->m()` never
    /// resolves to one: a mock's `shouldReceive()` is not the facade's.
    fn php_method_by_name(&self, name: &str, file: u32, is_static: bool) -> Option<u32> {
        let cands = self.by_name.get(name)?;
        match self.pick(
            cands.iter().copied(),
            Seek::Only(Kind::Method),
            true,
            file,
            false,
        ) {
            (Some(x), 1) if is_static || !self.nodes[x as usize].php_static => Some(x),
            _ => None,
        }
    }

    /// The project class with this fully qualified name, if any.
    pub(super) fn php_class(&self, name: &str, file: u32) -> Option<u32> {
        let key = name.trim().trim_start_matches('\\').to_ascii_lowercase();
        self.lookup(&key, file, Seek::Only(Kind::Class))
    }

    /// `name` in `class` or above it (and, for a dispatched call, its
    /// subclasses' overrides), else the magic method PHP falls back to.
    fn php_method(&self, class: u32, name: &str, is_static: bool, dispatched: bool) -> Option<u32> {
        let find = |name: &str| {
            let up = self.php_upward(class, name);
            if dispatched {
                up.or_else(|| self.walk(class, name, &self.children))
            } else {
                up
            }
        };
        let magic = if is_static { "__callstatic" } else { "__call" };
        find(name).or_else(|| find(magic))
    }

    /// `name` as PHP finds it from `class` upwards: the class's own methods,
    /// then its traits', then the same along the `extends` chain. Interfaces,
    /// which only declare, come last.
    fn php_upward(&self, class: u32, name: &str) -> Option<u32> {
        let mut cur = Some(class);
        for _ in 0..MAX_CHAIN {
            let Some(c) = cur else { break };
            if let Some(m) = self
                .own_method(c, name)
                .or_else(|| self.php_trait_method(c, name))
            {
                return Some(m);
            }
            cur = self.php_hierarchy.parent.get(&c).copied();
        }
        self.walk(class, name, &self.parents)
    }

    /// `name` in the traits `class` uses, and in the traits those use.
    fn php_trait_method(&self, class: u32, name: &str) -> Option<u32> {
        let traits = &self.php_hierarchy.traits;
        let mut queue: VecDeque<u32> = traits.get(&class)?.iter().copied().collect();
        let mut seen = vec![class];
        while let Some(t) = queue.pop_front() {
            if seen.contains(&t) {
                continue;
            }
            seen.push(t);
            if seen.len() > MAX_CHAIN {
                break;
            }
            if let Some(m) = self.own_method(t, name) {
                return Some(m);
            }
            if let Some(next) = traits.get(&t) {
                queue.extend(next.iter().copied());
            }
        }
        None
    }
}

#[cfg(test)]
mod tests {
    #![expect(clippy::expect_used)]

    use super::*;
    use crate::grammar_files::php::fixtures::{CART_PAGE, HELPERS, MODELS, SERVICE};
    use crate::grammar_files::php::parse_source;
    use crate::struc::kb_struct::CallGraph;
    use std::collections::HashMap;
    use std::path::Path;

    fn graph(files: &[(&str, &str)]) -> CallGraph {
        let structure: HashMap<String, FileData> = files
            .iter()
            .map(|(path, src)| {
                let fd = parse_source(Path::new(path), src)
                    .expect("parse should succeed")
                    .1;
                (path.to_string(), fd)
            })
            .collect();
        super::super::build_call_graph_v2(&structure)
    }

    fn edge(g: &CallGraph, from: &str, to: &str) -> bool {
        g.edges
            .iter()
            .any(|e| e.edge_type == "call" && e.from == from && e.to == to)
    }

    fn targets<'a>(g: &'a CallGraph, from: &str) -> Vec<&'a str> {
        g.edges
            .iter()
            .filter(|e| e.edge_type == "call" && e.from == from)
            .map(|e| e.to.as_str())
            .collect()
    }

    #[test]
    fn literal_tail_of_dynamic_includes() {
        assert_eq!(
            include_tail("$_SERVER['DOCUMENT_ROOT'] . '/lib/db.php'"),
            Some("/lib/db.php")
        );
        assert_eq!(include_tail("\"$dir/header.php\""), Some("/header.php"));
        assert_eq!(include_tail("{$dir}/header.inc"), Some("/header.inc"));
        assert_eq!(
            include_tail("PATH_LIB . 'Mail.class.PHP'"),
            Some("Mail.class.PHP")
        );
        assert_eq!(
            include_tail("ROOT . '/relatórios/mensal.php'"),
            Some("/relatórios/mensal.php")
        );
        assert_eq!(include_tail("$module . '.php'"), None);
        assert_eq!(include_tail("$page"), None);
    }

    #[test]
    fn normalize_collapses_dots_and_refuses_to_leave_the_root() {
        assert_eq!(
            normalize_path("pages/../lib/./x.php").as_deref(),
            Some("lib/x.php")
        );
        assert_eq!(normalize_path("../x.php"), None);
    }

    #[test]
    fn static_include_tries_the_includer_dir_then_the_root() {
        let paths = ["app/lib/x.php", "lib/x.php", "app/index.php"];
        let idx = IncludeIndex::new(&paths);
        assert_eq!(idx.resolve("app/index.php", "lib/x.php", false), Some(0));
        assert_eq!(idx.resolve("index.php", "lib/x.php", false), Some(1));
        assert_eq!(idx.resolve("app/index.php", "../lib/x.php", false), Some(1));
        assert_eq!(idx.resolve("app/index.php", "missing.php", false), None);
    }

    #[test]
    fn suffix_match_prefers_shared_segments_then_proximity_and_refuses_ties() {
        let paths = [
            "a/lib/db.php",
            "b/lib/db.php",
            "a/pages/index.php",
            "c/db.php",
        ];
        let idx = IncludeIndex::new(&paths);
        // Two trailing segments shared beat one.
        assert_eq!(idx.by_suffix("x/index.php", "/srv/www/c/db.php"), Some(3));
        // Same score: the file nearer the includer wins.
        assert_eq!(idx.by_suffix("a/pages/index.php", "/lib/db.php"), Some(0));
        // Still tied: no guess.
        assert_eq!(idx.by_suffix("x/index.php", "/lib/db.php"), None);
    }

    // A same-directory decoy: without include resolution the same-dir tier
    // would pick it for the page's `cart_total()` call.
    const DECOY: &str = r#"<?php
function cart_total($items) {
    return 0;
}
"#;

    #[test]
    fn procedural_call_resolves_through_include_to_another_file() {
        let g = graph(&[
            ("pages/cart.php", CART_PAGE),
            ("pages/decoy.php", DECOY),
            ("lib/helpers.php", HELPERS),
        ]);
        assert!(
            edge(
                &g,
                "func_{main}::pages/cart.php",
                "func_cart_total::lib/helpers.php"
            ),
            "{:?}",
            targets(&g, "func_{main}::pages/cart.php")
        );
        assert!(!edge(
            &g,
            "func_{main}::pages/cart.php",
            "func_cart_total::pages/decoy.php"
        ));
        assert!(edge(
            &g,
            "func_cart_total::lib/helpers.php",
            "func_format_price::lib/helpers.php"
        ));
    }

    #[test]
    fn includes_are_followed_transitively() {
        let bootstrap = "<?php\nrequire_once __DIR__ . '/lib/helpers.php';\n";
        let page = "<?php\nrequire 'bootstrap.php';\necho format_price(3);\n";
        let g = graph(&[
            ("index.php", page),
            ("bootstrap.php", bootstrap),
            ("lib/helpers.php", HELPERS),
            (
                "format.php",
                "<?php\nfunction format_price($v) { return $v; }\n",
            ),
        ]);
        assert!(
            edge(
                &g,
                "func_{main}::index.php",
                "func_format_price::lib/helpers.php"
            ),
            "{:?}",
            targets(&g, "func_{main}::index.php")
        );
    }

    #[test]
    fn dynamic_include_resolves_by_literal_tail() {
        let page = r#"<?php
require_once $_SERVER['DOCUMENT_ROOT'] . '/app/lib/db.php';
include $module . '.php';
db_connect();
"#;
        let g = graph(&[
            ("web/index.php", page),
            ("web/stub.php", "<?php\nfunction db_connect() {}\n"),
            ("app/lib/db.php", "<?php\nfunction db_connect() {}\n"),
        ]);
        assert!(
            edge(
                &g,
                "func_{main}::web/index.php",
                "func_db_connect::app/lib/db.php"
            ),
            "{:?}",
            targets(&g, "func_{main}::web/index.php")
        );
    }

    #[test]
    fn self_parent_and_this_resolve_through_the_class_hierarchy() {
        let g = graph(&[("models.php", MODELS)]);
        let ctor = "method_Invoice___construct::models.php";
        // `parent::__construct()` goes to the parent, never back to itself.
        assert!(edge(&g, ctor, "method_Model___construct::models.php"));
        assert!(!edge(&g, ctor, ctor));
        assert!(edge(&g, ctor, "method_Invoice_bump::models.php"));
        // `$this->save()` is inherited from Model.
        assert!(edge(&g, ctor, "method_Model_save::models.php"));
        // `static::find()` is inherited too.
        assert!(edge(
            &g,
            "method_Invoice_total::models.php",
            "method_Model_find::models.php"
        ));
        // `new static` instantiates the class itself.
        assert!(edge(
            &g,
            "method_Model_find::models.php",
            "class_Model::models.php"
        ));
        assert!(g.edges.iter().any(|e| e.edge_type == "inheritance"
            && e.from == "class_Invoice::models.php"
            && e.to == "class_Model::models.php"));
    }

    #[test]
    fn parent_call_with_parent_outside_the_project_stays_unresolved() {
        let src = r#"<?php
class Child extends \Vendor\Base {
    public function __construct() {
        parent::__construct();
    }
}
"#;
        let g = graph(&[("child.php", src)]);
        assert!(targets(&g, "method_Child___construct::child.php").is_empty());
    }

    #[test]
    fn a_trait_method_is_reached_through_the_class_that_uses_it() {
        let src = r#"<?php
interface Payable extends Countable, \Billing\Chargeable {
    public function pay($amount);
}

trait Loggable {
    protected function log($message) {
        error_log($message);
    }
}

abstract class Order implements Payable {
    use Loggable;

    abstract protected function amount();

    public function pay($amount) {
        $this->log('paid');
    }
}
"#;
        let g = graph(&[("order.php", src)]);
        assert!(edge(
            &g,
            "method_Order_pay::order.php",
            "method_Loggable_log::order.php"
        ));
    }

    #[test]
    fn use_import_picks_the_namespaced_class_over_a_same_named_one() {
        let invoice = r#"<?php
namespace App\Models;

class Invoice {
    public function total() {}
    public static function find($id) {}
}
"#;
        let g = graph(&[
            ("src/Billing/InvoiceService.php", SERVICE),
            ("src/Models/Invoice.php", invoice),
            // Same short name, other namespace, same directory as the caller.
            (
                "src/Billing/Invoice.php",
                &invoice.replace("App\\Models", "App\\Billing"),
            ),
            (
                "src/Support/Formatter.php",
                "<?php\nnamespace App\\Support;\nclass Formatter { public function format($x) {} }\n",
            ),
            (
                "src/Support/functions.php",
                "<?php\nnamespace App\\Support;\nfunction money($v) { return $v; }\n",
            ),
        ]);
        let from = "method_InvoiceService_render::src/Billing/InvoiceService.php";
        let to = targets(&g, from);
        for expected in [
            "class_Invoice::src/Models/Invoice.php",
            "method_Invoice_total::src/Models/Invoice.php",
            "method_Invoice_find::src/Models/Invoice.php",
            "method_Formatter_format::src/Support/Formatter.php",
            "func_money::src/Support/functions.php",
        ] {
            assert!(to.contains(&expected), "missing {}: {:?}", expected, to);
        }
        assert!(to.iter().all(|t| !t.ends_with("src/Billing/Invoice.php")));
    }

    #[test]
    fn calls_only_resolve_to_their_own_kind_of_target() {
        let src = r#"<?php
class Report {
    public function render() {}
}
function build() {}

render();
$report->build();
"#;
        let g = graph(&[("report.php", src)]);
        // PHP never calls a method as `render()` or a function as `$x->build()`.
        assert!(targets(&g, "func_{main}::report.php").is_empty());
    }

    #[test]
    fn conditional_redeclarations_share_one_edge_per_target() {
        let g = graph(&[(
            "dup.php",
            r#"<?php
if (PHP_VERSION_ID >= 80000) {
    function compat() { helper(); }
} else {
    function compat() { helper(); }
}
function helper() {}
"#,
        )]);
        assert_eq!(
            targets(&g, "func_compat::dup.php"),
            vec!["func_helper::dup.php"]
        );
    }

    #[test]
    fn absolute_include_is_not_joined_to_the_includer_dir() {
        let g = graph(&[
            (
                "web/pages/index.php",
                "<?php\ninclude '/lib/x.php';\nx();\n",
            ),
            ("lib/x.php", "<?php\nfunction x() { return 'root'; }\n"),
            (
                "web/pages/lib/x.php",
                "<?php\nfunction x() { return 'decoy'; }\n",
            ),
        ]);
        assert_eq!(
            targets(&g, "func_{main}::web/pages/index.php"),
            vec!["func_x::lib/x.php"]
        );
    }

    #[test]
    fn classes_outside_the_project_get_no_edge_instead_of_a_namesake() {
        let src = r#"<?php
namespace App\Tests;

use PHPUnit\Framework\TestCase;

class InvoiceTest extends TestCase {
    public function test_total() {
        throw new \InvalidArgumentException('x');
    }
}
"#;
        let g = graph(&[
            ("tests/InvoiceTest.php", src),
            (
                "src/Exceptions.php",
                "<?php\nnamespace App\\Errors;\nclass InvalidArgumentException {}\n",
            ),
            (
                "tests/TestCase.php",
                "<?php\nnamespace App\\Support;\nclass TestCase {}\n",
            ),
        ]);
        assert!(targets(&g, "method_InvoiceTest_test_total::tests/InvoiceTest.php").is_empty());
        assert!(g.edges.iter().all(|e| e.edge_type != "inheritance"));
    }

    #[test]
    fn aliases_sharing_a_short_name_resolve_to_their_own_classes() {
        let src = r#"<?php
namespace App;

use App\Models\User;
use App\Legacy\User as LegacyUser;

function migrate() {
    new User();
    new LegacyUser();
}
"#;
        let user = |ns: &str| format!("<?php\nnamespace App\\{};\nclass User {{}}\n", ns);
        let g = graph(&[
            ("src/migrate.php", src),
            ("src/Models/User.php", &user("Models")),
            ("src/Legacy/User.php", &user("Legacy")),
        ]);
        let mut to = targets(&g, "func_migrate::src/migrate.php");
        to.sort();
        assert_eq!(
            to,
            vec![
                "class_User::src/Legacy/User.php",
                "class_User::src/Models/User.php"
            ]
        );
    }

    #[test]
    fn a_named_class_without_the_method_goes_to_its_magic_method_or_nowhere() {
        let src = r#"<?php
class Facade {
    public static function __callStatic($name, $args) {}
}
class Route extends Facade {}
class Plain {}
class Mailer {
    public function __call($name, $args) {}
}
class Other {
    public function get() {}
    public function send() {}
}

function boot(Mailer $mailer, Plain $plain) {
    Route::get('/');
    Plain::get();
    $mailer->send();
    $plain->send();
}
"#;
        let g = graph(&[("app.php", src)]);
        let mut to = targets(&g, "func_boot::app.php");
        to.sort();
        assert_eq!(
            to,
            vec![
                "method_Facade___callStatic::app.php",
                "method_Mailer___call::app.php"
            ]
        );
    }

    #[test]
    fn no_op_stubs_and_guarded_fallbacks_lose_to_a_real_definition() {
        let noop = "<?php\nfunction __($text) {}\nif (!function_exists('esc')) {\n    function esc($s) { return $s; }\n}\n";
        let page = "<?php\necho __('Hi') . esc('x');\n";
        let loader = "<?php\nrequire __DIR__ . '/noop.php';\necho __('Hi');\n";
        let g = graph(&[
            ("admin/includes/page.php", page),
            ("admin/includes/noop.php", noop),
            ("admin/load.php", loader),
            (
                "lib/l10n.php",
                "<?php\nfunction __($text) { return translate($text); }\n",
            ),
            (
                "lib/format.php",
                "<?php\nfunction esc($s) { return htmlspecialchars($s); }\n",
            ),
        ]);
        let mut to = targets(&g, "func_{main}::admin/includes/page.php");
        to.sort();
        assert_eq!(
            to,
            vec!["func___::lib/l10n.php", "func_esc::lib/format.php"]
        );
        // A file that really includes the stubs runs them.
        assert_eq!(
            targets(&g, "func_{main}::admin/load.php"),
            vec!["func___::admin/includes/noop.php"]
        );
    }

    #[test]
    fn unqualified_calls_try_the_namespace_then_the_global_function() {
        let lib =
            "<?php\nnamespace Sodium;\nfunction bin2hex($s) {}\nfunction compare($a, $b) {}\n";
        let global = "<?php\nfunction compare($a, $b) {}\nfunction pad($s) {}\n";
        let caller =
            "<?php\nnamespace Sodium;\nfunction run() {\n    compare(1, 2);\n    pad('x');\n}\n";
        let g = graph(&[
            ("sodium/lib.php", lib),
            ("compat.php", global),
            ("sodium/run.php", caller),
            ("index.php", "<?php\necho bin2hex('x');\n"),
        ]);
        let mut to = targets(&g, "func_run::sodium/run.php");
        to.sort();
        assert_eq!(
            to,
            vec!["func_compare::sodium/lib.php", "func_pad::compat.php"]
        );
        // Global code never reaches `Sodium\bin2hex`.
        assert!(targets(&g, "func_{main}::index.php").is_empty());
    }

    #[test]
    fn names_resolve_case_insensitively_like_php() {
        let src = "<?php\nclass Invoice { public function Total() {} }\nfunction Format_Price() {}\nfunction run() {\n    $i = new invoice();\n    $i->TOTAL();\n    format_price();\n}\n";
        let g = graph(&[("a.php", src)]);
        let mut to = targets(&g, "func_run::a.php");
        to.sort();
        assert_eq!(
            to,
            vec![
                "class_Invoice::a.php",
                "func_Format_Price::a.php",
                "method_Invoice_Total::a.php"
            ]
        );
    }

    #[test]
    fn parent_follows_the_extended_class_not_an_interface() {
        let src = r#"<?php
interface Hasher { public function check($v); }
abstract class AbstractHasher { public function check($v) {} }
class BcryptHasher extends AbstractHasher implements Hasher {
    public function check($v) { return parent::check($v); }
}
"#;
        assert_eq!(
            targets(
                &graph(&[("h.php", src)]),
                "method_BcryptHasher_check::h.php"
            ),
            vec!["method_AbstractHasher_check::h.php"]
        );
    }

    #[test]
    fn unknown_receivers_resolve_by_name_only_when_the_name_is_unique() {
        let src = r#"<?php
class Invoice { public function save() {} public function render() {} }
class User { public function save() {} }
function run($x) {
    $x->save();
    $x->render();
}
"#;
        // Two unrelated `save` methods: guessing would be a coin toss.
        assert_eq!(
            targets(&graph(&[("app.php", src)]), "func_run::app.php"),
            vec!["method_Invoice_render::app.php"]
        );
    }

    #[test]
    fn variable_method_names_get_no_edge() {
        let src = "<?php\nclass Proxy {\n    public function __call($n, $a) {}\n    public function run($m) { return $this->$m(); }\n}\n";
        assert!(targets(&graph(&[("p.php", src)]), "method_Proxy_run::p.php").is_empty());
    }

    #[test]
    fn a_closure_parameter_does_not_type_the_enclosing_variable() {
        let src = r#"<?php
namespace App;
class User { public function notify() {} }
class Invoice { public function amount() { return 1; } }
function bill(array $users, array $invoices) {
    foreach ($users as $u) {
        $u->notify();
    }
    return array_map(fn (Invoice $u) => $u->amount(), $invoices);
}
"#;
        let g = graph(&[("models.php", src)]);
        let mut to = targets(&g, "func_bill::models.php");
        to.sort();
        assert_eq!(
            to,
            vec![
                "method_Invoice_amount::models.php",
                "method_User_notify::models.php"
            ]
        );
    }

    #[test]
    fn top_level_closure_parameters_are_qualified_in_their_namespace() {
        let src = "<?php\nnamespace App\\Config;\n\nclass Builder { public function services() {} }\n\nreturn static function (Builder $b) {\n    $b->services();\n};\n";
        let g = graph(&[("config.php", src)]);
        assert!(edge(
            &g,
            "func_{main}::config.php",
            "method_Builder_services::config.php"
        ));
    }

    #[test]
    fn main_calls_resolve_in_the_namespace_they_are_written_in() {
        let g = graph(&[
            (
                "app.php",
                "<?php\nnamespace A;\nhelper();\nnamespace B;\nhelper();\n",
            ),
            (
                "a.php",
                "<?php\nnamespace A;\nfunction helper() { work(); }\n",
            ),
            (
                "b.php",
                "<?php\nnamespace B;\nfunction helper() { work(); }\n",
            ),
        ]);
        let mut to = targets(&g, "func_{main}::app.php");
        to.sort();
        assert_eq!(to, vec!["func_helper::a.php", "func_helper::b.php"]);
    }

    #[test]
    fn same_named_declarations_in_two_namespaces_stay_apart() {
        let g = graph(&[
            (
                "bundle.php",
                "<?php\nnamespace A {\n    function make() { return new Logger(); }\n    class Logger {}\n}\nnamespace B {\n    function make() { return new Logger(); }\n    class Logger {}\n}\n",
            ),
            ("use.php", "<?php\nfunction u() { \\B\\make(); }\n"),
        ]);
        assert_eq!(
            targets(&g, "func_u::use.php"),
            vec!["func_B\\make::bundle.php"]
        );
        assert_eq!(
            targets(&g, "func_B\\make::bundle.php"),
            vec!["class_B\\Logger::bundle.php"]
        );
    }

    #[test]
    fn use_const_does_not_capture_a_function_call() {
        let g = graph(&[
            (
                "app.php",
                "<?php\nnamespace App;\nuse const App\\Config\\VERSION;\nfunction run() { return version(); }\n",
            ),
            ("lib.php", "<?php\nfunction version() { work(); }\n"),
        ]);
        assert_eq!(
            targets(&g, "func_run::app.php"),
            vec!["func_version::lib.php"]
        );
    }

    #[test]
    fn an_ambiguous_project_leaves_the_local_fallback_standing() {
        let mut files = vec![
            ("app/page.php".to_string(), "<?php\necho __('Hello');\n".to_string()),
            (
                "app/compat.php".to_string(),
                "<?php\nif (!function_exists('__')) {\n    function __($s) { return translate($s); }\n}\n".to_string(),
            ),
        ];
        for i in 0..9 {
            files.push((
                format!("plugins/p{i}/i18n.php"),
                format!("<?php\nfunction __($s) {{ return p{i}($s); }}\n"),
            ));
        }
        let refs: Vec<(&str, &str)> = files
            .iter()
            .map(|(p, s)| (p.as_str(), s.as_str()))
            .collect();
        let g = graph(&refs);
        assert_eq!(
            targets(&g, "func_{main}::app/page.php"),
            vec!["func___::app/compat.php"]
        );
    }

    #[test]
    fn a_name_only_method_needs_a_name_unique_in_the_project() {
        // The caller's own class is the only `check()` in its file, but
        // `$s` is a `Setting`: one candidate per tier is still a guess.
        let g = graph(&[
            (
                "src/Control.php",
                "<?php\nclass Control {\n    public function check() { return true; }\n    public function run(array $settings) {\n        foreach ($settings as $s) { $s->check(); $s->apply(); }\n    }\n}\n",
            ),
            (
                "src/Setting.php",
                "<?php\nclass Setting {\n    public function check() { return false; }\n    public function apply() {}\n}\n",
            ),
        ]);
        assert_eq!(
            targets(&g, "method_Control_run::src/Control.php"),
            vec!["method_Setting_apply::src/Setting.php"]
        );
    }

    #[test]
    fn a_name_only_arrow_call_skips_static_methods() {
        // `$mock->shouldReceive()` is the mock's own method, not the
        // facade's static helper; `$cls::shouldReceive()` may be the helper.
        // A static `nullable()` still makes the other one ambiguous.
        let g = graph(&[
            (
                "src/Facade.php",
                "<?php\nnamespace App;\nabstract class Facade {\n    public static function shouldReceive() {}\n    public function resolve() {}\n}\n",
            ),
            (
                "src/Types.php",
                "<?php\nnamespace App;\nclass Type { public function nullable() {} }\nclass Rule { public static function nullable() {} }\n",
            ),
            (
                "tests/FooTest.php",
                "<?php\nnamespace Tests;\nclass FooTest {\n    public function mocked($mock) {\n        $mock->shouldReceive('save');\n        $mock->resolve();\n        $mock->nullable();\n    }\n    public function aliased($cls) {\n        $cls::shouldReceive('save');\n    }\n}\n",
            ),
        ]);
        assert_eq!(
            targets(&g, "method_FooTest_mocked::tests/FooTest.php"),
            vec!["method_Facade_resolve::src/Facade.php"]
        );
        assert_eq!(
            targets(&g, "method_FooTest_aliased::tests/FooTest.php"),
            vec!["method_Facade_shouldReceive::src/Facade.php"]
        );
    }

    #[test]
    fn php_and_python_never_resolve_to_each_other() {
        let structure: HashMap<String, FileData> = [
            ("app/main.py", "def main():\n    helper()\n    render()\n"),
            ("app/util.py", "def render():\n    pass\n"),
            ("app/helpers.php", "<?php\nfunction helper() {}\n"),
            ("web/page.php", "<?php\nrender();\n"),
        ]
        .iter()
        .map(|(p, s)| {
            let fd = crate::grammar_files::parse_source(Path::new(p), s)
                .expect("parse should succeed")
                .1;
            (p.to_string(), fd)
        })
        .collect();
        let g = super::super::build_call_graph_v2(&structure);
        assert_eq!(
            targets(&g, "func_main::app/main.py"),
            vec!["func_render::app/util.py"]
        );
        assert!(targets(&g, "func_{main}::web/page.php").is_empty());
    }

    #[test]
    fn inherited_methods_follow_traits_then_extends_before_interfaces() {
        // `a/` and `b/` sort before `c/`: a plain walk over every base used
        // to reach the interface's abstract `greet()` first.
        let g = graph(&[
            (
                "a/Greeter.php",
                "<?php\ninterface Greeter { function greet(); }\n",
            ),
            (
                "b/Greets.php",
                "<?php\ntrait Greets { function wave() {} }\n",
            ),
            (
                "c/Base.php",
                "<?php\nclass Base { function greet() {} function wave() {} }\n",
            ),
            (
                "d/Child.php",
                "<?php\nclass Child extends Base implements Greeter {\n    use Greets;\n    function run() { $this->greet(); self::greet(); $this->wave(); }\n}\n",
            ),
        ]);
        let mut to = targets(&g, "method_Child_run::d/Child.php");
        to.sort();
        assert_eq!(
            to,
            vec![
                "method_Base_greet::c/Base.php",
                "method_Greets_wave::b/Greets.php"
            ]
        );
    }

    #[test]
    fn self_in_a_trait_is_the_class_using_it() {
        let g = graph(&[(
            "named.php",
            "<?php\ntrait Named {\n    function label() { return self::name(); }\n}\nclass User {\n    use Named;\n    static function name() { return 'u'; }\n}\n",
        )]);
        assert_eq!(
            targets(&g, "method_Named_label::named.php"),
            vec!["method_User_name::named.php"]
        );
    }
}
