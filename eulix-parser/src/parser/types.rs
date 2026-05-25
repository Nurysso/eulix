//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct KnowledgeBase {
    pub metadata: Metadata,
    pub structure: HashMap<String, FileData>,
    pub call_graph: CallGraph,
    pub dependency_graph: DependencyGraph,
    pub indices: Indices,
    pub entry_points: Vec<EntryPoint>,
    pub external_dependencies: Vec<ExternalDependency>,
    pub patterns: PatternInfo,
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
pub struct FileData {
    pub language: String,
    pub loc: usize,
    pub imports: Vec<Import>,
    pub functions: Vec<Function>,
    pub classes: Vec<Class>,
    pub global_vars: Vec<GlobalVar>,
    pub todos: Vec<Todo>,
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
    pub docstring: String,
    pub line_start: usize,
    pub line_end: usize,

    // Call information
    pub calls: Vec<FunctionCall>,
    pub called_by: Vec<CallerInfo>,

    // Variable tracking
    pub variables: Vec<Variable>,

    // Control flow
    pub control_flow: ControlFlow,

    // Exception handling
    pub exceptions: ExceptionInfo,

    // Metadata
    pub complexity: usize,
    pub is_async: bool,
    pub decorators: Vec<String>,
    pub tags: Vec<String>,
    pub importance_score: f32,
    #[serde(default)]
    pub lang_info: LanguageSpecificInfo,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Parameter {
    pub name: String,
    pub type_annotation: String,
    pub default_value: Option<String>,
}

// Detailed function call information
#[derive(Debug, Serialize, Deserialize, Clone, PartialEq, Eq, Hash)]
pub struct FunctionCall {
    pub callee: String,
    pub defined_in: Option<String>, // File path where callee is defined
    pub line: usize,
    pub args: Vec<String>,
    pub is_conditional: bool, // Inside if/loop/try block?
    pub context: String,      // "if", "else", "loop", "try", "unconditional"
}

// Caller information (reverse call graph)
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CallerInfo {
    pub function: String,
    pub file: String,
    pub line: usize,
}

// Variable tracking for data flow
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Variable {
    pub name: String,
    pub var_type: Option<String>,
    pub scope: String, // "param", "local", "global"
    pub defined_at: Option<usize>,
    pub transformations: Vec<VarTransformation>,
    pub used_in: Vec<String>, // Function calls that use this variable
    pub returned: bool,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct VarTransformation {
    pub line: usize,
    pub via: String,     // Function that transforms it
    pub becomes: String, // New variable name
}

// Control flow structure
#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct ControlFlow {
    pub complexity: usize, // Cyclomatic complexity
    pub branches: Vec<Branch>,
    pub loops: Vec<Loop>,
    pub try_blocks: Vec<TryBlock>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Branch {
    pub branch_type: String, // "if", "elif", "else", "match"
    pub condition: String,
    pub line: usize,
    pub true_path: ExecutionPath,
    pub false_path: Option<ExecutionPath>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ExecutionPath {
    pub calls: Vec<String>,
    pub returns: Option<String>,
    pub raises: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Loop {
    pub loop_type: String, // "for", "while"
    pub condition: String,
    pub line: usize,
    pub calls: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct TryBlock {
    pub line: usize,
    pub try_calls: Vec<String>,
    pub except_clauses: Vec<ExceptClause>,
    pub finally_calls: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ExceptClause {
    pub exception_type: String,
    pub line: usize,
    pub calls: Vec<String>,
}

//  Exception information
#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct ExceptionInfo {
    pub raises: Vec<String>,     // Explicitly raised exceptions
    pub propagates: Vec<String>, // Exceptions from called functions
    pub handles: Vec<String>,    // Exception types caught in try-except
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Class {
    pub id: String,
    pub name: String,
    pub bases: Vec<String>,
    pub docstring: String,
    pub line_start: usize,
    pub line_end: usize,
    pub methods: Vec<Function>,
    pub attributes: Vec<Attribute>,
    pub decorators: Vec<String>,
    #[serde(default)]
    pub lang_info: LanguageSpecificInfo,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Attribute {
    pub name: String,
    pub type_annotation: String,
    pub value: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct GlobalVar {
    pub name: String,
    pub type_annotation: String,
    pub value: Option<String>,
    pub line: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Todo {
    pub line: usize,
    pub text: String,
    pub priority: String, // "high", "medium", "low"
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct SecurityNote {
    pub note_type: String,
    pub line: usize,
    pub description: String,
}

// Call graph structure
#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct CallGraph {
    pub nodes: Vec<CallGraphNode>,
    pub edges: Vec<CallGraphEdge>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CallGraphNode {
    pub id: String,
    pub node_type: String, // "function", "method", "class"
    pub file: String,
    pub is_entry_point: bool,
    pub call_count_estimate: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CallGraphEdge {
    pub from: String,
    pub to: String,
    pub edge_type: String, // "calls", "inherits", "uses"
    pub conditional: bool,
    pub call_site_line: usize,
}

// Dependency graph structure (missing from original)
#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct DependencyGraph {
    pub nodes: Vec<GraphNode>,
    pub edges: Vec<GraphEdge>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct GraphNode {
    pub id: String,
    pub node_type: String, // "file", "module", "package"
    pub name: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct GraphEdge {
    pub from: String,
    pub to: String,
    pub edge_type: String, // "imports", "depends_on"
}

// Fast lookup indices
#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct Indices {
    pub functions_by_name: HashMap<String, Vec<String>>, // name -> [file:line]
    pub functions_calling: HashMap<String, Vec<String>>, // callee -> [callers]
    pub functions_by_tag: HashMap<String, Vec<String>>,
    pub types_by_name: HashMap<String, Vec<String>>,
    pub files_by_category: HashMap<String, Vec<String>>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct EntryPoint {
    pub entry_type: String,   // "api_endpoint", "cli_command", "main"
    pub path: Option<String>, // API path or CLI command
    pub function: String,     // Added missing field
    pub handler: String,
    pub file: String,
    pub line: usize,
    pub methods: Option<Vec<String>>, // HTTP methods for API endpoints
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ExternalDependency {
    pub name: String,
    pub version: Option<String>,
    pub source: String,       // Added missing field
    pub used_by: Vec<String>, // Files that import this
    pub import_count: usize,
}

#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct PatternInfo {
    pub naming_convention: String,
    pub structure_type: String,
    pub architecture_style: Option<String>, // "layered", "microservices", "mvc"
}

/// Language-specific metadata that doesn't apply universally.
/// Each parser populates only the variant relevant to its language.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct LanguageSpecificInfo {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub python: Option<PythonInfo>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub rust: Option<RustInfo>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub go: Option<GoInfo>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub typescript: Option<TypeScriptInfo>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub c: Option<CInfo>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cpp: Option<CppInfo>,
}

pub struct RustInfo {
    pub is_unsafe: bool,
    pub is_pub: bool,
    pub is_pub_crate: bool,
    pub is_const_fn: bool,
    pub is_async: bool,             // async fn
    pub is_extern: bool,            // extern "C" fn
    pub lifetimes: Vec<String>,     // e.g. ["'a", "'b"]
    pub derives: Vec<String>,       // #[derive(Debug, Clone, ...)]
    pub is_test: bool,              // #[test]
    pub is_bench: bool,             // #[bench]
    pub cfg_attrs: Vec<String>,     // #[cfg(target_os = "linux")] etc.
    pub unknown_attrs: Vec<String>, // catch-all for unrecognized #[...]
}

pub struct GoInfo {
    pub is_exported: bool,             // name starts with uppercase
    pub receiver_type: Option<String>, // e.g. "(*MyStruct)" for methods
    pub is_interface_method: bool,
    pub build_tags: Vec<String>,    // //go:build linux && amd64
    pub go_directives: Vec<String>, // //go:generate, //go:noescape etc.
}

pub struct TypeScriptInfo {
    pub is_async: bool,
    pub is_exported: bool,
    pub is_default_export: bool,
    pub is_abstract: bool,
    pub access_modifier: Option<String>, // "public" | "private" | "protected"
    pub is_readonly: bool,
    pub is_optional: bool,           // optional method/property (?)
    pub decorators: Vec<String>,     // @Component, @Injectable etc.
    pub generic_params: Vec<String>, // e.g. ["T", "K extends string"]
    pub is_arrow_fn: bool,           // const foo = () => ...
    pub is_overload: bool,           // TS function overloads
}

pub struct CInfo {
    pub is_static: bool, // file-scoped linkage
    pub is_inline: bool,
    pub is_extern: bool,
    pub is_variadic: bool,                  // printf-style ...
    pub calling_convention: Option<String>, // __cdecl, __stdcall etc.
}

pub struct CppInfo {
    pub is_static: bool,
    pub is_inline: bool,
    pub is_virtual: bool,
    pub is_pure_virtual: bool, // = 0
    pub is_override: bool,     // override keyword
    pub is_final: bool,        // final keyword
    pub is_const_method: bool, // void foo() const
    pub is_noexcept: bool,
    pub is_explicit: bool, // explicit constructors
    pub is_constexpr: bool,
    pub is_constructor: bool,
    pub is_destructor: bool,
    pub access_specifier: Option<String>, // "public" | "private" | "protected"
    pub template_params: Vec<String>,     // e.g. ["typename T", "int N"]
}

/// Python-specific metadata for a function or class.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PythonInfo {
    pub is_dataclass: bool,
    pub is_staticmethod: bool,
    pub is_classmethod: bool,
    pub is_property: bool,
    pub is_property_setter: bool,
    pub is_property_deleter: bool,
    pub is_abstractmethod: bool,
    pub is_cached_property: bool,
    pub is_overload: bool,
    pub is_override: bool,               // typing.override (Python 3.12+)
    pub is_final: bool,                  // typing.final
    pub flask_route: Option<String>,     // e.g. "/users/<id>"
    pub unknown_decorators: Vec<String>, // catch-all for anything unrecognized
}
