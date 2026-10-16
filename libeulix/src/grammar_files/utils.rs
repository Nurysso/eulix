// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::struc::kb_struct::*;
use regex::Regex;
use std::sync::LazyLock;

#[allow(clippy::expect_used)] // patterns are compile-time literals; failure = programmer bug at first use
pub fn static_regex(pattern: &str) -> Regex {
    Regex::new(pattern).expect("static regex pattern must be valid")
}

static TODO_RE: LazyLock<Regex> = LazyLock::new(|| {
    static_regex(r"(?i)(?://|/\*).*?\b(?:TODO|FIXME|XXX)\b[:\s]*(.*?)(?:\*/\s*)?$")
});

// The text of one comment, without its comment marker. Languages whose
// comments don't start with `//` or `/*` (Python's `#`) use this on their
// comment nodes instead of `extract_todos`.
static COMMENT_TODO_RE: LazyLock<Regex> =
    LazyLock::new(|| static_regex(r"(?i)\b(?:TODO|FIXME|XXX)\b[:\s]*(.*?)\s*$"));

pub fn extract_todos(source_code: &str) -> Vec<Todo> {
    source_code
        .lines()
        .enumerate()
        .filter_map(|(idx, line)| {
            TODO_RE
                .captures(line)
                .and_then(|caps| caps.get(1).map(|m| make_todo(idx + 1, m.as_str())))
        })
        .collect()
}

/// A TODO from the text of a single comment (marker already stripped) on a
/// 1-based line, or `None` if the comment has no TODO, FIXME or XXX.
pub fn todo_in_comment(line: usize, comment: &str) -> Option<Todo> {
    COMMENT_TODO_RE
        .captures(comment)
        .and_then(|caps| caps.get(1).map(|m| make_todo(line, m.as_str())))
}

fn make_todo(line: usize, text: &str) -> Todo {
    let text = text.trim().to_string();
    let text_lower = text.to_lowercase();

    let priority = if text_lower.contains("critical") || text_lower.contains("urgent") {
        "high"
    } else if text_lower.contains("minor") {
        "low"
    } else {
        "medium"
    };

    Todo {
        line,
        text,
        priority: priority.to_string(),
    }
}
