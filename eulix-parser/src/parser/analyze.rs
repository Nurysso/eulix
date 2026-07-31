//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::struc::kb_struct::*;
use rayon::prelude::*;
use serde::{Deserialize, Serialize};
use std::collections::VecDeque;
use std::collections::{HashMap, HashSet};
// use rustc_hash::{FxHashMap as HashMap, FxHashMap as HashSet};
/// Analyzes the knowledge base to extract high-level insights
pub struct Analyzer;

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

#[inline(always)]
fn short_name_of(id: &str, node_type: u8) -> &str {
    let stripped = id.split("::").last().unwrap_or(id);
    if node_type == 1 {
        stripped.strip_prefix("method_").unwrap_or(stripped)
    } else {
        stripped
    }
}

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
        // For very large codebases, skip expensive operations,
        let is_large = file_count > 100000;
        let use_precise = prism;
        if verbose && is_large {
            println!(
                "   [!]  Enabling memory-efficient mode for {} files",
                file_count
            );
        }

        // Build call graph (skip for very large repos to save memory)
        if !is_large {
            // Resolve first calle_id must be populated before anything
            // that builds edges or reverse maps.
            if verbose {
                println!("   → Resolving call locations...");
            }
            Self::resolve_call_locations(&mut kb);
            if verbose {
                println!("   → Building call graphs...");
            }
            // use_precise value can only be 1[default] or 2
            // calp manages value checking while parsing args
            if use_precise == 2 {
                if verbose {
                    println!("      Using precise analysis (PRISMv2)...");
                }
                // Build the research-grade call graph
                kb.call_graph = Self::build_call_graph_v2(&kb.structure);
            } else {
                if verbose {
                    println!("      Using direct analysis (PRISMv1)...");
                }
                // Build the simpler direct call graph
                kb.call_graph = Self::build_call_graph(&kb.structure);
            }
            if verbose {
                println!("   → Building reverse call graphs...");
            }
            if verbose {
                println!("   → Building reverse call graphs...");
            }
            // if use_precise == 2 || use_precise == 1 {
            // For precise graphs, populate called_by from the graph itself
            // Self::populate_called_by(&mut kb); // todo write a new populate_called_by_from_graph
            // } else {
            // Self::populate_called_by(&mut kb);
            // }
            Self::populate_called_by(&mut kb);
        } else if verbose {
            println!("   [!]  Skipping call graph (too large, would use excessive memory)");
        }

        // Build indices (always do this, it's useful )
        if verbose {
            println!("   → Generating indices...");
        }
        kb.indices = Self::generate_indices(&kb);

        // Detect patterns (lightweight)
        if verbose {
            println!("   → Detecting patterns...");
        }
        kb.patterns = Self::detect_patterns(&kb);

        // Find entry points (lightweight)
        if verbose {
            println!("   → Finding entry points...");
        }
        kb.entry_points = Self::find_entry_points(&kb);

        // Analyze external dependencies (lightweight)
        if verbose {
            println!("   → Analyzing dependencies...");
        }
        kb.external_dependencies = Self::analyze_external_deps(&kb);

        kb
    }

    /// Builds a call graph from the already-parsed codebase.
    /// Uses tiered resolution: Direct linkage -> PRISM
    ///
    /// # Overview
    /// The function takes a mapping of file paths to their extracted `FileData`
    /// (containing functions, classes, and their calls) and constructs a
    /// `CallGraph` with nodes (functions, methods, classes) and edges
    /// (direct calls, inheritance).
    ///
    /// The building process is split into five phases:
    /// - **Phase 1 – Node extraction and deduplication**
    ///   Gathers all entity IDs in parallel chunks, builds a compact
    ///   `CompactNode` list, and deduplicates them via a `node_map`.
    /// - **Phase 2 – Symbol index pre‑computation**
    ///   Creates a fallback lookup table (`symbol_index`) that maps the
    ///   unqualified “short” name (e.g., `"foo"` from `"module::Class::method_foo"`)
    ///   to the first node found with that short name. This is a simplified
    ///   Class Hierarchy Analysis (CHA) placeholder for indirect call resolution.
    /// - **Phase 3 – Edge extraction**
    ///   Scans all call sites in parallel chunks and resolves the callee ID
    ///   first by exact match (`node_map`) and then via the `symbol_index`.
    ///   Edges are stored as compact tuples `(from, to, kind, is_cond, line)`.
    /// - **Phase 4 – Call count estimation**
    ///   Counts how many times each node appears as a callee (ingoing edges)
    ///   and attaches the count to the final node as `call_count_estimate`.
    /// - **Phase 5 – Final conversion**
    ///   Converts compact nodes and edges into the public `CallGraphNode`
    ///   and `CallGraphEdge` types.
    ///
    /// # Parallelism
    /// The node and edge extraction steps are parallelised by splitting the
    /// input hashmap into chunks of `CHUNK_SIZE = 2000` entries and processing
    /// each chunk with Rayon’s `par_iter`.
    ///
    /// # Important Notice (Version 1)
    /// This is a **prototype** calling‑convention resolver. The CHA/Steensgaard
    /// analysis is deliberately simplified or boderline dosnet exists:
    /// * The `symbol_index` only stores the **first** occurrence of a short name.
    /// * It does **not** differentiate between classes, namespaces, or arities.
    /// * Inheritance edges are recorded but are **not used** in call resolution yet.
    /// A proper points‑to analysis (e.g., Steensgaard or Andersen) is planned
    /// for version 2 of the parser.

    fn build_call_graph(structure: &HashMap<String, FileData>) -> CallGraph {
        const CHUNK_SIZE: usize = 2000;

        // PHASE 1:
        //  Build compact node index in parallel
        let structure_vec: Vec<_> = structure.iter().collect();
        let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

        let all_nodes: Vec<(String, CompactNode)> = chunks
            .par_iter()
            .flat_map(|chunk| {
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

        let mut node_map: HashMap<String, usize> = HashMap::with_capacity(all_nodes.len());
        let mut unique_nodes: Vec<CompactNode> = Vec::with_capacity(all_nodes.len());

        for (id, node) in all_nodes {
            if node_map.insert(id.clone(), unique_nodes.len()).is_none() {
                unique_nodes.push(node);
            }
        }

        // SYMBOL INDEX PRE-COMPUTATION
        // This turns the 30-minute linear scan into a microsecond lookup.
        let mut symbol_index: HashMap<&str, usize> = HashMap::with_capacity(node_map.len());
for (id, &idx) in node_map.iter() {
    let short_name = id
        .split("::")
        .last()
        .and_then(|s| s.strip_prefix("method_"))
        .unwrap_or(id);

    // Conservative CHA: Map the simple name to the first ID found.
    symbol_index.entry(short_name).or_insert(idx);
}

        // (File index logic remains same, but omitted for brevity to focus on Phase 2)
        let file_list: Vec<String> = structure.keys().cloned().collect();
        let _file_map: HashMap<String, usize> = file_list
            .iter()
            .enumerate()
            .map(|(i, f)| (f.clone(), i))
            .collect();

        // PHASE 2:
        // Build edges using CSR-style compact storage
        type CompactEdge = (usize, usize, u8, bool, usize);

        let all_edges: Vec<CompactEdge> = chunks
            .par_iter()
            .flat_map(|chunk| {
                let mut local_edges = Vec::new();

                for (_, filedata) in chunk.iter() {
                    // Lambda for resolution helper to avoid passing 100 arguments
                    let resolve = |callee: &str| -> Option<usize> {
                        // Try exact ID first
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

        // PHASE 3
        // Pre-calculate Call Counts
        // Create a frequency map of how many times each node index appears as a 'to' (callee)
        let mut counts = vec![0; unique_nodes.len()];
        for (_, to_idx, _, _, _) in &all_edges {
            if let Some(count) = counts.get_mut(*to_idx) {
                *count += 1;
            }
        }

        // PHASE 4
        // Conversion to Final Nodes
        let final_nodes: Vec<CallGraphNode> = unique_nodes
            .into_iter()
            .enumerate()
            .map(|(i, node)| {
                // Recover the file path using the file_list created earlier
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

        // PHASE 5:
        // Conversion to Final Edges
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

        // Final result - These will now be in scope
        CallGraph {
            nodes: final_nodes,
            edges: final_edges,
        }
    }

    pub fn build_call_graph_v2(structure: &HashMap<String, FileData>) -> CallGraph {
        //  0. file list (stable order)
        let file_list: Vec<&str> = {
            let mut v: Vec<&str> = structure.keys().map(String::as_str).collect();
            v.sort_unstable();
            v
        };
        let file_map: HashMap<&str, usize> =
            file_list.iter().enumerate().map(|(i, &s)| (s, i)).collect();

        //  1. collect nodes (parallel, single pass)
        //
        // Each chunk emits (id_str, CompactNode).  We collect all
        // of them first, then dedup with a single serial scan.
        // file_idx is set correctly here — no fixup pass needed.
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

        // dedup — first occurrence wins
        let mut node_map: HashMap<&str, usize> = HashMap::with_capacity(raw_nodes.len());
        let mut unique_nodes: Vec<CompactNode> = Vec::with_capacity(raw_nodes.len());
        for (id, node) in raw_nodes {
            if !node_map.contains_key(id) {
                node_map.insert(id, unique_nodes.len());
                unique_nodes.push(node);
            }
        }

        //  2. build auxiliary indices (parallel per chunk)
        //
        // Each chunk produces local maps; we merge once serially.
        // Strings are borrowed from `structure` wherever possible.

         //  2a. BUILD SUFFIX INDEX ONCE (before hot path)
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

        //  merge chunk indices
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
                // look up the &str key from node_map so we don't clone cid
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

        // reverse inheritance (parent → children)
        let mut descendants_ids: HashMap<usize, Vec<usize>> =
            HashMap::with_capacity(inheritance_ids.len());
        for (&child, parents) in &inheritance_ids {
            for &parent in parents {
                descendants_ids.entry(parent).or_default().push(child);
            }
        }

        // idx → &str lookup (borrowed from node_map keys)
        let idx_to_key: Vec<&str> = {
            let mut v = vec![""; unique_nodes.len()];
            for (&id, &idx) in &node_map {
                v[idx] = id;
            }
            v
        };

        //  file-scope index: file → short_name → [node_idx]
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

        //  global symbol fallback: short_name → first idx
        let mut symbol_index: HashMap<&str, usize> = HashMap::with_capacity(node_map.len());
        for (&id, &idx) in &node_map {
            let short = short_name_of(id, unique_nodes[idx].node_type);
            symbol_index.entry(short).or_insert(idx);
        }

        //  module → files mapping
        let module_to_files: HashMap<String, Vec<&str>> =
            Self::build_module_to_files_borrowed(&file_list);

        //  3. edge collection (parallel)
        //
        // Borrows are all shared-ref; the closures are Send because
        // all maps are read-only at this point.
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
                        if let Some(bi) = Self::find_base_idx(base, ctx.node_map, &suffix_index) {
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

        //  4. build final graph
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

    /// Resolves a callee string to a node index using a two‑tier lookup.
    /// # Resolution Strategy
    /// 1. **Exact match** – looks up the full qualified name in `node_map`.
    /// 2. **CHA‑inspired fallback** – extracts the short name (last segment
    ///    after `"::"`, stripping an optional `"method_"` prefix) and
    ///    queries the precomputed `symbol_index`.
    ///
    /// TODO! have a better implementation if needed in the future
    /// A cheap approximation of Class Hierarchy Analysis + Steensgaard's Algorithm. In a full
    /// implementation the symbol index would be replaced by a lookup that
    /// takes the receiver type and virtual dispatch into account.
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
        .last()
        .and_then(|s: &str| s.strip_prefix("method_").or(Some(s)))
        .unwrap_or(callee);

    symbol_index.get(base_name).copied()
}

    /// Populate called_by fields in functions (reverse call graph)
    /// uses resolved callee IDs, not raw names.
    fn populate_called_by(kb: &mut KnowledgeBase) {
        const CHUNK_SIZE: usize = 1000;

        let structure_vec: Vec<_> = kb.structure.iter().collect();
        let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

        // Key on callee (resolved). Falls back to raw name only if
        // resolution failed, which is logged so it can be investigated.
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
                                    function: func.id.clone(), // caller's ID, not name
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

        // Reverse map is now ID → callers, no name collisions possible.
        let mut reverse: HashMap<String, Vec<CallerInfo>> = HashMap::new();
        for (callee, caller_info) in all_calls {
            reverse.entry(callee).or_default().push(caller_info);
        }

        // Apply: look up by each function's own ID.
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
    /// Must run before populate_called_by.
    fn resolve_call_locations(kb: &mut KnowledgeBase) {
        // name → (file, id) — when a name is ambiguous, we can only record
        // the first definition here; full disambiguation requires type info.
        // We still resolve what we can so called_by isn't silently wrong.
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

    for prefix in ["class_", "func_", "method_"] {
        let mut buf = [0u8; 256];
        let plen = prefix.len();
        let blen = base.len();
        if plen + blen <= buf.len() {
            buf[..plen].copy_from_slice(prefix.as_bytes());
            buf[plen..plen + blen].copy_from_slice(base.as_bytes());
            let candidate = unsafe { std::str::from_utf8_unchecked(&buf[..plen + blen]) };
            if let Some(&i) = node_map.get(candidate) {
                return Some(i);
            }
        }
    }

    // Fast suffix lookup instead of O(n) scan
    suffix_index
        .get(base)
        .and_then(|candidates| candidates.first().copied())
}

    /// Generate index for fast lookups - OPTIMIZED WITH CHUNKING
    fn generate_indices(kb: &KnowledgeBase) -> Indices {
        const CHUNK_SIZE: usize = 1000;

        let structure_vec: Vec<_> = kb.structure.iter().collect();
        let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

        // Process in chunks to avoid memory spikes
        let all_indices: Vec<_> = chunks
            .par_iter()
            .map(|chunk| {
                let mut local_fn_by_name: Vec<(String, String)> = Vec::new();
                let mut local_fn_by_tag: Vec<(String, String)> = Vec::new();
                let mut local_fn_calling: Vec<(String, String)> = Vec::new();
                let mut local_types: Vec<(String, String)> = Vec::new();

                for (filepath, filedata) in chunk.iter() {
                    // Index functions by name
                    for func in &filedata.functions {
                        local_fn_by_name.push((
                            func.name.clone(),
                            format!("{}:{}", filepath, func.line_start),
                        ));

                        // Index by tags
                        for tag in &func.tags {
                            local_fn_by_tag.push((tag.clone(), func.id.clone()));
                        }

                        // Index functions that call this
                        for call in &func.calls {
                            local_fn_calling.push((call.callee.clone(), func.id.clone()));
                        }
                    }

                    // Index classes
                    for class in &filedata.classes {
                        local_types.push((
                            class.name.clone(),
                            format!("{}:{}", filepath, class.line_start),
                        ));

                        // Index methods
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

        // Merge all collected data
        let mut functions_by_name: HashMap<String, Vec<String>> = HashMap::new();
        let mut functions_by_tag: HashMap<String, Vec<String>> = HashMap::new();
        let mut functions_calling: HashMap<String, Vec<String>> = HashMap::new();
        let mut types_by_name: HashMap<String, Vec<String>> = HashMap::new();

        for (fn_by_name, fn_by_tag, fn_calling, types) in all_indices {
            for (k, v) in fn_by_name {
                functions_by_name.entry(k).or_insert_with(Vec::new).push(v);
            }
            for (k, v) in fn_by_tag {
                functions_by_tag.entry(k).or_insert_with(Vec::new).push(v);
            }
            for (k, v) in fn_calling {
                functions_calling.entry(k).or_insert_with(Vec::new).push(v);
            }
            for (k, v) in types {
                types_by_name.entry(k).or_insert_with(Vec::new).push(v);
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
    fn find_entry_points(kb: &KnowledgeBase) -> Vec<EntryPoint> {
        let mut entry_points = Vec::new();

        for (filepath, filedata) in &kb.structure {
            for func in &filedata.functions {
                // Check for common entry point patterns
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

                // Check for API endpoints (Flask/FastAPI decorators)
                for decorator in &func.decorators {
                    if decorator.contains("route")
                        || decorator.contains("get")
                        || decorator.contains("post")
                        || decorator.contains("api")
                    {
                        // Try to extract route path
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

                // Check for CLI commands (click/argparse)
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
        // Extract path from decorators like @app.route("/api/login")
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

        if methods.is_empty() {
            methods.push("GET".to_string()); // Default
        }

        methods
    }

    /// Analyze external dependencies - OPTIMIZED
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

        // Build dependency map from collected data
        // (files, import_type) — import_type from first occurrence wins
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
    fn detect_patterns(kb: &KnowledgeBase) -> PatternInfo {
        let mut patterns = PatternInfo::default();

        // Naming convention detection
        let mut snake_case_count = 0;
        let mut camel_case_count = 0;

        for (_, filedata) in &kb.structure {
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

        // Project structure pattern
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

        // Architecture style detection
        patterns.architecture_style = Self::detect_architecture(kb);

        patterns
    }

    fn detect_architecture(kb: &KnowledgeBase) -> Option<String> {
        let file_paths: Vec<&String> = kb.structure.keys().collect();

        // Check for layered architecture
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

        // Check for MVC
        let has_model = file_paths.iter().any(|p| p.contains("model"));
        let has_view = file_paths
            .iter()
            .any(|p| p.contains("view") || p.contains("template"));
        let has_controller = file_paths.iter().any(|p| p.contains("controller"));

        if has_model && has_view && has_controller {
            return Some("mvc".to_string());
        }

        // Check for microservices (multiple services)
        let service_count = file_paths.iter().filter(|p| p.contains("service")).count();
        if service_count > 3 {
            return Some("microservices".to_string());
        }

        None
    }

    /// Generate project summary
    pub fn generate_summary(kb: &KnowledgeBase) -> ProjectSummary {
        let mut summary = ProjectSummary::default();

        summary.project_name = kb.metadata.project_name.clone();
        summary.total_files = kb.metadata.total_files;
        summary.total_loc = kb.metadata.total_loc;
        summary.languages = kb.metadata.languages.clone();

        summary.categories = Self::categorize_files(&kb.structure);
        summary.key_features = Self::extract_key_features(kb);
        summary.entry_points = kb
            .entry_points
            .iter()
            .map(|ep| format!("{}:{}", ep.file, ep.line))
            .collect();
        // for debuging level 3 todo
        // for d in &kb.external_dependencies {
        //     println!("DEP: {:?}  LANGS: {:?}", d.name, summary.languages);
        // }
        summary.dependencies = DependencyInfo {
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
        };
        summary.patterns = kb.patterns.clone();

        summary
    }

    pub fn generate_metrics<'a>(kb: &'a KnowledgeBase, k: usize) -> MetricsReport<'a> {
        use std::cmp::Ordering;

        // Flat iterator over every function and method in the KB.
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
                .or_insert_with(Vec::new)
                .push(filepath.to_string());
        }

        categories
    }

    fn classify_file(path: &str, data: &FileData) -> String {
        let path_lower = path.to_lowercase();

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
        let mut features = HashSet::new();

        for (_, filedata) in &kb.structure {
            for func in &filedata.functions {
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

        features.into_iter().take(10).collect()
    }

#[allow(dead_code)]
    fn resolve_v2(
        callee: &str,
        caller_file: &str,
        caller_class_id: Option<&str>,
        node_map: &HashMap<String, usize>,
        symbol_index: &HashMap<String, usize>,
        class_methods: &HashMap<String, HashMap<String, usize>>,
        inheritance_ids: &HashMap<usize, Vec<usize>>,
        descendants_ids: &HashMap<usize, Vec<usize>>,
        file_scope: &HashMap<String, HashMap<String, Vec<usize>>>,
        imports_per_file: &HashMap<String, Vec<(String, String)>>,
        module_to_files: &HashMap<String, Vec<String>>,
        idx_to_id: &HashMap<usize, String>,
    ) -> Option<usize> {
        // already fully-qualified (v1 pre-pass or exact ID) trust it, skip everything else.
        if let Some(&idx) = node_map.get(callee) {
            return Some(idx);
        }

        // self.foo() should resolve within the caller's own type hierarchy,
        // not globally, otherwise unrelated classes with the same method name collide.
        if let Some(cls_id) = caller_class_id {
            if let Some(class_idx) = node_map.get(cls_id).copied() {
                // Own class is the most likely target, check before touching the hierarchy.
                if let Some(methods) = class_methods.get(cls_id) {
                    if let Some(&idx) = methods.get(callee) {
                        return Some(idx);
                    }
                }
                // Inherited method not overridden locally, walk up to find where it's defined.
                if inheritance_ids.contains_key(&class_idx) {
                    if let Some(idx) = Self::lookup_method_in_chain(
                        callee,
                        class_idx,
                        inheritance_ids,
                        class_methods,
                        node_map,
                        idx_to_id,
                        true,
                    ) {
                        return Some(idx);
                    }
                }
                // Polymorphic dispatch, the runtime call could land in a subclass override.
                if let Some(idx) = Self::lookup_method_in_chain(
                    callee,
                    class_idx,
                    descendants_ids,
                    class_methods,
                    node_map,
                    idx_to_id,
                    true,
                ) {
                    return Some(idx);
                }
            }
        }

        // no class context (or step 2 missed), same file is still the strongest signal,
        // since shadowing across files is rare but shadowing within a file is common.
        if let Some(file_map) = file_scope.get(caller_file) {
            if let Some(candidates) = file_map.get(callee) {
                match candidates.len() {
                    0 => {}
                    1 => return Some(candidates[0]),
                    _ => {
                        // Ambiguous, ideally we'd prefer a method vs function candidate by scope,
                        // but node_type isn't available on this fast path, so fall back deterministically.
                        if let Some(cls_id) = caller_class_id {
                            let _ = cls_id;
                        }
                        return Some(candidates[0]);
                    }
                }
            }
        }

        // name isn't local, check if it was imported, since that's the next most
        // reliable link to a definition before we give up and guess globally.
        if let Some(imports) = imports_per_file.get(caller_file) {
            for (imported_name, source_module) in imports {
                if imported_name == callee {
                    if let Some(target_files) = module_to_files.get(source_module) {
                        for tf in target_files {
                            if let Some(file_map) = file_scope.get(tf) {
                                if let Some(candidates) = file_map.get(callee) {
                                    return candidates.first().copied();
                                }
                            }
                        }
                    }
                }
            }
        }

        // last resort, any match beats none, kept for v1 compatibility on
        // cross-module calls that slip past every structural check above.
        symbol_index.get(callee).copied()
    }

    /// BFS-walk the class hierarchy (ancestors or descendants) looking for
    /// a class that defines the given method short name.
    fn lookup_method_in_chain(
        method_short: &str,
        start_class_idx: usize,
        chain: &HashMap<usize, Vec<usize>>,
        class_methods: &HashMap<String, HashMap<String, usize>>,
        node_map: &HashMap<String, usize>,
        idx_to_id: &HashMap<usize, String>,
        _walk_kind: bool, // true=walk both, currently unused (kept for future tuning)
    ) -> Option<usize> {
        // Recover class id from node idx. node_map is id→idx; we need
        let _ = _walk_kind;
        let start_id = idx_to_id.get(&start_class_idx)?;

        let mut queue: VecDeque<String> = VecDeque::new();
        let mut visited: HashSet<String> = HashSet::new();
        queue.push_back(start_id.clone());
        visited.insert(start_id.clone());

        while let Some(cid) = queue.pop_front() {
            // Direct hit on this class?
            if let Some(methods) = class_methods.get(&cid) {
                if let Some(&idx) = methods.get(method_short) {
                    return Some(idx);
                }
            }
            // Walk the chain (ancestors or descendants).
            if let Some(cidx) = node_map.get(&cid).copied() {
                if let Some(neighbors) = chain.get(&cidx) {
                    for &nidx in neighbors {
                        if let Some(nid) = idx_to_id.get(&nidx) {
                            if visited.insert(nid.clone()) {
                                queue.push_back(nid.clone());
                            }
                        }
                    }
                }
            }
        }
        None
    }

    /// Build a reverse node_map (idx → id). Called once per resolver invocation
    /// from lookup_method_in_chain; for hot loops, cache this once in Phase 2.
    #[allow(dead_code)]
    fn build_idx_to_id(node_map: &HashMap<String, usize>) -> HashMap<usize, String> {
        node_map.iter().map(|(k, v)| (*v, k.clone())).collect()
    }

    /// Build a reverse index: parent_id → [child_id, ...].
    #[allow(dead_code)]
    fn reverse_inheritance(forward: &HashMap<String, Vec<String>>) -> HashMap<String, Vec<String>> {
        let mut reverse: HashMap<String, Vec<String>> = HashMap::new();
        for (child, parents) in forward {
            for parent in parents {
                reverse
                    .entry(parent.clone())
                    .or_default()
                    .push(child.clone());
            }
        }
        reverse
    }
    /// Heuristic: map a module name to the files that "declare" it.
    /// Examples (Python-style):
    ///   "os"             → files matching "os.py" or paths starting with "os/"
    ///   "pkg.subpkg"     → files matching "pkg/subpkg/" or "pkg/subpkg.py"
    ///   "react"          → "react.tsx", "react/index.ts"
    ///
    /// Conservative: if uncertain, returns an empty Vec, and Step 4 simply
    /// misses (Step 5 picks up the slack).
    #[allow(dead_code)]
    fn build_module_to_files(file_list: &[String]) -> HashMap<String, Vec<String>> {
        let mut map: HashMap<String, Vec<String>> = HashMap::new();
        for path in file_list {
            // Strip extension and any leading "./"
            let cleaned = path.trim_start_matches("./").to_string();
            let stem = match cleaned.rsplit_once('.') {
                Some((s, _ext)) => s,
                None => cleaned.as_str(),
            };
            // Last path component is a candidate module name.
            let module = stem.rsplit_once('/').map(|(_, tail)| tail).unwrap_or(stem);
            if !module.is_empty() {
                map.entry(module.to_string())
                    .or_default()
                    .push(path.clone());
            }
            // Also map the full dotted path for languages that use them.
            let dotted = stem.replace('/', ".");
            if dotted != module {
                map.entry(dotted).or_default().push(path.clone());
            }
        }
        map
    }
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
    /// Core resolution — called millions of times; keep it branch-minimal.
    #[inline]
    fn resolve(
        &self,
        callee: &str,
        caller_file: &str,
        caller_class_id: Option<&str>,
    ) -> Option<usize> {
        // ① exact id match (fastest)
        if let Some(&idx) = self.node_map.get(callee) {
            return Some(idx);
        }

        // ② class-aware method resolution
        if let Some(cls_id) = caller_class_id {
            if let Some(&cls_idx) = self.node_map.get(cls_id) {
                // own methods
                if let Some(methods) = self.class_methods.get(cls_id) {
                    if let Some(&idx) = methods.get(callee) {
                        return Some(idx);
                    }
                }
                // walk inheritance chain (parent classes)
                if let Some(idx) = self.lookup_in_chain(callee, cls_idx, self.inheritance_ids) {
                    return Some(idx);
                }
                // walk descendant chain (overrides)
                if let Some(idx) = self.lookup_in_chain(callee, cls_idx, self.descendants_ids) {
                    return Some(idx);
                }
            }
        }

        // ③ file-local scope
        if let Some(file_map) = self.file_scope.get(caller_file) {
            if let Some(candidates) = file_map.get(callee) {
                if let Some(&first) = candidates.first() {
                    return Some(first);
                }
            }
        }

        // ④ import-guided cross-file lookup
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

        // ⑤ global symbol fallback
        self.symbol_index.get(callee).copied()
    }

    /// BFS over an index map using only usize — no String clones.
    #[inline]
    fn lookup_in_chain(
        &self,
        method_short: &str,
        start: usize,
        chain: &HashMap<usize, Vec<usize>>,
    ) -> Option<usize> {
        // fast path: single parent, no queue needed
        let neighbors = chain.get(&start)?;
        if neighbors.len() == 1 {
            let n = neighbors[0];
            let nid = self.idx_to_key[n];
            if let Some(methods) = self.class_methods.get(nid) {
                if let Some(&idx) = methods.get(method_short) {
                    return Some(idx);
                }
            }
            // one more level (grandparent) without full BFS
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

        // full BFS (multi-parent / multiple inheritance)
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
