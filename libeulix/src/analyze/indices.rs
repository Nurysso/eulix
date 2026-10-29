// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::analyze::summary::classify_file;
use crate::grammar_files::utils::NESTED_TAG;
use crate::struc::kb_struct::{IndicesView, KnowledgeBase};

pub fn generate_indices_view(kb: &KnowledgeBase) -> IndicesView<'_> {
    let mut ix = IndicesView::default();
    // Path order, so each list in the view is the same on every run.
    let mut files: Vec<_> = kb.structure.iter().collect();
    files.sort_unstable_by(|a, b| a.0.cmp(b.0));
    for (path, fd) in files {
        let cat = classify_file(path.as_str(), fd);
        ix.files_by_category
            .entry(cat)
            .or_default()
            .push(path.as_str());

        for f in &fd.functions {
            if !f.tags.iter().any(|t| t.as_str() == NESTED_TAG) {
                ix.functions_by_name
                    .entry(f.name.as_str())
                    .or_default()
                    .push(format!("{}:{}", path, f.line_start));
            }
            for t in &f.tags {
                ix.functions_by_tag
                    .entry(t.as_str())
                    .or_default()
                    .push(f.id.as_str());
            }
            for c in &f.calls {
                ix.functions_calling
                    .entry(c.callee.as_str())
                    .or_default()
                    .push(f.id.as_str());
            }
        }
        for c in &fd.classes {
            ix.types_by_name
                .entry(c.name.as_str())
                .or_default()
                .push(format!("{}:{}", path, c.line_start));
            for m in &c.methods {
                ix.functions_by_name
                    .entry(m.name.as_str())
                    .or_default()
                    .push(format!("{}:{}", path, m.line_start));
                for t in &m.tags {
                    ix.functions_by_tag
                        .entry(t.as_str())
                        .or_default()
                        .push(m.id.as_str());
                }
                for call in &m.calls {
                    ix.functions_calling
                        .entry(call.callee.as_str())
                        .or_default()
                        .push(m.id.as_str());
                }
            }
        }
    }
    ix
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::struc::kb_struct::{Class, FileData, Function, FunctionCall};
    use std::collections::HashMap;

    fn kb(structure: HashMap<String, FileData>) -> KnowledgeBase {
        KnowledgeBase {
            metadata: crate::struc::kb_struct::Metadata {
                version: "1.0".to_string(),
                git_hash: "".to_string(),
                project_name: "test".to_string(),
                project_hash: "".to_string(),
                parsed_at: "".to_string(),
                languages: vec![],
                total_files: 1,
                total_loc: 20,
                total_functions: 0,
                total_classes: 1,
                total_methods: 1,
            },
            structure,
            call_graph: Default::default(),
            dependency_graph: Default::default(),
            indices: Default::default(),
            entry_points: vec![],
            external_dependencies: vec![],
            patterns: Default::default(),
        }
    }

    #[test]
    fn test_generate_indices_view_files_by_category_and_method_calls() {
        let mut structure = HashMap::new();

        let mut class = Class {
            id: "src/api_handler.py::Handler".to_string(),
            name: "Handler".to_string(),
            line_start: 10,
            ..Default::default()
        };

        let method = Function {
            id: "src/api_handler.py::Handler.handle".to_string(),
            name: "handle".to_string(),
            line_start: 15,
            calls: vec![FunctionCall {
                callee: "process_request".to_string(),
                defined_in: None,
                line: 16,
                args: vec![],
                is_conditional: false,
                context: "unconditional".to_string(),
            }],
            ..Default::default()
        };
        class.methods.push(method);

        let fd = FileData {
            classes: vec![class],
            ..Default::default()
        };
        structure.insert("src/api_handler.py".to_string(), fd);

        let kb = kb(structure);

        let view = generate_indices_view(&kb);

        assert_eq!(
            view.files_by_category.get("API"),
            Some(&vec!["src/api_handler.py"])
        );
        assert_eq!(
            view.functions_calling.get("process_request"),
            Some(&vec!["src/api_handler.py::Handler.handle"])
        );
    }

    #[test]
    fn nested_functions_stay_out_of_functions_by_name() {
        let nested = Function {
            id: "func_outer.helper::a.rs".to_string(),
            name: "helper".to_string(),
            tags: vec![NESTED_TAG.to_string()],
            ..Default::default()
        };
        let top = Function {
            id: "func_helper::a.rs".to_string(),
            name: "helper".to_string(),
            line_start: 9,
            ..Default::default()
        };
        let mut structure = HashMap::new();
        structure.insert(
            "a.rs".to_string(),
            FileData {
                functions: vec![nested, top],
                ..Default::default()
            },
        );
        let kb = kb(structure);
        let view = generate_indices_view(&kb);
        assert_eq!(
            view.functions_by_name.get("helper"),
            Some(&vec!["a.rs:9".to_string()])
        );
    }
}
