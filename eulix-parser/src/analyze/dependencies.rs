use rayon::prelude::*;
use std::collections::{HashMap, HashSet};

use crate::struc::kb_struct::{ExternalDependency, KnowledgeBase};

pub fn analyze_external_deps(kb: &KnowledgeBase) -> Vec<ExternalDependency> {
    let all_deps: Vec<_> = kb
        .structure
        .par_iter()
        .flat_map(|(filepath, filedata)| {
            filedata
                .imports
                .iter()
                .filter(|i| i.import_type != "internal" && i.import_type != "cgo")
                .map(|i| (i.module.clone(), filepath.clone(), i.import_type.clone()))
                .collect::<Vec<_>>()
        })
        .collect();

    // A HashSet for `used_by` because the same module can legitimately
    // be imported multiple times in one file (or the same file could
    // appear twice across parallel chunks) — dedup is required for
    // `import_count` to mean "number of distinct files," not "number of
    // import statements."
    let mut deps_map: HashMap<String, (HashSet<String>, String)> = HashMap::new();
    for (module, filepath, import_type) in all_deps {
        let entry = deps_map
            .entry(module)
            .or_insert_with(|| (HashSet::new(), import_type));
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
