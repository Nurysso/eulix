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

        // For very large codebases, skip expensive operations
        let is_large = file_count > 20000;

        if verbose && is_large {
            println!("   ⚠️  Enabling memory-efficient mode for {} files", file_count);
        }

        // Build call graph (skip for very large repos to save memory)
        if !is_large {
            if verbose { println!("   → Building call graph..."); }
            kb.call_graph = Self::build_call_graph(&kb.structure);
        } else if verbose {
            println!("   ⚠️  Skipping call graph (too large, would use excessive memory)");
        }

        // Build reverse call graph (populate called_by)
        if !is_large {
            if verbose { println!("   → Building reverse call graph..."); }
            Self::populate_called_by(&mut kb);
        }

        // Resolve function call locations
        if !is_large {
            if verbose { println!("   → Resolving call locations..."); }
            Self::resolve_call_locations(&mut kb);
        }

        // Build indices (always do this, it's useful)
        if verbose { println!("   → Generating indices..."); }
        kb.indices = Self::generate_indices(&kb);

        // Detect patterns (lightweight)
        if verbose { println!("   → Detecting patterns..."); }
        kb.patterns = Self::detect_patterns(&kb);

        // Find entry points (lightweight)
        if verbose { println!("   → Finding entry points..."); }
        kb.entry_points = Self::find_entry_points(&kb);

        // Analyze external dependencies (lightweight)
        if verbose { println!("   → Analyzing dependencies..."); }
        kb.external_dependencies = Self::analyze_external_deps(&kb);

        kb
    }

    /// Build call graph from structure
    fn build_call_graph(structure: &HashMap<String, FileData>) -> CallGraph {
        let mut nodes = Vec::new();
        let mut edges = Vec::new();
        let mut node_ids = HashSet::new();

        for (filepath, filedata) in structure {
            // Add function nodes
            for func in &filedata.functions {
                if !node_ids.contains(&func.id) {
                    nodes.push(CallGraphNode {
                        id: func.id.clone(),
                        node_type: if func.id.starts_with("method_") {
                            "method".to_string()
                        } else {
                            "function".to_string()
                        },
                        file: filepath.clone(),
                        is_entry_point: func.tags.contains(&"entry-point".to_string()),
                        call_count_estimate: 0, // Will be calculated
                    });
                    node_ids.insert(func.id.clone());
                }

                // Add edges for function calls
                for call in &func.calls {
                    edges.push(CallGraphEdge {
                        from: func.id.clone(),
                        to: call.callee.clone(),
                        edge_type: "calls".to_string(),
                        conditional: call.is_conditional,
                        call_site_line: call.line,
                    });
                }
            }

            // Add class nodes
            for class in &filedata.classes {
                if !node_ids.contains(&class.id) {
                    nodes.push(CallGraphNode {
                        id: class.id.clone(),
                        node_type: "class".to_string(),
                        file: filepath.clone(),
                        is_entry_point: false,
                        call_count_estimate: 0,
                    });
                    node_ids.insert(class.id.clone());
                }

                // Add inheritance edges
                for base in &class.bases {
                    edges.push(CallGraphEdge {
                        from: class.id.clone(),
                        to: base.clone(),
                        edge_type: "inherits".to_string(),
                        conditional: false,
                        call_site_line: class.line_start,
                    });
                }

                // Process class methods
                for method in &class.methods {
                    if !node_ids.contains(&method.id) {
                        nodes.push(CallGraphNode {
                            id: method.id.clone(),
                            node_type: "method".to_string(),
                            file: filepath.clone(),
                            is_entry_point: false,
                            call_count_estimate: 0,
                        });
                        node_ids.insert(method.id.clone());
                    }

                    for call in &method.calls {
                        edges.push(CallGraphEdge {
                            from: method.id.clone(),
                            to: call.callee.clone(),
                            edge_type: "calls".to_string(),
                            conditional: call.is_conditional,
                            call_site_line: call.line,
                        });
                    }
                }
            }
        }

        // Calculate call counts
        let mut call_counts: HashMap<String, usize> = HashMap::new();
        for edge in &edges {
            *call_counts.entry(edge.to.clone()).or_insert(0) += 1;
        }

        for node in &mut nodes {
            node.call_count_estimate = *call_counts.get(&node.id).unwrap_or(&0);
        }

        CallGraph { nodes, edges }
    }

    /// Populate called_by fields in functions (reverse call graph) - OPTIMIZED WITH CHUNKING
    fn populate_called_by(kb: &mut KnowledgeBase) {
        const CHUNK_SIZE: usize = 1000;

        let structure_vec: Vec<_> = kb.structure.iter().collect();
        let chunks: Vec<_> = structure_vec.chunks(CHUNK_SIZE).collect();

        // Collect all caller info in parallel with chunking
        let all_calls: Vec<_> = chunks
            .par_iter()
            .flat_map(|chunk| {
                let mut local_calls = Vec::new();

                for (filepath, filedata) in chunk.iter() {
                    for func in &filedata.functions {
                        for call in &func.calls {
                            local_calls.push((
                                call.callee.clone(),
                                CallerInfo {
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
                                local_calls.push((
                                    call.callee.clone(),
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

                local_calls
            })
            .collect();

        // Build reverse mapping from collected data
        let mut reverse_calls: HashMap<String, Vec<CallerInfo>> = HashMap::new();
        for (callee, caller_info) in all_calls {
            reverse_calls
                .entry(callee)
                .or_insert_with(Vec::new)
                .push(caller_info);
        }

        // Update called_by fields
        for (_, filedata) in kb.structure.iter_mut() {
            for func in &mut filedata.functions {
                if let Some(callers) = reverse_calls.get(&func.name) {
                    func.called_by = callers.clone();
                }
            }

            for class in &mut filedata.classes {
                for method in &mut class.methods {
                    if let Some(callers) = reverse_calls.get(&method.name) {
                        method.called_by = callers.clone();
                    }
                }
            }
        }
    }

    /// Resolve where called functions are defined
    fn resolve_call_locations(kb: &mut KnowledgeBase) {
        // Build function name -> file location mapping
        let mut func_locations: HashMap<String, String> = HashMap::new();

        for (filepath, filedata) in &kb.structure {
            for func in &filedata.functions {
                func_locations.insert(func.name.clone(), filepath.clone());
            }

            for class in &filedata.classes {
                for method in &class.methods {
                    func_locations.insert(method.name.clone(), filepath.clone());
                }
            }
        }

        // Update defined_in fields
        for (_, filedata) in kb.structure.iter_mut() {
            for func in &mut filedata.functions {
                for call in &mut func.calls {
                    call.defined_in = func_locations.get(&call.callee).cloned();
                }
            }

            for class in &mut filedata.classes {
                for method in &mut class.methods {
                    for call in &mut method.calls {
                        call.defined_in = func_locations.get(&call.callee).cloned();
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

                (local_fn_by_name, local_fn_by_tag, local_fn_calling, local_types)
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
                    if decorator.contains("route") || decorator.contains("get") ||
                       decorator.contains("post") || decorator.contains("api") {
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
                if func.decorators.iter().any(|d| d.contains("command") || d.contains("click")) {
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
        let all_deps: Vec<_> = kb.structure
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
        let has_api = file_paths.iter().any(|p| p.contains("api") || p.contains("routes"));
        let has_service = file_paths.iter().any(|p| p.contains("service") || p.contains("business"));
        let has_data = file_paths.iter().any(|p| p.contains("model") || p.contains("repository") || p.contains("dao"));

        if has_api && has_service && has_data {
            return Some("layered".to_string());
        }

        // Check for MVC
        let has_model = file_paths.iter().any(|p| p.contains("model"));
        let has_view = file_paths.iter().any(|p| p.contains("view") || p.contains("template"));
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
        summary.entry_points = kb.entry_points.iter().map(|ep| {
            format!("{}:{}", ep.file, ep.line)
        }).collect();
        summary.dependencies = DependencyInfo {
            stdlib: kb.external_dependencies
                .iter()
                .filter(|d| Self::is_stdlib(&d.name))
                .map(|d| d.name.clone())
                .collect(),
            third_party: kb.external_dependencies
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
            "os", "sys", "re", "json", "datetime", "time", "collections",
            "itertools", "functools", "pathlib", "subprocess", "threading",
            "asyncio", "typing", "math", "random", "hashlib", "uuid",
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
        if path_lower.contains("api") || path_lower.contains("endpoint") || path_lower.contains("route") {
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
            if name_lower.contains("crypt") || name_lower.contains("hash") || name_lower.contains("encrypt") {
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
