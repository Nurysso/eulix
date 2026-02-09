//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::kb::types::*;
use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use tree_sitter::{Node, Parser};

pub struct GoParser {
    source_code: String,
}

impl GoParser {
    pub fn new(source_code: String) -> Self {
        // let lines: Vec<String> = source_code.lines().map(|s| s.to_string()).collect();
        Self { source_code }
    }

    pub fn parse(&self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_go::language())
            .map_err(|e| format!("Failed to load Go grammar: {}", e))?;

        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse Go file".to_string())?;

        let root = tree.root_node();

        Ok(FileData {
            language: "go".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(&root),
            functions: self.extract_functions(&root),
            classes: self.extract_structs(&root),
            global_vars: self.extract_global_vars(&root),
            todos: self.extract_todos(),
            security_notes: self.detect_security_patterns(),
        })
    }

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            match child.kind() {
                "import_declaration" => {
                    let mut import_cursor = child.walk();
                    for spec_node in child.children(&mut import_cursor) {
                        if spec_node.kind() == "import_spec" {
                            if let Some(path_node) = spec_node.child_by_field_name("path") {
                                let path =
                                    self.get_node_text(&path_node).trim_matches('"').to_string();

                                let alias = spec_node
                                    .child_by_field_name("name")
                                    .map(|n| self.get_node_text(&n));

                                imports.push(Import {
                                    module: path.clone(),
                                    items: if let Some(a) = alias { vec![a] } else { vec![] },
                                    import_type: self.classify_import(&path),
                                });
                            }
                        } else if spec_node.kind() == "import_spec_list" {
                            let mut list_cursor = spec_node.walk();
                            for item in spec_node.children(&mut list_cursor) {
                                if item.kind() == "import_spec" {
                                    if let Some(path_node) = item.child_by_field_name("path") {
                                        let path = self
                                            .get_node_text(&path_node)
                                            .trim_matches('"')
                                            .to_string();

                                        imports.push(Import {
                                            module: path.clone(),
                                            items: vec![],
                                            import_type: self.classify_import(&path),
                                        });
                                    }
                                }
                            }
                        }
                    }
                }
                _ => {}
            }
        }

        imports
    }

    fn classify_import(&self, module: &str) -> String {
        // Go stdlib packages
        let stdlib = [
            "fmt",
            "os",
            "io",
            "strings",
            "strconv",
            "time",
            "net",
            "http",
            "encoding/json",
            "context",
            "sync",
            "errors",
            "log",
            "bytes",
            "math",
            "sort",
            "regexp",
            "path",
            "bufio",
            "crypto",
            "database/sql",
        ];

        if stdlib.iter().any(|s| module.starts_with(s)) {
            "stdlib".to_string()
        } else if module.starts_with('.') || !module.contains('/') {
            "internal".to_string()
        } else {
            "external".to_string()
        }
    }

    fn extract_functions(&self, root: &Node) -> Vec<Function> {
        let mut functions = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "function_declaration" {
                if let Some(func) = self.parse_function(&child, "") {
                    functions.push(func);
                }
            }
        }

        functions
    }

    fn parse_function(&self, node: &Node, struct_context: &str) -> Option<Function> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);

        // Check if it's a method (has receiver)
        let receiver = node
            .child_by_field_name("receiver")
            .map(|r| self.get_node_text(&r));

        let params = self.extract_parameters(node);
        let return_type = self.extract_return_type(node);
        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);
        let signature = self.build_signature(&name, &params, &return_type, receiver.as_deref());

        let body = node.child_by_field_name("body")?;
        let calls = self.extract_function_calls_detailed(&body);
        let variables = self.extract_variables(&body, &params);
        let control_flow = self.build_control_flow(&body);
        let exceptions = self.extract_exception_info(&body);
        let complexity = self.calculate_complexity(&body);

        let id = if struct_context.is_empty() {
            format!("func_{}", name)
        } else {
            format!("method_{}_{}", struct_context, name)
        };

        let tags = self.auto_tag_function(&name, &docstring, &calls);
        let importance_score = self.estimate_importance(&name, receiver.is_some());

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
            called_by: vec![], // Will be populated during post-processing
            variables,
            control_flow,
            exceptions,
            complexity,
            is_async: false,
            decorators: vec![],
            tags,
            importance_score,
        })
    }

    fn extract_parameters(&self, node: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();

        if let Some(param_list) = node.child_by_field_name("parameters") {
            let mut cursor = param_list.walk();
            for child in param_list.children(&mut cursor) {
                if child.kind() == "parameter_declaration" {
                    let param_text = self.get_node_text(&child);

                    if let Some(name_node) = child.child_by_field_name("name") {
                        let name = self.get_node_text(&name_node);
                        let type_annotation = child
                            .child_by_field_name("type")
                            .map(|t| self.get_node_text(&t))
                            .unwrap_or_default();

                        params.push(Parameter {
                            name,
                            type_annotation,
                            default_value: None,
                        });
                    } else {
                        // Handle unnamed parameters or variadic
                        let parts: Vec<&str> = param_text.split_whitespace().collect();
                        if parts.len() >= 2 {
                            params.push(Parameter {
                                name: parts[0].to_string(),
                                type_annotation: parts[1..].join(" "),
                                default_value: None,
                            });
                        }
                    }
                }
            }
        }

        params
    }

    fn extract_return_type(&self, node: &Node) -> String {
        if let Some(result) = node.child_by_field_name("result") {
            self.get_node_text(&result)
        } else {
            String::new()
        }
    }

    fn build_signature(
        &self,
        name: &str,
        params: &[Parameter],
        return_type: &str,
        receiver: Option<&str>,
    ) -> String {
        let param_str = params
            .iter()
            .map(|p| format!("{} {}", p.name, p.type_annotation))
            .collect::<Vec<_>>()
            .join(", ");

        let receiver_str = receiver.map(|r| format!("{} ", r)).unwrap_or_default();

        if return_type.is_empty() {
            format!("func {}{}({})", receiver_str, name, param_str)
        } else {
            format!(
                "func {}{}({}) {}",
                receiver_str, name, param_str, return_type
            )
        }
    }

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
            "for_statement" => "loop",
            "switch_statement" | "expression_switch_statement" => "switch",
            _ => context,
        };

        if node.kind() == "call_expression" {
            if let Some(func_node) = node.child_by_field_name("function") {
                let call_name = self.get_node_text(&func_node);
                let name = call_name
                    .split('.')
                    .last()
                    .unwrap_or(&call_name)
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

    fn extract_variables(&self, node: &Node, params: &[Parameter]) -> Vec<Variable> {
        let mut variables: HashMap<String, Variable> = HashMap::new();

        for param in params {
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

        self.track_variable_usage(node, &mut variables);
        variables.into_values().collect()
    }

    fn track_variable_usage(&self, node: &Node, variables: &mut HashMap<String, Variable>) {
        let mut cursor = node.walk();

        match node.kind() {
            "short_var_declaration" | "var_declaration" => {
                if let Some(left) = node.child_by_field_name("left") {
                    let var_name = self.get_node_text(&left);
                    let line = node.start_position().row + 1;

                    let var_type = node
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t));

                    if !variables.contains_key(&var_name) {
                        variables.insert(
                            var_name.clone(),
                            Variable {
                                name: var_name,
                                var_type,
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
            "return_statement" => {
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
            _ => {}
        }

        for child in node.children(&mut cursor) {
            self.track_variable_usage(&child, variables);
        }
    }

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
            "for_statement" => {
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
        let calls = self.extract_calls_from_block(block);
        let returns = self.find_return_value(block);
        let raises = None;

        Some(ExecutionPath {
            calls,
            returns,
            raises,
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
                let mut ret_vals = Vec::new();
                let mut ret_cursor = child.walk();
                for ret_child in child.children(&mut ret_cursor) {
                    if ret_child.kind() != "return" {
                        ret_vals.push(self.get_node_text(&ret_child));
                    }
                }
                return Some(ret_vals.join(", "));
            }
        }

        None
    }

    fn parse_loop(&self, node: &Node) -> Option<Loop> {
        let line = node.start_position().row + 1;
        let condition = node
            .child_by_field_name("condition")
            .map(|c| self.get_node_text(&c))
            .unwrap_or_default();

        let calls = self.extract_calls_from_block(node);

        Some(Loop {
            loop_type: "for".to_string(),
            condition,
            line,
            calls,
        })
    }

    fn extract_exception_info(&self, node: &Node) -> ExceptionInfo {
        let mut info = ExceptionInfo::default();
        self.find_panics(node, &mut info);
        info
    }

    fn find_panics(&self, node: &Node, info: &mut ExceptionInfo) {
        let mut cursor = node.walk();

        if node.kind() == "call_expression" {
            if let Some(func_node) = node.child_by_field_name("function") {
                let func_name = self.get_node_text(&func_node);
                if func_name == "panic" {
                    info.raises.push("panic".to_string());
                }
            }
        }

        for child in node.children(&mut cursor) {
            self.find_panics(&child, info);
        }
    }

    fn extract_structs(&self, root: &Node) -> Vec<Class> {
        let mut structs = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "type_declaration" {
                if let Some(spec) = child.child_by_field_name("spec") {
                    if let Some(struct_data) = self.parse_struct(&spec) {
                        structs.push(struct_data);
                    }
                }
            }
        }

        // Find methods for structs
        let mut methods_map: HashMap<String, Vec<Function>> = HashMap::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "function_declaration" {
                if let Some(receiver) = child.child_by_field_name("receiver") {
                    let receiver_text = self.get_node_text(&receiver);
                    let type_name = receiver_text
                        .trim_start_matches('(')
                        .trim_end_matches(')')
                        .split_whitespace()
                        .last()
                        .unwrap_or("")
                        .trim_start_matches('*')
                        .to_string();

                    if let Some(method) = self.parse_function(&child, &type_name) {
                        methods_map
                            .entry(type_name)
                            .or_insert_with(Vec::new)
                            .push(method);
                    }
                }
            }
        }

        for struct_data in &mut structs {
            if let Some(methods) = methods_map.remove(&struct_data.name) {
                struct_data.methods = methods;
            }
        }

        structs
    }

    fn parse_struct(&self, node: &Node) -> Option<Class> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        let attributes = if let Some(type_node) = node.child_by_field_name("type") {
            if type_node.kind() == "struct_type" {
                self.extract_struct_fields(&type_node)
            } else {
                vec![]
            }
        } else {
            vec![]
        };

        Some(Class {
            id: format!("struct_{}", name),
            name,
            bases: vec![],
            docstring,
            line_start,
            line_end,
            methods: vec![],
            attributes,
            decorators: vec![],
        })
    }

    fn extract_struct_fields(&self, struct_node: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();

        if let Some(body) = struct_node.child_by_field_name("body") {
            let mut cursor = body.walk();
            for child in body.children(&mut cursor) {
                if child.kind() == "field_declaration" {
                    if let Some(name_node) = child.child_by_field_name("name") {
                        let name = self.get_node_text(&name_node);
                        let type_annotation = child
                            .child_by_field_name("type")
                            .map(|t| self.get_node_text(&t))
                            .unwrap_or_default();

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
            if child.kind() == "var_declaration" {
                let mut var_cursor = child.walk();
                for spec in child.children(&mut var_cursor) {
                    if spec.kind() == "var_spec" {
                        if let Some(var) = self.parse_global_var(&spec) {
                            vars.push(var);
                        }
                    }
                }
            } else if child.kind() == "const_declaration" {
                let mut const_cursor = child.walk();
                for spec in child.children(&mut const_cursor) {
                    if spec.kind() == "const_spec" {
                        if let Some(var) = self.parse_global_var(&spec) {
                            vars.push(var);
                        }
                    }
                }
            }
        }

        vars
    }

    fn parse_global_var(&self, node: &Node) -> Option<GlobalVar> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);
        let line = node.start_position().row + 1;

        let type_annotation = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_default();

        let value = node
            .child_by_field_name("value")
            .map(|v| self.get_node_text(&v));

        Some(GlobalVar {
            name,
            type_annotation,
            value,
            line,
        })
    }

    fn extract_docstring(&self, node: &Node) -> String {
        if let Some(prev) = node.prev_sibling() {
            if prev.kind() == "comment" {
                return self
                    .get_node_text(&prev)
                    .trim_start_matches("//")
                    .trim()
                    .to_string();
            }
        }
        String::new()
    }

    fn calculate_complexity(&self, node: &Node) -> usize {
        let mut complexity = 1;

        fn count_complexity_nodes(node: &Node) -> usize {
            let mut count = 0;
            let mut cursor = node.walk();

            match node.kind() {
                "if_statement"
                | "for_statement"
                | "switch_statement"
                | "expression_switch_statement"
                | "binary_expression" => {
                    count += 1;
                }
                _ => {}
            }

            for child in node.children(&mut cursor) {
                count += count_complexity_nodes(&child);
            }

            count
        }

        complexity += count_complexity_nodes(node);
        complexity
    }

    fn extract_todos(&self) -> Vec<Todo> {
        let re = Regex::new(r"//\s*TODO:?\s*(.+)").unwrap();

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

    fn detect_security_patterns(&self) -> Vec<SecurityNote> {
        let mut notes = Vec::new();

        let patterns = vec![
            (
                r"password|secret|token|apikey",
                "sensitive_data",
                "Handles sensitive data",
            ),
            (r"eval\(", "code_execution", "Dynamic code execution"),
            (
                r"exec\.Command|os\.Exec",
                "command_execution",
                "System command execution",
            ),
            (r"unsafe\.", "unsafe_code", "Uses unsafe operations"),
            (
                r"sql\.Query|db\.Query",
                "sql_query",
                "Database query - check for SQL injection",
            ),
        ];

        for (pattern, note_type, description) in patterns {
            if let Ok(re) = Regex::new(pattern) {
                for (idx, line) in self.source_code.lines().enumerate() {
                    if re.is_match(&line.to_lowercase()) {
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

    fn auto_tag_function(
        &self,
        name: &str,
        docstring: &str,
        calls: &[FunctionCall],
    ) -> Vec<String> {
        let mut tags = Vec::new();
        let name_lower = name.to_lowercase();
        let doc_lower = docstring.to_lowercase();

        // Entry point
        if name == "main" || name == "run" || name == "start" {
            tags.push("entry-point".to_string());
        }

        // Initialization
        if name_lower.contains("init")
            || name_lower.contains("setup")
            || name_lower.contains("initialize")
            || name_lower.contains("bootstrap")
        {
            tags.push("initialization".to_string());
        }

        // Cleanup
        if name_lower.contains("cleanup")
            || name_lower.contains("close")
            || name_lower.contains("shutdown")
            || name_lower.contains("dispose")
        {
            tags.push("cleanup".to_string());
        }

        // Authentication & Security
        if name_lower.contains("auth")
            || name_lower.contains("login")
            || name_lower.contains("logout")
            || name_lower.contains("password")
            || name_lower.contains("hash")
            || name_lower.contains("encrypt")
            || name_lower.contains("decrypt")
            || name_lower.contains("token")
            || doc_lower.contains("authentication")
        {
            tags.push("authentication".to_string());
            tags.push("security".to_string());
        }

        // API & HTTP
        if name_lower.contains("api")
            || name_lower.contains("endpoint")
            || name_lower.contains("route")
            || name_lower.contains("handler")
            || name_lower.contains("serve")
            || name_lower.contains("http")
            || doc_lower.contains("http")
            || doc_lower.contains("endpoint")
        {
            tags.push("api".to_string());
        }

        if name_lower.contains("handler")
            || name_lower.contains("serve")
            || name_lower.contains("controller")
        {
            tags.push("http-handler".to_string());
        }

        // Database
        if name_lower.contains("db")
            || name_lower.contains("database")
            || name_lower.contains("query")
            || name_lower.contains("select")
            || name_lower.contains("insert")
            || name_lower.contains("update")
            || name_lower.contains("delete")
            || name_lower.contains("save")
            || name_lower.contains("find")
        {
            tags.push("database".to_string());
        }

        // Validation
        if name_lower.contains("validate")
            || name_lower.contains("check")
            || name_lower.contains("verify")
            || name_lower.contains("sanitize")
        {
            tags.push("validation".to_string());
        }

        // Error handling
        if name_lower.contains("error")
            || name_lower.contains("handle") && doc_lower.contains("error")
        {
            tags.push("error-handling".to_string());
        }

        // Utilities
        if name_lower.contains("util") || name_lower.contains("helper") {
            tags.push("utility".to_string());
        }

        // Testing
        if name_lower.starts_with("test") || name_lower.starts_with("bench") {
            tags.push("testing".to_string());
        }

        // File I/O
        if name_lower.contains("read")
            || name_lower.contains("write")
            || name_lower.contains("file")
            || name_lower.contains("open")
            || name_lower.contains("load")
        {
            tags.push("file-io".to_string());
        }

        // Network
        if name_lower.contains("dial")
            || name_lower.contains("listen")
            || name_lower.contains("connect")
            || name_lower.contains("request")
            || name_lower.contains("response")
        {
            tags.push("network".to_string());
        }

        // Configuration
        if name_lower.contains("config")
            || name_lower.contains("setting")
            || name_lower.contains("option")
        {
            tags.push("configuration".to_string());
        }

        // Logging
        if name_lower.contains("log") || name_lower.contains("debug") {
            tags.push("logging".to_string());
        }

        // Parsing
        if name_lower.contains("parse")
            || name_lower.contains("decode")
            || name_lower.contains("unmarshal")
        {
            tags.push("parsing".to_string());
        }

        // Serialization
        if name_lower.contains("serialize")
            || name_lower.contains("encode")
            || name_lower.contains("marshal")
        {
            tags.push("serialization".to_string());
        }

        // Goroutines/Concurrency
        if calls
            .iter()
            .any(|c| c.callee.contains("Go") || c.callee == "go")
        {
            tags.push("concurrent".to_string());
            tags.push("goroutine".to_string());
        }

        // Channels
        if calls
            .iter()
            .any(|c| c.callee.contains("chan") || c.callee.contains("channel"))
            || name_lower.contains("channel")
        {
            tags.push("channels".to_string());
            tags.push("concurrent".to_string());
        }

        // Defer/Panic/Recover
        if calls.iter().any(|c| c.callee == "defer") {
            tags.push("deferred".to_string());
        }
        if calls.iter().any(|c| c.callee == "panic") {
            tags.push("panics".to_string());
        }
        if calls.iter().any(|c| c.callee == "recover") {
            tags.push("recovers".to_string());
        }

        // Context
        if name_lower.contains("context") || calls.iter().any(|c| c.callee.contains("Context")) {
            tags.push("context".to_string());
        }

        // Middleware
        if name_lower.contains("middleware") {
            tags.push("middleware".to_string());
        }

        // Exported functions (start with uppercase)
        if name.chars().next().map_or(false, |c| c.is_uppercase()) {
            tags.push("exported".to_string());
        }

        // Remove duplicates and sort
        tags.sort();
        tags.dedup();
        tags
    }

    fn estimate_importance(&self, name: &str, is_method: bool) -> f32 {
        let mut score: f32 = 0.5;

        if name == "main" {
            score += 0.3;
        }

        if name.chars().next().map_or(false, |c| c.is_uppercase()) {
            score += 0.1;
        }

        if is_method {
            score += 0.1;
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

    let parser = GoParser::new(source_code);
    let file_data = parser.parse()?;

    let relative_path = path.to_string_lossy().to_string();

    Ok((relative_path, file_data))
}
