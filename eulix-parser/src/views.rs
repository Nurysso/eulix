// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use libeulix::struc::kb_struct::{
    CallGraphEdge, CallGraphNode, Class, EntryPoint, ExternalDependency, FileData, Function,
    IndicesView, KnowledgeBase, PatternInfo,
};
use serde::ser::{SerializeStruct, Serializer};
use serde::Serialize;
use std::collections::HashMap;

//borrow the full types, emit only the simplified fields.
pub struct FileDataSimpleView<'a>(pub &'a FileData);
pub struct FunctionSimpleView<'a>(pub &'a Function);
pub struct ClassSimpleView<'a>(pub &'a Class);

impl<'a> Serialize for FunctionSimpleView<'a> {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        let f = self.0;
        let mut st = s.serialize_struct("FunctionSimple", 8)?;
        st.serialize_field("id", &f.id)?;
        st.serialize_field("name", &f.name)?;
        st.serialize_field("signature", &f.signature)?;
        st.serialize_field("params", &f.params)?;
        st.serialize_field("return_type", &f.return_type)?;
        st.serialize_field("docstring", &f.docstring)?;
        st.serialize_field("line_start", &f.line_start)?;
        st.serialize_field("line_end", &f.line_end)?;
        // st.serialize_field("tags", &f.tags)?;
        // "complexity" removed
        // "importance_score" removed
        // "variables" removed
        st.end()
    }
}

struct SeqView<'a, T, V>(&'a [T], fn(&'a T) -> V);
impl<'a, T, V: Serialize> Serialize for SeqView<'a, T, V> {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.collect_seq(self.0.iter().map(|x| (self.1)(x)))
    }
}

impl<'a> Serialize for ClassSimpleView<'a> {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        let c = self.0;
        let mut st = s.serialize_struct("ClassSimple", 9)?;
        st.serialize_field("id", &c.id)?;
        st.serialize_field("name", &c.name)?;
        st.serialize_field("bases", &c.bases)?;
        st.serialize_field("docstring", &c.docstring)?;
        st.serialize_field("line_start", &c.line_start)?;
        st.serialize_field("line_end", &c.line_end)?;
        st.serialize_field("methods", &SeqView(&c.methods, FunctionSimpleView))?;
        st.serialize_field("attributes", &c.attributes)?;
        st.serialize_field("decorators", &c.decorators)?;
        // st.serialize_field("lang_info", &c.lang_info)?;
        st.end()
    }
}

impl<'a> Serialize for FileDataSimpleView<'a> {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        let d = self.0;
        let mut st = s.serialize_struct("FileDataSimple", 5)?;
        st.serialize_field("language", &d.language)?;
        st.serialize_field("loc", &d.loc)?;
        // "imports" removed
        st.serialize_field("functions", &SeqView(&d.functions, FunctionSimpleView))?;
        st.serialize_field("classes", &SeqView(&d.classes, ClassSimpleView))?;
        st.serialize_field("todos", &d.todos)?;
        st.end()
    }
}

impl<'a> CallGraphRef<'a> {
    pub fn new(kb: &'a KnowledgeBase, indices: &IndicesView<'a>) -> Self {
        let mut tags: HashMap<&'a str, &'a [String]> = HashMap::new();
        for fd in kb.structure.values() {
            let methods = fd.classes.iter().flat_map(|c| c.methods.iter());
            for f in fd.functions.iter().chain(methods) {
                if !f.tags.is_empty() {
                    tags.insert(f.id.as_str(), f.tags.as_slice());
                }
            }
        }

        let mut file_categories: HashMap<&'a str, &'a str> = HashMap::new();
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

#[derive(Serialize)]
pub struct IndexViewRef<'a> {
    pub indices: &'a IndicesView<'a>,
}

#[derive(Serialize)]
pub struct CallGraphRef<'a> {
    pub nodes: &'a [CallGraphNode],
    pub edges: &'a [CallGraphEdge],
    /// node id -> tags
    #[serde(skip_serializing_if = "HashMap::is_empty")]
    pub tags: HashMap<&'a str, &'a [String]>,
    /// file path -> category
    #[serde(skip_serializing_if = "HashMap::is_empty")]
    pub file_categories: HashMap<&'a str, &'a str>,
}

//kb_call_graph.json
#[derive(Serialize)]
pub struct EntryPointsRef<'a> {
    pub entry_points: &'a [EntryPoint],
}

/// kb_external_deps.json
#[derive(Serialize)]
pub struct ExternalDepsRef<'a> {
    pub external_dependencies: &'a [ExternalDependency],
}

/// kb_patterns.json
#[derive(Serialize)]
pub struct PatternsRef<'a> {
    pub patterns: &'a PatternInfo,
}
