// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

pub mod prism_v1;
pub mod prism_v2;

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use crate::analyze::call_graph::prism_v1::build_call_graph;
    use crate::analyze::call_graph::prism_v2::build_call_graph_v2;
    use crate::grammar_files::utils::NESTED_TAG;
    use crate::struc::kb_struct::{Class, FileData, Function, FunctionCall};

    fn func(id: &str, name: &str, calls: Vec<&str>) -> Function {
        Function {
            id: id.to_string(),
            name: name.to_string(),
            calls: calls
                .into_iter()
                .map(|c| FunctionCall {
                    receiver: None,
                    callee: c.to_string(),
                    defined_in: None,
                    line: 1,
                    args: vec![],
                    is_conditional: false,
                    context: "unconditional".to_string(),
                })
                .collect(),
            ..Default::default()
        }
    }

    fn filedata(functions: Vec<Function>, classes: Vec<Class>) -> FileData {
        FileData {
            functions,
            classes,
            ..Default::default()
        }
    }

    #[test]
    fn empty_project_produces_empty_graph_both_versions() {
        let structure: BTreeMap<String, FileData> = BTreeMap::new();
        let g1 = build_call_graph(&structure);
        let g2 = build_call_graph_v2(&structure);
        assert!(g1.nodes.is_empty() && g1.edges.is_empty());
        assert!(g2.nodes.is_empty() && g2.edges.is_empty());
    }

    #[test]
    fn self_recursive_function_produces_a_self_edge() {
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.c".to_string(),
            filedata(vec![func("func_fact::a.c", "fact", vec!["fact"])], vec![]),
        );
        let g = build_call_graph_v2(&structure);
        assert!(g
            .edges
            .iter()
            .any(|e| e.from == "func_fact::a.c" && e.to == "func_fact::a.c"));
    }

    #[test]
    fn colliding_function_ids_across_files_silently_dedupe_to_one_node() {
        // In practice ids embed the file path so this shouldn't happen,
        // but IF an upstream parser ever produces two identical ids
        // (e.g. two files sharing a generated/templated id), node_map's
        // `insert` keeps whichever came first and the second is dropped
        // without any warning or error.
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.c".to_string(),
            filedata(vec![func("func_dup::x", "dup", vec![])], vec![]),
        );
        structure.insert(
            "b.c".to_string(),
            filedata(vec![func("func_dup::x", "dup", vec![])], vec![]),
        );
        let g = build_call_graph_v2(&structure);
        let dup_nodes: Vec<_> = g.nodes.iter().filter(|n| n.id == "func_dup::x").collect();
        assert_eq!(
            dup_nodes.len(),
            1,
            "colliding ids silently collapse into a single node"
        );
    }

    #[test]
    fn ambiguous_short_name_resolves_to_some_candidate_nondeterministically() {
        // Two functions named "run" in different files. An unqualified
        // call to "run" resolves via symbol_index, which is populated
        // by iterating an unordered HashMap -- which candidate wins is
        // unspecified and can differ between runs/builds.
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.c".to_string(),
            filedata(vec![func("func_run::a.c", "run", vec![])], vec![]),
        );
        structure.insert(
            "b.c".to_string(),
            filedata(vec![func("func_run::b.c", "run", vec![])], vec![]),
        );
        structure.insert(
            "caller.c".to_string(),
            filedata(
                vec![func("func_caller::caller.c", "caller", vec!["run"])],
                vec![],
            ),
        );
        let g = build_call_graph_v2(&structure);
        let edge = g.edges.iter().find(|e| e.from == "func_caller::caller.c");
        assert!(
            edge.is_some(),
            "should resolve to *some* run — just not a guaranteed-stable one"
        );
    }

    #[test]
    fn call_to_nonexistent_function_produces_no_edge_and_no_panic() {
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.c".to_string(),
            filedata(
                vec![func(
                    "func_caller::a.c",
                    "caller",
                    vec!["totally_missing_fn"],
                )],
                vec![],
            ),
        );
        let g = build_call_graph_v2(&structure);
        assert!(g.edges.is_empty());
        assert_eq!(g.nodes.len(), 1);
    }

    #[test]
    fn v1_par_chunks_boundary_at_exactly_chunk_size() {
        // CHUNK_SIZE = 2000 in build_call_graph; verify n-1, n, n+1 all
        // preserve exactly the right node count (off-by-one bugs in
        // manual chunking are a classic source of silently dropped data).
        for n in [1999usize, 2000, 2001] {
            let mut structure = BTreeMap::new();
            for i in 0..n {
                structure.insert(
                    format!("f{i}.c"),
                    filedata(
                        vec![func(
                            &format!("func_f{i}::f{i}.c"),
                            &format!("f{i}"),
                            vec![],
                        )],
                        vec![],
                    ),
                );
            }
            let g = build_call_graph(&structure);
            assert_eq!(
                g.nodes.len(),
                n,
                "chunk boundary n={n} lost or duplicated nodes"
            );
        }
    }

    #[test]
    fn inheritance_to_an_unregistered_base_class_is_skipped_not_panicked() {
        // Base class reference to something that was never parsed as a
        // node (external/third-party base) must not create a dangling
        // edge or panic.
        let mut structure = BTreeMap::new();
        let cls = Class {
            id: "class_Child::a.c".to_string(),
            name: "Child".to_string(),
            bases: vec!["ExternalLibBase".to_string()],
            ..Default::default()
        };
        structure.insert("a.c".to_string(), filedata(vec![], vec![cls]));
        let g = build_call_graph_v2(&structure);
        assert!(g.edges.iter().all(|e| e.edge_type != "inheritance"));
    }

    #[test]
    fn duplicate_calls_on_the_v2_path_are_deduped_per_source_node() {
        // v2 uses a `seen: HashSet<(from, to)>` per chunk, so calling the
        // same function twice from the same caller produces one edge,
        // not two -- verify that intentional behavior explicitly.
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.c".to_string(),
            filedata(
                vec![func("func_caller::a.c", "caller", vec!["helper", "helper"])],
                vec![],
            ),
        );
        structure.insert(
            "b.c".to_string(),
            filedata(vec![func("func_helper::b.c", "helper", vec![])], vec![]),
        );
        let g = build_call_graph_v2(&structure);
        let count = g
            .edges
            .iter()
            .filter(|e| e.from == "func_caller::a.c" && e.to == "func_helper::b.c")
            .count();
        assert_eq!(
            count, 1,
            "v2's `seen` set collapses repeated calls to the same callee into one edge"
        );
    }

    #[test]
    fn duplicate_declarations_of_one_id_in_a_file_share_one_edge_per_target() {
        // `#ifdef` branches can declare the same function twice in one file;
        // both declarations are one node, so they get one edge per target.
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.c".to_string(),
            filedata(
                vec![
                    func("func_f::a.c", "f", vec!["g"]),
                    func("func_f::a.c", "f", vec!["g"]),
                    func("func_g::a.c", "g", vec![]),
                ],
                vec![],
            ),
        );
        let g = build_call_graph_v2(&structure);
        let count = g
            .edges
            .iter()
            .filter(|e| e.from == "func_f::a.c" && e.to == "func_g::a.c")
            .count();
        assert_eq!(count, 1);
    }

    #[test]
    fn nested_function_is_only_visible_to_its_parent() {
        let mut nested = func("func_outer.helper::a.rs", "helper", vec![]);
        nested.tags.push(NESTED_TAG.to_string());
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.rs".to_string(),
            filedata(
                vec![
                    nested,
                    func("func_outer::a.rs", "outer", vec!["helper"]),
                    func("func_helper::a.rs", "helper", vec![]),
                    func("func_main::a.rs", "main", vec!["helper"]),
                ],
                vec![],
            ),
        );
        let g = build_call_graph_v2(&structure);
        let to = |from: &str| -> Vec<&str> {
            g.edges
                .iter()
                .filter(|e| e.from == from)
                .map(|e| e.to.as_str())
                .collect()
        };
        assert_eq!(to("func_outer::a.rs"), ["func_outer.helper::a.rs"]);
        assert_eq!(to("func_main::a.rs"), ["func_helper::a.rs"]);
    }

    fn nested(id: &str, name: &str) -> Function {
        let mut f = func(id, name, vec![]);
        f.tags.push(NESTED_TAG.to_string());
        f
    }

    #[test]
    fn nested_function_is_not_visible_from_other_files() {
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.rs".to_string(),
            filedata(vec![nested("func_outer.helper::a.rs", "helper")], vec![]),
        );
        structure.insert(
            "b.rs".to_string(),
            filedata(
                vec![func("func_main::b.rs", "main", vec!["helper"])],
                vec![],
            ),
        );
        let g = build_call_graph_v2(&structure);
        assert!(!g.edges.iter().any(|e| e.from == "func_main::b.rs"));
    }

    #[test]
    fn function_nested_in_a_method_is_a_function_called_by_that_method() {
        let class = Class {
            id: "struct_S::a.rs".to_string(),
            name: "S".to_string(),
            methods: vec![func("method_S_m::a.rs", "m", vec!["h"])],
            ..Default::default()
        };
        let mut structure = BTreeMap::new();
        structure.insert(
            "a.rs".to_string(),
            filedata(vec![nested("method_S_m.h::a.rs", "h")], vec![class]),
        );
        let g = build_call_graph_v2(&structure);
        assert!(g
            .edges
            .iter()
            .any(|e| e.from == "method_S_m::a.rs" && e.to == "method_S_m.h::a.rs"));
        let h = g.nodes.iter().find(|n| n.id == "method_S_m.h::a.rs");
        assert_eq!(h.map(|n| n.node_type.as_str()), Some("function"));
    }
}
