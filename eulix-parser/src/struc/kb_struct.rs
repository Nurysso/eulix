//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

// kb.json structure
#[derive(Serialize)]
pub struct KnowledgeBaseSimplifiedRef<'a> {
    pub metadata: &'a Metadata,
    pub structure: &'a HashMap<String, FileData>,
}

// kb_index.json(*_index.json) structure
#[derive(Serialize)]
pub struct IndexDataRef<'a> {
    pub indices: &'a Indices,
}

// kb_call_graph.json (*_call_graph.json) structure
#[derive(Serialize)]
pub struct CallGraphRef<'a> {
    pub nodes: &'a [CallGraphNode],
    pub edges: &'a [CallGraphEdge],
}
//kb_call_graph.json
#[derive(Serialize)]
pub struct EntryPointsRef<'a> {
    pub entry_points: &'a [EntryPoint],
}

/// kb_external_deps.json
#[derive(Serialize)]
pub struct ExternalDepsRef<'a> {
    pub external_dependencies: &'a [ExternalDependency],
}

/// kb_patterns.json
#[derive(Serialize)]
pub struct PatternsRef<'a> {
    pub patterns: &'a PatternInfo,
}

/// kb_metrics.json metadata + top-K complex functions
#[derive(Serialize)]
pub struct MetricsReport<'a> {
    pub metadata: &'a Metadata,
    pub top_complex_functions: Vec<FunctionMetric<'a>>,
}

#[derive(Serialize)]
pub struct FunctionMetric<'a> {
    pub name: &'a str,
    pub file: &'a str,
    pub complexity: usize,
    pub importance_score: f32,
    pub line_start: usize,
    pub line_end: usize,
}

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
    pub git_hash: String,
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


#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct RustInfo {
    pub is_unsafe: bool,
    pub is_pub: bool,
    pub is_pub_crate: bool,
    pub is_const_fn: bool,
    pub is_async: bool,               // async fn
    pub is_extern: bool,              // extern "C" fn / extern fn
    pub abi: Option<String>,          // e.g. "C" in `extern "C" fn`

    pub lifetimes: Vec<String>,       // e.g. ["'a", "'b"]
    pub generics: Vec<String>,        // non-lifetime type params, e.g. ["T", "K: Clone"]
    pub where_clause: Option<String>, // raw text of a `where ...` clause, if present
    pub is_generic: bool,             // true if lifetimes or generics is non-empty

    pub derives: Vec<String>,         // #[derive(Debug, Clone, ...)]
    pub is_test: bool,                // #[test]
    pub is_bench: bool,               // #[bench]
    pub cfg_attrs: Vec<String>,       // #[cfg(target_os = "linux")] etc.
    pub unknown_attrs: Vec<String>,   // catch-all for unrecognized #[...] (incl. proc-macro attrs)

    // Trait / impl relationships (populated on functions & methods)
    /// The trait this method belongs to: `Some("Display")` for a method inside
    /// `impl Display for Foo`, or for a method declared inside `trait Display { .. }` itself.
    pub trait_name: Option<String>,
    /// True when this method lives inside an `impl Trait for Type` block.
    pub is_trait_impl_method: bool,
    /// True when this method is declared *inside a trait definition* and has a
    /// default body (as opposed to a required/abstract method signature).
    pub is_trait_default_method: bool,

    // Operator overloading, derived from trait_name
    pub is_operator_overload: bool,
    pub overloaded_operator: Option<String>, // e.g. "+", "==", "[]"

    // Item classification (populated on struct/enum/union/trait entries)
    /// "struct" | "tuple_struct" | "unit_struct" | "enum" | "union" | "trait"
    pub item_kind: Option<String>,
    pub supertraits: Vec<String>, // `trait Foo: Bar + Baz`
    pub is_marker_trait: bool,    // trait with no methods (marker/auto-trait-like)

    // Function-body signals
    pub uses_try_operator: bool,  // `?` propagation present somewhere in the body
    pub macro_calls: Vec<String>, // vec!, println!, format!, custom_macro!, etc. invoked in body
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct GoInfo {
    pub is_exported: bool,             // name starts with uppercase
    pub receiver_type: Option<String>, // e.g. "*MyStruct" for pointer receivers
    pub receiver_name: Option<String>, // e.g. "s" in `func (s *Server) Serve()`
    pub is_interface_method: bool,
    pub spawns_goroutines: bool,       // contains `go` statements
    pub uses_channels: bool,           // sends/receives on a chan
    pub uses_select: bool,             // contains a select statement
    pub uses_mutex: bool,              // sync.Mutex / sync.RWMutex usage
    pub uses_waitgroup: bool,          // sync.WaitGroup usage
    pub uses_atomic: bool,             // sync/atomic usage
    pub returns_error: bool,           // last return type is `error`
    pub uses_panic: bool,
    pub uses_recover: bool,
    pub defer_count: usize,            // number of `defer` statements
    pub type_params: Vec<String>,      // e.g. ["T any", "K comparable"]
    pub type_constraints: Vec<String>, // e.g. ["comparable", "~int | ~string"]
    pub build_tags: Vec<String>,       // //go:build linux && amd64
    pub go_directives: Vec<String>,    // //go:generate, //go:noescape, //go:linkname …
    pub uses_cgo: bool,                // import "C" present in file
    pub embed_patterns: Vec<String>,   // //go:embed *.html → ["*.html"]
    pub is_variadic: bool,             // last param is `...T`
    pub type_kind: Option<GoTypeKind>,
    pub is_pointer_receiver: bool,     // for method entries: with receiver *T ?
    pub has_embedded_types: bool,      // struct embeds another type
}

#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum GoTypeKind {
    #[default]
    Struct,
    Interface,
    Function,
    Method,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct TypeScriptInfo {
    pub is_async: bool,
    pub is_exported: bool,
    pub is_default_export: bool,
    pub is_abstract: bool,
    pub access_modifier: Option<String>, // "public" | "private" | "protected"
    pub is_readonly: bool,
    pub is_optional: bool,               // optional method/property (?)
    pub decorators: Vec<String>,         // @Component, @Injectable etc.
    pub generic_params: Vec<String>,     // e.g. ["T", "K extends string"]
    pub is_arrow_fn: bool,               // const foo = () => ...
    pub is_overload: bool,               // TS function overloads
}

#[derive(Debug, Default, Clone, Serialize, Deserialize)]
pub struct CInfo {
    pub is_static: bool,                    // file-scoped linkage
    pub is_inline: bool,
    pub is_extern: bool,
    pub is_variadic: bool,                  // printf-style ...
    pub calling_convention: Option<String>, // __cdecl, __stdcall etc.
}

#[derive(Debug, Default, Clone, Serialize, Deserialize)]
pub struct CppInfo {
    pub is_static: bool,
    pub is_inline: bool,
    pub is_virtual: bool,
    pub is_pure_virtual: bool,
    pub is_override: bool,
    pub is_final: bool,
    pub is_const_method: bool,
    pub is_noexcept: bool,
    pub is_explicit: bool,
    pub is_constexpr: bool,
    pub is_constructor: bool,
    pub is_destructor: bool,
    pub access_specifier: Option<String>,
    pub template_params: Vec<String>,

    pub type_kind: CppTypeKind, // for Class entries
    // Struct/Union specific
    pub is_pod: bool,           // no user-defined ctor/dtor/virtual → plain-old-data
    pub is_packed: bool,        // __attribute__((packed)) or #pragma pack
    pub has_vtable: bool,       // has at least one virtual method

    // Enum specific
    pub is_scoped_enum: bool,            // enum class / enum struct
    pub is_flags_enum: bool,             // bit-flag pattern detected
    pub underlying_type: Option<String>, // enum Foo : uint8_t → "uint8_t"

    // Template / generic
    pub is_template: bool,
    pub is_partial_specialization: bool,
    pub is_explicit_specialization: bool,
    pub concept_constraints: Vec<String>, // requires clauses / concept names

    // Linkage / storage
    pub is_extern_c: bool,                // extern "C" linkage
    pub is_thread_local: bool,            // thread_local storage
    pub is_consteval: bool,
    pub is_constinit: bool,

    // Operator overload
    pub is_operator_overload: bool,
    pub overloaded_operator: Option<String>, // e.g. "+", "[]", "()"

    // Inheritance (populated for Class entries)
    pub is_abstract: bool,                // has at least one pure virtual
    pub inheritance_type: Option<String>, // "public" | "protected" | "private"
}

#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum CppTypeKind {
    #[default]
    Function,
    Struct,
    Union,
    Enum,
    Class,
}

/// Python-specific metadata for a function or class.
#[derive(Debug, Default, Clone, Serialize, Deserialize)]
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
