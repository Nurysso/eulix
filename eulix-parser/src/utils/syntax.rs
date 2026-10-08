//! Tree-sitter never "fails" on bad input: `Parser::parse` returns a tree even
//! for garbage, with ERROR / MISSING nodes marking where it had to recover.
//! This module turns that into a number you can report.
//!
//! Cost: `root.has_error()` is a flag lookup, so clean files pay nothing. The
//! walk only runs on files that actually contain errors, and it only descends
//! into subtrees whose `has_error()` is set.
//!
//! Only uses `Node::{has_error,is_error,is_missing,start_position}` and
//! `TreeCursor`, which are identical in tree-sitter 0.20 and current.

use tree_sitter::Tree;

#[derive(Debug, Clone, Copy, Default)]
pub struct SyntaxHealth {
    /// Number of ERROR nodes (tokens/regions the grammar couldn't fit).
    pub error_nodes: usize,
    /// Number of MISSING nodes (tokens the parser inserted to recover).
    pub missing_nodes: usize,
    /// 1-based line of the first error in source order, if any.
    pub first_error_line: Option<usize>,
}

impl SyntaxHealth {
    #[inline]
    pub fn is_clean(&self) -> bool {
        self.error_nodes == 0 && self.missing_nodes == 0
    }
}

pub fn inspect(tree: &Tree) -> SyntaxHealth {
    let root = tree.root_node();
    let mut health = SyntaxHealth::default();
    if !root.has_error() {
        return health;
    }

    let mut cursor = root.walk();
    'walk: loop {
        let node = cursor.node();

        if node.is_error() {
            health.error_nodes += 1;
            note_line(&mut health, node.start_position().row);
        } else if node.is_missing() {
            health.missing_nodes += 1;
            note_line(&mut health, node.start_position().row);
        }

        // Descend only into subtrees that contain an error. Don't descend into
        // an ERROR node itself: its children are the unparseable tokens and
        // would inflate the count.
        if node.has_error() && !node.is_error() && cursor.goto_first_child() {
            continue;
        }

        // Next sibling, or climb until one exists.
        loop {
            if cursor.goto_next_sibling() {
                break;
            }
            if !cursor.goto_parent() {
                break 'walk;
            }
        }
    }

    // A tree can report has_error() with nothing found above in rare cases
    // (e.g. error-cost bookkeeping on very odd recoveries). Never report such
    // a file as clean.
    if health.is_clean() {
        health.error_nodes = 1;
        health.first_error_line = Some(1);
    }
    health
}

#[inline]
fn note_line(h: &mut SyntaxHealth, row0: usize) {
    let line = row0 + 1;
    h.first_error_line = Some(h.first_error_line.map_or(line, |l| l.min(line)));
}
