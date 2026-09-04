//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::struc::kb_struct::*;
// use once_cell::sync::Lazy;
use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::LazyLock;
use tree_sitter::{Node, Parser};

struct SecurityPattern {
    regex: &'static LazyLock<Regex>,
    note_type: &'static str,
    description: &'static str,
}

//  Regex Patterns compiled once at first use
static TODO_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?://|/\*)\s*TODO:?\s*(.+?)(?:\*/|$)").expect("Invalid TODO regex")
});

static EVAL_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"eval\s*\(").expect("Invalid eval regex"));

static INNERHTML_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"innerHTML\s*=|outerHTML\s*=").expect("Invalid innerHTML regex"));

static DOCUMENT_WRITE_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"document\.write\s*\(").expect("Invalid document.write regex"));

static DANGEROUSLY_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"dangerouslySetInnerHTML").expect("Invalid dangerouslySetInnerHTML regex")
});

static BROWSER_STORAGE_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"localStorage\.|sessionStorage\.").expect("Invalid browser_storage regex")
});

static WEAK_RANDOM_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"Math\.random\(\)").expect("Invalid weak_random regex"));

static DYNAMIC_REQUIRE_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"require\s*\(\s*req\.").expect("Invalid dynamic_require regex"));

static NEW_FUNCTION_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"new\s+Function\s*\(").expect("Invalid new Function regex"));

static SECURITY_PATTERNS: LazyLock<Vec<SecurityPattern>> = LazyLock::new(|| {
    vec![
        SecurityPattern {
            regex: &EVAL_RE,
            note_type: "code_injection",
            description: "eval() executes arbitrary code — XSS risk",
        },
        SecurityPattern {
            regex: &INNERHTML_RE,
            note_type: "xss",
            description: "innerHTML assignment — potential XSS",
        },
        SecurityPattern {
            regex: &DOCUMENT_WRITE_RE,
            note_type: "xss",
            description: "document.write() — potential XSS",
        },
        SecurityPattern {
            regex: &DANGEROUSLY_RE,
            note_type: "xss",
            description: "React dangerouslySetInnerHTML — ensure sanitization",
        },
        SecurityPattern {
            regex: &BROWSER_STORAGE_RE,
            note_type: "sensitive_storage",
            description: "Browser storage — avoid storing secrets",
        },
        SecurityPattern {
            regex: &WEAK_RANDOM_RE,
            note_type: "weak_random",
            description: "Math.random() is not cryptographically secure",
        },
        SecurityPattern {
            regex: &DYNAMIC_REQUIRE_RE,
            note_type: "dynamic_require",
            description: "Dynamic require() from user input is dangerous",
        },
        SecurityPattern {
            regex: &NEW_FUNCTION_RE,
            note_type: "code_injection",
            description: "new Function() is similar to eval()",
        },
    ]
});

pub struct TypeScriptParser {
    source_code: String,
    file_path: String,
}

impl TypeScriptParser {
    pub fn new(source_code: String, file_path: String) -> Self {
        Self {
            source_code,
            file_path,
        }
    }

    pub fn parse(&self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_typescript::language_typescript())
            .map_err(|e| format!("Failed to load TypeScript grammar: {}", e))?;

        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse TypeScript file".to_string())?;

        let root = tree.root_node();

        Ok(FileData {
            language: "typescript".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(&root),
            functions: self.extract_functions(&root),
            classes: self.extract_classes(&root),
            global_vars: self.extract_global_vars(&root),
            todos: self.extract_todos(),
            security_notes: self.detect_security_patterns(),
        })
    }
    /// Builds a file-qualified ID for a top-level function or method.
    /// e.g. func_rewriteHeader::internal/query/context_utils.go
    ///      method_ContextBuilder_expandFromKBFunction::internal/query/mmr.go
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

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            let target = if child.kind() == "export_statement" {
                child
                    .children(&mut child.walk())
                    .find(|c| c.kind() == "import_statement")
                    .unwrap_or(child)
            } else {
                child
            };

            if target.kind() == "import_statement" {
                self.parse_import_statement(&target, &mut imports);
            }
        }

        imports
    }

    fn parse_import_statement(&self, node: &Node, imports: &mut Vec<Import>) {
        let module = node
            .children(&mut node.walk())
            .find(|c| c.kind() == "string")
            .map(|s| {
                self.get_node_text(&s)
                    .trim_matches(|c| c == '\'' || c == '"')
                    .to_string()
            })
            .unwrap_or_default();

        if module.is_empty() {
            return;
        }

        let mut items: Vec<String> = Vec::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "import_clause" => {
                    let clause_text = self.get_node_text(&child);
                    items.push(clause_text);
                }
                "namespace_import" => {
                    items.push(self.get_node_text(&child));
                }
                "named_imports" => {
                    let mut nc = child.walk();
                    for item in child.children(&mut nc) {
                        if item.kind() == "import_specifier" {
                            items.push(self.get_node_text(&item));
                        }
                    }
                }
                _ => {}
            }
        }

        let import_type = self.classify_import(&module);
        imports.push(Import {
            module,
            items,
            import_type,
        });
    }

    fn classify_import(&self, module: &str) -> String {
        if module.starts_with('.') {
            "internal".to_string()
        } else {
            "external".to_string()
        }
    }

    fn extract_functions(&self, root: &Node) -> Vec<Function> {
        let mut functions = Vec::new();
        self.collect_top_level_functions(root, &mut functions);
        functions
    }

    fn collect_top_level_functions(&self, node: &Node, functions: &mut Vec<Function>) {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "function_declaration" | "function" | "generator_function_declaration" => {
                    if let Some(func) = self.parse_function(&child, "") {
                        functions.push(func);
                    }
                }
                "export_statement" | "ambient_declaration" => {
                    let mut ec = child.walk();
                    for inner in child.children(&mut ec) {
                        if matches!(
                            inner.kind(),
                            "function_declaration" | "function" | "generator_function_declaration"
                        ) {
                            if let Some(func) = self.parse_function(&inner, "") {
                                functions.push(func);
                            }
                        }
                    }
                }
                "lexical_declaration" | "variable_declaration" => {
                    let mut vc = child.walk();
                    for decl in child.children(&mut vc) {
                        if decl.kind() == "variable_declarator" {
                            if let Some(func) = self.parse_variable_function(&decl) {
                                functions.push(func);
                            }
                        }
                    }
                }
                _ => {}
            }
        }
    }

    fn parse_variable_function(&self, node: &Node) -> Option<Function> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let value = node.child_by_field_name("value")?;

        if !matches!(
            value.kind(),
            "arrow_function" | "function" | "generator_function"
        ) {
            return None;
        }

        self.parse_function_inner(&value, &name, "")
    }

    fn parse_function(&self, node: &Node, struct_context: &str) -> Option<Function> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))
            .unwrap_or_else(|| "anonymous".to_string());

        self.parse_function_inner(node, &name, struct_context)
    }

    fn parse_function_inner(
        &self,
        node: &Node,
        name: &str,
        struct_context: &str,
    ) -> Option<Function> {
        let is_async = node.children(&mut node.walk()).any(|c| c.kind() == "async");

        let params = self.extract_parameters(node);
        let return_type = self.extract_return_type(node);
        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);
        let signature = self.build_signature(name, &params, &return_type, is_async);
        let id = self.make_function_id(name, struct_context);

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

        let tags = self.auto_tag_function(name, &docstring, &calls);
        let importance_score = self.estimate_importance(name, !struct_context.is_empty());

        Some(Function {
            id,
            name: name.to_string(),
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
            is_async,
            decorators: self.extract_decorators(node),
            tags,
            importance_score,
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    fn extract_parameters(&self, node: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();

        if let Some(param_list) = node.child_by_field_name("parameters") {
            let mut cursor = param_list.walk();
            for child in param_list.children(&mut cursor) {
                match child.kind() {
                    "required_parameter" | "optional_parameter" => {
                        let name = child
                            .child_by_field_name("pattern")
                            .map(|n| self.get_node_text(&n))
                            .unwrap_or_else(|| self.get_node_text(&child));

                        let type_annotation = child
                            .child_by_field_name("type")
                            .map(|t| self.get_node_text(&t))
                            .unwrap_or_default();

                        let default_value = child
                            .child_by_field_name("value")
                            .map(|v| self.get_node_text(&v));

                        params.push(Parameter {
                            name,
                            type_annotation,
                            default_value,
                        });
                    }
                    "rest_pattern" => {
                        params.push(Parameter {
                            name: self.get_node_text(&child),
                            type_annotation: "rest".to_string(),
                            default_value: None,
                        });
                    }
                    _ => {}
                }
            }
        }

        params
    }

    fn extract_return_type(&self, node: &Node) -> String {
        node.child_by_field_name("return_type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_default()
    }

    fn build_signature(
        &self,
        name: &str,
        params: &[Parameter],
        return_type: &str,
        is_async: bool,
    ) -> String {
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

        let async_kw = if is_async { "async " } else { "" };

        if return_type.is_empty() {
            format!("{}function {}({})", async_kw, name, param_str)
        } else {
            format!(
                "{}function {}({}): {}",
                async_kw, name, param_str, return_type
            )
        }
    }

    fn extract_decorators(&self, node: &Node) -> Vec<String> {
        let mut decorators = Vec::new();
        if let Some(prev) = node.prev_sibling() {
            if prev.kind() == "decorator" {
                decorators.push(self.get_node_text(&prev));
            }
        }
        decorators
    }

    fn extract_classes(&self, root: &Node) -> Vec<Class> {
        let mut classes = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            match child.kind() {
                "class_declaration" => {
                    if let Some(class) = self.parse_class(&child) {
                        classes.push(class);
                    }
                }
                "interface_declaration" => {
                    if let Some(class) = self.parse_interface(&child) {
                        classes.push(class);
                    }
                }
                "export_statement" | "ambient_declaration" => {
                    let mut ec = child.walk();
                    for inner in child.children(&mut ec) {
                        match inner.kind() {
                            "class_declaration" => {
                                if let Some(class) = self.parse_class(&inner) {
                                    classes.push(class);
                                }
                            }
                            "interface_declaration" => {
                                if let Some(class) = self.parse_interface(&inner) {
                                    classes.push(class);
                                }
                            }
                            _ => {}
                        }
                    }
                }
                _ => {}
            }
        }

        classes
    }

    fn parse_class(&self, node: &Node) -> Option<Class> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        let bases: Vec<String> = node
            .children(&mut node.walk())
            .filter(|c| c.kind() == "class_heritage")
            .flat_map(|h| {
                let mut bases = Vec::new();
                let mut hc = h.walk();
                for item in h.children(&mut hc) {
                    if item.kind() == "extends_clause" || item.kind() == "implements_clause" {
                        let mut ic = item.walk();
                        for t in item.children(&mut ic) {
                            if t.kind() == "identifier" || t.kind() == "type_identifier" {
                                bases.push(self.get_node_text(&t));
                            }
                        }
                    }
                }
                bases
            })
            .collect();

        let (attributes, methods) = node
            .child_by_field_name("body")
            .map(|body| self.extract_class_body(&body, &name))
            .unwrap_or((vec![], vec![]));

        Some(Class {
            id: self.make_class_id(&name),
            name,
            bases,
            docstring,
            line_start,
            line_end,
            methods,
            attributes,
            decorators: self.extract_decorators(node),
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    fn parse_interface(&self, node: &Node) -> Option<Class> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        let bases: Vec<String> = node
            .children(&mut node.walk())
            .filter(|c| c.kind() == "extends_type_clause")
            .flat_map(|e| {
                let mut bc = e.walk();
                e.children(&mut bc)
                    .filter(|c| c.kind() == "type_identifier" || c.kind() == "identifier")
                    .map(|c| self.get_node_text(&c))
                    .collect::<Vec<_>>()
            })
            .collect();

        let attributes: Vec<Attribute> = node
            .child_by_field_name("body")
            .map(|body| {
                let mut attrs = Vec::new();
                let mut cursor = body.walk();
                for child in body.children(&mut cursor) {
                    if child.kind() == "property_signature" {
                        let attr_name = child
                            .child_by_field_name("name")
                            .map(|n| self.get_node_text(&n))
                            .unwrap_or_default();
                        let type_annotation = child
                            .child_by_field_name("type")
                            .map(|t| self.get_node_text(&t))
                            .unwrap_or_default();
                        if !attr_name.is_empty() {
                            attrs.push(Attribute {
                                name: attr_name,
                                type_annotation,
                                value: None,
                            });
                        }
                    }
                }
                attrs
            })
            .unwrap_or_default();

        Some(Class {
            id: format!("interface_{}", name),
            name,
            bases,
            docstring,
            line_start,
            line_end,
            methods: vec![],
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
                "public_field_definition" | "field_definition" => {
                    let attr_name = child
                        .child_by_field_name("name")
                        .map(|n| self.get_node_text(&n))
                        .unwrap_or_default();
                    let type_annotation = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_default();
                    let value = child
                        .child_by_field_name("value")
                        .map(|v| self.get_node_text(&v));
                    if !attr_name.is_empty() {
                        attributes.push(Attribute {
                            name: attr_name,
                            type_annotation,
                            value,
                        });
                    }
                }
                "method_definition" => {
                    if let Some(method) = self.parse_method(&child, class_name) {
                        methods.push(method);
                    }
                }
                _ => {}
            }
        }

        (attributes, methods)
    }

    fn parse_method(&self, node: &Node, class_name: &str) -> Option<Function> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        self.parse_function_inner(node, &name, class_name)
    }

    fn extract_global_vars(&self, root: &Node) -> Vec<GlobalVar> {
        let mut vars = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            match child.kind() {
                "lexical_declaration" | "variable_declaration" => {
                    let mut vc = child.walk();
                    for decl in child.children(&mut vc) {
                        if decl.kind() == "variable_declarator" {
                            let name = decl
                                .child_by_field_name("name")
                                .map(|n| self.get_node_text(&n))
                                .unwrap_or_default();

                            if name.is_empty() {
                                continue;
                            }

                            let value = decl.child_by_field_name("value");
                            if let Some(v) = &value {
                                if matches!(
                                    v.kind(),
                                    "arrow_function" | "function" | "generator_function"
                                ) {
                                    continue;
                                }
                            }

                            let type_annotation = decl
                                .child_by_field_name("type")
                                .map(|t| self.get_node_text(&t))
                                .unwrap_or_default();

                            vars.push(GlobalVar {
                                name,
                                type_annotation,
                                value: value.map(|v| self.get_node_text(&v)),
                                line: decl.start_position().row + 1,
                            });
                        }
                    }
                }
                _ => {}
            }
        }

        vars
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
            "for_statement" | "for_in_statement" | "for_of_statement" | "while_statement"
            | "do_statement" => "loop",
            "switch_statement" => "switch",
            "try_statement" => "try",
            _ => context,
        };

        if node.kind() == "call_expression" {
            if let Some(func_node) = node.child_by_field_name("function") {
                let call_text = self.get_node_text(&func_node);
                let name = call_text
                    .trim_start_matches("await ")
                    .split('.')
                    .next_back()
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
            "lexical_declaration" | "variable_declaration" => {
                let mut vc = node.walk();
                for decl in node.children(&mut vc) {
                    if decl.kind() == "variable_declarator" {
                        let name = decl
                            .child_by_field_name("name")
                            .map(|n| self.get_node_text(&n))
                            .unwrap_or_default();

                        if !name.is_empty() {
                            let var_type = decl
                                .child_by_field_name("type")
                                .map(|t| self.get_node_text(&t));

                            variables.entry(name.clone()).or_insert(Variable {
                                name,
                                var_type,
                                scope: "local".to_string(),
                                defined_at: Some(decl.start_position().row + 1),
                                transformations: vec![],
                                used_in: vec![],
                                returned: false,
                            });
                        }
                    }
                }
            }
            "return_statement" => {
                let mut rc = node.walk();
                for child in node.children(&mut rc) {
                    if child.kind() == "identifier" {
                        let vn = self.get_node_text(&child);
                        if let Some(var) = variables.get_mut(&vn) {
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
            "for_statement" | "for_in_statement" | "for_of_statement" | "while_statement"
            | "do_statement" => {
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
            "for_in_statement" => "for-in",
            "for_of_statement" => "for-of",
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
                "statement_block" if try_calls.is_empty() => {
                    try_calls = self.extract_calls_from_block(&child);
                }
                "catch_clause" => {
                    let exc_type = child
                        .child_by_field_name("parameter")
                        .map(|p| self.get_node_text(&p))
                        .unwrap_or_else(|| "Error".to_string());
                    let calls = child
                        .child_by_field_name("body")
                        .map(|b| self.extract_calls_from_block(&b))
                        .unwrap_or_default();
                    except_clauses.push(ExceptClause {
                        exception_type: exc_type,
                        line: child.start_position().row + 1,
                        calls,
                    });
                }
                "finally_clause" => {
                    let mut fc = child.walk();
                    for fin_child in child.children(&mut fc) {
                        if fin_child.kind() == "statement_block" {
                            finally_calls = self.extract_calls_from_block(&fin_child);
                        }
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
                "if_statement" | "for_statement" | "for_in_statement" | "for_of_statement"
                | "while_statement" | "do_statement" | "switch_statement" | "switch_case"
                | "ternary_expression" | "try_statement" | "catch_clause" => c += 1,
                _ => {}
            }
            for child in node.children(&mut cursor) {
                c += count(&child);
            }
            c
        }
        1 + count(node)
    }

    fn extract_docstring(&self, node: &Node) -> String {
        let prev = node.prev_sibling();

        while let Some(sib) = prev {
            match sib.kind() {
                "comment" => {
                    let text = self.get_node_text(&sib);
                    if text.starts_with("/**") {
                        return text
                            .trim_start_matches("/**")
                            .trim_end_matches("*/")
                            .lines()
                            .map(|l| l.trim_start_matches('*').trim().to_string())
                            .filter(|l| !l.is_empty())
                            .collect::<Vec<_>>()
                            .join(" ");
                    }
                    return text
                        .trim_start_matches("//")
                        .trim_start_matches("/*")
                        .trim_end_matches("*/")
                        .trim()
                        .to_string();
                }
                _ => break,
            }
        }

        String::new()
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

    fn auto_tag_function(&self, name: &str, doc: &str, calls: &[FunctionCall]) -> Vec<String> {
        let mut tags = Vec::new();
        let lower = name.to_lowercase();
        let doc_lower = doc.to_lowercase();

        if lower == "constructor" || lower.starts_with("create") || lower.starts_with("make") {
            tags.push("constructor".to_string());
        }
        if lower.starts_with("test") || lower.starts_with("spec") || lower.starts_with("should") {
            tags.push("test".to_string());
        }
        if lower.contains("parse") || lower.contains("deserializ") {
            tags.push("parsing".to_string());
        }
        if lower.contains("render") || lower.contains("component") {
            tags.push("ui".to_string());
        }
        if lower.contains("fetch") || lower.contains("request") || lower.contains("api") {
            tags.push("network".to_string());
        }
        if doc_lower.contains("deprecated") {
            tags.push("deprecated".to_string());
        }
        if calls.iter().any(|c| c.callee == "console") {
            tags.push("logging".to_string());
        }

        tags
    }

    fn estimate_importance(&self, name: &str, is_method: bool) -> f32 {
        let lower = name.to_lowercase();
        let mut score: f32 = 0.5;

        if lower == "main" || lower == "constructor" || lower == "init" {
            score += 0.4;
        }
        if lower.starts_with("test") || lower.starts_with("spec") {
            score -= 0.1;
        }
        if is_method {
            score += 0.1;
        }

        score.clamp(0.0, 1.0)
    }

    fn get_node_text(&self, node: &Node) -> String {
        let bytes = self.source_code.as_bytes();
        let start = node.start_byte();
        let end = node.end_byte();
        String::from_utf8_lossy(&bytes[start..end]).to_string()
    }
}

pub fn parse_file(path: &Path) -> Result<(String, FileData), String> {
    let source_code = std::fs::read_to_string(path)
        .map_err(|e| format!("Failed to read file {}: {}", path.display(), e))?;

    let clean_path = path.strip_prefix("./").unwrap_or(path);
    let path_str = clean_path.to_string_lossy().to_string();

    let parser = TypeScriptParser::new(source_code, path_str.clone());
    let file_data = parser.parse()?;

    Ok((path_str, file_data))
}
