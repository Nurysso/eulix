// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use libeulix::struc::kb_struct::{CallGraphEdge, CallGraphNode, IndicesView, KnowledgeBase};
use std::collections::BTreeMap;

impl<'a> CallGraphRef<'a> {
    pub fn new(kb: &'a KnowledgeBase, indices: &IndicesView<'a>) -> Self {
        let mut tags: BTreeMap<&'a str, &'a [String]> = BTreeMap::new();
        for fd in kb.structure.values() {
            let methods = fd.classes.iter().flat_map(|c| c.methods.iter());
            for f in fd.functions.iter().chain(methods) {
                if !f.tags.is_empty() {
                    tags.insert(f.id.as_str(), f.tags.as_slice());
                }
            }
        }

        let mut file_categories: BTreeMap<&'a str, &'a str> = BTreeMap::new();
        for (cat, files) in &indices.files_by_category {
            for file in files {
                file_categories.insert(*file, *cat);
            }
        }

        Self {
            nodes: &kb.call_graph.nodes,
            edges: &kb.call_graph.edges,
            tags,
            file_categories,
        }
    }
}

/// Borrowed parts of kb_call_graph.fb (libeulix::fb_io::encode_call_graph).
pub struct CallGraphRef<'a> {
    pub nodes: &'a [CallGraphNode],
    pub edges: &'a [CallGraphEdge],
    /// node id -> tags
    pub tags: BTreeMap<&'a str, &'a [String]>,
    /// file path -> category
    pub file_categories: BTreeMap<&'a str, &'a str>,
}
