//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::struc::kb_struct::*;
use once_cell::sync::Lazy;
use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use tree_sitter::{Node, Parser};

// Regex Patterns Compiled once
struct SecurityPattern {
    regex: &'static Lazy<Regex>,
    note_type: &'static str,
    description: &'static str,
}
struct TagRule {
    keywords: &'static [&'static str],
    tag: &'static str,
    check_docstring: bool,
}

static UNSAFE_STRING_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"strcpy|strcat|sprintf|vsprintf|gets").unwrap());
static COMMAND_EXEC_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"system\(|popen\(|exec").unwrap());
static MANUAL_MEMORY_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"malloc|calloc|realloc|free").unwrap());
static UNSAFE_INPUT_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"scanf|fscanf").unwrap());
static MEMORY_OP_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"memcpy|memmove|memset").unwrap());
static PRIVILEGE_CHANGE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"setuid|setgid|seteuid").unwrap());
static WEAK_RANDOM_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"rand\(\)|random\(\)").unwrap());

static INCLUDE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"^#include\s+[<"]([^>"]+)[>"]"#).expect("Invalid include regex"));

static TODO_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?://|/\*)\s*TODO:?\s*(.+?)(?:\*/|$)").expect("Invalid todo regex"));
static MACRO_DEFINE_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^#define\s+([A-Za-z_]\w*)(?:\([^)]*\))?\s+(.+)$")
        .expect("Invalid macro define regex")
});
static MALLOC_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"malloc|calloc|realloc|alloca").unwrap());
static FREE_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\bfree\b").unwrap());
static PTHREAD_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"pthread|fork|thread").unwrap());
static SYSCALL_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"syscall|ioctl|fcntl").unwrap());
static STRING_OPS_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"strcpy|strcat|sprintf|strncpy").unwrap());

static SECURITY_PATTERNS: Lazy<Vec<SecurityPattern>> = Lazy::new(|| {
    vec![
        SecurityPattern {
            regex: &UNSAFE_STRING_RE,
            note_type: "unsafe_string",
            description: "Uses unsafe string function",
        },
        SecurityPattern {
            regex: &COMMAND_EXEC_RE,
            note_type: "command_execution",
            description: "System command execution",
        },
        SecurityPattern {
            regex: &MANUAL_MEMORY_RE,
            note_type: "manual_memory",
            description: "Manual memory management",
        },
        SecurityPattern {
            regex: &UNSAFE_INPUT_RE,
            note_type: "unsafe_input",
            description: "Potentially unsafe input function",
        },
        SecurityPattern {
            regex: &MEMORY_OP_RE,
            note_type: "memory_operation",
            description: "Direct memory operation",
        },
        SecurityPattern {
            regex: &PRIVILEGE_CHANGE_RE,
            note_type: "privilege_change",
            description: "Changes privilege level",
        },
        SecurityPattern {
            regex: &WEAK_RANDOM_RE,
            note_type: "weak_random",
            description: "Weak random number generator",
        },
    ]
});

static TAG_RULES: Lazy<Vec<TagRule>> = Lazy::new(|| {
    vec![
        TagRule {
            keywords: &["init", "setup", "initialize", "bootstrap"],
            tag: "initialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &["free", "cleanup", "destroy", "dispose", "close", "shutdown"],
            tag: "cleanup",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "auth", "login", "logout", "password", "hash", "encrypt", "decrypt", "token",
            ],
            tag: "authentication",
            check_docstring: true,
        },
        TagRule {
            keywords: &["api", "endpoint", "route", "handler", "serve"],
            tag: "api",
            check_docstring: true,
        },
        TagRule {
            keywords: &[
                "db", "database", "query", "select", "insert", "update", "delete",
            ],
            tag: "database",
            check_docstring: false,
        },
        TagRule {
            keywords: &["validate", "check", "verify", "sanitize"],
            tag: "validation",
            check_docstring: false,
        },
        TagRule {
            keywords: &["error"],
            tag: "error-handling",
            check_docstring: false,
        },
        TagRule {
            keywords: &["util", "helper"],
            tag: "utility",
            check_docstring: false,
        },
        TagRule {
            keywords: &["read", "write", "file", "open"],
            tag: "file-io",
            check_docstring: false,
        },
        TagRule {
            keywords: &["socket", "connect", "send", "receive"],
            tag: "network",
            check_docstring: false,
        },
        TagRule {
            keywords: &["config", "setting"],
            tag: "configuration",
            check_docstring: false,
        },
        TagRule {
            keywords: &["log", "debug"],
            tag: "logging",
            check_docstring: false,
        },
        TagRule {
            keywords: &["parse", "decode"],
            tag: "parsing",
            check_docstring: false,
        },
        TagRule {
            keywords: &["serialize", "encode"],
            tag: "serialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &["signal"],
            tag: "signal-handling",
            check_docstring: false,
        },
    ]
});

pub struct CParser {
    source_code: String,
    /// macro_name -> expansion text (best-effort from #define)
    macro_defs: HashMap<String, String>,
    /// function_ptr_var -> resolved callee name (best-effort)
    fn_ptr_map: HashMap<String, String>,
}

impl CParser {
    pub fn new(source_code: String) -> Self {
        let macro_defs = Self::pre_scan_macros(&source_code);
        Self {
            source_code,
            macro_defs,
            fn_ptr_map: HashMap::new(),
        }
    }

    // Pre-scan: collect #define NAME(...) body
    fn pre_scan_macros(src: &str) -> HashMap<String, String> {
        let mut map = HashMap::new();
        // Matches both object-like and function-like macros, handles line continuations
        for caps in MACRO_DEFINE_RE.captures_iter(src) {
            let name = caps[1].to_string();
            let body = caps[2].trim_end_matches('\\').trim().to_string();
            map.insert(name, body);
        }
        map
    }

    pub fn parse(&mut self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_c::language())
            .map_err(|e| format!("Failed to load C grammar: {}", e))?;

        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse C file".to_string())?;

        let root = tree.root_node();

        // First pass: build function-pointer assignment map
        self.fn_ptr_map = self.build_fn_ptr_map(&root);
        // We need a mutable self here; work around by passing map into call extraction
        Ok(FileData {
            language: "c".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(&root),
            functions: self.extract_functions(&root, &self.fn_ptr_map),
            classes: self.extract_structs(&root),
            global_vars: self.extract_global_vars(&root),
            todos: self.extract_todos(),
            security_notes: self.detect_security_patterns(),
        })
    }

    // Function-pointer map
    /// Walk all assignment expressions looking for:
    ///   fp = some_func;  or  fp = &some_func;
    fn build_fn_ptr_map(&self, root: &Node) -> HashMap<String, String> {
        let mut map = HashMap::new();
        self.scan_fn_ptr_assignments(root, &mut map);
        map
    }

    fn scan_fn_ptr_assignments(&self, node: &Node, map: &mut HashMap<String, String>) {
        let mut cursor = node.walk();
        if node.kind() == "assignment_expression" {
            let lhs = node.child_by_field_name("left");
            let rhs = node.child_by_field_name("right");
            if let (Some(l), Some(r)) = (lhs, rhs) {
                let lhs_text = self.get_node_text(&l);
                let rhs_text = self
                    .get_node_text(&r)
                    .trim_start_matches('&')
                    .trim()
                    .to_string();
                // Only record if rhs looks like a plain identifier (a function name)
                if rhs_text.chars().all(|c| c.is_alphanumeric() || c == '_') {
                    map.insert(lhs_text, rhs_text);
                }
            }
        }
        // Also handle initializer:  fn_ptr_t fp = actual_func;
        if node.kind() == "init_declarator" {
            if let (Some(decl), Some(val)) = (
                node.child_by_field_name("declarator"),
                node.child_by_field_name("value"),
            ) {
                let name = self.extract_declarator_name(&decl);
                let val_text = self
                    .get_node_text(&val)
                    .trim_start_matches('&')
                    .trim()
                    .to_string();
                if !name.is_empty() && val_text.chars().all(|c| c.is_alphanumeric() || c == '_') {
                    map.insert(name, val_text);
                }
            }
        }
        for child in node.children(&mut cursor) {
            self.scan_fn_ptr_assignments(&child, map);
        }
    }

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "preproc_include" {
                let text = self.get_node_text(&child);
                if let Some(caps) = INCLUDE_RE.captures(&text) {
                    let path = caps[1].to_string();
                    let is_system = text.contains('<');
                    imports.push(Import {
                        module: path.clone(),
                        items: vec![],
                        import_type: self.classify_import(&path, is_system),
                    });
                }
            }
        }
        imports
    }

    fn classify_import(&self, path: &str, is_system: bool) -> String {
        let stdlib = [
            "stdio.h",
            "stdlib.h",
            "string.h",
            "math.h",
            "time.h",
            "ctype.h",
            "stdint.h",
            "stdbool.h",
            "stddef.h",
            "assert.h",
            "errno.h",
            "limits.h",
            "float.h",
            "stdarg.h",
            "signal.h",
            "setjmp.h",
            "locale.h",
            "wchar.h",
            "wctype.h",
            "complex.h",
            "fenv.h",
            "inttypes.h",
            "iso646.h",
            "stdalign.h",
            "threads.h",
            "unistd.h",
            "pthread.h",
            "sys/",
            "fcntl.h",
        ];
        if stdlib.iter().any(|s| path.starts_with(s)) || is_system {
            "stdlib".to_string()
        } else if path.starts_with('.') || !path.contains('/') {
            "internal".to_string()
        } else {
            "external".to_string()
        }
    }

    fn extract_functions(
        &self,
        root: &Node,
        fn_ptr_map: &HashMap<String, String>,
    ) -> Vec<Function> {
        let mut functions = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "function_definition" {
                if let Some(func) = self.parse_function(&child, "", fn_ptr_map) {
                    functions.push(func);
                }
            }
        }
        functions
    }

    fn parse_function(
        &self,
        node: &Node,
        struct_context: &str,
        fn_ptr_map: &HashMap<String, String>,
    ) -> Option<Function> {
        let declarator = node.child_by_field_name("declarator")?;
        let name = self.extract_function_name(&declarator)?;

        let params = self.extract_parameters(&declarator);
        let return_type = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_else(|| "void".to_string());

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);
        let signature = self.build_signature(&name, &params, &return_type);

        let body = node.child_by_field_name("body")?;
        let calls = self.extract_function_calls_detailed(&body, fn_ptr_map);
        let variables = self.extract_variables(&body, &params);
        let control_flow = self.build_control_flow(&body);
        let exceptions = ExceptionInfo::default();
        let complexity = self.calculate_complexity(&body);

        let id = if struct_context.is_empty() {
            format!("func_{}", name)
        } else {
            format!("method_{}_{}", struct_context, name)
        };

        let tags = self.auto_tag_function(&name, &docstring, &calls, &return_type);
        let importance_score = self.estimate_importance(&name, &return_type);

        Some(Function {
            id,
            name,
            signature,
            params,
            return_type,
            docstring,
            line_start,
            line_end,
            calls,
            called_by: vec![],
            variables,
            control_flow,
            exceptions,
            complexity,
            is_async: false,
            decorators: vec![],
            tags,
            importance_score,
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    fn extract_function_name(&self, declarator: &Node) -> Option<String> {
        match declarator.kind() {
            "function_declarator" => {
                if let Some(decl) = declarator.child_by_field_name("declarator") {
                    self.extract_function_name(&decl)
                } else {
                    None
                }
            }
            "pointer_declarator" => {
                if let Some(decl) = declarator.child_by_field_name("declarator") {
                    self.extract_function_name(&decl)
                } else {
                    None
                }
            }
            "identifier" => Some(self.get_node_text(declarator)),
            _ => None,
        }
    }

    fn extract_parameters(&self, declarator: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();

        fn find_params(node: &Node, parser: &CParser, params: &mut Vec<Parameter>) {
            if node.kind() == "parameter_list" {
                let mut cursor = node.walk();
                for child in node.children(&mut cursor) {
                    if child.kind() == "parameter_declaration" {
                        let type_node = child.child_by_field_name("type");
                        let declarator_node = child.child_by_field_name("declarator");

                        let type_annotation = type_node
                            .map(|t| parser.get_node_text(&t))
                            .unwrap_or_default();

                        let name = if let Some(decl) = declarator_node {
                            parser.extract_declarator_name(&decl)
                        } else {
                            format!("_param{}", params.len())
                        };

                        params.push(Parameter {
                            name,
                            type_annotation,
                            default_value: None,
                        });
                    } else if child.kind() == "..." {
                        params.push(Parameter {
                            name: "...".to_string(),
                            type_annotation: "variadic".to_string(),
                            default_value: None,
                        });
                    }
                }
            } else {
                let mut cursor = node.walk();
                for child in node.children(&mut cursor) {
                    find_params(&child, parser, params);
                }
            }
        }

        find_params(declarator, self, &mut params);
        params
    }

    fn extract_declarator_name(&self, declarator: &Node) -> String {
        match declarator.kind() {
            "identifier" => self.get_node_text(declarator),
            "pointer_declarator" | "array_declarator" | "function_declarator" => {
                if let Some(decl) = declarator.child_by_field_name("declarator") {
                    self.extract_declarator_name(&decl)
                } else {
                    String::new()
                }
            }
            _ => String::new(),
        }
    }

    fn build_signature(&self, name: &str, params: &[Parameter], return_type: &str) -> String {
        let param_str = params
            .iter()
            .map(|p| {
                if p.name.starts_with('_') && p.name != "..." {
                    p.type_annotation.clone()
                } else {
                    format!("{} {}", p.type_annotation, p.name)
                }
            })
            .collect::<Vec<_>>()
            .join(", ");
        format!("{} {}({})", return_type, name, param_str)
    }

    //  Call extraction

    fn extract_function_calls_detailed(
        &self,
        node: &Node,
        fn_ptr_map: &HashMap<String, String>,
    ) -> Vec<FunctionCall> {
        let mut calls = Vec::new();
        let mut seen = HashSet::new();
        self.find_calls_recursive(node, &mut calls, &mut seen, "unconditional", fn_ptr_map);
        calls
    }

    fn find_calls_recursive(
        &self,
        node: &Node,
        calls: &mut Vec<FunctionCall>,
        seen: &mut HashSet<String>,
        context: &str,
        fn_ptr_map: &HashMap<String, String>,
    ) {
        let child_context = match node.kind() {
            "if_statement" => "if",
            "for_statement" | "while_statement" | "do_statement" => "loop",
            "switch_statement" => "switch",
            _ => context,
        };

        if node.kind() == "call_expression" {
            if let Some(func_node) = node.child_by_field_name("function") {
                let raw_callee = self.get_node_text(&func_node);

                // Resolve the actual callee name through several strategies
                let resolved = self.resolve_callee(&raw_callee, fn_ptr_map);

                if !resolved.is_empty() {
                    let key = format!("{}:{}", resolved, node.start_position().row);
                    if !seen.contains(&key) {
                        seen.insert(key);
                        let args = self.extract_call_arguments(node);
                        calls.push(FunctionCall {
                            callee: resolved,
                            defined_in: None,
                            line: node.start_position().row + 1,
                            args,
                            is_conditional: context != "unconditional",
                            context: context.to_string(),
                        });
                    }
                }
            }
        }

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.find_calls_recursive(&child, calls, seen, child_context, fn_ptr_map);
        }
    }

    /// Multi-strategy callee resolution:
    ///  1. Strip leading `*` / `(*fp)` derefs  → direct call
    ///  2. Strip member access  `obj->method` / `obj.method`
    ///  3. Resolve through fn_ptr_map
    ///  4. Resolve through macro_defs (expand one level)
    ///  5. Fall back to the trimmed raw text
    fn resolve_callee(&self, raw: &str, fn_ptr_map: &HashMap<String, String>) -> String {
        let trimmed = raw.trim();

        // 1. Dereference:  (*fp)(args) → fp,  **fp → fp
        let deref_stripped = trimmed
            .trim_start_matches('*')
            .trim_start_matches('(')
            .trim_end_matches(')')
            .trim_start_matches('*')
            .trim();

        // 2. Member access: take last segment after `.` or `->`
        let base: &str = deref_stripped
            .rsplit(|c| c == '.' || c == '>')
            .next()
            .unwrap_or(deref_stripped)
            .trim();

        if base.is_empty() {
            return String::new();
        }

        // 3. Function-pointer map
        if let Some(real) = fn_ptr_map.get(base) {
            return real.clone();
        }

        // 4. Macro expansion (one level): if the identifier is an all-caps macro
        //    that expands to a single identifier, treat that as the real callee.
        if base.chars().all(|c| c.is_uppercase() || c == '_') {
            if let Some(expansion) = self.macro_defs.get(base) {
                let exp = expansion.trim();
                if exp.chars().all(|c| c.is_alphanumeric() || c == '_') {
                    return exp.to_string();
                }
            }
        }

        base.to_string()
    }

    fn extract_call_arguments(&self, call_node: &Node) -> Vec<String> {
        let mut args = Vec::new();
        if let Some(arg_list) = call_node.child_by_field_name("arguments") {
            let mut cursor = arg_list.walk();
            for child in arg_list.children(&mut cursor) {
                if child.kind() != "(" && child.kind() != ")" && child.kind() != "," {
                    args.push(self.get_node_text(&child));
                }
            }
        }
        args
    }

    //  Variables

    fn extract_variables(&self, node: &Node, params: &[Parameter]) -> Vec<Variable> {
        let mut variables: HashMap<String, Variable> = HashMap::new();

        for param in params {
            if !param.name.starts_with('_') && param.name != "..." {
                variables.insert(
                    param.name.clone(),
                    Variable {
                        name: param.name.clone(),
                        var_type: if param.type_annotation.is_empty() {
                            None
                        } else {
                            Some(param.type_annotation.clone())
                        },
                        scope: "param".to_string(),
                        defined_at: None,
                        transformations: vec![],
                        used_in: vec![],
                        returned: false,
                    },
                );
            }
        }

        self.track_variable_usage(node, &mut variables);
        variables.into_values().collect()
    }

    fn track_variable_usage(&self, node: &Node, variables: &mut HashMap<String, Variable>) {
        let mut cursor = node.walk();

        if node.kind() == "declaration" {
            // A declaration may have multiple declarators:  int a = 1, b = 2;
            let var_type = node
                .child_by_field_name("type")
                .map(|t| self.get_node_text(&t));

            let mut c = node.walk();
            for child in node.children(&mut c) {
                let decl_node = match child.kind() {
                    "init_declarator" => child.child_by_field_name("declarator"),
                    k if k.ends_with("declarator") => Some(child),
                    _ => None,
                };
                if let Some(decl) = decl_node {
                    let var_name = self.extract_declarator_name(&decl);
                    if !var_name.is_empty() && !variables.contains_key(&var_name) {
                        let line = node.start_position().row + 1;
                        variables.insert(
                            var_name.clone(),
                            Variable {
                                name: var_name,
                                var_type: var_type.clone(),
                                scope: "local".to_string(),
                                defined_at: Some(line),
                                transformations: vec![],
                                used_in: vec![],
                                returned: false,
                            },
                        );
                    }
                }
            }
        } else if node.kind() == "return_statement" {
            let mut ret_cursor = node.walk();
            for child in node.children(&mut ret_cursor) {
                if child.kind() == "identifier" {
                    let var_name = self.get_node_text(&child);
                    if let Some(var) = variables.get_mut(&var_name) {
                        var.returned = true;
                    }
                }
            }
        }

        for child in node.children(&mut cursor) {
            self.track_variable_usage(&child, variables);
        }
    }

    //  Control flow

    fn build_control_flow(&self, node: &Node) -> ControlFlow {
        let mut control_flow = ControlFlow {
            complexity: self.calculate_complexity(node),
            branches: vec![],
            loops: vec![],
            try_blocks: vec![],
        };
        self.extract_control_structures(node, &mut control_flow);
        control_flow
    }

    fn extract_control_structures(&self, node: &Node, cf: &mut ControlFlow) {
        let mut cursor = node.walk();
        match node.kind() {
            "if_statement" => {
                if let Some(branch) = self.parse_if_statement(node) {
                    cf.branches.push(branch);
                }
            }
            "for_statement" | "while_statement" | "do_statement" => {
                if let Some(loop_info) = self.parse_loop(node) {
                    cf.loops.push(loop_info);
                }
            }
            _ => {}
        }
        for child in node.children(&mut cursor) {
            self.extract_control_structures(&child, cf);
        }
    }

    fn parse_if_statement(&self, node: &Node) -> Option<Branch> {
        let line = node.start_position().row + 1;
        let condition = node
            .child_by_field_name("condition")
            .map(|c| self.get_node_text(&c))
            .unwrap_or_default();
        let consequence = node.child_by_field_name("consequence")?;
        let true_path = self.extract_execution_path(&consequence)?;
        let false_path = node
            .child_by_field_name("alternative")
            .and_then(|alt| self.extract_execution_path(&alt));
        Some(Branch {
            branch_type: "if".to_string(),
            condition,
            line,
            true_path,
            false_path,
        })
    }

    fn extract_execution_path(&self, block: &Node) -> Option<ExecutionPath> {
        Some(ExecutionPath {
            calls: self.extract_calls_from_block(block),
            returns: self.find_return_value(block),
            raises: None,
        })
    }

    fn extract_calls_from_block(&self, block: &Node) -> Vec<String> {
        let mut calls = Vec::new();
        let mut seen = HashSet::new();
        self.find_call_names(block, &mut calls, &mut seen);
        calls
    }

    fn find_call_names(&self, node: &Node, calls: &mut Vec<String>, seen: &mut HashSet<String>) {
        let mut cursor = node.walk();
        if node.kind() == "call_expression" {
            if let Some(func_node) = node.child_by_field_name("function") {
                // Use the same resolution logic for consistency
                let raw = self.get_node_text(&func_node);
                let name = self.resolve_callee(&raw, &HashMap::new());
                if !name.is_empty() && !seen.contains(&name) {
                    seen.insert(name.clone());
                    calls.push(name);
                }
            }
        }
        for child in node.children(&mut cursor) {
            self.find_call_names(&child, calls, seen);
        }
    }

    fn find_return_value(&self, node: &Node) -> Option<String> {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "return_statement" {
                let mut ret_vals = Vec::new();
                let mut ret_cursor = child.walk();
                for ret_child in child.children(&mut ret_cursor) {
                    if ret_child.kind() != "return" && ret_child.kind() != ";" {
                        ret_vals.push(self.get_node_text(&ret_child));
                    }
                }
                return Some(ret_vals.join(", "));
            }
            if let Some(val) = self.find_return_value(&child) {
                return Some(val);
            }
        }
        None
    }

    fn parse_loop(&self, node: &Node) -> Option<Loop> {
        let line = node.start_position().row + 1;
        let loop_type = match node.kind() {
            "for_statement" => "for",
            "while_statement" => "while",
            "do_statement" => "do-while",
            _ => "unknown",
        };
        let condition = node
            .child_by_field_name("condition")
            .map(|c| self.get_node_text(&c))
            .unwrap_or_default();
        Some(Loop {
            loop_type: loop_type.to_string(),
            condition,
            line,
            calls: self.extract_calls_from_block(node),
        })
    }

    //  Structs / globals

    fn extract_structs(&self, root: &Node) -> Vec<Class> {
        let mut structs = Vec::new();
        let mut cursor = root.walk();
        for child in root.children(&mut cursor) {
            if child.kind() == "declaration" {
                if let Some(s) = self.parse_struct(&child) {
                    structs.push(s);
                }
            }
        }
        structs
    }

    fn parse_struct(&self, node: &Node) -> Option<Class> {
        let type_node = node.child_by_field_name("type")?;
        if type_node.kind() != "struct_specifier" && type_node.kind() != "union_specifier" {
            return None;
        }
        let name = type_node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let struct_type = if type_node.kind() == "union_specifier" {
            "union"
        } else {
            "struct"
        };
        let attributes = type_node
            .child_by_field_name("body")
            .map(|b| self.extract_struct_fields(&b))
            .unwrap_or_default();

        Some(Class {
            id: format!("{}_{}", struct_type, name),
            name,
            bases: vec![],
            docstring: self.extract_docstring(node),
            line_start: node.start_position().row + 1,
            line_end: node.end_position().row + 1,
            methods: vec![],
            attributes,
            decorators: vec![],
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    fn extract_struct_fields(&self, body: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();
        let mut cursor = body.walk();
        for child in body.children(&mut cursor) {
            if child.kind() == "field_declaration" {
                let type_annotation = child
                    .child_by_field_name("type")
                    .map(|t| self.get_node_text(&t))
                    .unwrap_or_default();
                if let Some(decl) = child.child_by_field_name("declarator") {
                    let name = self.extract_declarator_name(&decl);
                    if !name.is_empty() {
                        fields.push(Attribute {
                            name,
                            type_annotation,
                            value: None,
                        });
                    }
                }
            }
        }
        fields
    }

    fn extract_global_vars(&self, root: &Node) -> Vec<GlobalVar> {
        let mut vars = Vec::new();
        let mut cursor = root.walk();
        for child in root.children(&mut cursor) {
            if child.kind() == "declaration" {
                if child
                    .child_by_field_name("declarator")
                    .map(|d| d.kind() != "function_declarator")
                    .unwrap_or(true)
                {
                    if let Some(var) = self.parse_global_var(&child) {
                        vars.push(var);
                    }
                }
            }
        }
        vars
    }

    fn parse_global_var(&self, node: &Node) -> Option<GlobalVar> {
        let type_annotation = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_default();
        let declarator = node.child_by_field_name("declarator")?;
        let name = self.extract_declarator_name(&declarator);
        if name.is_empty() {
            return None;
        }
        let value = declarator
            .child_by_field_name("value")
            .or_else(|| node.child_by_field_name("value"))
            .map(|v| self.get_node_text(&v));
        Some(GlobalVar {
            name,
            type_annotation,
            value,
            line: node.start_position().row + 1,
        })
    }

    //  Docstrings / comments

    fn extract_docstring(&self, node: &Node) -> String {
        if let Some(prev) = node.prev_sibling() {
            if prev.kind() == "comment" {
                let text = self.get_node_text(&prev);
                return text
                    .trim_start_matches("//")
                    .trim_start_matches("/*")
                    .trim_end_matches("*/")
                    .trim()
                    .to_string();
            }
        }
        String::new()
    }

    //  Complexity

    fn calculate_complexity(&self, node: &Node) -> usize {
        fn count(node: &Node) -> usize {
            let mut c = 0;
            let mut cursor = node.walk();
            match node.kind() {
                "if_statement"
                | "for_statement"
                | "while_statement"
                | "do_statement"
                | "switch_statement"
                | "case_statement"
                | "conditional_expression"
                | "&&"
                | "||" => c += 1,
                _ => {}
            }
            for child in node.children(&mut cursor) {
                c += count(&child);
            }
            c
        }
        1 + count(node)
    }

    //  TODOs / Security

    fn extract_todos(&self) -> Vec<Todo> {
        self.source_code
            .lines()
            .enumerate()
            .filter_map(|(idx, line)| {
                TODO_RE.captures(line).map(|caps| {
                    let text = caps[1].trim().to_string();
                    let text_lower = text.to_lowercase();

                    let priority =
                        if text_lower.contains("critical") || text_lower.contains("urgent") {
                            "high"
                        } else if text_lower.contains("minor") {
                            "low"
                        } else {
                            "medium"
                        };

                    Todo {
                        line: idx + 1,
                        text,
                        priority: priority.to_string(),
                    }
                })
            })
            .collect()
    }

    fn detect_security_patterns(&self) -> Vec<SecurityNote> {
        let mut notes = Vec::new();
        for (idx, line) in self.source_code.lines().enumerate() {
            for pattern in SECURITY_PATTERNS.iter() {
                if pattern.regex.is_match(line) {
                    notes.push(SecurityNote {
                        note_type: pattern.note_type.to_string(),
                        line: idx + 1,
                        description: pattern.description.to_string(),
                    });
                    break;
                }
            }
        }
        notes
    }

    fn auto_tag_function(
        &self,
        name: &str,
        docstring: &str,
        calls: &[FunctionCall],
        return_type: &str,
    ) -> Vec<String> {
        let mut tags = Vec::new();
        let name_lower = name.to_lowercase();
        let doc_lower = docstring.to_lowercase();

        // Special case: main function
        if name == "main" {
            tags.push("entry-point".to_string());
        }

        // Apply tag rules
        for rule in TAG_RULES.iter() {
            let name_matches = rule.keywords.iter().any(|kw| name_lower.contains(kw));
            let doc_matches = rule.check_docstring && doc_lower.contains(rule.keywords[0]);

            if name_matches || doc_matches {
                tags.push(rule.tag.to_string());

                // Push secondary tags for certain rules
                match rule.tag {
                    "authentication" => tags.push("security".to_string()),
                    "api" if name_lower.contains("handler") || name_lower.contains("serve") => {
                        tags.push("http-handler".to_string())
                    }
                    _ => {}
                }
            }
        }

        // Check function calls for patterns
        let calls_str = calls.iter().map(|c| c.callee.as_str()).collect::<Vec<_>>();
        let calls_joined = calls_str.join(" ");

        if MALLOC_RE.is_match(&calls_joined) {
            tags.push("allocates-memory".to_string());
            tags.push("memory-management".to_string());
        }

        if FREE_RE.is_match(&calls_joined) {
            tags.push("frees-memory".to_string());
            tags.push("memory-management".to_string());
        }

        if PTHREAD_RE.is_match(&calls_joined) {
            tags.push("concurrent".to_string());
            tags.push("threading".to_string());
        }

        if SYSCALL_RE.is_match(&calls_joined) {
            tags.push("system-call".to_string());
        }

        if STRING_OPS_RE.is_match(&calls_joined) {
            tags.push("string-operations".to_string());
        }

        if return_type.contains('*') {
            tags.push("returns-pointer".to_string());
        }

        // Check for test functions
        if name_lower.starts_with("test") || name_lower.contains("test_") {
            tags.push("testing".to_string());
        }

        tags.sort();
        tags.dedup();
        tags
    }

    fn estimate_importance(&self, name: &str, return_type: &str) -> f32 {
        let mut score: f32 = 0.5;
        if name == "main" {
            score += 0.3;
        }
        if name.starts_with('_') {
            score -= 0.1;
        }
        if return_type.contains("static") {
            score -= 0.1;
        }
        score.max(0.0).min(1.0)
    }

    fn get_node_text(&self, node: &Node) -> String {
        node.utf8_text(self.source_code.as_bytes())
            .unwrap_or("")
            .to_string()
    }
}

pub fn parse_file(path: &Path) -> Result<(String, FileData), String> {
    let source_code = std::fs::read_to_string(path)
        .map_err(|e| format!("Failed to read file {}: {}", path.display(), e))?;
    let mut parser = CParser::new(source_code);
    let file_data = parser.parse()?;
    Ok((path.to_string_lossy().to_string(), file_data))
}
