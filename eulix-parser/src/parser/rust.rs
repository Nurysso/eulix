//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::struc::kb_struct::*;
// use once_cell::sync::LazyLock;
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

static UNSAFE_BLOCK_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"unsafe\s*\{").expect("Invalid unsafe block regex"));

static TRANSMUTE_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"std::mem::transmute").expect("Invalid transmute regex"));

static UNWRAP_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"unwrap\(\)").expect("Invalid unwrap regex"));

static EXPECT_RE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"expect\(").expect("Invalid expect regex"));

static COMMAND_EXEC_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"Command::new|std::process::Command").expect("Invalid command_exec regex")
});

static RAW_POINTER_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"from_raw_parts|from_raw_parts_mut").expect("Invalid raw_pointer regex")
});

static SECURITY_PATTERNS: LazyLock<Vec<SecurityPattern>> = LazyLock::new(|| {
    vec![
        SecurityPattern {
            regex: &UNSAFE_BLOCK_RE,
            note_type: "unsafe_block",
            description: "Unsafe block manual memory safety required",
        },
        SecurityPattern {
            regex: &TRANSMUTE_RE,
            note_type: "transmute",
            description: "Use of mem::transmute is unsafe",
        },
        SecurityPattern {
            regex: &UNWRAP_RE,
            note_type: "panic_risk",
            description: "Unchecked unwrap() panics on None/Err",
        },
        SecurityPattern {
            regex: &EXPECT_RE,
            note_type: "panic_risk",
            description: "Unchecked expect() panics on None/Err",
        },
        SecurityPattern {
            regex: &COMMAND_EXEC_RE,
            note_type: "command_execution",
            description: "Shell command execution",
        },
        SecurityPattern {
            regex: &RAW_POINTER_RE,
            note_type: "raw_pointer",
            description: "Raw pointer slice construction",
        },
    ]
});

/// Traits from std::ops whose impl signals operator overloading.
/// Maps trait name → the operator symbol it overloads.
static OPERATOR_TRAIT_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut m = HashMap::new();
    m.insert("Add", "+");
    m.insert("Sub", "-");
    m.insert("Mul", "*");
    m.insert("Div", "/");
    m.insert("Rem", "%");
    m.insert("BitAnd", "&");
    m.insert("BitOr", "|");
    m.insert("BitXor", "^");
    m.insert("Shl", "<<");
    m.insert("Shr", ">>");
    m.insert("Neg", "-");
    m.insert("Not", "!");
    m.insert("Index", "[]");
    m.insert("IndexMut", "[]");
    m.insert("Deref", "*");
    m.insert("DerefMut", "*");
    m.insert("PartialEq", "==");
    m.insert("Eq", "==");
    m.insert("PartialOrd", "<");
    m.insert("Ord", "<");
    m.insert("AddAssign", "+=");
    m.insert("SubAssign", "-=");
    m.insert("MulAssign", "*=");
    m.insert("DivAssign", "/=");
    m.insert("RemAssign", "%=");
    m.insert("BitAndAssign", "&=");
    m.insert("BitOrAssign", "|=");
    m.insert("BitXorAssign", "^=");
    m.insert("ShlAssign", "<<=");
    m.insert("ShrAssign", ">>=");
    m
});

pub struct RustParser {
    source_code: String,
    file_path: String,
}

impl RustParser {
    pub fn new(source_code: String, file_path: String) -> Self {
        Self {
            source_code,
            file_path,
        }
    }

    pub fn parse(&self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_rust::language())
            .map_err(|e| format!("Failed to load Rust grammar: {}", e))?;

        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse Rust file".to_string())?;

        let root = tree.root_node();

        Ok(FileData {
            language: "rust".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(&root),
            functions: self.extract_functions(&root),
            classes: self.extract_structs_and_enums(&root),
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

    fn make_struct_id(&self, name: &str) -> String {
        format!("struct_{}::{}", name, self.file_path)
    }

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        self.collect_use_declarations(root, &mut imports);
        imports
    }

    fn collect_use_declarations(&self, node: &Node, imports: &mut Vec<Import>) {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "use_declaration" {
                let text = self.get_node_text(&child);
                let module = text
                    .trim_start_matches("use ")
                    .trim_end_matches(';')
                    .trim()
                    .to_string();

                let import_type = self.classify_import(&module);
                imports.push(Import {
                    module,
                    items: vec![],
                    import_type,
                });
            } else {
                self.collect_use_declarations(&child, imports);
            }
        }
    }

    fn classify_import(&self, module: &str) -> String {
        let std_prefixes = ["std::", "core::", "alloc::", "proc_macro::"];
        if std_prefixes.iter().any(|p| module.starts_with(p)) {
            "stdlib".to_string()
        } else if module.starts_with("crate::")
            || module.starts_with("super::")
            || module.starts_with("self::")
        {
            "internal".to_string()
        } else {
            "external".to_string()
        }
    }

    /// Extracts all `RustInfo` fields from a function, struct, enum, or trait node.
    ///
    /// `is_async`      – caller supplies this since async can appear as a function modifier
    ///                   parsed one level above (e.g. from `parse_function`).
    /// `trait_context` – `Some(trait_name)` when the node lives inside a trait/impl block;
    ///                   `None` for free items.
    fn extract_rust_info(
        &self,
        node: &Node,
        is_async: bool,
        trait_context: Option<&TraitContext>,
    ) -> RustInfo {
        let mut is_pub = false;
        let mut is_pub_crate = false;
        let mut is_unsafe = false;
        let mut is_const_fn = false;
        let mut is_extern = false;
        let mut abi: Option<String> = None;
        let mut lifetimes = Vec::new();
        let mut generics: Vec<String> = Vec::new();
        let mut where_clause: Option<String> = None;
        let mut derives = Vec::new();
        let mut is_test = false;
        let mut is_bench = false;
        let mut cfg_attrs = Vec::new();
        let mut unknown_attrs = Vec::new();

        // Walk direct children for modifiers, visibility, type params, where
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "visibility_modifier" => {
                    let text = self.get_node_text(&child);
                    if text.contains("(crate)") {
                        is_pub_crate = true;
                    } else {
                        is_pub = true;
                    }
                }
                "function_modifiers" => {
                    let text = self.get_node_text(&child);
                    if text.contains("unsafe") {
                        is_unsafe = true;
                    }
                    if text.contains("const") {
                        is_const_fn = true;
                    }
                    if text.contains("extern") {
                        is_extern = true;
                        // Try to extract the ABI string, e.g. extern "C"
                        abi = self.extract_abi(&child);
                    }
                }
                // extern "C" fn the ABI string sits directly as a string_literal child
                // of function_modifiers; handled above via extract_abi.
                "type_parameters" => {
                    let (lts, gens) = self.extract_type_params(&child);
                    lifetimes = lts;
                    generics = gens;
                }
                "where_clause" => {
                    where_clause = Some(self.get_node_text(&child));
                }
                _ => {}
            }
        }

        let is_generic = !lifetimes.is_empty() || !generics.is_empty();

        // Scan preceding attribute_items
        let mut prev = node.prev_sibling();
        while let Some(sib) = prev {
            match sib.kind() {
                "attribute_item" => {
                    let raw = self.get_node_text(&sib);
                    let inner = raw
                        .trim_start_matches('#')
                        .trim_start_matches('[')
                        .trim_end_matches(']')
                        .trim();

                    if inner.starts_with("derive") {
                        if let Some(s) = inner.find('(') {
                            let end = inner.rfind(')').unwrap_or(inner.len());
                            for d in inner[s + 1..end].split(',') {
                                let d = d.trim().to_string();
                                if !d.is_empty() {
                                    derives.push(d);
                                }
                            }
                        }
                    } else if inner == "test" {
                        is_test = true;
                    } else if inner == "bench" {
                        is_bench = true;
                    } else if inner.starts_with("cfg") {
                        cfg_attrs.push(raw);
                    } else {
                        unknown_attrs.push(raw);
                    }

                    prev = sib.prev_sibling();
                }
                "line_comment" | "block_comment" => {
                    prev = sib.prev_sibling();
                }
                _ => break,
            }
        }

        // Trait / impl context
        let (trait_name, is_trait_impl_method, is_trait_default_method) = match trait_context {
            Some(ctx) => (
                Some(ctx.trait_name.clone()),
                ctx.is_impl,
                // A method inside a trait definition with a body is a default method.
                !ctx.is_impl && node.child_by_field_name("body").is_some(),
            ),
            None => (None, false, false),
        };

        // Operator overloading (derived from trait_name)
        let (is_operator_overload, overloaded_operator) = trait_name
            .as_deref()
            .and_then(|t| {
                // Strip generic args: "Add<Rhs>" → "Add"
                let bare = t.split('<').next().unwrap_or(t).trim();
                OPERATOR_TRAIT_MAP
                    .get(bare)
                    .map(|op| (true, Some(op.to_string())))
            })
            .unwrap_or((false, None));

        RustInfo {
            is_unsafe,
            is_pub,
            is_pub_crate,
            is_const_fn,
            is_async,
            is_extern,
            abi,
            lifetimes,
            generics,
            where_clause,
            is_generic,
            derives,
            is_test,
            is_bench,
            cfg_attrs,
            unknown_attrs,
            trait_name,
            is_trait_impl_method,
            is_trait_default_method,
            is_operator_overload,
            overloaded_operator,
            // item_kind, supertraits, is_marker_trait filled by callers
            item_kind: None,
            supertraits: vec![],
            is_marker_trait: false,
            // body signals filled by callers that have access to the body node
            uses_try_operator: false,
            macro_calls: vec![],
        }
    }

    /// Returns `(lifetimes, generic_type_params)` from a `type_parameters` node.
    fn extract_type_params(&self, node: &Node) -> (Vec<String>, Vec<String>) {
        let mut lifetimes = Vec::new();
        let mut generics = Vec::new();
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "lifetime" => lifetimes.push(self.get_node_text(&child)),
                // type_identifier, constrained_type_parameter, optional_type_parameter …
                "type_identifier"
                | "constrained_type_parameter"
                | "optional_type_parameter"
                | "const_parameter" => {
                    generics.push(self.get_node_text(&child));
                }
                _ => {}
            }
        }

        (lifetimes, generics)
    }

    /// Extracts the ABI string from a `function_modifiers` node.
    /// e.g. `extern "C"` → `Some("C".to_string())`
    fn extract_abi(&self, modifiers_node: &Node) -> Option<String> {
        let mut cursor = modifiers_node.walk();
        for child in modifiers_node.children(&mut cursor) {
            if child.kind() == "string_literal" {
                let raw = self.get_node_text(&child);
                // Strip surrounding quotes
                return Some(raw.trim_matches('"').to_string());
            }
        }
        None
    }

    fn extract_functions(&self, root: &Node) -> Vec<Function> {
        let mut functions = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            if child.kind() == "function_item" {
                if let Some(func) = self.parse_function(&child, "", None) {
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
        trait_context: Option<&TraitContext>,
    ) -> Option<Function> {
        let name_node = node.child_by_field_name("name")?;
        let name = self.get_node_text(&name_node);

        let is_async = {
            let mut c = node.walk();
            node.children(&mut c).any(|ch| ch.kind() == "async")
                || node
                    .child_by_field_name("function_modifiers")
                    .map(|m| self.get_node_text(&m).contains("async"))
                    .unwrap_or(false)
        };

        let params = self.extract_parameters(node);
        let return_type = self.extract_return_type(node);
        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);
        let signature = self.build_signature(&name, &params, &return_type, is_async);
        let id = self.make_function_id(&name, struct_context);

        let (calls, variables, control_flow, complexity, uses_try_operator, macro_calls) =
            if let Some(body) = node.child_by_field_name("body") {
                (
                    self.extract_function_calls_detailed(&body),
                    self.extract_variables(&body, &params),
                    self.build_control_flow(&body),
                    self.calculate_complexity(&body),
                    self.detect_try_operator(&body),
                    self.extract_macro_calls(&body),
                )
            } else {
                (vec![], vec![], ControlFlow::default(), 1, false, vec![])
            };

        let mut rust_info = self.extract_rust_info(node, is_async, trait_context);
        // Populate body signals now that we have them
        rust_info.uses_try_operator = uses_try_operator;
        rust_info.macro_calls = macro_calls;

        let mut tags = self.auto_tag_function(&name, &docstring, &calls);
        if rust_info.is_test {
            tags.push("test".to_string());
        }
        if rust_info.is_unsafe {
            tags.push("unsafe".to_string());
        }
        if rust_info.is_operator_overload {
            tags.push("operator-overload".to_string());
        }

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
            is_async,
            decorators: vec![],
            tags,
            importance_score,
            lang_info: LanguageSpecificInfo {
                rust: Some(rust_info),
                ..Default::default()
            },
        })
    }

    fn extract_parameters(&self, node: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();

        if let Some(param_list) = node.child_by_field_name("parameters") {
            let mut cursor = param_list.walk();
            for child in param_list.children(&mut cursor) {
                match child.kind() {
                    "parameter" => {
                        let name = child
                            .child_by_field_name("pattern")
                            .map(|n| self.get_node_text(&n))
                            .unwrap_or_else(|| "_".to_string());
                        let type_annotation = child
                            .child_by_field_name("type")
                            .map(|t| self.get_node_text(&t))
                            .unwrap_or_default();
                        params.push(Parameter {
                            name,
                            type_annotation,
                            default_value: None,
                        });
                    }
                    "self_parameter" | "variadic_parameter" => {
                        params.push(Parameter {
                            name: self.get_node_text(&child),
                            type_annotation: String::new(),
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
            format!("{}fn {}({})", async_kw, name, param_str)
        } else {
            format!("{}fn {}({}) -> {}", async_kw, name, param_str, return_type)
        }
    }

    fn extract_structs_and_enums(&self, root: &Node) -> Vec<Class> {
        let mut items: Vec<Class> = Vec::new();
        let mut methods_map: HashMap<String, Vec<Function>> = HashMap::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            match child.kind() {
                "struct_item" => {
                    if let Some(class) = self.parse_struct(&child) {
                        items.push(class);
                    }
                }
                "enum_item" => {
                    if let Some(class) = self.parse_enum(&child) {
                        items.push(class);
                    }
                }
                "union_item" => {
                    if let Some(class) = self.parse_union(&child) {
                        items.push(class);
                    }
                }
                "trait_item" => {
                    if let Some(class) = self.parse_trait(&child) {
                        items.push(class);
                    }
                }
                "impl_item" => {
                    // Resolve impl context: `impl Trait for Type` vs `impl Type`
                    let trait_type = child.child_by_field_name("trait");
                    let self_type = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_default();

                    let (impl_trait_name, key) = match &trait_type {
                        Some(t) => {
                            let trait_str = self.get_node_text(t);
                            // key on the implementing type so methods land on the right Class
                            (Some(trait_str.clone()), self_type.clone())
                        }
                        None => (None, self_type.clone()),
                    };

                    if !key.is_empty() {
                        if let Some(body) = child.child_by_field_name("body") {
                            let ctx = impl_trait_name.as_deref().map(|t| TraitContext {
                                trait_name: t.to_string(),
                                is_impl: true,
                            });

                            let mut body_cursor = body.walk();
                            for item in body.children(&mut body_cursor) {
                                if item.kind() == "function_item" {
                                    if let Some(method) =
                                        self.parse_function(&item, &key, ctx.as_ref())
                                    {
                                        methods_map.entry(key.clone()).or_default().push(method);
                                    }
                                }
                            }
                        }
                    }
                }
                _ => {}
            }
        }

        for class in &mut items {
            if let Some(methods) = methods_map.remove(&class.name) {
                class.methods = methods;
            }
        }

        items
    }

    fn parse_struct(&self, node: &Node) -> Option<Class> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        let (attributes, item_kind) = match node.child_by_field_name("body") {
            Some(body) if body.kind() == "field_declaration_list" => {
                (self.extract_struct_fields(&body), "struct")
            }
            Some(body) if body.kind() == "ordered_field_declaration_list" => {
                (self.extract_tuple_fields(&body), "tuple_struct")
            }
            // unit struct: no body at all
            _ => (vec![], "unit_struct"),
        };

        let mut rust_info = self.extract_rust_info(node, false, None);
        rust_info.item_kind = Some(item_kind.to_string());

        Some(Class {
            id: self.make_struct_id(&name),
            name,
            bases: vec![],
            docstring,
            line_start,
            line_end,
            methods: vec![],
            attributes,
            decorators: vec![],
            lang_info: LanguageSpecificInfo {
                rust: Some(rust_info),
                ..Default::default()
            },
        })
    }

    fn parse_enum(&self, node: &Node) -> Option<Class> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        let attributes = node
            .child_by_field_name("body")
            .map(|body| {
                let mut fields = Vec::new();
                let mut cursor = body.walk();
                for child in body.children(&mut cursor) {
                    if child.kind() == "enum_variant" {
                        if let Some(vname) = child.child_by_field_name("name") {
                            fields.push(Attribute {
                                name: self.get_node_text(&vname),
                                type_annotation: String::new(),
                                value: None,
                            });
                        }
                    }
                }
                fields
            })
            .unwrap_or_default();

        let mut rust_info = self.extract_rust_info(node, false, None);
        rust_info.item_kind = Some("enum".to_string());

        Some(Class {
            id: format!("enum_{}::{}", name, self.file_path),
            name,
            bases: vec![],
            docstring,
            line_start,
            line_end,
            methods: vec![],
            attributes,
            decorators: vec![],
            lang_info: LanguageSpecificInfo {
                rust: Some(rust_info),
                ..Default::default()
            },
        })
    }

    fn parse_union(&self, node: &Node) -> Option<Class> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        let attributes = node
            .child_by_field_name("body")
            .map(|body| self.extract_struct_fields(&body))
            .unwrap_or_default();

        let mut rust_info = self.extract_rust_info(node, false, None);
        rust_info.item_kind = Some("union".to_string());
        // Unions are always implicitly unsafe to read
        rust_info.is_unsafe = true;

        Some(Class {
            id: format!("union_{}::{}", name, self.file_path),
            name,
            bases: vec![],
            docstring,
            line_start,
            line_end,
            methods: vec![],
            attributes,
            decorators: vec![],
            lang_info: LanguageSpecificInfo {
                rust: Some(rust_info),
                ..Default::default()
            },
        })
    }

    fn parse_trait(&self, node: &Node) -> Option<Class> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_docstring(node);

        // Collect supertraits from the `bounds` field: `trait Foo: Bar + Baz`
        let supertraits = node
            .child_by_field_name("bounds")
            .map(|b| self.extract_supertraits(&b))
            .unwrap_or_default();

        // Collect methods declared in the trait body
        let methods = node
            .child_by_field_name("body")
            .map(|body| {
                let ctx = TraitContext {
                    trait_name: name.clone(),
                    is_impl: false,
                };
                let mut ms = Vec::new();
                let mut cursor = body.walk();
                for child in body.children(&mut cursor) {
                    if child.kind() == "function_item" || child.kind() == "function_signature_item"
                    {
                        if let Some(method) = self.parse_function(&child, &name, Some(&ctx)) {
                            ms.push(method);
                        }
                    }
                }
                ms
            })
            .unwrap_or_default();

        let is_marker_trait = methods.iter().all(|m| {
            // A required method has no body; a default method does.
            // A marker trait has no required methods at all.
            m.lang_info
                .rust
                .as_ref()
                .map(|r| r.is_trait_default_method)
                .unwrap_or(true)
        }) && methods.is_empty();

        let mut rust_info = self.extract_rust_info(node, false, None);
        rust_info.item_kind = Some("trait".to_string());
        rust_info.supertraits = supertraits.clone();
        rust_info.is_marker_trait = is_marker_trait;

        Some(Class {
            id: format!("trait_{}::{}", name, self.file_path),
            name,
            bases: supertraits,
            docstring,
            line_start,
            line_end,
            methods,
            attributes: vec![],
            decorators: vec![],
            lang_info: LanguageSpecificInfo {
                rust: Some(rust_info),
                ..Default::default()
            },
        })
    }

    fn extract_struct_fields(&self, body: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();
        let mut cursor = body.walk();

        for child in body.children(&mut cursor) {
            if child.kind() == "field_declaration" {
                let name = child
                    .child_by_field_name("name")
                    .map(|n| self.get_node_text(&n))
                    .unwrap_or_default();
                let type_annotation = child
                    .child_by_field_name("type")
                    .map(|t| self.get_node_text(&t))
                    .unwrap_or_default();

                if !name.is_empty() {
                    fields.push(Attribute {
                        name,
                        type_annotation,
                        value: None,
                    });
                }
            }
        }

        fields
    }

    /// Extracts positional fields from a tuple struct, naming them by index.
    fn extract_tuple_fields(&self, body: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();
        let mut idx = 0usize;
        let mut cursor = body.walk();

        for child in body.children(&mut cursor) {
            if child.kind() == "field_declaration" {
                let type_annotation = child
                    .child_by_field_name("type")
                    .map(|t| self.get_node_text(&t))
                    .unwrap_or_default();
                fields.push(Attribute {
                    name: idx.to_string(),
                    type_annotation,
                    value: None,
                });
                idx += 1;
            }
        }

        fields
    }

    fn extract_supertraits(&self, bounds_node: &Node) -> Vec<String> {
        let mut traits = Vec::new();
        let mut cursor = bounds_node.walk();

        for child in bounds_node.children(&mut cursor) {
            match child.kind() {
                // type_identifier, scoped_type_identifier, generic_type …
                "type_identifier" | "scoped_type_identifier" | "generic_type" => {
                    traits.push(self.get_node_text(&child));
                }
                // lifetime bounds like `'a` are not supertraits
                "lifetime" => {}
                _ => {}
            }
        }

        traits
    }

    fn extract_global_vars(&self, root: &Node) -> Vec<GlobalVar> {
        let mut vars = Vec::new();
        let mut cursor = root.walk();

        for child in root.children(&mut cursor) {
            match child.kind() {
                "const_item" | "static_item" => {
                    if let Some(v) = self.parse_global_var(&child) {
                        vars.push(v);
                    }
                }
                _ => {}
            }
        }

        vars
    }

    fn parse_global_var(&self, node: &Node) -> Option<GlobalVar> {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

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
            line: node.start_position().row + 1,
        })
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
            "if_expression" => "if",
            "loop_expression" | "while_expression" | "for_expression" => "loop",
            "match_expression" => "match",
            _ => context,
        };

        if node.kind() == "call_expression" {
            if let Some(func_node) = node.child_by_field_name("function") {
                let call_text = self.get_node_text(&func_node);
                let name = call_text
                    .split("::")
                    .last()
                    .unwrap_or(&call_text)
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

    /// Returns `true` if the `?` try operator appears anywhere in `node`.
    fn detect_try_operator(&self, node: &Node) -> bool {
        if node.kind() == "try_expression" {
            return true;
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if self.detect_try_operator(&child) {
                return true;
            }
        }
        false
    }

    /// Collects unique macro invocation names from `node` (e.g. `vec!`, `println!`).
    fn extract_macro_calls(&self, node: &Node) -> Vec<String> {
        let mut seen: HashSet<String> = HashSet::new();
        let mut result: Vec<String> = Vec::new();
        self.collect_macro_calls(node, &mut seen, &mut result);
        result
    }

    fn collect_macro_calls(
        &self,
        node: &Node,
        seen: &mut HashSet<String>,
        result: &mut Vec<String>,
    ) {
        // tree-sitter-rust names macro invocations "macro_invocation"
        if node.kind() == "macro_invocation" {
            if let Some(macro_name_node) = node.child_by_field_name("macro") {
                let name = self.get_node_text(&macro_name_node);
                if seen.insert(name.clone()) {
                    result.push(name);
                }
            }
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.collect_macro_calls(&child, seen, result);
        }
    }

    fn extract_variables(&self, node: &Node, params: &[Parameter]) -> Vec<Variable> {
        let mut variables: HashMap<String, Variable> = HashMap::new();

        for param in params {
            let clean = param
                .name
                .trim_start_matches("mut ")
                .trim_start_matches('&')
                .to_string();
            variables.insert(
                clean.clone(),
                Variable {
                    name: clean,
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
            "let_declaration" => {
                let name = node
                    .child_by_field_name("pattern")
                    .map(|n| self.get_node_text(&n))
                    .unwrap_or_default()
                    .trim_start_matches("mut ")
                    .to_string();

                if !name.is_empty() {
                    let var_type = node
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t));
                    let line = node.start_position().row + 1;

                    variables.entry(name.clone()).or_insert(Variable {
                        name,
                        var_type,
                        scope: "local".to_string(),
                        defined_at: Some(line),
                        transformations: vec![],
                        used_in: vec![],
                        returned: false,
                    });
                }
            }
            "return_expression" => {
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
            "if_expression" => {
                if let Some(branch) = self.parse_if(node) {
                    cf.branches.push(branch);
                }
            }
            "loop_expression" | "while_expression" | "for_expression" => {
                if let Some(lp) = self.parse_loop(node) {
                    cf.loops.push(lp);
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
            if child.kind() == "return_expression" {
                let text = self.get_node_text(&child);
                return Some(text.trim_start_matches("return").trim().to_string());
            }
            if let Some(val) = self.find_return_value(&child) {
                return Some(val);
            }
        }

        None
    }

    fn parse_loop(&self, node: &Node) -> Option<Loop> {
        let loop_type = match node.kind() {
            "for_expression" => "for",
            "while_expression" => "while",
            _ => "loop",
        };

        let condition = node
            .child_by_field_name("condition")
            .map(|c| self.get_node_text(&c))
            .unwrap_or_default();

        let calls = self.extract_calls_from_block(node);

        Some(Loop {
            loop_type: loop_type.to_string(),
            condition,
            line: node.start_position().row + 1,
            calls,
        })
    }

    fn calculate_complexity(&self, node: &Node) -> usize {
        fn count(node: &Node) -> usize {
            let mut c = 0;
            let mut cursor = node.walk();
            match node.kind() {
                "if_expression" | "match_expression" | "loop_expression" | "while_expression"
                | "for_expression" | "binary_expression" => c += 1,
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
        let mut doc_lines = Vec::new();
        let mut prev = node.prev_sibling();

        while let Some(sib) = prev {
            if sib.kind() == "line_comment" || sib.kind() == "block_comment" {
                let text = self.get_node_text(&sib);
                let cleaned = text
                    .trim_start_matches("///")
                    .trim_start_matches("//!")
                    .trim_start_matches("//")
                    .trim_start_matches("/*!")
                    .trim_start_matches("/**")
                    .trim_start_matches("/*")
                    .trim_end_matches("*/")
                    .trim()
                    .to_string();
                doc_lines.insert(0, cleaned);
                prev = sib.prev_sibling();
            } else {
                break;
            }
        }

        doc_lines.join(" ")
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

        if lower.starts_with("new") || lower == "default" || lower == "init" {
            tags.push("constructor".to_string());
        }
        if (lower.starts_with("test") || lower.starts_with("should_"))
            && !tags.contains(&"test".to_string())
        {
            tags.push("test".to_string());
        }
        if lower.contains("parse") || lower.contains("deserializ") {
            tags.push("parsing".to_string());
        }
        if lower.contains("serialize") || lower.contains("encode") {
            tags.push("serialization".to_string());
        }
        if lower.contains("error") || lower.contains("handle") {
            tags.push("error-handling".to_string());
        }
        if doc_lower.contains("panic") || doc_lower.contains("safety") {
            tags.push("safety-critical".to_string());
        }
        if calls
            .iter()
            .any(|c| c.callee.contains("unwrap") || c.callee.contains("expect"))
        {
            tags.push("fallible".to_string());
        }

        tags
    }

    fn estimate_importance(&self, name: &str, is_method: bool) -> f32 {
        let lower = name.to_lowercase();
        let mut score: f32 = 0.5;

        if lower == "main" || lower == "new" || lower == "parse" {
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

    fn get_node_text(&self, node: &Node) -> String {
        let bytes = self.source_code.as_bytes();
        let start = node.start_byte();
        let end = node.end_byte();
        String::from_utf8_lossy(&bytes[start..end]).to_string()
    }
}

// Internal context carrier never surfaces in FileData

/// Carries trait/impl context down into `parse_function` so `RustInfo` fields
/// like `trait_name`, `is_trait_impl_method`, and `is_trait_default_method`
/// can be populated without threading extra parameters through every helper.
struct TraitContext {
    /// Name of the trait (e.g. `"Display"`, `"Iterator"`).
    trait_name: String,
    /// `true`  → we are inside `impl Trait for Type { … }`
    /// `false` → we are inside `trait Trait { … }` itself
    is_impl: bool,
}

/// Entry point called from main.rs
pub fn parse_file(path: &Path) -> Result<(String, FileData), String> {
    let source_code = std::fs::read_to_string(path)
        .map_err(|e| format!("Failed to read file {}: {}", path.display(), e))?;

    let clean_path = path.strip_prefix("./").unwrap_or(path);
    let path_str = clean_path.to_string_lossy().to_string();

    let parser = RustParser::new(source_code, path_str.clone());
    let file_data = parser.parse()?;

    Ok((path_str, file_data))
}
