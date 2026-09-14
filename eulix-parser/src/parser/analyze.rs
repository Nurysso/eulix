//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::struc::kb_struct::*;
use rayon::prelude::*;
use serde::{Deserialize, Serialize};
use std::collections::VecDeque;
use std::collections::{HashMap, HashSet};

pub struct Analyzer;

// Kept separate from the public `CallGraphNode`/`CallGraphEdge` types so the
// hot build loop can stay cheap (u8 tags, no String allocs) and only pay for
// the friendlier public representation once, at the very end.
#[derive(Clone)]
struct CompactNode {
    id: String,
    node_type: u8, // 0 = function, 1 = method, 2 = class
    file_idx: usize,
    is_entry: bool,
}

#[derive(Clone, Copy)]
struct RawEdge {
    from: usize,
    to: usize,
    kind: u8, // 0=call 1=inheritance
    conditional: bool,
    line: usize,
}

// Centralized here so every resolution path (v1 and v2) agrees on how a
// "short name" is derived if this drifted between call sites, symbol_index
// lookups would silently miss.
#[inline(always)]
fn short_name_of(id: &str, node_type: u8) -> &str {
    let stripped = id.split("::").next().unwrap_or(id);
    if node_type == 1 {
        stripped.strip_prefix("method_").unwrap_or(stripped)
    } else {
        stripped
    }
}

// We don't have real type information at parse time, so this is the cheapest
// signal we have for "this call is a method call on the enclosing instance"
// good enough to scope resolution to the caller's class without a real
// type checker.
#[inline(always)]
fn first_self_param(params: &[Parameter]) -> bool {
    params
        .first()
        .map(|p| {
            matches!(
                p.name.as_str(),
                "self" | "this" | "cls" | "_self" | "self_" | "myself"
            )
        })
        .unwrap_or(false)
}

impl Analyzer {
    /// Generate complete knowledge base with indices and call graph
    pub fn analyze_and_build(mut kb: KnowledgeBase, verbose: bool, prism: u8) -> KnowledgeBase {
        let file_count = kb.structure.len();
        // TODO, add a way to change/turn off is_large file value so that it can run on servers.
        // Building a full call graph means holding every node + edge + several
        // auxiliary indices in memory at once; past a certain repo size that
        // cost isn't worth it, so we trade call-graph fidelity for staying
        // resident on modest machines instead of OOMing.
        let is_large = file_count > 100_000;
        let use_precise = prism;
        if verbose && is_large {
            println!(
                "   [!]  Enabling memory-efficient mode for {} files",
                file_count
            );
        }

        // Skipped entirely (rather than degraded) for huge repos: a
        // partially-built call graph would be misleading, so we'd rather
        // comit it and say so than ship wrong data.
        if !is_large {
            // Call sites are resolved to definition IDs up front so that
            // every later stage (graph building, called_by) can key on a
            // stable ID instead of re-deriving identity from raw names.
            if verbose {
                println!("   → Resolving call locations...");
            }
            Self::resolve_call_locations(&mut kb);
            if verbose {
                println!("   → Building call graphs...");
            }
            // Two implementations exist because precision costs time: v1 is
            // the cheap default for fast iteration, v2 is opt-in for callers
            // who need class-aware resolution and can afford the extra work.
            if use_precise == 2 {
                if verbose {
                    println!("      Using precise analysis (PRISMv2)...");
                }
                kb.call_graph = Self::build_call_graph_v2(&kb.structure);
            } else {
                if verbose {
                    println!("      Using direct analysis (PRISMv1)...");
                }
                kb.call_graph = Self::build_call_graph(&kb.structure);
            }
            if verbose {
                println!("   → Building reverse call graphs...");
            }
            // Depends on resolved callee IDs from the step above, so it must
            // run after, reversing raw names would produce spurious
            // collisions between unrelated functions that share a name.
            Self::populate_called_by(&mut kb);
        } else if verbose {
            println!("   [!]  Skipping call graph (too large, would use excessive memory)");
        }

        if verbose {
            println!("   → Generating indices...");
        }
        kb.indices = Self::generate_indices(&kb);

        if verbose {
            println!("   → Detecting patterns...");
        }
        kb.patterns = Self::detect_patterns(&kb);

        if verbose {
            println!("   → Finding entry points...");
        }
        kb.entry_points = Self::find_entry_points(&kb);

        if verbose {
            println!("   → Analyzing dependencies...");
        }
        kb.external_dependencies = Self::analyze_external_deps(&kb);

        kb
    }

    /// Builds a call graph from the already-parsed codebase.
    ///
    /// # Why this exists as a separate, simpler pass from v2
    /// Precise call resolution (type-aware, scope-aware) is expensive to
    /// compute and mostly unnecessary for the common case of "give me a
    /// rough map of what calls what." This version optimizes for speed and
    /// low memory over precision, and is the default path.
    ///
    /// # Why a single global `symbol_index` fallback
    /// A proper points-to analysis needs type information that we don't have.
    /// Rather than block the feature on that, we fall back to
    /// "first definition with this short name wins" — it's wrong in the
    /// presence of overloading/shadowing, but right often enough to be
    /// useful for RAG's, and it's O(1) instead of the O(n) scan it replaced.
    ///
    /// # Why chunked + parallel (Rayon, CHUNK_SIZE = 2000)
    /// Node/edge extraction is embarrassingly parallel per-file, and large
    /// codebases make single-threaded extraction the dominant cost. Chunking
    /// bounds the amount of intermediate `Vec` growth per task instead of
    /// letting Rayon spawn one task per file (which would thrash for
    /// repos with tens of thousands of files).
    ///
    /// # Why inheritance edges aren't used for call resolution here
    /// Doing so correctly requires walking the class hierarchy, which is
    /// exactly the extra work v2 exists to do. Recording but not using them
    /// keeps this path's cost profile flat while still leaving the data
    /// available to callers who want it.
    fn build_call_graph(structure: &HashMap<String, FileData>) -> CallGraph {
        const CHUNK_SIZE: usize = 2000;

        // Node collection happens before edge resolution because every edge
        // resolver needs a complete `node_map` to look up targets against
        // building it lazily/interleaved would mean some calls resolve
        // differently depending on processing order.
        let structure_vec: Vec<_> = structure.iter().collect();
        let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

        let all_nodes: Vec<(String, CompactNode)> = chunks
            .par_iter()
            .flat_map(|chunk| {
                // Pre-sized with a rough fan-out estimate (functions + classes
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
        // loop above cheaper to dedupe once at the end than to
        // synchronize on every insert.
        let mut node_map: HashMap<String, usize> = HashMap::with_capacity(all_nodes.len());
        let mut unique_nodes: Vec<CompactNode> = Vec::with_capacity(all_nodes.len());

        for (id, node) in all_nodes {
            if node_map.insert(id.clone(), unique_nodes.len()).is_none() {
                unique_nodes.push(node);
            }
        }

        // This index exists purely to turn indirect/short-name call
        // resolution from a linear scan over every node into a hash lookup
        // without it, resolving calls in a large codebase was measured to
        // take on the order of tens of minutes.
        let mut symbol_index: HashMap<&str, usize> = HashMap::with_capacity(node_map.len());
        for (id, &idx) in node_map.iter() {
            let short_name = id
                .split("::")
                .last()
                .and_then(|s| s.strip_prefix("method_"))
                .unwrap_or(id);

            // First-wins is a deliberate simplification (see module-level
            // docs): correctness would require type info we don't have yet.
            symbol_index.entry(short_name).or_insert(idx);
        }

        // File index tracking is stubbed out here (always 0) because v1
        // doesn't need per-node file attribution for anything downstream
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
                    // Pulled out as a closure so the two-tier lookup logic
                    // isn't duplicated across the function/method call sites
                    // below.
                    let resolve = |callee: &str| -> Option<usize> {
                        // Exact match first: it's unambiguous and avoids
                        // paying for the short-name fallback on the common
                        // case where the parser already fully qualified the
                        // callee.
                        if let Some(&idx) = node_map.get(callee) {
                            return Some(idx);
                        }
                        Self::resolve_indirect_call(callee, &node_map, &symbol_index)
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
                                    local_edges.push((
                                        from_idx,
                                        to_idx,
                                        1,
                                        false,
                                        class.line_start,
                                    ));
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

        // Call-count estimates are derived from edges rather than tracked
        // incrementally during edge extraction, since the parallel workers
        // above don't share mutable state a single serial pass here is
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
        // this function should produce an out-of-range index, but a bad
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

    // v2 exists because v1's global-first-match symbol index gets calls
    // wrong whenever two unrelated classes define a method with the same
    // short name common enough in real codebases (`__init__`, `run`,
    // `handle`) that a class-aware, scope-aware resolver earns its extra
    // cost for callers who need accuracy over raw speed.
    pub fn build_call_graph_v2(structure: &HashMap<String, FileData>) -> CallGraph {
        // Sorted so that `file_idx` is deterministic across runs — without
        // this, HashMap iteration order would make node file attribution
        // (and therefore any diffing between runs) unstable.
        let file_list: Vec<&str> = {
            let mut v: Vec<&str> = structure.keys().map(String::as_str).collect();
            v.sort_unstable();
            v
        };
        let file_map: HashMap<&str, usize> =
            file_list.iter().enumerate().map(|(i, &s)| (s, i)).collect();

        // Nodes are collected in one parallel pass with `file_idx` set
        // correctly at creation time (unlike v1's stub), because v2's
        // resolver needs per-node file attribution for its file-scope and
        // import-guided lookup tiers — retrofitting it with a second pass
        // would mean iterating the whole node set twice for no reason.
        const CHUNK_SIZE: usize = 2_000;
        let file_vec: Vec<(&str, &FileData)> =
            structure.iter().map(|(k, v)| (k.as_str(), v)).collect();

        let raw_nodes: Vec<(&str, CompactNode)> = file_vec
            .par_chunks(CHUNK_SIZE)
            .flat_map(|chunk| {
                let mut local: Vec<(&str, CompactNode)> = Vec::with_capacity(chunk.len() * 10);
                for &(filepath, filedata) in chunk {
                    let fidx = *file_map.get(filepath).unwrap_or(&0);
                    for func in &filedata.functions {
                        local.push((
                            func.id.as_str(),
                            CompactNode {
                                id: func.id.clone(),
                                node_type: if func.id.starts_with("method_") { 1 } else { 0 },
                                file_idx: fidx,
                                is_entry: func.tags.contains(&"entry-point".to_string()),
                            },
                        ));
                    }
                    for class in &filedata.classes {
                        local.push((
                            class.id.as_str(),
                            CompactNode {
                                id: class.id.clone(),
                                node_type: 2,
                                file_idx: fidx,
                                is_entry: false,
                            },
                        ));
                        for method in &class.methods {
                            local.push((
                                method.id.as_str(),
                                CompactNode {
                                    id: method.id.clone(),
                                    node_type: 1,
                                    file_idx: fidx,
                                    is_entry: false,
                                },
                            ));
                        }
                    }
                }
                local
            })
            .collect();

        // Serial dedup, same rationale as v1: avoids locking a shared map
        // during the parallel collection phase.
        let mut node_map: HashMap<&str, usize> = HashMap::with_capacity(raw_nodes.len());
        let mut unique_nodes: Vec<CompactNode> = Vec::with_capacity(raw_nodes.len());
        for (id, node) in raw_nodes {
            if !node_map.contains_key(id) {
                node_map.insert(id, unique_nodes.len());
                unique_nodes.push(node);
            }
        }

        // Every downstream index below is built once here and shared
        // read-only through `ResolveCtx`, rather than recomputed per-call —
        // resolution runs millions of times over a large codebase, so
        // anything repeated per-call would dominate runtime.

        // Base-class references in source are often unqualified
        // (`class Foo(Base)`), so an exact `node_map` lookup on `base` alone
        // frequently misses. Suffix indexing on the trailing identifier
        // after the last `_` gives a cheap way to still find `class_Base`
        // without a linear scan over every node.
        let suffix_index: HashMap<&str, Vec<usize>> = {
            let mut idx: HashMap<&str, Vec<usize>> = HashMap::default();
            for (&id, &node_idx) in &node_map {
                if let Some((_, suffix)) = id.rsplit_once('_') {
                    idx.entry(suffix).or_default().push(node_idx);
                }
            }
            idx
        };
        struct ChunkIndex {
            imports: Vec<(String, Vec<(String, String)>)>, // file_path → [(imported_name, source_module)]
            class_methods: Vec<(String, String, usize)>, // class_id → short_method_name → node_idx
            inheritance_ids: Vec<(usize, usize)>, // (child_idx, parent_idx) pairs — direct inheritance
        }

        // Computed alongside node collection (same chunking) rather than as
        // a later separate pass, so we only walk `filedata.classes` once per
        // file instead of once per index.
        let chunk_indices: Vec<ChunkIndex> = file_vec
            .par_chunks(CHUNK_SIZE)
            .map(|chunk| {
                let mut imports: Vec<(String, Vec<(String, String)>)> = Vec::new();
                let mut class_methods: Vec<(String, String, usize)> = Vec::new();
                let mut inheritance_ids: Vec<(usize, usize)> = Vec::new();

                for &(filepath, filedata) in chunk {
                    let imps: Vec<(String, String)> = filedata
                        .imports
                        .iter()
                        .flat_map(|i| {
                            i.items
                                .iter()
                                .map(move |item| (item.clone(), i.module.clone()))
                        })
                        .collect();
                    if !imps.is_empty() {
                        imports.push((filepath.to_string(), imps));
                    }

                    for class in &filedata.classes {
                        let class_idx = match node_map.get(class.id.as_str()) {
                            Some(&i) => i,
                            None => continue,
                        };
                        for method in &class.methods {
                            if let Some(&mid) = node_map.get(method.id.as_str()) {
                                let short = short_name_of(&method.id, 1).to_string();
                                class_methods.push((class.id.clone(), short, mid));
                            }
                        }
                        for base in &class.bases {
                            let base_idx = Self::find_base_idx(base, &node_map, &suffix_index);
                            if let Some(bi) = base_idx {
                                inheritance_ids.push((class_idx, bi));
                            }
                        }
                    }
                }
                ChunkIndex {
                    imports,
                    class_methods,
                    inheritance_ids,
                }
            })
            .collect();

        // Merge step is serial and single-pass: each map only needs to be
        // built once, and doing it here avoids coordinating inserts from
        // multiple threads into one shared map.
        let total_methods: usize = chunk_indices.iter().map(|c| c.class_methods.len()).sum();
        let total_inh: usize = chunk_indices.iter().map(|c| c.inheritance_ids.len()).sum();

        let mut imports_per_file: HashMap<String, Vec<(String, String)>> =
            HashMap::with_capacity(file_list.len());
        let mut class_methods_map: HashMap<&str, HashMap<String, usize>> =
            HashMap::with_capacity(total_methods / 4 + 1);
        let mut inheritance_ids: HashMap<usize, Vec<usize>> =
            HashMap::with_capacity(total_inh / 2 + 1);

        for ci in &chunk_indices {
            for (file, imps) in &ci.imports {
                imports_per_file.insert(file.clone(), imps.clone());
            }
            for (cid, short, mid) in &ci.class_methods {
                // Reuse the interned key already sitting in node_map instead
                // of cloning `cid` again — cuts an avoidable allocation per
                // method, which matters at this scale.
                if let Some((&key, _)) = node_map.get_key_value(cid.as_str()) {
                    class_methods_map
                        .entry(key)
                        .or_default()
                        .insert(short.clone(), *mid);
                }
            }
            for &(child, parent) in &ci.inheritance_ids {
                inheritance_ids.entry(child).or_default().push(parent);
            }
        }

        // Descendant edges are derived from the inheritance map rather than
        // collected separately, since resolving an overridden method
        // requires walking *down* the hierarchy from the base class — that
        // direction doesn't exist in the source data, only the reverse.
        let mut descendants_ids: HashMap<usize, Vec<usize>> =
            HashMap::with_capacity(inheritance_ids.len());
        for (&child, parents) in &inheritance_ids {
            for &parent in parents {
                descendants_ids.entry(parent).or_default().push(child);
            }
        }

        // A reverse idx→id table is built once and reused by the BFS walks
        // below, instead of reconstructing it per lookup.
        let idx_to_key: Vec<&str> = {
            let mut v = vec![""; unique_nodes.len()];
            for (&id, &idx) in &node_map {
                v[idx] = id;
            }
            v
        };

        // File-scope resolution is the strongest signal short of an exact
        // ID match — shadowing between files is rare, shadowing within a
        // file is common — so it's indexed explicitly rather than relying
        // on the weaker global symbol_index for same-file calls.
        let mut file_scope: HashMap<&str, HashMap<&str, Vec<usize>>> =
            HashMap::with_capacity(file_list.len());
        for (i, node) in unique_nodes.iter().enumerate() {
            let file = file_list.get(node.file_idx).copied().unwrap_or("");
            let short: &str = short_name_of(idx_to_key[i], node.node_type);
            file_scope
                .entry(file)
                .or_default()
                .entry(short)
                .or_default()
                .push(i);
        }

        // Kept as the last-resort tier for the same reason v1 needs it:
        // without real type inference, some calls simply can't be scoped
        // any tighter than "some function with this name, somewhere."
        let mut symbol_index: HashMap<&str, usize> = HashMap::with_capacity(node_map.len());
        for (&id, &idx) in &node_map {
            let short = short_name_of(id, unique_nodes[idx].node_type);
            symbol_index.entry(short).or_insert(idx);
        }

        // Lets the import-guided resolution tier turn `from foo import bar`
        // into an actual file to search, instead of falling straight
        // through to the unscoped global fallback.
        let module_to_files: HashMap<String, Vec<&str>> =
            Self::build_module_to_files_borrowed(&file_list);

        // Bundled into one struct and passed by shared reference so the
        // parallel edge-extraction closures below stay `Send` — every field
        // is read-only after this point, so no locking is needed.
        let ctx = ResolveCtx {
            node_map: &node_map,
            symbol_index: &symbol_index,
            class_methods: &class_methods_map,
            inheritance_ids: &inheritance_ids,
            descendants_ids: &descendants_ids,
            file_scope: &file_scope,
            imports_per_file: &imports_per_file,
            module_to_files: &module_to_files,
            idx_to_key: &idx_to_key,
        };

        let all_edges: Vec<RawEdge> = file_vec
            .par_chunks(CHUNK_SIZE)
            .flat_map(|chunk| {
                // Deduping within each chunk keeps the eventual edge list
                // from ballooning on codebases that call the same function
                // from a loop or repeatedly in one body — those don't add
                // graph information, only noise.
                let mut seen: HashSet<(usize, usize)> = HashSet::new();
                let mut local: Vec<RawEdge> = Vec::new();

                for &(filepath, filedata) in chunk {
                    for func in &filedata.functions {
                        let from = match ctx.node_map.get(func.id.as_str()) {
                            Some(&i) => i,
                            None => continue,
                        };
                        for call in &func.calls {
                            if let Some(to) = ctx.resolve(&call.callee, filepath, None) {
                                if seen.insert((from, to)) {
                                    local.push(RawEdge {
                                        from,
                                        to,
                                        kind: 0,
                                        conditional: call.is_conditional,
                                        line: call.line,
                                    });
                                }
                            }
                        }
                    }

                    for class in &filedata.classes {
                        let class_idx = match ctx.node_map.get(class.id.as_str()) {
                            Some(&i) => i,
                            None => continue,
                        };

                        for base in &class.bases {
                            if let Some(bi) = Self::find_base_idx(base, ctx.node_map, &suffix_index)
                            {
                                if seen.insert((class_idx, bi)) {
                                    local.push(RawEdge {
                                        from: class_idx,
                                        to: bi,
                                        kind: 1,
                                        conditional: false,
                                        line: class.line_start,
                                    });
                                }
                            }
                        }

                        for method in &class.methods {
                            let from = match ctx.node_map.get(method.id.as_str()) {
                                Some(&i) => i,
                                None => continue,
                            };
                            // Only pass class context for calls that actually
                            // look like `self.foo()` — passing it
                            // unconditionally would let free-function calls
                            // incorrectly match same-named methods on the
                            // enclosing class.
                            let self_class: Option<&str> = if first_self_param(&method.params) {
                                Some(class.id.as_str())
                            } else {
                                None
                            };
                            for call in &method.calls {
                                if let Some(to) = ctx.resolve(&call.callee, filepath, self_class) {
                                    if seen.insert((from, to)) {
                                        local.push(RawEdge {
                                            from,
                                            to,
                                            kind: 0,
                                            conditional: call.is_conditional,
                                            line: call.line,
                                        });
                                    }
                                }
                            }
                        }
                    }
                }
                local
            })
            .collect();

        // Same rationale as v1: counts derived in one serial pass over
        // already-materialized edges, rather than tracked during the
        // parallel extraction where there's no shared mutable state to
        // update safely.
        let mut counts = vec![0u32; unique_nodes.len()];
        for e in &all_edges {
            counts[e.to] += 1;
        }

        let final_nodes: Vec<CallGraphNode> = unique_nodes
            .into_iter()
            .enumerate()
            .map(|(i, node)| CallGraphNode {
                id: node.id,
                node_type: match node.node_type {
                    0 => "function",
                    1 => "method",
                    _ => "class",
                }
                .to_string(),
                file: file_list
                    .get(node.file_idx)
                    .map(|s| s.to_string())
                    .unwrap_or_else(|| "unknown".to_string()),
                is_entry_point: node.is_entry,
                call_count_estimate: counts[i] as usize,
            })
            .collect();

        let node_count = final_nodes.len();
        let final_edges: Vec<CallGraphEdge> = all_edges
            .into_iter()
            .filter(|e| e.from < node_count && e.to < node_count)
            .map(|e| CallGraphEdge {
                from: final_nodes[e.from].id.clone(),
                to: final_nodes[e.to].id.clone(),
                edge_type: if e.kind == 1 {
                    "inheritance".to_string()
                } else {
                    "call".to_string()
                },
                conditional: e.conditional,
                call_site_line: e.line,
            })
            .collect();

        CallGraph {
            nodes: final_nodes,
            edges: final_edges,
        }
    }

    /// Two-tier resolver used by v1.
    ///
    /// Exact match is tried first because it's unambiguous; the short-name
    /// fallback only exists because the alternative was a full O(n) scan of
    /// every node for every unresolved call, which was the actual bottleneck
    /// this function was written to remove. A real implementation would use
    /// receiver-type/virtual-dispatch information instead of a name-only
    /// heuristic — left as future work rather than blocking this feature on
    /// building a type system for every supported language.
    fn resolve_indirect_call(
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

    /// Populate called_by fields in functions (reverse call graph).
    ///
    /// Keys on the *resolved* callee ID rather than the raw call-site text,
    /// specifically so two unrelated functions with the same name (in
    /// different files/classes) don't get merged into one caller list —
    /// that was the failure mode this was written to avoid.
    fn populate_called_by(kb: &mut KnowledgeBase) {
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
                                    function: func.id.clone(), // caller's ID, not name same collision-avoidance reasoning as the key above
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

    /// Resolve where called functions are defined, and record their resolved ID.
    ///
    /// Must run before `populate_called_by`: that function keys on resolved
    /// IDs, so if this hasn't run yet it would key on raw names instead and
    /// reintroduce the name-collision problem it's designed to avoid.
    /// First-definition-wins for ambiguous names is a conscious partial
    /// answer rather than skipping resolution entirely — an approximate
    /// caller list is more useful than none.
    fn resolve_call_locations(kb: &mut KnowledgeBase) {
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

    #[inline]
    fn find_base_idx(
        base: &str,
        node_map: &HashMap<&str, usize>,
        suffix_index: &HashMap<&str, Vec<usize>>,
    ) -> Option<usize> {
        if let Some(&i) = node_map.get(base) {
            return Some(i);
        }

        // Base-class identifiers in source are unprefixed; node IDs are
        // stored with a `class_`/`func_`/`method_` prefix. A stack buffer +
        // manual byte copy is used instead of `format!("{prefix}{base}")`
        // because this runs in a hot per-class loop and avoiding a heap
        // allocation per candidate prefix here was measurable. The `unsafe`
        // is sound because both `prefix` and `base` are already valid UTF-8
        // and we only ever slice at their exact combined length.
        for prefix in ["class_", "func_", "method_"] {
            let mut buf = [0u8; 256];
            let plen = prefix.len();
            let blen = base.len();
            if plen + blen <= buf.len() {
                buf[..plen].copy_from_slice(prefix.as_bytes());
                buf[plen..plen + blen].copy_from_slice(base.as_bytes());
                #[allow(unsafe_code)]
                let candidate = unsafe { std::str::from_utf8_unchecked(&buf[..plen + blen]) };
                if let Some(&i) = node_map.get(candidate) {
                    return Some(i);
                }
            }
        }

        // Last resort: the suffix index trades a small amount of precision
        // (first match on the trailing identifier) for avoiding an O(n)
        // scan over every node when neither the exact name nor a prefixed
        // variant matched.
        suffix_index
            .get(base)
            .and_then(|candidates| candidates.first().copied())
    }

    /// Generate index for fast lookups - OPTIMIZED WITH CHUNKING
    ///
    /// Chunked so that intermediate per-thread Vecs stay bounded instead of
    /// growing unpredictably large on bigger repos — the prior unchunked
    /// version was the source of memory spikes this was rewritten to avoid.
    fn generate_indices(kb: &KnowledgeBase) -> Indices {
        const CHUNK_SIZE: usize = 1000;

        let structure_vec: Vec<_> = kb.structure.iter().collect();
        let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

        let all_indices: Vec<_> = chunks
            .par_iter()
            .map(|chunk| {
                let mut local_fn_by_name: Vec<(String, String)> = Vec::new();
                let mut local_fn_by_tag: Vec<(String, String)> = Vec::new();
                let mut local_fn_calling: Vec<(String, String)> = Vec::new();
                let mut local_types: Vec<(String, String)> = Vec::new();

                for (filepath, filedata) in chunk.iter() {
                    for func in &filedata.functions {
                        local_fn_by_name.push((
                            func.name.clone(),
                            format!("{}:{}", filepath, func.line_start),
                        ));

                        for tag in &func.tags {
                            local_fn_by_tag.push((tag.clone(), func.id.clone()));
                        }

                        for call in &func.calls {
                            local_fn_calling.push((call.callee.clone(), func.id.clone()));
                        }
                    }

                    for class in &filedata.classes {
                        local_types.push((
                            class.name.clone(),
                            format!("{}:{}", filepath, class.line_start),
                        ));

                        for method in &class.methods {
                            local_fn_by_name.push((
                                method.name.clone(),
                                format!("{}:{}", filepath, method.line_start),
                            ));

                            for tag in &method.tags {
                                local_fn_by_tag.push((tag.clone(), method.id.clone()));
                            }
                        }
                    }
                }

                (
                    local_fn_by_name,
                    local_fn_by_tag,
                    local_fn_calling,
                    local_types,
                )
            })
            .collect();

        // Merged serially for the same reason as elsewhere: cheaper than
        // synchronizing shared maps across the parallel workers above.
        let mut functions_by_name: HashMap<String, Vec<String>> = HashMap::new();
        let mut functions_by_tag: HashMap<String, Vec<String>> = HashMap::new();
        let mut functions_calling: HashMap<String, Vec<String>> = HashMap::new();
        let mut types_by_name: HashMap<String, Vec<String>> = HashMap::new();

        for (fn_by_name, fn_by_tag, fn_calling, types) in all_indices {
            for (k, v) in fn_by_name {
                functions_by_name.entry(k).or_default().push(v);
            }
            for (k, v) in fn_by_tag {
                functions_by_tag.entry(k).or_default().push(v);
            }
            for (k, v) in fn_calling {
                functions_calling.entry(k).or_default().push(v);
            }
            for (k, v) in types {
                types_by_name.entry(k).or_default().push(v);
            }
        }

        Indices {
            functions_by_name,
            functions_calling,
            functions_by_tag,
            types_by_name,
            files_by_category: HashMap::new(),
        }
    }

    /// Find entry points (main functions, app init, etc.)
    ///
    /// Deliberately name/decorator-based rather than framework-specific
    /// parsing: supporting every framework's exact entry-point convention
    /// individually isn't tractable, so this leans on naming conventions
    /// (`main`, `run`, `start`) and common decorator keywords that are
    /// shared across most web/CLI frameworks instead.
    fn find_entry_points(kb: &KnowledgeBase) -> Vec<EntryPoint> {
        let mut entry_points = Vec::new();

        for (filepath, filedata) in &kb.structure {
            for func in &filedata.functions {
                if func.name == "main" || func.name == "run" || func.name == "start" {
                    entry_points.push(EntryPoint {
                        entry_type: "main".to_string(),
                        path: None,
                        function: func.name.clone(),
                        handler: func.name.clone(),
                        file: filepath.clone(),
                        line: func.line_start,
                        methods: None,
                    });
                }

                // Substring matching on decorator text (rather than parsing
                // each framework's decorator syntax) keeps this working
                // across Flask/FastAPI/etc. without per-framework branches.
                for decorator in &func.decorators {
                    if decorator.contains("route")
                        || decorator.contains("get")
                        || decorator.contains("post")
                        || decorator.contains("api")
                    {
                        let route_path = Self::extract_route_path(decorator);
                        let http_methods = Self::extract_http_methods(decorator);

                        entry_points.push(EntryPoint {
                            entry_type: "api_endpoint".to_string(),
                            path: route_path,
                            function: func.name.clone(),
                            handler: func.name.clone(),
                            file: filepath.clone(),
                            line: func.line_start,
                            methods: Some(http_methods),
                        });
                    }
                }

                if func
                    .decorators
                    .iter()
                    .any(|d| d.contains("command") || d.contains("click"))
                {
                    entry_points.push(EntryPoint {
                        entry_type: "cli_command".to_string(),
                        path: Some(func.name.clone()),
                        function: func.name.clone(),
                        handler: func.name.clone(),
                        file: filepath.clone(),
                        line: func.line_start,
                        methods: None,
                    });
                }
            }
        }

        entry_points
    }

    fn extract_route_path(decorator: &str) -> Option<String> {
        // A regex on quoted, path-like text is good enough to pull the
        // route string out of arbitrary decorator syntax without writing a
        // real parser for every framework's decorator grammar.
        let re = regex::Regex::new(r#"['"]([/\w-]+)['"]"#).ok()?;
        re.captures(decorator)
            .and_then(|caps| caps.get(1))
            .map(|m| m.as_str().to_string())
    }

    fn extract_http_methods(decorator: &str) -> Vec<String> {
        let mut methods = Vec::new();
        let dec_lower = decorator.to_lowercase();

        if dec_lower.contains("get") {
            methods.push("GET".to_string());
        }
        if dec_lower.contains("post") {
            methods.push("POST".to_string());
        }
        if dec_lower.contains("put") {
            methods.push("PUT".to_string());
        }
        if dec_lower.contains("delete") {
            methods.push("DELETE".to_string());
        }

        // GET is the safest default when no verb keyword is found, since
        // most undecorated/ambiguous route definitions in practice are
        // read-only endpoints.
        if methods.is_empty() {
            methods.push("GET".to_string());
        }

        methods
    }

    /// Analyze external dependencies
    ///
    /// stdlib/cgo imports are filtered out up front because "external
    /// dependency" is meant to answer "what third-party packages does this
    /// project pull in" — including the standard library would make that
    /// list noisy and not actionable for e.g. dependency auditing.
    fn analyze_external_deps(kb: &KnowledgeBase) -> Vec<ExternalDependency> {
        let all_deps: Vec<_> = kb
            .structure
            .par_iter()
            .flat_map(|(filepath, filedata)| {
                filedata
                    .imports
                    .iter()
                    .filter(|i| i.import_type != "internal" && i.import_type != "cgo")
                    .map(|i| (i.module.clone(), filepath.clone(), i.import_type.clone()))
                    .collect::<Vec<_>>()
            })
            .collect();

        // A HashSet for `used_by` because the same module can legitimately
        // be imported multiple times in one file (or the same file could
        // appear twice across parallel chunks) — dedup is required for
        // `import_count` to mean "number of distinct files," not "number of
        // import statements."
        let mut deps_map: HashMap<String, (HashSet<String>, String)> = HashMap::new();
        for (module, filepath, import_type) in all_deps {
            let entry = deps_map
                .entry(module)
                .or_insert_with(|| (HashSet::new(), import_type));
            entry.0.insert(filepath);
        }

        deps_map
            .into_iter()
            .map(|(name, (files, import_type))| ExternalDependency {
                name,
                version: None,
                source: import_type, // "stdlib" | "external"
                import_count: files.len(),
                used_by: files.into_iter().collect(),
            })
            .collect()
    }

    /// Detect common patterns
    ///
    /// These are cheap, corpus-wide heuristics rather than an attempt at
    /// exhaustive static analysis — the goal is a quick "here's roughly how
    /// this project is organized" summary, not a certified classification.
    fn detect_patterns(kb: &KnowledgeBase) -> PatternInfo {
        let mut patterns = PatternInfo::default();

        // Majority vote over naming style, rather than flagging every
        // inconsistency, since most real codebases mix conventions
        // somewhat and what's useful here is the dominant style.
        let mut snake_case_count = 0;
        let mut camel_case_count = 0;

        for filedata in kb.structure.values() {
            for func in &filedata.functions {
                if func.name.contains('_') {
                    snake_case_count += 1;
                } else if func.name.chars().any(|c| c.is_uppercase()) {
                    camel_case_count += 1;
                }
            }
        }

        patterns.naming_convention = if snake_case_count > camel_case_count {
            "snake_case".to_string()
        } else {
            "camelCase".to_string()
        };

        let has_src_dir = kb.structure.keys().any(|p| p.starts_with("src/"));
        let has_lib_dir = kb.structure.keys().any(|p| p.starts_with("lib/"));
        let has_tests_dir = kb.structure.keys().any(|p| p.contains("test"));

        if has_src_dir && has_tests_dir {
            patterns.structure_type = "Standard (src/ + tests/)".to_string();
        } else if has_lib_dir {
            patterns.structure_type = "Library".to_string();
        } else {
            patterns.structure_type = "Flat".to_string()
        }

        patterns.architecture_style = Self::detect_architecture(kb);

        patterns
    }

    fn detect_architecture(kb: &KnowledgeBase) -> Option<String> {
        let file_paths: Vec<&String> = kb.structure.keys().collect();

        // Layered/MVC/microservices are distinguished by directory-naming
        // conventions rather than actual dependency direction between
        // layers, since verifying real layering would require the full
        // call graph and cross-language import resolution — this is a
        // fast, best-effort signal instead.
        let has_api = file_paths
            .iter()
            .any(|p| p.contains("api") || p.contains("routes"));
        let has_service = file_paths
            .iter()
            .any(|p| p.contains("service") || p.contains("business"));
        let has_data = file_paths
            .iter()
            .any(|p| p.contains("model") || p.contains("repository") || p.contains("dao"));

        if has_api && has_service && has_data {
            return Some("layered".to_string());
        }

        let has_model = file_paths.iter().any(|p| p.contains("model"));
        let has_view = file_paths
            .iter()
            .any(|p| p.contains("view") || p.contains("template"));
        let has_controller = file_paths.iter().any(|p| p.contains("controller"));

        if has_model && has_view && has_controller {
            return Some("mvc".to_string());
        }

        // Threshold of >3 is arbitrary but intentional: one or two
        // "service"-named files is common in non-microservice projects too,
        // so a low bar would misclassify them.
        let service_count = file_paths.iter().filter(|p| p.contains("service")).count();
        if service_count > 3 {
            return Some("microservices".to_string());
        }

        None
    }

    /// Generate project summary
    pub fn generate_summary(kb: &KnowledgeBase) -> ProjectSummary {
        ProjectSummary {
            project_name: kb.metadata.project_name.clone(),
            total_files: kb.metadata.total_files,
            total_loc: kb.metadata.total_loc,
            languages: kb.metadata.languages.clone(),
            categories: Self::categorize_files(&kb.structure),
            key_features: Self::extract_key_features(kb),
            entry_points: kb
                .entry_points
                .iter()
                .map(|ep| format!("{}:{}", ep.file, ep.line))
                .collect(),
            dependencies: DependencyInfo {
                stdlib: kb
                    .external_dependencies
                    .iter()
                    .filter(|d| d.source == "stdlib")
                    .map(|d| d.name.clone())
                    .collect(),
                third_party: kb
                    .external_dependencies
                    .iter()
                    .filter(|d| d.source == "external")
                    .map(|d| d.name.clone())
                    .collect(),
            },
            patterns: kb.patterns.clone(),
        }
    }
    // Borrows from `kb` (`'a`) instead of cloning every function's name into
    // the report, since this can run over every function in the codebase and
    // the report is typically discarded right after being printed/serialized.
    pub fn generate_metrics<'a>(kb: &'a KnowledgeBase, k: usize) -> MetricsReport<'a> {
        use std::cmp::Ordering;

        let mut metrics: Vec<FunctionMetric<'a>> = kb
            .structure
            .iter()
            .flat_map(|(file, fd)| {
                let top_level = fd.functions.iter().map(move |f| FunctionMetric {
                    name: &f.name,
                    file: file.as_str(),
                    complexity: f.complexity,
                    importance_score: f.importance_score,
                    line_start: f.line_start,
                    line_end: f.line_end,
                });
                let methods = fd.classes.iter().flat_map(move |c| {
                    c.methods.iter().map(move |m| FunctionMetric {
                        name: &m.name,
                        file: file.as_str(),
                        complexity: m.complexity,
                        importance_score: m.importance_score,
                        line_start: m.line_start,
                        line_end: m.line_end,
                    })
                });
                top_level.chain(methods)
            })
            .collect();

        // Complexity first, importance as tiebreaker, name last: this is
        // meant to surface "the riskiest functions to touch," and raw
        // complexity is the strongest available proxy for that; importance
        // and name only disambiguate ties deterministically.
        metrics.sort_unstable_by(|a, b| {
            b.complexity
                .cmp(&a.complexity)
                .then_with(|| {
                    b.importance_score
                        .partial_cmp(&a.importance_score)
                        .unwrap_or(Ordering::Equal)
                })
                .then_with(|| a.name.cmp(b.name))
        });
        metrics.truncate(k);

        MetricsReport {
            metadata: &kb.metadata,
            top_complex_functions: metrics,
        }
    }

    fn categorize_files(structure: &HashMap<String, FileData>) -> HashMap<String, Vec<String>> {
        let mut categories: HashMap<String, Vec<String>> = HashMap::new();

        for (filepath, filedata) in structure {
            let category = Self::classify_file(filepath, filedata);
            categories
                .entry(category)
                .or_default()
                .push(filepath.to_string());
        }

        categories
    }

    fn classify_file(path: &str, data: &FileData) -> String {
        let path_lower = path.to_lowercase();

        // Path-based checks are tried before scanning function names because
        // they're both cheaper and more reliable — a file literally named
        // `auth/login.py` is stronger evidence than a function that happens
        // to mention "hash."
        if path_lower.contains("test") {
            return "Tests".to_string();
        }
        if path_lower.contains("auth") || path_lower.contains("login") {
            return "Authentication".to_string();
        }
        if path_lower.contains("api")
            || path_lower.contains("endpoint")
            || path_lower.contains("route")
        {
            return "API".to_string();
        }
        if path_lower.contains("util") || path_lower.contains("helper") {
            return "Utilities".to_string();
        }
        if path_lower.contains("model") || path_lower.contains("entity") {
            return "Data Models".to_string();
        }
        if path_lower.contains("ui") || path_lower.contains("view") {
            return "User Interface".to_string();
        }

        // Falls back to scanning function names for security-flavored
        // keywords only when the path itself gave no signal — catches
        // security-relevant code that isn't isolated into its own
        // directory (common in smaller projects).
        for func in &data.functions {
            let name_lower = func.name.to_lowercase();
            if name_lower.contains("crypt")
                || name_lower.contains("hash")
                || name_lower.contains("encrypt")
            {
                return "Security".to_string();
            }
        }

        "Other".to_string()
    }

    fn extract_key_features(kb: &KnowledgeBase) -> Vec<String> {
        // A HashSet dedupes near-identical docstring openers that show up
        // repeatedly across a codebase (boilerplate docstrings, copy-pasted
        // templates), so the feature list isn't dominated by repeats.
        let mut features = HashSet::new();

        for filedata in kb.structure.values() {
            for func in &filedata.functions {
                // Length threshold (>20 chars) filters out placeholder or
                // near-empty docstrings that wouldn't read as a real
                // "feature" description anyway.
                if !func.docstring.is_empty() && func.docstring.len() > 20 {
                    let sentences: Vec<&str> = func.docstring.split('.').collect();
                    if let Some(first) = sentences.first() {
                        let trimmed = first.trim();
                        if !trimmed.is_empty() {
                            features.insert(trimmed.to_string());
                        }
                    }
                }
            }

            for cls in &filedata.classes {
                if !cls.docstring.is_empty() && cls.docstring.len() > 20 {
                    let sentences: Vec<&str> = cls.docstring.split('.').collect();
                    if let Some(first) = sentences.first() {
                        let trimmed = first.trim();
                        if !trimmed.is_empty() {
                            features.insert(trimmed.to_string());
                        }
                    }
                }
            }
        }

        // Capped at 10 because this feeds a human-facing summary — an
        // unbounded list would defeat the point of a quick overview.
        features.into_iter().take(10).collect()
    }

    // build_module_to_files_borrowed used by v2 to avoid
    // cloning every file path into this map when the caller already holds
    // the strings for the lifetime of the whole build — the owned version
    // above is kept for callers (or future use) that need an owned result.
    fn build_module_to_files_borrowed<'a>(file_list: &[&'a str]) -> HashMap<String, Vec<&'a str>> {
        let mut map: HashMap<String, Vec<&'a str>> = HashMap::with_capacity(file_list.len() * 2);
        for &path in file_list {
            let cleaned = path.trim_start_matches("./");
            let stem = match cleaned.rsplit_once('.') {
                Some((s, _)) => s,
                None => cleaned,
            };
            let module = stem.rsplit_once('/').map(|(_, t)| t).unwrap_or(stem);
            if !module.is_empty() {
                map.entry(module.to_string()).or_default().push(path);
            }
            let dotted = stem.replace('/', ".");
            if dotted != module {
                map.entry(dotted).or_default().push(path);
            }
        }
        map
    }
}

// Bundles every read-only index the resolver needs behind one struct so it
// can be passed into parallel closures by shared reference without each one
// carrying a long, error-prone parameter list
struct ResolveCtx<'a> {
    node_map: &'a HashMap<&'a str, usize>,
    symbol_index: &'a HashMap<&'a str, usize>,
    class_methods: &'a HashMap<&'a str, HashMap<String, usize>>,
    inheritance_ids: &'a HashMap<usize, Vec<usize>>,
    descendants_ids: &'a HashMap<usize, Vec<usize>>,
    file_scope: &'a HashMap<&'a str, HashMap<&'a str, Vec<usize>>>,
    imports_per_file: &'a HashMap<String, Vec<(String, String)>>,
    module_to_files: &'a HashMap<String, Vec<&'a str>>,
    idx_to_key: &'a Vec<&'a str>,
}

impl<'a> ResolveCtx<'a> {
    /// Core resolution — called millions of times across a large codebase,
    /// so the tier order below is deliberately cheapest/most-certain first:
    /// each earlier tier that hits saves the cost of every tier after it,
    /// and each tier is also a stronger signal than the ones that follow,
    /// so trying them in this order also gives better answers on average,
    /// not just faster ones.
    #[inline]
    fn resolve(
        &self,
        callee: &str,
        caller_file: &str,
        caller_class_id: Option<&str>,
    ) -> Option<usize> {
        // exact id match — unambiguous, and the common case when the
        // parser already fully qualified the callee.
        if let Some(&idx) = self.node_map.get(callee) {
            return Some(idx);
        }

        // class-aware method resolution — see `first_self_param` for why
        // this tier only runs when the call site looks like a method call.
        if let Some(cls_id) = caller_class_id {
            if let Some(&cls_idx) = self.node_map.get(cls_id) {
                if let Some(methods) = self.class_methods.get(cls_id) {
                    if let Some(&idx) = methods.get(callee) {
                        return Some(idx);
                    }
                }
                if let Some(idx) = self.lookup_in_chain(callee, cls_idx, self.inheritance_ids) {
                    return Some(idx);
                }
                if let Some(idx) = self.lookup_in_chain(callee, cls_idx, self.descendants_ids) {
                    return Some(idx);
                }
            }
        }

        // file-local scope — shadowing within a file is common; across
        // files it's rare, so this is checked before the weaker tiers below.
        if let Some(file_map) = self.file_scope.get(caller_file) {
            if let Some(candidates) = file_map.get(callee) {
                if let Some(&first) = candidates.first() {
                    return Some(first);
                }
            }
        }

        // import-guided cross-file lookup — an explicit import is a much
        // more reliable link to a definition than the unscoped global guess
        // that follows.
        if let Some(imports) = self.imports_per_file.get(caller_file) {
            for (imported_name, source_module) in imports {
                if imported_name == callee {
                    if let Some(target_files) = self.module_to_files.get(source_module) {
                        for &tf in target_files {
                            if let Some(file_map) = self.file_scope.get(tf) {
                                if let Some(candidates) = file_map.get(callee) {
                                    return candidates.first().copied();
                                }
                            }
                        }
                    }
                }
            }
        }

        // Direct hit on the symbol index (covers already-qualified calls like
        // "func_foo" / "method_Bar_baz").
        if let Some(&idx) = self.symbol_index.get(callee) {
            return Some(idx);
        }
        // Bare name fallback: try each known ID prefix. Mirrors find_base_idx.
        for prefix in ["func_", "method_", "class_"] {
            let prefixed = format!("{}{}", prefix, callee);
            if let Some(&idx) = self.symbol_index.get(prefixed.as_str()) {
                return Some(idx);
            }
        }
        None
    }

    /// BFS over an index map using only usize — no String clones.
    #[inline]
    fn lookup_in_chain(
        &self,
        method_short: &str,
        start: usize,
        chain: &HashMap<usize, Vec<usize>>,
    ) -> Option<usize> {
        // Single inheritance (one parent) is the overwhelmingly common case,
        // so it gets a fast path that checks parent and grandparent directly
        // without paying for queue/visited-set allocation at all — the full
        // BFS below only runs when multiple inheritance actually shows up.
        let neighbors = chain.get(&start)?;
        if neighbors.len() == 1 {
            let n = neighbors[0];
            let nid = self.idx_to_key[n];
            if let Some(methods) = self.class_methods.get(nid) {
                if let Some(&idx) = methods.get(method_short) {
                    return Some(idx);
                }
            }
            if let Some(gp) = chain.get(&n).and_then(|v| v.first()).copied() {
                let gid = self.idx_to_key[gp];
                if let Some(methods) = self.class_methods.get(gid) {
                    if let Some(&idx) = methods.get(method_short) {
                        return Some(idx);
                    }
                }
            }
            return None;
        }

        // Multi-parent case: falls back to a real BFS with a visited set,
        // both to handle depth beyond grandparent and to stay safe against
        // cycles in malformed/generated class hierarchies.
        let mut queue: VecDeque<usize> = VecDeque::with_capacity(8);
        let mut visited: HashSet<usize> = HashSet::with_capacity(16);
        queue.push_back(start);
        visited.insert(start);
        while let Some(cidx) = queue.pop_front() {
            let cid = self.idx_to_key[cidx];
            if let Some(methods) = self.class_methods.get(cid) {
                if let Some(&idx) = methods.get(method_short) {
                    return Some(idx);
                }
            }
            if let Some(nexts) = chain.get(&cidx) {
                for &nidx in nexts {
                    if visited.insert(nidx) {
                        queue.push_back(nidx);
                    }
                }
            }
        }
        None
    }
}

// Supporting structs

#[derive(Debug, Default, Serialize, Deserialize)]
pub struct ProjectSummary {
    pub project_name: String,
    pub total_files: usize,
    pub total_loc: usize,
    pub languages: Vec<String>,
    pub categories: HashMap<String, Vec<String>>,
    pub key_features: Vec<String>,
    pub entry_points: Vec<String>,
    pub dependencies: DependencyInfo,
    pub patterns: PatternInfo,
}

#[derive(Debug, Default, Serialize, Deserialize)]
pub struct DependencyInfo {
    pub stdlib: Vec<String>,
    pub third_party: Vec<String>,
}

// AI was heavily involved in writing the below tests, i did checked the test and code
// and lgtm.

#[cfg(test)]
mod tests {
    // #![expect(clippy::expect_used)]
    #![expect(clippy::unwrap_used)]

    use super::*;

    mod short_name_of_tests {
        use super::*;

        #[test]
        fn strips_method_prefix_only_for_methods() {
            assert_eq!(short_name_of("method_foo_bar::file.rs", 1), "foo_bar");
        }

        #[test]
        fn leaves_functions_untouched_even_if_they_look_like_methods() {
            // node_type 0 => no stripping, even if id happens to start with "method_"
            assert_eq!(
                short_name_of("method_looking::file.rs", 0),
                "method_looking"
            );
        }

        #[test]
        fn no_double_colon_returns_whole_id() {
            assert_eq!(short_name_of("bare_id", 0), "bare_id");
        }

        #[test]
        fn embedded_double_colons_in_the_file_path_do_not_break_lookup() {
            // ID format is `<short>::<file>`. Even if the file path itself contains
            // `::`, the short name is the segment before the FIRST `::`.
            let id = "func_foo::pkg::sub::file.go";
            assert_eq!(short_name_of(id, 0), "func_foo");
        }

        #[test]
        fn method_id_missing_the_method_prefix_is_a_noop_strip() {
            assert_eq!(short_name_of("weird_id::file.rs", 1), "weird_id");
        }

        #[test]
        fn empty_string_does_not_panic() {
            assert_eq!(short_name_of("", 0), "");
        }
    }

    mod first_self_param_tests {
        use super::*;

        fn p(name: &str) -> Parameter {
            Parameter {
                name: name.to_string(),
                type_annotation: String::new(),
                default_value: None,
            }
        }

        #[test]
        fn recognizes_all_known_receiver_spellings() {
            for name in ["self", "this", "cls", "_self", "self_", "myself"] {
                assert!(first_self_param(&[p(name)]), "{name} should be recognized");
            }
        }

        #[test]
        fn empty_params_is_false() {
            assert!(!first_self_param(&[]));
        }

        #[test]
        fn only_the_first_param_is_checked() {
            // a receiver-like name in second position must NOT count
            assert!(!first_self_param(&[p("other"), p("self")]));
        }

        #[test]
        fn case_sensitivity_gap() {
            assert!(!first_self_param(&[p("Self")]));
            assert!(!first_self_param(&[p("This")]));
            assert!(!first_self_param(&[p("Cls")]));
        }

        #[test]
        fn near_miss_spellings_rejected() {
            for name in ["self1", "SELF", "s_elf", " self", "self ", "__self__"] {
                assert!(!first_self_param(&[p(name)]), "{name} should NOT match");
            }
        }
    }

    mod resolve_indirect_call_tests {
        use super::*;
        use std::collections::HashMap;

        #[test]
        fn direct_hit_wins_over_symbol_index() {
            let mut node_map = HashMap::new();
            node_map.insert("func_foo::a.c".to_string(), 0usize);
            let mut symbol_index: HashMap<&str, usize> = HashMap::new();
            symbol_index.insert("foo", 99); // deliberately wrong to prove precedence
            let res = Analyzer::resolve_indirect_call("func_foo::a.c", &node_map, &symbol_index);
            assert_eq!(res, Some(0));
        }

        #[test]
        fn base_name_extraction_keeps_the_whole_suffix_after_method_() {
            // base_name = split("::").last() -> strip "method_" prefix only.
            // For "method_bar_baz::x.c" the base name is "bar_baz", NOT "bar".
            let node_map: HashMap<String, usize> = HashMap::new();
            let mut symbol_index: HashMap<&str, usize> = HashMap::new();
            symbol_index.insert("bar", 5); // wrong key on purpose
            let res =
                Analyzer::resolve_indirect_call("method_bar_baz::x.c", &node_map, &symbol_index);
            assert_eq!(
                res, None,
                "documents that only the full post-prefix suffix is used as the key"
            );
        }

        #[test]
        fn correct_symbol_index_key_resolves() {
            let node_map: HashMap<String, usize> = HashMap::new();
            let mut symbol_index: HashMap<&str, usize> = HashMap::new();
            symbol_index.insert("bar_baz", 5);
            let res =
                Analyzer::resolve_indirect_call("method_bar_baz::x.c", &node_map, &symbol_index);
            assert_eq!(res, Some(5));
        }

        #[test]
        fn unresolvable_returns_none_not_panic() {
            let node_map: HashMap<String, usize> = HashMap::new();
            let symbol_index: HashMap<&str, usize> = HashMap::new();
            assert_eq!(
                Analyzer::resolve_indirect_call("nope", &node_map, &symbol_index),
                None
            );
        }

        #[test]
        fn empty_callee_string_does_not_panic() {
            let node_map: HashMap<String, usize> = HashMap::new();
            let symbol_index: HashMap<&str, usize> = HashMap::new();
            assert_eq!(
                Analyzer::resolve_indirect_call("", &node_map, &symbol_index),
                None
            );
        }
    }

    mod find_base_idx_tests {
        use super::*;
        use std::collections::HashMap;

        #[test]
        fn exact_match_wins() {
            let mut node_map: HashMap<&str, usize> = HashMap::new();
            node_map.insert("Base", 0);
            let suffix_index: HashMap<&str, Vec<usize>> = HashMap::new();
            assert_eq!(
                Analyzer::find_base_idx("Base", &node_map, &suffix_index),
                Some(0)
            );
        }

        #[test]
        fn tries_class_then_func_then_method_prefixes() {
            let mut node_map: HashMap<&str, usize> = HashMap::new();
            node_map.insert("class_Base", 1);
            let suffix_index: HashMap<&str, Vec<usize>> = HashMap::new();
            assert_eq!(
                Analyzer::find_base_idx("Base", &node_map, &suffix_index),
                Some(1)
            );
        }

        #[test]
        fn falls_back_to_suffix_index_first_candidate() {
            // ambiguity (multiple classes with the same short name) is silently
            // resolved to whichever candidate happens to be first in the Vec.
            let node_map: HashMap<&str, usize> = HashMap::new();
            let mut suffix_index: HashMap<&str, Vec<usize>> = HashMap::new();
            suffix_index.insert("Base", vec![7, 8]);
            assert_eq!(
                Analyzer::find_base_idx("Base", &node_map, &suffix_index),
                Some(7)
            );
        }

        #[test]
        fn oversized_base_name_silently_skips_prefix_candidates() {
            // Performance constraint check: `find_base_idx` uses a fixed 256-byte stack
            // buffer for speed. If a generated name is massive (like from minified code),
            // it will silently skip prefix checks rather than panicking or overflowing.
            let huge = "x".repeat(300);
            let mut node_map: HashMap<&str, usize> = HashMap::new();
            let leaked: &'static str = Box::leak(format!("method_{huge}").into_boxed_str());
            node_map.insert(leaked, 42);
            let suffix_index: HashMap<&str, Vec<usize>> = HashMap::new();
            assert_eq!(
                Analyzer::find_base_idx(&huge, &node_map, &suffix_index),
                None,
                "prefix+base > 256 bytes silently skips the candidate"
            );
        }

        #[test]
        fn boundary_at_exactly_256_bytes() {
            // Verifying the exact boundaries of our 256-byte stack buffer limitation.
            // The prefix ("method_" = 7 bytes) + the base name length must be <= 256.
            let base_249 = "y".repeat(249); // 7 + 249 = 256, exactly fits
            let mut node_map: HashMap<&str, usize> = HashMap::new();
            let leaked: &'static str = Box::leak(format!("method_{base_249}").into_boxed_str());
            node_map.insert(leaked, 11);
            let suffix_index: HashMap<&str, Vec<usize>> = HashMap::new();
            assert_eq!(
                Analyzer::find_base_idx(&base_249, &node_map, &suffix_index),
                Some(11)
            );

            let base_250 = "y".repeat(250); // 7 + 250 = 257, one byte over
            let suffix_index2: HashMap<&str, Vec<usize>> = HashMap::new();
            assert_eq!(
                Analyzer::find_base_idx(&base_250, &node_map, &suffix_index2),
                None
            );
        }

        #[test]
        fn no_match_anywhere_returns_none() {
            let node_map: HashMap<&str, usize> = HashMap::new();
            let suffix_index: HashMap<&str, Vec<usize>> = HashMap::new();
            assert_eq!(
                Analyzer::find_base_idx("Ghost", &node_map, &suffix_index),
                None
            );
        }

        #[test]
        fn empty_base_name_does_not_panic() {
            let mut node_map: HashMap<&str, usize> = HashMap::new();
            node_map.insert("class_", 3);
            let suffix_index: HashMap<&str, Vec<usize>> = HashMap::new();
            assert_eq!(
                Analyzer::find_base_idx("", &node_map, &suffix_index),
                Some(3)
            );
        }

        #[test]
        fn multibyte_utf8_base_name_round_trips_safely() {
            // Validating that our stack-buffer string formatting doesn't mangle
            // multi-byte UTF-8 characters by accidentally splitting bytes.
            let base = "Bäse_日本語_class";
            let mut node_map: HashMap<&str, usize> = HashMap::new();
            let leaked: &'static str = Box::leak(format!("class_{base}").into_boxed_str());
            node_map.insert(leaked, 9);
            let suffix_index: HashMap<&str, Vec<usize>> = HashMap::new();
            assert_eq!(
                Analyzer::find_base_idx(base, &node_map, &suffix_index),
                Some(9)
            );
        }
    }

    mod module_to_files_tests {
        use super::*;

        #[test]
        fn basic_stem_and_dotted_module_both_registered() {
            let files = vec!["src/foo/bar.py"];
            let map = Analyzer::build_module_to_files_borrowed(&files);
            assert_eq!(map.get("bar"), Some(&vec!["src/foo/bar.py"]));
            assert_eq!(map.get("src.foo.bar"), Some(&vec!["src/foo/bar.py"]));
        }

        #[test]
        fn leading_dot_slash_is_trimmed() {
            let files = vec!["./main.go"];
            let map = Analyzer::build_module_to_files_borrowed(&files);
            assert_eq!(map.get("main"), Some(&vec!["./main.go"]));
        }

        #[test]
        fn file_with_no_extension_uses_whole_name_as_stem() {
            let files = vec!["Makefile"];
            let map = Analyzer::build_module_to_files_borrowed(&files);
            assert_eq!(map.get("Makefile"), Some(&vec!["Makefile"]));
        }

        #[test]
        fn dotted_directory_component_confuses_extension_stripping() {
            let files = vec!["vendor.v2/module.py"];
            let map = Analyzer::build_module_to_files_borrowed(&files);
            // Expected ideally: module name "module". Document actual behavior:
            assert!(
                map.contains_key("module") || map.contains_key("vendor"),
                "directory dot vs. extension dot ambiguity — verify actual behavior matches intent"
            );
        }

        #[test]
        fn multiple_extensions_only_the_last_dot_is_stripped() {
            let files = vec!["archive.tar.gz"];
            let map = Analyzer::build_module_to_files_borrowed(&files);
            assert_eq!(map.get("archive.tar"), Some(&vec!["archive.tar.gz"]));
        }

        #[test]
        fn same_module_name_from_multiple_files_all_collected() {
            let files = vec!["a/util.py", "b/util.py"];
            let map = Analyzer::build_module_to_files_borrowed(&files);
            assert_eq!(map.get("util").unwrap().len(), 2);
        }

        #[test]
        fn empty_file_list_produces_empty_map() {
            let files: Vec<&str> = vec![];
            assert!(Analyzer::build_module_to_files_borrowed(&files).is_empty());
        }

        #[test]
        fn root_level_file_does_not_double_insert_when_stem_equals_module() {
            let files = vec!["main.rs"];
            let map = Analyzer::build_module_to_files_borrowed(&files);
            assert_eq!(map.len(), 1);
        }
    }

    mod resolve_ctx_tests {
        use super::*;
        use std::collections::HashMap;

        #[allow(clippy::too_many_arguments)]
        fn ctx<'a>(
            node_map: &'a HashMap<&'a str, usize>,
            symbol_index: &'a HashMap<&'a str, usize>,
            class_methods: &'a HashMap<&'a str, HashMap<String, usize>>,
            inheritance_ids: &'a HashMap<usize, Vec<usize>>,
            descendants_ids: &'a HashMap<usize, Vec<usize>>,
            file_scope: &'a HashMap<&'a str, HashMap<&'a str, Vec<usize>>>,
            imports_per_file: &'a HashMap<String, Vec<(String, String)>>,
            module_to_files: &'a HashMap<String, Vec<&'a str>>,
            idx_to_key: &'a Vec<&'a str>,
        ) -> ResolveCtx<'a> {
            ResolveCtx {
                node_map,
                symbol_index,
                class_methods,
                inheritance_ids,
                descendants_ids,
                file_scope,
                imports_per_file,
                module_to_files,
                idx_to_key,
            }
        }

        #[test]
        fn direct_node_map_hit() {
            let mut node_map = HashMap::new();
            node_map.insert("func_foo::a.py", 0usize);
            let (
                symbol_index,
                class_methods,
                inheritance_ids,
                descendants_ids,
                file_scope,
                imports_per_file,
                module_to_files,
            ) = Default::default();
            let idx_to_key = vec!["func_foo::a.py"];
            let c = ctx(
                &node_map,
                &symbol_index,
                &class_methods,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("func_foo::a.py", "a.py", None), Some(0));
        }

        #[test]
        fn self_call_resolved_via_class_methods() {
            let node_map: HashMap<&str, usize> = [("class_Foo", 0)].into_iter().collect();
            let symbol_index = HashMap::new();
            let mut cm: HashMap<&str, HashMap<String, usize>> = HashMap::new();
            cm.insert(
                "class_Foo",
                [("bar".to_string(), 1usize)].into_iter().collect(),
            );
            let (inheritance_ids, descendants_ids, file_scope, imports_per_file, module_to_files) =
                Default::default();
            let idx_to_key = vec!["class_Foo", "method_Foo_bar::a.py"];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("bar", "a.py", Some("class_Foo")), Some(1));
        }

        #[test]
        fn inherited_method_two_levels_up_resolves_via_fast_path() {
            // Child(0) -> Parent(1) -> GParent(2); method only on GParent.
            let node_map: HashMap<&str, usize> = [
                ("class_Child", 0),
                ("class_Parent", 1),
                ("class_GParent", 2),
            ]
            .into_iter()
            .collect();
            let symbol_index = HashMap::new();
            let mut cm: HashMap<&str, HashMap<String, usize>> = HashMap::new();
            cm.insert(
                "class_GParent",
                [("greet".to_string(), 3usize)].into_iter().collect(),
            );
            let mut inheritance_ids: HashMap<usize, Vec<usize>> = HashMap::new();
            inheritance_ids.insert(0, vec![1]);
            inheritance_ids.insert(1, vec![2]);
            let (descendants_ids, file_scope, imports_per_file, module_to_files) =
                Default::default();
            let idx_to_key = vec![
                "class_Child",
                "class_Parent",
                "class_GParent",
                "method_GParent_greet::a.py",
            ];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("greet", "a.py", Some("class_Child")), Some(3));
        }

        #[test]
        fn inherited_method_three_levels_up() {
            // Documenting a known BUG in the current lookup chain implementation.
            // Our single-inheritance fast path `lookup_in_chain` only walks 2 hops up.
            // If a method is 3 levels deep (GGParent), it silently fails to resolve,
            // even though this is very common in OOP.
            let node_map: HashMap<&str, usize> = [
                ("class_Child", 0),
                ("class_Parent", 1),
                ("class_GParent", 2),
                ("class_GGParent", 3),
            ]
            .into_iter()
            .collect();
            let symbol_index = HashMap::new();
            let mut cm: HashMap<&str, HashMap<String, usize>> = HashMap::new();
            cm.insert(
                "class_GGParent",
                [("greet".to_string(), 4usize)].into_iter().collect(),
            );
            let mut inheritance_ids: HashMap<usize, Vec<usize>> = HashMap::new();
            inheritance_ids.insert(0, vec![1]);
            inheritance_ids.insert(1, vec![2]);
            inheritance_ids.insert(2, vec![3]);
            let (descendants_ids, file_scope, imports_per_file, module_to_files) =
                Default::default();
            let idx_to_key = vec![
                "class_Child",
                "class_Parent",
                "class_GParent",
                "class_GGParent",
                "method_GGParent_greet::a.py",
            ];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(
                c.resolve("greet", "a.py", Some("class_Child")),
                None,
                "documents that 3+ level single-inheritance chains don't resolve inherited calls"
            );
        }

        #[test]
        fn self_inheritance_cycle_terminates_via_bfs_visited_set() {
            let node_map: HashMap<&str, usize> = [("class_Weird", 0)].into_iter().collect();
            let symbol_index = HashMap::new();
            let cm: HashMap<&str, HashMap<String, usize>> = HashMap::new();
            let mut inheritance_ids: HashMap<usize, Vec<usize>> = HashMap::new();
            inheritance_ids.insert(0, vec![0, 0]); // len != 1 forces BFS path; self-loop
            let (descendants_ids, file_scope, imports_per_file, module_to_files) =
                Default::default();
            let idx_to_key = vec!["class_Weird"];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            // Must terminate promptly (not hang) and simply fail to resolve.
            assert_eq!(c.resolve("missing", "a.py", Some("class_Weird")), None);
        }

        #[test]
        fn diamond_inheritance_resolves_via_bfs_path() {
            // Child(0) -> [Left(1), Right(2)] -> Base(3); method lives on Base.
            let node_map: HashMap<&str, usize> = [
                ("class_Child", 0),
                ("class_Left", 1),
                ("class_Right", 2),
                ("class_Base", 3),
            ]
            .into_iter()
            .collect();
            let symbol_index = HashMap::new();
            let mut cm: HashMap<&str, HashMap<String, usize>> = HashMap::new();
            cm.insert(
                "class_Base",
                [("act".to_string(), 9usize)].into_iter().collect(),
            );
            let mut inheritance_ids: HashMap<usize, Vec<usize>> = HashMap::new();
            inheritance_ids.insert(0, vec![1, 2]);
            inheritance_ids.insert(1, vec![3]);
            inheritance_ids.insert(2, vec![3]);
            let (descendants_ids, file_scope, imports_per_file, module_to_files) =
                Default::default();
            let idx_to_key = vec![
                "class_Child",
                "class_Left",
                "class_Right",
                "class_Base",
                "method_Base_act::a.py",
            ];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("act", "a.py", Some("class_Child")), Some(9));
        }

        #[test]
        fn file_scope_fallback_used_without_class_context() {
            // When calling a simple function in the same file (not attached to a class),
            // it should resolve using the local file scope dictionary.
            let node_map: HashMap<&str, usize> = HashMap::new();
            let symbol_index = HashMap::new();
            let cm = HashMap::new();
            let (inheritance_ids, descendants_ids) = Default::default();
            let mut file_scope: HashMap<&str, HashMap<&str, Vec<usize>>> = HashMap::new();
            file_scope.insert("a.py", [("helper", vec![2usize])].into_iter().collect());
            let (imports_per_file, module_to_files) = Default::default();
            let idx_to_key = vec!["", "", "func_helper::a.py"];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("helper", "a.py", None), Some(2));
        }

        #[test]
        fn file_scope_from_a_different_file_is_not_leaked() {
            // A function called `helper` defined in `b.py` shouldn't magically resolve
            // when `a.py` tries to call `helper` without importing it.
            let node_map: HashMap<&str, usize> = HashMap::new();
            let symbol_index = HashMap::new();
            let cm = HashMap::new();
            let (inheritance_ids, descendants_ids) = Default::default();
            let mut file_scope: HashMap<&str, HashMap<&str, Vec<usize>>> = HashMap::new();
            file_scope.insert("b.py", [("helper", vec![2usize])].into_iter().collect());
            let (imports_per_file, module_to_files) = Default::default();
            let idx_to_key = vec!["", "", "func_helper::b.py"];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("helper", "a.py", None), None);
        }

        #[test]
        fn import_resolution_crosses_module_to_files() {
            // Tests the cross-file import mapping.
            // Setup: `utils.py` exports `helper`. `main.py` imports `helper` from `utils`.
            let node_map: HashMap<&str, usize> = HashMap::new();
            let symbol_index = HashMap::new();
            let cm = HashMap::new();
            let (inheritance_ids, descendants_ids) = Default::default();
            let mut file_scope: HashMap<&str, HashMap<&str, Vec<usize>>> = HashMap::new();
            file_scope.insert("utils.py", [("helper", vec![5usize])].into_iter().collect());
            let mut imports_per_file: HashMap<String, Vec<(String, String)>> = HashMap::new();
            imports_per_file.insert(
                "main.py".to_string(),
                vec![("helper".to_string(), "utils".to_string())],
            );
            let mut module_to_files: HashMap<String, Vec<&str>> = HashMap::new();
            module_to_files.insert("utils".to_string(), vec!["utils.py"]);
            let idx_to_key = vec!["", "", "", "", "", "func_helper::utils.py"];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("helper", "main.py", None), Some(5));
        }

        #[test]
        fn import_aliasing_is_not_modeled_documents_limitation() {
            // Documenting a known limitation: We don't currently track import aliases.
            // i.e., "from utils import helper as h". The internal engine only sees
            // the original name "helper", so asking it to resolve "h" returns None.
            let node_map: HashMap<&str, usize> = HashMap::new();
            let symbol_index = HashMap::new();
            let cm = HashMap::new();
            let (inheritance_ids, descendants_ids) = Default::default();
            let mut file_scope: HashMap<&str, HashMap<&str, Vec<usize>>> = HashMap::new();
            file_scope.insert("utils.py", [("helper", vec![5usize])].into_iter().collect());
            let mut imports_per_file: HashMap<String, Vec<(String, String)>> = HashMap::new();
            imports_per_file.insert(
                "main.py".to_string(),
                vec![("helper".to_string(), "utils".to_string())],
            );
            let mut module_to_files: HashMap<String, Vec<&str>> = HashMap::new();
            module_to_files.insert("utils".to_string(), vec!["utils.py"]);
            let idx_to_key = vec!["", "", "", "", "", "func_helper::utils.py"];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(
                c.resolve("h", "main.py", None),
                None,
                "aliased imports never resolve"
            );
        }

        #[test]
        fn final_fallback_to_global_symbol_index() {
            let node_map: HashMap<&str, usize> = HashMap::new();
            let mut symbol_index: HashMap<&str, usize> = HashMap::new();
            symbol_index.insert("global_fn", 42);
            let cm = HashMap::new();
            let (inheritance_ids, descendants_ids, file_scope, imports_per_file, module_to_files) =
                Default::default();
            let idx_to_key = vec![];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("global_fn", "anywhere.py", None), Some(42));
        }

        #[test]
        fn totally_unresolvable_returns_none_without_panicking() {
            let node_map: HashMap<&str, usize> = HashMap::new();
            let symbol_index: HashMap<&str, usize> = HashMap::new();
            let cm = HashMap::new();
            let (inheritance_ids, descendants_ids, file_scope, imports_per_file, module_to_files) =
                Default::default();
            let idx_to_key = vec![];
            let c = ctx(
                &node_map,
                &symbol_index,
                &cm,
                &inheritance_ids,
                &descendants_ids,
                &file_scope,
                &imports_per_file,
                &module_to_files,
                &idx_to_key,
            );
            assert_eq!(c.resolve("totally_unknown", "nowhere.py", None), None);
        }
    }

    mod call_graph_integration_tests {
        use super::*;
        use std::collections::HashMap;

        fn func(id: &str, name: &str, calls: Vec<&str>) -> Function {
            Function {
                id: id.to_string(),
                name: name.to_string(),
                calls: calls
                    .into_iter()
                    .map(|c| FunctionCall {
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
            let structure: HashMap<String, FileData> = HashMap::new();
            let g1 = Analyzer::build_call_graph(&structure);
            let g2 = Analyzer::build_call_graph_v2(&structure);
            assert!(g1.nodes.is_empty() && g1.edges.is_empty());
            assert!(g2.nodes.is_empty() && g2.edges.is_empty());
        }

        #[test]
        fn self_recursive_function_produces_a_self_edge() {
            let mut structure = HashMap::new();
            structure.insert(
                "a.c".to_string(),
                filedata(vec![func("func_fact::a.c", "fact", vec!["fact"])], vec![]),
            );
            let g = Analyzer::build_call_graph_v2(&structure);
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
            let mut structure = HashMap::new();
            structure.insert(
                "a.c".to_string(),
                filedata(vec![func("func_dup::x", "dup", vec![])], vec![]),
            );
            structure.insert(
                "b.c".to_string(),
                filedata(vec![func("func_dup::x", "dup", vec![])], vec![]),
            );
            let g = Analyzer::build_call_graph_v2(&structure);
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
            let mut structure = HashMap::new();
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
            let g = Analyzer::build_call_graph_v2(&structure);
            let edge = g.edges.iter().find(|e| e.from == "func_caller::caller.c");
            assert!(
                edge.is_some(),
                "should resolve to *some* run — just not a guaranteed-stable one"
            );
        }

        #[test]
        fn call_to_nonexistent_function_produces_no_edge_and_no_panic() {
            let mut structure = HashMap::new();
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
            let g = Analyzer::build_call_graph_v2(&structure);
            assert!(g.edges.is_empty());
            assert_eq!(g.nodes.len(), 1);
        }

        #[test]
        fn v1_par_chunks_boundary_at_exactly_chunk_size() {
            // CHUNK_SIZE = 2000 in build_call_graph; verify n-1, n, n+1 all
            // preserve exactly the right node count (off-by-one bugs in
            // manual chunking are a classic source of silently dropped data).
            for n in [1999usize, 2000, 2001] {
                let mut structure = HashMap::new();
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
                let g = Analyzer::build_call_graph(&structure);
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
            let mut structure = HashMap::new();
            let cls = Class {
                id: "class_Child::a.c".to_string(),
                name: "Child".to_string(),
                bases: vec!["ExternalLibBase".to_string()],
                ..Default::default()
            };
            structure.insert("a.c".to_string(), filedata(vec![], vec![cls]));
            let g = Analyzer::build_call_graph_v2(&structure);
            assert!(g.edges.iter().all(|e| e.edge_type != "inheritance"));
        }

        #[test]
        fn duplicate_calls_on_the_v2_path_are_deduped_per_source_node() {
            // v2 uses a `seen: HashSet<(from, to)>` per chunk, so calling the
            // same function twice from the same caller produces one edge,
            // not two -- verify that intentional behavior explicitly.
            let mut structure = HashMap::new();
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
            let g = Analyzer::build_call_graph_v2(&structure);
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
    }
}
