// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0
//
//! Diagnostics
//!
//! Severity and noise policy matter more here than the parsing itself. An
//! editor that lights up a file with 40 speculative warnings gets muted, and
//! then the one real signal it had is gone too. So:
//!
//!   * syntax errors are the primary signal. They're line-granular because
//!     [`SyntaxReport`] carries no per-error span, see the note in
//!     [`crate::syntax`];
//!   * duplicate definitions within a single file are always reported,
//!     because that's a real defect with no false-positive mode;
//!   * unresolved calls are off by default. At this level of analysis, a
//!     call into the standard library, a macro expansion, a dynamic
//!     dispatch, and a typo all look the same, so turning it on without a
//!     curated allowlist just produces noise the user can't act on.

use ropey::Rope;
use tower_lsp::lsp_types::{Diagnostic, DiagnosticSeverity, NumberOrString, Position, Range, Url};

use crate::config::Config;
use crate::index::Index;
use crate::position::full_line_range;
use crate::syntax::ParsedFile;

const SOURCE: &str = "eulixlsp";

// Full-line range if we have the text, otherwise a synthetic range on the
// reported line. The synthetic fallback uses u32::MAX width because we have
// no way to know the real width without the file contents.
fn line_range_or_zero(rope: Option<&Rope>, line_1based: usize) -> Range {
    match rope {
        Some(r) => full_line_range(r, line_1based),
        None => {
            let l = line_1based.saturating_sub(1) as u32;
            Range::new(Position::new(l, 0), Position::new(l, u32::MAX))
        }
    }
}

pub fn diagnostics_for(
    uri: &Url,
    path: &std::path::Path,
    parsed: &ParsedFile,
    rope: Option<&Rope>,
    cfg: &Config,
    index: &Index,
) -> Vec<Diagnostic> {
    let mut out: Vec<Diagnostic> = Vec::new();

    // Syntax
    if !parsed.syntax.is_clean() {
        let line = parsed.syntax.first_error_line.unwrap_or(1);
        let mut parts = Vec::new();
        if parsed.syntax.error_nodes > 0 {
            parts.push(format!(
                "{} error node{}",
                parsed.syntax.error_nodes,
                plural(parsed.syntax.error_nodes)
            ));
        }
        if parsed.syntax.missing_nodes > 0 {
            parts.push(format!(
                "{} missing node{}",
                parsed.syntax.missing_nodes,
                plural(parsed.syntax.missing_nodes)
            ));
        }
        out.push(diag(
            line_range_or_zero(rope, line),
            DiagnosticSeverity::ERROR,
            "syntax",
            format!(
                "Syntax recovery: {}. Symbol extraction below this point is best-effort.",
                parts.join(", ")
            ),
        ));
    }

    // Duplicate definitions in one file.
    //
    // Deliberately scoped to a single file. Two functions named `run` in
    // different modules is idiomatic in most of the languages libeulix parses
    // and isn't a defect. The same name twice in one file is.
    let mut seen: std::collections::HashMap<(&str, &str), usize> = std::collections::HashMap::new();
    let mut dupes: Vec<(&str, usize, &'static str)> = Vec::new();
    for (kind, name, line, container) in parsed
        .functions
        .iter()
        .map(|f| ("function", f.name.as_str(), f.line_start, ""))
        .chain(parsed.classes.iter().flat_map(|c| {
            std::iter::once(("class", c.name.as_str(), c.line_start, "")).chain(
                c.methods
                    .iter()
                    .map(|m| ("method", m.name.as_str(), m.line_start, c.name.as_str())),
            )
        }))
    {
        // Keying on (name, container) means two methods with the same name in
        // different classes don't collide.
        let key = (name, container);
        match seen.get(&key) {
            Some(first) => dupes.push((name, *first, kind)),
            None => {
                seen.insert(key, line);
            }
        }
    }
    let mut dupes = dupes;
    dupes.sort_unstable();
    dupes.dedup();
    for (name, line, kind) in dupes {
        out.push(diag(
            line_range_or_zero(rope, line),
            DiagnosticSeverity::WARNING,
            "duplicate-definition",
            format!("Duplicate {kind} `{name}` in this file; the later definition shadows the earlier one."),
        ));
    }

    // Parser-reported TODOs and security notes
    if cfg.report_todos {
        for t in &parsed.todos {
            out.push(diag(
                line_range_or_zero(rope, t.line),
                DiagnosticSeverity::INFORMATION,
                "todo",
                format!("{}: {}", t.priority.to_uppercase(), t.text.trim()),
            ));
        }
    }
    if cfg.report_security_notes {
        for s in &parsed.notes {
            out.push(diag(
                line_range_or_zero(rope, s.line),
                DiagnosticSeverity::WARNING,
                "security",
                format!("[{}] {}", s.note_type, s.description.trim()),
            ));
        }
    }

    // Unresolved calls, opt-in only.
    if cfg.report_unresolved_calls {
        // Names brought into scope by an import might not be defined anywhere
        // in this workspace, so skip them instead of flagging.
        let imported: std::collections::HashSet<&str> = parsed
            .imports
            .iter()
            .flat_map(|i| i.items.iter().map(String::as_str))
            .collect();

        for call in parsed
            .functions
            .iter()
            .flat_map(|f| f.calls.iter().map(move |c| (c, f.name.as_str())))
            .chain(parsed.classes.iter().flat_map(|c| {
                c.methods
                    .iter()
                    .flat_map(move |m| m.calls.iter().map(move |x| (x, m.name.as_str())))
            }))
        {
            let (csite, _caller) = call;
            if imported.contains(csite.callee.as_str()) {
                continue;
            }
            // If we have a qualifier (Go package, Java receiver, etc) we
            // can't tell whether it resolves, so leave it alone. Only bare
            // calls with no definition in the index get flagged.
            if index.definition_count(&csite.callee) == 0 && !csite.qualifier.is_some() {
                out.push(diag(
                    line_range_or_zero(rope, csite.line),
                    DiagnosticSeverity::HINT,
                    "unresolved-call",
                    format!(
                        "`{}` has no definition in the indexed workspace.",
                        csite.callee
                    ),
                ));
            }
        }
    }

    let _ = (uri, path);
    out
}

// Diagnostics for a file that couldn't be parsed at all. Distinct from
// diagnostics_for because that path assumes we have a ParsedFile to work
// with, and this one is what we emit when we don't.
pub fn parse_failure_diagnostic(rope: Option<&Rope>, error: &str) -> Diagnostic {
    diag(
        match rope {
            Some(r) => full_line_range(r, 1),
            None => Range::new(Position::new(0, 0), Position::new(0, u32::MAX)),
        },
        DiagnosticSeverity::ERROR,
        "parse-failed",
        format!("eulixlsp could not parse this file: {error}. Language features are unavailable until it does."),
    )
}

fn plural(n: usize) -> &'static str {
    if n == 1 {
        ""
    } else {
        "s"
    }
}

fn diag(range: Range, severity: DiagnosticSeverity, code: &str, message: String) -> Diagnostic {
    Diagnostic {
        range,
        severity: Some(severity),
        code: Some(NumberOrString::String(code.to_string())),
        code_description: None,
        source: Some(SOURCE.to_string()),
        message,
        related_information: None,
        tags: None,
        data: None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::syntax::{CallSite, Func, SyntaxReport, TodoItem};

    fn uri() -> Url {
        Url::parse("file:///w/a.rs").unwrap()
    }

    fn fn_(name: &str, line: usize) -> Func {
        Func {
            name: name.to_string(),
            line_start: line,
            line_end: line + 1,
            ..Default::default()
        }
    }

    fn cfg() -> Config {
        Config::default()
    }

    #[test]
    fn clean_file_produces_nothing() {
        let p = ParsedFile {
            syntax: SyntaxReport::default(),
            ..Default::default()
        };
        let out = diagnostics_for(
            &uri(),
            std::path::Path::new("/w/a.rs"),
            &p,
            None,
            &cfg(),
            &Index::new(),
        );
        assert!(out.is_empty());
    }

    #[test]
    fn syntax_errors_become_error_diagnostics_at_the_reported_line() {
        let p = ParsedFile {
            syntax: SyntaxReport {
                error_nodes: 2,
                missing_nodes: 1,
                first_error_line: Some(7),
            },
            ..Default::default()
        };
        let out = diagnostics_for(
            &uri(),
            std::path::Path::new("/w/a.rs"),
            &p,
            None,
            &cfg(),
            &Index::new(),
        );
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].severity, Some(DiagnosticSeverity::ERROR));
        // libeulix reports 1-based lines, LSP wants 0-based, so line 7
        // becomes index 6.
        assert_eq!(out[0].range.start.line, 6);
    }

    #[test]
    fn duplicate_names_in_one_file_are_reported_once() {
        let p = ParsedFile {
            functions: vec![fn_("run", 1), fn_("run", 10), fn_("run", 20)],
            ..Default::default()
        };
        let out = diagnostics_for(
            &uri(),
            std::path::Path::new("/w/a.rs"),
            &p,
            None,
            &cfg(),
            &Index::new(),
        );
        assert_eq!(out.len(), 1, "three `run` defs should yield one complaint");
        assert!(out[0].message.contains("run"));
    }

    #[test]
    fn unresolved_calls_are_silent_unless_enabled() {
        let mut f = fn_("caller", 1);
        f.calls = vec![CallSite {
            callee: "mystery".into(),
            qualifier: None,
            line: 3,
            is_conditional: false,
            context: "unconditional".into(),
        }];
        let p = ParsedFile {
            functions: vec![f],
            ..Default::default()
        };

        assert!(
            diagnostics_for(
                &uri(),
                std::path::Path::new("/w/a.rs"),
                &p,
                None,
                &cfg(),
                &Index::new()
            )
            .is_empty(),
            "unresolved calls must be off by default"
        );

        let mut c = cfg();
        c.report_unresolved_calls = true;
        let out = diagnostics_for(
            &uri(),
            std::path::Path::new("/w/a.rs"),
            &p,
            None,
            &c,
            &Index::new(),
        );
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].severity, Some(DiagnosticSeverity::HINT));
    }

    #[test]
    fn qualified_calls_are_not_flagged_as_unresolved() {
        let mut f = fn_("caller", 1);
        f.calls = vec![CallSite {
            callee: "Println".into(),
            qualifier: Some("fmt".into()),
            line: 3,
            is_conditional: false,
            context: "unconditional".into(),
        }];
        let p = ParsedFile {
            functions: vec![f],
            ..Default::default()
        };
        let mut c = cfg();
        c.report_unresolved_calls = true;
        assert!(diagnostics_for(
            &uri(),
            std::path::Path::new("/w/a.rs"),
            &p,
            None,
            &c,
            &Index::new()
        )
        .is_empty());
    }

    #[test]
    fn todos_and_security_notes_respect_their_toggles() {
        let p = ParsedFile {
            todos: vec![TodoItem {
                line: 4,
                text: "fix this".into(),
                priority: "high".into(),
            }],
            ..Default::default()
        };
        assert_eq!(
            diagnostics_for(
                &uri(),
                std::path::Path::new("/w/a.rs"),
                &p,
                None,
                &cfg(),
                &Index::new()
            )
            .len(),
            1
        );
        let mut c = cfg();
        c.report_todos = false;
        assert!(diagnostics_for(
            &uri(),
            std::path::Path::new("/w/a.rs"),
            &p,
            None,
            &c,
            &Index::new()
        )
        .is_empty());
    }

    #[test]
    fn ranges_are_clamped_to_real_line_widths() {
        let rope = Rope::from_str("ab\ncd\n");
        let p = ParsedFile {
            syntax: SyntaxReport {
                error_nodes: 1,
                missing_nodes: 0,
                first_error_line: Some(1),
            },
            ..Default::default()
        };
        let out = diagnostics_for(
            &uri(),
            std::path::Path::new("/w/a.rs"),
            &p,
            Some(&rope),
            &cfg(),
            &Index::new(),
        );
        assert_eq!(
            out[0].range.end.character, 2,
            "must be the line width, not u32::MAX"
        );
    }
}
