// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use super::*;

pub(super) fn module_file<'a>(path: &str, caller: &str, index: &'a ModuleIndex<'a>) -> Target<'a> {
    // A package can contain distinct lib and main crates in the same directory.
    if path == crate_root(caller, index)
        && (matches!(caller.rsplit('/').next(), Some("main.rs" | "lib.rs"))
            || standalone_test(caller))
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

fn standalone_test(path: &str) -> bool {
    ["tests/", "benches/"].iter().any(|directory| {
        path.strip_prefix(directory)
            .or_else(|| {
                path.rsplit_once(&format!("/{directory}"))
                    .map(|(_, tail)| tail)
            })
            .is_some_and(|tail| !tail.contains('/') && tail.ends_with(".rs"))
    })
}

fn crate_root<'a>(path: &'a str, index: &ModuleIndex<'_>) -> &'a str {
    if standalone_test(path) {
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
    if matches!(stem.rsplit('/').next(), Some("lib" | "main" | "mod")) || standalone_test(path) {
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
    if matches!(first, "std" | "core" | "alloc" | "proc_macro") {
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
        let (root, tail) = receiver.split_once("::").unwrap_or((receiver, ""));
        if matches!(root, "std" | "core" | "alloc" | "proc_macro") {
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
        } else {
            project_path(receiver, self.files[file as usize], self.module_index)?
        };
        Some(
            module_file(&path, self.files[file as usize], self.module_index)
                .and_then(|files| self.lookup_in_files(name, files, file, Seek::LexicalCall))
                .or_else(|| self.lookup(name, file, Seek::Only(Kind::Function))),
        )
    }
}
