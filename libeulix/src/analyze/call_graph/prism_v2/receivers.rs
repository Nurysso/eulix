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
        if matches!(imp.import_type.as_str(), "reexport_all" | "reexport_named") {
            out.reexports.push((imp, target));
            continue;
        }
        if imp.items.is_empty() {
            let binding = if local != source {
                local
            } else {
                source.rsplit(sep).find(|s| !s.is_empty()).unwrap_or(source)
            };
            out.namespaces.insert(binding, target);
            out.namespace_modules.insert(binding, Cow::Borrowed(source));
            // Python allows a fully qualified module receiver.
            if local == source {
                out.namespaces.insert(source, target);
                out.namespace_modules.insert(source, Cow::Borrowed(source));
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
                        out.namespace_modules.insert(binding, Cow::Owned(submodule));
                        continue;
                    }
                }
                if original == "*" {
                    out.namespaces.insert(binding, target);
                    out.namespace_modules.insert(binding, Cow::Borrowed(source));
                } else if !original.is_empty() {
                    out.names.insert(binding, target);
                    out.symbols.insert(binding, original);
                }
            }
        }
    }
    // Explicit bindings override wildcard exports regardless of source order.
    out.reexports
        .sort_by_key(|(export, _)| export.import_type == "reexport_all");
    out
}

impl Resolver<'_> {
    /// A known receiver searches its own class and ancestors only. Descendant
    /// overrides do not establish the type of an otherwise missing method.
    fn receiver_method(&self, class: u32, name: &str) -> Option<u32> {
        self.own_method(class, name)
            .or_else(|| self.walk(class, name, &self.parents))
    }

    fn exported(&self, name: &str, files: &[u32], caller_file: u32, depth: usize) -> Option<u32> {
        // A named reexport defines the public binding, even if this module
        // also declares a private function with that name.
        if depth < 2 {
            for &file in files {
                for (export, target) in &self.imports[file as usize].reexports {
                    if export.import_type != "reexport_named" {
                        continue;
                    }
                    for item in &export.items {
                        let (original, public) = alias(item);
                        if public == name {
                            return self.exported(original, (*target)?, caller_file, depth + 1);
                        }
                    }
                }
            }
        }
        if let Some(found) = self.lookup_in_files(name, files, caller_file, Seek::LexicalCall) {
            return Some(found);
        }
        if depth == 2 {
            return None;
        }
        for &file in files {
            for (export, target) in &self.imports[file as usize].reexports {
                if export.import_type != "reexport_all" {
                    continue;
                }
                if let Some(target) = target {
                    if let Some(found) = self.exported(name, target, caller_file, depth + 1) {
                        return Some(found);
                    }
                }
            }
        }
        None
    }

    /// None: not an imported namespace. Some(None): external or unresolved.
    /// A project-owned root may fall back to the base resolver's name search.
    fn namespace_call(&self, receiver: &str, name: &str, file: u32) -> Option<Option<u32>> {
        let imports = &self.imports[file as usize];
        // An import may itself contain dots. Prefer its longest matching
        // prefix so `import pkg.sub; pkg.sub.deep.f()` keeps the module path.
        let mut prefix = receiver;
        let (binding, target) = loop {
            if let Some(binding) = imports.namespaces.get_key_value(prefix) {
                break binding;
            }
            prefix = split_callee(prefix).0?;
        };
        let Some(files) = target else {
            return Some(None);
        };
        if *binding == receiver {
            if let Some(found) = self.exported(name, files, file, 0) {
                return Some(Some(found));
            }
        } else {
            if self.languages[file as usize] == "python" {
                if let Some(original) = imports.namespace_modules.get(binding) {
                    let qualified = format!("{original}{}", &receiver[binding.len()..]);
                    if let Some(files) = module(
                        &qualified,
                        self.files[file as usize],
                        self.module_index,
                        "python",
                    ) {
                        if let Some(found) = self.exported(name, files, file, 0) {
                            return Some(Some(found));
                        }
                    }
                }
            }
            // Imported namespace followed by a class, e.g. api.Client.send().
            let short = split_callee(receiver).1;
            if let Some(class) = self.lookup_in_files(short, files, file, Want::Class) {
                return Some(self.receiver_method(class, name));
            }
        }
        Some(self.lookup(name, file, Seek::LexicalCall))
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
                return self
                    .exported(original, fs, file, 0)
                    .or_else(|| self.lookup(original, file, Seek::LexicalCall));
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
        // External aliases are authoritative. Internal namespaces without
        // class metadata retain the base resolver's name fallback.
        if let Some(original) = imports.symbols.get(receiver) {
            let fs = imports.names.get(receiver).copied().flatten()?;
            if let Some(cls) = self.lookup_in_files(original, fs, file, Want::Class) {
                return self.receiver_method(cls, name);
            }
            return self.lookup(name, file, Seek::LexicalCall);
        }
        if let Some(result) = self.namespace_call(receiver, name, file) {
            return result;
        }
        let root = receiver.split(sep).next().unwrap_or(receiver);
        if let Some(target) = imports.names.get(root) {
            return target.and_then(|_| self.lookup(name, file, Seek::LexicalCall));
        }
        // Explicit class receivers, including simple constructor expressions.
        if !receiver.contains(['(', ')', '[', ']']) {
            if let Some(cls) = self.lookup(receiver, file, Want::Class) {
                return self.receiver_method(cls, name);
            }
        }
        // An unknown receiver is insufficient evidence for a call edge, even
        // when only one indexed method happens to have this name.
        None
    }
}
