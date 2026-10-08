use crate::stats::{lang_counts, FailedFile, ParseStats, PartialFile};
use crate::struc::kb_struct::FileData;
use rustc_hash::{FxHashMap, FxHashSet};

/// Per-rayon-worker accumulator (fold) that is later merged (reduce).
#[derive(Default)]
pub struct Acc {
    pub structure: FxHashMap<String, FileData>,
    pub loc: usize,
    pub functions: usize,
    pub classes: usize,
    pub methods: usize,
    pub languages: FxHashSet<String>,
    pub stats: ParseStats,
    pub index: Vec<(String, u64, u32)>,
}

impl Acc {
    pub fn record_ok(&mut self, rel: String, fd: FileData, loc: (u64, u32)) {
        self.index.push((rel.clone(), loc.0, loc.1));
        self.loc += fd.loc;
        self.functions += fd.functions.len();
        self.classes += fd.classes.len();
        self.methods += fd.classes.iter().map(|c| c.methods.len()).sum::<usize>();
        if !self.languages.contains(fd.language.as_str()) {
            self.languages.insert(fd.language.clone());
        }

        let counts = lang_counts(&mut self.stats.by_lang, &fd.language);
        if fd.syntax.is_clean() {
            counts.clean += 1;
            self.stats.clean += 1;
        } else {
            counts.partial += 1;
            self.stats.partial.push(PartialFile {
                path: rel.clone(),
                language: fd.language.clone(),
                error_nodes: fd.syntax.error_nodes,
                missing_nodes: fd.syntax.missing_nodes,
                first_error_line: fd.syntax.first_error_line,
            });
        }
        self.structure.insert(rel, fd);
    }

    pub fn record_failed(&mut self, path: String, language: String, reason: String) {
        lang_counts(&mut self.stats.by_lang, &language).failed += 1;
        self.stats.failed.push(FailedFile {
            path,
            language,
            reason,
        });
    }

    pub fn merge(mut self, mut other: Acc) -> Acc {
        self.index.extend(other.index);

        if self.structure.len() < other.structure.len() {
            std::mem::swap(&mut self.structure, &mut other.structure);
        }
        self.structure.extend(other.structure);
        self.loc += other.loc;
        self.functions += other.functions;
        self.classes += other.classes;
        self.methods += other.methods;
        self.languages.extend(other.languages);
        self.stats.clean += other.stats.clean;
        self.stats.partial.extend(other.stats.partial);
        self.stats.failed.extend(other.stats.failed);
        for (lang, c) in other.stats.by_lang {
            let dst = self.stats.by_lang.entry(lang).or_default();
            dst.clean += c.clean;
            dst.partial += c.partial;
            dst.failed += c.failed;
        }
        self
    }
}
