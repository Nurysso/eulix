// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use libeulix::analyze::{call_graph, entry_points, patterns};
use libeulix::struc::kb_struct::{FileData, KnowledgeBase};
use std::collections::{BTreeMap, BTreeSet};
use std::path::Path;

/// The Go modules of the project, as PRISM v2 wants them: (directory of a
/// `go.mod` relative to `root`, module path). The module path is what maps a
/// Go import to one package directory and tells project imports from the
/// standard library and from dependencies.
///
/// Only directories above a parsed Go file are looked at, so the cost is one
/// `stat` per such directory, and nothing for a project without Go.
pub fn go_modules(root: &Path, structure: &BTreeMap<String, FileData>) -> Vec<(String, String)> {
    let mut dirs: BTreeSet<&str> = BTreeSet::new();
    for (path, _) in structure.iter().filter(|(_, fd)| fd.language == "go") {
        let mut dir = path.trim_start_matches("./");
        while let Some((parent, _)) = dir.rsplit_once('/') {
            if !dirs.insert(parent) {
                break; // its ancestors are in the set already
            }
            dir = parent;
        }
        dirs.insert("");
    }
    dirs.into_iter()
        .filter_map(|dir| {
            let text = std::fs::read_to_string(root.join(dir).join("go.mod")).ok()?;
            let module = text.lines().find_map(|line| {
                let rest = line.trim().strip_prefix("module")?;
                // `module example.com/m // comment`, `module "example.com/m"`
                let rest = rest.strip_prefix([' ', '\t'])?;
                let name = rest.split("//").next()?.trim().trim_matches('"');
                (!name.is_empty()).then(|| name.to_string())
            })?;
            Some((dir.to_string(), module))
        })
        .collect()
}

pub fn analyze_and_build(
    mut kb: KnowledgeBase,
    verbose: bool,
    prism: u8,
    root: &Path,
) -> KnowledgeBase {
    let file_count = kb.structure.len();
    let is_large = file_count > 100_000;
    let use_precise = prism;

    if verbose && is_large {
        println!(
            "   [!]  Enabling memory-efficient mode for {} files",
            file_count
        );
    }

    if !is_large {
        if use_precise != 2 {
            if verbose {
                println!("   → Resolving call locations...");
            }
            call_graph::prism_v1::resolve_call_locations(&mut kb);
        }

        if verbose {
            println!("   → Building call graphs...");
        }
        kb.call_graph = if use_precise == 2 {
            if verbose {
                println!("      • Using precise analysis with PRISMv2...");
            }
            let opts = call_graph::prism_v2::CallGraphOptions {
                go_modules: go_modules(root, &kb.structure),
                ..Default::default()
            };
            call_graph::prism_v2::build_with(&kb.structure, &opts)
        } else {
            if verbose {
                println!("      • Using direct analysis with PRISMv1...");
            }
            call_graph::prism_v1::build_call_graph(&kb.structure)
        };

        if verbose {
            println!("   → Building reverse call graphs...");
        }
        if use_precise == 2 {
            call_graph::prism_v2::populate_called_by_from_graph(&mut kb);
        } else {
            call_graph::prism_v1::populate_called_by(&mut kb);
        }
    } else if verbose {
        println!("   [!]  Skipping call graph (too large, would use excessive memory)");
    }

    if verbose {
        println!("   → Detecting patterns...");
    }
    kb.patterns = patterns::detect_patterns(&kb);

    if verbose {
        println!("   → Finding entry points...");
    }
    kb.entry_points = entry_points::find_entry_points(&kb);

    if verbose {
        println!("   → Analyzing dependencies... (pre-computed during parse)");
    }

    kb
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn go_modules_are_read_from_the_go_mod_above_each_go_file() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path();
        fs::create_dir_all(root.join("tools/gen/cmd")).unwrap();
        fs::create_dir_all(root.join("docs")).unwrap();
        fs::write(
            root.join("go.mod"),
            "// header\nmodule example.com/demo // the module\n\ngo 1.22\n",
        )
        .unwrap();
        fs::write(
            root.join("tools/gen/go.mod"),
            "module \"example.com/gen\"\n",
        )
        .unwrap();
        // Not above any Go file: never read.
        fs::write(root.join("docs/go.mod"), "module example.com/docs\n").unwrap();

        let go = FileData {
            language: "go".to_string(),
            ..Default::default()
        };
        let python = FileData {
            language: "python".to_string(),
            ..Default::default()
        };
        let structure: BTreeMap<String, FileData> = [
            ("main.go".to_string(), go.clone()),
            ("./tools/gen/cmd/main.go".to_string(), go),
            ("docs/conf.py".to_string(), python.clone()),
        ]
        .into();
        assert_eq!(
            go_modules(root, &structure),
            [
                (String::new(), "example.com/demo".to_string()),
                ("tools/gen".to_string(), "example.com/gen".to_string()),
            ]
        );

        let no_go: BTreeMap<String, FileData> = [("docs/conf.py".to_string(), python)].into();
        assert!(go_modules(root, &no_go).is_empty());
    }
}
