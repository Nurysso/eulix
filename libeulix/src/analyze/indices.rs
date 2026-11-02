// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::analyze::summary::classify_file;
use crate::grammar_files::utils::NESTED_TAG;
use crate::struc::kb_struct::{FunctionCall, IndicesView, KnowledgeBase};
use std::collections::HashSet;

pub fn generate_indices_view(kb: &KnowledgeBase) -> IndicesView<'_> {
    let mut ix = IndicesView::default();
    for (path, fd) in &kb.structure {
        let cat = classify_file(path.as_str(), fd);
        let per_line = matches!(fd.language.as_str(), "python" | "typescript" | "rust");
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
            push_callers(&mut ix, &f.calls, f.id.as_str(), per_line);
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
                push_callers(&mut ix, &m.calls, m.id.as_str(), per_line);
            }
        }
    }
    ix
}

// The Python, TypeScript and Rust parsers record one call per receiver, so
// `a.get() + b.get()` gives two calls on one line. The index keeps listing
// those callers once per callee and line, as it did before receivers.
fn push_callers<'a>(
    ix: &mut IndicesView<'a>,
    calls: &'a [FunctionCall],
    caller: &'a str,
    per_line: bool,
) {
    let mut seen = HashSet::new();
    for c in calls {
        if !per_line || seen.insert((c.callee.as_str(), c.line)) {
            ix.functions_calling
                .entry(c.callee.as_str())
                .or_default()
                .push(caller);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::struc::kb_struct::{Class, FileData, Function, FunctionCall};
    use std::collections::BTreeMap;

    fn kb(structure: BTreeMap<String, FileData>) -> KnowledgeBase {
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
        let mut structure = BTreeMap::new();

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
                receiver: None,
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
    fn functions_calling_lists_a_caller_once_per_line() {
        let source = "def total(a, b):\n    return a.get() + b.get()\n\ndef twice(a):\n    a.get()\n    a.get()\n";
        let (_, fd) =
            crate::grammar_files::parse_source(std::path::Path::new("pair.py"), source).unwrap();
        let mut structure = BTreeMap::new();
        structure.insert("pair.py".to_string(), fd);
        let kb = kb(structure);
        let view = generate_indices_view(&kb);
        assert_eq!(
            view.functions_calling.get("get"),
            Some(&vec![
                "func_total::pair.py",
                "func_twice::pair.py",
                "func_twice::pair.py"
            ])
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
        let mut structure = BTreeMap::new();
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
