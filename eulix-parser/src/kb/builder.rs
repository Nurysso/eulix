use anyhow::Result;
use chrono::Utc;
use std::collections::HashMap;
use std::path::{Path, PathBuf};

use crate::kb::types::{
    KnowledgeBase, Metadata, FileData, DependencyGraph, GraphNode, GraphEdge,
    EntryPoint, ExternalDependency, CallGraph, Indices, PatternInfo,
};

pub struct KnowledgeBaseBuilder {
    root_path: PathBuf,
}

#[allow(dead_code)]
impl KnowledgeBaseBuilder {
    pub fn new(root_path: &Path) -> Self {
        Self {
            root_path: root_path.to_path_buf(),
        }
    }

    pub fn build(&self, file_data: Vec<(String, FileData)>) -> Result<KnowledgeBase> {
        let total_files = file_data.len();
        let total_loc: usize = file_data.iter().map(|(_, data)| data.loc).sum();

        // Calculate function, class, and method counts
        let mut total_functions = 0;
        let mut total_classes = 0;
        let mut total_methods = 0;
        let mut languages_set = std::collections::HashSet::new();

        for (_, data) in &file_data {
            total_functions += data.functions.len();
            total_classes += data.classes.len();
            total_methods += data.classes.iter()
                .map(|c| c.methods.len())
                .sum::<usize>();
            languages_set.insert(data.language.clone());
        }

        // Build file structure
        let mut structure = HashMap::new();
        for (path, data) in file_data.iter() {
            structure.insert(path.clone(), data.clone());
        }

        // Build dependency graph
        let dependency_graph = self.build_dependency_graph(&file_data);

        // Detect entry points
        let entry_points = self.detect_entry_points(&file_data);

        // Extract external dependencies
        let external_dependencies = self.extract_external_dependencies(&self.root_path)?;

        let project_name = self.root_path
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("unknown")
            .to_string();

        Ok(KnowledgeBase {
            metadata: Metadata {
                project_name,
                version: "1.0".to_string(),
                parsed_at: Utc::now().to_rfc3339(),
                languages: languages_set.into_iter().collect(),
                total_files,
                total_loc,
                total_functions,
                total_classes,
                total_methods,
            },
            structure,
            dependency_graph,
            call_graph: CallGraph::default(),
            indices: Indices::default(),
            patterns: PatternInfo::default(),
            entry_points,
            external_dependencies,
        })
    }

    fn build_dependency_graph(&self, file_data: &[(String, FileData)]) -> DependencyGraph {
        let mut nodes = Vec::new();
        let mut edges = Vec::new();

        // Build a lookup map for quick function resolution
        let mut function_map: HashMap<String, String> = HashMap::new();

        for (_, data) in file_data {
            for func in &data.functions {
                function_map.insert(func.name.clone(), func.id.clone());
            }

            for class in &data.classes {
                for method in &class.methods {
                    function_map.insert(method.name.clone(), method.id.clone());
                }
            }
        }

        // Collect all functions and classes as nodes
        for (_, data) in file_data {
            // Add function nodes
            for func in &data.functions {
                nodes.push(GraphNode {
                    id: func.id.clone(),
                    node_type: "function".to_string(),
                    name: func.name.clone(),
                });

                // Add edges for function calls
                for call in &func.calls {
                    if let Some(target_id) = function_map.get(&call.callee) {
                        edges.push(GraphEdge {
                            from: func.id.clone(),
                            to: target_id.clone(),
                            edge_type: "calls".to_string(),
                        });
                    }
                }
            }

            // Add class nodes
            for class in &data.classes {
                nodes.push(GraphNode {
                    id: class.id.clone(),
                    node_type: "class".to_string(),
                    name: class.name.clone(),
                });

                // Add method nodes and edges
                for method in &class.methods {
                    nodes.push(GraphNode {
                        id: method.id.clone(),
                        node_type: "method".to_string(),
                        name: method.name.clone(),
                    });

                    // Class contains method
                    edges.push(GraphEdge {
                        from: class.id.clone(),
                        to: method.id.clone(),
                        edge_type: "contains".to_string(),
                    });

                    // Method calls
                    for call in &method.calls {
                        if let Some(target_id) = function_map.get(&call.callee) {
                            edges.push(GraphEdge {
                                from: method.id.clone(),
                                to: target_id.clone(),
                                edge_type: "calls".to_string(),
                            });
                        }
                    }
                }
            }
        }

        DependencyGraph { nodes, edges }
    }

    fn detect_entry_points(&self, file_data: &[(String, FileData)]) -> Vec<EntryPoint> {
        let mut entry_points = Vec::new();

        for (file_path, data) in file_data {
            // Check for main() function
            for func in &data.functions {
                if func.name == "main" {
                    entry_points.push(EntryPoint {
                        entry_type: "main".to_string(),
                        path: None,
                        function: func.name.clone(),
                        handler: func.name.clone(),
                        file: file_path.clone(),
                        line: func.line_start,
                        methods: None,
                    });
                }
            }

            // Check for Flask/FastAPI app initialization
            for var in &data.global_vars {
                if let Some(value) = &var.value {
                    let value_lower = value.to_lowercase();
                    if value_lower.contains("flask(") ||
                       value_lower.contains("fastapi(") ||
                       value_lower.contains("express(") {
                        entry_points.push(EntryPoint {
                            entry_type: "api_endpoint".to_string(),
                            path: None,
                            function: var.name.clone(),
                            handler: var.name.clone(),
                            file: file_path.clone(),
                            line: var.line,
                            methods: None,
                        });
                    }
                }
            }
        }

        entry_points
    }

    fn extract_external_dependencies(&self, root_path: &Path) -> Result<Vec<ExternalDependency>> {
        let mut dependencies = Vec::new();

        // Check requirements.txt
        let req_path = root_path.join("requirements.txt");
        if req_path.exists() {
            if let Ok(content) = std::fs::read_to_string(&req_path) {
                for line in content.lines() {
                    let line = line.trim();
                    if line.is_empty() || line.starts_with('#') {
                        continue;
                    }

                    // Handle different formats: package, package==version, package>=version
                    let cleaned = line.split_whitespace().next().unwrap_or(line);

                    let (name, version) = if cleaned.contains("==") {
                        let parts: Vec<&str> = cleaned.split("==").collect();
                        (parts[0].to_string(), parts.get(1).unwrap_or(&"*").to_string())
                    } else if cleaned.contains(">=") {
                        let parts: Vec<&str> = cleaned.split(">=").collect();
                        (parts[0].to_string(), format!(">={}", parts.get(1).unwrap_or(&"*")))
                    } else if cleaned.contains("~=") {
                        let parts: Vec<&str> = cleaned.split("~=").collect();
                        (parts[0].to_string(), format!("~={}", parts.get(1).unwrap_or(&"*")))
                    } else {
                        (cleaned.to_string(), "*".to_string())
                    };

                    dependencies.push(ExternalDependency {
                        name,
                        version: Some(version),
                        source: "requirements.txt".to_string(),
                        used_by: vec!["requirements.txt".to_string()],
                        import_count: 1,
                    });
                }
            }
        }

        // Check pyproject.toml (basic parsing)
        let pyproject_path = root_path.join("pyproject.toml");
        if pyproject_path.exists() {
            if let Ok(content) = std::fs::read_to_string(&pyproject_path) {
                let lines: Vec<&str> = content.lines().collect();
                let mut in_deps = false;

                for line in lines {
                    let trimmed = line.trim();

                    if trimmed.contains("[dependencies]") ||
                       trimmed.contains("[tool.poetry.dependencies]") ||
                       trimmed.contains("[project.dependencies]") {
                        in_deps = true;
                        continue;
                    }

                    if in_deps {
                        if trimmed.starts_with('[') {
                            break;
                        }

                        if let Some(dep) = self.parse_toml_dependency(trimmed) {
                            // Avoid duplicates from requirements.txt
                            if !dependencies.iter().any(|d| d.name == dep.name) {
                                dependencies.push(dep);
                            }
                        }
                    }
                }
            }
        }

        // Check setup.py (very basic)
        let setup_path = root_path.join("setup.py");
        if setup_path.exists() && dependencies.is_empty() {
            if let Ok(content) = std::fs::read_to_string(&setup_path) {
                // Look for install_requires
                if content.contains("install_requires") {
                    dependencies.push(ExternalDependency {
                        name: "unknown".to_string(),
                        version: Some("*".to_string()),
                        source: "setup.py".to_string(),
                        used_by: vec!["setup.py".to_string()],
                        import_count: 1,
                    });
                }
            }
        }

        Ok(dependencies)
    }

    fn parse_toml_dependency(&self, line: &str) -> Option<ExternalDependency> {
        if line.is_empty() || line.starts_with('#') {
            return None;
        }

        let parts: Vec<&str> = line.split('=').collect();
        if parts.len() >= 2 {
            let name = parts[0].trim().to_string();
            let version_part = parts[1].trim().trim_matches('"').trim_matches('\'');

            // Skip python version constraints
            if name == "python" {
                return None;
            }

            Some(ExternalDependency {
                name,
                version: Some(version_part.to_string()),
                source: "pyproject.toml".to_string(),
                used_by: vec!["pyproject.toml".to_string()],
                import_count: 1,
            })
        } else {
            None
        }
    }
}
