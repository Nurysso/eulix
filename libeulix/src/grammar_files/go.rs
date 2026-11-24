// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::utils::{declare_variable, extract_todos, walk};

use crate::struc::kb_struct::*;
use crate::utils::syntax;
use regex::Regex;
use std::collections::{BTreeMap, HashSet};
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
                || trimmed == "\"C\""
                || (trimmed.contains("\"C\"") && trimmed.starts_with("import"))
            {
                uses_cgo = true;
            }
        }

        (build_tags, go_directives, uses_cgo, embed_patterns)
    }

    pub fn parse(&self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(&tree_sitter_go::LANGUAGE.into())
            .map_err(|e| format!("Failed to load Go grammar: {}", e))?;
        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse Go file".to_string())?;
        let root = tree.root_node();
        let mut functions = self.extract_functions(&root);
        let (classes, orphaned_methods) = self.extract_structs(&root);
        functions.extend(orphaned_methods);

        Ok(FileData {
            language: "go".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(&root),
            functions,
            classes,
            global_vars: self.extract_global_vars(&root),
            todos: extract_todos(&self.source_code),
            security_notes: self.detect_security_patterns(),
            syntax: syntax::inspect(&tree),
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
                            let path = self.get_node_text(&path_node).trim_matches('"').to_string();

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

                                    // `alias "a/b"`, `_ "a/b"`, `. "a/b"`
                                    let alias = item
                                        .child_by_field_name("name")
                                        .map(|n| self.get_node_text(&n));

                                    imports.push(Import {
                                        module: path.clone(),
                                        items: alias.into_iter().collect(),
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
                    tags: vec!["external-impl".to_string()],
                    importance_score: 0.5,
                    lang_info: LanguageSpecificInfo::default(),
                });
            }
        };
        let calls = self.extract_function_calls_detailed(&body);
        let variables = self.extract_variables(&body, &params, &self.extract_named_results(node));
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

        let (chan_sends, chan_recvs) = self.extract_chan_ops(&body);
        let local_calls = self.extract_local_calls(&body);
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
            chan_sends,
            chan_recvs,
            local_calls,
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
                go: Some(Box::new(go_info)),
                ..Default::default()
            },
        })
    }

    /// Returns (receiver_type, receiver_name) for a method declaration.
    fn parse_receiver(&self, node: &Node) -> (Option<String>, Option<String>) {
        let receiver = match node.child_by_field_name("receiver") {
            Some(r) => r,
            None => {
                return (None, None);
            }
        };
        let mut rc = receiver.walk();
        for child in receiver.children(&mut rc) {
            if child.kind() == "parameter_declaration" {
                let rtype = child.child_by_field_name("type").map(|t| {
                    let type_text = self.get_node_text(&t);
                    type_text.trim_start_matches('*').to_string()
                });
                let rname = child
                    .child_by_field_name("name")
                    .map(|n| self.get_node_text(&n));
                return (rtype, rname);
            }
        }
        (None, None)
    }

    /// True if any descendant node has the given kind.
    fn body_contains_kind(&self, node: &Node, kind: &str) -> bool {
        let mut found = false;
        walk(*node, (), |node, _| {
            if found {
                return None;
            }
            let node = &node;
            if node.kind() == kind {
                found = true;
                return None;
            }
            Some(())
        });
        found
    }

    /// True if the body sends to or receives from a channel (`<-` operator).
    /// Sends and receives on field channels (`x.f <- v`, `<-x.f`,
    /// `range x.f`), including those inside closures and goroutines started
    /// here: they belong to the function that starts them. A `range` over a
    /// field is recorded even though it may be a slice or map; it only becomes
    /// an edge when some function also sends on a field of that name.
    fn extract_chan_ops(&self, body: &Node) -> (Vec<ChanOp>, Vec<ChanOp>) {
        let (mut sends, mut recvs) = (Vec::new(), Vec::new());
        walk(*body, (), |node, _| {
            let line = node.start_position().row + 1;
            match node.kind() {
                "send_statement" => {
                    if let Some(f) = node
                        .child_by_field_name("channel")
                        .and_then(|c| self.selector_field(&c))
                    {
                        sends.push(ChanOp { field: f, line });
                    }
                }
                "unary_expression" => {
                    let op = node
                        .child_by_field_name("operator")
                        .or_else(|| node.child(0));
                    if op.is_some_and(|o| self.get_node_text(&o) == "<-") {
                        if let Some(f) = node
                            .child_by_field_name("operand")
                            .and_then(|c| self.selector_field(&c))
                        {
                            recvs.push(ChanOp { field: f, line });
                        }
                    }
                }
                "range_clause" => {
                    if let Some(f) = node
                        .child_by_field_name("right")
                        .and_then(|c| self.selector_field(&c))
                    {
                        recvs.push(ChanOp { field: f, line });
                    }
                }
                _ => {}
            }
            Some(())
        });
        for v in [&mut sends, &mut recvs] {
            v.sort_by(|a, b| (&a.field, a.line).cmp(&(&b.field, b.line)));
            v.dedup_by(|a, b| a.field == b.field);
        }
        (sends, recvs)
    }

    /// Locals declared without a type from a call (`p := b.getPipe()`,
    /// `p, err := newPipe()`, `var p = newPipe()`), for the call graph to
    /// type from the callee's declared result. A name declared more than
    /// once in different ways is left out: which declaration a later call
    /// sees depends on scopes the call graph does not track.
    fn extract_local_calls(&self, body: &Node) -> Vec<LocalCall> {
        // name -> the one call it is declared from; None once declared another way.
        let mut seen: BTreeMap<String, Option<LocalCall>> = BTreeMap::new();
        let mut declare = |name: String, call: Option<LocalCall>| {
            if name == "_" {
                return;
            }
            match seen.get(&name) {
                None => {
                    seen.insert(name, call);
                }
                Some(prev) if *prev != call => {
                    seen.insert(name, None);
                }
                Some(_) => {}
            }
        };
        walk(*body, (), |node, _| {
            let (names, values) = match node.kind() {
                "short_var_declaration" => {
                    let Some(left) = node.child_by_field_name("left") else {
                        return Some(());
                    };
                    let names: Vec<String> = self
                        .identifier_children(&left)
                        .iter()
                        .map(|n| self.get_node_text(n))
                        .collect();
                    (names, node.child_by_field_name("right"))
                }
                "var_spec" if node.child_by_field_name("type").is_none() => (
                    self.collect_spec_names(&node),
                    node.child_by_field_name("value"),
                ),
                // Declared from something other than a call.
                "range_clause" | "type_switch_statement" | "receive_statement" | "func_literal" => {
                    let mut names = Vec::new();
                    for field in ["left", "alias"] {
                        if let Some(left) = node.child_by_field_name(field) {
                            if left.kind() == "identifier" {
                                names.push(self.get_node_text(&left));
                            } else {
                                names.extend(
                                    self.identifier_children(&left)
                                        .iter()
                                        .map(|n| self.get_node_text(n)),
                                );
                            }
                        }
                    }
                    for p in self
                        .extract_parameters(&node)
                        .into_iter()
                        .filter(|_| node.kind() == "func_literal")
                    {
                        names.push(p.name);
                    }
                    for name in names {
                        declare(name, None);
                    }
                    return Some(());
                }
                _ => return Some(()),
            };
            let exprs: Vec<Node> = match values {
                Some(v) if v.kind() == "expression_list" => {
                    let mut cursor = v.walk();
                    v.named_children(&mut cursor).collect()
                }
                Some(v) => vec![v],
                None => Vec::new(),
            };
            let count = names.len();
            for (i, name) in names.into_iter().enumerate() {
                // `a, b := f()` takes the results of one call in order;
                // `a, b := f(), g()` pairs names with expressions.
                let (expr, index) = if exprs.len() == 1 {
                    (exprs.first(), i)
                } else if exprs.len() == count {
                    (exprs.get(i), 0)
                } else {
                    (None, 0)
                };
                let call = expr.and_then(|e| self.local_call(e, &name, index));
                declare(name, call);
            }
            Some(())
        });
        seen.into_values().flatten().collect()
    }

    /// `receiver.callee(...)` or `callee(...)` as the `index`-th result
    /// assigned to `name`. `new(T)` and conversions spell their type and are
    /// typed by the variable table instead.
    fn local_call(&self, expr: &Node, name: &str, index: usize) -> Option<LocalCall> {
        if expr.kind() != "call_expression" || self.literal_type(expr).is_some() {
            return None;
        }
        let function = expr.child_by_field_name("function")?;
        let (receiver, callee) = match function.kind() {
            "identifier" => (None, self.get_node_text(&function)),
            "selector_expression" => (
                Some(self.get_node_text(&function.child_by_field_name("operand")?)),
                self.get_node_text(&function.child_by_field_name("field")?),
            ),
            _ => return None,
        };
        Some(LocalCall {
            name: name.to_string(),
            receiver,
            callee,
            index,
        })
    }

    /// The field name of a selector (`pq.queue` → "queue"), through parens.
    fn selector_field(&self, node: &Node) -> Option<String> {
        match node.kind() {
            "selector_expression" => node
                .child_by_field_name("field")
                .map(|f| self.get_node_text(&f).to_string()),
            "parenthesized_expression" => node.named_child(0).and_then(|c| self.selector_field(&c)),
            _ => None,
        }
    }

    fn body_contains_channel_ops(&self, node: &Node) -> bool {
        let mut found = false;
        walk(*node, (), |node, _| {
            if found {
                return None;
            }
            let node = &node;
            if node.kind() == "send_statement" || node.kind() == "receive_statement" {
                found = true;
                return None;
            }
            // Also catch `<-` used in expressions
            if node.kind() == "unary_expression" {
                let op = node.child(0).map(|c| self.get_node_text(&c));
                if op.as_deref() == Some("<-") {
                    found = true;
                    return None;
                }
            }
            Some(())
        });
        found
    }

    /// Counts direct descendant nodes of `kind` (non-recursive, one level for
    /// statements like defer that appear at statement-list level).
    fn count_kind_in_body(&self, node: &Node, kind: &str) -> usize {
        let mut count = 0;
        walk(*node, (), |child, _| {
            if child != *node && child.kind() == kind {
                count += 1;
                None
            } else {
                Some(())
            }
        });
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

    /// `(n int, err error)` after the parameters: results that have names.
    fn extract_named_results(&self, node: &Node) -> Vec<Parameter> {
        let Some(result) = node
            .child_by_field_name("result")
            .filter(|r| r.kind() == "parameter_list")
        else {
            return Vec::new();
        };
        let mut results = Vec::new();
        let mut cursor = result.walk();
        for child in result.children(&mut cursor) {
            if child.kind() != "parameter_declaration" {
                continue;
            }
            let type_annotation = child
                .child_by_field_name("type")
                .map(|t| self.get_node_text(&t))
                .unwrap_or_default();
            for name in self.identifier_children(&child) {
                results.push(Parameter {
                    name: self.get_node_text(&name),
                    type_annotation: type_annotation.clone(),
                    default_value: None,
                });
            }
        }
        results
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
        walk(*node, context, |node, context| {
            let node = &node;
            let current_context = match node.kind() {
                "if_statement" => "if",
                "for_statement" => "loop",
                "expression_switch_statement" | "type_switch_statement" => "switch",
                "select_statement" => "select",
                _ => context,
            };
            match node.kind() {
                // ── goroutine: extract inner call, mark as "goroutine", skip subtree
                "go_statement" => Some("goroutine"), // don't recurse further — the call is already captured
                // ── defer: same treatment
                "defer_statement" => Some("defer"),
                // ── normal call expression
                "call_expression" => {
                    self.push_call(node, calls, seen, current_context);
                    // still recurse: the arguments may contain nested calls
                    Some(current_context)
                }
                _ => Some(current_context),
            }
        });
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
            if func_node.kind() == "func_literal" {
                return;
            }
            // `f[T](x)` and `pkg.F[K, V](x)` call `f` and `pkg.F`: the type
            // arguments are not part of the name.
            let named = match func_node.kind() {
                "index_expression" => func_node.child_by_field_name("operand"),
                "type_instantiation_expression" => func_node.child_by_field_name("type"),
                _ => None,
            }
            .unwrap_or(func_node);
            let full = self.get_node_text(&named);
            // Use the last segment so "fmt.Println" → "Println"; what stands
            // before it ("fmt", "m", "c.engine") is the call's receiver.
            let callee = full
                .split('.')
                .next_back()
                .unwrap_or(&full)
                .trim()
                .to_string();

            if callee.is_empty() {
                return;
            }

            let key = format!(
                "{}:{}:{}",
                callee,
                call_node.start_position().row,
                call_node.start_position().column
            );
            if seen.insert(key) {
                let qualifier = if full.contains('.') {
                    let parts: Vec<&str> = full.rsplitn(2, '.').collect();
                    Some(parts[1].to_string()) // e.g. "fmt"
                } else {
                    None
                };

                calls.push(FunctionCall {
                    receiver: qualifier,
                    callee,
                    defined_in: None,
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

    fn extract_variables(
        &self,
        node: &Node,
        params: &[Parameter],
        results: &[Parameter],
    ) -> Vec<Variable> {
        let mut variables: BTreeMap<String, Variable> = BTreeMap::new();

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
        // Named results are variables of the body too.
        for result in results.iter().filter(|r| r.name != "_") {
            declare_variable(
                &mut variables,
                result.name.clone(),
                Some(result.type_annotation.clone()),
                "local",
                None,
            );
        }

        self.track_variable_usage(node, &mut variables);
        variables.into_values().collect()
    }

    // identifier_childer returns each direct identifier child. This is how
    // multi-name declarations (`a, b := f()`) are split
    // into individual names instead of being treated as one blob "a, b".
    fn identifier_children<'a>(&self, node: &Node<'a>) -> Vec<Node<'a>> {
        let mut cursor = node.walk();
        node.children(&mut cursor)
            .filter(|c| c.kind() == "identifier")
            .collect()
    }

    /// The type an initializer spells out: `T{...}`, `&T{...}`, `new(T)`.
    /// Anything else (a call, an arithmetic expression) has a type only the
    /// type checker knows.
    fn literal_type(&self, value: &Node) -> Option<String> {
        match value.kind() {
            "composite_literal" => value
                .child_by_field_name("type")
                .map(|t| self.get_node_text(&t)),
            "unary_expression" => {
                let operand = value.child_by_field_name("operand")?;
                let address = value
                    .child_by_field_name("operator")
                    .is_some_and(|op| op.kind() == "&");
                (address && operand.kind() == "composite_literal")
                    .then(|| self.literal_type(&operand))
                    .flatten()
                    .map(|t| format!("*{t}"))
            }
            "call_expression" => {
                let function = value.child_by_field_name("function")?;
                if function.kind() != "identifier" || self.get_node_text(&function) != "new" {
                    return None;
                }
                let args = value.child_by_field_name("arguments")?;
                let mut cursor = args.walk();
                let mut named = args.named_children(&mut cursor);
                let ty = named.next()?;
                named
                    .next()
                    .is_none()
                    .then(|| format!("*{}", self.get_node_text(&ty)))
            }
            _ => None,
        }
    }

    /// One literal type per declared name, when the two sides of a
    /// declaration pair up (`a, b := T{}, f()`); otherwise all unknown.
    fn initializer_types(&self, names: usize, values: Option<Node>) -> Vec<Option<String>> {
        let Some(values) = values else {
            return vec![None; names];
        };
        let exprs: Vec<Node> = if values.kind() == "expression_list" {
            let mut cursor = values.walk();
            values.named_children(&mut cursor).collect()
        } else {
            vec![values]
        };
        if exprs.len() != names {
            return vec![None; names];
        }
        exprs.iter().map(|e| self.literal_type(e)).collect()
    }

    /// Declares the names on the left of `:=` in a range clause, a type
    /// switch or a `select` case. Their types come from the right-hand side
    /// and are not visible in the syntax.
    fn declare_untyped(&self, left: &Node, variables: &mut BTreeMap<String, Variable>) {
        let line = left.start_position().row + 1;
        let names = if left.kind() == "identifier" {
            vec![*left]
        } else {
            self.identifier_children(left)
        };
        for name_node in names {
            let name = self.get_node_text(&name_node);
            if name != "_" {
                declare_variable(variables, name, None, "local", Some(line));
            }
        }
    }

    fn track_variable_usage(&self, node: &Node, variables: &mut BTreeMap<String, Variable>) {
        walk(*node, (), |node, _| {
            let node = &node;
            match node.kind() {
                "short_var_declaration" => {
                    if let Some(left) = node.child_by_field_name("left") {
                        let line = node.start_position().row + 1;
                        let names = self.identifier_children(&left);
                        let types =
                            self.initializer_types(names.len(), node.child_by_field_name("right"));
                        for (name_node, var_type) in names.iter().zip(types) {
                            let var_name = self.get_node_text(name_node);
                            if var_name == "_" {
                                continue;
                            }
                            declare_variable(variables, var_name, var_type, "local", Some(line));
                        }
                    }
                }
                "var_declaration" | "const_declaration" => {
                    let scope = if node.kind() == "const_declaration" {
                        "const"
                    } else {
                        "local"
                    };
                    let mut specs = Vec::new();
                    let mut decl_cursor = node.walk();
                    for child in node.children(&mut decl_cursor) {
                        match child.kind() {
                            "var_spec" | "const_spec" => specs.push(child),
                            // `var ( a = 1; b T )`
                            "var_spec_list" => {
                                let mut list_cursor = child.walk();
                                specs.extend(
                                    child
                                        .children(&mut list_cursor)
                                        .filter(|s| s.kind() == "var_spec"),
                                );
                            }
                            _ => {}
                        }
                    }
                    for spec in specs {
                        let line = spec.start_position().row + 1;
                        let declared = spec
                            .child_by_field_name("type")
                            .map(|t| self.get_node_text(&t));
                        let names = self.collect_spec_names(&spec);
                        let types = match declared {
                            Some(t) => vec![Some(t); names.len()],
                            None => self
                                .initializer_types(names.len(), spec.child_by_field_name("value")),
                        };
                        for (var_name, var_type) in names.into_iter().zip(types) {
                            if var_name == "_" {
                                continue;
                            }
                            declare_variable(variables, var_name, var_type, scope, Some(line));
                        }
                    }
                }
                // `for k, v := range xs`
                "range_clause" => {
                    let mut cursor = node.walk();
                    let declares = node.children(&mut cursor).any(|c| c.kind() == ":=");
                    if let Some(left) = node.child_by_field_name("left").filter(|_| declares) {
                        self.declare_untyped(&left, variables);
                    }
                }
                // `switch v := x.(type)`
                "type_switch_statement" => {
                    if let Some(alias) = node.child_by_field_name("alias") {
                        self.declare_untyped(&alias, variables);
                    }
                }
                // `case v := <-ch:`
                "receive_statement" => {
                    let mut cursor = node.walk();
                    let declares = node.children(&mut cursor).any(|c| c.kind() == ":=");
                    if let Some(left) = node.child_by_field_name("left").filter(|_| declares) {
                        self.declare_untyped(&left, variables);
                    }
                }
                // A closure's parameters and named results are in scope for
                // the calls in its body, which belong to this function.
                "func_literal" => {
                    let line = node.start_position().row + 1;
                    let declared = self
                        .extract_parameters(node)
                        .into_iter()
                        .chain(self.extract_named_results(node));
                    for p in declared.filter(|p| p.name != "_") {
                        let var_type = Some(p.type_annotation).filter(|t| !t.is_empty());
                        declare_variable(variables, p.name, var_type, "local", Some(line));
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
            Some(())
        });
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
        walk(*node, (), |node, _| {
            let node = &node;

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
            Some(())
        });
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
        walk(*node, (), |node, _| {
            let node = &node;

            if node.kind() == "call_expression" {
                if let Some(func_node) = node.child_by_field_name("function") {
                    let name = self.get_node_text(&func_node);
                    if !seen.contains(&name) {
                        seen.insert(name.clone());
                        calls.push(name);
                    }
                }
            }
            Some(())
        });
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
        walk(*node, (), |node, _| {
            let node = &node;

            if node.kind() == "call_expression" {
                if let Some(func_node) = node.child_by_field_name("function") {
                    let func_name = self.get_node_text(&func_node);
                    if func_name == "panic" {
                        info.raises.push("panic".to_string());
                    }
                }
            }
            Some(())
        });
    }

    fn extract_structs(&self, root: &Node) -> (Vec<Class>, Vec<Function>) {
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
        let mut methods_map: BTreeMap<String, Vec<Function>> = BTreeMap::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "method_declaration" {
                // Try to extract the receiver type
                let (receiver_type, _) = self.parse_receiver(&child);
                if let Some(type_name) = receiver_type {
                    // Parse this as a method of the receiver type
                    if let Some(method) = self.parse_function(&child, &type_name) {
                        methods_map.entry(type_name).or_default().push(method);
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

        let orphaned_methods: Vec<Function> = methods_map.into_values().flatten().collect();
        (structs, orphaned_methods)
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
        // An embedded type lends its methods to the struct, which is what a
        // base class does for method lookup.
        let bases = attributes
            .iter()
            .filter(|a| a.name.is_empty() && !a.type_annotation.is_empty())
            .map(|a| a.type_annotation.clone())
            .collect();

        Some(Class {
            id: self.make_struct_id(&name),
            name: name.clone(),
            bases,
            docstring,
            line_start,
            line_end,
            methods: vec![],
            attributes,
            decorators: vec![],
            lang_info: LanguageSpecificInfo {
                go: Some(Box::new(GoInfo {
                    is_exported: name.chars().next().is_some_and(|c| c.is_uppercase()),
                    type_kind: Some(GoTypeKind::Struct),
                    has_embedded_types: has_embedded,
                    build_tags: self.build_tags.clone(),
                    uses_cgo: self.uses_cgo,
                    ..Default::default()
                })),
                ..Default::default()
            },
        })
    }

    fn extract_struct_fields(&self, struct_node: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();

        // The field list is an unnamed child of `struct_type`.
        let mut struct_cursor = struct_node.walk();
        let body = struct_node
            .children(&mut struct_cursor)
            .find(|c| c.kind() == "field_declaration_list");
        if let Some(body) = body {
            let mut cursor = body.walk();
            for child in body.children(&mut cursor) {
                if child.kind() != "field_declaration" {
                    continue;
                }
                let type_text = child
                    .child_by_field_name("type")
                    .map(|t| self.get_node_text(&t))
                    .unwrap_or_default();
                let mut name_cursor = child.walk();
                let names: Vec<String> = child
                    .children_by_field_name("name", &mut name_cursor)
                    .map(|n| self.get_node_text(&n))
                    .collect();
                if names.is_empty() {
                    // Embedded type: struct { io.Reader } — no name node
                    if !type_text.is_empty() {
                        fields.push(Attribute {
                            name: String::new(), // signals embedded to has_embedded check
                            type_annotation: type_text.trim_start_matches('*').to_string(),
                            value: None,
                        });
                    }
                    continue;
                }
                let tag = child
                    .child_by_field_name("tag")
                    .map(|t| self.get_node_text(&t));
                // `a, b int` declares two fields of one type.
                for name in names {
                    fields.push(Attribute {
                        name,
                        type_annotation: type_text.clone(),
                        value: tag.clone(),
                    });
                }
            }
        }

        fields
    }

    /// Collects LHS node. Stops at `=` so RHS identifiers (e.g. `var x = y`) are not picked
    /// up. so anonymous tokens such as `,` are never returned.
    fn collect_spec_names(&self, spec: &Node) -> Vec<String> {
        let mut names = Vec::new();
        let mut cursor = spec.walk();
        for child in spec.children(&mut cursor) {
            match child.kind() {
                "=" => break,
                "identifier" => names.push(self.get_node_text(&child)),
                _ => {}
            }
        }
        names
    }

    fn extract_global_vars(&self, root: &Node) -> Vec<GlobalVar> {
        let mut vars = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "var_declaration" || child.kind() == "const_declaration" {
                let mut decl_cursor = child.walk();
                for spec in child.children(&mut decl_cursor) {
                    if spec.kind() == "var_spec" || spec.kind() == "const_spec" {
                        vars.extend(self.parse_global_var_spec(&spec));
                    }
                }
            }
        }
        vars
    }

    fn parse_global_var_spec(&self, node: &Node) -> Vec<GlobalVar> {
        let line = node.start_position().row + 1;
        let type_annotation = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_default();

        let names = self.collect_spec_names(node);
        if names.is_empty() {
            return vec![];
        }

        let values = self.extract_var_spec_values(node);
        names
            .into_iter()
            .enumerate()
            .map(|(i, name)| GlobalVar {
                name,
                type_annotation: type_annotation.clone(),
                value: values.get(i).cloned(),
                line,
            })
            .collect()
    }

    fn extract_var_spec_values(&self, spec: &Node) -> Vec<String> {
        let mut values: Vec<String> = Vec::new();

        // Check if tree-sitter wrapped the RHS in an `expression_list`
        if let Some(val_node) = spec.child_by_field_name("value") {
            if val_node.kind() == "expression_list" {
                let mut vc = val_node.walk();
                for child in val_node.children(&mut vc) {
                    if child.is_named() {
                        values.push(self.get_node_text(&child));
                    }
                }
                return values;
            }
        }

        // Otherwise, collect all named child nodes that appear after the '=' operator
        let mut cursor = spec.walk();
        let mut found_equals = false;

        for child in spec.children(&mut cursor) {
            if child.kind() == "=" {
                found_equals = true;
                continue;
            }
            if found_equals && child.is_named() {
                values.push(self.get_node_text(&child));
            }
        }

        values
    }

    fn parse_interface(&self, node: &Node) -> Option<Class> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);
        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        let interface = node
            .child_by_field_name("type")
            .filter(|t| t.kind() == "interface_type");
        let methods = interface
            .as_ref()
            .map(|t| self.extract_interface_methods(t))
            .unwrap_or_default();
        // An embedded interface (`io.Reader` inside `ReadWriter`); a union
        // (`~int | ~string`) is a constraint, not an embedding.
        let mut bases = Vec::new();
        if let Some(interface) = &interface {
            let mut cursor = interface.walk();
            for elem in interface.children(&mut cursor) {
                if elem.kind() != "type_elem" || elem.named_child_count() != 1 {
                    continue;
                }
                if let Some(ty) = elem.named_child(0).filter(|t| {
                    matches!(
                        t.kind(),
                        "type_identifier" | "qualified_type" | "generic_type"
                    )
                }) {
                    bases.push(self.get_node_text(&ty));
                }
            }
        }

        Some(Class {
            id: self.make_interface_id(&name),
            name: name.clone(),
            bases,
            docstring,
            line_start,
            line_end,
            methods,
            attributes: vec![],
            decorators: vec!["interface".to_string()], // reuse decorators to signal kind
            lang_info: LanguageSpecificInfo {
                go: Some(Box::new(GoInfo {
                    is_exported: name.chars().next().is_some_and(|c| c.is_uppercase()),
                    type_kind: Some(GoTypeKind::Interface),
                    build_tags: self.build_tags.clone(),
                    uses_cgo: self.uses_cgo,
                    ..Default::default()
                })),
                ..Default::default()
            },
        })
    }

    fn extract_interface_methods(&self, interface_node: &Node) -> Vec<Function> {
        let mut methods = Vec::new();

        let mut cursor = interface_node.walk();
        for child in interface_node.children(&mut cursor) {
            if child.kind() == "method_elem" || child.kind() == "method_spec" {
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
                            go: Some(Box::new(GoInfo {
                                is_exported: name.chars().next().is_some_and(|c| c.is_uppercase()),
                                is_interface_method: true,
                                returns_error: return_type.contains("error"),
                                is_variadic: params
                                    .last()
                                    .is_some_and(|p| p.type_annotation.starts_with("...")),
                                build_tags: self.build_tags.clone(),
                                uses_cgo: self.uses_cgo,
                                ..Default::default()
                            })),
                            ..Default::default()
                        },
                    });
                }
            }
        }

        methods
    }

    fn extract_docstring(&self, node: &Node) -> String {
        // Walk backwards through siblings looking for comment(s)
        let mut lines: Vec<String> = Vec::new();
        let mut expected_end_row = node.start_position().row;
        let mut sib = node.prev_sibling();

        while let Some(s) = sib {
            if s.kind() != "comment" {
                break;
            }
            if s.end_position().row + 1 != expected_end_row {
                break; // blank-line gap: this comment isn't attached to node
            }
            let text_raw = self.get_node_text(&s);
            expected_end_row = s.start_position().row;
            if !text_raw.trim().starts_with("//go:") {
                lines.push(text_raw.trim_start_matches("//").trim().to_string());
            }
            sib = s.prev_sibling();
        }

        lines.reverse();
        lines.join(" ")
    }

    fn calculate_complexity(&self, node: &Node) -> usize {
        let mut complexity = 1;
        walk(*node, (), |node, _| {
            let node = &node;
            match node.kind() {
                "if_statement"
                | "for_statement"
                | "expression_switch_statement"
                | "type_switch_statement"
                | "select_statement" => {
                    complexity += 1;
                }
                "binary_expression" => {
                    if let Some(op) = node.child_by_field_name("operator") {
                        let op_text = self.get_node_text(&op);
                        if op_text == "&&" || op_text == "||" {
                            complexity += 1;
                        }
                    }
                }
                _ => {}
            }
            Some(())
        });
        complexity
    }

    fn detect_security_patterns(&self) -> Vec<SecurityNote> {
        let mut notes = Vec::new();

        let patterns = vec![
            (
                r"(?i)password|secret|token|apikey",
                "sensitive_data",
                "Handles sensitive data",
            ),
            (r"(?i)eval\(", "code_execution", "Dynamic code execution"),
            (
                r"(?i)exec\.Command|os\.Exec",
                "command_execution",
                "System command execution",
            ),
            (r"(?i)unsafe\.", "unsafe_code", "Uses unsafe operations"),
            (
                r"(?i)sql\.Query|db\.Query",
                "sql_query",
                "Database query - check for SQL injection",
            ),
        ];

        for (pattern, note_type, description) in patterns {
            if let Ok(re) = Regex::new(pattern) {
                for (idx, line) in self.source_code.lines().enumerate() {
                    let line_bytes = line;
                    if re.is_match(line_bytes) {
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
        score.clamp(0.0, 1.0)
    }

    fn get_node_text(&self, node: &Node) -> String {
        node.utf8_text(self.source_code.as_bytes())
            .unwrap_or("")
            .to_string()
    }
}

pub fn parse_source(path: &Path, source_code: &str) -> Result<(String, FileData), String> {
    // Strips leading "./" or ".\" if present, otherwise leaves path as is
    let clean_path = path.strip_prefix("./").unwrap_or(path);
    let path_str = clean_path.to_string_lossy().to_string();

    let parser = GoParser::new(source_code.to_string(), path_str.clone());
    let file_data = parser.parse()?;

    Ok((path_str, file_data))
}

pub fn parse_file(path: &Path) -> Result<(String, FileData), String> {
    let source_code = std::fs::read_to_string(path)
        .map_err(|e| format!("Failed to read file {}: {}", path.display(), e))?;
    parse_source(path, &source_code)
}

#[cfg(test)]
mod tests {
    #![expect(clippy::expect_used)]
    #![expect(clippy::unwrap_used)]

    #[test]
    fn deep_expression_on_small_stack() {
        // A left-associative expression must not grow the Rust call stack.
        let expression = vec!["x"; 10_000].join(" + ");
        let source = format!(
            "package main\nfunc deep(x int) int {{ return {} }}",
            expression
        );
        std::thread::Builder::new()
            .stack_size(2 * 1024 * 1024)
            .spawn(move || {
                let (_, data) =
                    crate::grammar_files::parse_source(std::path::Path::new("deep.go"), &source)
                        .expect("deep expression parses");
                assert!(data.syntax.is_clean(), "deep expression is valid syntax");
                assert!(
                    data.functions
                        .iter()
                        .chain(data.classes.iter().flat_map(|c| &c.methods))
                        .any(|f| f.name == "deep"),
                    "deep function extracted"
                );
            })
            .expect("spawn parser thread")
            .join()
            .expect("parser thread completes on a 2 MiB stack");
    }

    use super::*;

    fn parse(src: &str) -> FileData {
        GoParser::new(src.to_string(), "test.go".to_string())
            .parse()
            .expect("parse should succeed")
    }

    fn find_fn<'a>(fd: &'a FileData, name: &str) -> &'a Function {
        #[allow(clippy::panic)]
        fd.functions
            .iter()
            .find(|f| f.name == name)
            .unwrap_or_else(|| panic!("function `{}` not found", name))
    }

    fn var_type<'a>(f: &'a Function, name: &str) -> Option<&'a str> {
        #[allow(clippy::panic)]
        f.variables
            .iter()
            .find(|v| v.name == name)
            .unwrap_or_else(|| panic!("variable `{}` not found", name))
            .var_type
            .as_deref()
    }

    // PRISM-5: the receiver has its own field; `defined_in` is a file path
    // that only the call-graph pass fills.
    #[test]
    fn call_receiver_is_stored_apart_from_the_callee() {
        let fd = parse(
            "package p\nfunc run(m *Store) {\n\tm.Save()\n\tfmt.Println(1)\n\tc.engine.Reset()\n\thelper()\n}\n",
        );
        let calls: Vec<(&str, Option<&str>, Option<&str>)> = find_fn(&fd, "run")
            .calls
            .iter()
            .map(|c| {
                (
                    c.callee.as_str(),
                    c.receiver.as_deref(),
                    c.defined_in.as_deref(),
                )
            })
            .collect();
        assert_eq!(
            calls,
            [
                ("Save", Some("m"), None),
                ("Println", Some("fmt"), None),
                ("Reset", Some("c.engine"), None),
                ("helper", None, None),
            ]
        );
    }

    // The field list is not a `body` field of `struct_type`; read as one,
    // every struct came out without fields.
    #[test]
    fn struct_fields_are_extracted() {
        let fd = parse(
            "package p\ntype Ctx struct {\n\tengine *Engine `json:\"e\"`\n\ta, b int\n\tBase\n}\n",
        );
        let fields: Vec<(&str, &str)> = fd.classes[0]
            .attributes
            .iter()
            .map(|a| (a.name.as_str(), a.type_annotation.as_str()))
            .collect();
        assert_eq!(
            fields,
            [
                ("engine", "*Engine"),
                ("a", "int"),
                ("b", "int"),
                ("", "Base")
            ]
        );
    }

    // `f[T](x)` calls `f`; the type arguments are not part of its name.
    #[test]
    fn generic_call_is_recorded_under_the_function_name() {
        // With one argument the grammar reads `f[T](x)` as a conversion to
        // the generic type `f[T]`, which is not recorded as a call.
        let fd = parse(
            "package p\nfunc run() {\n\tgetTyped[string](c, k)\n\tconv.Make[int, string](c, k)\n\tone[string](k)\n}\n",
        );
        let calls: Vec<(&str, Option<&str>)> = find_fn(&fd, "run")
            .calls
            .iter()
            .map(|c| (c.callee.as_str(), c.receiver.as_deref()))
            .collect();
        assert_eq!(calls, [("getTyped", None), ("Make", Some("conv"))]);
    }

    #[test]
    fn embedded_types_are_bases() {
        let fd = parse(
            "package p\ntype Engine struct {\n\tGroup\n\t*sync.Mutex\n\tname string\n}\ntype RW interface {\n\tReader\n\tio.Writer\n\tClose() error\n}\n",
        );
        let bases = |name: &str| &fd.classes.iter().find(|c| c.name == name).unwrap().bases;
        assert_eq!(bases("Engine"), &["Group", "sync.Mutex"]);
        assert_eq!(bases("RW"), &["Reader", "io.Writer"]);
    }

    #[test]
    fn local_takes_the_type_its_initializer_spells_out() {
        let fd = parse(
            "package p\nfunc run() {\n\ta := Store{}\n\tb := &Store{}\n\tc := new(db.Store)\n\td, e := Store{}, load()\n\tvar f = &Store{}\n\tg := []Store{}\n}\n",
        );
        let f = find_fn(&fd, "run");
        assert_eq!(var_type(f, "a"), Some("Store"));
        assert_eq!(var_type(f, "b"), Some("*Store"));
        assert_eq!(var_type(f, "c"), Some("*db.Store"));
        assert_eq!(var_type(f, "d"), Some("Store"));
        assert_eq!(var_type(f, "e"), None);
        assert_eq!(var_type(f, "f"), Some("*Store"));
        assert_eq!(var_type(f, "g"), Some("[]Store"));
    }

    // Every way a name can be declared in a body is recorded, so that a
    // name with two types is seen as such.
    #[test]
    fn every_declaration_form_is_tracked_and_a_second_type_drops_the_type() {
        let fd = parse(
            "package p\nfunc run(m *Store, keep *Store, items []Other) (out *Store, err error) {\n\tfor _, m := range items {\n\t\t_ = m\n\t}\n\tgo func(keep *Store, extra Other) {}(nil, Other{})\n\tswitch v := x.(type) {\n\tdefault:\n\t\t_ = v\n\t}\n\tselect {\n\tcase got := <-ch:\n\t\t_ = got\n\t}\n\treturn\n}\n",
        );
        let f = find_fn(&fd, "run");
        assert_eq!(var_type(f, "m"), None, "param, then a range variable");
        assert_eq!(var_type(f, "keep"), Some("*Store"), "same type twice");
        assert_eq!(var_type(f, "extra"), Some("Other"));
        assert_eq!(var_type(f, "out"), Some("*Store"));
        assert_eq!(var_type(f, "err"), Some("error"));
        assert_eq!(var_type(f, "v"), None);
        assert_eq!(var_type(f, "got"), None);
    }

    // Calls inside a goroutine/defer body must be found,
    // and the outer call must not record the closure source as its callee.
    #[test]
    fn goroutine_and_defer_anonymous_func_calls_are_found() {
        let src = r#"
package main

func Worker() {
    go func() {
        step1()
        step2()
    }()

    defer func() {
        cleanupStep()
    }()
}

func step1() {}
func step2() {}
func cleanupStep() {}
"#;
        let fd = parse(src);
        let worker = find_fn(&fd, "Worker");
        let callees: Vec<&str> = worker.calls.iter().map(|c| c.callee.as_str()).collect();
        assert!(callees.contains(&"step1"), "calls: {:?}", callees);
        assert!(callees.contains(&"step2"), "calls: {:?}", callees);
        assert!(callees.contains(&"cleanupStep"), "calls: {:?}", callees);
        // no call should have carried the whole closure text as its callee
        assert!(callees.iter().all(|c| !c.contains('{')));
        // contexts should reflect goroutine/defer, not "unconditional"
        let step1_call = worker.calls.iter().find(|c| c.callee == "step1").unwrap();
        assert_eq!(step1_call.context, "goroutine");
        let cleanup_call = worker
            .calls
            .iter()
            .find(|c| c.callee == "cleanupStep")
            .unwrap();
        assert_eq!(cleanup_call.context, "defer");
    }

    // `var y int` (no `:=` anywhere) must be tracked as a variable.
    #[test]
    fn local_var_declaration_is_tracked() {
        let src = r#"
package main

func F() {
    var y int
    _ = y
}
"#;
        let fd = parse(src);
        let f = find_fn(&fd, "F");
        assert!(
            f.variables
                .iter()
                .any(|v| v.name == "y" && v.scope == "local"),
            "variables: {:?}",
            f.variables.iter().map(|v| &v.name).collect::<Vec<_>>()
        );
    }

    // `a, b := computeTwo()` must produce two separate variables,
    // not one variable literally named "a, b".
    #[test]
    fn multi_name_short_declaration_is_split() {
        let src = r#"
package main

func F() {
    a, b := computeTwo()
    _ = a
    _ = b
}

func computeTwo() (int, int) { return 1, 2 }
"#;
        let fd = parse(src);
        let f = find_fn(&fd, "F");
        let names: Vec<&str> = f.variables.iter().map(|v| v.name.as_str()).collect();
        assert!(names.contains(&"a"), "names: {:?}", names);
        assert!(names.contains(&"b"), "names: {:?}", names);
        assert!(
            !names.iter().any(|n| n.contains(',')),
            "no combined name expected, got: {:?}",
            names
        );
    }

    // `var Foo, Bar = 1, 2` declared as global vars must produce two
    // GlobalVars, not one with a combined value or 3(including the `,`).
    #[test]
    fn multi_name_local_var_is_split() {
        let src = r#"
package main

func main() {
    var Foo, Bar = 1, 2
    _ = Foo
    _ = Bar
}
"#;
        let fd = parse(src);
        let main_fn = fd.functions.iter().find(|f| f.name == "main").unwrap();
        let names: Vec<&str> = main_fn.variables.iter().map(|v| v.name.as_str()).collect();
        assert!(names.contains(&"Foo"), "names: {:?}", names);
        assert!(names.contains(&"Bar"), "names: {:?}", names);
        assert!(
            !names.contains(&","),
            "comma leaked into names: {:?}",
            names
        );
        assert!(
            !names.contains(&"var"),
            "'var' leaked into names: {:?}",
            names
        );
    }
    #[test]
    fn no_punctuation_leaks_into_local_var_names() {
        let src = r#"
package main

func main() {
    var Foo, Bar = 1, 2
    baz, qux := 3, 4
    _ = Foo; _ = Bar; _ = baz; _ = qux
}
"#;
        let fd = parse(src);
        let main_fn = fd.functions.iter().find(|f| f.name == "main").unwrap();
        for v in &main_fn.variables {
            assert!(
                v.name.chars().all(|c| c.is_alphanumeric() || c == '_'),
                "non-identifier char in variable name: {:?}",
                v.name
            );
            assert!(
                !matches!(v.name.as_str(), "," | "=" | ";" | "(" | ")"),
                "punctuation leaked into variable names: {:?}",
                v.name
            );
        }
    }

    // Security regex must catch idiomatic exported Go names like
    // `Password`, `APIKey` (capitalized), not just lowercase.
    #[test]
    fn security_patterns_are_case_insensitive() {
        let src = r#"
package main

type Config struct {
    Password string
    APIKey   string
}
"#;
        let fd = parse(src);
        assert!(
            fd.security_notes
                .iter()
                .any(|n| n.note_type == "sensitive_data"),
            "security_notes: {:?}",
            fd.security_notes
        );
    }

    // Arithmetic-only code shouldn't be treated as more complex than
    // code with actual branches, and && / || should still count.
    #[test]
    fn complexity_ignores_arithmetic_but_counts_logical_ops() {
        let src = r#"
package main

func Sum(a, b, c, d int) int {
    return a + b + c + d
}

func Branchy(x, y int) int {
    if x > 0 && y > 0 {
        return x
    }
    return y
}
"#;
        let fd = parse(src);
        let sum = find_fn(&fd, "Sum");
        let branchy = find_fn(&fd, "Branchy");
        // Sum has no branches and no &&/||, so complexity should be the
        // baseline (1), not inflated by the `+` chain.
        assert_eq!(sum.complexity, 1, "Sum complexity: {}", sum.complexity);
        // Branchy has one `if` and one `&&`, so complexity should be 3
        // (base 1 + if + &&).
        assert!(
            branchy.complexity > sum.complexity,
            "expected Branchy ({}) > Sum ({})",
            branchy.complexity,
            sum.complexity
        );
    }

    // Type switches must be recognized (both for complexity and for
    // context tagging of calls within their bodies).
    #[test]
    fn type_switch_is_recognized() {
        let src = r#"
package main

func F(v interface{}) {
    switch v.(type) {
    case int:
        handleInt()
    case string:
        handleString()
    }
}

func handleInt() {}
func handleString() {}
"#;
        let fd = parse(src);
        let f = find_fn(&fd, "F");
        assert!(f.complexity > 1, "complexity: {}", f.complexity);
    }

    // Cgo usage via the grouped-import form must be detected.
    #[test]
    fn cgo_detected_in_grouped_import() {
        let src = r#"
package main

import (
    "fmt"
    "C"
)

func F() {
    fmt.Println("hi")
}
"#;
        let fd = parse(src);
        let f = find_fn(&fd, "F");
        let go_info = f.lang_info.go.as_ref().expect("go info present");
        assert!(go_info.uses_cgo, "expected uses_cgo = true");
    }

    // TODO regex should catch lowercase "todo" and TODOs that aren't
    // the very first token in the comment.
    #[test]
    fn todo_regex_is_case_insensitive_and_flexible() {
        let src = r#"
package main

// see TODO below about retries
// todo: fix this later
/* ToDo: handle edge case in block comment */
func F() {}
"#;
        let fd = parse(src);
        assert_eq!(fd.todos.len(), 3, "todos found: {:?}", fd.todos);
    }

    // Docstring must not absorb a comment separated by a blank
    // line, and must not include //go: directive text in the docstring.
    #[test]
    fn docstring_stops_at_blank_line_and_skips_directives() {
        let src = "package main\n\n// Unrelated note, separated by a blank line.\n\n//go:noinline\n// Foo does something.\nfunc Foo() {}\n";
        let fd = parse(src);
        let foo = find_fn(&fd, "Foo");
        assert_eq!(foo.docstring, "Foo does something.");
        assert!(!foo.docstring.contains("go:noinline"));
        assert!(!foo.docstring.contains("Unrelated note"));
    }

    // Body-less top-level func (e.g. an asm stub) should not be
    // tagged as an interface method.
    #[test]
    fn bodyless_func_is_not_tagged_interface_method() {
        let src = r#"
package main

func fastHash(data []byte) uint64
"#;
        let fd = parse(src);
        let f = find_fn(&fd, "fastHash");
        assert!(!f.tags.contains(&"interface-method".to_string()));
    }

    // two distinct calls to the same function on the same source
    // line must both be recorded, not deduplicated into one.
    #[test]
    fn same_line_duplicate_callee_calls_are_both_recorded() {
        let src = r#"
package main

func F(a, b int) bool {
    return validate(a) && validate(b)
}

func validate(x int) bool { return x > 0 }
"#;
        let fd = parse(src);
        let f = find_fn(&fd, "F");
        let validate_calls: Vec<_> = f.calls.iter().filter(|c| c.callee == "validate").collect();
        assert_eq!(
            validate_calls.len(),
            2,
            "expected 2 distinct validate() calls, got: {:?}",
            validate_calls
        );
    }

    // Sanity check for a real interface method, to make sure FIX #11 didn't
    // regress the correct "interface-method" path.
    #[test]
    fn interface_methods_still_tagged_correctly() {
        let src = r#"
package main

type Runner interface {
    Run() error
}
"#;
        let fd = parse(src);
        let runner = fd
            .classes
            .iter()
            .find(|c| c.name == "Runner")
            .expect("Runner interface found");
        let run_method = runner
            .methods
            .iter()
            .find(|m| m.name == "Run")
            .expect("Run method found");
        assert!(run_method.tags.contains(&"interface-method".to_string()));
    }
}
