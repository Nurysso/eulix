// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use rayon::prelude::*;
use std::collections::{BTreeMap, BTreeSet};

use crate::struc::kb_struct::{ExternalDependency, KnowledgeBase};

pub fn analyze_external_deps(kb: &KnowledgeBase) -> Vec<ExternalDependency> {
    let all_deps: Vec<_> = kb
        .structure
        .par_iter()
        .flat_map(|(filepath, filedata)| {
            filedata
                .imports
                .iter()
                // PHP `include`/`require` name project files, not packages.
                .filter(|i| {
                    !matches!(
                        i.import_type.as_str(),
                        "internal" | "cgo" | "include" | "include_dynamic"
                    )
                })
                .map(|i| (i.module.clone(), filepath.clone(), i.import_type.clone()))
                .collect::<Vec<_>>()
        })
        .collect();

    // A set for `used_by` because the same module can legitimately
    // be imported multiple times in one file (or the same file could
    // appear twice across parallel chunks) — dedup is required for
    // `import_count` to mean "number of distinct files," not "number of
    // import statements."
    let mut deps_map: BTreeMap<String, (BTreeSet<String>, String)> = BTreeMap::new();
    for (module, filepath, import_type) in all_deps {
        let entry = deps_map
            .entry(module)
            .or_insert_with(|| (BTreeSet::new(), import_type));
        entry.0.insert(filepath);
    }

    deps_map
        .into_iter()
        .map(|(name, (files, import_type))| ExternalDependency {
            name,
            version: None,
            source: import_type, // "stdlib" | "external"
            import_count: files.len(),
            used_by: files.into_iter().collect(),
        })
        .collect()
}
