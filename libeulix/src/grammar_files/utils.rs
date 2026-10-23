// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::struc::kb_struct::*;
use regex::Regex;
use std::sync::LazyLock;
use tree_sitter::Node;

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

/// Pre-order walk of `root`'s subtree without recursion. A recursive walk
/// grows the stack with the depth of the tree, and long expression chains
/// (generated or minified code, PHP templates' `.` concatenation) are deep
/// enough to overflow a worker thread's stack. `enter` gets each node with
/// the context inherited from its parent and returns the context for the
/// node's children, or `None` to skip them.
pub(super) fn walk<'t, C: Copy>(
    root: Node<'t>,
    ctx: C,
    mut enter: impl FnMut(Node<'t>, C) -> Option<C>,
) {
    let mut cursor = root.walk();
    // Context handed to the children of each node on the current path.
    let mut stack: Vec<C> = Vec::new();
    let mut next = enter(root, ctx);
    loop {
        if let Some(c) = next {
            if cursor.goto_first_child() {
                stack.push(c);
                next = enter(cursor.node(), c);
                continue;
            }
        }
        loop {
            if cursor.goto_next_sibling() {
                next = enter(cursor.node(), stack.last().copied().unwrap_or(ctx));
                break;
            }
            if !cursor.goto_parent() {
                return;
            }
            stack.pop();
        }
    }
}

#[cfg(test)]
mod tests {
    #![expect(clippy::expect_used)]

    use super::*;
    use tree_sitter::Parser;

    #[test]
    fn walk_preserves_preorder_pruning_and_sibling_context() {
        let source = "outer(first(), skip(hidden()), last()); after();";
        let mut parser = Parser::new();
        parser
            .set_language(&tree_sitter_javascript::LANGUAGE.into())
            .expect("JavaScript grammar");
        let tree = parser.parse(source, None).expect("syntax tree");
        let mut calls = Vec::new();
        walk(tree.root_node(), 0usize, |node, depth| {
            if node.kind() != "call_expression" {
                return Some(depth);
            }
            let name = node
                .child_by_field_name("function")
                .expect("call target")
                .utf8_text(source.as_bytes())
                .expect("call name");
            calls.push((name, depth));
            if name == "skip" {
                None
            } else {
                Some(depth + 1)
            }
        });
        assert_eq!(
            calls,
            [
                ("outer", 0),
                ("first", 1),
                ("skip", 1),
                ("last", 1),
                ("after", 0)
            ]
        );
    }

    #[test]
    fn walk_stays_inside_the_selected_subtree() {
        let source = "first(); second();";
        let mut parser = Parser::new();
        parser
            .set_language(&tree_sitter_javascript::LANGUAGE.into())
            .expect("JavaScript grammar");
        let tree = parser.parse(source, None).expect("syntax tree");
        let first = tree.root_node().named_child(0).expect("first statement");
        let mut names = Vec::new();
        walk(first, (), |node, _| {
            if node.kind() == "identifier" {
                names.push(node.utf8_text(source.as_bytes()).expect("identifier"));
            }
            Some(())
        });
        assert_eq!(names, ["first"]);
    }
}
