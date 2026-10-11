use crate::struc::kb_struct::{IndicesView, KnowledgeBase};

pub fn generate_indices_view(kb: &KnowledgeBase) -> IndicesView<'_> {
    let mut ix = IndicesView::default();
    for (path, fd) in &kb.structure {
        for f in &fd.functions {
            ix.functions_by_name
                .entry(f.name.as_str())
                .or_default()
                .push(format!("{}:{}", path, f.line_start));
            for t in &f.tags {
                ix.functions_by_tag
                    .entry(t.as_str())
                    .or_default()
                    .push(f.id.as_str());
            }
            for c in &f.calls {
                ix.functions_calling
                    .entry(c.callee.as_str())
                    .or_default()
                    .push(f.id.as_str());
            }
        }
        for c in &fd.classes {
            ix.types_by_name
                .entry(c.name.as_str())
                .or_default()
                .push(format!("{}:{}", path, c.line_start));
            for m in &c.methods {
                ix.functions_by_name
                    .entry(m.name.as_str())
                    .or_default()
                    .push(format!("{}:{}", path, m.line_start));
                for t in &m.tags {
                    ix.functions_by_tag
                        .entry(t.as_str())
                        .or_default()
                        .push(m.id.as_str());
                }
            }
        }
    }
    ix
}
