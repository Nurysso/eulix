// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

use crate::struc::kb_struct::{FunctionMetric, KnowledgeBase, MetricsReport};

// Borrows from `kb` (`'a`) instead of cloning every function's name into
// the report, since this can run over every function in the codebase and
// the report is typically discarded right after being printed/serialized.
pub fn generate_metrics<'a>(kb: &'a KnowledgeBase, k: usize) -> MetricsReport<'a> {
    use std::cmp::Ordering;

    let mut metrics: Vec<FunctionMetric<'a>> = kb
        .structure
        .iter()
        .flat_map(|(file, fd)| {
            let top_level = fd.functions.iter().map(move |f| FunctionMetric {
                name: &f.name,
                file: file.as_str(),
                complexity: f.complexity,
                importance_score: f.importance_score,
                line_start: f.line_start,
                line_end: f.line_end,
            });
            let methods = fd.classes.iter().flat_map(move |c| {
                c.methods.iter().map(move |m| FunctionMetric {
                    name: &m.name,
                    file: file.as_str(),
                    complexity: m.complexity,
                    importance_score: m.importance_score,
                    line_start: m.line_start,
                    line_end: m.line_end,
                })
            });
            top_level.chain(methods)
        })
        .collect();

    // Complexity first, importance as tiebreaker, name last: this is
    // meant to surface "the riskiest functions to touch," and raw
    // complexity is the strongest available proxy for that; importance
    // and name only disambiguate ties deterministically.
    metrics.sort_unstable_by(|a, b| {
        b.complexity
            .cmp(&a.complexity)
            .then_with(|| {
                b.importance_score
                    .partial_cmp(&a.importance_score)
                    .unwrap_or(Ordering::Equal)
            })
            .then_with(|| a.name.cmp(b.name))
            .then_with(|| a.file.cmp(b.file))
            .then_with(|| a.line_start.cmp(&b.line_start))
    });
    metrics.truncate(k);

    MetricsReport {
        metadata: &kb.metadata,
        top_complex_functions: metrics,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::struc::kb_struct::{
        CallGraph, DependencyGraph, FileData, Function, Indices, Metadata, PatternInfo,
    };
    use std::collections::HashMap;

    #[test]
    fn same_named_ties_are_broken_by_file() {
        // Locale files with an identical `error` function: the top-K cut
        // used to keep an arbitrary one of them.
        let mut structure = HashMap::new();
        for path in [
            "locales/zh.ts",
            "locales/ps.ts",
            "locales/az.ts",
            "locales/sl.ts",
            "locales/fi.ts",
            "locales/hu.ts",
            "locales/fa.ts",
        ] {
            let f = Function {
                name: "error".to_string(),
                complexity: 29,
                ..Default::default()
            };
            structure.insert(
                path.to_string(),
                FileData {
                    functions: vec![f],
                    ..Default::default()
                },
            );
        }
        let kb = KnowledgeBase {
            metadata: Metadata {
                version: String::new(),
                git_hash: String::new(),
                project_name: String::new(),
                project_hash: String::new(),
                parsed_at: String::new(),
                languages: vec![],
                total_files: 0,
                total_loc: 0,
                total_functions: 0,
                total_classes: 0,
                total_methods: 0,
            },
            structure,
            call_graph: CallGraph::default(),
            dependency_graph: DependencyGraph::default(),
            indices: Indices::default(),
            entry_points: vec![],
            external_dependencies: vec![],
            patterns: PatternInfo::default(),
        };
        let report = generate_metrics(&kb, 3);
        let files: Vec<&str> = report
            .top_complex_functions
            .iter()
            .map(|m| m.file)
            .collect();
        assert_eq!(
            files,
            vec!["locales/az.ts", "locales/fa.ts", "locales/fi.ts"]
        );
    }
}
