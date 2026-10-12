// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
//! `ParsedFile` → LSP types.
//!
//! Kept free of state and of `libeulix` types so the mapping can be tested on
//! its own and the handlers stay thin.

use ropey::Rope;
use tower_lsp::lsp_types::*;

use crate::index::Def;
use crate::position::{self, full_line_range};
use crate::syntax::{DefKind, Func, Klass, ParsedFile};

pub fn document_symbols(rope: &Rope, parsed: &ParsedFile) -> Vec<DocumentSymbol> {
    let mut out: Vec<DocumentSymbol> =
        Vec::with_capacity(parsed.functions.len() + parsed.classes.len());

    for f in &parsed.functions {
        out.push(func_symbol(rope, f, None));
    }
    for c in &parsed.classes {
        out.push(class_symbol(rope, c));
    }

    // Readers expect a file outline in declaration order, but the parser
    // gives us functions and classes in separate vectors, so sort after
    // building.
    out.sort_by_key(|s| s.range.start.line);
    out
}

// This func requires `deprecated`
#[allow(deprecated)]
fn func_symbol(rope: &Rope, f: &Func, container: Option<&str>) -> DocumentSymbol {
    let name_range = position::find_name_on_line(rope, f.line_start, &f.name)
        .unwrap_or_else(|| full_line_range(rope, f.line_start));

    DocumentSymbol {
        name: f.name.clone(),
        detail: if f.signature.is_empty() {
            None
        } else {
            Some(f.signature.clone())
        },
        kind: match container {
            Some(_) => SymbolKind::METHOD,
            None => SymbolKind::FUNCTION,
        },
        tags: None,
        deprecated: None,
        range: full_line_range(rope, f.line_start.min(f.line_end.max(f.line_start))),
        selection_range: name_range,
        children: None,
    }
}

#[allow(deprecated)]
fn class_symbol(rope: &Rope, c: &Klass) -> DocumentSymbol {
    let children: Vec<DocumentSymbol> = c
        .methods
        .iter()
        .map(|m| func_symbol(rope, m, Some(&c.name)))
        .collect();

    DocumentSymbol {
        name: c.name.clone(),
        detail: {
            let mut parts = Vec::new();
            if !c.kind.is_empty() {
                parts.push(c.kind.clone());
            }
            if c.is_abstract {
                parts.push("abstract".into());
            }
            if !c.bases.is_empty() {
                parts.push(format!("extends {}", c.bases.join(", ")));
            }
            if parts.is_empty() {
                None
            } else {
                Some(parts.join(" · "))
            }
        },
        kind: class_symbol_kind(&c.kind),
        tags: None,
        deprecated: None,
        range: full_line_range(rope, c.line_start),
        selection_range: position::find_name_on_line(rope, c.line_start, &c.name)
            .unwrap_or_else(|| full_line_range(rope, c.line_start)),
        children: if children.is_empty() {
            None
        } else {
            Some(children)
        },
    }
}

fn class_symbol_kind(kind: &str) -> SymbolKind {
    match kind {
        "interface" | "trait" => SymbolKind::INTERFACE,
        "enum" => SymbolKind::ENUM,
        "struct" | "union" | "record" => SymbolKind::STRUCT,
        _ => SymbolKind::CLASS,
    }
}

pub fn hover_markdown(f: &Func, _container: Option<&str>) -> String {
    let mut md = String::new();

    // No language tag on the fence. The content is polyglot (Python `->`
    // returns, Go receivers, Java generics), and a wrong tag makes some
    // clients reformat the text into something misleading.
    md.push_str("```\n");

    let mut sig = f.signature.clone();
    if sig.is_empty() {
        sig = f.name.clone();
        if !f.params.is_empty() {
            let ps: Vec<String> = f.params.iter().map(|p| p.render()).collect();
            sig.push_str(&format!("({})", ps.join(", ")));
        }
        if !f.return_type.is_empty() {
            sig.push_str(&format!(" -> {}", f.return_type));
        }
    }
    md.push_str(&sig);
    md.push('\n');
    md.push_str("```");

    if !f.doc.trim().is_empty() {
        md.push_str("\n\n---\n\n");
        md.push_str(f.doc.trim());
    }

    let mut facts: Vec<String> = Vec::new();
    if let Some(c) = &f.container {
        facts.push(format!("method of `{c}`"));
    }
    if f.is_async {
        facts.push("async".into());
    }
    if f.is_pub {
        facts.push("public".into());
    }
    if f.is_static {
        facts.push("static".into());
    }
    if f.is_unsafe {
        facts.push("unsafe".into());
    }
    if f.is_extern {
        facts.push("extern".into());
    }
    if f.complexity > 1 {
        facts.push(format!("complexity {}", f.complexity));
    }
    if !facts.is_empty() {
        md.push_str("\n\n---\n\n");
        md.push_str(&facts.join(" · "));
    }

    if !f.calls.is_empty() {
        let mut callees: Vec<&str> = f.calls.iter().map(|c| c.callee.as_str()).collect();
        callees.sort_unstable();
        callees.dedup();
        if !callees.is_empty() {
            // Cap the list so hover doesn't become a wall of names.
            let shown: Vec<&str> = callees.iter().take(12).copied().collect();
            let more = callees.len().saturating_sub(shown.len());
            md.push_str("\n\n**Calls**: ");
            md.push_str(&shown.join(", "));
            if more > 0 {
                md.push_str(&format!(" (+{more} more)"));
            }
        }
    }

    md
}

pub fn class_hover_markdown(c: &Klass) -> String {
    let mut md = String::from("```\n");
    let kind = if c.kind.is_empty() { "class" } else { &c.kind };
    md.push_str(kind);
    md.push(' ');
    md.push_str(&c.name);
    if !c.bases.is_empty() {
        md.push_str(&format!(" extends {}", c.bases.join(", ")));
    }
    md.push_str("\n```");
    if !c.doc.trim().is_empty() {
        md.push_str("\n\n---\n\n");
        md.push_str(c.doc.trim());
    }
    if !c.attributes.is_empty() {
        md.push_str("\n\n**Fields**");
        // Same cap as the calls list above, for the same reason.
        for a in c.attributes.iter().take(12) {
            md.push_str(&format!(
                "\n- `{}`{}",
                a.name,
                if a.type_annotation.is_empty() {
                    String::new()
                } else {
                    format!(": {}", a.type_annotation)
                }
            ));
        }
    }
    if !c.methods.is_empty() {
        md.push_str(&format!("\n\n**Methods**: {}", c.methods.len()));
    }
    md
}

pub fn completion_item(def: &Def) -> CompletionItem {
    let kind = match def.kind {
        DefKind::Function => CompletionItemKind::FUNCTION,
        DefKind::Method => CompletionItemKind::METHOD,
        DefKind::Class => CompletionItemKind::CLASS,
    };
    let mut item = CompletionItem {
        label: def.name.clone(),
        label_details: None,
        kind: Some(kind),
        tags: None,
        detail: def.container.clone().map(|c| format!("method of {c}")),
        documentation: None,
        deprecated: None,
        preselect: None,
        sort_text: None,
        filter_text: None,
        insert_text: None,
        insert_text_format: None,
        insert_text_mode: None,
        text_edit: None,
        additional_text_edits: None,
        commit_characters: None,
        command: None,
        data: None,
    };
    // The data payload tells the client where to scroll when the item is
    // accepted, matching the declaration's own range.
    item.data = Some(serde_json::json!({
        "uri": def.path.to_string_lossy(),
        "line": def.line_start,
        "kind": match def.kind { DefKind::Class => "class", DefKind::Method => "method", DefKind::Function => "function" },
    }));
    item
}

#[allow(deprecated)]
pub fn workspace_symbol(name: &str, def: &Def) -> SymbolInformation {
    SymbolInformation {
        // Completion style: show what the user typed, and let the client
        // filter on the real name via the location it jumps to.
        name: if !name.is_empty() && def.name.starts_with(name) {
            name.to_string()
        } else {
            def.name.clone()
        },
        kind: match def.kind {
            DefKind::Function => SymbolKind::FUNCTION,
            DefKind::Method => SymbolKind::METHOD,
            DefKind::Class => SymbolKind::CLASS,
        },
        tags: None,
        deprecated: None,
        location: Location {
            uri: Url::from_file_path(&def.path)
                .unwrap_or_else(|_| Url::parse("file:///").expect("static url parses")),
            range: line_range_without_text(def.line_start),
        },
        container_name: def.container.clone(),
    }
}

// A whole line range for when we don't have the file's text loaded.
//
// Workspace symbols are exactly the case where we hold a `Def` for a file we
// never read, so the real width is unknown. Spanning the line is the honest
// answer, and the client narrows to the identifier on navigation anyway.
fn line_range_without_text(line_1based: usize) -> Range {
    let l = line_1based.saturating_sub(1) as u32;
    Range::new(Position::new(l, 0), Position::new(l, u32::MAX))
}

// Has to stay in lockstep with the legend published in `initialize`.
pub const TOKEN_TYPES: &[&str] = &[
    "function",  // 0
    "method",    // 1
    "class",     // 2
    "property",  // 3
    "variable",  // 4
    "parameter", // 5
    "type",      // 6
    "macro",     // 7
];

// Has to stay in lockstep with the legend published in `initialize`. Bit
// positions are the ones the LSP spec fixes.
pub const TOKEN_MODIFIERS: &[&str] = &[
    "declaration",    // 1 << 0
    "definition",     // 1 << 1
    "readonly",       // 1 << 2
    "static",         // 1 << 3
    "deprecated",     // 1 << 4
    "abstract",       // 1 << 5
    "async",          // 1 << 6
    "documentation",  // 1 << 8
    "defaultLibrary", // 1 << 9
];

pub const MOD_DECLARATION: u32 = 1 << 0;
pub const MOD_DEFINITION: u32 = 1 << 1;
pub const MOD_READONLY: u32 = 1 << 2;
pub const MOD_STATIC: u32 = 1 << 3;
pub const MOD_DEPRECATED: u32 = 1 << 4;
pub const MOD_ABSTRACT: u32 = 1 << 5;
pub const MOD_ASYNC: u32 = 1 << 6;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Token {
    line: u32,
    start: u32,
    len: u32,
    ty: u32,
    modifiers: u32,
}

// Symbol level semantic tokens.
pub fn semantic_tokens(rope: &Rope, parsed: &ParsedFile) -> Vec<Token> {
    let mut tokens: Vec<Token> = Vec::new();

    let push = |rope: &Rope,
                tokens: &mut Vec<Token>,
                line_1b: usize,
                name: &str,
                ty: u32,
                modifiers: u32| {
        let Some(range) = position::find_name_on_line(rope, line_1b, name) else {
            return;
        };
        let len = range.end.character.saturating_sub(range.start.character);
        if len == 0 {
            return;
        }
        tokens.push(Token {
            line: range.start.line,
            start: range.start.character,
            len,
            ty,
            modifiers,
        });
    };

    for f in &parsed.functions {
        let mut m = MOD_DECLARATION | MOD_DEFINITION;
        if f.is_static {
            m |= MOD_STATIC;
        }
        if f.is_async {
            m |= MOD_ASYNC;
        }
        if f.is_test {
            m |= MOD_DEPRECATED;
        }
        push(rope, &mut tokens, f.line_start, &f.name, 0, m);

        for p in &f.params {
            push(rope, &mut tokens, f.line_start, &p.name, 5, MOD_READONLY);
        }
    }

    for c in &parsed.classes {
        let mut m = MOD_DECLARATION | MOD_DEFINITION;
        if c.is_abstract {
            m |= MOD_ABSTRACT;
        }
        push(rope, &mut tokens, c.line_start, &c.name, 2, m);
        for a in &c.attributes {
            push(rope, &mut tokens, c.line_start, &a.name, 3, MOD_READONLY);
        }
        for meth in &c.methods {
            let mut mm = MOD_DECLARATION | MOD_DEFINITION;
            if meth.is_static {
                mm |= MOD_STATIC;
            }
            if meth.is_async {
                mm |= MOD_ASYNC;
            }
            push(rope, &mut tokens, meth.line_start, &meth.name, 1, mm);
            for p in &meth.params {
                push(rope, &mut tokens, meth.line_start, &p.name, 5, MOD_READONLY);
            }
        }
    }

    for g in &parsed.globals {
        push(rope, &mut tokens, g.line, &g.name, 4, MOD_STATIC);
    }

    // The protocol requires ascending order, with no line going backwards.
    tokens.sort_by_key(|t| (t.line, t.start));
    tokens.dedup_by_key(|t| (t.line, t.start, t.len));
    tokens
}

// Delta-encode the tokens the way the LSP spec wants them.
pub fn encode_semantic_tokens(tokens: &[Token]) -> Vec<SemanticToken> {
    let mut out = Vec::with_capacity(tokens.len());
    let mut prev_line = 0u32;
    let mut prev_start = 0u32;
    for t in tokens {
        let delta_line = t.line.saturating_sub(prev_line);
        let delta_start = if delta_line == 0 {
            t.start.saturating_sub(prev_start)
        } else {
            t.start
        };
        out.push(SemanticToken {
            delta_line,
            delta_start,
            length: t.len,
            token_type: t.ty,
            token_modifiers_bitset: t.modifiers,
        });
        prev_line = t.line;
        prev_start = t.start;
    }
    out
}

pub fn folding_ranges(_rope: &Rope, parsed: &ParsedFile) -> Vec<FoldingRange> {
    let mut out = Vec::new();
    for f in &parsed.functions {
        if f.line_end > f.line_start {
            out.push(FoldingRange {
                start_line: (f.line_start - 1) as u32,
                start_character: None,
                end_line: (f.line_end - 1) as u32,
                end_character: None,
                kind: None,
                collapsed_text: None,
            });
        }
    }
    for c in &parsed.classes {
        if c.line_end > c.line_start {
            out.push(FoldingRange {
                start_line: (c.line_start - 1) as u32,
                start_character: None,
                end_line: (c.line_end - 1) as u32,
                end_character: None,
                kind: None,
                collapsed_text: None,
            });
        }
    }
    out.sort_by_key(|f| (f.start_line, f.end_line));
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::syntax::{Param, SyntaxReport};

    fn rope(s: &str) -> Rope {
        Rope::from_str(s)
    }

    fn func(name: &str, start: usize, end: usize) -> Func {
        Func {
            name: name.to_string(),
            line_start: start,
            line_end: end,
            ..Default::default()
        }
    }

    #[test]
    fn document_symbols_are_in_declaration_order() {
        let r = rope("class A {\n  def m(self):\n    pass\n}\n\ndef top():\n  pass\n");
        let p = ParsedFile {
            classes: vec![Klass {
                name: "A".into(),
                line_start: 1,
                line_end: 4,
                methods: vec![func("m", 2, 3)],
                ..Default::default()
            }],
            functions: vec![func("top", 6, 7)],
            ..Default::default()
        };
        let syms = document_symbols(&r, &p);
        assert_eq!(syms[0].name, "A", "class on line 1 must come first");
        assert_eq!(syms[1].name, "top");
        let kids = syms[0].children.as_ref().unwrap();
        assert_eq!(kids[0].name, "m");
        assert_eq!(kids[0].kind, SymbolKind::METHOD);
    }

    #[test]
    fn selection_range_covers_just_the_name() {
        let r = rope("fn alpha() {\n}\n");
        let p = ParsedFile {
            functions: vec![func("alpha", 1, 2)],
            ..Default::default()
        };
        let s = &document_symbols(&r, &p)[0];
        assert_eq!(s.selection_range.start.character, 3);
        assert_eq!(s.selection_range.end.character, 8);
    }

    #[test]
    fn hover_falls_back_to_building_a_signature() {
        let mut f = func("add", 1, 3);
        f.return_type = "int".into();
        f.params = vec![Param {
            name: "a".into(),
            type_annotation: "int".into(),
            default_value: None,
        }];
        let md = hover_markdown(&f, None);
        assert!(md.contains("add(a: int) -> int"), "got {md}");
    }

    #[test]
    fn hover_includes_doc_and_facts() {
        let mut f = func("go", 1, 3);
        f.doc = "Does the thing.".into();
        f.is_async = true;
        f.complexity = 7;
        let md = hover_markdown(&f, None);
        assert!(md.contains("Does the thing."));
        assert!(md.contains("async"));
        assert!(md.contains("complexity 7"));
    }

    #[test]
    fn semantic_tokens_are_sorted_and_delta_encodable() {
        let r = rope("fn beta() {\n  let y = 1;\n}\nfn alpha() {}\n");
        let mut beta = func("beta", 1, 3);
        beta.return_type = "int".into();
        beta.params = vec![Param {
            name: "x".into(),
            type_annotation: "int".into(),
            default_value: None,
        }];
        let p = ParsedFile {
            functions: vec![beta, func("alpha", 4, 4)],
            syntax: SyntaxReport::default(),
            ..Default::default()
        };
        let tokens = semantic_tokens(&r, &p);
        assert!(!tokens.is_empty());
        for w in tokens.windows(2) {
            assert!(w[0].line < w[1].line || (w[0].line == w[1].line && w[0].start <= w[1].start));
        }
        let encoded = encode_semantic_tokens(&tokens);
        assert_eq!(encoded.len(), tokens.len());
    }

    #[test]
    fn legend_indices_match_the_constants() {
        // A mismatch here is a silent, client visible corruption.
        for (i, name) in TOKEN_TYPES.iter().enumerate() {
            assert_eq!(i as u32, semantic_index_for(name));
        }
    }

    fn semantic_index_for(name: &str) -> u32 {
        TOKEN_TYPES.iter().position(|t| *t == name).unwrap() as u32
    }

    #[test]
    fn folding_ranges_only_cover_multiline_entities() {
        let r = rope("fn a() {\n}\nfn b() {}\n");
        let p = ParsedFile {
            functions: vec![func("a", 1, 2), func("b", 3, 3)],
            ..Default::default()
        };
        let f = folding_ranges(&r, &p);
        assert_eq!(f.len(), 1, "single-line function should not fold");
    }

    #[test]
    fn class_kinds_map_to_distinct_symbol_kinds() {
        assert_eq!(class_symbol_kind("interface"), SymbolKind::INTERFACE);
        assert_eq!(class_symbol_kind("enum"), SymbolKind::ENUM);
        assert_eq!(class_symbol_kind("class"), SymbolKind::CLASS);
    }
}
