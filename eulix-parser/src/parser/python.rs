//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::struc::kb_struct::*;
use once_cell::sync::Lazy;
use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use tree_sitter::{Node, Parser};

struct SecurityPattern {
    regex: &'static Lazy<Regex>,
    note_type: &'static str,
    description: &'static str,
}

//  Regex Patterns compiled once at first use
static FROM_IMPORT_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"from\s+(\S+)\s+import\s+(.+)").expect("Invalid from-import regex"));

static ATTRIBUTE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(\w+)\s*:\s*([^=]+)(?:=\s*(.+))?").expect("Invalid attribute regex"));

static TODO_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"#\s*TODO:?\s*(.+)").expect("Invalid TODO regex"));

static PASSWORD_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"password").expect("Invalid password regex"));

static SENSITIVE_DATA_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"secret|api_key|token").expect("Invalid sensitive_data regex"));

static EVAL_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"eval\(").expect("Invalid eval regex"));

static EXEC_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"exec\(").expect("Invalid exec regex"));

static DYNAMIC_IMPORT_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"__import__").expect("Invalid dynamic_import regex"));

static PICKLE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"pickle\.load").expect("Invalid pickle regex"));

static COMMAND_EXEC_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"subprocess|os\.system|os\.popen").expect("Invalid command_exec regex")
});

static SECURITY_PATTERNS: Lazy<Vec<SecurityPattern>> = Lazy::new(|| {
    vec![
        SecurityPattern {
            regex: &PASSWORD_RE,
            note_type: "password_handling",
            description: "Handles passwords",
        },
        SecurityPattern {
            regex: &SENSITIVE_DATA_RE,
            note_type: "sensitive_data",
            description: "Handles sensitive data",
        },
        SecurityPattern {
            regex: &EVAL_RE,
            note_type: "code_execution",
            description: "Uses eval() - potential security risk",
        },
        SecurityPattern {
            regex: &EXEC_RE,
            note_type: "code_execution",
            description: "Uses exec() - potential security risk",
        },
        SecurityPattern {
            regex: &DYNAMIC_IMPORT_RE,
            note_type: "dynamic_import",
            description: "Dynamic imports detected",
        },
        SecurityPattern {
            regex: &PICKLE_RE,
            note_type: "deserialization",
            description: "Uses pickle - potential security risk",
        },
        SecurityPattern {
            regex: &COMMAND_EXEC_RE,
            note_type: "command_execution",
            description: "System command execution",
        },
    ]
});

pub struct PythonParser {
    source_code: String,
    file_path: String,
}

impl PythonParser {
    pub fn new(source_code: String, file_path: String) -> Self {
        Self {
            source_code,
            file_path,
         }
    }

    fn make_function_id(&self, name: &str, struct_context: &str) -> String {
        if struct_context.is_empty() {
            format!("func_{}::{}", name, self.file_path)
        } else {
            format!("method_{}_{}::{}", struct_context, name, self.file_path)
        }
    }

    fn make_class_id(&self, name: &str) -> String {
        format!("class_{}::{}", name, self.file_path)
    }

    pub fn parse(&self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_python::language())
            .map_err(|e| format!("Failed to load Python grammar: {}", e))?;

        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse Python file".to_string())?;

        let root = tree.root_node();

        Ok(FileData {
            language: "python".to_string(),
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

    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            match child.kind() {
                "import_statement" => {
                    let module = self.get_node_text(&child);
                    let cleaned = module
                        .replace("import ", "")
                        .split(',')
                        .next()
                        .unwrap_or("")
                        .trim()
                        .to_string();

                    if !cleaned.is_empty() {
                        imports.push(Import {
                            module: cleaned.clone(),
                            items: vec![],
                            import_type: self.classify_import(&cleaned),
                        });
                    }
                }
                "import_from_statement" => {
                    if let Some(import) = self.parse_import_from(&child) {
                        imports.push(import);
                    }
                }
                _ => {}
            }
        }

        imports
    }

    fn classify_import(&self, module: &str) -> String {
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

        if stdlib.contains(&module) {
            "stdlib".to_string()
        } else if module.starts_with('.') || module.contains('/') {
            "internal".to_string()
        } else {
            "external".to_string()
        }
    }

    fn parse_import_from(&self, node: &Node) -> Option<Import> {
        let text = self.get_node_text(node);

        if let Some(caps) = FROM_IMPORT_RE.captures(&text) {
            let module = caps.get(1)?.as_str().to_string();
            let items_str = caps.get(2)?.as_str();
            let items: Vec<String> = items_str
                .split(',')
                .map(|s| s.trim().split_whitespace().next().unwrap_or(s.trim()))
                .map(|s| s.to_string())
                .filter(|s| !s.is_empty())
                .collect();

            return Some(Import {
                module: module.clone(),
                items,
                import_type: self.classify_import(&module),
            });
        }

        None
    }

    fn extract_functions(&self, root: &Node) -> Vec<Function> {
        let mut functions = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            match child.kind() {
                "function_definition" => {
                    if let Some(func) = self.parse_function(&child, "") {
                        functions.push(func);
                    }
                }
                "decorated_definition" => {
                    let mut inner_cursor = child.walk();
                    for inner in child.children(&mut inner_cursor) {
                        if inner.kind() == "function_definition" {
                            if let Some(func) = self.parse_function(&inner, "") {
                                functions.push(func);
                            }
                        }
                    }
                }
                _ => {}
            }
        }

        functions
    }

    fn parse_function(&self, node: &Node, class_context: &str) -> Option<Function> {
        let mut cursor = node.walk();
        let mut name = String::new();
        let is_async = node.kind() == "async_function_definition";

        let mut decorators = Vec::new();
        if let Some(parent) = node.parent() {
            if parent.kind() == "decorated_definition" {
                let mut cur = parent.walk();
                for child in parent.children(&mut cur) {
                    if child.kind() == "decorator" {
                        decorators.push(self.get_node_text(&child));
                    }
                }
            }
        }

        for child in node.children(&mut cursor) {
            if child.kind() == "identifier" && name.is_empty() {
                name = self.get_node_text(&child);
                break;
            }
        }

        if name.is_empty() {
            return None;
        }

        let params = self.extract_parameters(node);
        let return_type = self.extract_return_type(node);
        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);
        let signature = self.build_signature(&name, &params, &return_type, is_async);

        let calls = self.extract_function_calls_detailed(node);

        let variables = self.extract_variables(node, &params);

        let control_flow = self.build_control_flow(node);

        let exceptions = self.extract_exception_info(node);

        let complexity = self.calculate_complexity(node);

        let id = self.make_function_id(&name, class_context);

        let tags = self.auto_tag_function(&name, &docstring, &calls);

        let importance_score = self.estimate_importance(&name, &decorators);

        let python_info = self.classify_decorators(&decorators);

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
            is_async,
            decorators,
            tags,
            importance_score,
            lang_info: LanguageSpecificInfo {
                python: Some(python_info),
                ..Default::default()
            },
        })
    }

    fn extract_parameters(&self, node: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            if child.kind() == "parameters" {
                let mut param_cursor = child.walk();
                for param_node in child.children(&mut param_cursor) {
                    match param_node.kind() {
                        "identifier" => {
                            let name = self.get_node_text(&param_node);
                            if name != "self" && name != "cls" {
                                params.push(Parameter {
                                    name,
                                    type_annotation: String::new(),
                                    default_value: None,
                                });
                            }
                        }
                        "typed_parameter" | "default_parameter" | "typed_default_parameter" => {
                            if let Some(param) = self.parse_parameter(&param_node) {
                                if param.name != "self" && param.name != "cls" {
                                    params.push(param);
                                }
                            }
                        }
                        _ => {}
                    }
                }
                break;
            }
        }

        params
    }

    fn parse_parameter(&self, node: &Node) -> Option<Parameter> {
        let text = self.get_node_text(node);

        let parts: Vec<&str> = text.splitn(2, '=').collect();
        let name_type_part = parts[0].trim();
        let default_value = if parts.len() > 1 {
            Some(parts[1].trim().to_string())
        } else {
            None
        };

        if name_type_part.contains(':') {
            let type_parts: Vec<&str> = name_type_part.split(':').collect();
            Some(Parameter {
                name: type_parts[0].trim().to_string(),
                type_annotation: type_parts
                    .get(1)
                    .map(|s| s.trim().to_string())
                    .unwrap_or_default(),
                default_value,
            })
        } else {
            Some(Parameter {
                name: name_type_part.to_string(),
                type_annotation: String::new(),
                default_value,
            })
        }
    }

    fn extract_return_type(&self, node: &Node) -> String {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            if child.kind() == "type" {
                return self.get_node_text(&child);
            }
        }

        String::new()
    }

    fn build_signature(
        &self,
        name: &str,
        params: &[Parameter],
        return_type: &str,
        is_async: bool,
    ) -> String {
        let async_prefix = if is_async { "async " } else { "" };
        let param_str = params
            .iter()
            .map(|p| {
                if p.type_annotation.is_empty() {
                    p.name.clone()
                } else {
                    format!("{}: {}", p.name, p.type_annotation)
                }
            })
            .collect::<Vec<_>>()
            .join(", ");

        if return_type.is_empty() {
            format!("{}def {}({})", async_prefix, name, param_str)
        } else {
            format!(
                "{}def {}({}) -> {}",
                async_prefix, name, param_str, return_type
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
            "elif_clause" => "elif",
            "else_clause" => "else",
            "while_statement" | "for_statement" => "loop",
            "try_statement" => "try",
            "except_clause" => "except",
            _ => context,
        };

        if node.kind() == "call" {
            if let Some(func_node) = node.child_by_field_name("function") {
                if let Ok(call_name) = func_node.utf8_text(self.source_code.as_bytes()) {
                    let name = call_name
                        .split('.')
                        .last()
                        .unwrap_or(call_name)
                        .trim()
                        .to_string();

                    if !name.is_empty() {
                        let key = format!("{}:{}", name, node.start_position().row);
                        if !seen.contains(&key) {
                            seen.insert(key);

                            let args = self.extract_call_arguments(node);

                            calls.push(FunctionCall {
                                callee: name,
                                defined_in: None, // Will be resolved in post-processing
                                line: node.start_position().row + 1,
                                args,
                                is_conditional: context != "unconditional",
                                context: context.to_string(),
                            });
                        }
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
                if child.kind() == "identifier"
                    || child.kind() == "string"
                    || child.kind() == "integer"
                {
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

        if node.kind() == "assignment" {
            if let Some(left) = node.child_by_field_name("left") {
                if let Some(right) = node.child_by_field_name("right") {
                    let var_name = self.get_node_text(&left);
                    let line = node.start_position().row + 1;

                    if right.kind() == "call" {
                        if let Some(func_node) = right.child_by_field_name("function") {
                            let func_name = self.get_node_text(&func_node);

                            if let Some(var) = variables.get_mut(&var_name) {
                                var.transformations.push(VarTransformation {
                                    line,
                                    via: func_name.clone(),
                                    becomes: var_name.clone(),
                                });
                            } else {
                                variables.insert(
                                    var_name.clone(),
                                    Variable {
                                        name: var_name.clone(),
                                        var_type: None,
                                        scope: "local".to_string(),
                                        defined_at: Some(line),
                                        transformations: vec![],
                                        used_in: vec![],
                                        returned: false,
                                    },
                                );
                            }
                        }
                    } else {
                        if !variables.contains_key(&var_name) {
                            variables.insert(
                                var_name.clone(),
                                Variable {
                                    name: var_name.clone(),
                                    var_type: None,
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
            }
        }

        if node.kind() == "return_statement" {
            if let Some(value) = node.child(1) {
                let returned_var = self.get_node_text(&value);
                if let Some(var) = variables.get_mut(&returned_var) {
                    var.returned = true;
                }
            }
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
            "while_statement" | "for_statement" => {
                if let Some(loop_info) = self.parse_loop(node) {
                    cf.loops.push(loop_info);
                }
            }
            "try_statement" => {
                if let Some(try_block) = self.parse_try_statement(node) {
                    cf.try_blocks.push(try_block);
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
        let condition = self.extract_condition(node)?;

        let true_path = self.extract_execution_path(node, "consequence")?;
        let false_path = self.extract_execution_path(node, "alternative");

        Some(Branch {
            branch_type: "if".to_string(),
            condition,
            line,
            true_path,
            false_path,
        })
    }

    fn extract_condition(&self, node: &Node) -> Option<String> {
        if let Some(cond_node) = node.child_by_field_name("condition") {
            Some(self.get_node_text(&cond_node))
        } else {
            None
        }
    }

    fn extract_execution_path(&self, node: &Node, field: &str) -> Option<ExecutionPath> {
        let block = node.child_by_field_name(field)?;
        let calls = self.extract_calls_from_block(&block);
        let returns = self.find_return_value(&block);
        let raises = self.find_raise_value(&block);

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

        if node.kind() == "call" {
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
                if let Some(value) = child.child(1) {
                    return Some(self.get_node_text(&value));
                }
            }
        }

        None
    }

    fn find_raise_value(&self, node: &Node) -> Option<String> {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            if child.kind() == "raise_statement" {
                if let Some(exc) = child.child(1) {
                    return Some(self.get_node_text(&exc));
                }
            }
        }

        None
    }

    fn parse_loop(&self, node: &Node) -> Option<Loop> {
        let loop_type = if node.kind() == "for_statement" {
            "for"
        } else {
            "while"
        };
        let line = node.start_position().row + 1;
        let condition = self.extract_condition(node).unwrap_or_default();
        let calls = self.extract_calls_from_block(node);

        Some(Loop {
            loop_type: loop_type.to_string(),
            condition,
            line,
            calls,
        })
    }

    fn parse_try_statement(&self, node: &Node) -> Option<TryBlock> {
        let line = node.start_position().row + 1;
        let try_calls = self.extract_calls_from_block(node);
        let except_clauses = self.extract_except_clauses(node);
        let finally_calls = self.extract_finally_calls(node);

        Some(TryBlock {
            line,
            try_calls,
            except_clauses,
            finally_calls,
        })
    }

    fn extract_except_clauses(&self, node: &Node) -> Vec<ExceptClause> {
        let mut clauses = Vec::new();
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            if child.kind() == "except_clause" {
                let exception_type = if let Some(exc_node) = child.child(1) {
                    self.get_node_text(&exc_node)
                } else {
                    "Exception".to_string()
                };

                clauses.push(ExceptClause {
                    exception_type,
                    line: child.start_position().row + 1,
                    calls: self.extract_calls_from_block(&child),
                });
            }
        }

        clauses
    }

    fn extract_finally_calls(&self, node: &Node) -> Vec<String> {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            if child.kind() == "finally_clause" {
                return self.extract_calls_from_block(&child);
            }
        }

        vec![]
    }

    fn extract_exception_info(&self, node: &Node) -> ExceptionInfo {
        let mut info = ExceptionInfo::default();

        self.find_exceptions(node, &mut info);
        info
    }

    fn find_exceptions(&self, node: &Node, info: &mut ExceptionInfo) {
        let mut cursor = node.walk();

        match node.kind() {
            "raise_statement" => {
                if let Some(exc) = node.child(1) {
                    let exc_type = self.get_node_text(&exc);
                    if !info.raises.contains(&exc_type) {
                        info.raises.push(exc_type);
                    }
                }
            }
            "except_clause" => {
                if let Some(exc) = node.child(1) {
                    let exc_type = self.get_node_text(&exc);
                    if !info.handles.contains(&exc_type) {
                        info.handles.push(exc_type);
                    }
                }
            }
            _ => {}
        }

        for child in node.children(&mut cursor) {
            self.find_exceptions(&child, info);
        }
    }

    fn extract_classes(&self, root: &Node) -> Vec<Class> {
        let mut classes = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            match child.kind() {
                "class_definition" => {
                    if let Some(class) = self.parse_class(&child) {
                        classes.push(class);
                    }
                }
                "decorated_definition" => {
                    let mut inner_cursor = child.walk();
                    for inner in child.children(&mut inner_cursor) {
                        if inner.kind() == "class_definition" {
                            if let Some(class) = self.parse_class(&inner) {
                                classes.push(class);
                            }
                        }
                    }
                }
                _ => {}
            }
        }

        classes
    }

    fn parse_class(&self, node: &Node) -> Option<Class> {
        let mut cursor = node.walk();
        let mut name = String::new();
        let mut bases = Vec::new();
        let mut methods = Vec::new();
        let mut attributes = Vec::new();
        let mut decorators = Vec::new();

        // Extract decorators
        if let Some(parent) = node.parent() {
            if parent.kind() == "decorated_definition" {
                let mut cur = parent.walk();
                for child in parent.children(&mut cur) {
                    if child.kind() == "decorator" {
                        decorators.push(self.get_node_text(&child));
                    }
                }
            }
        }

        for child in node.children(&mut cursor) {
            match child.kind() {
                "identifier" => {
                    if name.is_empty() {
                        name = self.get_node_text(&child);
                    }
                }
                "argument_list" => {
                    // This is where base classes are defined
                    bases = self.extract_base_classes(&child);
                }
                "block" => {
                    let (class_methods, class_attrs) = self.parse_class_body(&child, &name);
                    methods = class_methods;
                    attributes = class_attrs;
                }
                _ => {}
            }
        }

        if name.is_empty() {
            return None;
        }

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        Some(Class {
            id: self.make_class_id(&name),
            name,
            bases,
            docstring,
            line_start,
            line_end,
            methods,
            attributes,
            decorators,
            lang_info: LanguageSpecificInfo {
                python: Some(PythonInfo::default()),
                ..Default::default()
            },
        })
    }

    fn classify_decorators(&self, decorators: &[String]) -> PythonInfo {
        let mut info = PythonInfo {
            is_dataclass: false,
            is_staticmethod: false,
            is_classmethod: false,
            is_property: false,
            is_property_setter: false,
            is_property_deleter: false,
            is_abstractmethod: false,
            is_cached_property: false,
            is_overload: false,
            is_override: false,
            is_final: false,
            flask_route: None,
            unknown_decorators: Vec::new(),
        };

        for raw in decorators {
            let d = raw.trim().trim_start_matches('@');
            let base = d.split('(').next().unwrap_or(d).trim();
            let leaf = base.split('.').last().unwrap_or(base);

            match leaf {
                "dataclass" => info.is_dataclass = true,
                "staticmethod" => info.is_staticmethod = true,
                "classmethod" => info.is_classmethod = true,
                "property" => info.is_property = true,
                "setter" => info.is_property_setter = true,
                "deleter" => info.is_property_deleter = true,
                "abstractmethod" | "abstractstaticmethod" | "abstractclassmethod" => {
                    info.is_abstractmethod = true
                }
                "cached_property" => info.is_cached_property = true,
                "overload" => info.is_overload = true,
                "override" => info.is_override = true,
                "final" => info.is_final = true,
                "route" | "get" | "post" | "put" | "patch" | "delete" => {
                    let path = self.extract_decorator_arg(raw);
                    info.flask_route = path;
                }
                _ => info.unknown_decorators.push(raw.clone()),
            }
        }

        info
    }

    fn extract_decorator_arg(&self, raw: &str) -> Option<String> {
        let start = raw.find('(')?;
        let inner = &raw[start + 1..];
        for quote in ['"', '\''] {
            if let Some(q_start) = inner.find(quote) {
                let after = &inner[q_start + 1..];
                if let Some(q_end) = after.find(quote) {
                    return Some(after[..q_end].to_string());
                }
            }
        }
        None
    }

    fn extract_base_classes(&self, node: &Node) -> Vec<String> {
        let mut bases = Vec::new();
        let mut cursor = node.walk();

        // The argument_list node contains all base classes and arguments
        // We need to find all identifiers and attributes that are base classes
        for child in node.children(&mut cursor) {
            match child.kind() {
                "identifier" | "attribute" | "call" => {
                    let text = self.get_node_text(&child);
                    // Skip built-in object (implicit in Python 3)
                    if text != "object" && !text.contains('=') && !text.contains('(') {
                        // Clean up the base class name
                        let base_name = text.trim();
                        if !base_name.is_empty() {
                            bases.push(base_name.to_string());
                        }
                    }
                }
                "subscript" => {
                    // Handle things like List[Base]
                    let text = self.get_node_text(&child);
                    // Extract just the base part before the [
                    if let Some(base_part) = text.split('[').next() {
                        let base_name = base_part.trim();
                        if !base_name.is_empty() && base_name != "object" {
                            bases.push(base_name.to_string());
                        }
                    }
                }
                _ => {}
            }
        }

        bases
    }

    fn parse_class_body(&self, node: &Node, class_name: &str) -> (Vec<Function>, Vec<Attribute>) {
        let mut methods = Vec::new();
        let mut attributes = Vec::new();
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "function_definition" => {
                    if let Some(method) = self.parse_function(&child, class_name) {
                        methods.push(method);
                    }
                }
                "expression_statement" => {
                    if let Some(attr) = self.parse_attribute(&child) {
                        attributes.push(attr);
                    }
                }
                _ => {}
            }
        }

        (methods, attributes)
    }

    fn parse_attribute(&self, node: &Node) -> Option<Attribute> {
        let text = self.get_node_text(node);

        if text.contains(':') && !text.contains("def ") {
            if let Some(caps) = ATTRIBUTE_RE.captures(&text) {
                return Some(Attribute {
                    name: caps.get(1)?.as_str().trim().to_string(),
                    type_annotation: caps.get(2)?.as_str().trim().to_string(),
                    value: caps.get(3).map(|m| m.as_str().trim().to_string()),
                });
            }
        }

        None
    }

    fn extract_global_vars(&self, root: &Node) -> Vec<GlobalVar> {
        let mut vars = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "expression_statement" {
                if let Some(var) = self.parse_global_var(&child) {
                    vars.push(var);
                }
            }
        }

        vars
    }

    fn parse_global_var(&self, node: &Node) -> Option<GlobalVar> {
        let text = self.get_node_text(node);
        let line = node.start_position().row + 1;

        if text.starts_with("def ") || text.starts_with("class ") || text.starts_with("@") {
            return None;
        }

        if text.contains('=') {
            let parts: Vec<&str> = text.splitn(2, '=').collect();
            if parts.len() == 2 {
                let left = parts[0].trim();
                let value = parts[1].trim().to_string();

                if left.contains(':') {
                    let type_parts: Vec<&str> = left.splitn(2, ':').collect();
                    if type_parts.len() == 2 {
                        let name = type_parts[0].trim();
                        if !name.is_empty() && name.chars().all(|c| c.is_alphanumeric() || c == '_')
                        {
                            return Some(GlobalVar {
                                name: name.to_string(),
                                type_annotation: type_parts[1].trim().to_string(),
                                value: Some(value),
                                line,
                            });
                        }
                    }
                } else {
                    let name = left.trim();
                    if !name.is_empty() && name.chars().all(|c| c.is_alphanumeric() || c == '_') {
                        return Some(GlobalVar {
                            name: name.to_string(),
                            type_annotation: String::new(),
                            value: Some(value),
                            line,
                        });
                    }
                }
            }
        }

        None
    }

    fn extract_docstring(&self, node: &Node) -> String {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            if child.kind() == "block" {
                let mut block_cursor = child.walk();
                for stmt in child.children(&mut block_cursor) {
                    if stmt.kind() == "expression_statement" {
                        let mut expr_cursor = stmt.walk();
                        for expr in stmt.children(&mut expr_cursor) {
                            if expr.kind() == "string" {
                                let text = self.get_node_text(&expr);
                                return text
                                    .trim_start_matches(|c| c == '"' || c == '\'')
                                    .trim_end_matches(|c| c == '"' || c == '\'')
                                    .trim()
                                    .to_string();
                            }
                        }
                    }
                }
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
                "if_statement" | "elif_clause" | "while_statement" | "for_statement"
                | "except_clause" | "with_statement" | "and" | "or" => {
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
        self.source_code
            .lines()
            .enumerate()
            .filter_map(|(idx, line)| {
                TODO_RE.captures(line).map(|caps| {
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
        let source_lower = self.source_code.to_lowercase();

        for (idx, line) in source_lower.lines().enumerate() {
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
    ) -> Vec<String> {
        let mut tags = Vec::new();
        let name_lower = name.to_lowercase();
        let doc_lower = docstring.to_lowercase();

        if name == "main" || name == "run" || name == "start" || name == "__main__" {
            tags.push("entry-point".to_string());
        }

        if name_lower.contains("init")
            || name_lower.contains("setup")
            || name_lower.contains("initialize")
            || name_lower.contains("bootstrap")
        {
            tags.push("initialization".to_string());
        }

        if name_lower.contains("cleanup")
            || name_lower.contains("close")
            || name_lower.contains("shutdown")
            || name_lower.contains("dispose")
        {
            tags.push("cleanup".to_string());
        }

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

        if name_lower.contains("api")
            || name_lower.contains("endpoint")
            || name_lower.contains("route")
            || name_lower.contains("handler")
            || name_lower.contains("view")
            || doc_lower.contains("http")
            || doc_lower.contains("endpoint")
        {
            tags.push("api".to_string());
        }

        if name_lower.contains("handler")
            || name_lower.contains("view")
            || name_lower.contains("controller")
        {
            tags.push("http-handler".to_string());
        }

        if name_lower.contains("db")
            || name_lower.contains("database")
            || name_lower.contains("query")
            || name_lower.contains("select")
            || name_lower.contains("insert")
            || name_lower.contains("update")
            || name_lower.contains("delete")
            || name_lower.contains("save")
            || name_lower.contains("fetch")
            || name_lower.contains("find")
        {
            tags.push("database".to_string());
        }

        if name_lower.contains("validate")
            || name_lower.contains("check")
            || name_lower.contains("verify")
            || name_lower.contains("sanitize")
        {
            tags.push("validation".to_string());
        }

        if name_lower.contains("error")
            || name_lower.contains("exception")
            || (name_lower.contains("handle")
                && (doc_lower.contains("error") || doc_lower.contains("exception")))
        {
            tags.push("error-handling".to_string());
        }

        if name_lower.contains("util") || name_lower.contains("helper") {
            tags.push("utility".to_string());
        }

        if name_lower.starts_with("test_") || name_lower.starts_with("test") {
            tags.push("testing".to_string());
        }

        if name_lower.contains("read")
            || name_lower.contains("write")
            || name_lower.contains("file")
            || name_lower.contains("open")
            || name_lower.contains("load")
        {
            tags.push("file-io".to_string());
        }

        if name_lower.contains("socket")
            || name_lower.contains("connect")
            || name_lower.contains("request")
            || name_lower.contains("response")
        {
            tags.push("network".to_string());
        }

        if name_lower.contains("config")
            || name_lower.contains("setting")
            || name_lower.contains("option")
        {
            tags.push("configuration".to_string());
        }

        if name_lower.contains("log") || name_lower.contains("debug") {
            tags.push("logging".to_string());
        }

        if name_lower.contains("parse") || name_lower.contains("decode") {
            tags.push("parsing".to_string());
        }

        if name_lower.contains("serialize") || name_lower.contains("encode") {
            tags.push("serialization".to_string());
        }

        if calls
            .iter()
            .any(|c| c.callee.contains("await") || c.callee.contains("async"))
            || name_lower.contains("async")
        {
            tags.push("async".to_string());
            tags.push("coroutine".to_string());
        }

        if name.starts_with("__") && name.ends_with("__") {
            tags.push("dunder-method".to_string());

            match name {
                "__init__" => tags.push("constructor".to_string()),
                "__del__" => tags.push("destructor".to_string()),
                "__str__" | "__repr__" => tags.push("string-representation".to_string()),
                "__enter__" | "__exit__" => tags.push("context-manager".to_string()),
                "__call__" => tags.push("callable".to_string()),
                "__iter__" | "__next__" => tags.push("iterator".to_string()),
                "__getitem__" | "__setitem__" | "__delitem__" => {
                    tags.push("subscriptable".to_string())
                }
                "__len__" => tags.push("sized".to_string()),
                "__eq__" | "__ne__" | "__lt__" | "__le__" | "__gt__" | "__ge__" => {
                    tags.push("comparison".to_string())
                }
                "__add__" | "__sub__" | "__mul__" | "__div__" => {
                    tags.push("arithmetic".to_string())
                }
                _ => {}
            }
        }

        if name.starts_with("_") && !name.starts_with("__") {
            tags.push("protected".to_string());
        }
        if name.starts_with("__") && !name.ends_with("__") {
            tags.push("private".to_string());
        }

        if name_lower.contains("property") || doc_lower.contains("@property") {
            tags.push("property".to_string());
        }

        if calls
            .iter()
            .any(|c| c.callee.contains("Thread") || c.callee.contains("threading"))
        {
            tags.push("threading".to_string());
            tags.push("concurrent".to_string());
        }

        if calls
            .iter()
            .any(|c| c.callee.contains("Process") || c.callee.contains("multiprocessing"))
        {
            tags.push("multiprocessing".to_string());
            tags.push("concurrent".to_string());
        }

        if calls.iter().any(|c| c.callee == "yield") {
            tags.push("generator".to_string());
        }

        if name_lower.contains("classmethod") || doc_lower.contains("@classmethod") {
            tags.push("class-method".to_string());
        }
        if name_lower.contains("staticmethod") || doc_lower.contains("@staticmethod") {
            tags.push("static-method".to_string());
        }

        tags.sort();
        tags.dedup();
        tags
    }

    fn estimate_importance(&self, name: &str, decorators: &[String]) -> f32 {
        let mut score: f32 = 0.5;

        if name == "main" || name == "run" || name == "start" {
            score += 0.3;
        }

        if decorators
            .iter()
            .any(|d| d.contains("route") || d.contains("api") || d.contains("endpoint"))
        {
            score += 0.2;
        }

        if name.to_lowercase().contains("auth") || name.to_lowercase().contains("login") {
            score += 0.2;
        }

        if name.starts_with('_') && !name.starts_with("__") {
            score -= 0.2;
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

    let clean_path = path.strip_prefix("./").unwrap_or(path);
    let path_str = clean_path.to_string_lossy().to_string();

    let parser = PythonParser::new(source_code, path_str.clone());
    let file_data = parser.parse()?;

    Ok((path_str, file_data))
}
