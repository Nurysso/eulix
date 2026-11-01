// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::*;

/// Roots that are known to live outside the project. Any other unresolved
/// root may be a sibling crate of the same workspace, so it keeps the name
/// lookup instead of being treated as external.
pub(super) fn external_root(root: &str) -> bool {
    matches!(
        root,
        "std"
            | "core"
            | "alloc"
            | "proc_macro"
            | "bool"
            | "char"
            | "str"
            | "f32"
            | "f64"
            | "i8"
            | "i16"
            | "i32"
            | "i64"
            | "i128"
            | "isize"
            | "u8"
            | "u16"
            | "u32"
            | "u64"
            | "u128"
            | "usize"
    )
}

pub(super) fn module_file<'a>(path: &str, caller: &str, index: &'a ModuleIndex<'a>) -> Target<'a> {
    // A package can contain distinct lib and main crates in the same directory.
    if path == crate_root(caller, index)
        && (matches!(caller.rsplit('/').next(), Some("main.rs" | "lib.rs"))
            || standalone_target(caller))
    {
        return index.get(format!("./{caller}").as_str()).map(Vec::as_slice);
    }
    index
        .get(format!("./{path}.rs").as_str())
        .or_else(|| index.get(format!("./{path}/mod.rs").as_str()))
        .or_else(|| index.get(format!("./{path}/lib.rs").as_str()))
        .or_else(|| index.get(format!("./{path}/main.rs").as_str()))
        .map(Vec::as_slice)
}

fn standalone_target(path: &str) -> bool {
    ["tests/", "benches/", "examples/"].iter().any(|directory| {
        path.strip_prefix(directory)
            .or_else(|| {
                path.rsplit_once(&format!("/{directory}"))
                    .map(|(_, tail)| tail)
            })
            .is_some_and(|tail| !tail.contains('/') && tail.ends_with(".rs"))
    })
}

fn crate_root<'a>(path: &'a str, index: &ModuleIndex<'_>) -> &'a str {
    if standalone_target(path) {
        return dir_of(path);
    }
    if let Some((_, binary)) = path.rsplit_once("src/bin/") {
        if !binary.contains('/') {
            return path.strip_suffix(".rs").unwrap_or(path);
        }
    }
    let fallback = if let Some((prefix, _)) = path.rsplit_once("/src/") {
        &path[..prefix.len() + 4]
    } else if path.starts_with("src/") {
        "src"
    } else {
        ""
    };
    // Prefer the nearest root, including src/bin/tool/main.rs, before the
    // enclosing package's src/lib.rs. No Cargo metadata is needed here.
    let mut dir = dir_of(path);
    loop {
        if ["lib.rs", "main.rs"]
            .iter()
            .any(|name| index.contains_key(format!("./{}", join(dir, name)).as_str()))
        {
            return dir;
        }
        if dir == fallback || dir.is_empty() {
            return fallback;
        }
        dir = dir.rsplit_once('/').map_or("", |(parent, _)| parent);
    }
}

fn module_dir(path: &str) -> &str {
    let stem = path.strip_suffix(".rs").unwrap_or(path);
    if matches!(stem.rsplit('/').next(), Some("lib" | "main" | "mod")) || standalone_target(path) {
        dir_of(path)
    } else {
        stem
    }
}

fn join(parent: &str, child: &str) -> String {
    if parent.is_empty() {
        child.to_string()
    } else {
        format!("{parent}/{child}")
    }
}

/// Resolve only paths rooted in this crate. A missing tail is allowed once
/// the root is known, so callers can retain the base's lexical fallback.
pub(super) fn project_path(name: &str, path: &str, index: &ModuleIndex<'_>) -> Option<String> {
    let mut parts = name.split("::").filter(|s| !s.is_empty()).peekable();
    let first = parts.next()?;
    if external_root(first) {
        return None;
    }
    let root = crate_root(path, index);
    let mut base = match first {
        "crate" => root.to_string(),
        "self" => module_dir(path).to_string(),
        "super" => {
            let mut base = module_dir(path);
            let mut count = 1;
            while parts.peek() == Some(&"super") {
                parts.next();
                count += 1;
            }
            for _ in 0..count {
                if base == root {
                    return None;
                }
                base = base.rsplit_once('/').map_or("", |(p, _)| p);
            }
            base.to_string()
        }
        _ => [module_dir(path), dir_of(path), root]
            .iter()
            .map(|base| join(base, first))
            .find(|candidate| module_file(candidate, path, index).is_some())?,
    };
    for part in parts {
        base = join(&base, part);
    }
    Some(base)
}

impl Resolver<'_> {
    fn rust_crate_contains(&self, roots: Target<'_>, target: u32) -> bool {
        let Some(roots) = roots else { return false };
        let mut pending = roots.to_vec();
        let mut seen = HashSet::new();
        while let Some(file) = pending.pop() {
            if file == target {
                return true;
            }
            if !seen.insert(file) {
                continue;
            }
            // Known crate-local modules/imports also cover shared modules
            // of lib/main and children of standalone tests or examples.
            for path in self.imports[file as usize].rust_paths.values().flatten() {
                if let Some(files) = module_file(path, self.files[file as usize], self.module_index)
                {
                    pending.extend(files.iter().copied());
                }
            }
        }
        false
    }

    pub(super) fn rust_type_outside_crate(&self, name: &str, file: u32) -> Option<u32> {
        let caller = self.files[file as usize];
        let root = crate_root(caller, self.module_index);
        let root_files = module_file(root, caller, self.module_index);
        let candidates = self
            .by_name
            .get(name)?
            .iter()
            .copied()
            .filter(|&candidate| {
                let target_file = self.nodes[candidate as usize].file as usize;
                if self.languages[target_file] != "rust" {
                    return false;
                }
                let target = self.files[target_file];
                let target_root = crate_root(target, self.module_index);
                let target_roots = module_file(target_root, target, self.module_index);
                if self.rust_crate_contains(root_files, target_file as u32)
                    || self.rust_crate_contains(target_roots, file)
                {
                    return false;
                }
                // Root files distinguish library/binary and standalone targets
                // sharing a directory. With no root file, retain the directory
                // fallback already used by crate-local path resolution.
                match (root_files, target_roots) {
                    (Some(a), Some(b)) => a != b,
                    _ => root != target_root,
                }
            });
        let (found, count) = self.pick(candidates, Want::Class, true, file, true);
        (count <= self.max_ambiguous.max(1))
            .then_some(found)
            .flatten()
    }

    /// Some(None) blocks an explicit external root; None leaves type/instance
    /// receivers to their existing resolver.
    pub(super) fn rust_module_call(
        &self,
        receiver: &str,
        name: &str,
        file: u32,
    ) -> Option<Option<u32>> {
        let receiver = receiver.strip_suffix("::")?;
        if receiver.contains(['.', '(', ')', '[', ']']) || receiver == "Self" {
            return None;
        }
        let imports = &self.imports[file as usize];
        let (root, tail) = receiver.split_once("::").unwrap_or((receiver, ""));
        let module_binding = imports
            .rust_paths
            .get(root)
            .and_then(Option::as_deref)
            .and_then(|path| module_file(path, self.files[file as usize], self.module_index))
            .is_some();
        // A module path and a callable value may share a Rust binding name.
        // Preserve the module for qualified calls without losing bare imports.
        if !module_binding {
            if let Some(original) = imports.rust_foreign_types.get(receiver) {
                return Some(
                    self.rust_type_outside_crate(original, file)
                        .and_then(|class| self.receiver_method(class, name)),
                );
            }
            // Imported types retain class/ancestor dispatch, not module fallback.
            if let Some(original) = imports.symbols.get(receiver) {
                if let Some(Some(files)) = imports.names.get(receiver) {
                    if self
                        .lookup_in_files(original, files, file, Want::Class)
                        .is_some()
                    {
                        return None;
                    }
                }
            }
        }
        if external_root(root) && !imports.rust_paths.contains_key(root) {
            return Some(None);
        }
        let path = if let Some(binding) = imports.rust_paths.get(root) {
            let Some(binding) = binding else {
                return Some(None);
            };
            if tail.is_empty() {
                binding.clone()
            } else {
                join(binding, &tail.replace("::", "/"))
            }
        } else if let Some(path) =
            project_path(receiver, self.files[file as usize], self.module_index)
        {
            path
        } else if root.starts_with(|c: char| c.is_ascii_lowercase())
            && !receiver
                .rsplit("::")
                .next()
                .is_some_and(|last| last.starts_with(|c: char| c.is_ascii_uppercase()))
        {
            // A module path outside this crate, such as a sibling workspace
            // crate, keeps the base's function lookup. `krate::Type::f()` is
            // a method and stays with the type resolver.
            return Some(self.lookup(name, file, Seek::Only(Kind::Function)));
        } else {
            return None;
        };
        Some(
            module_file(&path, self.files[file as usize], self.module_index)
                .and_then(|files| self.lookup_in_files(name, files, file, Seek::LexicalCall))
                .or_else(|| self.lookup(name, file, Seek::Only(Kind::Function))),
        )
    }
}
