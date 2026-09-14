//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

use crate::kb_struct::*;
use once_cell::sync::Lazy as LazyLock;
use regex::bytes::Regex;
use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::str;
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

static SQL_INJECTION_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"createStatement\(|executeQuery\(|executeUpdate\(")
        .expect("static sql injection regex pattern is valid")
});
static COMMAND_EXEC_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"Runtime\.getRuntime\(\)\.exec\(|new ProcessBuilder\(")
        .expect("static command execution regex pattern is valid")
});
static DESERIALIZATION_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"ObjectInputStream|readObject\(|readUnshared\(")
        .expect("static deserialization regex pattern is valid")
});
static XXE_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"DocumentBuilderFactory|SAXParserFactory|XMLInputFactory|TransformerFactory")
        .expect("static XXE regex pattern is valid")
});
static WEAK_CRYPTO_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?i)getInstance\(\s*"(DES|RC4|MD5|SHA1)"|/ECB/"#)
        .expect("static weak crypto regex pattern is valid")
});
static WEAK_RANDOM_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"Math\.random\(\)|new Random\(").expect("static weak random regex pattern is valid")
});
static HARDCODED_CREDENTIAL_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?i)(password|passwd|secret|api[_-]?key)\s*=\s*"[^"]+""#)
        .expect("static hardcoded credential regex pattern is valid")
});
static TRUST_MANAGER_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"TrustManager|checkClientTrusted|checkServerTrusted|HostnameVerifier")
        .expect("static trust manager regex pattern is valid")
});
static REFLECTION_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"setAccessible\(\s*true\s*\)|Class\.forName\(")
        .expect("static reflection regex pattern is valid")
});
static PATH_TRAVERSAL_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"new File\(.*(getParameter|request\.)")
        .expect("static path traversal regex pattern is valid")
});
static NATIVE_CODE_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"System\.loadLibrary\(|System\.load\(")
        .expect("static native code regex pattern is valid")
});

static SECURITY_PATTERNS: LazyLock<Vec<SecurityPattern>> = LazyLock::new(|| {
    vec![
        SecurityPattern {
            regex: &SQL_INJECTION_RE,
            note_type: "sql_injection",
            description: "Potential SQL injection via JDBC statement",
        },
        SecurityPattern {
            regex: &COMMAND_EXEC_RE,
            note_type: "command_execution",
            description: "System command execution",
        },
        SecurityPattern {
            regex: &DESERIALIZATION_RE,
            note_type: "unsafe_deserialization",
            description: "Unsafe Java deserialization",
        },
        SecurityPattern {
            regex: &XXE_RE,
            note_type: "xxe_risk",
            description: "XML parser potentially vulnerable to XXE",
        },
        SecurityPattern {
            regex: &WEAK_CRYPTO_RE,
            note_type: "weak_crypto",
            description: "Weak or broken cryptographic algorithm",
        },
        SecurityPattern {
            regex: &WEAK_RANDOM_RE,
            note_type: "weak_random",
            description: "Weak random number generator",
        },
        SecurityPattern {
            regex: &HARDCODED_CREDENTIAL_RE,
            note_type: "hardcoded_credential",
            description: "Hardcoded credential or secret",
        },
        SecurityPattern {
            regex: &TRUST_MANAGER_RE,
            note_type: "trust_manager",
            description: "Custom TLS trust/hostname verification",
        },
        SecurityPattern {
            regex: &REFLECTION_RE,
            note_type: "reflection",
            description: "Reflection used to bypass access checks",
        },
        SecurityPattern {
            regex: &PATH_TRAVERSAL_RE,
            note_type: "path_traversal",
            description: "File path built from user input",
        },
        SecurityPattern {
            regex: &NATIVE_CODE_RE,
            note_type: "native_code",
            description: "Loads native code",
        },
    ]
});

static TODO_RE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?://|/\*)\s*(?:TODO|FIXME|XXX)[:\s]*(.*?)(?:\*/\s*)?$")
        .expect("static TODO comment regex pattern is valid")
});

static TAG_RULES: LazyLock<Vec<TagRule>> = LazyLock::new(|| {
    vec![
        TagRule {
            keywords: &["init", "setup", "initialize", "bootstrap"],
            tag: "initialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &[
                "close", "cleanup", "destroy", "dispose", "shutdown", "release",
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
            keywords: &["controller", "endpoint", "mapping", "handler", "resource"],
            tag: "api",
            check_docstring: true,
        },
        TagRule {
            keywords: &[
                "repository",
                "dao",
                "query",
                "select",
                "insert",
                "update",
                "delete",
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
            keywords: &["error", "exception", "fail"],
            tag: "error-handling",
            check_docstring: false,
        },
        TagRule {
            keywords: &["util", "helper"],
            tag: "utility",
            check_docstring: false,
        },
        TagRule {
            keywords: &["read", "write", "file", "stream"],
            tag: "file-io",
            check_docstring: false,
        },
        TagRule {
            keywords: &["socket", "connect", "send", "receive", "client", "server"],
            tag: "network",
            check_docstring: false,
        },
        TagRule {
            keywords: &["config", "properties", "setting"],
            tag: "configuration",
            check_docstring: false,
        },
        TagRule {
            keywords: &["log", "logger", "debug"],
            tag: "logging",
            check_docstring: false,
        },
        TagRule {
            keywords: &["parse", "decode"],
            tag: "parsing",
            check_docstring: false,
        },
        TagRule {
            keywords: &["serialize", "encode", "marshal"],
            tag: "serialization",
            check_docstring: false,
        },
        TagRule {
            keywords: &["thread", "executor", "runnable", "callable", "async"],
            tag: "concurrency",
            check_docstring: false,
        },
        TagRule {
            keywords: &["cache"],
            tag: "caching",
            check_docstring: false,
        },
        TagRule {
            keywords: &["event", "listener", "observer"],
            tag: "event-driven",
            check_docstring: false,
        },
    ]
});

/// Direct (non-recursive) search for a child of the given kind. Free function so its
/// returned borrow is tied only to `node`'s lifetime, not to `&self`.
fn find_child_by_kind<'a>(node: &Node<'a>, kind: &str) -> Option<Node<'a>> {
    let mut cursor = node.walk();
    node.children(&mut cursor).find(|c| c.kind() == kind)
}

#[derive(Default)]
struct ModifierInfo {
    access: Option<String>,
    flags: HashSet<String>,
    annotations: Vec<String>,
}

pub struct JavaParser {
    source_code: String,
    file_path: String,
    package_name: Option<String>,
}

impl JavaParser {
    pub fn new(source_code: String, file_path: String) -> Self {
        Self {
            source_code,
            file_path,
            package_name: None,
        }
    }

    fn make_type_id(&self, qualified_name: &str, kind_label: &str) -> String {
        format!("{}_{}::{}", kind_label, qualified_name, self.file_path)
    }

    fn make_method_id(&self, qualified_class: &str, method_name: &str, is_ctor: bool) -> String {
        if qualified_class.is_empty() {
            format!("func_{}::{}", method_name, self.file_path)
        } else if is_ctor {
            format!(
                "ctor_{}_{}::{}",
                qualified_class, method_name, self.file_path
            )
        } else {
            format!(
                "method_{}_{}::{}",
                qualified_class, method_name, self.file_path
            )
        }
    }

    pub fn parse(&mut self) -> Result<FileData, String> {
        let mut parser = Parser::new();
        parser
            .set_language(tree_sitter_java::language())
            .map_err(|e| format!("Failed to load Java grammar: {}", e))?;
        let tree = parser
            .parse(&self.source_code, None)
            .ok_or_else(|| "Failed to parse Java file".to_string())?;
        let root = tree.root_node();
        self.package_name = self.extract_package(&root);
        let mut classes = Vec::new();
        let mut stack: Vec<String> = Vec::new();
        self.walk_types(&root, &mut stack, &mut classes);
        Ok(FileData {
            language: "java".to_string(),
            loc: self.count_lines(),
            imports: self.extract_imports(&root),
            functions: vec![],
            classes,
            global_vars: vec![],
            todos: self.extract_todos(),
            security_notes: self.detect_security_patterns(),
        })
    }

    fn count_lines(&self) -> usize {
        self.source_code.lines().count()
    }

    fn get_node_text(&self, node: &Node) -> String {
        node.utf8_text(self.source_code.as_bytes())
            .unwrap_or("")
            .to_string()
    }

    fn extract_package(&self, root: &Node) -> Option<String> {
        let mut cursor = root.walk();
        for child in root.children(&mut cursor) {
            if child.kind() == "package_declaration" {
                let mut c2 = child.walk();
                for gc in child.children(&mut c2) {
                    if gc.kind() == "scoped_identifier" || gc.kind() == "identifier" {
                        return Some(self.get_node_text(&gc));
                    }
                }
            }
        }
        None
    }

    fn classify_import(&self, path: &str) -> String {
        if path.starts_with("java.") || path.starts_with("javax.") || path.starts_with("jakarta.") {
            "stdlib".to_string()
        } else if let Some(pkg) = &self.package_name {
            if !pkg.is_empty() && path.starts_with(pkg.as_str()) {
                "internal".to_string()
            } else {
                "external".to_string()
            }
        } else {
            "external".to_string()
        }
    }

    fn extract_imports(&self, root: &Node) -> Vec<Import> {
        let mut imports = Vec::new();
        let mut cursor = root.walk();
        for child in root.children(&mut cursor) {
            if child.kind() != "import_declaration" {
                continue;
            }
            let text = self.get_node_text(&child);
            let is_static = text.trim_start().starts_with("import static");
            let is_wildcard = find_child_by_kind(&child, "asterisk").is_some();
            let mut path = String::new();
            let mut c2 = child.walk();
            for gc in child.children(&mut c2) {
                if gc.kind() == "scoped_identifier" || gc.kind() == "identifier" {
                    path = self.get_node_text(&gc);
                }
            }
            if path.is_empty() {
                continue;
            }
            let base_type = self.classify_import(&path);
            let import_type = if is_static {
                format!("static_{}", base_type)
            } else {
                base_type
            };
            let items = if is_wildcard {
                vec!["*".to_string()]
            } else if let Some(idx) = path.rfind('.') {
                vec![path[idx + 1..].to_string()]
            } else {
                vec![path.clone()]
            };
            let module = if is_wildcard {
                format!("{}.*", path)
            } else {
                path.clone()
            };
            imports.push(Import {
                module,
                items,
                import_type,
            });
        }
        imports
    }

    fn parse_modifiers(&self, node: Option<Node>) -> ModifierInfo {
        let mut info = ModifierInfo::default();
        let Some(node) = node else { return info };
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "annotation" | "marker_annotation" => {
                    if let Some(name_node) = child.child_by_field_name("name") {
                        info.annotations
                            .push(format!("@{}", self.get_node_text(&name_node)));
                    }
                }
                "public" | "private" | "protected" => {
                    info.access = Some(child.kind().to_string());
                }
                "static" | "final" | "abstract" | "synchronized" | "native" | "strictfp"
                | "default" | "transient" | "volatile" | "sealed" | "non-sealed" => {
                    info.flags.insert(child.kind().to_string());
                }
                _ => {}
            }
        }
        info
    }

    fn extract_types_from_container(&self, container: &Node) -> Vec<String> {
        if let Some(type_list) = find_child_by_kind(container, "type_list") {
            let mut out = Vec::new();
            let mut cursor = type_list.walk();
            for child in type_list.named_children(&mut cursor) {
                out.push(self.get_node_text(&child));
            }
            out
        } else {
            let text = self.get_node_text(container);
            let cleaned = text
                .trim_start_matches("implements")
                .trim_start_matches("extends")
                .trim_start_matches("permits")
                .trim();
            if cleaned.is_empty() {
                vec![]
            } else {
                vec![cleaned.to_string()]
            }
        }
    }

    fn first_named_child_text(&self, node: &Node) -> Option<String> {
        let mut cursor = node.walk();
        node.named_children(&mut cursor)
            .next()
            .map(|c| self.get_node_text(&c))
    }

    fn extract_javadoc(&self, node: &Node) -> String {
        if let Some(prev) = node.prev_sibling() {
            if prev.kind() == "block_comment"
                || prev.kind() == "line_comment"
                || prev.kind() == "comment"
            {
                return self.clean_comment(&self.get_node_text(&prev));
            }
        }
        String::new()
    }

    fn clean_comment(&self, text: &str) -> String {
        text.lines()
            .map(|l| l.trim())
            .map(|l| {
                l.trim_start_matches("/**")
                    .trim_start_matches("/*")
                    .trim_end_matches("*/")
            })
            .map(|l| l.trim_start_matches('*').trim())
            .filter(|l| !l.is_empty())
            .collect::<Vec<_>>()
            .join(" ")
    }

    fn walk_types(&self, node: &Node, stack: &mut Vec<String>, classes: &mut Vec<Class>) {
        let is_type_decl = matches!(
            node.kind(),
            "class_declaration"
                | "interface_declaration"
                | "enum_declaration"
                | "record_declaration"
                | "annotation_type_declaration"
        );
        let pushed = if is_type_decl {
            if let Some(class) = self.parse_type_declaration(node, stack) {
                stack.push(class.name.clone());
                classes.push(class);
                true
            } else {
                false
            }
        } else {
            false
        };
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.walk_types(&child, stack, classes);
        }
        if pushed {
            stack.pop();
        }
    }

    fn parse_type_declaration(&self, node: &Node, stack: &[String]) -> Option<Class> {
        let (kind_label, type_kind) = match node.kind() {
            "class_declaration" => ("class", JavaTypeKind::Class),
            "interface_declaration" => ("interface", JavaTypeKind::Interface),
            "enum_declaration" => ("enum", JavaTypeKind::Enum),
            "record_declaration" => ("record", JavaTypeKind::Record),
            "annotation_type_declaration" => ("annotation", JavaTypeKind::Annotation),
            _ => return None,
        };
        let name = self.get_node_text(&node.child_by_field_name("name")?);
        let qualified_name = if stack.is_empty() {
            name.clone()
        } else {
            format!("{}.{}", stack.join("."), name)
        };
        let modifiers_node = find_child_by_kind(node, "modifiers");
        let modinfo = self.parse_modifiers(modifiers_node);

        let extends = match node.kind() {
            "class_declaration" => node
                .child_by_field_name("superclass")
                .and_then(|s| self.first_named_child_text(&s)),
            "interface_declaration" => find_child_by_kind(node, "extends_interfaces")
                .map(|e| self.extract_types_from_container(&e))
                .and_then(|v| v.first().cloned()),
            _ => None,
        };
        let implements = node
            .child_by_field_name("interfaces")
            .map(|i| self.extract_types_from_container(&i))
            .unwrap_or_default();
        let permits = node
            .child_by_field_name("permits")
            .map(|p| self.extract_types_from_container(&p))
            .unwrap_or_default();

        let body = node.child_by_field_name("body");
        let mut attributes = Vec::new();
        let mut methods = Vec::new();
        if let Some(body) = &body {
            let mut cursor = body.walk();
            for child in body.children(&mut cursor) {
                match child.kind() {
                    "field_declaration" => attributes.extend(self.extract_fields(&child)),
                    "enum_constant" => attributes.push(self.extract_enum_constant(&child)),
                    "method_declaration"
                    | "constructor_declaration"
                    | "compact_constructor_declaration" => {
                        if let Some(f) = self.parse_method(
                            &child,
                            &qualified_name,
                            node.kind() == "interface_declaration",
                        ) {
                            methods.push(f);
                        }
                    }
                    _ => {}
                }
            }
        }
        if node.kind() == "record_declaration" {
            if let Some(params) = node.child_by_field_name("parameters") {
                attributes.extend(self.extract_record_components(&params));
            }
        }

        let mut decorators = modinfo.annotations.clone();
        decorators.push(kind_label.to_string());
        let is_nested = !stack.is_empty();
        let id = self.make_type_id(&qualified_name, kind_label);

        let mut bases = implements.clone();
        if let Some(e) = &extends {
            bases.insert(0, e.clone());
        }

        let java_info = JavaInfo {
            access_modifier: modinfo.access.clone(),
            is_static: modinfo.flags.contains("static"),
            is_final: modinfo.flags.contains("final"),
            is_abstract: modinfo.flags.contains("abstract"),
            is_sealed: modinfo.flags.contains("sealed"),
            is_non_sealed: modinfo.flags.contains("non-sealed"),
            permits,
            annotations: modinfo.annotations.clone(),
            type_kind: Some(type_kind),
            extends: extends.clone(),
            implements,
            is_nested,
            is_record: node.kind() == "record_declaration",
            package: self.package_name.clone(),
            ..Default::default()
        };

        Some(Class {
            id,
            name,
            bases,
            docstring: self.extract_javadoc(node),
            line_start: node.start_position().row + 1,
            line_end: node.end_position().row + 1,
            methods,
            attributes,
            decorators,
            lang_info: LanguageSpecificInfo {
                java: Some(java_info),
                ..Default::default()
            },
        })
    }

    fn extract_fields(&self, node: &Node) -> Vec<Attribute> {
        let mut fields = Vec::new();
        let type_text = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_default();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "variable_declarator" {
                let name = child
                    .child_by_field_name("name")
                    .map(|n| self.get_node_text(&n))
                    .unwrap_or_default();
                if name.is_empty() {
                    continue;
                }
                let value = child
                    .child_by_field_name("value")
                    .map(|v| self.get_node_text(&v));
                fields.push(Attribute {
                    name,
                    type_annotation: type_text.clone(),
                    value,
                });
            }
        }
        fields
    }

    fn extract_enum_constant(&self, node: &Node) -> Attribute {
        let name = node
            .child_by_field_name("name")
            .map(|n| self.get_node_text(&n))
            .unwrap_or_default();
        let value = node
            .child_by_field_name("arguments")
            .map(|a| self.get_node_text(&a));
        Attribute {
            name,
            type_annotation: "enum_constant".to_string(),
            value,
        }
    }

    fn extract_record_components(&self, node: &Node) -> Vec<Attribute> {
        let mut out = Vec::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "formal_parameter" => {
                    let type_annotation = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_default();
                    let name = child
                        .child_by_field_name("name")
                        .map(|n| self.get_node_text(&n))
                        .unwrap_or_default();
                    if !name.is_empty() {
                        out.push(Attribute {
                            name,
                            type_annotation,
                            value: None,
                        });
                    }
                }
                "spread_parameter" => {
                    let text = self.get_node_text(&child);
                    let name = find_child_by_kind(&child, "variable_declarator")
                        .map(|vd| self.get_node_text(&vd))
                        .unwrap_or_else(|| "varargs".to_string());
                    out.push(Attribute {
                        name,
                        type_annotation: text,
                        value: None,
                    });
                }
                _ => {}
            }
        }
        out
    }

    fn extract_parameters(&self, node: &Node) -> Vec<Parameter> {
        let mut params = Vec::new();
        let Some(plist) = node.child_by_field_name("parameters") else {
            return params;
        };
        let mut cursor = plist.walk();
        for child in plist.children(&mut cursor) {
            match child.kind() {
                "formal_parameter" => {
                    let type_annotation = child
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t))
                        .unwrap_or_default();
                    let name = child
                        .child_by_field_name("name")
                        .map(|n| self.get_node_text(&n))
                        .unwrap_or_default();
                    params.push(Parameter {
                        name,
                        type_annotation,
                        default_value: None,
                    });
                }
                "spread_parameter" => {
                    let text = self.get_node_text(&child);
                    let base_type = text.split("...").next().unwrap_or("").trim().to_string();
                    let name = find_child_by_kind(&child, "variable_declarator")
                        .map(|vd| self.get_node_text(&vd))
                        .unwrap_or_else(|| "args".to_string());
                    params.push(Parameter {
                        name,
                        type_annotation: format!("{}...", base_type),
                        default_value: None,
                    });
                }
                "receiver_parameter" => {
                    params.push(Parameter {
                        name: "this".to_string(),
                        type_annotation: self.get_node_text(&child),
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
        return_type: &str,
        is_ctor: bool,
    ) -> String {
        let param_str = params
            .iter()
            .map(|p| format!("{} {}", p.type_annotation, p.name))
            .collect::<Vec<_>>()
            .join(", ");
        if is_ctor {
            format!("{}({})", name, param_str)
        } else {
            format!("{} {}({})", return_type, name, param_str)
        }
    }

    fn extract_type_params(&self, node: &Node) -> Vec<String> {
        let mut out = Vec::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "type_parameter" {
                out.push(self.get_node_text(&child));
            }
        }
        out
    }

    fn contains_kind(&self, node: &Node, kind: &str) -> bool {
        if node.kind() == kind {
            return true;
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if self.contains_kind(&child, kind) {
                return true;
            }
        }
        false
    }

    fn parse_method(
        &self,
        node: &Node,
        qualified_class: &str,
        is_interface_method: bool,
    ) -> Option<Function> {
        let kind = node.kind();
        let is_ctor = kind == "constructor_declaration";
        let is_compact_ctor = kind == "compact_constructor_declaration";
        let name = self.get_node_text(&node.child_by_field_name("name")?);

        let modifiers_node = find_child_by_kind(node, "modifiers");
        let modinfo = self.parse_modifiers(modifiers_node);
        let mut annotations = modinfo.annotations.clone();
        let mut cursor0 = node.walk();
        for child in node.children(&mut cursor0) {
            if child.kind() == "annotation" || child.kind() == "marker_annotation" {
                if let Some(name_node) = child.child_by_field_name("name") {
                    let ann = format!("@{}", self.get_node_text(&name_node));
                    if !annotations.contains(&ann) {
                        annotations.push(ann);
                    }
                }
            }
        }

        let return_type = node
            .child_by_field_name("type")
            .map(|t| self.get_node_text(&t))
            .unwrap_or_default();
        let params = self.extract_parameters(node);
        let is_varargs = params.iter().any(|p| p.type_annotation.ends_with("..."));
        let throws_types = find_child_by_kind(node, "throws")
            .map(|t| {
                let mut out = Vec::new();
                let mut c = t.walk();
                for child in t.named_children(&mut c) {
                    out.push(self.get_node_text(&child));
                }
                out
            })
            .unwrap_or_default();

        let line_start = node.start_position().row + 1;
        let line_end = node.end_position().row + 1;
        let docstring = self.extract_javadoc(node);
        let signature =
            self.build_signature(&name, &params, &return_type, is_ctor || is_compact_ctor);
        let body = node.child_by_field_name("body");

        let (calls, variables, control_flow, exceptions, complexity) = if let Some(body) = &body {
            (
                self.extract_calls(body),
                self.extract_variables(body, &params),
                self.build_control_flow(body),
                self.build_exception_info(body, &throws_types),
                self.calculate_complexity(body),
            )
        } else {
            (
                vec![],
                self.extract_variables_from_params(&params),
                ControlFlow::default(),
                ExceptionInfo {
                    propagates: throws_types.clone(),
                    ..Default::default()
                },
                1,
            )
        };

        let body_text = body
            .as_ref()
            .map(|b| self.get_node_text(b))
            .unwrap_or_default();
        let uses_lambda = body
            .as_ref()
            .map(|b| self.contains_kind(b, "lambda_expression"))
            .unwrap_or(false);
        let uses_method_reference = body
            .as_ref()
            .map(|b| self.contains_kind(b, "method_reference"))
            .unwrap_or(false);
        let uses_try_with_resources = body
            .as_ref()
            .map(|b| self.contains_kind(b, "try_with_resources_statement"))
            .unwrap_or(false);

        let tags = self.auto_tag_method(
            &name,
            &docstring,
            &calls,
            &return_type,
            &body_text,
            &modinfo,
            is_ctor,
            &annotations,
            &params,
        );
        let is_override = annotations.iter().any(|a| a == "@Override");
        let is_deprecated = annotations.iter().any(|a| a == "@Deprecated");
        let is_test = annotations.iter().any(|a| {
            a == "@Test" || a.starts_with("@ParameterizedTest") || a.starts_with("@RepeatedTest")
        });
        let is_getter = !is_ctor
            && (name.starts_with("get") || name.starts_with("is"))
            && params.is_empty()
            && return_type != "void";
        let is_setter = !is_ctor && name.starts_with("set") && params.len() == 1;
        let importance_score = self.estimate_importance(&name, &modinfo, is_ctor, is_override);
        let id = self.make_method_id(qualified_class, &name, is_ctor || is_compact_ctor);

        let is_abstract = modinfo.flags.contains("abstract")
            || (is_interface_method
                && body.is_none()
                && !modinfo.flags.contains("default")
                && !modinfo.flags.contains("static"));

        let java_info = JavaInfo {
            access_modifier: modinfo.access.clone(),
            is_static: modinfo.flags.contains("static"),
            is_final: modinfo.flags.contains("final"),
            is_abstract,
            is_synchronized: modinfo.flags.contains("synchronized"),
            is_native: modinfo.flags.contains("native"),
            is_strictfp: modinfo.flags.contains("strictfp"),
            is_default_method: modinfo.flags.contains("default"),
            is_constructor: is_ctor || is_compact_ctor,
            is_compact_constructor: is_compact_ctor,
            is_interface_method,
            is_varargs,
            is_generic: node.child_by_field_name("type_parameters").is_some(),
            generic_params: node
                .child_by_field_name("type_parameters")
                .map(|tp| self.extract_type_params(&tp))
                .unwrap_or_default(),
            throws: throws_types,
            annotations: annotations.clone(),
            is_override,
            is_deprecated,
            is_test,
            is_getter,
            is_setter,
            package: self.package_name.clone(),
            uses_lambda,
            uses_method_reference,
            uses_try_with_resources,
            uses_reflection: body_text.contains("setAccessible")
                || body_text.contains("Class.forName"),
            uses_stream_api: body_text.contains(".stream()") || body_text.contains("Collectors."),
            ..Default::default()
        };

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
            decorators: annotations,
            tags,
            importance_score,
            lang_info: LanguageSpecificInfo {
                java: Some(java_info),
                ..Default::default()
            },
        })
    }

    fn extract_variables_from_params(&self, params: &[Parameter]) -> Vec<Variable> {
        params
            .iter()
            .filter(|p| !p.name.is_empty())
            .map(|p| Variable {
                name: p.name.clone(),
                var_type: if p.type_annotation.is_empty() {
                    None
                } else {
                    Some(p.type_annotation.clone())
                },
                scope: "param".to_string(),
                defined_at: None,
                transformations: vec![],
                used_in: vec![],
                returned: false,
            })
            .collect()
    }

    fn extract_calls(&self, node: &Node) -> Vec<FunctionCall> {
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
            "for_statement" | "enhanced_for_statement" | "while_statement" | "do_statement" => {
                "loop"
            }
            "switch_expression" => "switch",
            "catch_clause" => "catch",
            "lambda_expression" => "lambda",
            _ => context,
        };
        match node.kind() {
            "method_invocation" => {
                if let Some(name_node) = node.child_by_field_name("name") {
                    let callee = self.get_node_text(&name_node);
                    let object = node
                        .child_by_field_name("object")
                        .map(|o| self.get_node_text(&o));
                    let key = format!(
                        "{}:{}:{}",
                        callee,
                        object.as_deref().unwrap_or(""),
                        node.start_position().row
                    );
                    if seen.insert(key) {
                        calls.push(FunctionCall {
                            callee,
                            defined_in: object,
                            line: node.start_position().row + 1,
                            args: self.extract_call_arguments(node),
                            is_conditional: context != "unconditional",
                            context: context.to_string(),
                        });
                    }
                }
            }
            "object_creation_expression" => {
                if let Some(type_node) = node.child_by_field_name("type") {
                    let callee = format!("new {}", self.get_node_text(&type_node));
                    let key = format!("{}:{}", callee, node.start_position().row);
                    if seen.insert(key) {
                        calls.push(FunctionCall {
                            callee,
                            defined_in: None,
                            line: node.start_position().row + 1,
                            args: self.extract_call_arguments(node),
                            is_conditional: context != "unconditional",
                            context: context.to_string(),
                        });
                    }
                }
            }
            "explicit_constructor_invocation" => {
                if let Some(ctor_node) = node.child_by_field_name("constructor") {
                    let callee = self.get_node_text(&ctor_node);
                    let key = format!("{}:{}", callee, node.start_position().row);
                    if seen.insert(key) {
                        calls.push(FunctionCall {
                            callee,
                            defined_in: None,
                            line: node.start_position().row + 1,
                            args: self.extract_call_arguments(node),
                            is_conditional: context != "unconditional",
                            context: context.to_string(),
                        });
                    }
                }
            }
            _ => {}
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.find_calls_recursive(&child, calls, seen, child_context);
        }
    }

    fn extract_call_arguments(&self, call_node: &Node) -> Vec<String> {
        let mut args = Vec::new();
        if let Some(arg_list) = call_node.child_by_field_name("arguments") {
            let mut cursor = arg_list.walk();
            for child in arg_list.named_children(&mut cursor) {
                args.push(self.get_node_text(&child));
            }
        }
        args
    }

    fn extract_variables(&self, node: &Node, params: &[Parameter]) -> Vec<Variable> {
        let mut variables: HashMap<String, Variable> = HashMap::new();
        for param in params {
            if param.name.is_empty() {
                continue;
            }
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
        match node.kind() {
            "local_variable_declaration" => {
                let var_type = node
                    .child_by_field_name("type")
                    .map(|t| self.get_node_text(&t));
                let mut cursor = node.walk();
                for child in node.children(&mut cursor) {
                    if child.kind() == "variable_declarator" {
                        if let Some(name_node) = child.child_by_field_name("name") {
                            let var_name = self.get_node_text(&name_node);
                            if !var_name.is_empty() && !variables.contains_key(&var_name) {
                                variables.insert(
                                    var_name.clone(),
                                    Variable {
                                        name: var_name,
                                        var_type: var_type.clone(),
                                        scope: "local".to_string(),
                                        defined_at: Some(node.start_position().row + 1),
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
            "enhanced_for_statement" => {
                if let Some(name_node) = node.child_by_field_name("name") {
                    let var_name = self.get_node_text(&name_node);
                    let var_type = node
                        .child_by_field_name("type")
                        .map(|t| self.get_node_text(&t));
                    if !var_name.is_empty() && !variables.contains_key(&var_name) {
                        variables.insert(
                            var_name.clone(),
                            Variable {
                                name: var_name,
                                var_type,
                                scope: "local".to_string(),
                                defined_at: Some(node.start_position().row + 1),
                                transformations: vec![],
                                used_in: vec![],
                                returned: false,
                            },
                        );
                    }
                }
            }
            "catch_formal_parameter" => {
                if let Some(name_node) = node.child_by_field_name("name") {
                    let var_name = self.get_node_text(&name_node);
                    let var_type =
                        find_child_by_kind(node, "catch_type").map(|t| self.get_node_text(&t));
                    if !var_name.is_empty() && !variables.contains_key(&var_name) {
                        variables.insert(
                            var_name.clone(),
                            Variable {
                                name: var_name,
                                var_type,
                                scope: "local".to_string(),
                                defined_at: Some(node.start_position().row + 1),
                                transformations: vec![],
                                used_in: vec![],
                                returned: false,
                            },
                        );
                    }
                }
            }
            "return_statement" => {
                let mut cursor = node.walk();
                for child in node.children(&mut cursor) {
                    if child.kind() == "identifier" {
                        let var_name = self.get_node_text(&child);
                        if let Some(v) = variables.get_mut(&var_name) {
                            v.returned = true;
                        }
                    }
                }
            }
            _ => {}
        }
        let mut cursor = node.walk();
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
        match node.kind() {
            "if_statement" => {
                if let Some(b) = self.parse_if_statement(node) {
                    cf.branches.push(b);
                }
            }
            "for_statement" | "enhanced_for_statement" | "while_statement" | "do_statement" => {
                if let Some(l) = self.parse_loop(node) {
                    cf.loops.push(l);
                }
            }
            "try_statement" | "try_with_resources_statement" => {
                if let Some(t) = self.parse_try_block(node) {
                    cf.try_blocks.push(t);
                }
            }
            _ => {}
        }
        let mut cursor = node.walk();
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
        let true_path = self.extract_execution_path(&consequence);
        let false_path = node
            .child_by_field_name("alternative")
            .map(|alt| self.extract_execution_path(&alt));
        Some(Branch {
            branch_type: "if".to_string(),
            condition,
            line,
            true_path,
            false_path,
        })
    }

    fn extract_execution_path(&self, block: &Node) -> ExecutionPath {
        ExecutionPath {
            calls: self.extract_call_names(block),
            returns: self.find_return_value(block),
            raises: self.find_thrown_type(block),
        }
    }

    fn extract_call_names(&self, node: &Node) -> Vec<String> {
        let mut names = Vec::new();
        let mut seen = HashSet::new();
        self.collect_call_names(node, &mut names, &mut seen);
        names
    }

    fn collect_call_names(&self, node: &Node, names: &mut Vec<String>, seen: &mut HashSet<String>) {
        if node.kind() == "method_invocation" {
            if let Some(n) = node.child_by_field_name("name") {
                let name = self.get_node_text(&n);
                if !name.is_empty() && seen.insert(name.clone()) {
                    names.push(name);
                }
            }
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.collect_call_names(&child, names, seen);
        }
    }

    fn find_return_value(&self, node: &Node) -> Option<String> {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "return_statement" {
                let mut vals = Vec::new();
                let mut rc = child.walk();
                for rchild in child.named_children(&mut rc) {
                    vals.push(self.get_node_text(&rchild));
                }
                return Some(vals.join(", "));
            }
            if let Some(v) = self.find_return_value(&child) {
                return Some(v);
            }
        }
        None
    }

    fn find_thrown_type(&self, node: &Node) -> Option<String> {
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            if child.kind() == "throw_statement" {
                let mut tc = child.walk();
                if let Some(expr) = child.named_children(&mut tc).next() {
                    return Some(self.get_node_text(&expr));
                }
            }
            if let Some(v) = self.find_thrown_type(&child) {
                return Some(v);
            }
        }
        None
    }

    fn parse_loop(&self, node: &Node) -> Option<Loop> {
        let line = node.start_position().row + 1;
        let loop_type = match node.kind() {
            "for_statement" => "for",
            "enhanced_for_statement" => "for-each",
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
            calls: self.extract_call_names(node),
        })
    }

    fn parse_try_block(&self, node: &Node) -> Option<TryBlock> {
        let line = node.start_position().row + 1;
        let body = node.child_by_field_name("body")?;
        let try_calls = self.extract_call_names(&body);
        let mut except_clauses = Vec::new();
        let mut finally_calls = Vec::new();
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            match child.kind() {
                "catch_clause" => {
                    if let Some(param) = find_child_by_kind(&child, "catch_formal_parameter") {
                        let exception_type = find_child_by_kind(&param, "catch_type")
                            .map(|t| self.get_node_text(&t))
                            .unwrap_or_default();
                        let calls = child
                            .child_by_field_name("body")
                            .map(|b| self.extract_call_names(&b))
                            .unwrap_or_default();
                        except_clauses.push(ExceptClause {
                            exception_type,
                            line: child.start_position().row + 1,
                            calls,
                        });
                    }
                }
                "finally_clause" => {
                    finally_calls = self.extract_call_names(&child);
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

    fn build_exception_info(&self, body: &Node, throws_types: &[String]) -> ExceptionInfo {
        let mut raises = Vec::new();
        let mut handles = Vec::new();
        self.collect_exceptions(body, &mut raises, &mut handles);
        ExceptionInfo {
            raises,
            propagates: throws_types.to_vec(),
            handles,
        }
    }

    fn collect_exceptions(&self, node: &Node, raises: &mut Vec<String>, handles: &mut Vec<String>) {
        match node.kind() {
            "throw_statement" => {
                let mut cursor = node.walk();
                if let Some(expr) = node.named_children(&mut cursor).next() {
                    let text = self.get_node_text(&expr);
                    if !raises.contains(&text) {
                        raises.push(text);
                    }
                }
            }
            "catch_formal_parameter" => {
                if let Some(t) = find_child_by_kind(node, "catch_type") {
                    let text = self.get_node_text(&t);
                    for part in text.split('|') {
                        let p = part.trim().to_string();
                        if !p.is_empty() && !handles.contains(&p) {
                            handles.push(p);
                        }
                    }
                }
            }
            _ => {}
        }
        let mut cursor = node.walk();
        for child in node.children(&mut cursor) {
            self.collect_exceptions(&child, raises, handles);
        }
    }

    fn calculate_complexity(&self, node: &Node) -> usize {
        fn count(node: &Node) -> usize {
            let mut c = 0;
            match node.kind() {
                "if_statement"
                | "for_statement"
                | "enhanced_for_statement"
                | "while_statement"
                | "do_statement"
                | "catch_clause"
                | "switch_label"
                | "switch_rule"
                | "ternary_expression"
                | "&&"
                | "||" => c += 1,
                _ => {}
            }
            let mut cursor = node.walk();
            for child in node.children(&mut cursor) {
                c += count(&child);
            }
            c
        }
        1 + count(node)
    }

    fn auto_tag_method(
        &self,
        name: &str,
        docstring: &str,
        calls: &[FunctionCall],
        return_type: &str,
        body_text: &str,
        modinfo: &ModifierInfo,
        is_ctor: bool,
        annotations: &[String],
        params: &[Parameter],
    ) -> Vec<String> {
        let mut tags = Vec::new();
        let name_lower = name.to_lowercase();
        let doc_lower = docstring.to_lowercase();
        if name == "main" && modinfo.flags.contains("static") {
            tags.push("entry-point".to_string());
        }
        if is_ctor {
            tags.push("constructor".to_string());
        }
        for rule in TAG_RULES.iter() {
            let name_matches = rule.keywords.iter().any(|kw| name_lower.contains(kw));
            let doc_matches = rule.check_docstring && doc_lower.contains(rule.keywords[0]);
            if name_matches || doc_matches {
                tags.push(rule.tag.to_string());
            }
        }
        if (name_lower.starts_with("get") || name_lower.starts_with("is")) && params.is_empty() {
            tags.push("getter".to_string());
        }
        if name_lower.starts_with("set") && params.len() == 1 {
            tags.push("setter".to_string());
        }
        if annotations.iter().any(|a| a == "@Override") {
            tags.push("override".to_string());
        }
        if annotations.iter().any(|a| a.starts_with("@Test")) {
            tags.push("testing".to_string());
        }
        if annotations.iter().any(|a| a == "@Deprecated") {
            tags.push("deprecated".to_string());
        }
        if modinfo.flags.contains("synchronized") {
            tags.push("concurrent".to_string());
            tags.push("threading".to_string());
        }
        let calls_joined = calls
            .iter()
            .map(|c| c.callee.as_str())
            .collect::<Vec<_>>()
            .join(" ");
        if calls_joined.contains("Thread") || calls_joined.contains("Executor") {
            tags.push("concurrent".to_string());
        }
        if body_text.contains("->") {
            tags.push("functional".to_string());
        }
        if body_text.contains(".stream()") {
            tags.push("stream-api".to_string());
        }
        if return_type.ends_with("[]") {
            tags.push("returns-array".to_string());
        }
        tags.sort();
        tags.dedup();
        tags
    }

    fn estimate_importance(
        &self,
        name: &str,
        modinfo: &ModifierInfo,
        is_ctor: bool,
        is_override: bool,
    ) -> f32 {
        let mut score: f32 = 0.5;
        if name == "main" {
            score += 0.3;
        }
        match modinfo.access.as_deref() {
            Some("public") => score += 0.1,
            Some("private") => score -= 0.1,
            _ => {}
        }
        if is_ctor {
            score += 0.05;
        }
        if is_override {
            score -= 0.05;
        }
        score.clamp(0.0, 1.0)
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
}

pub fn parse_file(path: &Path) -> Result<(String, FileData), String> {
    let source_code = std::fs::read_to_string(path)
        .map_err(|e| format!("Failed to read file {}: {}", path.display(), e))?;
    let clean_path = path.strip_prefix("./").unwrap_or(path);
    let path_str = clean_path.to_string_lossy().to_string();
    let mut parser = JavaParser::new(source_code, path_str.clone());
    let file_data = parser.parse()?;
    Ok((path_str, file_data))
}
