use crate::struc::kb_struct::*;
use regex::bytes::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::str;
use std::sync::LazyLock;
use tree_sitter::{Node, Parser};

struct SecurityPattern {
    regex: &'static LazyLock<Regex>,
    note_type: &'static str,
    description: &'static str,
}

struct TagRule {
    keywords: &'static [&'static str],
    tag: &'static str,
    check_docstring: bool,
}

// Security patterns (line-based, mirrors the C detector's approach)
static EVAL_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"\beval\s*\(|new\s+Function\s*\(")
        .expect("static eval/Function regex pattern is valid")
});
static INNERHTML_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"\.innerHTML\s*=|\.outerHTML\s*=|document\.write\s*\(")
        .expect("static innerHTML regex pattern is valid")
});
static DANGEROUS_HTML_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"dangerouslySetInnerHTML")
        .expect("static dangerouslySetInnerHTML regex pattern is valid")
});
static CHILD_PROCESS_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"\bexec\s*\(|\bexecSync\s*\(|\bspawn\s*\(|\bspawnSync\s*\(")
        .expect("static child_process regex pattern is valid")
});
static WEAK_RANDOM_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"Math\.random\s*\(\)").expect("static weak random regex pattern is valid")
});
static TLS_DISABLE_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"rejectUnauthorized\s*:\s*false|NODE_TLS_REJECT_UNAUTHORIZED")
        .expect("static TLS disable regex pattern is valid")
});
static PROTO_POLLUTION_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"__proto__|Object\.setPrototypeOf")
        .expect("static prototype pollution regex pattern is valid")
});
static CORS_WILDCARD_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r#"Access-Control-Allow-Origin['"]?\s*[,:]\s*['"]\*['"]"#)
        .expect("static CORS wildcard regex pattern is valid")
});
static SQL_TEMPLATE_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"(?i)(select|insert|update|delete)\s+.*\$\{")
        .expect("static SQL template-interpolation regex pattern is valid")
});
static CLIENT_STORAGE_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"localStorage\.|sessionStorage\.")
        .expect("static client storage regex pattern is valid")
});
static TODO_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"(?://|/\*)\s*(?i:TODO|FIXME|HACK|XXX)[:\s]*(.*?)(?:\*/|$)")
        .expect("static TODO comment regex pattern is valid")
});

static SECURITY_PATTERNS: LazyLock<Vec<SecurityPattern>> = LazyLock::new(|| {
    vec![
        SecurityPattern {
            regex: &EVAL_RE,
            note_type: "code_injection",
            description: "Uses eval() or the Function constructor",
        },
        SecurityPattern {
            regex: &INNERHTML_RE,
            note_type: "dom_xss",
            description: "Direct DOM write (innerHTML/outerHTML/document.write)",
        },
        SecurityPattern {
            regex: &DANGEROUS_HTML_RE,
            note_type: "dom_xss",
            description: "React dangerouslySetInnerHTML usage",
        },
        SecurityPattern {
            regex: &CHILD_PROCESS_RE,
            note_type: "command_execution",
            description: "Spawns a child process",
        },
        SecurityPattern {
            regex: &WEAK_RANDOM_RE,
            note_type: "weak_random",
            description: "Math.random() is not cryptographically secure",
        },
        SecurityPattern {
            regex: &TLS_DISABLE_RE,
            note_type: "tls_disabled",
            description: "TLS/certificate verification disabled",
        },
        SecurityPattern {
            regex: &PROTO_POLLUTION_RE,
            note_type: "prototype_pollution",
            description: "Direct prototype manipulation",
        },
        SecurityPattern {
            regex: &CORS_WILDCARD_RE,
            note_type: "cors_wildcard",
            description: "Wildcard CORS origin",
        },
        SecurityPattern {
            regex: &SQL_TEMPLATE_RE,
            note_type: "sql_injection",
            description: "SQL keyword combined with template interpolation",
        },
        SecurityPattern {
            regex: &CLIENT_STORAGE_RE,
            note_type: "client_storage",
            description: "Reads/writes browser storage (verify no sensitive data)",
        },
    ]
});

// Tag rules
static TAG_RULES: LazyLock<Vec<TagRule>> = LazyLock::new(|| {
    vec![
        TagRule {
            keywords: &["init", "setup", "initialize", "bootstrap", "mount"],
            tag: "initialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &["cleanup", "destroy", "dispose", "unmount", "teardown"],
            tag: "cleanup",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "auth", "login", "logout", "password", "hash", "encrypt", "decrypt", "token", "jwt",
            ],
            tag: "authentication",
            check_docstring: true,
        },
        TagRule {
            keywords: &["api", "endpoint", "route", "handler", "controller"],
            tag: "api",
            check_docstring: true,
        },
        TagRule {
            keywords: &[
                "db",
                "database",
                "query",
                "select",
                "insert",
                "update",
                "delete",
                "prisma",
                "mongoose",
                "sequelize",
            ],
            tag: "database",
            check_docstring: false,
        },
        TagRule {
            keywords: &["validate", "check", "verify", "sanitize", "schema"],
            tag: "validation",
            check_docstring: false,
        },
        TagRule {
            keywords: &["error", "catch", "exception"],
            tag: "error-handling",
            check_docstring: false,
        },
        TagRule {
            keywords: &["util", "helper", "format"],
            tag: "utility",
            check_docstring: false,
        },
        TagRule {
            keywords: &["read", "write", "file", "fs.", "readfile", "writefile"],
            tag: "file-io",
            check_docstring: false,
        },
        TagRule {
            keywords: &["fetch", "axios", "socket", "http", "request", "websocket"],
            tag: "network",
            check_docstring: false,
        },
        TagRule {
            keywords: &["config", "settings", "env"],
            tag: "configuration",
            check_docstring: false,
        },
        TagRule {
            keywords: &["log", "console", "debug"],
            tag: "logging",
            check_docstring: false,
        },
        TagRule {
            keywords: &["parse", "decode"],
            tag: "parsing",
            check_docstring: false,
        },
        TagRule {
            keywords: &["serialize", "encode", "stringify"],
            tag: "serialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "on",
                "listener",
                "emit",
                "addeventlistener",
                "handleclick",
                "handlechange",
            ],
            tag: "event-handling",
            check_docstring: false,
        },
        TagRule {
            keywords: &["reducer", "store", "dispatch", "context", "provider"],
            tag: "state-management",
            check_docstring: false,
        },
        TagRule {
            keywords: &["middleware"],
            tag: "middleware",
            check_docstring: false,
        },
        TagRule {
            keywords: &["test", "spec", "mock", "jest", "describe", "expect"],
            tag: "testing",
            check_docstring: false,
        },
    ]
});

pub struct JsParser {
    source_code: String,
    file_path: String,
}

impl JsParser {
    pub fn new(source_code: String, file_path: String) -> Self {
        Self {
            source_code,
            file_path,
        }
    }

    fn make_function_id(&self, name: &str, class_context: &str) -> String {
        if class_context.is_empty() {
            format!("func_{}::{}", name, self.file_path)
        } else {
            format!("method_{}_{}::{}", class_context, name, self.file_path)
        }
    }

    fn make_class_id(&self, name: &str) -> String {
        format!("class_{}::{}", name, self.file_path)
    }

    pub fn parse(&mut self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_javascript::language())
            .map_err(|e| format!("Failed to load JavaScript grammar: {}", e))?;
        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse JS/JSX file".to_string())?;
        let root = tree.root_node();
        Ok(FileData {
            language: "javascript".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(&root),
            functions: self.extract_top_level_functions(&root),
            classes: self.extract_classes(&root),
            global_vars: self.extract_global_vars(&root),
            todos: self.extract_todos(),
            security_notes: self.detect_security_patterns(),
        })
    }

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    // Imports (ES modules + CommonJS require)
    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        self.walk_top_level_imports(root, &mut imports);
        imports
    }

    fn walk_top_level_imports(&self, node: &Node, imports: &mut Vec<Import>) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "import_statement" => {
                    if let Some(source) = self.import_source_text(&child) {
                        let items = self.extract_import_items(&child);
                        imports.push(Import {
                            import_type: self.classify_import(&source),
                            module: source,
                            items,
                        });
                    }
                }
                "export_statement" => {
                    // export ... from '...'
                    if let Some(source) = self.import_source_text(&child) {
                        imports.push(Import {
                            import_type: self.classify_import(&source),
                            module: source,
                            items: vec![],
                        });
                    }
                    self.walk_top_level_imports(&child, imports);
                }
                "lexical_declaration" | "variable_declaration" => {
                    self.extract_require_calls(&child, imports);
                }
                "expression_statement" => {
                    self.extract_require_calls(&child, imports);
                }
                _ => {}
            }
        }
    }

    fn import_source_text(&self, node: &Node) -> Option<String> {
        if let Some(src) = node.child_by_field_name("source") {
            return Some(self.strip_quotes(&self.get_node_text(&src)));
        }
        // scan children for a plain string node (covers `import "x"` and export-from)
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "string" {
                return Some(self.strip_quotes(&self.get_node_text(&child)));
            }
        }
        None
    }

    fn extract_import_items(&self, node: &Node) -> Vec<String> {
        let mut items = Vec::new();
        if let Some(clause) = node.child_by_field_name("import_clause") {
            self.collect_import_names(&clause, &mut items);
        } else {
            self.collect_import_names(node, &mut items);
        }
        items
    }

    fn collect_import_names(&self, node: &Node, items: &mut Vec<String>) {
        let mut cursor = node.walk();
        match node.kind() {
            "identifier" => items.push(self.get_node_text(node)),
            "import_specifier" => {
                let name = node
                    .child_by_field_name("name")
                    .map(|n| self.get_node_text(&n))
                    .unwrap_or_default();
                if !name.is_empty() {
                    items.push(name);
                }
            }
            "namespace_import" => items.push(format!("* as {}", self.get_node_text(node))),
            _ => {
                for child in node.children(&mut cursor) {
                    self.collect_import_names(&child, items);
                }
            }
        }
    }

    /// Finds `const x = require('y')` / `require('y')` call expressions.
    fn extract_require_calls(&self, node: &Node, imports: &mut Vec<Import>) {
        let mut cursor = node.walk();
        if node.kind() == "call_expression" {
            if let Some(func) = node.child_by_field_name("function") {
                if self.get_node_text(&func) == "require" {
                    if let Some(args) = node.child_by_field_name("arguments") {
                        let mut acur = args.walk();
                        for arg in args.children(&mut acur) {
                            if arg.kind() == "string" {
                                let source = self.strip_quotes(&self.get_node_text(&arg));
                                imports.push(Import {
                                    import_type: self.classify_import(&source),
                                    module: source,
                                    items: vec![],
                                });
                            }
                        }
                    }
                }
            }
        }
        for child in node.children(&mut cursor) {
            self.extract_require_calls(&child, imports);
        }
    }

    fn strip_quotes(&self, text: &str) -> String {
        text.trim_matches(|c| c == '\'' || c == '"' || c == '`')
            .to_string()
    }

    fn classify_import(&self, path: &str) -> String {
        const NODE_BUILTINS: &[&str] = &[
            "fs",
            "path",
            "http",
            "https",
            "os",
            "crypto",
            "child_process",
            "util",
            "stream",
            "events",
            "url",
            "querystring",
            "net",
            "buffer",
            "assert",
            "zlib",
            "cluster",
            "dns",
        ];
        let bare = path.strip_prefix("node:").unwrap_or(path);
        if NODE_BUILTINS.contains(&bare) {
            "stdlib".to_string()
        } else if path.starts_with('.') || path.starts_with('/') {
            "internal".to_string()
        } else {
            "external".to_string()
        }
    }

    // Functions (declarations, exported, arrow/function expr bound to a var)
    fn extract_top_level_functions(&self, root: &Node) -> Vec<Function> {
        let mut functions = Vec::new();
        let mut cursor = root.walk();
        for child in root.children(&mut cursor) {
            self.collect_function_like(&child, &mut functions, true);
        }
        functions
    }

    /// Looks at a top-level (or export-wrapped) statement and pulls out any
    /// function/arrow-function declarations found directly in it.
    fn collect_function_like(&self, node: &Node, functions: &mut Vec<Function>, top_level: bool) {
        match node.kind() {
            "function_declaration" | "generator_function_declaration" => {
                if let Some(f) = self.parse_function_decl(node, "") {
                    functions.push(f);
                }
            }
            "export_statement" if top_level => {
                if let Some(decl) = node.child_by_field_name("declaration") {
                    self.collect_function_like(&decl, functions, true);
                } else {
                    let mut cursor = node.walk();
                    for child in node.children(&mut cursor) {
                        self.collect_function_like(&child, functions, false);
                    }
                }
            }
            "lexical_declaration" | "variable_declaration" if top_level => {
                let mut cursor = node.walk();
                for child in node.children(&mut cursor) {
                    if child.kind() == "variable_declarator" {
                        if let Some(f) = self.parse_var_bound_function(&child) {
                            functions.push(f);
                        }
                    }
                }
            }
            _ => {}
        }
    }

    fn parse_var_bound_function(&self, declarator: &Node) -> Option<Function> {
        let name_node = declarator.child_by_field_name("name")?;
        let value = declarator.child_by_field_name("value")?;
        if value.kind() != "arrow_function" && value.kind() != "function_expression" {
            return None;
        }
        let name = self.get_node_text(&name_node);
        self.parse_function_body(&value, &name, "", declarator)
    }

    fn parse_function_decl(&self, node: &Node, class_context: &str) -> Option<Function> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);
        self.parse_function_body(node, &name, class_context, node)
    }

    /// Shared body for function_declaration / function_expression / arrow_function.
    fn parse_function_body(
        &self,
        fn_node: &Node,
        name: &str,
        class_context: &str,
        span_node: &Node,
    ) -> Option<Function> {
        let params = self.extract_parameters(fn_node);
        let is_async = self.has_keyword_child(fn_node, "async");
        let is_arrow_fn = fn_node.kind() == "arrow_function";
        let line_start = span_node.start_position().row + 1;
        let line_end = span_node.end_position().row + 1;
        let docstring = self.extract_docstring(span_node);
        let return_type = String::new(); // JS is untyped
        let signature = self.build_signature(name, &params, is_async, is_arrow_fn);
        let body = fn_node.child_by_field_name("body")?;
        let body_text = self.get_node_text(&body);
        let calls = self.extract_function_calls_detailed(&body);
        let variables = self.extract_variables(&body, &params);
        let control_flow = self.build_control_flow(&body);
        let exceptions = self.build_exception_info(&body);
        let complexity = self.calculate_complexity(&body);
        let id = self.make_function_id(name, class_context);
        let has_jsx = self.contains_jsx(&body);
        let tags = self.auto_tag_function(name, &docstring, &calls, &body_text, has_jsx);
        let importance_score = self.estimate_importance(name, &tags);
        let is_generator = fn_node.kind() == "generator_function_declaration"
            || self.has_keyword_child(fn_node, "*");
        let is_iife = self.is_iife(span_node);
        let is_commonjs_export = self.is_commonjs_export(name);

        let lang_info = LanguageSpecificInfo {
            javascript: Some(JavaScriptInfo {
                is_async,
                is_exported: self.is_exported(span_node),
                is_default_export: self.is_default_export(span_node),
                is_arrow_fn,
                is_generator,
                is_iife,
                is_strict_mode: self.uses_strict_mode(&body),
                module_system: self.detect_module_system(),
                is_commonjs_export,
                binds_this_lexically: is_arrow_fn,
                is_callback: false,
                is_higher_order: self.returns_function(&body),
                uses_hoisted_var: self.uses_var_keyword(&body),
            }),
            ..Default::default()
        };

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
            exceptions,
            complexity,
            is_async,
            decorators: vec![],
            tags,
            importance_score,
            lang_info,
        })
    }

    fn has_keyword_child(&self, node: &Node, keyword: &str) -> bool {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == keyword {
                return true;
            }
        }
        false
    }

    fn is_exported(&self, node: &Node) -> bool {
        node.parent()
            .map(|p| p.kind() == "export_statement")
            .unwrap_or(false)
            || node
                .parent()
                .and_then(|p| p.parent())
                .map(|gp| gp.kind() == "export_statement")
                .unwrap_or(false)
    }

    fn is_default_export(&self, node: &Node) -> bool {
        if let Some(parent) = node.parent() {
            if parent.kind() == "export_statement" {
                return self.has_keyword_child(&parent, "default");
            }
        }
        false
    }

    fn extract_parameters(&self, fn_node: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();
        let param_container = fn_node
            .child_by_field_name("parameters")
            .or_else(|| fn_node.child_by_field_name("parameter"));
        let Some(container) = param_container else {
            return params;
        };
        if container.kind() != "formal_parameters" {
            // bare single identifier param on an arrow function: `x => ...`
            params.push(Parameter {
                name: self.get_node_text(&container),
                type_annotation: String::new(),
                default_value: None,
            });
            return params;
        }
        let mut cursor = container.walk();
        for child in container.children(&mut cursor) {
            match child.kind() {
                "identifier" => params.push(Parameter {
                    name: self.get_node_text(&child),
                    type_annotation: String::new(),
                    default_value: None,
                }),
                "assignment_pattern" => {
                    let left = child
                        .child_by_field_name("left")
                        .map(|n| self.get_node_text(&n))
                        .unwrap_or_default();
                    let right = child
                        .child_by_field_name("right")
                        .map(|n| self.get_node_text(&n));
                    params.push(Parameter {
                        name: left,
                        type_annotation: String::new(),
                        default_value: right,
                    });
                }
                "rest_pattern" => {
                    let inner = self.get_node_text(&child);
                    params.push(Parameter {
                        name: inner,
                        type_annotation: "variadic".to_string(),
                        default_value: None,
                    });
                }
                "object_pattern" | "array_pattern" => {
                    params.push(Parameter {
                        name: self.get_node_text(&child),
                        type_annotation: "destructured".to_string(),
                        default_value: None,
                    });
                }
                _ => {}
            }
        }
        params
    }

    fn build_signature(
        &self,
        name: &str,
        params: &[Parameter],
        is_async: bool,
        is_arrow_fn: bool,
    ) -> String {
        let param_str = params
            .iter()
            .map(|p| match &p.default_value {
                Some(d) => format!("{} = {}", p.name, d),
                None => p.name.clone(),
            })
            .collect::<Vec<_>>()
            .join(", ");
        let prefix = if is_async { "async " } else { "" };
        if is_arrow_fn {
            format!("{}{} ({}) => {{}}", prefix, name, param_str)
        } else {
            format!("{}function {}({})", prefix, name, param_str)
        }
    }

    // Calls
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
        let child_context = match node.kind() {
            "if_statement" => "if",
            "for_statement" | "for_in_statement" | "while_statement" | "do_statement" => "loop",
            "switch_statement" => "switch",
            "try_statement" => "try",
            _ => context,
        };
        if node.kind() == "call_expression" || node.kind() == "new_expression" {
            if let Some(func_node) = node.child_by_field_name("function") {
                let callee = self.resolve_callee(&func_node);
                if !callee.is_empty() {
                    let key = format!("{}:{}", callee, node.start_position().row);
                    if !seen.contains(&key) {
                        seen.insert(key);
                        let args = self.extract_call_arguments(node);
                        calls.push(FunctionCall {
                            callee,
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
            self.find_calls_recursive(&child, calls, seen, child_context);
        }
    }

    /// Resolves a callee expression to a dotted name, e.g. `foo.bar` or `foo`.
    fn resolve_callee(&self, func_node: &Node) -> String {
        match func_node.kind() {
            "identifier" => self.get_node_text(func_node),
            "member_expression" => {
                let obj = func_node
                    .child_by_field_name("object")
                    .map(|n| self.get_node_text(&n))
                    .unwrap_or_default();
                let prop = func_node
                    .child_by_field_name("property")
                    .map(|n| self.get_node_text(&n))
                    .unwrap_or_default();
                if obj.is_empty() {
                    prop
                } else {
                    format!("{}.{}", obj, prop)
                }
            }
            _ => {
                let text = self.get_node_text(func_node);
                text.trim().to_string()
            }
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
    fn extract_variables(&self, body: &Node, params: &[Parameter]) -> Vec<Variable> {
        let mut variables: HashMap<String, Variable> = HashMap::new();
        for param in params {
            if param.type_annotation != "destructured" {
                variables.insert(
                    param.name.clone(),
                    Variable {
                        name: param.name.clone(),
                        var_type: None,
                        scope: "param".to_string(),
                        defined_at: None,
                        transformations: vec![],
                        used_in: vec![],
                        returned: false,
                    },
                );
            }
        }
        self.track_variable_usage(body, &mut variables);
        variables.into_values().collect()
    }

    fn track_variable_usage(&self, node: &Node, variables: &mut HashMap<String, Variable>) {
        let mut cursor = node.walk();
        if node.kind() == "lexical_declaration" || node.kind() == "variable_declaration" {
            let mut c = node.walk();
            for child in node.children(&mut c) {
                if child.kind() == "variable_declarator" {
                    if let Some(name_node) = child.child_by_field_name("name") {
                        if name_node.kind() == "identifier" {
                            let var_name = self.get_node_text(&name_node);
                            if !variables.contains_key(&var_name) {
                                let line = child.start_position().row + 1;
                                variables.insert(
                                    var_name.clone(),
                                    Variable {
                                        name: var_name,
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

    // Control flow / exceptions
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
            "for_statement" | "for_in_statement" | "while_statement" | "do_statement" => {
                if let Some(loop_info) = self.parse_loop(node) {
                    cf.loops.push(loop_info);
                }
            }
            "try_statement" => {
                cf.try_blocks.push(self.parse_try_statement(node));
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
            raises: self.find_throw_value(block),
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
                let name = self.resolve_callee(&func_node);
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

    fn find_throw_value(&self, node: &Node) -> Option<String> {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "throw_statement" {
                let mut vals = Vec::new();
                let mut tc = child.walk();
                for tchild in child.children(&mut tc) {
                    if tchild.kind() != "throw" && tchild.kind() != ";" {
                        vals.push(self.get_node_text(&tchild));
                    }
                }
                return Some(vals.join(", "));
            }
            if let Some(val) = self.find_throw_value(&child) {
                return Some(val);
            }
        }
        None
    }

    fn parse_loop(&self, node: &Node) -> Option<Loop> {
        let line = node.start_position().row + 1;
        let loop_type = match node.kind() {
            "for_statement" => "for",
            "for_in_statement" => "for-in/of",
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

    fn parse_try_statement(&self, node: &Node) -> TryBlock {
        let line = node.start_position().row + 1;
        let mut try_calls = Vec::new();
        let mut except_clauses = Vec::new();
        let mut finally_calls = Vec::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "statement_block" if try_calls.is_empty() && except_clauses.is_empty() => {
                    try_calls = self.extract_calls_from_block(&child);
                }
                "catch_clause" => {
                    let exception_type = child
                        .child_by_field_name("parameter")
                        .map(|p| self.get_node_text(&p))
                        .unwrap_or_else(|| "Error".to_string());
                    let body = child.child_by_field_name("body").unwrap_or(child);
                    except_clauses.push(ExceptClause {
                        exception_type,
                        line: child.start_position().row + 1,
                        calls: self.extract_calls_from_block(&body),
                    });
                }
                "finally_clause" => {
                    finally_calls = self.extract_calls_from_block(&child);
                }
                _ => {}
            }
        }
        TryBlock {
            line,
            try_calls,
            except_clauses,
            finally_calls,
        }
    }

    fn build_exception_info(&self, body: &Node) -> ExceptionInfo {
        let mut raises = Vec::new();
        let mut handles = Vec::new();
        self.collect_throws(body, &mut raises);
        self.collect_catches(body, &mut handles);
        ExceptionInfo {
            raises,
            propagates: vec![],
            handles,
        }
    }

    fn collect_throws(&self, node: &Node, raises: &mut Vec<String>) {
        let mut cursor = node.walk();
        if node.kind() == "throw_statement" {
            raises.push(self.get_node_text(node));
        }
        for child in node.children(&mut cursor) {
            self.collect_throws(&child, raises);
        }
    }

    fn collect_catches(&self, node: &Node, handles: &mut Vec<String>) {
        let mut cursor = node.walk();
        if node.kind() == "catch_clause" {
            let exception_type = node
                .child_by_field_name("parameter")
                .map(|p| self.get_node_text(&p))
                .unwrap_or_else(|| "Error".to_string());
            handles.push(exception_type);
        }
        for child in node.children(&mut cursor) {
            self.collect_catches(&child, handles);
        }
    }

    // Classes
    fn extract_classes(&self, root: &Node) -> Vec<Class> {
        let mut classes = Vec::new();
        self.walk_top_level_classes(root, &mut classes);
        classes
    }

    fn walk_top_level_classes(&self, node: &Node, classes: &mut Vec<Class>) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "class_declaration" => {
                    if let Some(c) = self.parse_class(&child) {
                        classes.push(c);
                    }
                }
                "export_statement" => {
                    if let Some(decl) = child.child_by_field_name("declaration") {
                        if decl.kind() == "class_declaration" {
                            if let Some(c) = self.parse_class(&decl) {
                                classes.push(c);
                            }
                        }
                    }
                }
                _ => {}
            }
        }
    }

    fn parse_class(&self, node: &Node) -> Option<Class> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);
        let bases = node
            .child_by_field_name("superclass")
            .map(|s| vec![self.get_node_text(&s)])
            .unwrap_or_default();
        let body = node.child_by_field_name("body")?;
        let (methods, attributes) = self.extract_class_members(&body, &name);
        let id = self.make_class_id(&name);
        Some(Class {
            id,
            name,
            bases,
            docstring: self.extract_docstring(node),
            line_start: node.start_position().row + 1,
            line_end: node.end_position().row + 1,
            methods,
            attributes,
            decorators: vec!["class".to_string()],
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    fn extract_class_members(
        &self,
        body: &Node,
        class_name: &str,
    ) -> (Vec<Function>, Vec<Attribute>) {
        let mut methods = Vec::new();
        let mut attributes = Vec::new();
        let mut cursor = body.walk();
        for child in body.children(&mut cursor) {
            match child.kind() {
                "method_definition" => {
                    if let Some(m) = self.parse_method(&child, class_name) {
                        methods.push(m);
                    }
                }
                "field_definition" | "public_field_definition" => {
                    let name = child
                        .child_by_field_name("property")
                        .map(|n| self.get_node_text(&n))
                        .unwrap_or_default();
                    let value = child
                        .child_by_field_name("value")
                        .map(|v| self.get_node_text(&v));
                    if !name.is_empty() {
                        attributes.push(Attribute {
                            name,
                            type_annotation: String::new(),
                            value,
                        });
                    }
                }
                _ => {}
            }
        }
        (methods, attributes)
    }

    fn parse_method(&self, node: &Node, class_name: &str) -> Option<Function> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);
        let mut func = self.parse_function_body(node, &name, class_name, node)?;
        let is_static = self.has_keyword_child(node, "static");
        let kind_get = self.has_keyword_child(node, "get");
        let kind_set = self.has_keyword_child(node, "set");
        if is_static {
            func.decorators.push("static".to_string());
        }
        if kind_get {
            func.decorators.push("getter".to_string());
        }
        if kind_set {
            func.decorators.push("setter".to_string());
        }
        if name == "constructor" {
            func.tags.push("constructor".to_string());
        }
        func.tags.sort();
        func.tags.dedup();
        Some(func)
    }

    // Global vars
    fn extract_global_vars(&self, root: &Node) -> Vec<GlobalVar> {
        let mut vars = Vec::new();
        let mut cursor = root.walk();
        for child in root.children(&mut cursor) {
            self.collect_global_var_decl(&child, &mut vars);
        }
        vars
    }

    fn collect_global_var_decl(&self, node: &Node, vars: &mut Vec<GlobalVar>) {
        match node.kind() {
            "lexical_declaration" | "variable_declaration" => {
                let mut cursor = node.walk();
                for child in node.children(&mut cursor) {
                    if child.kind() == "variable_declarator" {
                        if let Some(value) = child.child_by_field_name("value") {
                            // skip functions - those are captured as Function entries
                            if value.kind() == "arrow_function"
                                || value.kind() == "function_expression"
                            {
                                continue;
                            }
                        }
                        if let Some(name_node) = child.child_by_field_name("name") {
                            if name_node.kind() == "identifier" {
                                vars.push(GlobalVar {
                                    name: self.get_node_text(&name_node),
                                    type_annotation: String::new(),
                                    value: child
                                        .child_by_field_name("value")
                                        .map(|v| self.get_node_text(&v)),
                                    line: node.start_position().row + 1,
                                });
                            }
                        }
                    }
                }
            }
            "export_statement" => {
                if let Some(decl) = node.child_by_field_name("declaration") {
                    self.collect_global_var_decl(&decl, vars);
                }
            }
            _ => {}
        }
    }

    // Docstrings / comments / TODOs
    fn extract_docstring(&self, node: &Node) -> String {
        // walk up through export wrappers to find the true leading sibling
        let target = if let Some(parent) = node.parent() {
            if parent.kind() == "export_statement" {
                parent
            } else {
                *node
            }
        } else {
            *node
        };
        if let Some(prev) = target.prev_sibling() {
            if prev.kind() == "comment" {
                let text = self.get_node_text(&prev);
                return text
                    .trim_start_matches("/**")
                    .trim_start_matches("/*")
                    .trim_start_matches("//")
                    .trim_end_matches("*/")
                    .lines()
                    .map(|l| l.trim().trim_start_matches('*').trim())
                    .collect::<Vec<_>>()
                    .join(" ")
                    .trim()
                    .to_string();
            }
        }
        String::new()
    }

    fn extract_todos(&self) -> Vec<Todo> {
        self.source_code
            .lines()
            .enumerate()
            .filter_map(|(idx, line)| {
                TODO_RE.captures(line.as_bytes()).map(|caps| {
                    let text = caps
                        .get(1)
                        .map(|m| String::from_utf8_lossy(m.as_bytes()).trim().to_string())
                        .unwrap_or_default();
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
            let line_bytes = line.as_bytes();
            for pattern in SECURITY_PATTERNS.iter() {
                if pattern.regex.is_match(line_bytes) {
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

    // JSX / React heuristics
    fn contains_jsx(&self, node: &Node) -> bool {
        if matches!(
            node.kind(),
            "jsx_element" | "jsx_self_closing_element" | "jsx_fragment"
        ) {
            return true;
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if self.contains_jsx(&child) {
                return true;
            }
        }
        false
    }

    /// A function expression immediately invoked, e.g. `(function(){...})()`
    /// or `(() => {...})()`.
    fn is_iife(&self, fn_node: &Node) -> bool {
        fn_node
            .parent()
            .map(|p| p.kind() == "parenthesized_expression")
            .unwrap_or(false)
            && fn_node
                .parent()
                .and_then(|p| p.parent())
                .map(|gp| gp.kind() == "call_expression")
                .unwrap_or(false)
    }

    /// `module.exports = name` or `exports.name = ...` / `module.exports.name`.
    fn is_commonjs_export(&self, name: &str) -> bool {
        let re_hit = self
            .source_code
            .contains(&format!("module.exports.{}", name))
            || self.source_code.contains(&format!("exports.{}", name));
        re_hit
    }

    fn detect_module_system(&self) -> Option<String> {
        let has_esm = self.source_code.contains("import ") || self.source_code.contains("export ");
        let has_cjs =
            self.source_code.contains("require(") || self.source_code.contains("module.exports");
        match (has_esm, has_cjs) {
            (true, true) => Some("mixed".to_string()),
            (true, false) => Some("esm".to_string()),
            (false, true) => Some("commonjs".to_string()),
            (false, false) => None,
        }
    }

    fn uses_strict_mode(&self, body: &Node) -> bool {
        let mut cursor = body.walk();
        for child in body.children(&mut cursor) {
            if child.kind() == "expression_statement" {
                let text = self.get_node_text(&child);
                if text.trim() == "\"use strict\";" || text.trim() == "'use strict';" {
                    return true;
                }
            }
        }
        false
    }

    fn uses_var_keyword(&self, body: &Node) -> bool {
        self.has_var_decl(body)
    }

    fn has_var_decl(&self, node: &Node) -> bool {
        if node.kind() == "variable_declaration" {
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                if child.kind() == "var" {
                    return true;
                }
            }
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if self.has_var_decl(&child) {
                return true;
            }
        }
        false
    }

    /// Does this function's body return another function (arrow/expression)?
    fn returns_function(&self, body: &Node) -> bool {
        let mut cursor = body.walk();
        for child in body.children(&mut cursor) {
            if child.kind() == "return_statement" {
                let mut rc = child.walk();
                for rchild in child.children(&mut rc) {
                    if matches!(rchild.kind(), "arrow_function" | "function_expression") {
                        return true;
                    }
                }
            }
            if self.returns_function(&child) {
                return true;
            }
        }
        false
    }

    fn is_hook_name(&self, name: &str) -> bool {
        name.len() > 3
            && name.starts_with("use")
            && name
                .chars()
                .nth(3)
                .map(|c| c.is_uppercase())
                .unwrap_or(false)
    }

    fn is_component_name(&self, name: &str) -> bool {
        name.chars()
            .next()
            .map(|c| c.is_uppercase())
            .unwrap_or(false)
    }

    // Tagging / scoring
    fn auto_tag_function(
        &self,
        name: &str,
        docstring: &str,
        calls: &[FunctionCall],
        body_text: &str,
        has_jsx: bool,
    ) -> Vec<String> {
        let mut tags = Vec::new();
        let name_lower = name.to_lowercase();
        let doc_lower = docstring.to_lowercase();

        if name == "main" {
            tags.push("entry-point".to_string());
        }

        for rule in TAG_RULES.iter() {
            let name_matches = rule.keywords.iter().any(|kw| name_lower.contains(kw));
            let doc_matches =
                rule.check_docstring && rule.keywords.iter().any(|kw| doc_lower.contains(kw));
            if name_matches || doc_matches {
                tags.push(rule.tag.to_string());
                if rule.tag == "authentication" {
                    tags.push("security".to_string());
                }
            }
        }

        if self.is_hook_name(name) {
            tags.push("react-hook".to_string());
        }
        if has_jsx {
            tags.push("jsx".to_string());
            if self.is_component_name(name) {
                tags.push("react-component".to_string());
            }
        }

        let calls_joined = calls
            .iter()
            .map(|c| c.callee.as_str())
            .collect::<Vec<_>>()
            .join(" ");
        if calls_joined.contains("useState") || calls_joined.contains("useReducer") {
            tags.push("stateful".to_string());
        }
        if calls_joined.contains("useEffect") || calls_joined.contains("useLayoutEffect") {
            tags.push("side-effects".to_string());
        }
        if EVAL_RE.is_match(body_text.as_bytes()) {
            tags.push("unsafe".to_string());
        }
        if name_lower.starts_with("test") || name_lower.starts_with("it_") {
            tags.push("testing".to_string());
        }

        tags.sort();
        tags.dedup();
        tags
    }

    fn calculate_complexity(&self, node: &Node) -> usize {
        fn count(node: &Node) -> usize {
            let mut c = 0;
            let mut cursor = node.walk();
            match node.kind() {
                "if_statement" | "for_statement" | "for_in_statement" | "while_statement"
                | "do_statement" | "switch_statement" | "switch_case" | "ternary_expression"
                | "catch_clause" | "&&" | "||" | "??" => c += 1,
                _ => {}
            }
            for child in node.children(&mut cursor) {
                c += count(&child);
            }
            c
        }
        1 + count(node)
    }

    fn estimate_importance(&self, name: &str, tags: &[String]) -> f32 {
        let mut score: f32 = 0.5;
        if name == "main" || tags.iter().any(|t| t == "entry-point") {
            score += 0.3;
        }
        if tags.iter().any(|t| t == "react-component" || t == "api") {
            score += 0.1;
        }
        if name.starts_with('_') {
            score -= 0.1;
        }
        if tags.iter().any(|t| t == "testing") {
            score -= 0.1;
        }
        score.clamp(0.0, 1.0)
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
    let mut parser = JsParser::new(source_code, path_str.clone());
    let file_data = parser.parse()?;
    Ok((path_str, file_data))
}
