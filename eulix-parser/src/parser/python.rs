use crate::kb::types::*;
use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use tree_sitter::{Node, Parser, TreeCursor};

pub struct PythonParser {
    source_code: String,
    lines: Vec<String>,
}

impl PythonParser {
    pub fn new(source_code: String) -> Self {
        let lines = source_code.lines().map(|s| s.to_string()).collect();
        Self { source_code, lines }
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
        // Python stdlib modules (common ones)
        let stdlib = [
            "os", "sys", "re", "json", "datetime", "time", "collections",
            "itertools", "functools", "pathlib", "subprocess", "threading",
            "asyncio", "typing", "math", "random", "hashlib", "uuid",
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
        let re = Regex::new(r"from\s+(\S+)\s+import\s+(.+)").ok()?;

        if let Some(caps) = re.captures(&text) {
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
            if child.kind() == "function_definition" {
                if let Some(func) = self.parse_function(&child, "", None) {
                    functions.push(func);
                }
            }
        }

        functions
    }

    fn parse_function(&self, node: &Node, class_context: &str, file_path: Option<&str>) -> Option<Function> {
        let mut cursor = node.walk();
        let mut name = String::new();
        let mut is_async = false;

        // Check for async
        if let Some(prev) = node.prev_sibling() {
            if prev.kind() == "async" {
                is_async = true;
            }
        }

        // Extract decorators
        let mut decorators = Vec::new();
        let mut current = node.prev_sibling();
        while let Some(sibling) = current {
            if sibling.kind() == "decorator" {
                decorators.insert(0, self.get_node_text(&sibling));
            }
            current = sibling.prev_sibling();
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

        // Extract function calls with context
        let calls = self.extract_function_calls_detailed(node, file_path);

        // Extract variables and data flow
        let variables = self.extract_variables(node, &params);

        // Build control flow
        let control_flow = self.build_control_flow(node);

        // Extract exception info
        let exceptions = self.extract_exception_info(node);

        let complexity = self.calculate_complexity(node);

        let id = if class_context.is_empty() {
            format!("func_{}", name)
        } else {
            format!("method_{}_{}", class_context, name)
        };

        // Auto-tag functions
        let tags = self.auto_tag_function(&name, &docstring, &calls);

        // Calculate importance (placeholder, will be refined later)
        let importance_score = self.estimate_importance(&name, &decorators);

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

        let parts: Vec<&str> = text.split('=').collect();
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
                type_annotation: type_parts.get(1)
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

    fn build_signature(&self, name: &str, params: &[Parameter], return_type: &str, is_async: bool) -> String {
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
            format!("{}def {}({}) -> {}", async_prefix, name, param_str, return_type)
        }
    }

    // Extract function calls with detailed context
    fn extract_function_calls_detailed(&self, node: &Node, file_path: Option<&str>) -> Vec<FunctionCall> {
        let mut calls = Vec::new();
        let mut seen = HashSet::new();

        self.find_calls_recursive(node, &mut calls, &mut seen, "unconditional");
        calls
    }

    fn find_calls_recursive(&self, node: &Node, calls: &mut Vec<FunctionCall>, seen: &mut HashSet<String>, context: &str) {
        let mut cursor = node.walk();

        // Determine context for children
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

                            // Extract arguments
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
                if child.kind() == "identifier" || child.kind() == "string" || child.kind() == "integer" {
                    args.push(self.get_node_text(&child));
                }
            }
        }

        args
    }

    // Extract variables and track transformations
    fn extract_variables(&self, node: &Node, params: &[Parameter]) -> Vec<Variable> {
        let mut variables: HashMap<String, Variable> = HashMap::new();

        // Add parameters as variables
        for param in params {
            variables.insert(param.name.clone(), Variable {
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
            });
        }

        // Track assignments and usage
        self.track_variable_usage(node, &mut variables);

        variables.into_values().collect()
    }

    fn track_variable_usage(&self, node: &Node, variables: &mut HashMap<String, Variable>) {
        let mut cursor = node.walk();

        // Check for assignments
        if node.kind() == "assignment" {
            if let Some(left) = node.child_by_field_name("left") {
                if let Some(right) = node.child_by_field_name("right") {
                    let var_name = self.get_node_text(&left);
                    let line = node.start_position().row + 1;

                    // Check if it's a function call transformation
                    if right.kind() == "call" {
                        if let Some(func_node) = right.child_by_field_name("function") {
                            let func_name = self.get_node_text(&func_node);

                            // Track transformation
                            if let Some(var) = variables.get_mut(&var_name) {
                                var.transformations.push(VarTransformation {
                                    line,
                                    via: func_name.clone(),
                                    becomes: var_name.clone(),
                                });
                            } else {
                                // New local variable
                                variables.insert(var_name.clone(), Variable {
                                    name: var_name.clone(),
                                    var_type: None,
                                    scope: "local".to_string(),
                                    defined_at: Some(line),
                                    transformations: vec![],
                                    used_in: vec![],
                                    returned: false,
                                });
                            }
                        }
                    } else {
                        // Simple assignment
                        if !variables.contains_key(&var_name) {
                            variables.insert(var_name.clone(), Variable {
                                name: var_name.clone(),
                                var_type: None,
                                scope: "local".to_string(),
                                defined_at: Some(line),
                                transformations: vec![],
                                used_in: vec![],
                                returned: false,
                            });
                        }
                    }
                }
            }
        }

        // Check for return statements
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

    // Build control flow structure
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

        Some(ExecutionPath { calls, returns, raises })
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
        let loop_type = if node.kind() == "for_statement" { "for" } else { "while" };
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

    // Extract exception information
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
            if child.kind() == "class_definition" {
                if let Some(class) = self.parse_class(&child, None) {
                    classes.push(class);
                }
            }
        }

        classes
    }

    fn parse_class(&self, node: &Node, file_path: Option<&str>) -> Option<Class> {
        let mut cursor = node.walk();
        let mut name = String::new();
        let mut bases = Vec::new();
        let mut methods = Vec::new();
        let mut attributes = Vec::new();
        let mut decorators = Vec::new();

        // Extract decorators
        let mut current = node.prev_sibling();
        while let Some(sibling) = current {
            if sibling.kind() == "decorator" {
                decorators.insert(0, self.get_node_text(&sibling));
            }
            current = sibling.prev_sibling();
        }

        for child in node.children(&mut cursor) {
            match child.kind() {
                "identifier" => {
                    if name.is_empty() {
                        name = self.get_node_text(&child);
                    }
                }
                "argument_list" => {
                    bases = self.extract_base_classes(&child);
                }
                "block" => {
                    let (class_methods, class_attrs) = self.parse_class_body(&child, &name, file_path);
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
            id: format!("class_{}", name),
            name,
            bases,
            docstring,
            line_start,
            line_end,
            methods,
            attributes,
            decorators,
        })
    }

    fn extract_base_classes(&self, node: &Node) -> Vec<String> {
        let text = self.get_node_text(node);
        text.trim_matches(|c| c == '(' || c == ')')
            .split(',')
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect()
    }

    fn parse_class_body(&self, node: &Node, class_name: &str, file_path: Option<&str>) -> (Vec<Function>, Vec<Attribute>) {
        let mut methods = Vec::new();
        let mut attributes = Vec::new();
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "function_definition" => {
                    if let Some(method) = self.parse_function(&child, class_name, file_path) {
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
            let re = Regex::new(r"(\w+)\s*:\s*([^=]+)(?:=\s*(.+))?").ok()?;
            if let Some(caps) = re.captures(&text) {
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
                        if !name.is_empty() && name.chars().all(|c| c.is_alphanumeric() || c == '_') {
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
                "if_statement" | "elif_clause" | "while_statement" |
                "for_statement" | "except_clause" | "with_statement" |
                "and" | "or" => {
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
        let re = Regex::new(r"#\s*TODO:?\s*(.+)").unwrap();

        self.source_code
            .lines()
            .enumerate()
            .filter_map(|(idx, line)| {
                re.captures(line).map(|caps| {
                    let text = caps.get(1).unwrap().as_str().trim().to_string();
                    let priority = if text.to_lowercase().contains("critical") ||
                                      text.to_lowercase().contains("urgent") {
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
        let code_lower = self.source_code.to_lowercase();

        let patterns = vec![
            (r"password", "password_handling", "Handles passwords"),
            (r"secret|api_key|token", "sensitive_data", "Handles sensitive data"),
            (r"eval\(", "code_execution", "Uses eval() - potential security risk"),
            (r"exec\(", "code_execution", "Uses exec() - potential security risk"),
            (r"__import__", "dynamic_import", "Dynamic imports detected"),
            (r"pickle\.load", "deserialization", "Uses pickle - potential security risk"),
            (r"subprocess|os\.system|os\.popen", "command_execution", "System command execution"),
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

    // Auto-tag functions based on name and behavior
    fn auto_tag_function(&self, name: &str, docstring: &str, calls: &[FunctionCall]) -> Vec<String> {
        let mut tags = Vec::new();
        let name_lower = name.to_lowercase();
        let doc_lower = docstring.to_lowercase();

        // Entry point detection
        if name == "main" || name == "run" || name == "start" {
            tags.push("entry-point".to_string());
        }

        // Authentication/Security
        if name_lower.contains("auth") || name_lower.contains("login") ||
           name_lower.contains("password") || name_lower.contains("hash") {
            tags.push("authentication".to_string());
            tags.push("security".to_string());
        }

        // API/HTTP
        if name_lower.contains("api") || name_lower.contains("endpoint") ||
           name_lower.contains("route") || doc_lower.contains("http") {
            tags.push("api".to_string());
        }

        // Database
        if name_lower.contains("db") || name_lower.contains("database") ||
           name_lower.contains("query") || name_lower.contains("save") {
            tags.push("database".to_string());
        }

        // Async
        if calls.iter().any(|c| c.callee.contains("await") || c.callee.contains("async")) {
            tags.push("async".to_string());
        }

        // Validation
        if name_lower.contains("validate") || name_lower.contains("check") ||
           name_lower.contains("verify") {
            tags.push("validation".to_string());
        }

        // Utils
        if name_lower.contains("util") || name_lower.contains("helper") {
            tags.push("utility".to_string());
        }

        tags
    }

    // Estimate function importance
    fn estimate_importance(&self, name: &str, decorators: &[String]) -> f32 {
        let mut score: f32 = 0.5; // Base score

        // Entry points are important
        if name == "main" || name == "run" || name == "start" {
            score += 0.3;
        }

        // Public API functions (decorated)
        if decorators.iter().any(|d| d.contains("route") || d.contains("api") || d.contains("endpoint")) {
            score += 0.2;
        }

        // Auth functions are important
        if name.to_lowercase().contains("auth") || name.to_lowercase().contains("login") {
            score += 0.2;
        }

        // Private functions less important
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

    let parser = PythonParser::new(source_code);
    let file_data = parser.parse()?;

    let relative_path = path.to_string_lossy().to_string();

    Ok((relative_path, file_data))
}
