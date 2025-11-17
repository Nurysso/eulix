// eulix_embed/src/kb_loader.rs
use anyhow::Result;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs::File;
use std::io::BufReader;
use std::path::Path;

/// Root structure matching the new JSON schema
#[derive(Debug, Serialize, Deserialize)]
pub struct KnowledgeBase {
    pub metadata: Metadata,
    pub structure: HashMap<String, FileStructure>,
    pub call_graph: CallGraph,
    pub dependency_graph: DependencyGraph,
    pub indices: Indices,
    pub entry_points: Vec<EntryPoint>,
    pub external_dependencies: Vec<ExternalDependency>,
    pub patterns: Patterns,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Metadata {
    pub project_name: String,
    pub version: String,
    pub parsed_at: String,
    pub languages: Vec<String>,
    pub total_files: usize,
    pub total_loc: usize,
    pub total_functions: usize,
    pub total_classes: usize,
    pub total_methods: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct FileStructure {
    pub language: String,
    pub loc: usize,
    pub imports: Vec<Import>,
    pub functions: Vec<Function>,
    pub classes: Vec<Class>,
    pub global_vars: Vec<GlobalVar>,
    #[serde(default)]
    pub todos: Vec<Todo>,
    #[serde(default)]
    pub security_notes: Vec<SecurityNote>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Import {
    pub module: String,
    pub items: Vec<String>,
    #[serde(rename = "type")]
    pub import_type: String, // "external" | "internal"
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Function {
    pub id: String,
    pub name: String,
    pub signature: String,
    pub params: Vec<Parameter>,
    pub return_type: String,
    #[serde(default)]
    pub docstring: String,
    pub line_start: usize,
    pub line_end: usize,
    #[serde(default)]
    pub calls: Vec<FunctionCall>,
    #[serde(default)]
    pub called_by: Vec<CalledBy>,
    #[serde(default)]
    pub variables: Vec<Variable>,
    #[serde(default)]
    pub control_flow: ControlFlow,
    #[serde(default)]
    pub exceptions: Exceptions,
    #[serde(default)]
    pub complexity: usize,
    #[serde(default)]
    pub is_async: bool,
    #[serde(default)]
    pub decorators: Vec<String>,
    #[serde(default)]
    pub tags: Vec<String>,
    #[serde(default)]
    pub importance_score: f32,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Parameter {
    pub name: String,
    #[serde(default)]
    pub type_annotation: String,
    pub default_value: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct FunctionCall {
    pub callee: String,
    pub defined_in: Option<String>,
    pub line: usize,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub is_conditional: bool,
    pub context: String, // "if" | "else" | "loop" | "try" | "unconditional"
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CalledBy {
    pub function: String,
    pub file: String,
    pub line: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Variable {
    pub name: String,
    pub var_type: Option<String>,
    pub scope: String, // "param" | "local" | "global"
    pub defined_at: Option<usize>,
    #[serde(default)]
    pub transformations: Vec<Transformation>,
    #[serde(default)]
    pub used_in: Vec<String>,
    #[serde(default)]
    pub returned: bool,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Transformation {
    pub line: usize,
    pub via: String,
    pub becomes: String,
}

#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct ControlFlow {
    #[serde(default)]
    pub complexity: usize,
    #[serde(default)]
    pub branches: Vec<Branch>,
    #[serde(default)]
    pub loops: Vec<Loop>,
    #[serde(default)]
    pub try_blocks: Vec<TryBlock>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Branch {
    pub branch_type: String, // "if" | "elif" | "else" | "match"
    pub condition: String,
    pub line: usize,
    pub true_path: PathInfo,
    pub false_path: Option<PathInfo>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct PathInfo {
    #[serde(default)]
    pub calls: Vec<String>,
    pub returns: Option<String>,
    pub raises: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Loop {
    pub loop_type: String, // "for" | "while"
    pub condition: String,
    pub line: usize,
    #[serde(default)]
    pub calls: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct TryBlock {
    pub line: usize,
    #[serde(default)]
    pub try_calls: Vec<String>,
    #[serde(default)]
    pub except_clauses: Vec<ExceptClause>,
    #[serde(default)]
    pub finally_calls: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ExceptClause {
    pub exception_type: String,
    pub line: usize,
    #[serde(default)]
    pub calls: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct Exceptions {
    #[serde(default)]
    pub raises: Vec<String>,
    #[serde(default)]
    pub propagates: Vec<String>,
    #[serde(default)]
    pub handles: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Class {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub bases: Vec<String>,
    #[serde(default)]
    pub docstring: String,
    pub line_start: usize,
    pub line_end: usize,
    #[serde(default)]
    pub methods: Vec<Function>,
    #[serde(default)]
    pub attributes: Vec<Attribute>,
    #[serde(default)]
    pub decorators: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Attribute {
    pub name: String,
    #[serde(default)]
    pub type_annotation: String,
    pub value: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct GlobalVar {
    pub name: String,
    #[serde(default)]
    pub type_annotation: String,
    pub value: Option<String>,
    pub line: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Todo {
    pub line: usize,
    pub text: String,
    pub priority: String, // "high" | "medium" | "low"
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct SecurityNote {
    pub note_type: String,
    pub line: usize,
    pub description: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CallGraph {
    pub nodes: Vec<CallGraphNode>,
    pub edges: Vec<CallGraphEdge>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CallGraphNode {
    pub id: String,
    pub node_type: String, // "function" | "method" | "class"
    pub file: String,
    #[serde(default)]
    pub is_entry_point: bool,
    #[serde(default)]
    pub call_count_estimate: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CallGraphEdge {
    pub from: String,
    pub to: String,
    pub edge_type: String, // "calls" | "inherits" | "uses"
    #[serde(default)]
    pub conditional: bool,
    pub call_site_line: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct DependencyGraph {
    pub nodes: Vec<DependencyNode>,
    pub edges: Vec<DependencyEdge>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct DependencyNode {
    pub id: String,
    pub node_type: String, // "file" | "module" | "package"
    pub name: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct DependencyEdge {
    pub from: String,
    pub to: String,
    pub edge_type: String, // "imports" | "depends_on"
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Indices {
    #[serde(default)]
    pub functions_by_name: HashMap<String, Vec<String>>,
    #[serde(default)]
    pub functions_calling: HashMap<String, Vec<String>>,
    #[serde(default)]
    pub functions_by_tag: HashMap<String, Vec<String>>,
    #[serde(default)]
    pub types_by_name: HashMap<String, Vec<String>>,
    #[serde(default)]
    pub files_by_category: HashMap<String, Vec<String>>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct EntryPoint {
    pub entry_type: String, // "api_endpoint" | "cli_command" | "main"
    pub path: Option<String>,
    pub function: String,
    pub handler: String,
    pub file: String,
    pub line: usize,
    pub methods: Option<Vec<String>>, // HTTP methods for API endpoints
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ExternalDependency {
    pub name: String,
    pub version: Option<String>,
    pub source: String,
    #[serde(default)]
    pub used_by: Vec<String>,
    #[serde(default)]
    pub import_count: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Patterns {
    #[serde(default)]
    pub naming_convention: String,
    #[serde(default)]
    pub structure_type: String,
    pub architecture_style: Option<String>, // "layered" | "microservices" | "mvc"
}

pub fn load_knowledge_base(path: &Path) -> Result<KnowledgeBase> {
    let file = File::open(path)?;
    let reader = BufReader::new(file);
    let kb = serde_json::from_reader(reader)?;
    Ok(kb)
}

impl KnowledgeBase {
    /// Get all functions across all files
    pub fn all_functions(&self) -> Vec<(&String, &Function)> {
        self.structure
            .iter()
            .flat_map(|(file_path, file_struct)| {
                file_struct.functions.iter().map(move |func| (file_path, func))
            })
            .collect()
    }

    /// Get all classes across all files
    pub fn all_classes(&self) -> Vec<(&String, &Class)> {
        self.structure
            .iter()
            .flat_map(|(file_path, file_struct)| {
                file_struct.classes.iter().map(move |class| (file_path, class))
            })
            .collect()
    }

    /// Get all methods from all classes
    pub fn all_methods(&self) -> Vec<(&String, &Class, &Function)> {
        self.structure
            .iter()
            .flat_map(|(file_path, file_struct)| {
                file_struct.classes.iter().flat_map(move |class| {
                    class.methods.iter().map(move |method| (file_path, class, method))
                })
            })
            .collect()
    }

    /// Get function by ID
    pub fn get_function(&self, id: &str) -> Option<(&String, &Function)> {
        self.all_functions().into_iter().find(|(_, func)| func.id == id)
    }

    /// Get class by ID
    pub fn get_class(&self, id: &str) -> Option<(&String, &Class)> {
        self.all_classes().into_iter().find(|(_, class)| class.id == id)
    }

    /// Get functions by name from indices
    pub fn functions_by_name(&self, name: &str) -> Vec<String> {
        self.indices
            .functions_by_name
            .get(name)
            .cloned()
            .unwrap_or_default()
    }

    /// Get entry points of a specific type
    pub fn entry_points_by_type(&self, entry_type: &str) -> Vec<&EntryPoint> {
        self.entry_points
            .iter()
            .filter(|ep| ep.entry_type == entry_type)
            .collect()
    }

    /// Get external dependencies used by a file
    pub fn dependencies_for_file(&self, file_path: &str) -> Vec<&ExternalDependency> {
        self.external_dependencies
            .iter()
            .filter(|dep| dep.used_by.iter().any(|f| f == file_path))
            .collect()
    }

    /// Get call graph edges for a function
    pub fn get_calls_from(&self, function_id: &str) -> Vec<&CallGraphEdge> {
        self.call_graph
            .edges
            .iter()
            .filter(|edge| edge.from == function_id)
            .collect()
    }

    /// Get functions that call a specific function
    pub fn get_calls_to(&self, function_id: &str) -> Vec<&CallGraphEdge> {
        self.call_graph
            .edges
            .iter()
            .filter(|edge| edge.to == function_id)
            .collect()
    }

    /// Get all entry point functions
    pub fn get_entry_point_functions(&self) -> Vec<&Function> {
        let entry_point_ids: Vec<String> = self.entry_points.iter().map(|ep| ep.function.clone()).collect();

        self.all_functions()
            .into_iter()
            .filter(|(_, func)| entry_point_ids.contains(&func.id))
            .map(|(_, func)| func)
            .collect()
    }
}
