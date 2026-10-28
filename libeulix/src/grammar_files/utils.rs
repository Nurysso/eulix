// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::struc::kb_struct::*;
use regex::Regex;
use std::cell::RefCell;
use std::sync::LazyLock;
use tree_sitter::Node;

pub const NESTED_TAG: &str = "nested";

pub(super) const JS_NESTED_KINDS: &[&str] =
    &["function_declaration", "generator_function_declaration"];

/// Functions declared in another function's body, extracted with an id
/// scoped by their parent (`func_outer.inner::file`) and tagged `NESTED_TAG`.
#[derive(Default)]
pub(super) struct Nested {
    fns: RefCell<Vec<Function>>,
    next_id: RefCell<Option<String>>,
}

impl Nested {
    pub(super) fn id(&self, default: impl FnOnce() -> String) -> String {
        self.next_id.take().unwrap_or_else(default)
    }

    pub(super) fn take(&self) -> Vec<Function> {
        self.fns.take()
    }

    /// `parse` must call `self.id` for the function's id.
    pub(super) fn collect<'t>(
        &self,
        body: Node<'t>,
        kinds: &[&str],
        parent_id: &str,
        src: &str,
        mut parse: impl FnMut(Node<'t>) -> Option<Function>,
    ) {
        let Some((scope, file)) = parent_id.split_once("::") else {
            return;
        };
        let mut found = Vec::new();
        walk(body, (), |n, _| {
            if kinds.contains(&n.kind()) {
                found.push(n);
                return None;
            }
            Some(())
        });
        for n in found {
            let Some(name) = n
                .child_by_field_name("name")
                .and_then(|x| x.utf8_text(src.as_bytes()).ok())
            else {
                continue;
            };
            let mut id = format!("{scope}.{name}::{file}");
            if self.fns.borrow().iter().any(|f| f.id == id) {
                id = format!("{scope}.{name}@{}::{file}", n.start_position().row + 1);
            }
            *self.next_id.borrow_mut() = Some(id);
            let parsed = parse(n);
            self.next_id.take();
            if let Some(mut f) = parsed {
                f.tags.push(NESTED_TAG.to_string());
                self.fns.borrow_mut().push(f);
            }
        }
    }
}

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

    fn parse(path: &str, src: &str) -> FileData {
        crate::grammar_files::parse_source(std::path::Path::new(path), src)
            .expect(path)
            .1
    }

    fn callees<'a>(fd: &'a FileData, id: &str) -> Vec<&'a str> {
        let f = fd.functions.iter().find(|f| f.id == id).expect(id);
        f.calls.iter().map(|c| c.callee.as_str()).collect()
    }

    #[test]
    fn nested_functions_get_scoped_ids_and_their_own_calls() {
        let cases = [
            (
                "lib.rs",
                "fn compute(y: u32) -> u32 { y }\n\
                 fn outer(x: u32) -> u32 {\n    fn inner(y: u32) -> u32 { compute(y) }\n    inner(x)\n}\n",
            ),
            (
                "lib.py",
                "def compute(y):\n    return y\n\n\
                 def outer(x):\n    def inner(y):\n        return compute(y)\n    return inner(x)\n",
            ),
            (
                "lib.ts",
                "function compute(y: number) { return y; }\n\
                 function outer(x: number) {\n  function inner(y: number) { return compute(y); }\n  return inner(x);\n}\n",
            ),
            (
                "lib.js",
                "function compute(y) { return y; }\n\
                 function outer(x) {\n  function inner(y) { return compute(y); }\n  return inner(x);\n}\n",
            ),
        ];
        for (path, src) in cases {
            let fd = parse(path, src);
            assert_eq!(
                callees(&fd, &format!("func_outer::{path}")),
                ["inner"],
                "{path}"
            );
            let inner = format!("func_outer.inner::{path}");
            assert_eq!(callees(&fd, &inner), ["compute"], "{path}");
            assert!(
                fd.functions
                    .iter()
                    .any(|f| f.id == inner && f.tags.iter().any(|t| t == NESTED_TAG)),
                "{path}"
            );
        }
    }

    #[test]
    fn same_named_nested_functions_are_told_apart_by_line() {
        let fd = parse(
            "x.py",
            "def as_path():\n    if unix:\n        def imp():\n            return a()\n    else:\n        def imp():\n            return b()\n    return imp()\n",
        );
        assert_eq!(callees(&fd, "func_as_path.imp::x.py"), ["a"]);
        assert_eq!(callees(&fd, "func_as_path.imp@6::x.py"), ["b"]);
    }

    #[test]
    fn nesting_two_levels_deep_scopes_by_the_full_path() {
        let fd = parse(
            "x.py",
            "def outer():\n    def mid():\n        def deep():\n            leaf()\n        deep()\n    mid()\n",
        );
        assert_eq!(callees(&fd, "func_outer::x.py"), ["mid"]);
        assert_eq!(callees(&fd, "func_outer.mid::x.py"), ["deep"]);
        assert_eq!(callees(&fd, "func_outer.mid.deep::x.py"), ["leaf"]);
    }

    #[test]
    fn functions_nested_in_methods_are_scoped_by_the_method() {
        let rs = parse(
            "lib.rs",
            "struct S;\nimpl S {\n    fn m(&self) {\n        fn h() { g() }\n        h()\n    }\n}\n",
        );
        assert_eq!(callees(&rs, "method_S_m.h::lib.rs"), ["g"]);
        let m = rs
            .classes
            .iter()
            .flat_map(|c| &c.methods)
            .find(|m| m.name == "m");
        let m_calls: Vec<&str> = m
            .expect("method m")
            .calls
            .iter()
            .map(|c| c.callee.as_str())
            .collect();
        assert_eq!(m_calls, ["h"]);

        let ts = parse(
            "c.ts",
            "class C {\n  m() {\n    function h() { g(); }\n    h();\n  }\n}\n",
        );
        assert_eq!(callees(&ts, "method_C_m.h::c.ts"), ["g"]);
    }

    #[test]
    fn nested_body_does_not_count_toward_the_outer_function() {
        let fd = parse(
            "lib.rs",
            "fn plain() {}\n\
             fn outer() {\n    fn inner(x: bool) {\n        if x { println!(\"a\") }\n    }\n    inner(true)\n}\n",
        );
        let get = |id: &str| fd.functions.iter().find(|f| f.id == id).expect(id);
        let (plain, outer) = (get("func_plain::lib.rs"), get("func_outer::lib.rs"));
        let inner = get("func_outer.inner::lib.rs");
        assert_eq!(outer.complexity, plain.complexity);
        assert!(inner.complexity > plain.complexity);
        let macros = |f: &Function| {
            f.lang_info
                .rust
                .as_ref()
                .expect("rust info")
                .macro_calls
                .clone()
        };
        assert!(macros(outer).is_empty());
        assert!(!macros(inner).is_empty());
    }

    #[test]
    fn closures_stay_part_of_the_outer_function() {
        let fd = parse(
            "lib.js",
            "function outer() {\n  const f = () => g();\n  f();\n}\n",
        );
        assert!(callees(&fd, "func_outer::lib.js").contains(&"g"));
        assert!(!fd.functions.iter().any(|f| f.id.contains("outer.")));
    }
}
