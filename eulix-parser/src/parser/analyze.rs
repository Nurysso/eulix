//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::kb::types::*;
use rayon::prelude::*;
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};

/// Analyzes the knowledge base to extract high-level insights
pub struct Analyzer;

impl Analyzer {
    /// Generate complete knowledge base with indices and call graph
    pub fn analyze_and_build(mut kb: KnowledgeBase, verbose: bool) -> KnowledgeBase {
        let file_count = kb.structure.len();
        let is_large = file_count > 100000; // For very large codebases, skip expensive operations
        let use_precise = true; // todo! add a way to change algorithm maybe is_large or args
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
            if use_precise {
                if verbose {
                    println!("      Using precise analysis (PRISM)...");
                }
                // Build the research-grade call graph
                kb.call_graph = Self::build_call_graph(&kb.structure); // todo! write a new build_call_graph_precise.
            } else {
                if verbose {
                    println!("      Using direct analysis...");
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
            if use_precise {
                // For precise graphs, populate called_by from the graph itself
                Self::populate_called_by(&mut kb); // todo write a new populate_called_by_from_graph
            } else {
                Self::populate_called_by(&mut kb);
            }
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

        // PHASE 1: Build compact node index in parallel (Same as before)
        let structure_vec: Vec<_> = structure.iter().collect();
        let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

        #[derive(Clone)]
        struct CompactNode {
            id: String,
            node_type: u8,
            file_idx: usize,
            is_entry: bool,
        }

        let all_nodes: Vec<(String, CompactNode)> = chunks
            .par_iter()
            .flat_map(|chunk| {
                let mut local_nodes = Vec::with_capacity(chunk.len() * 10);
                for (filepath, filedata) in chunk.iter() {
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
        let mut symbol_index: HashMap<String, usize> = HashMap::with_capacity(node_map.len());
        for (id, &idx) in node_map.iter() {
            let short_name = id
                .split("::")
                .last()
                .and_then(|s| s.strip_prefix("method_"))
                .unwrap_or(id);

            // Conservative CHA: Map the simple name to the first ID found.
            symbol_index.entry(short_name.to_string()).or_insert(idx);
        }

        // (File index logic remains same, but omitted for brevity to focus on Phase 2)
        let file_list: Vec<String> = structure.keys().cloned().collect();
        let file_map: HashMap<String, usize> = file_list
            .iter()
            .enumerate()
            .map(|(i, f)| (f.clone(), i))
            .collect();

        // PHASE 2: Build edges using CSR-style compact storage
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

        // --- PHASE 3: Pre-calculate Call Counts ---
        // Create a frequency map of how many times each node index appears as a 'to' (callee)
        let mut counts = vec![0; unique_nodes.len()];
        for (_, to_idx, _, _, _) in &all_edges {
            if let Some(count) = counts.get_mut(*to_idx) {
                *count += 1;
            }
        }

        // --- PHASE 4: Conversion to Final Nodes ---
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

        // --- PHASE 5: Conversion to Final Edges ---
        let final_edges: Vec<CallGraphEdge> = all_edges
            .into_iter()
            .map(|(from_idx, to_idx, kind, cond, line)| {
                CallGraphEdge {
                    // Use the index to get the actual String ID (e.g., "my_function_name")
                    from: final_nodes[from_idx].id.clone(),
                    to: final_nodes[to_idx].id.clone(),
                    edge_type: if kind == 1 {
                        "inheritance".to_string()
                    } else {
                        "call".to_string()
                    },
                    conditional: cond,
                    call_site_line: line,
                }
            })
            .collect();

        // Final result - These will now be in scope
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
        symbol_index: &HashMap<String, usize>,
    ) -> Option<usize> {
        // 1. Try exact match first
        if let Some(&idx) = node_map.get(callee) {
            return Some(idx);
        }

        let base_name = callee
            .split("::")
            .last()
            .and_then(|s: &str| s.strip_prefix("method_").or(Some(s)))
            .unwrap_or(callee);

        // Remove the semicolon to return the value!
        symbol_index.get(base_name).copied()
    }

    /// Populate called_by fields in functions (reverse call graph) - OPTIMIZED WITH CHUNKING
    /// Populate called_by fields using resolved callee IDs, not raw names.
    /// resolve_call_locations must be called first.
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
        let mut func_info: HashMap<String, (String, String)> = HashMap::new();

        for (filepath, filedata) in &kb.structure {
            for func in &filedata.functions {
                func_info
                    .entry(func.name.clone())
                    .or_insert_with(|| (filepath.clone(), func.id.clone()));
            }
            for class in &filedata.classes {
                for method in &class.methods {
                    func_info
                        .entry(method.name.clone())
                        .or_insert_with(|| (filepath.clone(), method.id.clone()));
                }
            }
        }

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
        // Collect all dependencies in parallel without locks
        let all_deps: Vec<_> = kb
            .structure
            .par_iter()
            .flat_map(|(filepath, filedata)| {
                let mut local_deps = Vec::new();
                for import in &filedata.imports {
                    if import.import_type == "external" {
                        local_deps.push((import.module.clone(), filepath.clone()));
                    }
                }
                local_deps
            })
            .collect();

        // Build dependency map from collected data
        let mut deps_map: HashMap<String, HashSet<String>> = HashMap::new();
        for (module, filepath) in all_deps {
            deps_map
                .entry(module)
                .or_insert_with(HashSet::new)
                .insert(filepath);
        }

        // Convert to vec
        deps_map
            .into_iter()
            .map(|(name, files)| ExternalDependency {
                name,
                version: None,
                source: "imports".to_string(),
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
        summary.dependencies = DependencyInfo {
            stdlib: kb
                .external_dependencies
                .iter()
                .filter(|d| Self::is_stdlib(&d.name))
                .map(|d| d.name.clone())
                .collect(),
            third_party: kb
                .external_dependencies
                .iter()
                .filter(|d| !Self::is_stdlib(&d.name))
                .map(|d| d.name.clone())
                .collect(),
        };
        summary.patterns = kb.patterns.clone();

        summary
    }

    fn is_stdlib(module: &str) -> bool {
        let stdlib = [
            "os",
            "sys",
            "re",
            "json",
            "datetime",
            "time",
            "collections",
            "itertools",
            "functools",
            "pathlib",
            "subprocess",
            "threading",
            "asyncio",
            "typing",
            "math",
            "random",
            "hashlib",
            "uuid",
        ];
        stdlib.contains(&module)
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
