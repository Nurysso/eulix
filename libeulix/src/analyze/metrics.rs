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
    });
    metrics.truncate(k);

    MetricsReport {
        metadata: &kb.metadata,
        top_complex_functions: metrics,
    }
}
