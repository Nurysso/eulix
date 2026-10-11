//! PRISM v1 — Polyglot Resolution via Inverted Symbol Map.
//!
//! This module builds an approximate call graph from already‑parsed code.
//! It is deliberately fast and memory‑light, at the cost of precision.
//! The core idea is an *inverted symbol map*: a short‑name → node‑index
//! lookup that turns expensive O(n) scans into O(1) hash lookups.
//!
//! v1 is the default path. A more precise, type‑aware implementation
//! lives in `prism_v2` and should be preferred when correctness matters
//! more than speed.

use crate::struc::kb_struct::{
    CallGraph, CallGraphEdge, CallGraphNode, CallerInfo, FileData, KnowledgeBase,
};
use rayon::prelude::*;
use std::collections::HashMap;

/// Internal, compact representation of a node.
///
/// Kept separate from the public `CallGraphNode`/`CallGraphEdge` types so
/// the hot build loop can stay cheap (u8 tags, no `String` allocations)
/// and only pay for the friendlier public representation once, at the end.
#[derive(Clone)]
struct CompactNode {
    id: String,
    node_type: u8, // 0 = function, 1 = method, 2 = class
    file_idx: usize,
    is_entry: bool,
}

/// Builds a call graph from the already‑parsed codebase.
///
/// # Why this exists as a separate, simpler pass from v2
/// Precise call resolution (type‑aware, scope‑aware) is expensive to
/// compute and mostly unnecessary for the common case of "give me a
/// rough map of what calls what." This version optimizes for speed and
/// low memory over precision, and is the default path.
///
/// # Why a single global `symbol_index` fallback
/// A proper points‑to analysis needs type information that we don't have.
/// Rather than block the feature on that, we fall back to
/// "first definition with this short name wins" — it's wrong in the
/// presence of overloading/shadowing, but right often enough to be
/// useful for RAGs, and it's O(1) instead of the O(n) scan it replaced.
///
/// # Why chunked + parallel (Rayon, CHUNK_SIZE = 2000)
/// Node/edge extraction is embarrassingly parallel per‑file, and large
/// codebases make single‑threaded extraction the dominant cost. Chunking
/// bounds the amount of intermediate `Vec` growth per task instead of
/// letting Rayon spawn one task per file (which would thrash for
/// repos with tens of thousands of files).
///
/// # Why inheritance edges aren't used for call resolution here
/// Doing so correctly requires walking the class hierarchy, which is
/// exactly the extra work v2 exists to do. Recording but not using them
/// keeps this path's cost profile flat while still leaving the data
/// available to callers who want it.
pub fn build_call_graph(structure: &HashMap<String, FileData>) -> CallGraph {
    const CHUNK_SIZE: usize = 2000;

    // Node collection happens before edge resolution because every edge
    // resolver needs a complete `node_map` to look up targets against.
    // Building it lazily/interleaved would mean some calls resolve
    // differently depending on processing order.
    let structure_vec: Vec<_> = structure.iter().collect();
    let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

    let all_nodes: Vec<(String, CompactNode)> = chunks
        .par_iter()
        .flat_map(|chunk| {
            // Pre-sized with a rough fan‑out estimate (functions + classes
            // + methods per file) so pushes don't repeatedly reallocate.
            let mut local_nodes = Vec::with_capacity(chunk.len() * 10);
            for (_filepath, filedata) in chunk.iter() {
                for func in &filedata.functions {
                    local_nodes.push((
                        func.id.clone(),
                        CompactNode {
                            id: func.id.clone(),
                            node_type: if func.id.starts_with("method_") { 1 } else { 0 },
                            file_idx: 0,
                            is_entry: func.tags.contains(&"entry-point".to_string()),
                        },
                    ));
                }
                for class in &filedata.classes {
                    local_nodes.push((
                        class.id.clone(),
                        CompactNode {
                            id: class.id.clone(),
                            node_type: 2,
                            file_idx: 0,
                            is_entry: false,
                        },
                    ));
                    for method in &class.methods {
                        local_nodes.push((
                            method.id.clone(),
                            CompactNode {
                                id: method.id.clone(),
                                node_type: 1,
                                file_idx: 0,
                                is_entry: false,
                            },
                        ));
                    }
                }
            }
            local_nodes
        })
        .collect();

    // Deduplication is done serially, after the parallel collection,
    // because a shared map would need locking during the hot parallel
    // loop above — cheaper to dedupe once at the end than to
    // synchronize on every insert.
    let mut node_map: HashMap<String, usize> = HashMap::with_capacity(all_nodes.len());
    let mut unique_nodes: Vec<CompactNode> = Vec::with_capacity(all_nodes.len());

    for (id, node) in all_nodes {
        if node_map.insert(id.clone(), unique_nodes.len()).is_none() {
            unique_nodes.push(node);
        }
    }

    // This index exists purely to turn indirect/short‑name call
    // resolution from a linear scan over every node into a hash lookup —
    // without it, resolving calls in a large codebase was measured to
    // take on the order of tens of minutes.
    let mut symbol_index: HashMap<&str, usize> = HashMap::with_capacity(node_map.len());
    for (id, &idx) in node_map.iter() {
        let short_name = id
            .split("::")
            .last()
            .and_then(|s| s.strip_prefix("method_"))
            .unwrap_or(id);

        // First‑wins is a deliberate simplification (see module‑level
        // docs): correctness would require type info we don't have yet.
        symbol_index.entry(short_name).or_insert(idx);
    }

    // File index tracking is stubbed out here (always 0) because v1
    // doesn't need per‑node file attribution for anything downstream —
    // v2 does the real bookkeeping where it matters.
    let file_list: Vec<String> = structure.keys().cloned().collect();
    let _file_map: HashMap<String, usize> = file_list
        .iter()
        .enumerate()
        .map(|(i, f)| (f.clone(), i))
        .collect();

    // Edges are stored as plain tuples instead of a struct so this stays
    // Copy and cheap to push across threads without extra indirection.
    type CompactEdge = (usize, usize, u8, bool, usize);

    let all_edges: Vec<CompactEdge> = chunks
        .par_iter()
        .flat_map(|chunk| {
            let mut local_edges = Vec::new();

            for (_, filedata) in chunk.iter() {
                // Pulled out as a closure so the two‑tier lookup logic
                // isn't duplicated across the function/method call sites
                // below.
                let resolve = |callee: &str| -> Option<usize> {
                    // Exact match first: it's unambiguous and avoids
                    // paying for the short‑name fallback on the common
                    // case where the parser already fully qualified the
                    // callee.
                    if let Some(&idx) = node_map.get(callee) {
                        return Some(idx);
                    }
                    resolve_indirect_call(callee, &node_map, &symbol_index)
                };

                for func in &filedata.functions {
                    if let Some(&from_idx) = node_map.get(&func.id) {
                        for call in &func.calls {
                            if let Some(to_idx) = resolve(&call.callee) {
                                local_edges.push((
                                    from_idx,
                                    to_idx,
                                    0,
                                    call.is_conditional,
                                    call.line,
                                ));
                            }
                        }
                    }
                }

                for class in &filedata.classes {
                    if let Some(&from_idx) = node_map.get(&class.id) {
                        for base in &class.bases {
                            if let Some(&to_idx) = node_map.get(base) {
                                local_edges.push((from_idx, to_idx, 1, false, class.line_start));
                            }
                        }
                        for method in &class.methods {
                            if let Some(&m_from_idx) = node_map.get(&method.id) {
                                for call in &method.calls {
                                    if let Some(to_idx) = resolve(&call.callee) {
                                        local_edges.push((
                                            m_from_idx,
                                            to_idx,
                                            0,
                                            call.is_conditional,
                                            call.line,
                                        ));
                                    }
                                }
                            }
                        }
                    }
                }
            }
            local_edges
        })
        .collect();

    // Call‑count estimates are derived from edges rather than tracked
    // incrementally during edge extraction, since the parallel workers
    // above don't share mutable state — a single serial pass here is
    // simpler and fast enough given edges are already materialized.
    let mut counts = vec![0; unique_nodes.len()];
    for (_, to_idx, _, _, _) in &all_edges {
        if let Some(count) = counts.get_mut(*to_idx) {
            *count += 1;
        }
    }

    // Conversion to the public node type happens last so the hot loops
    // above never touch `CallGraphNode`'s heavier String fields.
    let final_nodes: Vec<CallGraphNode> = unique_nodes
        .into_iter()
        .enumerate()
        .map(|(i, node)| {
            let file_path = file_list
                .get(node.file_idx)
                .cloned()
                .unwrap_or_else(|| "unknown".to_string());

            CallGraphNode {
                id: node.id,
                node_type: match node.node_type {
                    0 => "function".to_string(),
                    1 => "method".to_string(),
                    _ => "class".to_string(),
                },
                file: file_path,
                is_entry_point: node.is_entry,
                call_count_estimate: counts[i],
            }
        })
        .collect();

    // Edges are filtered against `node_count` defensively: nothing in
    // this function should produce an out‑of‑range index, but a bad
    // index here would panic on the `final_nodes[..]` access below, so
    // we guard rather than trust invariants across the whole pipeline.
    let node_count = final_nodes.len();
    let final_edges: Vec<CallGraphEdge> = all_edges
        .into_iter()
        .filter(|(from_idx, to_idx, _, _, _)| *from_idx < node_count && *to_idx < node_count)
        .map(|(from_idx, to_idx, kind, cond, line)| CallGraphEdge {
            from: final_nodes[from_idx].id.clone(),
            to: final_nodes[to_idx].id.clone(),
            edge_type: if kind == 1 {
                "inheritance".to_string()
            } else {
                "call".to_string()
            },
            conditional: cond,
            call_site_line: line,
        })
        .collect();

    CallGraph {
        nodes: final_nodes,
        edges: final_edges,
    }
}

/// Two‑tier resolver used by v1.
///
/// Exact match is tried first because it's unambiguous; the short‑name
/// fallback only exists because the alternative was a full O(n) scan of
/// every node for every unresolved call, which was the actual bottleneck
/// this function was written to remove. A real implementation would use
/// receiver‑type/virtual‑dispatch information instead of a name‑only
/// heuristic — left as future work rather than blocking this feature on
/// building a type system for every supported language.
pub fn resolve_indirect_call(
    callee: &str,
    node_map: &HashMap<String, usize>,
    symbol_index: &HashMap<&str, usize>,
) -> Option<usize> {
    if let Some(&idx) = node_map.get(callee) {
        return Some(idx);
    }

    let base_name = callee
        .split("::")
        .next()
        .and_then(|s: &str| s.strip_prefix("method_").or(Some(s)))
        .unwrap_or(callee);

    symbol_index.get(base_name).copied()
}

/// Resolve where called functions are defined, and record their resolved ID.
///
/// Must run before `populate_called_by`: that function keys on resolved
/// IDs, so if this hasn't run yet it would key on raw names instead and
/// reintroduce the name‑collision problem it's designed to avoid.
/// First‑definition‑wins for ambiguous names is a conscious partial
/// answer rather than skipping resolution entirely — an approximate
/// caller list is more useful than none.
pub fn resolve_call_locations(kb: &mut KnowledgeBase) {
    const CHUNK_SIZE: usize = 1000;
    let structure_vec: Vec<_> = kb.structure.iter().collect();
    let func_info: HashMap<String, (String, String)> = structure_vec
        .par_chunks(CHUNK_SIZE)
        .map(|chunk| {
            let mut local: HashMap<String, (String, String)> = HashMap::default();
            for (filepath, filedata) in chunk {
                for func in &filedata.functions {
                    local
                        .entry(func.name.clone())
                        .or_insert_with(|| (filepath.to_string(), func.id.clone()));
                }
                for class in &filedata.classes {
                    for method in &class.methods {
                        local
                            .entry(method.name.clone())
                            .or_insert_with(|| (filepath.to_string(), method.id.clone()));
                    }
                }
            }
            local
        })
        // `reduce` (not a plain merge) is used because each parallel
        // chunk can independently claim the same name; the fold here
        // preserves "first insert wins" semantics across chunk
        // boundaries too, keeping the result independent of how Rayon
        // happened to partition the work.
        .reduce(HashMap::default, |mut a, b| {
            for (k, v) in b {
                a.entry(k).or_insert(v);
            }
            a
        });

    for filedata in kb.structure.values_mut() {
        for func in &mut filedata.functions {
            for call in &mut func.calls {
                if let Some((file, id)) = func_info.get(&call.callee) {
                    call.defined_in = Some(file.clone());
                    call.callee = id.clone();
                }
            }
        }
        for class in &mut filedata.classes {
            for method in &mut class.methods {
                for call in &mut method.calls {
                    if let Some((file, id)) = func_info.get(&call.callee) {
                        call.defined_in = Some(file.clone());
                        call.callee = id.clone();
                    }
                }
            }
        }
    }
}

/// Populate called_by fields in functions (reverse call graph).
///
/// Keys on the *resolved* callee ID rather than the raw call‑site text,
/// specifically so two unrelated functions with the same name (in
/// different files/classes) don't get merged into one caller list —
/// that was the failure mode this was written to avoid.
pub fn populate_called_by(kb: &mut KnowledgeBase) {
    const CHUNK_SIZE: usize = 1000;

    let structure_vec: Vec<_> = kb.structure.iter().collect();
    let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

    let all_calls: Vec<(String, CallerInfo)> = chunks
        .par_iter()
        .flat_map(|chunk| {
            let mut local = Vec::new();

            for (filepath, filedata) in chunk.iter() {
                for func in &filedata.functions {
                    for call in &func.calls {
                        let key = call.callee.clone();
                        local.push((
                            key,
                            CallerInfo {
                                // caller's ID, not name — same collision‑avoidance
                                // reasoning as the key above.
                                function: func.id.clone(),
                                file: filepath.to_string(),
                                line: call.line,
                            },
                        ));
                    }
                }

                for class in &filedata.classes {
                    for method in &class.methods {
                        for call in &method.calls {
                            let key = call.callee.clone();
                            local.push((
                                key,
                                CallerInfo {
                                    function: method.id.clone(),
                                    file: filepath.to_string(),
                                    line: call.line,
                                },
                            ));
                        }
                    }
                }
            }

            local
        })
        .collect();

    // Built once and applied in a second pass rather than mutating
    // `kb.structure` while iterating it — Rust's borrow rules aside,
    // this also means every function sees the complete caller set
    // regardless of iteration order.
    let mut reverse: HashMap<String, Vec<CallerInfo>> = HashMap::new();
    for (callee, caller_info) in all_calls {
        reverse.entry(callee).or_default().push(caller_info);
    }

    for filedata in kb.structure.values_mut() {
        for func in &mut filedata.functions {
            if let Some(callers) = reverse.get(&func.id) {
                func.called_by = callers.clone();
            }
        }
        for class in &mut filedata.classes {
            for method in &mut class.methods {
                if let Some(callers) = reverse.get(&method.id) {
                    method.called_by = callers.clone();
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    // #![expect(clippy::expect_used)]
    // #![expect(clippy::unwrap_used)]

    use super::*;
    mod resolve_indirect_call_tests {
        use super::*;
        use std::collections::HashMap;

        #[test]
        fn direct_hit_wins_over_symbol_index() {
            let mut node_map = HashMap::new();
            node_map.insert("func_foo::a.c".to_string(), 0usize);
            let mut symbol_index: HashMap<&str, usize> = HashMap::new();
            symbol_index.insert("foo", 99); // deliberately wrong to prove precedence
            let res = resolve_indirect_call("func_foo::a.c", &node_map, &symbol_index);
            assert_eq!(res, Some(0));
        }

        #[test]
        fn base_name_extraction_keeps_the_whole_suffix_after_method_() {
            // base_name = split("::").last() -> strip "method_" prefix only.
            // For "method_bar_baz::x.c" the base name is "bar_baz", NOT "bar".
            let node_map: HashMap<String, usize> = HashMap::new();
            let mut symbol_index: HashMap<&str, usize> = HashMap::new();
            symbol_index.insert("bar", 5); // wrong key on purpose
            let res = resolve_indirect_call("method_bar_baz::x.c", &node_map, &symbol_index);
            assert_eq!(
                res, None,
                "documents that only the full post‑prefix suffix is used as the key"
            );
        }

        #[test]
        fn correct_symbol_index_key_resolves() {
            let node_map: HashMap<String, usize> = HashMap::new();
            let mut symbol_index: HashMap<&str, usize> = HashMap::new();
            symbol_index.insert("bar_baz", 5);
            let res = resolve_indirect_call("method_bar_baz::x.c", &node_map, &symbol_index);
            assert_eq!(res, Some(5));
        }

        #[test]
        fn unresolvable_returns_none_not_panic() {
            let node_map: HashMap<String, usize> = HashMap::new();
            let symbol_index: HashMap<&str, usize> = HashMap::new();
            assert_eq!(
                resolve_indirect_call("nope", &node_map, &symbol_index),
                None
            );
        }

        #[test]
        fn empty_callee_string_does_not_panic() {
            let node_map: HashMap<String, usize> = HashMap::new();
            let symbol_index: HashMap<&str, usize> = HashMap::new();
            assert_eq!(resolve_indirect_call("", &node_map, &symbol_index), None);
        }
    }
}
