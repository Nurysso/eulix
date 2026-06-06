//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::kb::types::*;
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

struct TagRule {
    keywords: &'static [&'static str],
    tag: &'static str,
    check_docstring: bool,
}

static UNSAFE_STRING_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"strcpy|strcat|sprintf|vsprintf|gets|wcscpy|wcscat|_mbscpy").unwrap());
static COMMAND_EXEC_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"system\(|popen\(|exec|CreateProcess|ShellExecute|std::system").unwrap()
});
static MANUAL_MEMORY_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"malloc|calloc|realloc|free|new\s|new\[|delete\s|delete\[\]").unwrap()
});
static UNSAFE_INPUT_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"scanf|fscanf|cin\s*>>|gets_s").unwrap());
static MEMORY_OP_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"memcpy|memmove|memset|std::memcpy|std::memmove|std::memset").unwrap()
});
static PRIVILEGE_CHANGE_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"setuid|setgid|seteuid|SetTokenInformation|AdjustTokenPrivileges").unwrap()
});
static WEAK_RANDOM_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"rand\(\)|random\(\)|std::rand\(\)").unwrap());
static RAW_POINTER_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"new\s+(?:std::|make_unique|make_shared)\w+|new\s+\w+|delete\s+\w+").unwrap()
});
static REINTERPRET_CAST_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"reinterpret_cast\s*<").unwrap());
static C_STYLE_CAST_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\(\s*\w+\s*\)\s*\w+").unwrap());
static EXCEPTION_SAFETY_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"catch\s*\(\.\.\.\)|noexcept\s*\(\s*false\s*\)").unwrap());

static INCLUDE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"^#include\s+[<"]([^>"]+)[>"]"#).expect("Invalid include regex"));

static TODO_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"(?://|/\*)\s*TODO:?\s*(.+?)(?:\*/|$)").expect("Invalid todo regex"));

static MACRO_DEFINE_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?m)^#define\s+([A-Za-z_]\w*)(?:\([^)]*\))?\s+(.+)$")
        .expect("Invalid macro define regex")
});

static MALLOC_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"malloc|calloc|realloc|alloca|new\s|new\[").unwrap());
static FREE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"\bfree\b|\bdelete\b|\bdelete\[\]\b").unwrap());
static THREAD_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"std::thread|pthread|std::async|std::future|fork|std::jthread").unwrap()
});
static MUTEX_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"std::mutex|std::lock_guard|std::unique_lock|std::shared_lock|std::scoped_lock")
        .unwrap()
});
static SYSCALL_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"syscall|ioctl|fcntl").unwrap());
static STRING_OPS_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"strcpy|strcat|sprintf|strncpy|std::string::c_str|std::string::data").unwrap()
});
static SMART_POINTER_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"std::unique_ptr|std::shared_ptr|std::weak_ptr|std::make_unique|std::make_shared")
        .unwrap()
});
static TEMPLATE_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"template\s*<|typename|constexpr|consteval|constinit").unwrap());
static LAMBDA_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\[\s*[=&\w]*\s*\]\s*\(").unwrap());
static NAMESPACE_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\bnamespace\s+(\w+)").unwrap());
static CLASS_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\bclass\s+(\w+)").unwrap());
static STRUCT_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\bstruct\s+(\w+)").unwrap());
static VIRTUAL_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"\bvirtual\b|\boverride\b|\bfinal\b").unwrap());
static RTTI_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r"\btypeid\b|\bdynamic_cast\s*<").unwrap());
static OPERATOR_OVERLOAD_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"\boperator\s*[+\-*/%=<>!&|^~\[\]()]+\s*\(").unwrap());

static SECURITY_PATTERNS: Lazy<Vec<SecurityPattern>> = Lazy::new(|| {
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
            description: "Manual memory management (prefer RAII)",
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
            regex: &RAW_POINTER_RE,
            note_type: "raw_pointer",
            description: "Raw pointer new/delete (prefer smart pointers)",
        },
        SecurityPattern {
            regex: &REINTERPRET_CAST_RE,
            note_type: "reinterpret_cast",
            description: "Potentially unsafe reinterpret_cast",
        },
        SecurityPattern {
            regex: &C_STYLE_CAST_RE,
            note_type: "c_style_cast",
            description: "C-style cast (prefer C++ casts)",
        },
        SecurityPattern {
            regex: &EXCEPTION_SAFETY_RE,
            note_type: "exception_safety",
            description: "Potential exception safety issue",
        },
    ]
});

static TAG_RULES: Lazy<Vec<TagRule>> = Lazy::new(|| {
    vec![
        TagRule {
            keywords: &[
                "init",
                "setup",
                "initialize",
                "bootstrap",
                "constructor",
                "Construct",
            ],
            tag: "initialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "free",
                "cleanup",
                "destroy",
                "dispose",
                "close",
                "shutdown",
                "destructor",
                "Destruct",
            ],
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
            keywords: &[
                "api", "endpoint", "route", "handler", "serve", "rest", "graphql",
            ],
            tag: "api",
            check_docstring: true,
        },
        TagRule {
            keywords: &[
                "db", "database", "query", "select", "insert", "update", "delete", "sql",
            ],
            tag: "database",
            check_docstring: false,
        },
        TagRule {
            keywords: &["validate", "check", "verify", "sanitize", "assert"],
            tag: "validation",
            check_docstring: false,
        },
        TagRule {
            keywords: &["error", "exception", "throw", "catch"],
            tag: "error-handling",
            check_docstring: false,
        },
        TagRule {
            keywords: &["util", "helper", "utility"],
            tag: "utility",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "read", "write", "file", "open", "stream", "fstream", "ifstream", "ofstream",
            ],
            tag: "file-io",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "socket", "connect", "send", "receive", "network", "http", "tcp", "udp",
            ],
            tag: "network",
            check_docstring: false,
        },
        TagRule {
            keywords: &["config", "setting", "option", "parameter"],
            tag: "configuration",
            check_docstring: false,
        },
        TagRule {
            keywords: &["log", "debug", "trace", "print"],
            tag: "logging",
            check_docstring: false,
        },
        TagRule {
            keywords: &["parse", "decode", "deserialize", "unmarshal"],
            tag: "parsing",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "serialize",
                "encode",
                "marshal",
                "to_string",
                "to_json",
                "to_xml",
            ],
            tag: "serialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "signal", "slot", "callback", "observer", "listener", "notify", "emit",
            ],
            tag: "signal-handling",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "thread",
                "async",
                "promise",
                "future",
                "coroutine",
                "mutex",
                "lock",
            ],
            tag: "concurrency",
            check_docstring: false,
        },
        TagRule {
            keywords: &["template", "generic", "concept", "typename"],
            tag: "template",
            check_docstring: false,
        },
        TagRule {
            keywords: &["operator"],
            tag: "operator-overload",
            check_docstring: false,
        },
        TagRule {
            keywords: &["virtual", "override", "final", "abstract", "interface"],
            tag: "polymorphism",
            check_docstring: false,
        },
        TagRule {
            keywords: &["move", "forward", "emplace", "swap"],
            tag: "move-semantics",
            check_docstring: false,
        },
        TagRule {
            keywords: &["constexpr", "consteval", "constinit"],
            tag: "compile-time",
            check_docstring: false,
        },
    ]
});

pub struct CppParser {
    source_code: String,
    macro_defs: HashMap<String, String>,
    /// function_ptr_var -> resolved callee name (best-effort)
    fn_ptr_map: HashMap<String, String>,
    /// type aliases from typedef/using (for resolving complex types)
    type_aliases: HashMap<String, String>,
}

impl CppParser {
    pub fn new(source_code: String) -> Self {
        let macro_defs = Self::pre_scan_macros(&source_code);
        let type_aliases = Self::pre_scan_type_aliases(&source_code);
        Self {
            source_code,
            macro_defs,
            fn_ptr_map: HashMap::new(),
            type_aliases,
        }
    }

    // Pre-scan: collect #define NAME(...) body
    fn pre_scan_macros(src: &str) -> HashMap<String, String> {
        let mut map = HashMap::new();
        // Matches both object-like and function-like macros, handles line continuations
        for caps in MACRO_DEFINE_RE.captures_iter(src) {
            let name = caps[1].to_string();
            let body = caps[2].trim_end_matches('\\').trim().to_string();
            map.insert(name, body);
        }
        map
    }

    fn pre_scan_type_aliases(src: &str) -> HashMap<String, String> {
        let mut map = HashMap::new();

        // typedef existing_type new_name;
        static TYPEDEF_RE: Lazy<Regex> =
            Lazy::new(|| Regex::new(r"(?m)^\s*typedef\s+(.+?)\s+(\w+)\s*;").unwrap());

        // using new_name = existing_type;
        static USING_ALIAS_RE: Lazy<Regex> =
            Lazy::new(|| Regex::new(r"(?m)^\s*using\s+(\w+)\s*=\s*(.+?)\s*;").unwrap());

        for caps in TYPEDEF_RE.captures_iter(src) {
            map.insert(caps[2].to_string(), caps[1].trim().to_string());
        }

        for caps in USING_ALIAS_RE.captures_iter(src) {
            map.insert(caps[1].to_string(), caps[2].trim().to_string());
        }

        map
    }

    pub fn parse(&mut self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_cpp::language())
            .map_err(|e| format!("Failed to load C++ grammar: {}", e))?;

        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse C++ file".to_string())?;

        let root = tree.root_node();

        // Build function-ponter and std::function resolution map
        self.fn_ptr_map = self.build_fn_ptr_map(&root);

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

    fn build_fn_ptr_map(&self, root: &Node) -> HashMap<String, String> {
        let mut map = HashMap::new();
        self.scan_fn_ptr_assignments(root, &mut map);
        self.scan_std_function_assignments(root, &mut map);
        map
    }

    fn scan_fn_ptr_assignments(&self, node: &Node, map: &mut HashMap<String, String>) {
        let mut cursor = node.walk();

        // Direct function pointer assignment: fp = &func or fp = func
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

                // Only record if rhs looks like a plain identifier
                if rhs_text.chars().all(|c| c.is_alphanumeric() || c == '_') && !rhs_text.is_empty()
                {
                    map.insert(lhs_text, rhs_text);
                }
            }
        }

        // Initialization: fn_ptr_t fp = actual_func; or auto fp = &Class::method;
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

                if !name.is_empty() && !val_text.is_empty() {
                    // Check if it looks like a function name (possibly qualified)
                    if val_text
                        .chars()
                        .all(|c| c.is_alphanumeric() || c == '_' || c == ':')
                    {
                        map.insert(name, val_text);
                    }
                }
            }
        }

        // Constructor initializer lists: Class() : callback(func) {}
        if node.kind() == "field_initializer" {
            let field = node.child_by_field_name("field");
            let value = node.child_by_field_name("value");
            if let (Some(f), Some(v)) = (field, value) {
                let field_name = self.get_node_text(&f);
                let val_text = self
                    .get_node_text(&v)
                    .trim_start_matches('&')
                    .trim()
                    .to_string();

                if val_text.chars().all(|c| c.is_alphanumeric() || c == '_') && !val_text.is_empty()
                {
                    map.insert(field_name, val_text);
                }
            }
        }

        for child in node.children(&mut cursor) {
            self.scan_fn_ptr_assignments(&child, map);
        }
    }
    fn scan_std_function_assignments(&self, node: &Node, map: &mut HashMap<String, String>) {
        let mut cursor = node.walk();

        // std::function<void(int)> f = &Class::method;
        // auto f = std::bind(&func, args...)
        if node.kind() == "init_declarator" || node.kind() == "assignment_expression" {
            let (decl_name, value_node) = if node.kind() == "init_declarator" {
                (
                    node.child_by_field_name("declarator")
                        .map(|d| self.extract_declarator_name(&d)),
                    node.child_by_field_name("value"),
                )
            } else {
                (
                    node.child_by_field_name("left")
                        .map(|l| Some(self.get_node_text(&l)))
                        .flatten(),
                    node.child_by_field_name("right"),
                )
            };

            if let (Some(name), Some(val)) = (decl_name, value_node) {
                let val_text = self.get_node_text(&val);

                // std::function assignment
                if val_text.contains("std::bind") || val_text.contains("std::function") {
                    // Extract the actual function from std::bind(&func, ...)
                    if let Some(func_name) = self.extract_bound_function(&val_text) {
                        map.insert(name, func_name);
                    }
                }
            }
        }

        for child in node.children(&mut cursor) {
            self.scan_std_function_assignments(&child, map);
        }
    }

    fn extract_bound_function(&self, bind_expr: &str) -> Option<String> {
        static BIND_RE: Lazy<Regex> =
            Lazy::new(|| Regex::new(r"std::bind\s*\(\s*&?(\w+(?:::?\w+)*)").unwrap());

        BIND_RE.captures(bind_expr).map(|caps| caps[1].to_string())
    }

    fn resolve_callee(&self, raw: &str, fn_ptr_map: &HashMap<String, String>) -> String {
        let trimmed = raw.trim();

        // Handle C++ member function calls: obj->method(), obj.method(), Class::method()
        let base = if let Some(last_part) = trimmed
            .rsplit("->")
            .next()
            .or_else(|| trimmed.rsplit('.').next())
            .or_else(|| trimmed.rsplit("::").next())
        {
            last_part.trim()
        } else {
            trimmed
        };

        // Remove template arguments: func<int, float> -> func
        let base = if let Some(template_pos) = base.find('<') {
            base[..template_pos].trim()
        } else {
            base
        };

        // Strip leading * (dereference) and & (address-of)
        let base = base.trim_start_matches('*').trim_start_matches('&');

        if base.is_empty() {
            return String::new();
        }

        // 1. Function-pointer map lookup
        if let Some(real) = fn_ptr_map.get(base) {
            return real.clone();
        }

        // 2. Macro expansion
        if base.chars().all(|c| c.is_uppercase() || c == '_') {
            if let Some(expansion) = self.macro_defs.get(base) {
                let exp = expansion.trim();
                // Only use if expands to a single identifier
                if exp.chars().all(|c| c.is_alphanumeric() || c == '_') {
                    return exp.to_string();
                }
            }
        }

        // 3. Type alias resolution (for std::function callbacks etc.)
        if let Some(resolved_type) = self.type_aliases.get(base) {
            // If the alias points to a function pointer type, extract the name
            if resolved_type.contains('(') && resolved_type.contains('*') {
                return resolved_type.clone();
            }
        }

        base.to_string()
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
                if let Some(caps) = INCLUDE_RE.captures(&text) {
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
        self.collect_functions(root, &mut functions, "", &self.fn_ptr_map);
        functions
    }

    fn collect_functions(
        &self,
        node: &Node,
        functions: &mut Vec<Function>,
        ctx: &str,
        fn_ptr_map: &HashMap<String, String>,
    ) {
        let mut cursor = node.walk();

        for child in node.children(&mut cursor) {
            match child.kind() {
                "function_definition" => {
                    if let Some(func) = self.parse_function(&child, ctx, fn_ptr_map, None) {
                        functions.push(func);
                    }
                }
                "namespace_definition" => {
                    // Recurse into namespaces
                    if let Some(body) = child.child_by_field_name("body") {
                        self.collect_functions(&body, functions, ctx, fn_ptr_map);
                    }
                }
                _ => {}
            }
        }
    }

    fn parse_function(
        &self,
        node: &Node,
        struct_context: &str,
        fn_ptr_map: &HashMap<String, String>,
        access: Option<String>,
    ) -> Option<Function> {
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
                    self.extract_function_calls_detailed(&body, fn_ptr_map),
                    self.extract_variables(&body, &params),
                    self.build_control_flow(&body),
                    self.calculate_complexity(&body),
                )
            } else {
                (vec![], vec![], ControlFlow::default(), 1)
            };

        let body_text = node
            .child_by_field_name("body")
            .map(|b| self.get_node_text(&b))
            .unwrap_or_default();
        let tags = self.auto_tag_function(&name, &docstring, &calls, &return_type, &body_text);
        let importance_score = self.estimate_importance(&name, !struct_context.is_empty());

        //  Building template
        let template_params = if let Some(parent) = node.parent() {
            if parent.kind() == "template_declaration" {
                self.extract_template_params(&parent)
            } else {
                vec![]
            }
        } else {
            vec![]
        };

        let cpp_info = self.extract_cpp_info(&node, &name, struct_context, access, template_params);

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
            // lang_info: LanguageSpecificInfo::default(),
            lang_info: LanguageSpecificInfo {
                cpp: Some(cpp_info),
                ..Default::default()
            },
        })
    }

    fn extract_template_params(&self, template_node: &Node) -> Vec<String> {
        let mut params = vec![];
        if let Some(param_list) = template_node.child_by_field_name("parameters") {
            let mut cursor = param_list.walk();
            for child in param_list.children(&mut cursor) {
                if child.kind() == "template_parameter" {
                    params.push(self.get_node_text(&child).trim().to_string());
                }
            }
        }
        params
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
        // let class_text = self.get_node_text(node);
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
            // lang_info: self.extract_lang_info(&class_text),
            lang_info: LanguageSpecificInfo::default(),
        })
    }

    fn extract_class_body(&self, body: &Node, class_name: &str) -> (Vec<Attribute>, Vec<Function>) {
        let mut attributes = Vec::new();
        let mut methods = Vec::new();
        let mut cursor = body.walk();
        let mut current_access = String::from("private");
        for child in body.children(&mut cursor) {
            match child.kind() {
                "access_specifier" => {
                    // eg "public:", "protected:", "private:"
                    let text = self.get_node_text(&child);
                    if text.contains("public") {
                        current_access = "public".to_string();
                    } else if text.contains("protected") {
                        current_access = "protected".to_string();
                    } else {
                        current_access = "private".to_string();
                    }
                }
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
                    if let Some(method) = self.parse_function(
                        &child,
                        class_name,
                        &self.fn_ptr_map,
                        Some(current_access.clone()),
                    ) {
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
                let resolved = self.resolve_callee(&call_text, fn_ptr_map);
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

        for child in node.children(&mut cursor) {
            self.find_calls_recursive(&child, calls, seen, child_context, fn_ptr_map);
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

    // Security patterns

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
        body_text: &str, // <-- new parameter
    ) -> Vec<String> {
        let mut tags = HashSet::new();
        let lower = name.to_lowercase();
        let doc_lower = doc.to_lowercase();

        // Apply TAG_RULES first
        for rule in TAG_RULES.iter() {
            let search_in = if rule.check_docstring {
                format!("{} {}", lower, doc_lower)
            } else {
                lower.clone()
            };
            if rule.keywords.iter().any(|kw| search_in.contains(kw)) {
                tags.insert(rule.tag.to_string());
            }
        }

        // Existing heuristics
        if lower.contains("init") || lower.starts_with("create") || lower.starts_with("make") {
            tags.insert("constructor".to_string());
        }
        if lower.starts_with("test") || lower.starts_with("check") {
            tags.insert("test".to_string());
        }
        if return_type.contains("bool") || lower.starts_with("is_") || lower.starts_with("has_") {
            tags.insert("predicate".to_string());
        }
        if doc_lower.contains("thread-safe") || doc_lower.contains("mutex") {
            tags.insert("concurrent".to_string());
        }
        if calls
            .iter()
            .any(|c| c.callee.contains("throw") || c.callee.contains("assert"))
        {
            tags.insert("error-handling".to_string());
        }

        // --- New pattern‑based tags ---
        // Memory
        if MALLOC_RE.is_match(body_text) {
            tags.insert("heap-allocation".to_string());
        }
        if FREE_RE.is_match(body_text) {
            tags.insert("manual-free".to_string());
        }
        if SMART_POINTER_RE.is_match(body_text) {
            tags.insert("smart-pointer".to_string());
        }

        // Concurrency
        if THREAD_RE.is_match(body_text) {
            tags.insert("threading".to_string());
        }
        if MUTEX_RE.is_match(body_text) {
            tags.insert("synchronization".to_string());
        }

        // System
        if SYSCALL_RE.is_match(body_text) {
            tags.insert("syscall".to_string());
        }

        // Strings
        if STRING_OPS_RE.is_match(body_text) {
            tags.insert("string-ops".to_string());
        }

        // C++ features
        if TEMPLATE_RE.is_match(body_text) {
            tags.insert("template".to_string());
        }
        if LAMBDA_RE.is_match(body_text) {
            tags.insert("lambda".to_string());
        }
        if VIRTUAL_RE.is_match(body_text) {
            tags.insert("virtual".to_string());
        }
        if RTTI_RE.is_match(body_text) {
            tags.insert("rtti".to_string());
        }
        if OPERATOR_OVERLOAD_RE.is_match(body_text) {
            tags.insert("operator-overload".to_string());
        }

        // Structural (class-level only – you may skip these for functions)
        // For functions inside a namespace/class, you can detect from context.
        // If you want to keep them, they're fine.
        if NAMESPACE_RE.is_match(body_text) {
            tags.insert("namespaced".to_string());
        }
        if CLASS_RE.is_match(body_text) {
            tags.insert("defines-class".to_string());
        }
        if STRUCT_RE.is_match(body_text) {
            tags.insert("defines-struct".to_string());
        }

        let mut result: Vec<String> = tags.into_iter().collect();
        result.sort();
        result
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

    fn extract_cpp_info(
        &self,
        func_node: &Node,
        name: &str,
        struct_context: &str,
        access: Option<String>,       // from class body scanning
        template_params: Vec<String>, // filled later
    ) -> CppInfo {
        let mut info = CppInfo::default();
        let text = self.get_node_text(func_node);

        // --- Analyse children by kind -------------------------------------------------
        let mut cursor = func_node.walk();
        for child in func_node.children(&mut cursor) {
            match child.kind() {
                "storage_class_specifier" => {
                    let kw = self.get_node_text(&child);
                    if kw == "static" {
                        info.is_static = true;
                    }
                    // you can also catch "extern", "mutable", etc. if needed
                }
                "virtual_specifier" => {
                    info.is_virtual = true;
                }
                "override_specifier" => {
                    info.is_override = true;
                }
                "final_specifier" => {
                    info.is_final = true;
                }
                "pure_virtual_specifier" => {
                    // tree-sitter-cpp has this?
                    info.is_pure_virtual = true;
                }
                _ => {}
            }
        }

        // --- Things best checked with a simple substring (already parsed) ------------
        // pure virtual "= 0"
        if text.contains("= 0") {
            info.is_pure_virtual = true;
        }

        // const method (const after parameter list)
        if let Some(decl) = func_node.child_by_field_name("declarator") {
            let decl_text = self.get_node_text(&decl);
            if decl_text.contains(") const")
                || decl_text.contains(") const ")
                || decl_text.ends_with(") const")
            {
                info.is_const_method = true;
            }
        }

        // noexcept
        if text.contains("noexcept") {
            info.is_noexcept = true;
        }

        // explicit (only meaningful for constructors, but we record it anyway)
        if text.contains("explicit") {
            info.is_explicit = true;
        }

        // constexpr / consteval / constinit
        if text.contains("constexpr") || text.contains("consteval") || text.contains("constinit") {
            info.is_constexpr = true;
        }

        // inline – if the text starts with "inline"
        if text.trim_start().starts_with("inline") {
            info.is_inline = true;
        }

        // constructor / destructor (by name matching)
        if !struct_context.is_empty() {
            if name == struct_context {
                info.is_constructor = true;
            } else if name.starts_with('~') {
                info.is_destructor = true;
            }
        }

        // access specifier
        info.access_specifier = access;

        // template parameters (passed from parent detection)
        info.template_params = template_params;

        info
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
    let mut parser = CppParser::new(source);
    let file_data = parser
        .parse()
        .map_err(|e| -> Box<dyn std::error::Error> { e.into() })?;
    let name = path.to_string_lossy().to_string();
    Ok((name, file_data))
}
