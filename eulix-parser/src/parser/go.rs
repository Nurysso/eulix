//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::struc::kb_struct::*;
use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use tree_sitter::{Node, Parser};

pub struct GoParser {
    source_code: String,
    file_path: String,
    build_tags: Vec<String>,
    #[allow(dead_code)]
    go_directives: Vec<String>,
    uses_cgo: bool,
    embed_patterns: Vec<String>,
}

impl GoParser {
    pub fn new(source_code: String, file_path: String) -> Self {
        let (build_tags, go_directives, uses_cgo, embed_patterns) =
            Self::pre_scan_file_directives(&source_code);
        Self {
            source_code,
            file_path,
            build_tags,
            go_directives,
            uses_cgo,
            embed_patterns,
        }
    }
    fn pre_scan_file_directives(src: &str) -> (Vec<String>, Vec<String>, bool, Vec<String>) {
        let mut build_tags = Vec::new();
        let mut go_directives = Vec::new();
        let mut uses_cgo = false;
        let mut embed_patterns = Vec::new();

        for line in src.lines() {
            let trimmed = line.trim();

            // //go:build constraints (new style)
            if let Some(rest) = trimmed.strip_prefix("//go:build ") {
                build_tags.push(rest.trim().to_string());
            }
            // Old-style: // +build
            else if let Some(rest) = trimmed.strip_prefix("// +build ") {
                build_tags.push(rest.trim().to_string());
            }
            // //go:embed
            else if let Some(rest) = trimmed.strip_prefix("//go:embed ") {
                embed_patterns.push(rest.trim().to_string());
            }
            // Other //go: directives
            else if trimmed.starts_with("//go:") {
                go_directives.push(trimmed.to_string());
            }
            // CGo
            if trimmed == r#"import "C""#
                || trimmed.contains("\"C\"") && trimmed.starts_with("import")
            {
                uses_cgo = true;
            }
        }

        (build_tags, go_directives, uses_cgo, embed_patterns)
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

    /// e.g. struct_PathGate::internal/query/gate.go
    fn make_struct_id(&self, name: &str) -> String {
        format!("struct_{}::{}", name, self.file_path)
    }

    /// e.g. interface_QueryEmbedder::internal/query/types.go
    fn make_interface_id(&self, name: &str) -> String {
        format!("interface_{}::{}", name, self.file_path)
    }

    /// e.g. ifacemethod_EmbedQueryBinary::internal/query/types.go
    fn make_ifacemethod_id(&self, name: &str) -> String {
        format!("ifacemethod_{}::{}", name, self.file_path)
    }

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "import_declaration" {
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
        }

        imports
    }

    fn classify_import(&self, module: &str) -> String {
        // Top-level stdlib package names and common sub-paths
        const STDLIB_PREFIXES: &[&str] = &[
            "archive",
            "bufio",
            "builtin",
            "bytes",
            "compress",
            "container",
            "context",
            "crypto",
            "database",
            "debug",
            "encoding",
            "errors",
            "expvar",
            "flag",
            "fmt",
            "go/",
            "hash",
            "html",
            "image",
            "index",
            "io",
            "log",
            "math",
            "mime",
            "net",
            "os",
            "path",
            "plugin",
            "reflect",
            "regexp",
            "runtime",
            "sort",
            "strconv",
            "strings",
            "sync",
            "syscall",
            "testing",
            "text",
            "time",
            "unicode",
            "unsafe",
            "embed",
            "slices",
            "maps",
            "cmp", // Go 1.21+
            "iter",
            "structs", // Go 1.23+
        ];

        // CGo pseudo-package
        if module == "C" {
            return "cgo".to_string();
        }

        if STDLIB_PREFIXES
            .iter()
            .any(|s| module == *s || module.starts_with(&format!("{}/", s)))
        {
            return "stdlib".to_string();
        }

        // Relative or local path
        if module.starts_with('.') {
            return "internal".to_string();
        }

        // No dot in first path segment → could be an old-style local pkg, but
        // modern Go modules always have a domain; treat no-slash as internal.
        if !module.contains('/') {
            return "internal".to_string();
        }

        "external".to_string()
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
            // method_declaration intentionally NOT handled here methods are
            // parsed once, in extract_structs()'s methods_map, and attached to
            // their owning struct. Parsing them here too was producing a second
            // Function with the same ID.
        }

        functions
    }

    /// Extracts constraint text from type parameters: [T any, K comparable] → ["any", "comparable"]
    fn extract_type_constraints(&self, node: &Node) -> Vec<String> {
        let mut constraints = Vec::new();
        if let Some(tp_node) = node.child_by_field_name("type_parameters") {
            let mut cursor = tp_node.walk();
            for child in tp_node.children(&mut cursor) {
                if child.kind() == "type_parameter_declaration" {
                    // constraint is the type field of the parameter decl
                    if let Some(constraint) = child.child_by_field_name("type") {
                        constraints.push(self.get_node_text(&constraint).trim().to_string());
                    }
                }
            }
        }
        constraints
    }

    /// Extracts //go: directives that appear in comments immediately above the function.
    fn extract_func_directives(&self, node: &Node) -> Vec<String> {
        let mut directives = Vec::new();
        let mut sib = node.prev_sibling();
        while let Some(s) = sib {
            if s.kind() == "comment" {
                let text = self.get_node_text(&s);
                if text.trim().starts_with("//go:") {
                    directives.push(text.trim().to_string());
                }
                sib = s.prev_sibling();
            } else {
                break;
            }
        }
        directives.reverse();
        directives
    }

    fn parse_function(&self, node: &Node, struct_context: &str) -> Option<Function> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);

        // Check if it's a method (has receiver)
        let receiver = node
            .child_by_field_name("receiver")
            .map(|r| self.get_node_text(&r));
        // eprintln!("    Receiver text: {:?}", receiver);

        let params = self.extract_parameters(node);
        // eprintln!(
        //     "    Parameters: {:?}",
        //     params.iter().map(|p| &p.name).collect::<Vec<_>>()
        // );
        let return_type = self.extract_return_type(node);
        // eprintln!("    Return type: {}", return_type);
        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);
        let signature = self.build_signature(&name, &params, &return_type, receiver.as_deref());

        let body = match node.child_by_field_name("body") {
            Some(b) => b,
            None => {
                // Interface methods don't have bodies
                // Return a minimal Function struct
                let id = self.make_function_id(&name, struct_context);

                return Some(Function {
                    id,
                    name,
                    signature,
                    params,
                    return_type,
                    docstring,
                    line_start,
                    line_end,
                    calls: vec![],
                    called_by: vec![],
                    variables: vec![],
                    control_flow: ControlFlow::default(),
                    exceptions: ExceptionInfo::default(),
                    complexity: 0,
                    is_async: false,
                    decorators: vec![],
                    tags: vec!["interface-method".to_string()],
                    importance_score: 0.5,
                    lang_info: LanguageSpecificInfo::default(),
                });
            }
        };
        let calls = self.extract_function_calls_detailed(&body);
        let variables = self.extract_variables(&body, &params);
        let control_flow = self.build_control_flow(&body);
        let exceptions = self.extract_exception_info(&body);
        let complexity = self.calculate_complexity(&body);

        let id = self.make_function_id(&name, struct_context);

        let tags = self.auto_tag_function(&name, &docstring, &calls);
        let importance_score = self.estimate_importance(&name, receiver.is_some());

        let (recv_type, recv_name) = self.parse_receiver(node);
        let is_pointer_receiver = node
            .child_by_field_name("receiver")
            .map(|r| self.get_node_text(&r).contains('*'))
            .unwrap_or(false);

        let type_params = self.extract_type_params(node);
        let type_constraints = self.extract_type_constraints(node);

        let go_info = GoInfo {
            is_exported: name.chars().next().is_some_and(|c| c.is_uppercase()),
            receiver_type: recv_type.map(|t| {
                // Preserve pointer indicator for caller
                if is_pointer_receiver {
                    format!("*{}", t)
                } else {
                    t
                }
            }),
            receiver_name: recv_name,
            is_interface_method: false,
            spawns_goroutines: self.body_contains_kind(&body, "go_statement"),
            uses_channels: self.body_contains_channel_ops(&body),
            uses_select: self.body_contains_kind(&body, "select_statement"),
            uses_mutex: self.source_contains_in_range("sync.Mutex", line_start, line_end)
                || self.source_contains_in_range("sync.RWMutex", line_start, line_end),
            uses_waitgroup: self.source_contains_in_range("sync.WaitGroup", line_start, line_end),
            uses_atomic: self.source_contains_in_range("sync/atomic", line_start, line_end)
                || self.source_contains_in_range("atomic.", line_start, line_end),
            returns_error: return_type.contains("error"),
            uses_panic: calls.iter().any(|c| c.callee == "panic"),
            uses_recover: calls.iter().any(|c| c.callee == "recover"),
            defer_count: self.count_kind_in_body(&body, "defer_statement"),
            type_params,
            type_constraints,
            build_tags: self.build_tags.clone(),
            go_directives: self.extract_func_directives(node),
            uses_cgo: self.uses_cgo,
            embed_patterns: self.embed_patterns.clone(),
            is_variadic: params
                .last()
                .is_some_and(|p| p.type_annotation.starts_with("...")),
            // Add the missing fields:
            has_embedded_types: false,
            is_pointer_receiver,
            type_kind: None,
        };

        // eprintln!(
        //     "    Successfully created function: {} (lines {}-{})",
        //     name, line_start, line_end
        // );
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
            lang_info: LanguageSpecificInfo {
                go: Some(go_info),
                ..Default::default()
            },
        })
    }

    /// Returns (receiver_type, receiver_name) for a method declaration.
    fn parse_receiver(&self, node: &Node) -> (Option<String>, Option<String>) {
        let receiver = match node.child_by_field_name("receiver") {
            Some(r) => {
                // let text = self.get_node_text(&r);
                // eprintln!("      Receiver node kind: '{}', text: '{}'", r.kind(), text);
                r
            }
            None => {
                // eprintln!("      No receiver field found");
                return (None, None);
            }
        };

        // Print children structure
        let mut rc = receiver.walk();
        for child in receiver.children(&mut rc) {
            // let _child_text = self.get_node_text(&child);
            // eprintln!(
            //     "      Receiver child: kind='{}', text='{}'",
            //     child.kind(),
            //     child_text
            // );

            if child.kind() == "parameter_declaration" {
                let rtype = child.child_by_field_name("type").map(|t| {
                    let type_text = self.get_node_text(&t);
                    // eprintln!(
                    //     "        Type node text: '{}', trimmed: '{}'",
                    //     type_text,
                    //     type_text.trim_start_matches('*')
                    // );
                    type_text.trim_start_matches('*').to_string()
                });
                let rname = child.child_by_field_name("name").map(|n| {
                    let name_text = self.get_node_text(&n);
                    // eprintln!("        Name node text: '{}'", name_text);
                    name_text
                });
                // eprintln!(
                //     "        Parsed receiver - type: {:?}, name: {:?}",
                //     rtype, rname
                // );
                return (rtype, rname);
            }
        }

        // eprintln!("      No parameter_declaration found in receiver children");
        (None, None)
    }

    /// True if any descendant node has the given kind.
    fn body_contains_kind(&self, node: &Node, kind: &str) -> bool {
        if node.kind() == kind {
            return true;
        }
        let mut cursor = node.walk();
        let children: Vec<_> = node.children(&mut cursor).collect();
        children.iter().any(|c| self.body_contains_kind(c, kind))
    }

    /// True if the body sends to or receives from a channel (`<-` operator).
    fn body_contains_channel_ops(&self, node: &Node) -> bool {
        if node.kind() == "send_statement" || node.kind() == "receive_statement" {
            return true;
        }
        // Also catch `<-` used in expressions
        if node.kind() == "unary_expression" {
            let op = node.child(0).map(|c| self.get_node_text(&c));
            if op.as_deref() == Some("<-") {
                return true;
            }
        }
        let mut cursor = node.walk();
        let children: Vec<_> = node.children(&mut cursor).collect();
        children.iter().any(|c| self.body_contains_channel_ops(c))
    }

    /// Counts direct descendant nodes of `kind` (non-recursive, one level for
    /// statements like defer that appear at statement-list level).
    fn count_kind_in_body(&self, node: &Node, kind: &str) -> usize {
        let mut count = 0;
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == kind {
                count += 1;
            } else {
                count += self.count_kind_in_body(&child, kind);
            }
        }
        count
    }

    /// Checks if `needle` appears in the source lines between `start` and `end`
    /// (1-indexed, inclusive).  Cheap string scan — no AST needed.
    fn source_contains_in_range(&self, needle: &str, start: usize, end: usize) -> bool {
        self.source_code
            .lines()
            .enumerate()
            .skip(start.saturating_sub(1))
            .take(end.saturating_sub(start) + 1)
            .any(|(_, line)| line.contains(needle))
    }

    /// Extracts generic type parameters from a function/method declaration.
    /// Returns e.g. ["T any", "K comparable"] for `func Foo[T any, K comparable]`.
    fn extract_type_params(&self, node: &Node) -> Vec<String> {
        let mut result = Vec::new();
        if let Some(tp_node) = node.child_by_field_name("type_parameters") {
            let mut cursor = tp_node.walk();
            for child in tp_node.children(&mut cursor) {
                if child.kind() == "type_parameter_declaration" {
                    result.push(self.get_node_text(&child));
                }
            }
        }
        result
    }

    fn extract_parameters(&self, node: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();

        let param_list = match node.child_by_field_name("parameters") {
            Some(p) => p,
            None => return params,
        };

        let mut cursor = param_list.walk();
        for child in param_list.children(&mut cursor) {
            match child.kind() {
                "parameter_declaration" => {
                    // May have multiple names: (x, y int)
                    let type_annotation = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_default();

                    // Collect all identifier children as names
                    let mut names: Vec<String> = Vec::new();
                    let mut pc = child.walk();
                    for sub in child.children(&mut pc) {
                        if sub.kind() == "identifier" {
                            names.push(self.get_node_text(&sub));
                        }
                    }

                    if names.is_empty() {
                        // Unnamed param (e.g. in interface method specs)
                        params.push(Parameter {
                            name: "_".to_string(),
                            type_annotation,
                            default_value: None,
                        });
                    } else {
                        for name in names {
                            params.push(Parameter {
                                name,
                                type_annotation: type_annotation.clone(),
                                default_value: None,
                            });
                        }
                    }
                }
                "variadic_parameter_declaration" => {
                    // `args ...string`  or unnamed `...string`
                    let type_annotation = child
                        .child_by_field_name("type")
                        .map(|t| format!("...{}", self.get_node_text(&t)))
                        .unwrap_or_else(|| "...interface{}".to_string());

                    let name = child
                        .child_by_field_name("name")
                        .map(|n| self.get_node_text(&n))
                        .unwrap_or_else(|| "_".to_string());

                    params.push(Parameter {
                        name,
                        type_annotation,
                        default_value: None,
                    });
                }
                _ => {}
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
        let current_context = match node.kind() {
            "if_statement" => "if",
            "for_statement" => "loop",
            "switch_statement" | "expression_switch_statement" => "switch",
            "select_statement" => "select",
            _ => context,
        };

        match node.kind() {
            // ── goroutine: extract inner call, mark as "goroutine", skip subtree
            "go_statement" => {
                let mut gc = node.walk();
                for child in node.children(&mut gc) {
                    if child.kind() == "call_expression" {
                        self.push_call(&child, calls, seen, "goroutine");
                    }
                }
                return; // don't recurse further — the call is already captured
            }

            // ── defer: same treatment
            "defer_statement" => {
                let mut dc = node.walk();
                for child in node.children(&mut dc) {
                    if child.kind() == "call_expression" {
                        self.push_call(&child, calls, seen, "defer");
                    }
                }
                return;
            }

            // ── normal call expression
            "call_expression" => {
                self.push_call(node, calls, seen, current_context);
                // still recurse: the arguments may contain nested calls
            }

            _ => {}
        }

        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.find_calls_recursive(&child, calls, seen, current_context);
        }
    }

    /// Helper — extracts callee name and pushes a FunctionCall if not already seen.
    fn push_call(
        &self,
        call_node: &Node,
        calls: &mut Vec<FunctionCall>,
        seen: &mut HashSet<String>,
        context: &str,
    ) {
        if let Some(func_node) = call_node.child_by_field_name("function") {
            let full = self.get_node_text(&func_node);
            // Use the last segment so "fmt.Println" → "Println", but keep
            // the full name too for qualified calls in `defined_in`.
            let callee = full.split('.').next_back().unwrap_or(&full).trim().to_string();

            if callee.is_empty() {
                return;
            }

            let key = format!("{}:{}", callee, call_node.start_position().row);
            if seen.insert(key) {
                let qualifier = if full.contains('.') {
                    let parts: Vec<&str> = full.rsplitn(2, '.').collect();
                    Some(parts[1].to_string()) // e.g. "fmt"
                } else {
                    None
                };

                calls.push(FunctionCall {
                    callee,
                    defined_in: qualifier,
                    line: call_node.start_position().row + 1,
                    args: self.extract_call_arguments(call_node),
                    is_conditional: matches!(context, "if" | "loop" | "switch" | "select"),
                    context: context.to_string(),
                });
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

        // First pass: find all type declarations
        for child in root.children(&mut cursor) {
            if child.kind() == "type_declaration" {
                let mut type_cursor = child.walk();
                for spec in child.children(&mut type_cursor) {
                    if spec.kind() == "type_spec" {
                        if let Some(type_node) = spec.child_by_field_name("type") {
                            match type_node.kind() {
                                "struct_type" => {
                                    if let Some(s) = self.parse_struct(&spec) {
                                        structs.push(s);
                                    }
                                }
                                "interface_type" => {
                                    if let Some(i) = self.parse_interface(&spec) {
                                        structs.push(i);
                                    }
                                }
                                _ => {}
                            }
                        }
                    }
                }
            }
        }

        // Second pass: find methods for structs
        let mut methods_map: HashMap<String, Vec<Function>> = HashMap::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "method_declaration" {
                // Try to extract the receiver type
                let (receiver_type, _) = self.parse_receiver(&child);
                if let Some(type_name) = receiver_type {
                    // Parse this as a method of the receiver type
                    if let Some(method) = self.parse_function(&child, &type_name) {
                        methods_map
                            .entry(type_name)
                            .or_default()
                            .push(method);
                    }
                }
            }
        }

        // Attach methods to their structs
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

        let has_embedded = attributes
            .iter()
            .any(|a| a.name.is_empty() || a.name == "_");

        Some(Class {
            id: self.make_struct_id(&name),
            name: name.clone(),
            bases: vec![],
            docstring,
            line_start,
            line_end,
            methods: vec![],
            attributes,
            decorators: vec![],
            lang_info: LanguageSpecificInfo {
                go: Some(GoInfo {
                    is_exported: name.chars().next().is_some_and(|c| c.is_uppercase()),
                    type_kind: Some(GoTypeKind::Struct),
                    has_embedded_types: has_embedded,
                    build_tags: self.build_tags.clone(),
                    uses_cgo: self.uses_cgo,
                    ..Default::default()
                }),
                ..Default::default()
            },
        })
    }

    fn extract_struct_fields(&self, struct_node: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();

        if let Some(body) = struct_node.child_by_field_name("body") {
            let mut cursor = body.walk();
            for child in body.children(&mut cursor) {
                if child.kind() == "field_declaration" {
                    if let Some(name_node) = child.child_by_field_name("name") {
                        // Normal named field
                        let name = self.get_node_text(&name_node);
                        let type_annotation = child
                            .child_by_field_name("type")
                            .map(|t| self.get_node_text(&t))
                            .unwrap_or_default();
                        let tag = child
                            .child_by_field_name("tag")
                            .map(|t| self.get_node_text(&t));
                        fields.push(Attribute {
                            name,
                            type_annotation,
                            value: tag,
                        });
                    } else if let Some(type_node) = child.child_by_field_name("type") {
                        // Embedded type: struct { io.Reader } — no name node
                        let type_text = self
                            .get_node_text(&type_node)
                            .trim_start_matches('*')
                            .to_string();
                        fields.push(Attribute {
                            name: String::new(), // signals embedded to has_embedded check
                            type_annotation: type_text,
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
        let line = node.start_position().row + 1;

        // name field can be an identifier_list for multi-name specs
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))
            .unwrap_or_default();

        if name.is_empty() {
            return None;
        }

        let type_annotation = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_default();

        // value can be an expression_list for multi-value specs
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

    fn parse_interface(&self, node: &Node) -> Option<Class> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);
        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        let methods = if let Some(type_node) = node.child_by_field_name("type") {
            if type_node.kind() == "interface_type" {
                self.extract_interface_methods(&type_node)
            } else {
                vec![]
            }
        } else {
            vec![]
        };

        Some(Class {
            id: self.make_interface_id(&name),
            name: name.clone(),
            bases: vec![],
            docstring,
            line_start,
            line_end,
            methods,
            attributes: vec![],
            decorators: vec!["interface".to_string()], // reuse decorators to signal kind
            lang_info: LanguageSpecificInfo {
                go: Some(GoInfo {
                    is_exported: name.chars().next().is_some_and(|c| c.is_uppercase()),
                    type_kind: Some(GoTypeKind::Interface),
                    build_tags: self.build_tags.clone(),
                    uses_cgo: self.uses_cgo,
                    ..Default::default()
                }),
                ..Default::default()
            },
        })
    }

    fn extract_interface_methods(&self, interface_node: &Node) -> Vec<Function> {
        let mut methods = Vec::new();

        if let Some(body) = interface_node.child_by_field_name("body") {
            let mut cursor = body.walk();
            for child in body.children(&mut cursor) {
                if child.kind() == "method_spec" {
                    if let Some(name_node) = child.child_by_field_name("name") {
                        let name = self.get_node_text(&name_node);
                        let params = self.extract_parameters(&child);
                        let return_type = self.extract_return_type(&child);
                        let signature = self.build_signature(&name, &params, &return_type, None);
                        let line_start = child.start_position().row + 1;

                        methods.push(Function {
                            id: self.make_ifacemethod_id(&name),
                            name: name.clone(),
                            signature,
                            params: params.clone(),
                            return_type: return_type.clone(),
                            docstring: String::new(),
                            line_start,
                            line_end: line_start,
                            calls: vec![],
                            called_by: vec![],
                            variables: vec![],
                            control_flow: ControlFlow::default(),
                            exceptions: ExceptionInfo::default(),
                            complexity: 1,
                            is_async: false,
                            decorators: vec![],
                            tags: vec!["interface-method".to_string()],
                            importance_score: 0.5,
                            lang_info: LanguageSpecificInfo {
                                go: Some(GoInfo {
                                    is_exported: name
                                        .chars()
                                        .next()
                                        .is_some_and(|c| c.is_uppercase()),
                                    is_interface_method: true,
                                    returns_error: return_type.contains("error"),
                                    is_variadic: params
                                        .last()
                                        .is_some_and(|p| p.type_annotation.starts_with("...")),
                                    build_tags: self.build_tags.clone(),
                                    uses_cgo: self.uses_cgo,
                                    ..Default::default()
                                }),
                                ..Default::default()
                            },
                        });
                    }
                }
            }
        }

        methods
    }

    fn extract_docstring(&self, node: &Node) -> String {
        // Walk backwards through siblings looking for comment(s)
        let mut lines: Vec<String> = Vec::new();
        let mut sib = node.prev_sibling();

        while let Some(s) = sib {
            match s.kind() {
                "comment" => {
                    let text = self
                        .get_node_text(&s)
                        .trim_start_matches("//")
                        .trim()
                        .to_string();
                    lines.push(text);
                    sib = s.prev_sibling();
                }
                // skip blank lines represented as empty source spans between nodes
                _ => break,
            }
        }

        lines.reverse();
        lines.join(" ")
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
        // In auto_tag_function, replace the goroutine check:
        if calls.iter().any(|c| c.context == "goroutine") {
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
        if name.chars().next().is_some_and(|c| c.is_uppercase()) {
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

        if name.chars().next().is_some_and(|c| c.is_uppercase()) {
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

/// Entry point called from main.rs
pub fn parse_file(path: &Path) -> Result<(String, FileData), String> {
    let source_code = std::fs::read_to_string(path)
        .map_err(|e| format!("Failed to read file {}: {}", path.display(), e))?;

    // Strips leading "./" or ".\" if present, otherwise leaves path as is
    let clean_path = path.strip_prefix("./").unwrap_or(path);
    let path_str = clean_path.to_string_lossy().to_string();

    let parser = GoParser::new(source_code, path_str.clone());
    let file_data = parser.parse()?;

    Ok((path_str, file_data))
}
