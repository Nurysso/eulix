//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::kb::types::*;
use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use tree_sitter::{Node, Parser};

pub struct CppParser {
    source_code: String,
}

impl CppParser {
    pub fn new(source_code: String) -> Self {
        Self { source_code }
    }

    pub fn parse(&self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_cpp::language())
            .map_err(|e| format!("Failed to load C++ grammar: {}", e))?;

        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse C++ file".to_string())?;

        let root = tree.root_node();

        Ok(FileData {
            language: "cpp".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(&root),
            functions: self.extract_functions(&root),
            classes: self.extract_classes(&root),
            global_vars: self.extract_global_vars(&root),
            todos: self.extract_todos(),
            security_notes: self.detect_security_patterns(),
        })
    }

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    // Imports — #include directives

    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "preproc_include" {
                let text = self.get_node_text(&child);
                if let Some(caps) = Regex::new(r#"#include\s*[<"]([^>"]+)[>"]"#)
                    .ok()
                    .and_then(|re| re.captures(&text))
                {
                    let path = caps.get(1).unwrap().as_str().to_string();
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
        let cpp_stdlib = [
            "iostream",
            "string",
            "vector",
            "map",
            "unordered_map",
            "set",
            "unordered_set",
            "algorithm",
            "functional",
            "memory",
            "utility",
            "tuple",
            "variant",
            "optional",
            "array",
            "deque",
            "list",
            "queue",
            "stack",
            "bitset",
            "numeric",
            "iterator",
            "type_traits",
            "typeinfo",
            "exception",
            "stdexcept",
            "cassert",
            "cstdlib",
            "cstring",
            "cstdio",
            "cmath",
            "climits",
            "cfloat",
            "thread",
            "mutex",
            "condition_variable",
            "future",
            "atomic",
            "chrono",
            "regex",
            "fstream",
            "sstream",
            "iomanip",
            "filesystem",
            // C headers
            "stdio.h",
            "stdlib.h",
            "string.h",
            "math.h",
            "time.h",
            "ctype.h",
            "stdint.h",
            "stdbool.h",
            "assert.h",
            "errno.h",
            "unistd.h",
            "pthread.h",
        ];

        if cpp_stdlib.iter().any(|s| path.starts_with(s)) || is_system {
            "stdlib".to_string()
        } else if path.starts_with('.') || !path.contains('/') {
            "internal".to_string()
        } else {
            "external".to_string()
        }
    }

    // Functions — top-level and namespace-level

    fn extract_functions(&self, root: &Node) -> Vec<Function> {
        let mut functions = Vec::new();
        self.collect_functions(root, &mut functions, "");
        functions
    }

    fn collect_functions(&self, node: &Node, functions: &mut Vec<Function>, ctx: &str) {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "function_definition" => {
                    if let Some(func) = self.parse_function(&child, ctx) {
                        functions.push(func);
                    }
                }
                "namespace_definition" => {
                    // Recurse into namespaces
                    if let Some(body) = child.child_by_field_name("body") {
                        self.collect_functions(&body, functions, ctx);
                    }
                }
                _ => {}
            }
        }
    }

    fn parse_function(&self, node: &Node, struct_context: &str) -> Option<Function> {
        let declarator = node.child_by_field_name("declarator")?;
        let name = self.extract_function_name(&declarator)?;

        // Skip constructors/destructors captured by parse_class
        // In top-level context we still capture them
        let params = self.extract_parameters(&declarator);
        let return_type = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_default();

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);
        let signature = self.build_signature(&name, &params, &return_type);

        let id = if struct_context.is_empty() {
            format!("func_{}", name)
        } else {
            format!("method_{}_{}", struct_context, name)
        };

        let (calls, variables, control_flow, complexity) =
            if let Some(body) = node.child_by_field_name("body") {
                (
                    self.extract_function_calls_detailed(&body),
                    self.extract_variables(&body, &params),
                    self.build_control_flow(&body),
                    self.calculate_complexity(&body),
                )
            } else {
                (vec![], vec![], ControlFlow::default(), 1)
            };

        let tags = self.auto_tag_function(&name, &docstring, &calls, &return_type);
        let importance_score = self.estimate_importance(&name, !struct_context.is_empty());

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
            exceptions: ExceptionInfo::default(),
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
            "function_declarator" => declarator
                .child_by_field_name("declarator")
                .and_then(|d| self.extract_function_name(&d)),
            "pointer_declarator" | "reference_declarator" => declarator
                .child_by_field_name("declarator")
                .and_then(|d| self.extract_function_name(&d)),
            "qualified_identifier" | "destructor_name" | "operator_name" => {
                Some(self.get_node_text(declarator))
            }
            "identifier" => Some(self.get_node_text(declarator)),
            _ => None,
        }
    }

    fn extract_parameters(&self, declarator: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();
        self.find_params(declarator, &mut params);
        params
    }

    fn find_params(&self, node: &Node, params: &mut Vec<Parameter>) {
        if node.kind() == "parameter_list" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "parameter_declaration" {
                    let type_annotation = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_default();

                    let name = child
                        .child_by_field_name("declarator")
                        .map(|d| self.extract_declarator_name(&d))
                        .unwrap_or_else(|| format!("_p{}", params.len()));

                    params.push(Parameter {
                        name,
                        type_annotation,
                        default_value: None,
                    });
                } else if child.kind() == "optional_parameter_declaration" {
                    let type_annotation = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_default();
                    let name = child
                        .child_by_field_name("declarator")
                        .map(|d| self.extract_declarator_name(&d))
                        .unwrap_or_else(|| format!("_p{}", params.len()));
                    let default_value = child
                        .child_by_field_name("default_value")
                        .map(|v| self.get_node_text(&v));

                    params.push(Parameter {
                        name,
                        type_annotation,
                        default_value,
                    });
                } else if child.kind() == "variadic_parameter_declaration" {
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
                self.find_params(&child, params);
            }
        }
    }

    fn extract_declarator_name(&self, node: &Node) -> String {
        match node.kind() {
            "identifier" => self.get_node_text(node),
            "pointer_declarator"
            | "reference_declarator"
            | "array_declarator"
            | "function_declarator" => node
                .child_by_field_name("declarator")
                .map(|d| self.extract_declarator_name(&d))
                .unwrap_or_default(),
            _ => self.get_node_text(node),
        }
    }

    fn build_signature(&self, name: &str, params: &[Parameter], return_type: &str) -> String {
        let param_str = params
            .iter()
            .map(|p| {
                if p.type_annotation.is_empty() {
                    p.name.clone()
                } else {
                    format!("{} {}", p.type_annotation, p.name)
                }
            })
            .collect::<Vec<_>>()
            .join(", ");

        if return_type.is_empty() {
            format!("{}({})", name, param_str)
        } else {
            format!("{} {}({})", return_type, name, param_str)
        }
    }

    // Classes — class_specifier / struct_specifier

    fn extract_classes(&self, root: &Node) -> Vec<Class> {
        let mut classes = Vec::new();
        self.collect_classes(root, &mut classes);
        classes
    }

    fn collect_classes(&self, node: &Node, classes: &mut Vec<Class>) {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "class_specifier" | "struct_specifier" => {
                    if let Some(class) = self.parse_class(&child) {
                        classes.push(class);
                    }
                }
                "namespace_definition" => {
                    if let Some(body) = child.child_by_field_name("body") {
                        self.collect_classes(&body, classes);
                    }
                }
                _ => {}
            }
        }
    }

    fn parse_class(&self, node: &Node) -> Option<Class> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        // Base classes
        let bases = node
            .child_by_field_name("base_class_clause")
            .map(|bc| {
                let text = self.get_node_text(&bc);
                // Simple extraction — strip leading `: `
                text.trim_start_matches(':')
                    .split(',')
                    .map(|s| {
                        s.trim()
                            .trim_start_matches("public ")
                            .trim_start_matches("protected ")
                            .trim_start_matches("private ")
                            .trim()
                            .to_string()
                    })
                    .filter(|s| !s.is_empty())
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default();

        let (attributes, methods) = node
            .child_by_field_name("body")
            .map(|body| self.extract_class_body(&body, &name))
            .unwrap_or((vec![], vec![]));

        let is_struct = node.kind() == "struct_specifier";
        Some(Class {
            id: format!("{}_{}", if is_struct { "struct" } else { "class" }, name),
            name,
            bases,
            docstring,
            line_start,
            line_end,
            methods,
            attributes,
            decorators: vec![],
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    fn extract_class_body(&self, body: &Node, class_name: &str) -> (Vec<Attribute>, Vec<Function>) {
        let mut attributes = Vec::new();
        let mut methods = Vec::new();
        let mut cursor = body.walk();

        for child in body.children(&mut cursor) {
            match child.kind() {
                "field_declaration" => {
                    let type_annotation = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_default();

                    if let Some(decl) = child.child_by_field_name("declarator") {
                        let name = self.extract_declarator_name(&decl);
                        if !name.is_empty() {
                            let value = decl
                                .child_by_field_name("value")
                                .map(|v| self.get_node_text(&v));
                            attributes.push(Attribute {
                                name,
                                type_annotation,
                                value,
                            });
                        }
                    }
                }
                "function_definition" => {
                    if let Some(method) = self.parse_function(&child, class_name) {
                        methods.push(method);
                    }
                }
                "declaration" => {
                    // Forward declarations / pure virtual — skip body-less
                }
                _ => {}
            }
        }

        (attributes, methods)
    }

    // Global variables

    fn extract_global_vars(&self, root: &Node) -> Vec<GlobalVar> {
        let mut vars = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "declaration" {
                // Skip function declarations
                let declarator = child.child_by_field_name("declarator");
                let is_func = declarator
                    .map(|d| d.kind() == "function_declarator")
                    .unwrap_or(false);

                if !is_func {
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
            .map(|v| self.get_node_text(&v));

        Some(GlobalVar {
            name,
            type_annotation,
            value,
            line: node.start_position().row + 1,
        })
    }

    // Function call extraction

    fn extract_function_calls_detailed(&self, node: &Node) -> Vec<FunctionCall> {
        let mut calls = Vec::new();
        let mut seen = HashSet::new();
        self.find_calls_recursive(node, &mut calls, &mut seen, "unconditional");
        calls
    }

    fn find_calls_recursive(
        &self,
        node: &Node,
        calls: &mut Vec<FunctionCall>,
        seen: &mut HashSet<String>,
        context: &str,
    ) {
        let mut cursor = node.walk();

        let child_context = match node.kind() {
            "if_statement" => "if",
            "for_statement" | "while_statement" | "do_statement" => "loop",
            "switch_statement" => "switch",
            "try_statement" => "try",
            _ => context,
        };

        if node.kind() == "call_expression" {
            if let Some(func_node) = node.child_by_field_name("function") {
                let call_text = self.get_node_text(&func_node);
                // Handle `obj.method()`, `ns::func()`, `obj->method()`
                let name = call_text
                    .split(|c: char| c == '.' || c == '>')
                    .last()
                    .and_then(|s| s.split("::").last())
                    .unwrap_or(&call_text)
                    .trim()
                    .to_string();

                if !name.is_empty() {
                    let key = format!("{}:{}", name, node.start_position().row);
                    if !seen.contains(&key) {
                        seen.insert(key);
                        let args = self.extract_call_arguments(node);
                        calls.push(FunctionCall {
                            callee: name,
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

        for child in node.children(&mut cursor) {
            self.find_calls_recursive(&child, calls, seen, child_context);
        }
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

    // Variables

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
            if let Some(decl) = node.child_by_field_name("declarator") {
                let var_name = self.extract_declarator_name(&decl);
                if !var_name.is_empty() {
                    let var_type = node
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t));
                    let line = node.start_position().row + 1;
                    variables.entry(var_name.clone()).or_insert(Variable {
                        name: var_name,
                        var_type,
                        scope: "local".to_string(),
                        defined_at: Some(line),
                        transformations: vec![],
                        used_in: vec![],
                        returned: false,
                    });
                }
            }
        } else if node.kind() == "return_statement" {
            let mut ret_cursor = node.walk();
            for child in node.children(&mut ret_cursor) {
                if child.kind() == "identifier" {
                    let vn = self.get_node_text(&child);
                    if let Some(var) = variables.get_mut(&vn) {
                        var.returned = true;
                    }
                }
            }
        }

        for child in node.children(&mut cursor) {
            self.track_variable_usage(&child, variables);
        }
    }

    // Control flow

    fn build_control_flow(&self, node: &Node) -> ControlFlow {
        let mut cf = ControlFlow {
            complexity: self.calculate_complexity(node),
            branches: vec![],
            loops: vec![],
            try_blocks: vec![],
        };
        self.extract_control_structures(node, &mut cf);
        cf
    }

    fn extract_control_structures(&self, node: &Node, cf: &mut ControlFlow) {
        let mut cursor = node.walk();

        match node.kind() {
            "if_statement" => {
                if let Some(branch) = self.parse_if(node) {
                    cf.branches.push(branch);
                }
            }
            "for_statement" | "while_statement" | "do_statement" => {
                if let Some(lp) = self.parse_loop(node) {
                    cf.loops.push(lp);
                }
            }
            "try_statement" => {
                if let Some(tb) = self.parse_try(node) {
                    cf.try_blocks.push(tb);
                }
            }
            _ => {}
        }

        for child in node.children(&mut cursor) {
            self.extract_control_structures(&child, cf);
        }
    }

    fn parse_if(&self, node: &Node) -> Option<Branch> {
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
            line: node.start_position().row + 1,
            true_path,
            false_path,
        })
    }

    fn parse_loop(&self, node: &Node) -> Option<Loop> {
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
            line: node.start_position().row + 1,
            calls: self.extract_calls_from_block(node),
        })
    }

    fn parse_try(&self, node: &Node) -> Option<TryBlock> {
        let line = node.start_position().row + 1;
        let mut try_calls = Vec::new();
        let mut except_clauses = Vec::new();
        let mut finally_calls = Vec::new();
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "compound_statement" => {
                    try_calls = self.extract_calls_from_block(&child);
                }
                "catch_clause" => {
                    let exc_type = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_else(|| "...".to_string());
                    let calls = if let Some(body) = child.child_by_field_name("body") {
                        self.extract_calls_from_block(&body)
                    } else {
                        vec![]
                    };
                    except_clauses.push(ExceptClause {
                        exception_type: exc_type,
                        line: child.start_position().row + 1,
                        calls,
                    });
                }
                "finally_clause" => {
                    if let Some(body) = child.child_by_field_name("body") {
                        finally_calls = self.extract_calls_from_block(&body);
                    }
                }
                _ => {}
            }
        }

        Some(TryBlock {
            line,
            try_calls,
            except_clauses,
            finally_calls,
        })
    }

    fn extract_execution_path(&self, block: &Node) -> Option<ExecutionPath> {
        let calls = self.extract_calls_from_block(block);
        let returns = self.find_return_value(block);
        Some(ExecutionPath {
            calls,
            returns,
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
                let name = self.get_node_text(&func_node);
                if !seen.contains(&name) {
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
                let mut vals = Vec::new();
                let mut rc = child.walk();
                for rc_child in child.children(&mut rc) {
                    if rc_child.kind() != "return" && rc_child.kind() != ";" {
                        vals.push(self.get_node_text(&rc_child));
                    }
                }
                return Some(vals.join(", "));
            }
            if let Some(v) = self.find_return_value(&child) {
                return Some(v);
            }
        }

        None
    }

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
                | "try_statement"
                | "catch_clause"
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

    // Docstrings

    fn extract_docstring(&self, node: &Node) -> String {
        if let Some(prev) = node.prev_sibling() {
            if prev.kind() == "comment" {
                let text = self.get_node_text(&prev);
                return text
                    .trim_start_matches("//")
                    .trim_start_matches("/**")
                    .trim_start_matches("/*!")
                    .trim_start_matches("/*")
                    .trim_end_matches("*/")
                    .trim()
                    .to_string();
            }
        }
        String::new()
    }

    // TODOs

    fn extract_todos(&self) -> Vec<Todo> {
        let re = Regex::new(r"(?://|/\*)\s*TODO:?\s*(.+?)(?:\*/|$)").unwrap();

        self.source_code
            .lines()
            .enumerate()
            .filter_map(|(idx, line)| {
                re.captures(line).map(|caps| {
                    let text = caps.get(1).unwrap().as_str().trim().to_string();
                    let priority = if text.to_lowercase().contains("critical")
                        || text.to_lowercase().contains("urgent")
                    {
                        "high"
                    } else if text.to_lowercase().contains("minor") {
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

    // Security patterns

    fn detect_security_patterns(&self) -> Vec<SecurityNote> {
        let mut notes = Vec::new();

        let patterns = vec![
            (
                r"strcpy|strcat|sprintf|vsprintf|gets",
                "unsafe_string",
                "Uses unsafe string function (buffer overflow risk)",
            ),
            (
                r"system\(|popen\(|exec[lv]",
                "command_execution",
                "Shell command execution",
            ),
            (
                r"reinterpret_cast",
                "unsafe_cast",
                "reinterpret_cast bypasses type safety",
            ),
            (
                r"new\s+\w+|delete\s+",
                "manual_memory",
                "Manual memory management — prefer smart pointers",
            ),
            (
                r"scanf|sscanf|fscanf",
                "format_string",
                "scanf without width limit is unsafe",
            ),
            (
                r"rand\(\)|srand\(",
                "weak_random",
                "Weak pseudo-random generator",
            ),
            (
                r"const_cast",
                "const_cast",
                "const_cast removes const qualifier — use carefully",
            ),
        ];

        for (pat, note_type, description) in patterns {
            if let Ok(re) = Regex::new(pat) {
                for (idx, line) in self.source_code.lines().enumerate() {
                    if re.is_match(line) {
                        notes.push(SecurityNote {
                            note_type: note_type.to_string(),
                            line: idx + 1,
                            description: description.to_string(),
                        });
                    }
                }
            }
        }

        notes
    }

    // Auto-tagging & importance scoring

    fn auto_tag_function(
        &self,
        name: &str,
        doc: &str,
        calls: &[FunctionCall],
        return_type: &str,
    ) -> Vec<String> {
        let mut tags = Vec::new();
        let lower = name.to_lowercase();
        let doc_lower = doc.to_lowercase();

        if lower.contains("init") || lower.starts_with("create") || lower.starts_with("make") {
            tags.push("constructor".to_string());
        }
        if lower.starts_with("test") || lower.starts_with("check") {
            tags.push("test".to_string());
        }
        if lower.contains("parse") || lower.contains("decode") {
            tags.push("parsing".to_string());
        }
        if lower.contains("serialize") || lower.contains("encode") || lower.contains("marshal") {
            tags.push("serialization".to_string());
        }
        if return_type.contains("bool") || lower.starts_with("is_") || lower.starts_with("has_") {
            tags.push("predicate".to_string());
        }
        if doc_lower.contains("thread-safe") || doc_lower.contains("mutex") {
            tags.push("concurrent".to_string());
        }
        if calls
            .iter()
            .any(|c| c.callee.contains("throw") || c.callee.contains("assert"))
        {
            tags.push("error-handling".to_string());
        }

        tags
    }

    fn estimate_importance(&self, name: &str, is_method: bool) -> f32 {
        let lower = name.to_lowercase();
        let mut score: f32 = 0.5;

        if lower == "main" || lower == "run" || lower == "execute" {
            score += 0.4;
        }
        if lower.starts_with("test") {
            score -= 0.1;
        }
        if is_method {
            score += 0.1;
        }

        score.clamp(0.0, 1.0)
    }

    // Utility

    fn get_node_text(&self, node: &Node) -> String {
        let bytes = self.source_code.as_bytes();
        let start = node.start_byte();
        let end = node.end_byte();
        String::from_utf8_lossy(&bytes[start..end]).to_string()
    }
}

/// Entry point called from main.rs
pub fn parse_file(path: &Path) -> Result<(String, FileData), Box<dyn std::error::Error>> {
    let source = std::fs::read_to_string(path)?;
    let parser = CppParser::new(source);
    let file_data = parser
        .parse()
        .map_err(|e| -> Box<dyn std::error::Error> { e.into() })?;
    let name = path.to_string_lossy().to_string();
    Ok((name, file_data))
}
