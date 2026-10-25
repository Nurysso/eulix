// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
//! PHP parser, built on the `LANGUAGE_PHP` grammar: the variant that accepts
//! inline HTML, so templates mixing markup and `<?php ... ?>` blocks parse as
//! one tree.
//!
//! Call sites are written in the shapes PRISM v2 resolves:
//!
//! | source             | callee                                              |
//! |--------------------|-----------------------------------------------------|
//! | `foo()`            | `foo` (the namespace's `foo`, else the global one)  |
//! | `foo()` in `{main}`, later `namespace B` | `B\foo` (`B`'s `foo`, else the global one) |
//! | `\ns\foo()`         | `\ns\foo`                                           |
//! | `new Foo`          | `new \App\Foo`                                      |
//! | `Foo::bar()`       | `\App\Foo::bar`, `self::bar`, `static::bar`, `parent::bar` |
//! | `$this->bar()`     | `this.bar`                                          |
//! | `$x->bar()`        | `\App\Foo.bar` when `$x` is known to hold a `Foo`, else `$x.bar` |
//!
//! Class names are written fully qualified, by PHP's own rules (`use`
//! imports and aliases, the current namespace, `namespace\`), so the resolver
//! can match them exactly. Code outside any function is collected into a
//! synthetic `{main}` function (PHP's own name for it in stack traces), so
//! the calls a page script makes still show up in the call graph.

use super::utils::{extract_todos, walk};

use crate::struc::kb_struct::*;
use crate::utils::syntax;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::LazyLock;
use tree_sitter::{Node, Parser};

/// Name of the synthetic function holding a file's top-level code.
const MAIN_FUNCTION: &str = "{main}";

/// Tag of the synthetic `{main}` function, which is not a declared function.
pub const SCRIPT_TAG: &str = "script";

/// Tag for a function declared only as a fallback for a same-named one: a
/// no-op stub (empty body or a bare `return <literal>;`, like WordPress'
/// `wp-admin/includes/noop.php`) or a declaration under an
/// `if (!function_exists('name'))` that names it (polyfills, back-compat
/// shims). PRISM v2 prefers a real definition when it has to guess.
pub const FALLBACK_TAG: &str = "fallback";

/// Values longer than this are cut: page scripts routinely assign whole HTML
/// or SQL blocks to a variable.
const MAX_VALUE_LEN: usize = 120;

/// Cap on the pieces of a statically evaluated include path, on the nesting
/// of its directory anchor and on the levels the anchor climbs. Real code
/// uses a handful of each; the cap keeps adversarial input
/// (`dirname(__DIR__, PHP_INT_MAX)`, thousands of nested `dirname(`) from
/// allocating or recursing without bound.
const MAX_PATH_PARTS: usize = 16;

/// Language constructs that tree-sitter-php parses as function calls.
const CONSTRUCTS: [&str; 5] = ["isset", "empty", "die", "exit", "eval"];

const COMMAND_EXEC_FNS: [&str; 6] = [
    "exec",
    "shell_exec",
    "system",
    "passthru",
    "popen",
    "proc_open",
];

/// Query functions and the index of their SQL argument.
const SQL_QUERY_FNS: [(&str, usize); 2] = [("mysql_query", 0), ("mysqli_query", 1)];

/// Nodes that open a new variable scope.
const FUNCTION_LIKE: [&str; 4] = [
    "function_definition",
    "method_declaration",
    "anonymous_function",
    "arrow_function",
];

const CLASS_LIKE: [&str; 4] = [
    "class_declaration",
    "interface_declaration",
    "trait_declaration",
    "enum_declaration",
];

/// Types that never name a class, docblock spellings (`integer`, `boolean`,
/// `resource`...) included.
const BUILTIN_TYPES: [&str; 21] = [
    "array", "bool", "boolean", "callable", "double", "false", "float", "int", "integer",
    "iterable", "mixed", "never", "null", "number", "object", "parent", "resource", "scalar",
    "string", "true", "void",
];

struct TagRule {
    keywords: &'static [&'static str],
    tag: &'static str,
    check_docstring: bool,
}

// Tag rules. Docstrings are not matched: `auth` would hit every `@author`.
static TAG_RULES: LazyLock<Vec<TagRule>> = LazyLock::new(|| {
    vec![
        TagRule {
            keywords: &["init", "setup", "bootstrap"],
            tag: "initialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &["cleanup", "close", "shutdown", "dispose"],
            tag: "cleanup",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "auth", "login", "logout", "password", "hash", "encrypt", "decrypt", "token",
            ],
            tag: "authentication",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "api",
                "endpoint",
                "route",
                "handler",
                "action",
                "controller",
            ],
            tag: "api",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "db", "database", "query", "sql", "select", "insert", "update", "delete", "save",
                "fetch", "find",
            ],
            tag: "database",
            check_docstring: false,
        },
        TagRule {
            keywords: &["validate", "check", "verify", "sanitize", "escape"],
            tag: "validation",
            check_docstring: false,
        },
        TagRule {
            keywords: &["error", "exception"],
            tag: "error-handling",
            check_docstring: false,
        },
        TagRule {
            keywords: &["util", "helper"],
            tag: "utility",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "read", "write", "file", "open", "load", "upload", "download",
            ],
            tag: "file-io",
            check_docstring: false,
        },
        TagRule {
            keywords: &["socket", "connect", "request", "response", "curl"],
            tag: "network",
            check_docstring: false,
        },
        TagRule {
            keywords: &["config", "setting", "option"],
            tag: "configuration",
            check_docstring: false,
        },
        TagRule {
            keywords: &["log", "debug"],
            tag: "logging",
            check_docstring: false,
        },
        TagRule {
            keywords: &["parse", "decode"],
            tag: "parsing",
            check_docstring: false,
        },
        TagRule {
            keywords: &["serialize", "encode", "json"],
            tag: "serialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &["render", "display", "template", "html"],
            tag: "rendering",
            check_docstring: false,
        },
    ]
});

fn is_definition(n: Node) -> bool {
    match n.kind() {
        // A method of `new class { ... }` has no named class to be extracted
        // under, so, like a closure, it belongs to the code around it.
        "method_declaration" => !n
            .parent()
            .and_then(|body| body.parent())
            .is_some_and(|c| c.kind() == "anonymous_class"),
        k => k == "function_definition" || CLASS_LIKE.contains(&k),
    }
}

/// True if `text` contains `word` with no identifier character on either side.
fn mentions(text: &str, word: &str) -> bool {
    let ident = |c: char| c == '_' || c.is_alphanumeric();
    text.match_indices(word).any(|(i, _)| {
        !text[..i].chars().next_back().is_some_and(ident)
            && !text[i + word.len()..].chars().next().is_some_and(ident)
    })
}

/// Node kinds whose text is not code, so a `{` inside them is never an offset.
const NOT_CODE: [&str; 7] = [
    "comment",
    "string",
    "encapsed_string",
    "heredoc",
    "nowdoc",
    "text",
    "shell_command_expression",
];

/// A same-length copy of `src` in which the string offsets PHP 5 and 7 also
/// wrote with braces (`$s{0}`, `$this->f{$i}`, `$a['k']{0}`) use brackets,
/// the only form the grammar knows; `None` if there is none. Strings,
/// comments and inline HTML, taken from `root`, are left alone. Outside them
/// a `{` glued to a variable, a property or a `]` does not parse in PHP 8,
/// so valid code never changes.
fn legacy_offsets_as_brackets(src: &str, root: Node) -> Option<String> {
    // Disjoint and in source order: the walk does not enter a skipped node.
    let mut skip: Vec<(usize, usize)> = Vec::new();
    walk(root, (), |n, _| {
        if NOT_CODE.contains(&n.kind()) {
            skip.push((n.start_byte(), n.end_byte()));
            None
        } else {
            Some(())
        }
    });
    let in_skip = |i: usize| {
        let k = skip.partition_point(|r| r.0 <= i);
        k > 0 && i < skip[k - 1].1
    };
    let b = src.as_bytes();
    let ident = |c: u8| c == b'_' || c.is_ascii_alphanumeric() || c >= 0x80;
    let mut out: Option<Vec<u8>> = None;
    for (i, _) in src.match_indices('{') {
        if i == 0 || in_skip(i) {
            continue;
        }
        let glued = b[i - 1] == b']' || {
            let mut j = i;
            while j > 0 && ident(b[j - 1]) {
                j -= 1;
            }
            j < i && j > 0 && (b[j - 1] == b'$' || j >= 2 && &b[j - 2..j] == b"->")
        };
        if !glued {
            continue;
        }
        // The matching `}`, which an offset closes on the same line.
        let mut depth = 0usize;
        let close = b[i..].iter().take(200).position(|&c| match c {
            b'{' => {
                depth += 1;
                false
            }
            b'}' => {
                depth -= 1;
                depth == 0
            }
            _ => false,
        });
        let Some(len) = close.filter(|&len| !b[i..i + len].contains(&b'\n')) else {
            continue;
        };
        let o = out.get_or_insert_with(|| b.to_vec());
        o[i] = b'[';
        o[i + len] = b']';
    }
    // Swapping ASCII bytes keeps the text valid UTF-8.
    out.and_then(|v| String::from_utf8(v).ok())
}

fn collapse_ws(s: &str) -> String {
    s.split_whitespace().collect::<Vec<_>>().join(" ")
}

fn truncate(s: &str) -> String {
    let s = collapse_ws(s);
    match s.char_indices().nth(MAX_VALUE_LEN) {
        Some((i, _)) => format!("{}…", &s[..i]),
        None => s,
    }
}

/// `\App\Models\User` -> `User`
fn last_segment(name: &str) -> &str {
    let name = name.trim_start_matches('\\');
    name.rsplit('\\').next().unwrap_or(name)
}

/// Types given by a docblock tag: `@var Foo` and `@return Foo` yield
/// `("Foo", None)`, `@param Foo $x` and `@global Foo $x` yield
/// `("Foo", Some("$x"))`.
fn doc_tags<'d>(doc: &'d str, tag: &'d str) -> impl Iterator<Item = (&'d str, Option<&'d str>)> {
    doc.lines().filter_map(move |line| {
        let rest = line.trim().strip_prefix(tag)?;
        if !rest.starts_with(char::is_whitespace) {
            return None;
        }
        let mut words = rest.split_whitespace();
        let ty = words.next()?;
        Some((ty, words.next().filter(|w| w.starts_with('$'))))
    })
}

/// `@template T` / `@template T of Bound` names declared in a docblock,
/// with their bound.
fn doc_templates(doc: &str) -> Vec<(String, Option<String>)> {
    doc.lines()
        .filter_map(|line| {
            let rest = line.trim().strip_prefix("@template")?;
            // `@template-covariant T`
            let rest = rest.split_once(char::is_whitespace).map_or("", |(_, r)| r);
            let mut words = rest.split_whitespace();
            let name = words.next()?.to_string();
            let bound = match words.next() {
                Some("of" | "as") => words.next().map(str::to_string),
                _ => None,
            };
            Some((name, bound))
        })
        .collect()
}

/// A docblock type with `@template` names replaced by their bound; an
/// unbounded template names no class.
fn untemplate(ty: &str, templates: &[(String, Option<String>)]) -> Option<String> {
    let bare = ty.trim_start_matches('?');
    match templates.iter().find(|(name, _)| name == bare) {
        Some((_, bound)) => bound.clone(),
        None => Some(ty.to_string()),
    }
}

/// Strip the comment markers of a `/** ... */` docblock.
fn clean_docblock(raw: &str) -> String {
    let inner = raw.trim_start_matches("/**").trim_end_matches("*/");
    let lines: Vec<&str> = inner
        .lines()
        .map(|l| l.trim().trim_start_matches('*').trim())
        .collect();
    let start = lines
        .iter()
        .position(|l| !l.is_empty())
        .unwrap_or(lines.len());
    let end = lines
        .iter()
        .rposition(|l| !l.is_empty())
        .map_or(start, |i| i + 1);
    lines[start..end].join("\n")
}

/// What a call site needs from its enclosing class.
#[derive(Default)]
struct ClassCtx {
    name: String,
    /// `\App\Models\User`
    fqn: String,
    /// property name (no `$`) -> class
    props: HashMap<String, String>,
    /// lowercase method name -> class it returns
    returns: HashMap<String, String>,
    /// `@template` names of the class docblock, with their bound
    templates: Vec<(String, Option<String>)>,
}

/// What a call site needs from its enclosing function or script.
struct Scope<'s> {
    class: Option<&'s ClassCtx>,
    /// Namespace the function is filed under (`PhpInfo::namespace`), which
    /// PRISM v2 reads an unqualified `foo()` in; `{main}` gets the first
    /// region's, though its code may span several.
    namespace: Option<String>,
    /// `$var` -> class, from `new` assignments and type hints.
    vars: HashMap<String, String>,
}

#[derive(Default)]
struct Modifiers {
    access_modifier: Option<String>,
    is_static: bool,
    is_abstract: bool,
    is_final: bool,
    text: String,
}

struct UseClause {
    fqn: String,
    alias: Option<String>,
    /// `use function ...` / `use const ...`
    is_symbol: bool,
    /// `use const ...`
    is_const: bool,
}

/// Parses one PHP file (inline HTML included) into `FileData`.
pub struct PhpParser {
    source_code: String,
    file_path: String,
    /// (start byte, end byte, namespace) of each `namespace` region; `None`
    /// is the global namespace.
    namespaces: Vec<(usize, usize, Option<String>)>,
    /// Class and namespace imports, (namespace region, lowercase alias) ->
    /// qualified name: `use A\B as C` -> `c` => `A\B`. PHP resets the
    /// imports at every `namespace` declaration, so each region has its own.
    uses: HashMap<(Option<usize>, String), String>,
    /// `use function A\b as c` -> `c` => `A\b`, per region too
    fn_uses: HashMap<(Option<usize>, String), String>,
    /// `$var` -> class for top-level `$var = new Class`; also visible to
    /// functions that pull the variable in with `global $var`.
    globals: HashMap<String, String>,
}

impl PhpParser {
    pub fn new(source_code: String, file_path: String) -> Self {
        Self {
            source_code,
            file_path,
            namespaces: Vec::new(),
            uses: HashMap::new(),
            fn_uses: HashMap::new(),
            globals: HashMap::new(),
        }
    }

    fn make_function_id(&self, name: &str, struct_context: &str) -> String {
        if struct_context.is_empty() {
            format!("func_{}::{}", name, self.file_path)
        } else {
            format!("method_{}_{}::{}", struct_context, name, self.file_path)
        }
    }

    fn make_class_id(&self, name: &str) -> String {
        format!("class_{}::{}", name, self.file_path)
    }

    /// Name a declaration's id is built from. In a file with several
    /// `namespace` regions, `A\f` and `B\f` are different symbols and need
    /// different ids; everywhere else it is the plain name.
    fn id_name(&self, name: &str, at: usize) -> String {
        match self.namespace_at(at) {
            Some(ns) if self.namespaces.len() > 1 => format!("{}\\{}", ns, name),
            _ => name.to_string(),
        }
    }

    pub fn parse(&mut self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(&tree_sitter_php::LANGUAGE_PHP.into())
            .map_err(|e| format!("Failed to load PHP grammar: {}", e))?;

        let mut tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse PHP file".to_string())?;
        // Legacy code also writes string offsets with braces (`$s{0}`), which
        // the grammar does not know. When the file does not parse, try a copy
        // with brackets there; it has the same length, so node text is still
        // read from the original, at the same byte offsets.
        if tree.root_node().has_error() {
            let errors = |t: &tree_sitter::Tree| {
                let health = syntax::inspect(t);
                health.error_nodes + health.missing_nodes
            };
            let fixed = legacy_offsets_as_brackets(&self.source_code, tree.root_node())
                .and_then(|src| parser.parse(&src, None));
            if let Some(t) = fixed.filter(|t| errors(t) < errors(&tree)) {
                tree = t;
            }
        }

        let root = tree.root_node();
        self.read_file_context(root);

        let (mut functions, classes) = self.extract_definitions(root);
        if let Some(main) = self.build_main(root) {
            functions.push(main);
        }

        Ok(FileData {
            language: "php".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(root),
            functions,
            classes,
            global_vars: self.extract_global_vars(root),
            todos: self.collect_todos(root),
            security_notes: self.detect_security_patterns(root),
            syntax: syntax::inspect(&tree),
        })
    }

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    fn get_node_text(&self, node: Node) -> &str {
        node.utf8_text(self.source_code.as_bytes()).unwrap_or("")
    }

    /// Namespace, `use` aliases and top-level object variables: everything a
    /// call site may need from outside its own function.
    fn read_file_context(&mut self, root: Node) {
        // `namespace A;` runs until the next one; `namespace A { }` is a block.
        let mut namespaces: Vec<(usize, usize, Option<String>)> = Vec::new();
        let mut cursor = root.walk();
        for n in root.named_children(&mut cursor) {
            if n.kind() != "namespace_definition" {
                continue;
            }
            let name = n
                .child_by_field_name("name")
                .map(|name| self.get_node_text(name).to_string());
            if n.child_by_field_name("body").is_some() {
                namespaces.push((n.start_byte(), n.end_byte(), name));
            } else {
                if let Some(prev) = namespaces.last_mut() {
                    prev.1 = prev.1.min(n.start_byte());
                }
                namespaces.push((n.start_byte(), root.end_byte(), name));
            }
        }
        self.namespaces = namespaces;

        let mut uses = HashMap::new();
        let mut fn_uses = HashMap::new();
        walk(root, (), |n, _| match n.kind() {
            "namespace_use_declaration" => {
                let region = self.region_at(n.start_byte());
                for clause in self.use_clauses(n) {
                    let alias = clause
                        .alias
                        .unwrap_or_else(|| last_segment(&clause.fqn).to_string())
                        .to_ascii_lowercase();
                    // `use const` names a constant, never a function.
                    if clause.is_const {
                        continue;
                    }
                    if clause.is_symbol {
                        fn_uses.insert((region, alias), clause.fqn);
                    } else {
                        uses.insert((region, alias), clause.fqn);
                    }
                }
                None
            }
            _ if is_definition(n) => None,
            _ => Some(()),
        });
        self.uses = uses;
        self.fn_uses = fn_uses;
        self.globals = self.var_types(root, None, &[], "");
    }

    // Imports

    fn extract_imports(&self, root: Node) -> Vec<Import> {
        let mut imports: Vec<Import> = Vec::new();
        let mut seen: HashSet<(String, String)> = HashSet::new();
        walk(root, (), |n, _| match n.kind() {
            "include_expression"
            | "include_once_expression"
            | "require_expression"
            | "require_once_expression" => {
                if let Some(arg) = n.named_child(0) {
                    let (module, import_type) = match self.static_path(arg) {
                        Some(path) => (path, "include"),
                        None => (collapse_ws(self.get_node_text(arg)), "include_dynamic"),
                    };
                    if seen.insert((module.clone(), String::new())) {
                        imports.push(Import {
                            module,
                            items: vec![],
                            import_type: import_type.to_string(),
                        });
                    }
                }
                None
            }
            "namespace_use_declaration" => {
                for clause in self.use_clauses(n) {
                    let short = last_segment(&clause.fqn).to_string();
                    // A function or constant lives *in* the namespace, while a
                    // class conventionally has a file of its own.
                    let module = if clause.is_symbol {
                        clause
                            .fqn
                            .rsplit_once('\\')
                            .map_or(String::new(), |(ns, _)| ns.to_string())
                    } else {
                        clause.fqn.clone()
                    };
                    if module.is_empty() || !seen.insert((module.clone(), short.clone())) {
                        continue;
                    }
                    imports.push(Import {
                        import_type: self.classify_import(&module, n.start_byte()),
                        module,
                        items: vec![short],
                    });
                }
                None
            }
            _ => Some(()),
        });
        imports
    }

    /// Fully qualified name of a class (or qualified function) reference,
    /// by PHP's rules: `\A\B` is absolute, `namespace\B` is relative to the
    /// current namespace, a first segment imported with `use` expands to the
    /// import, anything else is relative to the current namespace. Always
    /// written with a leading `\`, which tells the resolver it is exact.
    fn qualify(&self, name: &str, at: usize) -> String {
        let name = name.trim();
        if let Some(absolute) = name.strip_prefix('\\') {
            return format!("\\{}", absolute);
        }
        let in_namespace = |rest: &str| match self.namespace_at(at) {
            Some(ns) => format!("\\{}\\{}", ns, rest),
            None => format!("\\{}", rest),
        };
        // `get` rather than slicing: a non-ASCII name may not split at byte 10.
        if let Some(relative) = name
            .get(..10)
            .filter(|p| p.eq_ignore_ascii_case("namespace\\"))
            .and_then(|_| name.get(10..))
            .filter(|r| !r.is_empty())
        {
            return in_namespace(relative);
        }
        let (head, rest) = match name.split_once('\\') {
            Some((h, r)) => (h, Some(r)),
            None => (name, None),
        };
        let key = (self.region_at(at), head.to_ascii_lowercase());
        match (self.uses.get(&key), rest) {
            (Some(import), Some(rest)) => format!("\\{}\\{}", import, rest),
            (Some(import), None) => format!("\\{}", import),
            (None, _) => in_namespace(name),
        }
    }

    fn namespace_at(&self, byte: usize) -> Option<String> {
        self.region_at(byte)
            .and_then(|i| self.namespaces[i].2.clone())
    }

    /// Index of the `namespace` region holding `byte`, if any. Regions are
    /// disjoint and in source order, so a binary search finds it: every class
    /// reference asks, and bundled files hold thousands of namespaces.
    fn region_at(&self, byte: usize) -> Option<usize> {
        let i = self
            .namespaces
            .partition_point(|(start, _, _)| *start <= byte)
            .checked_sub(1)?;
        (byte < self.namespaces[i].1).then_some(i)
    }

    /// A `use` is internal when it shares its root namespace with the code
    /// that declares it.
    fn classify_import(&self, module: &str, at: usize) -> String {
        let root_of = |ns: &str| ns.split('\\').next().unwrap_or(ns).to_string();
        match self.namespace_at(at) {
            Some(own) if root_of(&own) == root_of(module) => "internal".to_string(),
            _ => "external".to_string(),
        }
    }

    fn use_clauses(&self, decl: Node) -> Vec<UseClause> {
        let mut out = Vec::new();
        // `use function A\{b, c}` carries the kind on the declaration.
        let kind_of = |n: Node| {
            n.child_by_field_name("type")
                .map(|t| self.get_node_text(t).to_ascii_lowercase())
        };
        let decl_kind = kind_of(decl);
        let mut prefix = String::new();
        let mut clauses = Vec::new();
        let mut cursor = decl.walk();
        for child in decl.named_children(&mut cursor) {
            match child.kind() {
                "namespace_name" => prefix = self.get_node_text(child).to_string(),
                "namespace_use_clause" => clauses.push(child),
                "namespace_use_group" => {
                    let mut gc = child.walk();
                    clauses.extend(
                        child
                            .named_children(&mut gc)
                            .filter(|c| c.kind() == "namespace_use_clause"),
                    );
                }
                _ => {}
            }
        }
        for clause in clauses {
            let mut target = None;
            let mut cc = clause.walk();
            for c in clause.named_children(&mut cc) {
                if matches!(c.kind(), "name" | "qualified_name") && target.is_none() {
                    target = Some(self.get_node_text(c).trim_start_matches('\\'));
                }
            }
            let Some(target) = target else { continue };
            let fqn = if prefix.is_empty() {
                target.to_string()
            } else {
                format!("{}\\{}", prefix.trim_start_matches('\\'), target)
            };
            let kind = kind_of(clause).or_else(|| decl_kind.clone());
            out.push(UseClause {
                fqn,
                alias: clause
                    .child_by_field_name("alias")
                    .map(|a| self.get_node_text(a).to_string()),
                is_symbol: kind.is_some(),
                is_const: kind.as_deref() == Some("const"),
            });
        }
        out
    }

    /// Path of an include whose argument can be evaluated without running
    /// code: a string literal, optionally prefixed by a directory anchor
    /// (`__DIR__`, `dirname(__FILE__)`, `dirname(__DIR__, 2)`...). Anchored
    /// paths are rewritten relative to the including file (`../lib/x.php`).
    fn static_path(&self, arg: Node) -> Option<String> {
        let mut parts = Vec::new();
        let mut stack = vec![arg];
        while let Some(n) = stack.pop() {
            match n.kind() {
                "parenthesized_expression" => stack.extend(n.named_child(0)),
                "binary_expression" if self.operator(n) == "." => {
                    stack.extend(n.child_by_field_name("right"));
                    stack.extend(n.child_by_field_name("left"));
                }
                _ => parts.push(n),
            }
            if parts.len() > MAX_PATH_PARTS {
                return None;
            }
        }
        let (ups, literals) = match parts.split_first() {
            Some((first, rest)) => match self.dir_anchor(*first, 0) {
                Some(ups) => (Some(ups), rest),
                None => (None, &parts[..]),
            },
            None => return None,
        };
        let mut path = String::new();
        for p in literals {
            path.push_str(&self.literal(*p)?);
        }
        if path.is_empty() {
            return None;
        }
        Some(match ups {
            Some(ups) => format!("{}{}", "../".repeat(ups), path.trim_start_matches('/')),
            None => path,
        })
    }

    /// How many directories above the current file a directory expression
    /// points at, or `None` if it is not one. Magic constants and function
    /// names are case-insensitive, and `\dirname()` is `dirname()`.
    fn dir_anchor(&self, n: Node, depth: usize) -> Option<usize> {
        if depth > MAX_PATH_PARTS {
            return None;
        }
        match n.kind() {
            "name" if self.get_node_text(n).eq_ignore_ascii_case("__DIR__") => Some(0),
            "parenthesized_expression" => self.dir_anchor(n.named_child(0)?, depth + 1),
            "function_call_expression" => {
                let func = last_segment(self.get_node_text(n.child_by_field_name("function")?));
                let args = self.argument_nodes(n);
                let first = *args.first()?;
                if func.eq_ignore_ascii_case("realpath") {
                    return self.dir_anchor(first, depth + 1);
                }
                if !func.eq_ignore_ascii_case("dirname") {
                    return None;
                }
                let levels = match args.get(1) {
                    Some(l) => self.get_node_text(*l).parse::<usize>().ok()?,
                    None => 1,
                };
                let ups = if first.kind() == "name"
                    && self.get_node_text(first).eq_ignore_ascii_case("__FILE__")
                {
                    levels.checked_sub(1)?
                } else {
                    self.dir_anchor(first, depth + 1)?.checked_add(levels)?
                };
                (ups <= MAX_PATH_PARTS).then_some(ups)
            }
            _ => None,
        }
    }

    /// Contents of a string literal without interpolation.
    fn literal(&self, n: Node) -> Option<String> {
        match n.kind() {
            "string" | "encapsed_string" => {
                let mut cursor = n.walk();
                let plain = n
                    .named_children(&mut cursor)
                    .all(|c| matches!(c.kind(), "string_content" | "escape_sequence"));
                if !plain {
                    return None;
                }
                let t = self.get_node_text(n);
                Some(t.get(1..t.len().saturating_sub(1))?.to_string())
            }
            _ => None,
        }
    }

    fn argument_nodes<'t>(&self, call: Node<'t>) -> Vec<Node<'t>> {
        let Some(args) = call
            .child_by_field_name("arguments")
            .or_else(|| self.find_child_by_kind(call, "arguments"))
        else {
            return vec![];
        };
        let mut cursor = args.walk();
        args.named_children(&mut cursor)
            .filter(|a| a.kind() == "argument")
            .filter_map(|a| a.named_children(&mut a.walk()).last())
            .collect()
    }

    fn find_child_by_kind<'t>(&self, n: Node<'t>, kind: &str) -> Option<Node<'t>> {
        let mut cursor = n.walk();
        let found = n.named_children(&mut cursor).find(|c| c.kind() == kind);
        found
    }

    fn operator(&self, n: Node) -> &str {
        n.child_by_field_name("operator")
            .map_or("", |o| self.get_node_text(o))
    }

    // Definitions

    fn extract_definitions(&self, root: Node) -> (Vec<Function>, Vec<Class>) {
        let mut functions = Vec::new();
        let mut classes = Vec::new();
        // Context: the condition of the innermost `if` / `elseif` that checks
        // `function_exists`; it guards the functions it names.
        walk(root, None, |n, guard: Option<Node>| match n.kind() {
            // Keep descending: PHP declares functions conditionally
            // (`if (!function_exists('f')) { function f() {} }`) and even
            // inside other functions, and all of them are global.
            "function_definition" => {
                if let Some(mut f) = self.parse_function(n, None) {
                    let guarded = guard.is_some_and(|c| {
                        mentions(
                            &self.get_node_text(c).to_ascii_lowercase(),
                            &f.name.to_ascii_lowercase(),
                        )
                    });
                    if guarded || self.is_stub(n) {
                        f.tags.push(FALLBACK_TAG.to_string());
                        f.tags.sort();
                    }
                    functions.push(f);
                }
                Some(guard)
            }
            "if_statement" | "else_if_clause" => Some(
                n.child_by_field_name("condition")
                    .filter(|c| {
                        self.get_node_text(*c)
                            .to_ascii_lowercase()
                            .contains("function_exists")
                    })
                    .or(guard),
            ),
            // Keep descending here too: a method body can declare global
            // functions and classes just like a function body can.
            k if CLASS_LIKE.contains(&k) => {
                classes.extend(self.parse_class(n));
                Some(guard)
            }
            _ => Some(guard),
        });
        (functions, classes)
    }

    /// An empty body, or a bare `return <literal>;`.
    fn is_stub(&self, function: Node) -> bool {
        let Some(body) = function.child_by_field_name("body") else {
            return false;
        };
        let mut cursor = body.walk();
        let statements: Vec<Node> = body
            .named_children(&mut cursor)
            .filter(|c| c.kind() != "comment")
            .collect();
        match statements.as_slice() {
            [] => true,
            [ret] if ret.kind() == "return_statement" => ret.named_child(0).is_none_or(|v| {
                matches!(
                    v.kind(),
                    "boolean" | "null" | "integer" | "string" | "encapsed_string"
                )
            }),
            _ => false,
        }
    }

    fn parse_modifiers(&self, node: Node) -> Modifiers {
        let mut m = Modifiers::default();
        let mut words = Vec::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "visibility_modifier" => {
                    m.access_modifier = Some(self.get_node_text(child).to_lowercase());
                }
                "static_modifier" => m.is_static = true,
                "abstract_modifier" => m.is_abstract = true,
                "final_modifier" => m.is_final = true,
                "readonly_modifier" => {}
                _ => continue,
            }
            words.push(self.get_node_text(child).to_lowercase());
        }
        m.text = words.join(" ");
        m
    }

    fn extract_docstring(&self, node: Node) -> String {
        match node.prev_named_sibling() {
            Some(prev)
                if prev.kind() == "comment" && self.get_node_text(prev).starts_with("/**") =>
            {
                clean_docblock(self.get_node_text(prev))
            }
            _ => String::new(),
        }
    }

    fn attributes(&self, node: Node) -> Vec<String> {
        let Some(list) = node.child_by_field_name("attributes") else {
            return vec![];
        };
        let mut cursor = list.walk();
        list.named_children(&mut cursor)
            .map(|g| collapse_ws(self.get_node_text(g)))
            .collect()
    }

    fn parse_function(&self, node: Node, class: Option<&ClassCtx>) -> Option<Function> {
        let name = self
            .get_node_text(node.child_by_field_name("name")?)
            .to_string();
        if name.is_empty() {
            return None;
        }

        let params = self.extract_parameters(node);
        let return_type = node
            .child_by_field_name("return_type")
            .map(|r| self.get_node_text(r).to_string())
            .unwrap_or_default();
        let mods = self.parse_modifiers(node);
        let docstring = self.extract_docstring(node);
        let decorators = self.attributes(node);
        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;

        let signature = self.build_signature(node, &name, &mods, &return_type);

        let body = node.child_by_field_name("body");
        let scope = Scope {
            class,
            namespace: self.namespace_at(node.start_byte()),
            vars: body.map_or_else(HashMap::new, |b| {
                self.var_types(b, class, &params, &docstring)
            }),
        };
        let (calls, variables, control_flow, exceptions, complexity) = match body {
            Some(b) => (
                self.extract_calls(b, &scope),
                self.extract_variables(b, &params),
                self.build_control_flow(b, &scope),
                self.extract_exception_info(b),
                self.calculate_complexity(b),
            ),
            None => (
                vec![],
                vec![],
                ControlFlow {
                    complexity: 1,
                    ..Default::default()
                },
                ExceptionInfo::default(),
                1,
            ),
        };

        let tags = self.auto_tag_function(&name, &docstring, &mods);
        let importance_score = self.estimate_importance(&name, &mods, &decorators);

        Some(Function {
            id: match class {
                Some(c) => self.make_function_id(&name, &self.id_name(&c.name, node.start_byte())),
                None => self.make_function_id(&self.id_name(&name, node.start_byte()), ""),
            },
            name,
            signature,
            params,
            return_type,
            docstring,
            line_start,
            line_end,
            calls,
            called_by: vec![],
            variables,
            control_flow,
            exceptions,
            complexity,
            is_async: false,
            decorators,
            tags,
            importance_score,
            lang_info: LanguageSpecificInfo {
                php: Some(Box::new(PhpInfo {
                    namespace: self.namespace_at(node.start_byte()),
                    access_modifier: mods.access_modifier,
                    is_static: mods.is_static,
                    is_abstract: mods.is_abstract,
                    is_final: mods.is_final,
                    ..Default::default()
                })),
                ..Default::default()
            },
        })
    }

    /// `public static function find($id): ?User`, parameters as written.
    fn build_signature(
        &self,
        node: Node,
        name: &str,
        mods: &Modifiers,
        return_type: &str,
    ) -> String {
        let params = node
            .child_by_field_name("parameters")
            .map_or_else(|| "()".to_string(), |p| collapse_ws(self.get_node_text(p)));
        let mut signature = String::new();
        if !mods.text.is_empty() {
            signature.push_str(&mods.text);
            signature.push(' ');
        }
        signature.push_str(&format!("function {}{}", name, params));
        if !return_type.is_empty() {
            signature.push_str(&format!(": {}", return_type));
        }
        signature
    }

    /// Top-level code as a synthetic `{main}` function, if it calls anything.
    fn build_main(&self, root: Node) -> Option<Function> {
        let scope = Scope {
            class: None,
            namespace: self.namespaces.first().and_then(|r| r.2.clone()),
            vars: self.globals.clone(),
        };
        let calls = self.extract_calls(root, &scope);
        if calls.is_empty() {
            return None;
        }
        Some(Function {
            id: self.make_function_id(MAIN_FUNCTION, ""),
            name: MAIN_FUNCTION.to_string(),
            signature: MAIN_FUNCTION.to_string(),
            line_start: 1,
            line_end: self.count_lines().max(1),
            calls,
            control_flow: self.build_control_flow(root, &scope),
            exceptions: self.extract_exception_info(root),
            complexity: self.calculate_complexity(root),
            tags: vec![SCRIPT_TAG.to_string()],
            importance_score: 0.5,
            lang_info: LanguageSpecificInfo {
                php: Some(Box::new(PhpInfo {
                    namespace: self.namespaces.first().and_then(|r| r.2.clone()),
                    is_script: true,
                    ..Default::default()
                })),
                ..Default::default()
            },
            ..Default::default()
        })
    }

    fn extract_parameters(&self, node: Node) -> Vec<Parameter> {
        let Some(list) = node.child_by_field_name("parameters") else {
            return vec![];
        };
        let mut params = Vec::new();
        let mut cursor = list.walk();
        for p in list.named_children(&mut cursor) {
            if !matches!(
                p.kind(),
                "simple_parameter" | "variadic_parameter" | "property_promotion_parameter"
            ) {
                continue;
            }
            let Some(name) = p.child_by_field_name("name") else {
                continue;
            };
            params.push(Parameter {
                name: self.get_node_text(name).to_string(),
                type_annotation: p
                    .child_by_field_name("type")
                    .map(|t| self.get_node_text(t).to_string())
                    .unwrap_or_default(),
                default_value: p
                    .child_by_field_name("default_value")
                    .map(|d| truncate(self.get_node_text(d))),
            });
        }
        params
    }

    /// Class named by a type hint or docblock type, fully qualified, if it
    /// names exactly one: `Foo`, `?Foo`, `Foo|null`, `Foo|false`. Arrays,
    /// generics and unions of several classes name none.
    fn class_of_type(&self, hint: &str, class: Option<&ClassCtx>, at: usize) -> Option<String> {
        let mut parts = hint
            .trim()
            .trim_start_matches('?')
            .split('|')
            .map(str::trim)
            .filter(|p| !p.eq_ignore_ascii_case("null") && !p.eq_ignore_ascii_case("false"));
        let hint = match (parts.next(), parts.next()) {
            (Some(h), None) => h,
            _ => return None,
        };
        if hint.is_empty() || hint.contains(['&', ' ', '(', '[', '<', '{', '$']) {
            return None;
        }
        let lower = hint.to_ascii_lowercase();
        if lower == "self" || lower == "static" {
            return class.map(|c| c.fqn.clone());
        }
        if BUILTIN_TYPES.contains(&lower.as_str()) {
            return None;
        }
        Some(self.qualify(hint, at))
    }

    /// Class instantiated by a `new` expression, fully qualified (`self` and
    /// `static` included), or the variable of `new $class`.
    fn new_class(&self, n: Node, class: Option<&ClassCtx>) -> Option<String> {
        let mut cursor = n.walk();
        let target = n.named_children(&mut cursor).find(|c| {
            matches!(
                c.kind(),
                "name" | "qualified_name" | "relative_name" | "variable_name"
            )
        })?;
        if target.kind() == "variable_name" {
            return Some(self.get_node_text(target).to_string());
        }
        match self.get_node_text(target).to_ascii_lowercase().as_str() {
            "self" | "static" => class.map(|c| c.fqn.clone()),
            "parent" => None,
            _ => Some(self.qualify(self.get_node_text(target), target.start_byte())),
        }
    }

    /// `$param` -> class, from the type hint or, when it names no class,
    /// the docblock's `@param`.
    fn param_types(
        &self,
        params: &[Parameter],
        doc: &str,
        class: Option<&ClassCtx>,
        at: usize,
    ) -> HashMap<String, String> {
        let mut templates = doc_templates(doc);
        templates.extend(class.map_or(&[][..], |c| &c.templates[..]).iter().cloned());
        let documented: HashMap<&str, &str> = doc_tags(doc, "@param")
            .filter_map(|(ty, var)| Some((var?, ty)))
            .collect();
        params
            .iter()
            .filter_map(|p| {
                let cls = self
                    .class_of_type(&p.type_annotation, class, at)
                    .or_else(|| {
                        let ty = untemplate(documented.get(p.name.as_str())?, &templates)?;
                        self.class_of_type(&ty, class, at)
                    })?;
                Some((p.name.clone(), cls))
            })
            .collect()
    }

    /// `$var` -> class it holds, from `$var = new Class`, `$var =
    /// $this->own()`, typed or documented parameters (closures' included)
    /// and, inside functions, `global $var` of a known top-level object or
    /// a documented `@global`. A variable seen with two classes is dropped.
    fn var_types(
        &self,
        region: Node,
        class: Option<&ClassCtx>,
        params: &[Parameter],
        doc: &str,
    ) -> HashMap<String, String> {
        let mut types: HashMap<String, Option<String>> = HashMap::new();
        let mut note = |var: &str, cls: String| {
            types
                .entry(var.to_string())
                .and_modify(|t| {
                    if t.as_deref() != Some(cls.as_str()) {
                        *t = None;
                    }
                })
                .or_insert(Some(cls));
        };
        let at = region.start_byte();
        for (var, cls) in self.param_types(params, doc, class, at) {
            note(&var, cls);
        }
        let documented_globals: HashMap<&str, &str> = doc_tags(doc, "@global")
            .filter_map(|(ty, var)| Some((var?, ty)))
            .collect();
        walk(region, (), |n, _| {
            if n != region && is_definition(n) {
                return None;
            }
            match n.kind() {
                "assignment_expression" => {
                    let left = n.child_by_field_name("left");
                    let right = n.child_by_field_name("right").map(|r| self.unparen(r));
                    if let (Some(l), Some(r)) = (left, right) {
                        let assigned = match r.kind() {
                            "object_creation_expression" => self.new_class(r, class),
                            _ => self.own_return(r, class),
                        };
                        if let (true, Some(cls)) = (l.kind() == "variable_name", assigned) {
                            if !cls.starts_with('$') {
                                note(self.get_node_text(l), cls);
                            }
                        }
                    }
                }
                "global_declaration" => {
                    let mut cursor = n.walk();
                    for v in n.named_children(&mut cursor) {
                        let var = self.get_node_text(v);
                        let cls = self.globals.get(var).cloned().or_else(|| {
                            self.class_of_type(documented_globals.get(var)?, class, n.start_byte())
                        });
                        if let Some(cls) = cls {
                            note(var, cls);
                        }
                    }
                }
                _ => {}
            }
            Some(())
        });
        types
            .into_iter()
            .filter_map(|(k, v)| v.map(|v| (k, v)))
            .collect()
    }

    /// Class returned by `$this->m()` / `self::m()` / `static::m()` when `m`
    /// is one of the enclosing class's own methods with a class return type.
    fn own_return(&self, call: Node, class: Option<&ClassCtx>) -> Option<String> {
        let class = class?;
        let on_self = match call.kind() {
            "member_call_expression" | "nullsafe_member_call_expression" => call
                .child_by_field_name("object")
                .is_some_and(|o| self.get_node_text(o) == "$this"),
            "scoped_call_expression" => call.child_by_field_name("scope").is_some_and(|s| {
                s.kind() == "relative_scope"
                    && matches!(
                        self.get_node_text(s).to_ascii_lowercase().as_str(),
                        "self" | "static"
                    )
            }),
            _ => false,
        };
        if !on_self {
            return None;
        }
        let name = self.get_node_text(call.child_by_field_name("name")?);
        class.returns.get(&name.to_ascii_lowercase()).cloned()
    }

    fn unparen<'t>(&self, mut n: Node<'t>) -> Node<'t> {
        while n.kind() == "parenthesized_expression" {
            match n.named_child(0) {
                Some(inner) => n = inner,
                None => break,
            }
        }
        n
    }

    // Calls

    fn extract_calls(&self, region: Node, scope: &Scope) -> Vec<FunctionCall> {
        let mut calls = Vec::new();
        let mut seen: HashSet<(String, usize)> = HashSet::new();
        // `fn (User $u) => $u->save()`: a closure's parameters type its own
        // variables, shadowing the enclosing ones, and mean nothing outside
        // it. Context `(_, i)` reads `closures[i - 1]`, or `scope` for 0.
        let mut closures: Vec<Scope> = Vec::new();
        walk(
            region,
            ("unconditional", 0usize),
            |n, (context, closure)| {
                if n != region && is_definition(n) {
                    return None;
                }
                let scope = match closure {
                    0 => scope,
                    i => &closures[i - 1],
                };
                if let Some(callee) = self.callee_of(n, scope) {
                    let line = n.start_position().row + 1;
                    if seen.insert((callee.clone(), line)) {
                        calls.push(FunctionCall {
                            callee,
                            defined_in: None, // resolved in post-processing
                            line,
                            args: self.extract_call_arguments(n),
                            is_conditional: context != "unconditional",
                            context: context.to_string(),
                        });
                    }
                }
                let context = match n.kind() {
                    "if_statement" => "if",
                    "else_if_clause" => "elif",
                    "else_clause" => "else",
                    "while_statement" | "do_statement" | "for_statement" | "foreach_statement" => {
                        "loop"
                    }
                    "switch_statement" | "match_expression" => "switch",
                    "try_statement" => "try",
                    "catch_clause" => "catch",
                    _ => context,
                };
                if n != region && matches!(n.kind(), "anonymous_function" | "arrow_function") {
                    let params = self.extract_parameters(n);
                    let mut vars = scope.vars.clone();
                    for p in &params {
                        vars.remove(&p.name);
                    }
                    vars.extend(self.var_types(n, scope.class, &params, ""));
                    closures.push(Scope {
                        class: scope.class,
                        namespace: scope.namespace.clone(),
                        vars,
                    });
                    return Some((context, closures.len()));
                }
                Some((context, closure))
            },
        );
        calls
    }

    /// The callee string for a call-like node, in the shapes listed in the
    /// module docs; `None` for anything that is not a call.
    fn callee_of(&self, n: Node, scope: &Scope) -> Option<String> {
        self.callee_at(n, scope, 0)
    }

    /// `callee_of` for a call nested `depth` receivers deep, so a chain of
    /// static calls (`A::b()::c()::...`) stays within `receiver`'s depth cap.
    fn callee_at(&self, n: Node, scope: &Scope, depth: usize) -> Option<String> {
        match n.kind() {
            "function_call_expression" => {
                let func = n.child_by_field_name("function")?;
                match func.kind() {
                    "name" => {
                        let name = self.get_node_text(func);
                        if CONSTRUCTS.iter().any(|c| name.eq_ignore_ascii_case(c)) {
                            None
                        } else {
                            // `use function A\f as g; g()` calls `A\f`.
                            let at = func.start_byte();
                            let key = (self.region_at(at), name.to_ascii_lowercase());
                            Some(match (self.fn_uses.get(&key), self.namespace_at(at)) {
                                (Some(f), _) => format!("\\{}", f),
                                (None, here) if here == scope.namespace => name.to_string(),
                                // `{main}` code in a later region: `B\f` is
                                // "B's `f`, else the global one".
                                (None, Some(ns)) => format!("{}\\{}", ns, name),
                                (None, None) => format!("\\{}", name),
                            })
                        }
                    }
                    "qualified_name" | "relative_name" => {
                        Some(self.qualify(self.get_node_text(func), func.start_byte()))
                    }
                    // `$handler()`: dynamic, kept so it shows up as a call site.
                    "variable_name" => Some(self.get_node_text(func).to_string()),
                    _ => None,
                }
            }
            "member_call_expression" | "nullsafe_member_call_expression" => {
                let method = self.get_node_text(n.child_by_field_name("name")?);
                let receiver = self.receiver(n.child_by_field_name("object")?, scope, depth);
                Some(format!("{}.{}", receiver, method))
            }
            "scoped_call_expression" => {
                let method = self.get_node_text(n.child_by_field_name("name")?);
                let s = n.child_by_field_name("scope")?;
                let qualifier = match s.kind() {
                    // `parent::` inside `new class extends X` is X: those
                    // methods belong to the enclosing code.
                    "relative_scope" => match self.get_node_text(s).to_ascii_lowercase().as_str() {
                        "parent" => self
                            .anonymous_class_base(s)
                            .filter(|base| base != "?")
                            .unwrap_or_else(|| "parent".to_string()),
                        other => other.to_string(),
                    },
                    "name" | "qualified_name" | "relative_name" => {
                        self.qualify(self.get_node_text(s), s.start_byte())
                    }
                    _ => self.receiver(s, scope, depth),
                };
                Some(format!("{}::{}", qualifier, method))
            }
            "object_creation_expression" => self
                .new_class(n, scope.class)
                .map(|cls| format!("new {}", cls)),
            _ => None,
        }
    }

    /// Compact rendering of a method call's receiver. Known object types
    /// replace the variable (`$repo` -> `Repo`); anything else keeps enough
    /// shape to read (`this.db`, `$rows`, `getDb()`).
    fn receiver(&self, n: Node, scope: &Scope, depth: usize) -> String {
        if depth > 3 {
            return "?".to_string();
        }
        match n.kind() {
            "variable_name" => {
                let v = self.get_node_text(n);
                if v == "$this" {
                    self.anonymous_class_base(n)
                        .unwrap_or_else(|| "this".to_string())
                } else {
                    scope.vars.get(v).cloned().unwrap_or_else(|| v.to_string())
                }
            }
            "member_access_expression" | "nullsafe_member_access_expression" => {
                let prop = n
                    .child_by_field_name("name")
                    .map_or("?", |p| self.get_node_text(p));
                let object = n.child_by_field_name("object");
                if object.is_some_and(|o| self.get_node_text(o) == "$this") {
                    if let Some(cls) = scope.class.and_then(|c| c.props.get(prop)) {
                        return cls.clone();
                    }
                }
                let base =
                    object.map_or_else(|| "?".to_string(), |o| self.receiver(o, scope, depth + 1));
                format!("{}.{}", base, prop)
            }
            "member_call_expression" | "nullsafe_member_call_expression" => {
                if let Some(cls) = self.own_return(n, scope.class) {
                    return cls;
                }
                let name = n
                    .child_by_field_name("name")
                    .map_or("?", |p| self.get_node_text(p));
                let base = n
                    .child_by_field_name("object")
                    .map_or_else(|| "?".to_string(), |o| self.receiver(o, scope, depth + 1));
                format!("{}.{}()", base, name)
            }
            "function_call_expression" | "scoped_call_expression" => self
                .own_return(n, scope.class)
                .or_else(|| {
                    self.callee_at(n, scope, depth + 1)
                        .map(|c| format!("{}()", c))
                })
                .unwrap_or_else(|| "?".to_string()),
            "scoped_property_access_expression" => {
                let prop = n
                    .child_by_field_name("name")
                    .map_or("?", |p| self.get_node_text(p));
                // `self::$x` / `static::$x` / `parent::$x` name no class.
                let cls = n.child_by_field_name("scope").map_or_else(
                    || "?".to_string(),
                    |s| match s.kind() {
                        "relative_scope" => self.get_node_text(s).to_ascii_lowercase(),
                        _ => self.qualify(self.get_node_text(s), s.start_byte()),
                    },
                );
                format!("{}::{}", cls, prop)
            }
            // `(new Foo)->bar()`: the receiver *is* a Foo.
            "object_creation_expression" => self
                .new_class(n, scope.class)
                .unwrap_or_else(|| "?".to_string()),
            // `unparen` strips any number of parentheses without recursing,
            // and without spending the depth budget on them.
            "parenthesized_expression" => match self.unparen(n) {
                inner if inner != n => self.receiver(inner, scope, depth),
                _ => "?".to_string(),
            },
            "name" | "qualified_name" | "relative_name" => {
                self.qualify(self.get_node_text(n), n.start_byte())
            }
            _ => "?".to_string(),
        }
    }

    /// For `$this` inside `new class extends X { ... }`, whose methods belong
    /// to the enclosing code: `\X`, or `?` when the class extends nothing.
    /// `None` when `$this` is not inside an anonymous class.
    fn anonymous_class_base(&self, this: Node) -> Option<String> {
        let mut node = this;
        // Bounded: each `parent()` walks down from the root.
        for _ in 0..64 {
            node = node.parent()?;
            match node.kind() {
                "anonymous_class" => {
                    let base = self
                        .find_child_by_kind(node, "base_clause")
                        .and_then(|b| b.named_child(0));
                    return Some(base.map_or_else(
                        || "?".to_string(),
                        |b| self.qualify(self.get_node_text(b), b.start_byte()),
                    ));
                }
                k if k == "function_definition" || CLASS_LIKE.contains(&k) => return None,
                _ => {}
            }
        }
        None
    }

    fn extract_call_arguments(&self, call: Node) -> Vec<String> {
        self.argument_nodes(call)
            .into_iter()
            .filter(|a| {
                matches!(
                    a.kind(),
                    "variable_name"
                        | "string"
                        | "encapsed_string"
                        | "integer"
                        | "float"
                        | "boolean"
                        | "null"
                        | "name"
                )
            })
            .map(|a| truncate(self.get_node_text(a)))
            .collect()
    }

    // Variables and control flow

    fn extract_variables(&self, body: Node, params: &[Parameter]) -> Vec<Variable> {
        let mut variables: HashMap<String, Variable> = HashMap::new();
        let new_var = |name: &str, scope: &str, line: Option<usize>| Variable {
            name: name.to_string(),
            var_type: None,
            scope: scope.to_string(),
            defined_at: line,
            transformations: vec![],
            used_in: vec![],
            returned: false,
        };
        for p in params {
            let mut v = new_var(&p.name, "param", None);
            if !p.type_annotation.is_empty() {
                v.var_type = Some(p.type_annotation.clone());
            }
            variables.insert(p.name.clone(), v);
        }
        let empty = Scope {
            class: None,
            namespace: self.namespace_at(body.start_byte()),
            vars: HashMap::new(),
        };
        walk(body, (), |n, _| {
            if n != body && is_definition(n) {
                return None;
            }
            let line = n.start_position().row + 1;
            match n.kind() {
                "assignment_expression" => {
                    let (Some(left), Some(right)) = (
                        n.child_by_field_name("left"),
                        n.child_by_field_name("right"),
                    ) else {
                        return Some(());
                    };
                    if left.kind() != "variable_name" {
                        return Some(());
                    }
                    let name = self.get_node_text(left);
                    let via = self.callee_of(self.unparen(right), &empty);
                    match (variables.get_mut(name), via) {
                        (Some(var), Some(via)) => var.transformations.push(VarTransformation {
                            line,
                            via,
                            becomes: name.to_string(),
                        }),
                        (Some(_), None) => {}
                        (None, via) => {
                            let mut v = new_var(name, "local", Some(line));
                            v.var_type = via.and_then(|c| c.strip_prefix("new ").map(String::from));
                            variables.insert(name.to_string(), v);
                        }
                    }
                }
                "global_declaration" => {
                    let mut cursor = n.walk();
                    for g in n.named_children(&mut cursor) {
                        let name = self.get_node_text(g);
                        variables
                            .entry(name.to_string())
                            .or_insert_with(|| new_var(name, "global", Some(line)))
                            .scope = "global".to_string();
                    }
                }
                "return_statement" => {
                    if let Some(value) = n.named_child(0) {
                        if let Some(var) = variables.get_mut(self.get_node_text(value)) {
                            var.returned = true;
                        }
                    }
                }
                _ => {}
            }
            Some(())
        });
        let mut out: Vec<Variable> = variables.into_values().collect();
        out.sort_by(|a, b| (a.defined_at, &a.name).cmp(&(b.defined_at, &b.name)));
        out
    }

    fn extract_calls_from_block(&self, n: Node, scope: &Scope) -> Vec<String> {
        let mut seen = HashSet::new();
        self.extract_calls(n, scope)
            .into_iter()
            .map(|c| c.callee)
            .filter(|c| seen.insert(c.clone()))
            .collect()
    }

    fn build_control_flow(&self, region: Node, scope: &Scope) -> ControlFlow {
        let mut cf = ControlFlow {
            complexity: self.calculate_complexity(region),
            ..Default::default()
        };
        walk(region, (), |n, _| {
            if n != region && is_definition(n) {
                return None;
            }
            let line = n.start_position().row + 1;
            match n.kind() {
                "if_statement" => cf.branches.push(Branch {
                    branch_type: "if".to_string(),
                    condition: self.extract_condition(n),
                    line,
                    true_path: self.extract_execution_path(n.child_by_field_name("body"), scope),
                    false_path: n.child_by_field_name("alternative").map(|alt| {
                        // `else if (...)` is `elseif` written as a nested
                        // statement: take that branch's body, as for an
                        // `else_if_clause`, not the whole rest of the chain.
                        let body = alt.child_by_field_name("body").map(|b| match b.kind() {
                            "if_statement" => b.child_by_field_name("body").unwrap_or(b),
                            _ => b,
                        });
                        self.extract_execution_path(body.or(Some(alt)), scope)
                    }),
                }),
                "while_statement" | "do_statement" | "for_statement" | "foreach_statement" => {
                    cf.loops.push(Loop {
                        loop_type: n.kind().trim_end_matches("_statement").to_string(),
                        condition: if n.kind() == "foreach_statement" {
                            self.foreach_header(n)
                        } else {
                            self.extract_condition(n)
                        },
                        line,
                        calls: self.extract_calls_from_block(n, scope),
                    })
                }
                "try_statement" => {
                    let mut except_clauses = Vec::new();
                    let mut finally_calls = Vec::new();
                    let mut cursor = n.walk();
                    for c in n.named_children(&mut cursor) {
                        match c.kind() {
                            "catch_clause" => except_clauses.push(ExceptClause {
                                exception_type: c.child_by_field_name("type").map_or_else(
                                    || "Exception".to_string(),
                                    |t| self.get_node_text(t).to_string(),
                                ),
                                line: c.start_position().row + 1,
                                calls: self.extract_calls_from_block(c, scope),
                            }),
                            "finally_clause" => {
                                finally_calls = self.extract_calls_from_block(c, scope)
                            }
                            _ => {}
                        }
                    }
                    cf.try_blocks.push(TryBlock {
                        line,
                        try_calls: n
                            .child_by_field_name("body")
                            .map_or_else(Vec::new, |b| self.extract_calls_from_block(b, scope)),
                        except_clauses,
                        finally_calls,
                    });
                }
                _ => {}
            }
            Some(())
        });
        cf
    }

    fn extract_condition(&self, n: Node) -> String {
        n.child_by_field_name("condition")
            .map(|c| collapse_ws(self.get_node_text(self.unparen(c))))
            .unwrap_or_default()
    }

    /// `foreach ($rows as $k => $v)` -> `$rows as $k => $v`
    fn foreach_header(&self, n: Node) -> String {
        let text = self.get_node_text(n);
        let end = n
            .child_by_field_name("body")
            .map_or(text.len(), |b| b.start_byte() - n.start_byte());
        let header = text[..end].trim_start_matches("foreach").trim();
        collapse_ws(
            header
                .trim_start_matches('(')
                .trim_end_matches(':')
                .trim_end()
                .trim_end_matches(')'),
        )
    }

    fn extract_execution_path(&self, block: Option<Node>, scope: &Scope) -> ExecutionPath {
        let Some(block) = block else {
            return ExecutionPath {
                calls: vec![],
                returns: None,
                raises: None,
            };
        };
        let mut returns = None;
        let mut raises = None;
        let mut cursor = block.walk();
        for stmt in block.named_children(&mut cursor) {
            match stmt.kind() {
                "return_statement" if returns.is_none() => {
                    returns = stmt.named_child(0).map(|v| truncate(self.get_node_text(v)));
                }
                "expression_statement" if raises.is_none() => {
                    raises = stmt
                        .named_child(0)
                        .filter(|e| e.kind() == "throw_expression")
                        .and_then(|e| self.thrown(e));
                }
                _ => {}
            }
        }
        ExecutionPath {
            calls: self.extract_calls_from_block(block, scope),
            returns,
            raises,
        }
    }

    fn thrown(&self, throw: Node) -> Option<String> {
        let value = self.unparen(throw.named_child(0)?);
        if value.kind() == "object_creation_expression" {
            self.new_class(value, None)
                .map(|c| last_segment(&c).to_string())
        } else {
            Some(truncate(self.get_node_text(value)))
        }
    }

    fn extract_exception_info(&self, region: Node) -> ExceptionInfo {
        let mut info = ExceptionInfo::default();
        walk(region, (), |n, _| {
            if n != region && is_definition(n) {
                return None;
            }
            match n.kind() {
                "throw_expression" => {
                    if let Some(t) = self.thrown(n) {
                        if !info.raises.contains(&t) {
                            info.raises.push(t);
                        }
                    }
                }
                "catch_clause" => {
                    if let Some(types) = n.child_by_field_name("type") {
                        let mut cursor = types.walk();
                        for t in types.named_children(&mut cursor) {
                            let t = last_segment(self.get_node_text(t)).to_string();
                            if !info.handles.contains(&t) {
                                info.handles.push(t);
                            }
                        }
                    }
                }
                _ => {}
            }
            Some(())
        });
        info
    }

    fn calculate_complexity(&self, region: Node) -> usize {
        let mut complexity = 1;
        walk(region, (), |n, _| {
            if n != region && is_definition(n) {
                return None;
            }
            match n.kind() {
                "if_statement"
                | "else_if_clause"
                | "while_statement"
                | "do_statement"
                | "for_statement"
                | "foreach_statement"
                | "case_statement"
                | "catch_clause"
                | "conditional_expression"
                | "match_conditional_expression" => complexity += 1,
                "binary_expression" => {
                    let op = self.operator(n).to_ascii_lowercase();
                    if matches!(op.as_str(), "&&" | "||" | "and" | "or") {
                        complexity += 1;
                    }
                }
                _ => {}
            }
            Some(())
        });
        complexity
    }

    // Classes

    fn parse_class(&self, node: Node) -> Option<Class> {
        let name = self
            .get_node_text(node.child_by_field_name("name")?)
            .to_string();
        if name.is_empty() {
            return None;
        }
        let type_kind = match node.kind() {
            "interface_declaration" => PhpTypeKind::Interface,
            "trait_declaration" => PhpTypeKind::Trait,
            "enum_declaration" => PhpTypeKind::Enum,
            _ => PhpTypeKind::Class,
        };
        let names_in = |clause: Node| -> Vec<String> {
            let mut cursor = clause.walk();
            clause
                .named_children(&mut cursor)
                .filter(|c| matches!(c.kind(), "name" | "qualified_name" | "relative_name"))
                .map(|c| self.qualify(self.get_node_text(c), c.start_byte()))
                .collect()
        };

        let mut extends = Vec::new();
        let mut implements = Vec::new();
        let mut cursor = node.walk();
        for child in node.named_children(&mut cursor) {
            match child.kind() {
                "base_clause" => extends = names_in(child),
                "class_interface_clause" => implements = names_in(child),
                _ => {}
            }
        }

        let mut traits = Vec::new();
        let mut attributes = Vec::new();
        let mut method_nodes = Vec::new();
        let fqn = match self.namespace_at(node.start_byte()) {
            Some(ns) => format!("\\{}\\{}", ns, name),
            None => format!("\\{}", name),
        };
        let mut ctx = ClassCtx {
            name: name.clone(),
            fqn,
            props: HashMap::new(),
            returns: HashMap::new(),
            templates: doc_templates(&self.extract_docstring(node)),
        };
        if let Some(body) = node.child_by_field_name("body") {
            let mut cursor = body.walk();
            for member in body.named_children(&mut cursor) {
                match member.kind() {
                    "use_declaration" => traits.extend(names_in(member)),
                    "method_declaration" => method_nodes.push(member),
                    "property_declaration" => {
                        let hint = member
                            .child_by_field_name("type")
                            .map(|t| self.get_node_text(t).to_string())
                            .unwrap_or_default();
                        // Pre-7.4 properties carry their type in `@var`.
                        let doc = self.extract_docstring(member);
                        let typed = match doc_tags(&doc, "@var").next() {
                            Some((ty, _)) if hint.is_empty() => {
                                untemplate(ty, &ctx.templates).unwrap_or_default()
                            }
                            _ => hint.clone(),
                        };
                        let mut pc = member.walk();
                        for el in member.named_children(&mut pc) {
                            if el.kind() != "property_element" {
                                continue;
                            }
                            let Some(var) = el.child_by_field_name("name") else {
                                continue;
                            };
                            let prop = self.get_node_text(var).to_string();
                            if let Some(cls) =
                                self.class_of_type(&typed, Some(&ctx), member.start_byte())
                            {
                                ctx.props
                                    .insert(prop.trim_start_matches('$').to_string(), cls);
                            }
                            attributes.push(Attribute {
                                name: prop,
                                type_annotation: hint.clone(),
                                value: el
                                    .child_by_field_name("default_value")
                                    .map(|v| truncate(self.get_node_text(v))),
                            });
                        }
                    }
                    "const_declaration" => {
                        let mut cc = member.walk();
                        for el in member.named_children(&mut cc) {
                            if el.kind() != "const_element" {
                                continue;
                            }
                            let mut ec = el.walk();
                            let parts: Vec<Node> = el.named_children(&mut ec).collect();
                            if let Some(n) = parts.first() {
                                attributes.push(Attribute {
                                    name: self.get_node_text(*n).to_string(),
                                    type_annotation: "const".to_string(),
                                    value: parts.get(1).map(|v| truncate(self.get_node_text(*v))),
                                });
                            }
                        }
                    }
                    "enum_case" => {
                        if let Some(n) = member.child_by_field_name("name") {
                            attributes.push(Attribute {
                                name: self.get_node_text(n).to_string(),
                                type_annotation: "enum_constant".to_string(),
                                value: member
                                    .child_by_field_name("value")
                                    .map(|v| truncate(self.get_node_text(v))),
                            });
                        }
                    }
                    _ => {}
                }
            }
        }
        self.collect_property_types(&method_nodes, &mut ctx);
        self.collect_return_types(&method_nodes, &mut ctx);

        let methods: Vec<Function> = method_nodes
            .iter()
            .filter_map(|m| self.parse_function(*m, Some(&ctx)))
            .collect();

        let bases = extends
            .iter()
            .chain(&implements)
            .chain(&traits)
            .cloned()
            .collect();
        let mods = self.parse_modifiers(node);

        Some(Class {
            id: self.make_class_id(&self.id_name(&name, node.start_byte())),
            name,
            bases,
            docstring: self.extract_docstring(node),
            line_start: node.start_position().row + 1,
            line_end: node.end_position().row + 1,
            methods,
            attributes,
            decorators: self.attributes(node),
            lang_info: LanguageSpecificInfo {
                php: Some(Box::new(PhpInfo {
                    namespace: self.namespace_at(node.start_byte()),
                    is_abstract: mods.is_abstract,
                    is_final: mods.is_final,
                    type_kind: Some(type_kind),
                    extends,
                    implements,
                    traits,
                    ..Default::default()
                })),
                ..Default::default()
            },
        })
    }

    /// Property types from promoted constructor parameters, from
    /// `$this->prop = new Class` and from `$this->prop = $param` with a typed
    /// parameter, anywhere in the class. Typed properties are read with the
    /// declarations; a property seen with two classes is dropped.
    fn collect_property_types(&self, methods: &[Node], ctx: &mut ClassCtx) {
        let mut found: HashMap<String, Option<String>> = HashMap::new();
        for m in methods {
            let params = self.extract_parameters(*m);
            let param_types = self.param_types(
                &params,
                &self.extract_docstring(*m),
                Some(ctx),
                m.start_byte(),
            );
            if let Some(list) = m.child_by_field_name("parameters") {
                let mut cursor = list.walk();
                for p in list.named_children(&mut cursor) {
                    if p.kind() != "property_promotion_parameter" {
                        continue;
                    }
                    let name = p.child_by_field_name("name").map(|n| self.get_node_text(n));
                    if let Some(cls) = name.and_then(|n| param_types.get(n)) {
                        let prop = name.unwrap_or_default().trim_start_matches('$');
                        ctx.props.insert(prop.to_string(), cls.clone());
                    }
                }
            }
            let Some(body) = m.child_by_field_name("body") else {
                continue;
            };
            walk(body, (), |n, _| {
                if n.kind() == "assignment_expression" {
                    let left = n.child_by_field_name("left");
                    let right = n.child_by_field_name("right").map(|r| self.unparen(r));
                    if let (Some(l), Some(r)) = (left, right) {
                        let on_this = l.kind() == "member_access_expression"
                            && l.child_by_field_name("object")
                                .is_some_and(|o| self.get_node_text(o) == "$this");
                        let assigned = match r.kind() {
                            "object_creation_expression" => self.new_class(r, Some(ctx)),
                            "variable_name" => param_types.get(self.get_node_text(r)).cloned(),
                            _ => None,
                        };
                        if on_this {
                            let prop = l.child_by_field_name("name").map(|p| self.get_node_text(p));
                            if let (Some(prop), Some(cls)) = (prop, assigned) {
                                found
                                    .entry(prop.to_string())
                                    .and_modify(|t| {
                                        if t.as_deref() != Some(cls.as_str()) {
                                            *t = None;
                                        }
                                    })
                                    .or_insert(Some(cls));
                            }
                        }
                    }
                }
                Some(())
            });
        }
        for (prop, cls) in found {
            match cls {
                Some(cls) if !cls.starts_with('$') => {
                    ctx.props.entry(prop).or_insert(cls);
                }
                _ => {
                    ctx.props.remove(&prop);
                }
            }
        }
    }

    /// Classes returned by the class's own methods, from the return type or
    /// `@return`; `: static` / `: self` (fluent setters) return the class.
    fn collect_return_types(&self, methods: &[Node], ctx: &mut ClassCtx) {
        for m in methods {
            let Some(name) = m.child_by_field_name("name") else {
                continue;
            };
            let declared = m
                .child_by_field_name("return_type")
                .map(|r| self.get_node_text(r).to_string());
            let doc = self.extract_docstring(*m);
            let mut templates = doc_templates(&doc);
            templates.extend(ctx.templates.iter().cloned());
            let ty = declared.or_else(|| {
                let (t, _) = doc_tags(&doc, "@return").next()?;
                untemplate(t, &templates)
            });
            if let Some(cls) = ty.and_then(|t| self.class_of_type(&t, Some(ctx), m.start_byte())) {
                ctx.returns
                    .insert(self.get_node_text(name).to_ascii_lowercase(), cls);
            }
        }
    }

    // File-level data

    /// `define('X', ...)`, `const X = ...`, top-level `$x = ...` and the
    /// variables named by `global $x`; first occurrence wins.
    fn extract_global_vars(&self, root: Node) -> Vec<GlobalVar> {
        let mut vars: Vec<GlobalVar> = Vec::new();
        let mut seen: HashSet<String> = HashSet::new();
        let mut push = |name: String, kind: &str, value: Option<String>, line: usize| {
            if seen.insert(name.clone()) {
                vars.push(GlobalVar {
                    name,
                    type_annotation: kind.to_string(),
                    value,
                    line,
                });
            }
        };
        walk(root, false, |n, in_function| {
            let line = n.start_position().row + 1;
            match n.kind() {
                k if CLASS_LIKE.contains(&k) => return None,
                k if FUNCTION_LIKE.contains(&k) => return Some(true),
                "function_call_expression" => {
                    let is_define = n.child_by_field_name("function").is_some_and(|f| {
                        last_segment(self.get_node_text(f)).eq_ignore_ascii_case("define")
                    });
                    let args = self.argument_nodes(n);
                    if let (true, Some(name)) =
                        (is_define, args.first().and_then(|a| self.literal(*a)))
                    {
                        let value = args.get(1).map(|v| truncate(self.get_node_text(*v)));
                        push(name, "constant", value, line);
                    }
                }
                "const_declaration" if !in_function => {
                    let mut cursor = n.walk();
                    for el in n.named_children(&mut cursor) {
                        let mut ec = el.walk();
                        let parts: Vec<Node> = el.named_children(&mut ec).collect();
                        if let (Some(name), "const_element") = (parts.first(), el.kind()) {
                            let value = parts.get(1).map(|v| truncate(self.get_node_text(*v)));
                            push(self.get_node_text(*name).to_string(), "const", value, line);
                        }
                    }
                }
                "assignment_expression" if !in_function => {
                    let left = n.child_by_field_name("left");
                    if let Some(l) = left.filter(|l| l.kind() == "variable_name") {
                        let name = self.get_node_text(l).to_string();
                        let kind = self.globals.get(&name).cloned().unwrap_or_default();
                        let value = n
                            .child_by_field_name("right")
                            .map(|r| truncate(self.get_node_text(r)));
                        push(name, &kind, value, line);
                    }
                }
                "global_declaration" => {
                    let mut cursor = n.walk();
                    for v in n.named_children(&mut cursor) {
                        push(self.get_node_text(v).to_string(), "global", None, line);
                    }
                }
                _ => {}
            }
            Some(in_function)
        });
        vars
    }

    /// `extract_todos` reads `//` and `/* */` comments; PHP also has `#`.
    fn collect_todos(&self, root: Node) -> Vec<Todo> {
        let mut hashes = Vec::new();
        walk(root, (), |n, _| {
            if n.kind() == "comment" {
                let t = self.get_node_text(n);
                if t.starts_with('#') && !t.starts_with("#[") {
                    hashes.push(n.start_byte());
                }
                return None;
            }
            Some(())
        });
        if hashes.is_empty() {
            return extract_todos(&self.source_code);
        }
        // Rewrite `#` as `//`: lines are unchanged, so are line numbers.
        let mut src = String::with_capacity(self.source_code.len() + hashes.len());
        let mut last = 0;
        for at in hashes {
            src.push_str(&self.source_code[last..at]);
            src.push_str("//");
            last = at + 1;
        }
        src.push_str(&self.source_code[last..]);
        extract_todos(&src)
    }

    /// True if `expr` splices raw variables into a string: concatenation or
    /// interpolation. Function calls are not followed, since that is where
    /// escaping helpers (`intval`, `mysql_real_escape_string`) sit.
    fn splices_variables(&self, expr: Node) -> bool {
        let mut found = false;
        walk(expr, false, |n, in_string| {
            if found {
                return None;
            }
            match n.kind() {
                "function_call_expression"
                | "member_call_expression"
                | "nullsafe_member_call_expression"
                | "scoped_call_expression" => None,
                // A closure's body is not part of the value being built, and
                // its own assignments are checked on their own.
                k if FUNCTION_LIKE.contains(&k) => None,
                "binary_expression" => Some(in_string || self.operator(n) == "."),
                "encapsed_string" | "heredoc" => Some(true),
                "variable_name"
                | "subscript_expression"
                | "member_access_expression"
                | "nullsafe_member_access_expression"
                | "scoped_property_access_expression"
                    if in_string =>
                {
                    found = true;
                    None
                }
                _ => Some(in_string),
            }
        });
        found
    }

    fn detect_security_patterns(&self, root: Node) -> Vec<SecurityNote> {
        let scope_of = |n: Node, scope: usize| {
            if FUNCTION_LIKE.contains(&n.kind()) {
                n.start_byte()
            } else {
                scope
            }
        };

        // Pass 1: variables that hold SQL built from other variables, per scope.
        let mut spliced: HashSet<(usize, &str)> = HashSet::new();
        walk(root, 0usize, |n, scope| {
            let scope = scope_of(n, scope);
            if matches!(
                n.kind(),
                "assignment_expression" | "augmented_assignment_expression"
            ) {
                let left = n
                    .child_by_field_name("left")
                    .filter(|l| l.kind() == "variable_name");
                let right = n.child_by_field_name("right");
                if let (Some(l), Some(r)) = (left, right) {
                    let appends_var = self.operator(n) == ".="
                        && self.literal(r).is_none()
                        && matches!(
                            r.kind(),
                            "variable_name" | "subscript_expression" | "member_access_expression"
                        );
                    if appends_var || self.splices_variables(r) {
                        spliced.insert((scope, self.get_node_text(l)));
                    }
                }
            }
            Some(scope)
        });

        // Pass 2: the risky calls themselves.
        let mut notes: Vec<SecurityNote> = Vec::new();
        let mut seen: HashSet<(usize, &str)> = HashSet::new();
        let mut note = |line: usize, note_type: &'static str, description: String| {
            if seen.insert((line, note_type)) {
                notes.push(SecurityNote {
                    note_type: note_type.to_string(),
                    line,
                    description,
                });
            }
        };
        walk(root, 0usize, |n, scope| {
            let scope = scope_of(n, scope);
            let line = n.start_position().row + 1;
            match n.kind() {
                "shell_command_expression" => note(
                    line,
                    "command_execution",
                    "Backtick shell execution".to_string(),
                ),
                "function_call_expression" => {
                    let Some(func) = n.child_by_field_name("function") else {
                        return Some(scope);
                    };
                    let name = last_segment(self.get_node_text(func)).to_ascii_lowercase();
                    if name == "eval" {
                        note(
                            line,
                            "code_execution",
                            "Uses eval() - potential security risk".to_string(),
                        );
                    } else if COMMAND_EXEC_FNS.contains(&name.as_str()) {
                        note(
                            line,
                            "command_execution",
                            format!("System command execution ({}())", name),
                        );
                    } else if name == "unserialize" {
                        note(
                            line,
                            "deserialization",
                            "Uses unserialize() - potential object injection".to_string(),
                        );
                    } else if let Some(&(f, idx)) = SQL_QUERY_FNS.iter().find(|(f, _)| *f == name) {
                        let spliced_sql = self.argument_nodes(n).get(idx).is_some_and(|a| {
                            self.splices_variables(*a)
                                || (a.kind() == "variable_name"
                                    && spliced.contains(&(scope, self.get_node_text(*a))))
                        });
                        if spliced_sql {
                            note(
                                line,
                                "sql_injection",
                                format!("SQL built from variables passed to {}() - check for SQL injection", f),
                            );
                        }
                    }
                }
                _ => {}
            }
            Some(scope)
        });
        notes
    }

    // Tags

    fn auto_tag_function(&self, name: &str, docstring: &str, mods: &Modifiers) -> Vec<String> {
        let mut tags: Vec<String> = Vec::new();
        let lower = name.to_lowercase();
        let doc = docstring.to_lowercase();

        if matches!(lower.as_str(), "main" | "run" | "start") {
            tags.push("entry-point".to_string());
        }
        for rule in TAG_RULES.iter() {
            let name_matches = rule.keywords.iter().any(|kw| lower.contains(kw));
            let doc_matches = rule.check_docstring && doc.contains(rule.keywords[0]);
            if name_matches || doc_matches {
                tags.push(rule.tag.to_string());
            }
        }
        if doc.contains("authentication") {
            tags.push("authentication".to_string());
        }
        if tags.iter().any(|t| t == "authentication") {
            tags.push("security".to_string());
        }
        if lower.ends_with("action") || lower.contains("controller") || doc.contains("@route") {
            tags.push("http-handler".to_string());
        }
        if lower.starts_with("test") {
            tags.push("testing".to_string());
        }
        if let Some(magic) = lower.strip_prefix("__") {
            tags.push("magic-method".to_string());
            match magic {
                "construct" => tags.push("constructor".to_string()),
                "destruct" => tags.push("destructor".to_string()),
                "tostring" => tags.push("string-representation".to_string()),
                "get" | "set" | "isset" | "unset" => tags.push("property-access".to_string()),
                "call" | "callstatic" | "invoke" => tags.push("callable".to_string()),
                _ => {}
            }
        }
        match mods.access_modifier.as_deref() {
            Some("private") => tags.push("private".to_string()),
            Some("protected") => tags.push("protected".to_string()),
            _ => {}
        }
        if mods.is_static {
            tags.push("static-method".to_string());
        }
        if mods.is_abstract {
            tags.push("abstract".to_string());
        }

        tags.sort();
        tags.dedup();
        tags
    }

    fn estimate_importance(&self, name: &str, mods: &Modifiers, decorators: &[String]) -> f32 {
        let lower = name.to_lowercase();
        let mut score: f32 = 0.5;
        if matches!(lower.as_str(), "main" | "run" | "start") {
            score += 0.3;
        }
        // `#[Route(...)]`-style attributes, matched like Python's decorators.
        if decorators.iter().any(|d| {
            let d = d.to_lowercase();
            d.contains("route") || d.contains("api") || d.contains("endpoint")
        }) {
            score += 0.2;
        }
        if lower.contains("auth") || lower.contains("login") {
            score += 0.2;
        }
        if mods.access_modifier.as_deref() == Some("private") {
            score -= 0.2;
        }
        score.clamp(0.0, 1.0)
    }
}

/// Whether `src` opens PHP code: `<?=`, or `<?php` or a short `<?` followed
/// by whitespace (space, tab, CR or LF, as PHP reads it) or the end of the
/// text; `<?xml` is markup. C, C++ and assembler projects name files `.inc`
/// too, and this tells the PHP ones apart.
pub fn has_open_tag(src: &str) -> bool {
    let ends_tag = |rest: &[u8]| {
        rest.first()
            .is_none_or(|c| matches!(c, b' ' | b'\t' | b'\r' | b'\n'))
    };
    src.match_indices("<?").any(|(i, _)| {
        let rest = &src.as_bytes()[i + 2..];
        rest.first() == Some(&b'=')
            || ends_tag(rest)
            || rest.len() >= 3 && rest[..3].eq_ignore_ascii_case(b"php") && ends_tag(&rest[3..])
    })
}

/// Parses PHP source already in memory; `path` names the file in the knowledge base.
pub fn parse_source(path: &Path, source_code: &str) -> Result<(String, FileData), String> {
    // Strips leading "./" or ".\" if present, otherwise leaves path as is
    let clean_path = path.strip_prefix("./").unwrap_or(path);
    let path_str = clean_path.to_string_lossy().to_string();

    let mut parser = PhpParser::new(source_code.to_string(), path_str.clone());
    let file_data = parser.parse()?;

    Ok((path_str, file_data))
}

/// Reads and parses a PHP file.
pub fn parse_file(path: &Path) -> Result<(String, FileData), String> {
    let source_code = std::fs::read_to_string(path)
        .map_err(|e| format!("Failed to read file {}: {}", path.display(), e))?;
    parse_source(path, &source_code)
}

/// PHP sources shared by the parser tests here and the resolution tests in
/// `analyze::call_graph::prism_v2::php`.
#[cfg(test)]
pub(crate) mod fixtures {
    pub(crate) const HELPERS: &str = r#"<?php
function format_price($value) {
    return number_format($value, 2);
}

function cart_total($items) {
    $sum = 0;
    foreach ($items as $item) {
        $sum += $item;
    }
    return format_price($sum);
}
"#;
    pub(crate) const CART_PAGE: &str = r#"<?php
require_once dirname(__FILE__) . '/../lib/helpers.php';
include 'partials/header.php';
$items = array(10, 20);
?>
<p>Total: <?php echo cart_total($items); ?></p>
"#;
    pub(crate) const MODELS: &str = r#"<?php
class Model {
    protected $table;

    public function __construct($table) {
        $this->table = $table;
    }

    public static function find($id) {
        return new static($id);
    }

    public function save() {
        return true;
    }
}

class Invoice extends Model {
    const TAX = 0.2;
    private static $count = 0;

    /**
     * Creates an invoice.
     *
     * @param int $id
     */
    public function __construct($id) {
        parent::__construct('invoices');
        self::bump();
        $this->save();
    }

    private static function bump() {
        self::$count++;
    }

    final public function total() {
        return static::find(1);
    }
}
"#;
    pub(crate) const SERVICE: &str = r#"<?php
namespace App\Billing;

use App\Models\Invoice;
use App\Support\Formatter as Fmt;
use function App\Support\money;

class InvoiceService {
    public function render(Fmt $fmt) {
        $invoice = new Invoice();
        $invoice->total();
        Invoice::find(1);
        return money($fmt->format($invoice));
    }
}
"#;
}

#[cfg(test)]
mod tests {
    #![expect(clippy::expect_used)]
    #![expect(clippy::unwrap_used)]

    use super::fixtures::*;
    use super::*;

    fn parse_at(path: &str, src: &str) -> FileData {
        parse_source(Path::new(path), src)
            .expect("parse should succeed")
            .1
    }

    fn parse(src: &str) -> FileData {
        parse_at("test.php", src)
    }

    fn find_fn<'a>(fd: &'a FileData, name: &str) -> &'a Function {
        #[allow(clippy::panic)]
        fd.functions
            .iter()
            .find(|f| f.name == name)
            .unwrap_or_else(|| panic!("function `{}` not found", name))
    }

    fn find_class<'a>(fd: &'a FileData, name: &str) -> &'a Class {
        #[allow(clippy::panic)]
        fd.classes
            .iter()
            .find(|c| c.name == name)
            .unwrap_or_else(|| panic!("class `{}` not found", name))
    }

    fn find_method<'a>(fd: &'a FileData, class: &str, name: &str) -> &'a Function {
        #[allow(clippy::panic)]
        find_class(fd, class)
            .methods
            .iter()
            .find(|m| m.name == name)
            .unwrap_or_else(|| panic!("method `{}::{}` not found", class, name))
    }

    fn callees(f: &Function) -> Vec<&str> {
        f.calls.iter().map(|c| c.callee.as_str()).collect()
    }

    fn php(f: &Function) -> &PhpInfo {
        f.lang_info.php.as_deref().expect("php info")
    }

    #[test]
    fn procedural_include_is_recorded_with_its_literal_path() {
        let fd = parse_at("pages/cart.php", CART_PAGE);
        let modules: Vec<(&str, &str)> = fd
            .imports
            .iter()
            .map(|i| (i.module.as_str(), i.import_type.as_str()))
            .collect();
        assert_eq!(
            modules,
            vec![
                ("../lib/helpers.php", "include"),
                ("partials/header.php", "include")
            ]
        );
    }

    #[test]
    fn top_level_code_becomes_a_main_function() {
        let fd = parse_at("pages/cart.php", CART_PAGE);
        let main = find_fn(&fd, MAIN_FUNCTION);
        assert_eq!(main.id, "func_{main}::pages/cart.php");
        assert!(callees(main).contains(&"cart_total"), "{:?}", callees(main));
        assert!(php(main).is_script);
        assert_eq!(main.tags, vec!["script"]);

        // A pure function library has no top-level calls and no `{main}`.
        let lib = parse_at("lib/helpers.php", HELPERS);
        assert!(lib.functions.iter().all(|f| f.name != MAIN_FUNCTION));
    }

    #[test]
    fn dynamic_include_keeps_the_expression() {
        let page = r#"<?php
require_once $_SERVER['DOCUMENT_ROOT'] . '/app/lib/db.php';
include $module . '.php';
db_connect();
"#;
        let fd = parse_at("web/index.php", page);
        let dynamic: Vec<&str> = fd
            .imports
            .iter()
            .filter(|i| i.import_type == "include_dynamic")
            .map(|i| i.module.as_str())
            .collect();
        assert_eq!(
            dynamic,
            vec![
                "$_SERVER['DOCUMENT_ROOT'] . '/app/lib/db.php'",
                "$module . '.php'"
            ]
        );
    }

    #[test]
    fn class_methods_carry_visibility_static_and_docblock() {
        let fd = parse(MODELS);
        let invoice = find_class(&fd, "Invoice");
        assert_eq!(invoice.bases, vec!["\\Model"]);
        assert_eq!(invoice.id, "class_Invoice::test.php");
        let info = invoice.lang_info.php.as_deref().unwrap();
        assert_eq!(info.type_kind, Some(PhpTypeKind::Class));
        assert_eq!(info.extends, vec!["\\Model"]);

        let attrs: Vec<(&str, &str)> = invoice
            .attributes
            .iter()
            .map(|a| (a.name.as_str(), a.type_annotation.as_str()))
            .collect();
        assert_eq!(attrs, vec![("TAX", "const"), ("$count", "")]);

        let ctor = find_method(&fd, "Invoice", "__construct");
        assert_eq!(ctor.id, "method_Invoice___construct::test.php");
        assert_eq!(ctor.docstring, "Creates an invoice.\n\n@param int $id");
        assert!(ctor.tags.contains(&"constructor".to_string()));
        assert_eq!(
            callees(ctor),
            vec!["parent::__construct", "self::bump", "this.save"]
        );

        let bump = find_method(&fd, "Invoice", "bump");
        assert_eq!(bump.signature, "private static function bump()");
        assert_eq!(php(bump).access_modifier.as_deref(), Some("private"));
        assert!(php(bump).is_static);
        assert!(php(find_method(&fd, "Invoice", "total")).is_final);

        let find = find_method(&fd, "Model", "find");
        assert_eq!(callees(find), vec!["new \\Model"]);
    }

    #[test]
    fn interfaces_traits_and_abstract_methods_are_extracted() {
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
        let fd = parse_at("order.php", src);
        let payable = find_class(&fd, "Payable");
        assert_eq!(payable.bases, vec!["\\Countable", "\\Billing\\Chargeable"]);
        assert_eq!(
            payable.lang_info.php.as_deref().unwrap().type_kind,
            Some(PhpTypeKind::Interface)
        );
        let order = find_class(&fd, "Order");
        assert_eq!(order.bases, vec!["\\Payable", "\\Loggable"]);
        let info = order.lang_info.php.as_deref().unwrap();
        assert!(info.is_abstract);
        assert_eq!(info.implements, vec!["\\Payable"]);
        assert_eq!(info.traits, vec!["\\Loggable"]);
        assert!(php(find_method(&fd, "Order", "amount")).is_abstract);
    }

    #[test]
    fn use_imports_are_classified_and_aliases_undone() {
        let fd = parse_at("src/Billing/InvoiceService.php", SERVICE);
        let imports: Vec<(&str, Vec<&str>, &str)> = fd
            .imports
            .iter()
            .map(|i| {
                (
                    i.module.as_str(),
                    i.items.iter().map(String::as_str).collect(),
                    i.import_type.as_str(),
                )
            })
            .collect();
        assert_eq!(
            imports,
            vec![
                ("App\\Models\\Invoice", vec!["Invoice"], "internal"),
                ("App\\Support\\Formatter", vec!["Formatter"], "internal"),
                ("App\\Support", vec!["money"], "internal"),
            ]
        );

        let render = find_method(&fd, "InvoiceService", "render");
        assert_eq!(php(render).namespace.as_deref(), Some("App\\Billing"));
        // The alias is undone and both receivers get their class.
        assert_eq!(
            callees(render),
            vec![
                "new \\App\\Models\\Invoice",
                "\\App\\Models\\Invoice.total",
                "\\App\\Models\\Invoice::find",
                "\\App\\Support\\money",
                "\\App\\Support\\Formatter.format"
            ]
        );
    }

    #[test]
    fn inline_html_template_calls_carry_branch_and_loop_context() {
        let src = r#"<!DOCTYPE html>
<html>
<head><title><?= page_title() ?></title></head>
<body>
<?php if ($user): ?>
  <p>Hello, <?php echo esc_html($user['name']); ?></p>
<?php else: ?>
  <a href="login.php">Login // not a comment</a>
<?php endif; ?>
<ul>
<?php foreach (load_items() as $item) { ?>
  <li><?php render_item($item); ?></li>
<?php } ?>
</ul>
<?php
// TODO: paginate the list
# FIXME: urgent, escape the footer
function render_item($item) {
    echo '<b>' . $item . '</b>';
}
?>
</body>
</html>
"#;
        let fd = parse_at("views/list.phtml", src);
        assert!(fd.syntax.is_clean());
        assert!(fd.imports.is_empty());
        assert_eq!(fd.loc, 23);

        let main = find_fn(&fd, MAIN_FUNCTION);
        let calls: Vec<(&str, &str)> = main
            .calls
            .iter()
            .map(|c| (c.callee.as_str(), c.context.as_str()))
            .collect();
        assert_eq!(
            calls,
            vec![
                ("page_title", "unconditional"),
                ("esc_html", "if"),
                ("load_items", "loop"),
                ("render_item", "loop"),
            ]
        );
        assert!(main.calls[1].is_conditional);
        assert_eq!(find_fn(&fd, "render_item").line_start, 18);

        let todos: Vec<(usize, &str, &str)> = fd
            .todos
            .iter()
            .map(|t| (t.line, t.text.as_str(), t.priority.as_str()))
            .collect();
        assert_eq!(
            todos,
            vec![
                (16, "paginate the list", "medium"),
                (17, "urgent, escape the footer", "high"),
            ]
        );
    }

    #[test]
    fn legacy_php54_procedural_code_is_fully_extracted() {
        let src = r#"<?php
define('APP_VERSION', '1.0');
$config = array('debug' => false);

/**
 * Legacy helper.
 */
function build_query($table, $where = array(), $limit = null) {
    global $db, $config;
    $sql = "SELECT * FROM " . $table;
    if (count($where) > 0) {
        $sql .= " WHERE " . implode(' AND ', $where);
    }
    if ($limit) {
        $sql .= " LIMIT " . $limit;
    }
    $result = mysql_query($sql, $db) or die(mysql_error());
    $rows = array();
    while ($row = mysql_fetch_assoc($result)) {
        $rows[] = $row;
    }
    return $rows;
}

class Legacy_Report {
    var $title;

    function Legacy_Report($title) {
        $this->title = $title;
    }

    function render() {
        return build_query('reports');
    }
}
"#;
        let fd = parse(src);
        assert!(fd.syntax.is_clean());

        let f = find_fn(&fd, "build_query");
        assert_eq!(f.docstring, "Legacy helper.");
        assert_eq!(
            f.signature,
            "function build_query($table, $where = array(), $limit = null)"
        );
        let params: Vec<(&str, &str, Option<&str>)> = f
            .params
            .iter()
            .map(|p| {
                (
                    p.name.as_str(),
                    p.type_annotation.as_str(),
                    p.default_value.as_deref(),
                )
            })
            .collect();
        assert_eq!(
            params,
            vec![
                ("$table", "", None),
                ("$where", "", Some("array()")),
                ("$limit", "", Some("null")),
            ]
        );
        // if, if, while, `or`
        assert_eq!(f.complexity, 5);
        assert_eq!(f.control_flow.branches.len(), 2);
        assert_eq!(f.control_flow.loops.len(), 1);
        let db = f.variables.iter().find(|v| v.name == "$db").unwrap();
        assert_eq!(db.scope, "global");
        assert!(f.variables.iter().any(|v| v.name == "$rows" && v.returned));
        assert!(callees(f).contains(&"mysql_fetch_assoc"));
        assert!(!callees(f).contains(&"die"));

        assert!(fd
            .security_notes
            .iter()
            .any(|n| n.note_type == "sql_injection" && n.line == 17));

        let globals: Vec<(&str, &str)> = fd
            .global_vars
            .iter()
            .map(|g| (g.name.as_str(), g.type_annotation.as_str()))
            .collect();
        assert_eq!(
            globals,
            vec![
                ("APP_VERSION", "constant"),
                ("$config", ""),
                ("$db", "global"),
            ]
        );

        // PHP 4 style: `var` property and a constructor named after the class.
        let report = find_class(&fd, "Legacy_Report");
        assert_eq!(report.attributes[0].name, "$title");
        assert_eq!(
            find_method(&fd, "Legacy_Report", "Legacy_Report").id,
            "method_Legacy_Report_Legacy_Report::test.php"
        );
        assert_eq!(
            callees(find_method(&fd, "Legacy_Report", "render")),
            vec!["build_query"]
        );
    }

    #[test]
    fn risky_calls_produce_security_notes() {
        let src = r#"<?php
eval($code);
$out = `ls -la`;
shell_exec("ls " . $dir);
$data = unserialize($_COOKIE['cart']);
mysqli_query($link, "DELETE FROM t WHERE id = {$_GET['id']}");
mysql_query("SELECT * FROM t WHERE id = " . intval($id));
mysql_query('SELECT 1');
"#;
        let fd = parse(src);
        let notes: Vec<(usize, &str)> = fd
            .security_notes
            .iter()
            .map(|n| (n.line, n.note_type.as_str()))
            .collect();
        assert_eq!(
            notes,
            vec![
                (2, "code_execution"),
                (3, "command_execution"),
                (4, "command_execution"),
                (5, "deserialization"),
                (6, "sql_injection"),
            ]
        );
    }

    #[test]
    fn global_declarations_constants_and_object_variables() {
        let src = r#"<?php
const LIMIT = 10;
$db = new Database();

function fetch_user($id) {
    global $db;
    return $db->query($id);
}
"#;
        let fd = parse(src);
        let globals: Vec<(&str, &str)> = fd
            .global_vars
            .iter()
            .map(|g| (g.name.as_str(), g.type_annotation.as_str()))
            .collect();
        assert_eq!(globals, vec![("LIMIT", "const"), ("$db", "\\Database")]);
        // `global $db` brings the top-level object's class along.
        assert_eq!(
            callees(find_fn(&fd, "fetch_user")),
            vec!["\\Database.query"]
        );
    }

    #[test]
    fn call_sites_are_written_in_the_shapes_prism_resolves() {
        let src = r#"<?php
function shapes($handler, Mailer $mailer) {
    try {
        $handler();
        $mailer->send();
        $this->repo->save();
        foo()->bar();
        (new Builder)->build();
        $cls = 'Report';
        new $cls();
    } catch (\RuntimeException $e) {
        report($e);
        throw new LogicException('wrapped');
    }
}
"#;
        let fd = parse(src);
        let f = find_fn(&fd, "shapes");
        let calls: Vec<(&str, &str)> = f
            .calls
            .iter()
            .map(|c| (c.callee.as_str(), c.context.as_str()))
            .collect();
        assert_eq!(
            calls,
            vec![
                ("$handler", "try"),
                ("\\Mailer.send", "try"),
                ("this.repo.save", "try"),
                ("foo().bar", "try"),
                ("foo", "try"),
                ("\\Builder.build", "try"),
                ("new \\Builder", "try"),
                ("new $cls", "try"),
                ("report", "catch"),
                ("new \\LogicException", "catch"),
            ]
        );
        assert_eq!(f.exceptions.handles, vec!["RuntimeException"]);
        assert_eq!(f.exceptions.raises, vec!["LogicException"]);
        assert_eq!(f.control_flow.try_blocks.len(), 1);
    }

    #[test]
    fn functions_declared_conditionally_or_in_namespace_blocks() {
        let src = r#"<?php
namespace Shop {
    function inner() { return 1; }
}
namespace {
    if (!function_exists('legacy')) {
        function legacy() { return 2; }
    }
}
"#;
        let fd = parse(src);
        assert_eq!(
            php(find_fn(&fd, "inner")).namespace.as_deref(),
            Some("Shop")
        );
        assert_eq!(php(find_fn(&fd, "legacy")).namespace, None);
    }

    #[test]
    fn deep_concatenation_does_not_overflow_the_stack() {
        let mut src = String::from("<?php\nfunction big() {\n    $html = 'a'");
        for _ in 0..20_000 {
            src.push_str(" . 'a'");
        }
        src.push_str(" . render();\n}\n");
        let fd = std::thread::Builder::new()
            .stack_size(256 * 1024)
            .spawn(move || parse(&src))
            .unwrap()
            .join()
            .expect("parsing must not overflow a small stack");
        assert_eq!(callees(find_fn(&fd, "big")), vec!["render"]);
    }

    #[test]
    fn absurd_dirname_levels_do_not_allocate_or_overflow() {
        for src in [
            "<?php\ninclude dirname(__DIR__, 99999999999) . '/x.php';\n",
            "<?php\ninclude dirname(__DIR__, 18446744073709551615) . '/x.php';\n",
            "<?php\ninclude dirname(dirname(__DIR__, 18446744073709551615)) . '/x.php';\n",
        ] {
            let fd = parse(src);
            assert_eq!(fd.imports.len(), 1);
            assert_eq!(fd.imports[0].import_type, "include_dynamic", "{}", src);
        }
    }

    #[test]
    fn deep_receivers_and_anchors_do_not_overflow_the_stack() {
        let mut parens = String::from("<?php\nfunction p($x) {\n");
        parens.push_str(&"(".repeat(20_000));
        parens.push_str("$x");
        parens.push_str(&")".repeat(20_000));
        parens.push_str("->m();\n}\n");
        let mut chain = String::from("<?php\nfunction s() {\nA::b()");
        chain.push_str(&"::c()".repeat(20_000));
        chain.push_str(";\n}\n");
        let mut dirs = String::from("<?php\ninclude ");
        dirs.push_str(&"dirname(".repeat(20_000));
        dirs.push_str("__FILE__");
        dirs.push_str(&")".repeat(20_000));
        dirs.push_str(" . '/x.php';\n");
        for src in [parens, chain, dirs] {
            std::thread::Builder::new()
                .stack_size(256 * 1024)
                .spawn(move || parse(&src))
                .unwrap()
                .join()
                .expect("parsing must not overflow a small stack");
        }
    }

    #[test]
    fn relative_names_are_calls_instantiations_and_bases() {
        let fd = parse(
            r#"<?php
namespace App;
function f() {
    namespace\helper();
    $o = new namespace\Foo();
    namespace\Foo::bar();
}
class C extends namespace\Base implements namespace\I {}
"#,
        );
        assert_eq!(
            callees(find_fn(&fd, "f")),
            vec!["\\App\\helper", "new \\App\\Foo", "\\App\\Foo::bar"]
        );
        assert_eq!(find_class(&fd, "C").bases, vec!["\\App\\Base", "\\App\\I"]);
    }

    #[test]
    fn functions_declared_inside_methods_are_extracted() {
        let fd = parse(
            r#"<?php
class Outer {
    public function m() {
        if (!function_exists('in_method')) {
            function in_method() { nested(); }
        }
    }
}
"#,
        );
        assert_eq!(callees(find_fn(&fd, "in_method")), vec!["nested"]);
        assert_eq!(php(find_fn(&fd, "in_method")).access_modifier, None);
    }

    #[test]
    fn anonymous_class_methods_belong_to_the_enclosing_code() {
        let fd = parse(
            "<?php\nfunction outer() {\n    return new class extends Base { public function m() { inner(); } };\n}\n",
        );
        assert!(callees(find_fn(&fd, "outer")).contains(&"inner"));
    }

    #[test]
    fn fully_qualified_or_lowercase_anchors_are_static() {
        let fd = parse_at(
            "public/index.php",
            "<?php\nrequire \\dirname(__DIR__) . '/bootstrap.php';\nrequire __dir__ . '/local.php';\n",
        );
        let modules: Vec<(&str, &str)> = fd
            .imports
            .iter()
            .map(|i| (i.module.as_str(), i.import_type.as_str()))
            .collect();
        assert_eq!(
            modules,
            vec![("../bootstrap.php", "include"), ("local.php", "include")]
        );
    }

    #[test]
    fn use_function_aliases_are_undone() {
        let fd = parse("<?php\nuse function App\\Str\\slug as mkslug;\nmkslug('x');\n");
        assert_eq!(
            callees(find_fn(&fd, MAIN_FUNCTION)),
            vec!["\\App\\Str\\slug"]
        );
    }

    #[test]
    fn else_if_false_path_matches_elseif() {
        let elseif = parse("<?php\nfunction f($x) {\n if ($x == 1) { a(); } elseif ($x == 2) { b(); } elseif ($x == 3) { c(); }\n}\n");
        let else_if = parse("<?php\nfunction f($x) {\n if ($x == 1) { a(); } else if ($x == 2) { b(); } else if ($x == 3) { c(); }\n}\n");
        let first = |fd: &FileData| {
            find_fn(fd, "f").control_flow.branches[0]
                .false_path
                .as_ref()
                .map(|p| p.calls.clone())
        };
        assert_eq!(first(&elseif), Some(vec!["b".to_string()]));
        assert_eq!(first(&else_if), first(&elseif));
    }

    #[test]
    fn closure_bodies_are_not_spliced_into_the_assignment() {
        let fd = parse("<?php\nfunction f($id) {\n $q = function() use ($id) { return \"SELECT $id\"; };\n mysql_query($q);\n}\n");
        assert!(fd
            .security_notes
            .iter()
            .all(|n| n.note_type != "sql_injection"));
    }

    #[test]
    fn no_op_stubs_and_guarded_functions_are_tagged_fallback() {
        let noop = "<?php\nfunction __($text) {}\nif (!function_exists('esc')) {\n    function esc($s) { return $s; }\n}\n";
        let fd = parse_at("admin/includes/noop.php", noop);
        assert!(find_fn(&fd, "esc").tags.contains(&FALLBACK_TAG.to_string()));
        assert!(find_fn(&fd, "__").tags.contains(&FALLBACK_TAG.to_string()));
    }

    #[test]
    fn receivers_are_typed_from_docblocks_closures_and_own_methods() {
        let src = r#"<?php
class User { public function save() {} }
class Repo {
    /** @var User */
    protected $cached;
    protected $files;
    private User|null $owner = null;

    public function __construct(User $files) { $this->files = $files; }

    public function current(): User { return new User; }
    public function fluent(): static { return $this; }

    /**
     * @param User $u
     * @global User $current_user
     */
    public function run($u) {
        global $current_user;
        array_map(fn (User $x) => $x->save(), []);
        $this->cached->save();
        $this->files->save();
        $this->owner?->save();
        $this->current()->save();
        $u->save();
        $current_user->save();
        $me = $this->fluent();
        $me->run(1);
    }
}
"#;
        let fd = parse(src);
        let run = find_method(&fd, "Repo", "run");
        let mut typed: Vec<&str> = callees(run)
            .into_iter()
            .filter(|c| c.starts_with('\\'))
            .collect();
        typed.dedup();
        assert_eq!(
            typed,
            vec!["\\User.save", "\\Repo.run"],
            "{:?}",
            callees(run)
        );
        let saves = run.calls.iter().filter(|c| c.callee == "\\User.save");
        // One record per line: the closure, @var, the constructor, `|null`,
        // the return type, @param and @global.
        assert_eq!(saves.count(), 7);
    }

    #[test]
    fn template_types_map_to_their_bound() {
        let src = r#"<?php
class Model { public function save() {} }
class Repo {
    /**
     * @template T of Model
     * @param T $model
     * @template U
     * @param U $other
     */
    public function store($model, $other) {
        $model->save();
        $other->save();
    }
}
"#;
        let fd = parse(src);
        assert_eq!(
            callees(find_method(&fd, "Repo", "store")),
            vec!["\\Model.save", "$other.save"]
        );
    }

    #[test]
    fn this_inside_an_anonymous_class_is_its_parent() {
        let src = r#"<?php
function migration() {
    return new class extends Migration {
        public function up() { $this->schema(); }
    };
}
"#;
        let fd = parse(src);
        assert!(callees(find_fn(&fd, "migration")).contains(&"\\Migration.schema"));
    }

    #[test]
    fn relative_scopes_are_not_qualified_and_parent_in_anonymous_class_is_its_base() {
        let src = r#"<?php
namespace App;
class Registry {
    protected static $instance;
    public function get() {
        self::$instance->load();
        return new class extends Base {
            public function up() { parent::up(); }
        };
    }
}
"#;
        let fd = parse(src);
        assert_eq!(
            callees(find_method(&fd, "Registry", "get")),
            vec!["self::$instance.load", "\\App\\Base::up"]
        );
    }

    #[test]
    fn multibyte_class_names_do_not_panic() {
        // `name[..10]` used to cut inside the `ç` of `Configuração`.
        let fd = parse("<?php\nfunction f(Movimentação $m) {\n    $c = new Configuração();\n    Autenticação::login();\n}\nclass Pedido extends Integração {}\n");
        assert_eq!(
            callees(find_fn(&fd, "f")),
            vec!["new \\Configuração", "\\Autenticação::login"]
        );
        assert_eq!(find_class(&fd, "Pedido").bases, vec!["\\Integração"]);
    }

    #[test]
    fn use_imports_belong_to_their_own_namespace() {
        let fd = parse(
            r#"<?php
namespace A;
use X\Logger;
function fa() { return new Logger(); }
namespace B;
use Y\Logger;
function fb() { return new Logger(); }
namespace C;
function fc() { return new Logger(); }
"#,
        );
        let callee = |name: &str| {
            fd.functions
                .iter()
                .find(|f| f.name == name)
                .map(|f| f.calls[0].callee.clone())
        };
        assert_eq!(callee("fa").as_deref(), Some("new \\X\\Logger"));
        assert_eq!(callee("fb").as_deref(), Some("new \\Y\\Logger"));
        assert_eq!(callee("fc").as_deref(), Some("new \\C\\Logger"));
    }

    #[test]
    fn function_exists_guards_tag_only_the_functions_they_name() {
        let fd = parse(
            r#"<?php
if ($x) {} elseif (!function_exists('in_elseif')) { function in_elseif() { a(); b(); } }
if (!function_exists('guarded')) { function guarded() { a(); b(); } } else { function other() { a(); b(); } }
if (function_exists('mb_strlen')) { function uses_mb() { a(); b(); } }
if (!$r->function_exists_here('x')) { function lookalike() { a(); b(); } }
"#,
        );
        let fallback = |name: &str| find_fn(&fd, name).tags.iter().any(|t| t == FALLBACK_TAG);
        assert!(fallback("in_elseif"));
        assert!(fallback("guarded"));
        assert!(!fallback("other"));
        assert!(!fallback("uses_mb"));
        assert!(!fallback("lookalike"));
    }

    #[test]
    fn static_property_receivers_keep_self_and_static() {
        let fd = parse(
            "<?php\nnamespace App;\nclass R {\n    static $i;\n    function go() { self::$i->a(); static::$i->b(); }\n}\n",
        );
        assert_eq!(
            callees(find_method(&fd, "R", "go")),
            vec!["self::$i.a", "static::$i.b"]
        );
    }

    #[test]
    fn top_level_closure_parameters_use_the_file_imports() {
        // Symfony's `config/services.php` shape: the closure is `{main}` code.
        let fd = parse(
            "<?php\nnamespace App\\Config;\nuse Lib\\Loader;\nreturn function (Configurator $c, Loader $l) {\n    $c->services();\n    $l->load();\n};\n",
        );
        assert_eq!(
            callees(find_fn(&fd, MAIN_FUNCTION)),
            vec!["\\App\\Config\\Configurator.services", "\\Lib\\Loader.load"]
        );
    }

    #[test]
    fn open_tags_tell_php_inc_files_from_c_ones() {
        assert!(has_open_tag("<?php\nfunction f() {}\n"));
        assert!(has_open_tag("<html>\n<?PHP echo 1; ?>"));
        assert!(has_open_tag("<p><?= $title ?></p>"));
        assert!(has_open_tag("<? echo 1; ?>"));
        assert!(has_open_tag("<?php"));
        assert!(!has_open_tag("#include <stdio.h>\n#define N 4\n"));
        assert!(!has_open_tag("<?xml version=\"1.0\"?>\n<root/>"));
        assert!(!has_open_tag("<?phpx"));
        assert!(!has_open_tag("<?\x0bphp"));
        // The library refuses a `.inc` without PHP code instead of reading it as PHP.
        let c = crate::grammar_files::parse_source(Path::new("table.inc"), "#include <x.h>\n");
        assert!(c.is_err());
        let php =
            crate::grammar_files::parse_source(Path::new("lib.inc"), "<?php\nfunction f() {}\n");
        assert_eq!(php.expect("PHP .inc parses").1.language, "php");
    }

    #[test]
    fn shared_fixtures_parse_without_syntax_errors() {
        for (name, src) in [
            ("HELPERS", HELPERS),
            ("CART_PAGE", CART_PAGE),
            ("MODELS", MODELS),
            ("SERVICE", SERVICE),
        ] {
            assert!(parse(src).syntax.is_clean(), "{name} has syntax errors");
        }
    }

    #[test]
    fn legacy_brace_string_offsets_parse_like_brackets() {
        let src = r#"<?php
function check($cpf, $s) {
    for ($i = 0; $i < 9; $i++) { $soma += $cpf{$i} * 2; }
    if ($s{0} == '=' && $this->formula{0} != '+') { done(); }
    $label = "{$s} and ${s}";
    return helper($s{strlen($s) - 1}, $s['k']{0});
}
"#;
        let fd = parse(src);
        assert!(fd.syntax.is_clean(), "{:?}", fd.syntax);
        let check = find_fn(&fd, "check");
        for callee in ["done", "strlen", "helper"] {
            assert!(callees(check).contains(&callee), "{:?}", callees(check));
        }
        // Text still comes from the source as written.
        let cond = &check.control_flow.branches[0].condition;
        assert!(cond.contains("$s{0}"), "{cond}");
        // Code that already parses is not rewritten.
        let tree = |src: &str| {
            let mut p = Parser::new();
            p.set_language(&tree_sitter_php::LANGUAGE_PHP.into())
                .expect("grammar loads");
            p.parse(src, None).expect("parses")
        };
        let valid =
            "<?php\nif ($x){ $a = [1]; } $f = function () use ($a){ return \"{$a[0]}\"; };\n";
        assert_eq!(
            legacy_offsets_as_brackets(valid, tree(valid).root_node()),
            None
        );
    }
}
