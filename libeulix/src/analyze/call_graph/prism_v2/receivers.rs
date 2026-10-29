// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//! Receiver-aware resolution for parsers that retain a separate receiver.
//! Keep the legacy resolver for the other languages.
use super::*;
use crate::struc::kb_struct::FunctionCall;

pub(super) fn enabled(language: &str) -> bool {
    matches!(language, "python" | "typescript" | "rust")
}

fn alias(s: &str) -> (&str, &str) {
    s.trim()
        .split_once(" as ")
        .map_or((s.trim(), s.trim()), |(name, local)| {
            (name.trim(), local.trim())
        })
}

fn module<'a>(name: &str, path: &str, index: &'a ModuleIndex<'a>, language: &str) -> Target<'a> {
    // Relative JS modules are relative to the caller, not a global suffix.
    // An explicit .js specifier may refer to a .ts source file.
    if language == "typescript" && name.starts_with('.') {
        let mut parts: Vec<&str> = dir_of(path).split('/').filter(|s| !s.is_empty()).collect();
        for part in name.split('/') {
            match part {
                "." | "" => {}
                ".." => {
                    parts.pop()?;
                }
                _ => parts.push(part),
            }
        }
        let mut key = format!("./{}", parts.join("/"));
        for ext in [".js", ".ts", ".tsx", ".jsx", ".mjs", ".mts"] {
            if key.ends_with(ext) {
                key.truncate(key.len() - ext.len());
                break;
            }
        }
        return index
            .get(key.as_str())
            .or_else(|| index.get(format!("{key}/index").as_str()))
            .map(Vec::as_slice);
    }
    resolve_module(name, index)
}

// Rust keeps a complete use tree in `Import.module`. Expand groups while
// borrowing the binding names from that text; only module paths are temporary.
fn rust_import<'a>(
    source: &'a str,
    path: &str,
    index: &'a ModuleIndex<'a>,
    out: &mut FileImports<'a>,
) {
    let mut pending = vec![(String::new(), source)];
    while let Some((prefix, source)) = pending.pop() {
        let source = source.trim();
        if let Some((head, group)) = source.split_once('{') {
            let Some(group) = group.trim().strip_suffix('}') else {
                continue;
            };
            let prefix = format!("{prefix}{head}");
            let (mut depth, mut start) = (0usize, 0usize);
            for (i, ch) in group.char_indices() {
                match ch {
                    '{' => depth += 1,
                    '}' => depth = depth.saturating_sub(1),
                    ',' if depth == 0 => {
                        pending.push((prefix.clone(), &group[start..i]));
                        start = i + 1;
                    }
                    _ => {}
                }
            }
            pending.push((prefix, &group[start..]));
            continue;
        }
        let (original, local) = alias(source);
        if original.is_empty() || original == "*" {
            continue;
        }
        let full = format!("{prefix}{original}");
        let symbol = original.rsplit("::").next().unwrap_or(original);
        let binding = if local != original { local } else { symbol };
        if let Some(target) = module(&full, path, index, "rust") {
            out.namespaces.entry(binding).or_insert(Some(target));
        } else {
            let target = full
                .rsplit_once("::")
                .and_then(|(parent, _)| module(parent, path, index, "rust"));
            out.names.entry(binding).or_insert(target);
            out.symbols.entry(binding).or_insert(symbol);
        }
    }
}

pub(super) fn build_imports<'a>(
    fd: &'a FileData,
    index: &'a ModuleIndex<'a>,
    path: &str,
) -> FileImports<'a> {
    let mut out = FileImports::default();
    for imp in &fd.imports {
        if fd.language == "rust" {
            rust_import(&imp.module, path, index, &mut out);
            continue;
        }
        let (source, local) = alias(&imp.module);
        let target = module(source, path, index, &fd.language);
        if imp.items.is_empty() {
            let binding = if local != source {
                local
            } else {
                source.rsplit(sep).find(|s| !s.is_empty()).unwrap_or(source)
            };
            out.namespaces.insert(binding, target);
            // Python allows a fully qualified module receiver.
            if local == source {
                out.namespaces.insert(source, target);
            }
        }
        for item in &imp.items {
            // TypeScript stores an import clause, Python stores each item.
            for piece in item.split(',') {
                let piece = piece.trim().trim_matches(['{', '}']).trim();
                let (original, binding) = alias(piece);
                if fd.language == "python" {
                    let submodule = format!("{source}.{original}");
                    if let Some(files) = module(&submodule, path, index, "python") {
                        out.namespaces.insert(binding, Some(files));
                        continue;
                    }
                }
                if original == "*" {
                    out.namespaces.insert(binding, target);
                } else if !original.is_empty() {
                    out.names.insert(binding, target);
                    out.symbols.insert(binding, original);
                }
            }
        }
    }
    out
}

impl Resolver<'_> {
    /// Receiver calls never use the scope ladder's arbitrary tie-breaker.
    fn unique_method(&self, name: &str, file: u32) -> Option<u32> {
        let mut candidates = self.by_name.get(name)?.iter().copied().filter(|&i| {
            let n = &self.nodes[i as usize];
            n.kind == Kind::Method && self.php[n.file as usize] == self.php[file as usize]
        });
        let only = candidates.next()?;
        candidates.next().is_none().then_some(only)
    }

    /// A known receiver searches its own class and ancestors only. Descendant
    /// overrides do not establish the type of an otherwise missing method.
    fn receiver_method(&self, class: u32, name: &str) -> Option<u32> {
        self.own_method(class, name)
            .or_else(|| self.walk(class, name, &self.parents))
    }

    pub(super) fn receiver_call(
        &self,
        call: &FunctionCall,
        file: u32,
        class: Option<u32>,
        has_self: bool,
        caller: &str,
    ) -> Option<u32> {
        let Some(receiver) = call.receiver.as_deref() else {
            if let Some(nested) = self.nested_in(caller, &call.callee) {
                return Some(nested);
            }
            // A bare Python/Rust/TS call is lexical, never an implicit sibling
            // method. For example, `send()` and `self.send()` are different.
            if let Some(original) = self.imports[file as usize]
                .symbols
                .get(call.callee.as_str())
            {
                let fs = self.imports[file as usize]
                    .names
                    .get(call.callee.as_str())
                    .copied()
                    .flatten()?;
                return self.lookup_in_files(original, fs, file, Seek::LexicalCall);
            }
            return self.lookup(&call.callee, file, Seek::LexicalCall);
        };
        let name = &call.callee;
        // Rust's module `self::f()` is distinct from the instance `self.f()`.
        if receiver == "self::" {
            return self.lookup_in_files(name, &[file], file, Seek::LexicalCall);
        }
        if is_self_qualifier(receiver) {
            return if has_self || receiver == "Self" {
                class.and_then(|c| self.receiver_method(c, name))
            } else {
                None
            };
        }
        // Simple constructor receivers retain their source spelling in the
        // KB, but class/import lookup uses the constructed class name.
        let receiver = receiver.strip_prefix("new ").unwrap_or(receiver);
        let receiver = receiver.strip_suffix("()").unwrap_or(receiver);
        let imports = &self.imports[file as usize];
        // The alias is authoritative, including when its module is external
        // or the requested symbol does not exist in the imported module.
        if let Some(original) = imports.symbols.get(receiver) {
            let fs = imports.names.get(receiver).copied().flatten()?;
            let cls = self.lookup_in_files(original, fs, file, Want::Class)?;
            return self.receiver_method(cls, name);
        }
        if let Some(target) = imports.namespaces.get(receiver) {
            return self
                .lookup_in_files(name, (*target)?, file, Seek::Only(Kind::Function))
                .or_else(|| self.lookup_in_files(name, (*target)?, file, Want::Class));
        }
        let (qual, short) = split_callee(receiver);
        if let Some(q) = qual {
            if let Some(target) = imports.namespaces.get(q) {
                let cls = self.lookup_in_files(short, (*target)?, file, Want::Class)?;
                return self.receiver_method(cls, name);
            }
            // Never let `external.member` escape an explicit external import.
            let root = receiver.split(sep).next().unwrap_or(receiver);
            if imports.namespaces.contains_key(root) || imports.names.contains_key(root) {
                return None;
            }
        }
        // Explicit class receivers, including simple constructor expressions.
        if !receiver.contains(['(', ')', '[', ']']) {
            if let Some(cls) = self.lookup(receiver, file, Want::Class) {
                return self.receiver_method(cls, name);
            }
        }
        self.unique_method(name, file)
    }
}
