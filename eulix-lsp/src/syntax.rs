// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
//! Syntax: the trust boundary.
//!
//! Every `libeulix` type in this server is named in this file and nowhere
//! else. Downstream modules work against the plain structs below, so an
//! upstream rename lands here as a compile error instead of rippling through
//! the whole server.
//!
//! Two facts about libeulix 0.8.1 shape this adapter:
//!
//! 1. `parse_source` leaves `FunctionCall::defined_in` as `None` for almost
//!    every language (Go puts a qualifier like `"fmt"` in it, Java an `object`
//!    reference). Only the batch `prism_v1::resolve_call_locations` turns it
//!    into a file path, and that needs a whole `KnowledgeBase` and walks
//!    every function, so it's unusable at keystroke frequency. We treat call
//!    sites as unresolved and let [`crate::index`] resolve them
//!    incrementally. The qualifier is carried across as `CallSite::qualifier`
//!    because it's genuinely useful for ranking.
//! 2. `FileData::syntax` (`SyntaxHealth`) reports only counts plus a single
//!    `first_error_line`. There's no per-error span, so syntax diagnostics
//!    are line-granular. See [`crate::diagnostics`].

// The model below is a faithful projection of libeulix's schema rather than
// just the subset this server reads today. Keeping the whole shape means a
// future feature (inlay hints, signature help, type hover) doesn't require
// coming back to the trust boundary, and unused fields are the visible cost
// of that choice.
#![allow(dead_code)]

use std::path::Path;

use libeulix::grammar_files::language::Language;
use libeulix::grammar_files::parse_source as lib_parse_source;
use libeulix::struc::kb_struct::FileData;

// Neutral model

// What the tree-sitter pass saw, reduced to what an editor can act on.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SyntaxReport {
    pub error_nodes: usize,
    pub missing_nodes: usize,
    // 1-based, matching libeulix's convention everywhere.
    pub first_error_line: Option<usize>,
}

impl SyntaxReport {
    pub fn is_clean(&self) -> bool {
        self.error_nodes == 0 && self.missing_nodes == 0
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum DefKind {
    Function,
    Method,
    Class,
}

impl DefKind {
    pub fn is_callable(self) -> bool {
        matches!(self, DefKind::Function | DefKind::Method)
    }
}

#[derive(Debug, Clone)]
pub struct CallSite {
    // Trailing identifier of the callee, e.g. `Println` for `fmt.Println`.
    pub callee: String,
    // Receiver/package/owner when the parser captured one. Never a file path.
    pub qualifier: Option<String>,
    // 1-based.
    pub line: usize,
    pub is_conditional: bool,
    pub context: String,
}

#[derive(Debug, Clone, Default)]
pub struct Func {
    pub id: String,
    pub name: String,
    pub signature: String,
    pub doc: String,
    // Rendered `name: type` pairs, for hover and completion detail.
    pub params: Vec<Param>,
    pub return_type: String,
    // 1-based, inclusive.
    pub line_start: usize,
    pub line_end: usize,
    pub calls: Vec<CallSite>,
    // Name of the enclosing class, when this is a method.
    pub container: Option<String>,
    pub is_async: bool,
    pub is_pub: bool,
    pub is_static: bool,
    pub is_test: bool,
    pub is_unsafe: bool,
    pub is_extern: bool,
    pub is_trait_method: bool,
    pub decorators: Vec<String>,
    pub complexity: usize,
}

#[derive(Debug, Clone)]
pub struct Param {
    pub name: String,
    pub type_annotation: String,
    pub default_value: Option<String>,
}

impl Param {
    // `name: Type` or `name: Type = default`.
    pub fn render(&self) -> String {
        let mut s = self.name.clone();
        if !self.type_annotation.is_empty() {
            s.push_str(": ");
            s.push_str(&self.type_annotation);
        }
        if let Some(d) = &self.default_value {
            s.push_str(" = ");
            s.push_str(d);
        }
        s
    }
}

#[derive(Debug, Clone, Default)]
pub struct Klass {
    pub id: String,
    pub name: String,
    pub bases: Vec<String>,
    pub doc: String,
    pub line_start: usize,
    pub line_end: usize,
    pub methods: Vec<Func>,
    pub attributes: Vec<Field>,
    pub decorators: Vec<String>,
    // "class" | "struct" | "enum" | "interface" | "trait" | "union" | "record"
    pub kind: String,
    pub is_abstract: bool,
}

#[derive(Debug, Clone)]
pub struct Field {
    pub name: String,
    pub type_annotation: String,
    // 1-based.
    pub line: usize,
}

#[derive(Debug, Clone)]
pub struct GlobalVar {
    pub name: String,
    pub type_annotation: String,
    // 1-based.
    pub line: usize,
}

#[derive(Debug, Clone)]
pub struct ImportItem {
    pub module: String,
    pub items: Vec<String>,
    // "external" | "internal"
    pub import_type: String,
}

impl ImportItem {
    pub fn is_external(&self) -> bool {
        self.import_type == "external"
    }
}

#[derive(Debug, Clone)]
pub struct TodoItem {
    // 1-based.
    pub line: usize,
    pub text: String,
    pub priority: String,
}

#[derive(Debug, Clone)]
pub struct SecurityNote {
    pub note_type: String,
    // 1-based.
    pub line: usize,
    pub description: String,
}

// One parsed file, in server native terms. Never mutated after construction
// except through `Arc`, so it can be shared across handler tasks freely.
#[derive(Debug, Clone, Default)]
pub struct ParsedFile {
    pub language: String,
    pub loc: usize,
    pub functions: Vec<Func>,
    pub classes: Vec<Klass>,
    pub globals: Vec<GlobalVar>,
    pub imports: Vec<ImportItem>,
    pub todos: Vec<TodoItem>,
    pub notes: Vec<SecurityNote>,
    pub syntax: SyntaxReport,
}

impl ParsedFile {
    // Every callable in the file, methods included, with `container` set.
    pub fn all_callables(&self) -> impl Iterator<Item = (&Func, DefKind)> {
        self.functions.iter().map(|f| (f, DefKind::Function)).chain(
            self.classes
                .iter()
                .flat_map(|c| c.methods.iter().map(move |m| (m, DefKind::Method))),
        )
    }

    pub fn is_empty(&self) -> bool {
        self.functions.is_empty()
            && self.classes.is_empty()
            && self.globals.is_empty()
            && self.imports.is_empty()
    }
}

// Why a file couldn't be parsed. Kept separate from "parsed but empty",
// because a file full of syntax errors still yields a usable symbol table,
// and conflating the two is how a buffer ends up permanently dead in an
// editor.
#[derive(Debug, Clone)]
pub struct ParseFailure(pub String);

// Language support

// Extensions libeulix can actually parse.
//
// Note the absences: `Language::detect` maps `.jsx`/`.tsx` to
// `Language::Jsx`/`Language::Tsx`, but `parse_source` has no arm for either
// and falls through to `Err("Unsupported language: …")`. They're excluded
// here so the server never offers features it can't back up. `.inc` is out
// too: C, C++ and assembler share it, and `parse_source` refuses a `.inc`
// without PHP code, which would show up as a parse failure.
pub const SUPPORTED_EXTENSIONS: &[&str] = &[
    "c", "h", "cpp", "cc", "cxx", "hpp", "hxx", "h++", "c++", "go", "java", "js", "mjs", "cjs",
    "php", "phtml", "py", "pyw", "pyi", "rs", "ts", "mts", "cts",
];

pub fn is_supported(path: &Path) -> bool {
    path.extension()
        .and_then(|e| e.to_str())
        .map(|e| e.to_ascii_lowercase())
        .is_some_and(|e| SUPPORTED_EXTENSIONS.contains(&e.as_str()))
}

pub fn detect_language(path: &Path) -> &'static str {
    match Language::detect(path) {
        Language::C => "c",
        Language::Cpp => "cpp",
        Language::Go => "go",
        Language::Java => "java",
        Language::JavaScript => "javascript",
        Language::Jsx => "jsx",
        Language::TypeScript => "typescript",
        Language::Tsx => "tsx",
        Language::Python => "python",
        Language::Rust => "rust",
        Language::Php => "php",
        Language::Unknown => "unknown",
    }
}

// The one call into libeulix

// Parse `src` as `path`, or explain why not.
//
// Never blocks the caller: this is CPU-bound and belongs behind
// `spawn_blocking` (see [`crate::backend::Backend::parse_one`]).
pub fn parse(path: &Path, src: &str) -> Result<ParsedFile, ParseFailure> {
    // The first tuple element is the source path, not the language. Verified
    // against 0.8.1: parse_source("lib.rs", ..) returns ("lib.rs", fd).
    let (_path, fd) = lib_parse_source(path, src).map_err(ParseFailure)?;
    Ok(convert_file_data(fd, path))
}

fn convert_file_data(fd: FileData, path: &Path) -> ParsedFile {
    // `FileData::language` is populated by the parsers, but fall back to our
    // own detection so a parser that leaves it empty still gets a label.
    let language = if fd.language.is_empty() {
        detect_language(path).to_string()
    } else {
        fd.language.clone()
    };
    ParsedFile {
        language,
        loc: fd.loc,
        functions: fd.functions.iter().map(convert_function).collect(),
        classes: fd.classes.iter().map(convert_class).collect(),
        globals: fd
            .global_vars
            .iter()
            .map(|g| GlobalVar {
                name: g.name.clone(),
                type_annotation: g.type_annotation.clone(),
                line: g.line,
            })
            .collect(),
        imports: fd
            .imports
            .iter()
            .map(|i| ImportItem {
                module: i.module.clone(),
                items: i.items.clone(),
                import_type: i.import_type.clone(),
            })
            .collect(),
        todos: fd
            .todos
            .iter()
            .map(|t| TodoItem {
                line: t.line,
                text: t.text.clone(),
                priority: t.priority.clone(),
            })
            .collect(),
        notes: fd
            .security_notes
            .iter()
            .map(|s| SecurityNote {
                note_type: s.note_type.clone(),
                line: s.line,
                description: s.description.clone(),
            })
            .collect(),
        syntax: SyntaxReport {
            error_nodes: fd.syntax.error_nodes,
            missing_nodes: fd.syntax.missing_nodes,
            first_error_line: fd.syntax.first_error_line,
        },
    }
}

fn convert_function(f: &libeulix::struc::kb_struct::Function) -> Func {
    let r = f.lang_info.rust.as_deref();
    let g = f.lang_info.go.as_deref();
    let j = f.lang_info.java.as_deref();
    let t = f.lang_info.typescript.as_deref();
    let js = f.lang_info.javascript.as_deref();
    let c = f.lang_info.c.as_deref();
    let cpp = f.lang_info.cpp.as_deref();
    let php = f.lang_info.php.as_deref();

    Func {
        id: f.id.clone(),
        name: f.name.clone(),
        signature: f.signature.clone(),
        doc: f.docstring.clone(),
        params: f
            .params
            .iter()
            .map(|p| Param {
                name: p.name.clone(),
                type_annotation: p.type_annotation.clone(),
                default_value: p.default_value.clone(),
            })
            .collect(),
        return_type: f.return_type.clone(),
        line_start: f.line_start,
        line_end: f.line_end,
        calls: f
            .calls
            .iter()
            .map(|c| CallSite {
                callee: c.callee.clone(),
                // Verified: libeulix fills this with a receiver/qualifier for
                // Go and Java and leaves it None elsewhere. It's never a path.
                qualifier: c.defined_in.clone().filter(|q| !q.is_empty()),
                line: c.line,
                is_conditional: c.is_conditional,
                context: c.context.clone(),
            })
            .collect(),
        container: None,
        is_async: f.is_async
            || r.is_some_and(|i| i.is_async)
            || g.is_some_and(|i| i.spawns_goroutines)
            || t.is_some_and(|i| i.is_async)
            || js.is_some_and(|i| i.is_async),
        is_pub: r.is_some_and(|i| i.is_pub)
            || g.is_some_and(|i| i.is_exported)
            || t.is_some_and(|i| i.is_exported)
            || js.is_some_and(|i| i.is_exported)
            || j.is_some_and(|i| i.access_modifier.as_deref() == Some("public"))
            || c.is_some_and(|i| !i.is_static)
            || cpp.is_some_and(|i| i.access_specifier.as_deref() != Some("private"))
            // PHP functions and methods without a modifier are public.
            || php.is_some_and(|i| {
                !matches!(i.access_modifier.as_deref(), Some("private" | "protected"))
            }),
        is_static: c.is_some_and(|i| i.is_static)
            || cpp.is_some_and(|i| i.is_static)
            || j.is_some_and(|i| i.is_static)
            || php.is_some_and(|i| i.is_static),
        is_test: r.is_some_and(|i| i.is_test) || j.is_some_and(|i| i.is_test),
        is_unsafe: r.is_some_and(|i| i.is_unsafe),
        is_extern: r.is_some_and(|i| i.is_extern) || cpp.is_some_and(|i| i.is_extern_c),
        is_trait_method: r.is_some_and(|i| i.trait_name.is_some() || i.is_trait_impl_method),
        decorators: f.decorators.clone(),
        complexity: f.complexity,
    }
}

fn convert_class(c: &libeulix::struc::kb_struct::Class) -> Klass {
    let r = c.lang_info.rust.as_deref();
    let j = c.lang_info.java.as_deref();
    let cpp = c.lang_info.cpp.as_deref();
    let g = c.lang_info.go.as_deref();
    let t = c.lang_info.typescript.as_deref();
    let php = c.lang_info.php.as_deref();

    // Each language reports the class-like kind in its own enum, so normalize
    // to a lowercase string that the LSP layer can map to a SymbolKind.
    let kind = if let Some(item) = r.and_then(|i| i.item_kind.as_deref()) {
        item.to_string()
    } else if let Some(k) = j.and_then(|i| i.type_kind.as_ref()) {
        // JavaTypeKind: Class | Interface | Enum | Record | Annotation
        format!("{k:?}").to_lowercase()
    } else if let Some(k) = cpp.map(|i| &i.type_kind) {
        // CppTypeKind: Function | Struct | Union | Enum | Class
        format!("{k:?}").to_lowercase()
    } else if let Some(k) = g.map(|i| &i.type_kind) {
        // GoTypeKind: Struct | Interface | Function | Method
        format!("{k:?}").to_lowercase()
    } else if let Some(k) = php.and_then(|i| i.type_kind.as_ref()) {
        // PhpTypeKind: Class | Interface | Trait | Enum
        format!("{k:?}").to_lowercase()
    } else if t.is_some_and(|i| i.is_abstract) {
        "interface".to_string()
    } else {
        "class".to_string()
    };

    let is_abstract = r.is_some_and(|i| i.is_marker_trait)
        || cpp.is_some_and(|i| i.is_abstract)
        || j.is_some_and(|i| i.is_abstract)
        || t.is_some_and(|i| i.is_abstract)
        || php.is_some_and(|i| i.is_abstract)
        || kind == "interface"
        || kind == "trait";

    Klass {
        id: c.id.clone(),
        name: c.name.clone(),
        bases: c.bases.clone(),
        doc: c.docstring.clone(),
        line_start: c.line_start,
        line_end: c.line_end,
        methods: c
            .methods
            .iter()
            .map(|m| {
                let mut f = convert_function(m);
                f.container = Some(c.name.clone());
                f
            })
            .collect(),
        attributes: c
            .attributes
            .iter()
            .map(|a| Field {
                name: a.name.clone(),
                type_annotation: a.type_annotation.clone(),
                // Attributes carry no line of their own in kb_struct. Rather
                // than pin them to a wrong line here, the semantic token pass
                // emits them at class scope.
                line: c.line_start,
            })
            .collect(),
        decorators: c.decorators.clone(),
        kind,
        is_abstract,
    }
}

#[cfg(test)]
mod tests {

    use super::*;
    use std::path::Path;

    #[test]
    #[allow(clippy::expect_used)]
    fn parses_a_rust_file() {
        let src = r#"
            /// Adds two numbers.
            pub fn add(a: i32, b: i32) -> i32 {
                helper(a) + b
            }

            fn helper(x: i32) -> i32 { x }
        "#;
        let parsed = parse(Path::new("lib.rs"), src).expect("rust should parse");
        assert_eq!(parsed.language, "rust");
        let names: Vec<_> = parsed.functions.iter().map(|f| f.name.as_str()).collect();
        assert!(names.contains(&"add"), "got {names:?}");
        assert!(names.contains(&"helper"), "got {names:?}");
    }

    #[test]
    fn jsx_is_reported_unsupported_rather_than_pretending() {
        // libeulix detects .jsx but has no parse_source arm for it.
        let err = parse(Path::new("App.jsx"), "export default function App() {}");
        assert!(
            err.is_err(),
            "jsx must surface as unsupported, not as empty success"
        );
    }

    #[test]
    fn garbage_still_yields_a_usable_symbol_table() {
        // This is the case that used to brick a buffer: a parse must either
        // give symbols or give an error, never "succeeded with nothing".
        let parsed = parse(Path::new("m.py"), "def good():\n    pass\ndef ((( :\n");
        match parsed {
            Ok(p) => assert!(
                p.functions.iter().any(|f| f.name == "good"),
                "recovered parse should still expose `good`"
            ),
            Err(_) => {} // an explicit error is also acceptable
        }
    }

    #[test]
    fn unsupported_extension_predicate_matches_detect() {
        assert!(is_supported(Path::new("a.rs")));
        assert!(is_supported(Path::new("a.PY")));
        assert!(!is_supported(Path::new("a.jsx")));
        assert!(!is_supported(Path::new("a.md")));
        assert!(is_supported(Path::new("a.php")));
        assert!(!is_supported(Path::new("a.inc")));
    }
}
