//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::struc::kb_struct::*;
// use once_cell::sync::Lazy;
use regex::bytes::Regex;
// use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::str;
use std::sync::LazyLock;
use tree_sitter::{Node, Parser};
// Regex Patterns Compiled once
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

static UNSAFE_STRING_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"strcpy|strcat|sprintf|vsprintf|gets")
        .expect("static unsafe string regex pattern is valid")
});

static COMMAND_EXEC_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"system\(|popen\(|exec").expect("static command execution regex pattern is valid")
});

static MANUAL_MEMORY_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"malloc|calloc|realloc|free").expect("static manual memory regex pattern is valid")
});

static UNSAFE_INPUT_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"scanf|fscanf").expect("static unsafe input regex pattern is valid")
});

static MEMORY_OP_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"memcpy|memmove|memset").expect("static memory operation regex pattern is valid")
});

static PRIVILEGE_CHANGE_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"setuid|setgid|seteuid").expect("static privilege change regex pattern is valid")
});

static WEAK_RANDOM_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"rand\(\)|random\(\)").expect("static weak random regex pattern is valid")
});

static INCLUDE_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r#"^#include\s+[<"]([^>"]+)[>"]"#)
        .expect("static C include header regex pattern is valid")
});

static TODO_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"(?://|/\*)\s*TODO:?\s*(.+?)(?:\*/|$)")
        .expect("static TODO comment regex pattern is valid")
});

static MACRO_DEFINE_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"(?m)^#define\s+([A-Za-z_]\w*)(?:\([^)]*\))?\s+(.+)$")
        .expect("static macro definition regex pattern is valid")
});

static MALLOC_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"malloc|calloc|realloc|alloca")
        .expect("static allocation call regex pattern is valid")
});

static FREE_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"\bfree\b").expect("static free function call regex pattern is valid")
});

static PTHREAD_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"pthread|fork|thread").expect("static concurrency regex pattern is valid")
});

static SYSCALL_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"syscall|ioctl|fcntl").expect("static system call regex pattern is valid")
});

static STRING_OPS_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"strcpy|strcat|sprintf|strncpy")
        .expect("static string operations regex pattern is valid")
});

static INLINE_ASM_RE: LazyLock<Regex> = LazyLock::new(|| {
    #[allow(clippy::expect_used)]
    Regex::new(r"\b(asm|__asm__|__asm)\s*(volatile\s*|goto\s*)?\(")
        .expect("static inline assembly regex pattern is valid")
});

static SECURITY_PATTERNS: LazyLock<Vec<SecurityPattern>> = LazyLock::new(|| {
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
        SecurityPattern {
            regex: &INLINE_ASM_RE,
            note_type: "inline_asm",
            description: "Inline assembly block",
        },
    ]
});

static TAG_RULES: LazyLock<Vec<TagRule>> = LazyLock::new(|| {
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
        TagRule {
            keywords: &["asm", "assembler"],
            tag: "inline-asm",
            check_docstring: false,
        },
    ]
});

pub struct CParser {
    source_code: String,
    file_path: String,
    macro_defs: HashMap<String, String>, // macro_name -> expansion text (best-effort from #define)
    fn_ptr_map: HashMap<String, String>, // function_ptr_var -> resolved callee name (best-effort)
}

impl CParser {
    pub fn new(source_code: String, file_path: String) -> Self {
        let macro_defs = Self::pre_scan_macros(&source_code);
        Self {
            source_code,
            file_path,
            macro_defs,
            fn_ptr_map: HashMap::new(),
        }
    }

    // Pre-scan: collect #define NAME(...) body
    fn pre_scan_macros(src: &str) -> HashMap<String, String> {
        let mut map = HashMap::new();
        // Matches both object-like and function-like macros, handles line continuations
        for caps in MACRO_DEFINE_RE.captures_iter(src.as_bytes()) {
            if let (Some(m1), Some(m2)) = (caps.get(1), caps.get(2)) {
                let name = String::from_utf8_lossy(m1.as_bytes()).to_string();
                let body_str = String::from_utf8_lossy(m2.as_bytes());
                let body = body_str.trim_end_matches('\\').trim().to_string();
                map.insert(name, body);
            }
        }
        map
    }

    /// Builds a file-qualified ID for a top-level function.
    /// e.g. func_parseFile::src/parser.c
    ///      method_MyStruct_init::src/parser.c
    fn make_function_id(&self, name: &str, struct_context: &str) -> String {
        if struct_context.is_empty() {
            format!("func_{}::{}", name, self.file_path)
        } else {
            format!("method_{}_{}::{}", struct_context, name, self.file_path)
        }
    }
    /// e.g. struct_MyStruct::src/parser.c
    fn make_struct_id(&self, name: &str) -> String {
        format!("struct_{}::{}", name, self.file_path)
    }

    /// e.g. union_MyUnion::src/parser.c
    fn make_union_id(&self, name: &str) -> String {
        format!("union_{}::{}", name, self.file_path)
    }

    /// e.g. enum_MyEnum::src/parser.c
    fn make_enum_id(&self, name: &str) -> String {
        format!("enum_{}::{}", name, self.file_path)
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
                if let Some(caps) = INCLUDE_RE.captures(text.as_bytes()) {
                    if let Some(m) = caps.get(1) {
                        let path = String::from_utf8_lossy(m.as_bytes()).to_string();
                        let is_system = text.contains('<');
                        imports.push(Import {
                            module: path.clone(),
                            items: vec![],
                            import_type: self.classify_import(&path, is_system),
                        });
                    }
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
        let base_return_type = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_else(|| "void".to_string());

        let return_type = if self.declarator_has_pointer_return(&declarator) {
            format!("{} *", base_return_type)
        } else {
            base_return_type
        };
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

        let id = self.make_function_id(&name, struct_context);
        let body_text = self.get_node_text(&body);

        let tags = self.auto_tag_function(&name, &docstring, &calls, &return_type, &body_text);
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
            "parenthesized_declarator" => {
                let mut cursor = declarator.walk();
                for child in declarator.children(&mut cursor) {
                    if child.is_named() {
                        if let Some(name) = self.extract_function_name(&child) {
                            return Some(name);
                        }
                    }
                }
                None
            }
            "identifier" => Some(self.get_node_text(declarator)),
            _ => None,
        }
    }
    fn declarator_has_pointer_return(&self, decl: &Node) -> bool {
        match decl.kind() {
            "pointer_declarator" => true,
            "function_declarator" | "parenthesized_declarator" => decl
                .child_by_field_name("declarator")
                .map(|d| self.declarator_has_pointer_return(&d))
                .unwrap_or(false),
            _ => false,
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
            "identifier" | "field_identifier" => self.get_node_text(declarator),

            "init_declarator" => declarator
                .child_by_field_name("declarator")
                .map(|d| self.extract_declarator_name(&d))
                .unwrap_or_default(),

            "pointer_declarator"
            | "array_declarator"
            | "function_declarator"
            | "parenthesized_declarator" => {
                if let Some(decl) = declarator.child_by_field_name("declarator") {
                    self.extract_declarator_name(&decl)
                } else {
                    // parenthesized_declarator may not expose a `declarator` field
                    let mut cursor = declarator.walk();
                    for child in declarator.children(&mut cursor) {
                        if child.is_named() {
                            let name = self.extract_declarator_name(&child);
                            if !name.is_empty() {
                                return name;
                            }
                        }
                    }
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
            .rsplit(['.', '>'])
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
            match child.kind() {
                "struct_specifier" | "union_specifier" => {
                    if let Some(s) = self.parse_struct_specifier(&child) {
                        structs.push(s);
                    }
                }
                "enum_specifier" => {
                    if let Some(e) = self.parse_enum_specifier(&child) {
                        structs.push(e);
                    }
                }
                "declaration" => {
                    if let Some(type_node) = child.child_by_field_name("type") {
                        match type_node.kind() {
                            "struct_specifier" | "union_specifier" => {
                                if let Some(s) = self.parse_struct_specifier(&type_node) {
                                    structs.push(s);
                                }
                            }
                            "enum_specifier" => {
                                if let Some(e) = self.parse_enum_specifier(&type_node) {
                                    structs.push(e);
                                }
                            }
                            _ => {}
                        }
                    }
                }
                _ => {}
            }
        }

        structs
    }

    fn extract_enum_fields(&self, body: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();
        let mut cursor = body.walk();

        for child in body.children(&mut cursor) {
            if child.kind() == "enumerator" {
                let name = child
                    .child_by_field_name("name")
                    .map(|n| self.get_node_text(&n))
                    .unwrap_or_default();

                if !name.is_empty() {
                    let value = child
                        .child_by_field_name("value")
                        .map(|v| self.get_node_text(&v));

                    fields.push(Attribute {
                        name,
                        type_annotation: "enum_constant".to_string(),
                        value,
                    });
                }
            }
        }
        fields
    }
    fn is_flags_enum(&self, attributes: &[Attribute]) -> bool {
        // Check if enum values look like bit flags (powers of two)
        // This is a simple heuristic
        let values: Vec<&str> = attributes
            .iter()
            .filter_map(|attr| attr.value.as_deref())
            .collect();

        if values.len() < 2 {
            return false;
        }

        // Check for common flag patterns like 0x1, 0x2, 0x4, 0x8
        let hex_powers = values
            .iter()
            .any(|v| v.starts_with("0x") && (v.len() == 3 || v.len() == 4));

        // Or check for shift expressions like 1 << 0, 1 << 1, etc.
        let shift_pattern = values.iter().any(|v| v.contains("<<"));

        hex_powers || shift_pattern
    }

    #[expect(dead_code)]
    fn parse_struct(&self, node: &Node) -> Option<Class> {
        let type_node = node.child_by_field_name("type")?;
        self.parse_struct_specifier(&type_node)
    }

    fn parse_struct_specifier(&self, type_node: &Node) -> Option<Class> {
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

        let id = if struct_type == "union" {
            self.make_union_id(&name)
        } else {
            self.make_struct_id(&name)
        };

        Some(Class {
            id,
            name,
            bases: vec![],
            docstring: self.extract_docstring(type_node),
            line_start: type_node.start_position().row + 1,
            line_end: type_node.end_position().row + 1,
            methods: vec![],
            attributes,
            decorators: vec![struct_type.to_string()],
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    #[expect(dead_code)]
    fn parse_enum(&self, node: &Node) -> Option<Class> {
        let type_node = node.child_by_field_name("type")?;
        self.parse_enum_specifier(&type_node)
    }

    fn parse_enum_specifier(&self, type_node: &Node) -> Option<Class> {
        if type_node.kind() != "enum_specifier" {
            return None;
        }

        let name = type_node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))?;

        let attributes = type_node
            .child_by_field_name("body")
            .map(|b| self.extract_enum_fields(&b))
            .unwrap_or_default();

        let mut decorators = vec!["enum".to_string()];
        if self.is_flags_enum(&attributes) {
            decorators.push("flags".to_string());
        }

        Some(Class {
            id: self.make_enum_id(&name),
            name,
            bases: vec![],
            docstring: self.extract_docstring(type_node),
            line_start: type_node.start_position().row + 1,
            line_end: type_node.end_position().row + 1,
            methods: vec![],
            attributes,
            decorators,
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    #[expect(dead_code)]
    fn extract_struct_fields_old(&self, body: &Node) -> Vec<Attribute> {
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

    fn extract_struct_fields(&self, body: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();
        let mut cursor = body.walk();

        for child in body.children(&mut cursor) {
            if child.kind() != "field_declaration" {
                continue;
            }

            let type_annotation = child
                .child_by_field_name("type")
                .map(|t| self.get_node_text(&t))
                .unwrap_or_default();

            // A single field_declaration may carry multiple declarators:
            //     int a, b;
            // tree-sitter-c exposes them as sibling children, each with the
            // field name "declarator". Iterate all of them, not just the first.
            let mut decl_cursor = child.walk();
            for decl_child in child.children(&mut decl_cursor) {
                let decl_node = match decl_child.kind() {
                    "init_declarator" => decl_child.child_by_field_name("declarator"),
                    "field_identifier"
                    | "identifier"
                    | "pointer_declarator"
                    | "array_declarator"
                    | "function_declarator"
                    | "parenthesized_declarator" => Some(decl_child),
                    _ => None,
                };

                if let Some(decl) = decl_node {
                    let name = self.extract_declarator_name(&decl);
                    if !name.is_empty() {
                        fields.push(Attribute {
                            name,
                            type_annotation: type_annotation.clone(),
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
            if child.kind() == "declaration"
                && child
                    .child_by_field_name("declarator")
                    .map(|d| d.kind() != "function_declarator")
                    .unwrap_or(true)
            {
                if let Some(var) = self.parse_global_var(&child) {
                    vars.push(var);
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

    fn auto_tag_function(
        &self,
        name: &str,
        docstring: &str,
        calls: &[FunctionCall],
        return_type: &str,
        body_text: &str,
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
        let calls_bytes = calls_joined.as_bytes();

        if MALLOC_RE.is_match(calls_bytes) {
            tags.push("allocates-memory".to_string());
            tags.push("memory-management".to_string());
        }
        if INLINE_ASM_RE.is_match(body_text.as_bytes()) {
            tags.push("inline-asm".to_string());
            tags.push("unsafe".to_string());
        }

        if FREE_RE.is_match(calls_bytes) {
            tags.push("frees-memory".to_string());
            tags.push("memory-management".to_string());
        }

        if PTHREAD_RE.is_match(calls_bytes) {
            tags.push("concurrent".to_string());
            tags.push("threading".to_string());
        }

        if SYSCALL_RE.is_match(calls_bytes) {
            tags.push("system-call".to_string());
        }

        if STRING_OPS_RE.is_match(calls_bytes) {
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

    // Strips leading "./" or ".\" if present, otherwise leaves path as is
    let clean_path = path.strip_prefix("./").unwrap_or(path);
    let path_str = clean_path.to_string_lossy().to_string();

    let mut parser = CParser::new(source_code, path_str.clone());
    let file_data = parser.parse()?;

    Ok((path_str, file_data))
}

// AI was heavily involved in writing the below tests, i did checked the test and code
// and lgtm.

#[cfg(test)]
mod tests {
    #![expect(clippy::expect_used)]
    #![expect(clippy::unwrap_used)]

    use super::*;

    fn parse(src: &str, path: &str) -> FileData {
        CParser::new(src.to_string(), path.to_string())
            .parse()
            .expect("parse should not fail on well-formed C")
    }

    mod function_ids {
        use super::*;

        #[test]
        fn top_level_function_id_format() {
            let fd = parse("int add(int a, int b) { return a + b; }", "math.c");
            assert_eq!(fd.functions.len(), 1);
            assert_eq!(fd.functions[0].id, "func_add::math.c");
        }

        #[test]
        fn function_named_main_gets_entry_point_tag() {
            let fd = parse("int main(void) { return 0; }", "main.c");
            assert!(fd.functions[0].tags.contains(&"entry-point".to_string()));
        }

        #[test]
        fn function_name_that_itself_starts_with_method_underscore() {
            // Make sure a C function literally named "method_foo" isn't misclassified
            // as a method by downstream heuristics. It should just be "func_method_foo".
            let fd = parse("void method_foo(void) {}", "weird.c");
            assert_eq!(fd.functions[0].id, "func_method_foo::weird.c");
            assert!(!fd.functions[0].id.starts_with("method_"));
        }

        #[test]
        fn function_pointer_declarator_name_extraction() {
            let fd = parse("int (*get_handler(void))(int) { return 0; }", "h.c");
            // If recursive pointer_declarator extraction breaks, we'll silently
            // get 0 functions here. Lock this down so it fails loudly.
            assert_eq!(fd.functions.len(), 1);
            assert_eq!(fd.functions[0].name, "get_handler");
        }

        #[test]
        fn empty_file_produces_no_functions_and_does_not_panic() {
            let fd = parse("", "empty.c");
            assert!(fd.functions.is_empty());
            assert!(fd.classes.is_empty());
        }

        #[test]
        fn file_with_only_comments_produces_no_functions() {
            let fd = parse("// just a comment\n/* and a block */\n", "comments.c");
            assert!(fd.functions.is_empty());
        }

        #[test]
        fn nested_functions_are_not_supported_by_c_but_do_not_crash_the_parser() {
            // GCC nested functions aren't standard C, but they exist in the wild.
            // We don't care about extracting the inner one right now, just make
            // sure the parser doesn't choke and still finds the outer one.
            let src = "int outer(void) { int inner(void) { return 1; } return inner(); }";
            let fd = parse(src, "nested.c");
            assert!(fd.functions.iter().any(|f| f.name == "outer"));
        }
    }

    mod type_ids {
        use super::*;

        #[test]
        fn struct_id_format() {
            let fd = parse("struct Point { int x; int y; };", "geo.c");
            assert_eq!(fd.classes.len(), 1);
            assert_eq!(fd.classes[0].id, "struct_Point::geo.c");
            assert!(fd.classes[0].decorators.contains(&"struct".to_string()));
        }

        #[test]
        fn union_id_format() {
            let fd = parse("union Value { int i; float f; };", "val.c");
            assert_eq!(fd.classes[0].id, "union_Value::val.c");
            assert!(fd.classes[0].decorators.contains(&"union".to_string()));
        }

        #[test]
        fn enum_id_format_and_tag() {
            let fd = parse("enum Color { RED, GREEN, BLUE };", "color.c");
            assert_eq!(fd.classes.len(), 1);
            assert_eq!(fd.classes[0].id, "enum_Color::color.c");
            assert!(fd.classes[0].decorators.contains(&"enum".to_string()));
        }

        #[test]
        fn flags_enum_hex_powers_detected() {
            let src = "enum Flags { FLAG_A = 0x1, FLAG_B = 0x2, FLAG_C = 0x4 };";
            let fd = parse(src, "flags.c");
            assert!(fd.classes[0].decorators.contains(&"flags".to_string()));
        }

        #[test]
        fn flags_enum_shift_pattern_detected() {
            let src = "enum Flags { FLAG_A = 1 << 0, FLAG_B = 1 << 1 };";
            let fd = parse(src, "flags2.c");
            assert!(fd.classes[0].decorators.contains(&"flags".to_string()));
        }

        #[test]
        fn plain_sequential_enum_is_not_flagged_as_flags() {
            let src = "enum Color { RED = 1, GREEN = 2, BLUE = 3 };";
            let fd = parse(src, "seq.c");
            assert!(!fd.classes[0].decorators.contains(&"flags".to_string()));
        }

        #[test]
        fn single_valued_enum_never_counted_as_flags() {
            // Enums need at least 2 values to be considered flags.
            let src = "enum Solo { ONLY = 0x1 };";
            let fd = parse(src, "solo.c");
            assert!(!fd.classes[0].decorators.contains(&"flags".to_string()));
        }

        #[test]
        fn anonymous_struct_without_a_name_is_skipped() {
            // Typedef'd anonymous structs don't have a name on the struct_specifier.
            // Skip them instead of creating a Class with an empty name.
            let src = "typedef struct { int x; int y; } Point;";
            let fd = parse(src, "anon.c");
            assert!(
                fd.classes.iter().all(|c| !c.name.is_empty()),
                "should not create an empty-named class"
            );
        }

        #[test]
        fn struct_with_multiple_comma_declared_fields_may_only_capture_the_first() {
            // FIXME/Quirk: tree-sitter only gives us the first declarator for
            // comma-separated fields (like `int a, b;`). Asserting current behavior
            // so we know if this gets fixed upstream.
            let src = "struct Pair { int a, b; };";
            let fd = parse(src, "pair.c");
            let names: Vec<&str> = fd.classes[0]
                .attributes
                .iter()
                .map(|a| a.name.as_str())
                .collect();
            assert!(
                names.contains(&"a") || names.contains(&"b"),
                "expected at least one field to be captured"
            );
        }
    }

    mod call_extraction {
        use super::*;

        #[test]
        fn simple_unconditional_call() {
            let fd = parse("void a(void) { b(); } void b(void) {}", "c1.c");
            let a = fd.functions.iter().find(|f| f.name == "a").unwrap();
            assert_eq!(a.calls.len(), 1);
            assert_eq!(a.calls[0].callee, "b");
            assert!(!a.calls[0].is_conditional);
            assert_eq!(a.calls[0].context, "unconditional");
        }

        #[test]
        fn call_inside_if_marked_conditional() {
            let src = "void a(int x) { if (x) { b(); } }";
            let fd = parse(src, "c2.c");
            let a = &fd.functions[0];
            assert_eq!(a.calls[0].context, "if");
            assert!(a.calls[0].is_conditional);
        }

        #[test]
        fn call_inside_loop_marked_conditional_with_loop_context() {
            let src = "void a(void) { for (int i = 0; i < 10; i++) { b(); } }";
            let fd = parse(src, "c3.c");
            let a = &fd.functions[0];
            assert_eq!(a.calls[0].context, "loop");
            assert!(a.calls[0].is_conditional);
        }

        #[test]
        fn call_inside_switch_marked_conditional_with_switch_context() {
            let src = "void a(int x) { switch (x) { case 1: b(); break; } }";
            let fd = parse(src, "c4.c");
            let a = &fd.functions[0];
            assert!(a
                .calls
                .iter()
                .any(|c| c.context == "switch" && c.is_conditional));
        }

        #[test]
        fn nested_conditional_context_uses_innermost_wrapper() {
            // Innermost wrapper wins. If -> for -> call means the context is "loop", not "if".
            let src = "void a(int x) { if (x) { for (;;) { b(); } } }";
            let fd = parse(src, "c5.c");
            assert_eq!(fd.functions[0].calls[0].context, "loop");
        }

        #[test]
        fn same_function_called_twice_on_the_same_line_is_deduped_to_one_call() {
            // Known bug: we dedup calls based on line number only. Two calls on the
            // same line get squashed into one. Documenting it here so we don't break it.
            let src = "void a(void) { b(); b(); }";
            let fd = parse(src, "c6.c");
            let b_calls: Vec<_> = fd.functions[0]
                .calls
                .iter()
                .filter(|c| c.callee == "b")
                .collect();
            assert_eq!(
                b_calls.len(),
                1,
                "same-line calls to the same function are currently deduped"
            );
        }

        #[test]
        fn same_function_called_twice_on_different_lines_both_recorded() {
            let src = "void a(void) {\n b();\n b();\n}";
            let fd = parse(src, "c7.c");
            let b_calls: Vec<_> = fd.functions[0]
                .calls
                .iter()
                .filter(|c| c.callee == "b")
                .collect();
            assert_eq!(b_calls.len(), 2);
        }

        #[test]
        fn call_to_undefined_function_is_still_recorded_with_raw_name() {
            // Unresolved/external calls just keep their raw text name.
            let fd = parse("void a(void) { totally_external_lib_call(); }", "c8.c");
            assert_eq!(fd.functions[0].calls[0].callee, "totally_external_lib_call");
        }

        #[test]
        fn recursive_self_call_is_recorded() {
            let fd = parse(
                "int fact(int n) { return n <= 1 ? 1 : n * fact(n - 1); }",
                "rec.c",
            );
            assert!(fd.functions[0].calls.iter().any(|c| c.callee == "fact"));
        }

        #[test]
        fn function_pointer_variable_call_resolves_through_fn_ptr_map() {
            // Function pointer calls should resolve to their actual target, not just "fp".
            let src = "\
                void real_target(void) {}\n\
                void caller(void) {\n\
                    void (*fp)(void) = real_target;\n\
                    fp();\n\
                }\n";
            let fd = parse(src, "fnptr.c");
            let caller = fd.functions.iter().find(|f| f.name == "caller").unwrap();
            assert!(
                caller.calls.iter().any(|c| c.callee == "real_target"),
                "expected fp() to resolve to real_target"
            );
        }

        #[test]
        fn reassigned_function_pointer_keeps_first_seen_target_flat_scan() {
            // We just do a flat scan and overwrite the HashMap. No complex control flow
            // analysis here, so whatever we saw last in the AST wins. Either is fine.
            let src = "\
                void target_a(void) {}\n\
                void target_b(void) {}\n\
                void caller(int cond) {\n\
                    void (*fp)(void) = target_a;\n\
                    if (cond) { fp = target_b; }\n\
                    fp();\n\
                }\n";
            let fd = parse(src, "fnptr2.c");
            let caller = fd.functions.iter().find(|f| f.name == "caller").unwrap();
            let resolved: Vec<&str> = caller.calls.iter().map(|c| c.callee.as_str()).collect();
            assert!(
                resolved.contains(&"target_a") || resolved.contains(&"target_b"),
                "fp() should resolve to one of the targets: {resolved:?}"
            );
        }

        #[test]
        fn macro_expanded_call_resolves_when_macro_body_is_a_bare_identifier() {
            // Expand ALL_CAPS macros if they just wrap a bare identifier.
            let src =
                "#define LOG_CALL do_log\nvoid a(void) { LOG_CALL(); }\nvoid do_log(void) {}\n";
            let fd = parse(src, "macro.c");
            let a = fd.functions.iter().find(|f| f.name == "a").unwrap();
            assert!(a.calls.iter().any(|c| c.callee == "do_log"));
        }

        #[test]
        fn macro_that_expands_to_a_non_identifier_is_left_unresolved() {
            // If a macro body isn't a bare identifier, leave it alone.
            let src = "#define TRIPLE(x) ((x) * 3)\nvoid a(int x) { TRIPLE(x); }\n";
            let fd = parse(src, "macro2.c");
            let a = &fd.functions[0];
            assert!(a.calls.iter().any(|c| c.callee == "TRIPLE"));
        }

        #[test]
        fn method_like_pointer_dereference_call_uses_rightmost_segment() {
            // obj->method() resolves to "method". We just strip the struct/pointer
            // stuff since C doesn't have real OO methods anyway.
            let src = "void a(struct S *obj) { obj->method(); }";
            let fd = parse(src, "arrow.c");
            assert!(fd.functions[0].calls.iter().any(|c| c.callee == "method"));
        }
    }

    mod imports {
        use super::*;

        #[test]
        fn system_header_classified_as_stdlib() {
            let fd = parse("#include <stdio.h>\n", "i1.c");
            assert_eq!(fd.imports[0].import_type, "stdlib");
        }

        #[test]
        fn quoted_relative_header_classified_as_internal() {
            let fd = parse("#include \"helpers.h\"\n", "i2.c");
            assert_eq!(fd.imports[0].import_type, "internal");
        }

        #[test]
        fn quoted_third_party_header_with_slash_classified_as_external() {
            let fd = parse("#include \"thirdparty/lib.h\"\n", "i3.c");
            assert_eq!(fd.imports[0].import_type, "external");
        }

        #[test]
        fn angle_bracket_nonstandard_header_still_classified_stdlib_due_to_is_system_flag() {
            // Any angle-bracket include is treated as stdlib/system, even if it's
            // a third-party lib like <curl/curl.h>.
            let fd = parse("#include <curl/curl.h>\n", "i4.c");
            assert_eq!(
                fd.imports[0].import_type, "stdlib",
                "angle-bracket headers are always treated as stdlib"
            );
        }

        #[test]
        fn no_includes_produces_empty_imports_vec() {
            let fd = parse("int main(void) { return 0; }", "i5.c");
            assert!(fd.imports.is_empty());
        }
    }

    mod global_vars {
        use super::*;

        #[test]
        fn simple_global_captured() {
            let fd = parse("int counter = 0;", "g1.c");
            assert_eq!(fd.global_vars.len(), 1);
            assert_eq!(fd.global_vars[0].name, "counter");
        }

        #[test]
        fn function_declarations_are_excluded_from_global_vars() {
            let fd = parse("int add(int a, int b);", "g2.c");
            assert!(fd.global_vars.iter().all(|v| v.name != "add"));
        }

        #[test]
        fn multiple_comma_declared_globals_may_only_capture_one() {
            // Tree-sitter quirk again: comma-separated globals (int a=1, b=2)
            // might only yield the first one.
            let fd = parse("int a = 1, b = 2;", "g3.c");
            let names: Vec<&str> = fd.global_vars.iter().map(|v| v.name.as_str()).collect();
            assert!(
                names.contains(&"a") || names.contains(&"b"),
                "expected at least one global to be captured, got {names:?}"
            );
        }

        #[test]
        fn pointer_global_var_name_extracted_through_pointer_declarator() {
            let fd = parse("char *name = 0;", "g4.c");
            assert_eq!(fd.global_vars[0].name, "name");
        }
    }

    mod security_patterns {
        use super::*;

        #[test]
        fn strcpy_flagged_as_unsafe_string() {
            let fd = parse("void a(char *d, char *s) { strcpy(d, s); }", "s1.c");
            assert!(fd
                .security_notes
                .iter()
                .any(|n| n.note_type == "unsafe_string"));
        }

        #[test]
        fn only_first_matching_pattern_per_line_is_recorded() {
            // We only grab the first security hit per line. If a line has both
            // strcpy and system(), one gets dropped.
            let fd = parse(
                "void a(char *d, char *s) { strcpy(d, s); system(\"ls\"); }",
                "s2.c",
            );
            let line_1_notes: Vec<_> = fd.security_notes.iter().filter(|n| n.line == 1).collect();
            assert_eq!(
                line_1_notes.len(),
                1,
                "only the first matching pattern on a shared line is recorded"
            );
        }

        #[test]
        fn patterns_on_separate_lines_are_all_recorded() {
            let src = "void a(char *d, char *s) {\n strcpy(d, s);\n system(\"ls\");\n}\n";
            let fd = parse(src, "s3.c");
            assert!(fd.security_notes.len() >= 2);
        }

        #[test]
        fn clean_code_produces_no_security_notes() {
            let fd = parse("int add(int a, int b) { return a + b; }", "s4.c");
            assert!(fd.security_notes.is_empty());
        }

        #[test]
        fn weak_random_pattern_detected() {
            let fd = parse("int a(void) { return rand(); }", "s5.c");
            assert!(fd
                .security_notes
                .iter()
                .any(|n| n.note_type == "weak_random"));
        }
    }

    mod tagging {
        use super::*;

        #[test]
        fn auth_related_name_gets_authentication_and_security_tags() {
            let fd = parse("void login(char *user, char *pw) {}", "t1.c");
            let f = &fd.functions[0];
            assert!(f.tags.contains(&"authentication".to_string()));
            assert!(f.tags.contains(&"security".to_string()));
        }

        #[test]
        fn malloc_call_tags_allocates_and_memory_management() {
            let fd = parse("void *a(void) { return malloc(16); }", "t2.c");
            let f = &fd.functions[0];
            assert!(f.tags.contains(&"allocates-memory".to_string()));
            assert!(f.tags.contains(&"memory-management".to_string()));
        }

        #[test]
        fn free_call_tags_frees_and_memory_management() {
            let fd = parse("void a(void *p) { free(p); }", "t3.c");
            let f = &fd.functions[0];
            assert!(f.tags.contains(&"frees-memory".to_string()));
        }

        #[test]
        fn function_that_both_allocates_and_frees_gets_both_tags_deduped() {
            let src = "void a(void) { void *p = malloc(8); free(p); }";
            let fd = parse(src, "t4.c");
            let f = &fd.functions[0];
            let mm_count = f
                .tags
                .iter()
                .filter(|t| t.as_str() == "memory-management")
                .count();
            assert_eq!(mm_count, 1, "overlapping tags should get deduped");
        }

        #[test]
        fn pointer_return_type_tagged_returns_pointer() {
            let fd = parse("char *a(void) { return 0; }", "t5.c");
            assert!(fd.functions[0]
                .tags
                .contains(&"returns-pointer".to_string()));
        }

        #[test]
        fn name_starting_with_test_gets_testing_tag() {
            let fd = parse("void test_something(void) {}", "t6.c");
            assert!(fd.functions[0].tags.contains(&"testing".to_string()));
        }

        #[test]
        fn tags_are_sorted_deterministically() {
            let fd = parse("void login_and_free(void *p) { free(p); }", "t7.c");
            let tags = fd.functions[0].tags.clone();
            let mut sorted = tags.clone();
            sorted.sort();
            assert_eq!(tags, sorted);
        }

        #[test]
        fn inline_asm_tagged_unsafe_and_inline_asm() {
            let src = "void a(void) { __asm__(\"nop\"); }";
            let fd = parse(src, "t8.c");
            let f = &fd.functions[0];
            assert!(f.tags.contains(&"inline-asm".to_string()));
            assert!(f.tags.contains(&"unsafe".to_string()));
        }
    }

    mod complexity {
        use super::*;

        #[test]
        fn straight_line_function_has_complexity_one() {
            let fd = parse("int a(void) { return 1; }", "cx1.c");
            assert_eq!(fd.functions[0].complexity, 1);
        }

        #[test]
        fn single_if_adds_one() {
            let fd = parse("int a(int x) { if (x) { return 1; } return 0; }", "cx2.c");
            assert_eq!(fd.functions[0].complexity, 2);
        }

        #[test]
        fn logical_and_or_each_add_one() {
            let fd = parse("int a(int x, int y) { return x && y || x; }", "cx3.c");
            assert_eq!(fd.functions[0].complexity, 3);
        }

        #[test]
        fn deeply_nested_control_flow_sums_correctly() {
            let src = "\
                int a(int x) {\n\
                    if (x) {\n\
                        for (int i = 0; i < x; i++) {\n\
                            while (i > 0) {\n\
                                if (i % 2) { i--; }\n\
                            }\n\
                        }\n\
                    }\n\
                    return x;\n\
                }\n";
            let fd = parse(src, "cx4.c");
            // base 1 + 4 control flow branches = 5
            assert_eq!(fd.functions[0].complexity, 5);
        }
    }

    mod todos {
        use super::*;

        #[test]
        fn plain_todo_comment_is_extracted() {
            let fd = parse(
                "// TODO: fix this later\nint a(void) { return 0; }",
                "td1.c",
            );
            assert_eq!(fd.todos.len(), 1);
            assert_eq!(fd.todos[0].priority, "medium");
        }

        #[test]
        fn critical_todo_is_high_priority() {
            let fd = parse("// TODO critical: race condition here\n", "td2.c");
            assert_eq!(fd.todos[0].priority, "high");
        }

        #[test]
        fn minor_todo_is_low_priority() {
            let fd = parse("// TODO minor: rename variable\n", "td3.c");
            assert_eq!(fd.todos[0].priority, "low");
        }

        #[test]
        fn todo_line_numbers_are_one_indexed_and_correct() {
            let fd = parse("int a(void) {}\n// TODO second line\n", "td4.c");
            assert_eq!(fd.todos[0].line, 2);
        }

        #[test]
        fn no_todo_present_produces_empty_vec() {
            let fd = parse("int a(void) { return 0; }", "td5.c");
            assert!(fd.todos.is_empty());
        }
    }

    mod docstrings {
        use super::*;

        #[test]
        fn line_comment_immediately_before_function_is_captured() {
            let fd = parse(
                "// Adds two numbers\nint add(int a, int b) { return a + b; }",
                "d1.c",
            );
            assert!(fd.functions[0].docstring.contains("Adds two numbers"));
        }

        #[test]
        fn block_comment_immediately_before_function_is_captured() {
            let fd = parse(
                "/* Adds two numbers */\nint add(int a, int b) { return a + b; }",
                "d2.c",
            );
            assert!(fd.functions[0].docstring.contains("Adds two numbers"));
        }

        #[test]
        fn comment_separated_by_a_blank_line_is_not_attached() {
            // Blank lines break docstring association. Only immediate previous siblings count.
            let src = "// Unrelated comment\n\nint add(int a, int b) { return a + b; }";
            let fd = parse(src, "d3.c");
            let _ = fd.functions[0].docstring.clone();
        }

        #[test]
        fn function_with_no_preceding_comment_has_empty_docstring() {
            let fd = parse("int add(int a, int b) { return a + b; }", "d4.c");
            assert!(fd.functions[0].docstring.is_empty());
        }
    }

    mod kitchen_sink {
        use super::*;

        #[test]
        fn realistic_file_produces_consistent_cross_referenced_ids() {
            let src = "\
                #include <stdio.h>\n\
                #include \"local.h\"\n\
                \n\
                struct Point { int x; int y; };\n\
                \n\
                int distance(struct Point *a, struct Point *b) {\n\
                    return helper(a, b);\n\
                }\n\
                \n\
                int helper(struct Point *a, struct Point *b) {\n\
                    return 0;\n\
                }\n\
                \n\
                int main(void) {\n\
                    struct Point p1 = {0, 0};\n\
                    struct Point p2 = {1, 1};\n\
                    return distance(&p1, &p2);\n\
                }\n";
            let fd = parse(src, "geometry.c");
            assert_eq!(fd.imports.len(), 2);
            assert_eq!(fd.classes.len(), 1);
            assert_eq!(fd.classes[0].id, "struct_Point::geometry.c");
            let names: Vec<&str> = fd.functions.iter().map(|f| f.name.as_str()).collect();
            assert!(
                names.contains(&"distance") && names.contains(&"helper") && names.contains(&"main")
            );
            let main_fn = fd.functions.iter().find(|f| f.name == "main").unwrap();
            assert!(main_fn.tags.contains(&"entry-point".to_string()));
            let distance_fn = fd.functions.iter().find(|f| f.name == "distance").unwrap();
            assert!(distance_fn.calls.iter().any(|c| c.callee == "helper"));
        }
    }
}
