use crate::memory::max_rss_mb;
use crate::stats::FailedFile;
use crate::stats::{LangCounts, ParseStats, PartialFile};
use crate::struc::kb_struct::{Class, FileData, Function, KnowledgeBase, Metadata};
use rustc_hash::FxHashMap;
use std::fs;
use std::io::{BufWriter, Write};
use std::path::Path;

pub fn field_report(kb: &KnowledgeBase) {
    fn json_len<T: serde::Serialize>(v: &T) -> usize {
        serde_json::to_vec(v).map_or(0, |b| b.len())
    }
    let (mut n, mut calls, mut vars, mut cf, mut exc, mut rest) = (0usize, 0, 0, 0, 0, 0);
    for fd in kb.structure.values() {
        let all = fd
            .functions
            .iter()
            .chain(fd.classes.iter().flat_map(|c| c.methods.iter()));
        for f in all {
            n += 1;
            calls += json_len(&f.calls);
            vars += json_len(&f.variables);
            cf += json_len(&f.control_flow);
            exc += json_len(&f.exceptions);
            rest += json_len(&f.signature) + json_len(&f.docstring) + json_len(&f.params);
        }
    }
    let mb = |b: usize| b as f64 / 1e6;
    eprintln!(
        "size_of: Function={}B Class={}B FileData={}B",
        std::mem::size_of::<Function>(),
        std::mem::size_of::<Class>(),
        std::mem::size_of::<FileData>()
    );
    eprintln!(
        "{n} fns | calls {:.0}MB | variables {:.0}MB | control_flow {:.0}MB | exceptions {:.0}MB | sig+doc+params {:.0}MB (JSON bytes; heap is larger)",
        mb(calls), mb(vars), mb(cf), mb(exc), mb(rest)
    );
}

pub fn print_final_summary(metadata: &Metadata, stats: &ParseStats, total_time: f64) {
    println!("EXECUTION TIME");
    println!("   Total:                  {:.2}s", total_time);
    println!("   Peak Rss:               {:.1} MB", max_rss_mb());
    println!();
    println!("CODE METRICS");
    println!("   Files Processed:        {}", metadata.total_files);
    println!("   Total Lines of Code:    {}", metadata.total_loc);
    println!("   Functions:              {}", metadata.total_functions);
    println!("   Classes:                {}", metadata.total_classes);
    println!("   Methods:                {}", metadata.total_methods);
    println!();
    println!("LANGUAGES DETECTED");
    for lang in &metadata.languages {
        println!("   • {}", lang);
    }
    println!();
    println!(" PARSING STATISTICS");
    print_parse_counts(stats, "   ");
    print_parse_details(stats);
    println!(" Analysis complete!");
}

pub fn print_parse_counts(stats: &ParseStats, indent: &str) {
    let total = stats.total().max(1) as f64;
    let pct = |n: usize| 100.0 * n as f64 / total;
    println!(
        "{indent}Clean:        {:>8} files ({:.2}%)",
        stats.clean,
        pct(stats.clean)
    );
    println!(
        "{indent}Partial:      {:>8} files ({:.2}%)  syntax errors; extracted best-effort, kept in KB",
        stats.partial.len(),
        pct(stats.partial.len())
    );
    println!(
        "{indent}Failed:       {:>8} files ({:.2}%)  no usable tree; NOT in KB",
        stats.failed.len(),
        pct(stats.failed.len())
    );
}

pub fn print_parse_details(stats: &ParseStats) {
    const TOP: usize = 10;

    if !stats.by_lang.is_empty() {
        println!();
        println!("   BY LANGUAGE");
        println!(
            "   {:<12} {:>9} {:>9} {:>9} {:>9}",
            "language", "clean", "partial", "failed", "partial%"
        );
        let mut rows: Vec<(&String, &LangCounts)> = stats.by_lang.iter().collect();
        rows.sort_by(|a, b| a.0.cmp(b.0));
        for (lang, c) in rows {
            let n = (c.clean + c.partial + c.failed).max(1) as f64;
            println!(
                "   {:<12} {:>9} {:>9} {:>9} {:>8.2}%",
                lang,
                c.clean,
                c.partial,
                c.failed,
                100.0 * c.partial as f64 / n
            );
        }
    }

    if !stats.partial.is_empty() {
        println!();
        println!(
            "   PARTIAL PARSES (worst {} by ERROR+MISSING node count)",
            TOP
        );
        let mut worst: Vec<&PartialFile> = stats.partial.iter().collect();
        worst.sort_by(|a, b| b.total().cmp(&a.total()).then_with(|| a.path.cmp(&b.path)));
        for p in worst.iter().take(TOP) {
            let loc = p
                .first_error_line
                .map(|l| format!(":{l}"))
                .unwrap_or_default();
            println!(
                "   {:>5} err {:>5} missing  [{}] {}{}",
                p.error_nodes, p.missing_nodes, p.language, p.path, loc
            );
        }
        if stats.partial.len() > TOP {
            println!("   ... and {} more", stats.partial.len() - TOP);
        }
    }

    if !stats.failed.is_empty() {
        println!();
        println!("   FAILURES (grouped by reason)");
        let mut by_reason: FxHashMap<&str, (usize, &str)> = FxHashMap::default();
        for f in &stats.failed {
            by_reason
                .entry(f.reason.as_str())
                .or_insert((0, f.path.as_str()))
                .0 += 1;
        }
        let mut reasons: Vec<(&str, (usize, &str))> = by_reason.into_iter().collect();
        reasons.sort_by(|a, b| b.1 .0.cmp(&a.1 .0).then_with(|| a.0.cmp(b.0)));
        for (reason, (count, example)) in reasons.iter().take(TOP) {
            println!("   {:>6}x {}  (e.g. {})", count, reason, example);
        }
        if reasons.len() > TOP {
            println!("   ... and {} more distinct reasons", reasons.len() - TOP);
        }
    }

    if !stats.partial.is_empty() || !stats.failed.is_empty() {
        println!();
        println!("   Tip: --parse-report <file.tsv> writes the full list (diff it between runs).");
    }
}

pub fn write_parse_report(path: &Path, stats: &ParseStats) -> std::io::Result<()> {
    if let Some(parent) = path.parent() {
        if !parent.as_os_str().is_empty() {
            fs::create_dir_all(parent)?;
        }
    }
    let mut w = BufWriter::new(fs::File::create(path)?);
    writeln!(
        w,
        "status\tlanguage\terror_nodes\tmissing_nodes\tfirst_error_line\tpath\treason"
    )?;

    let mut partial: Vec<&PartialFile> = stats.partial.iter().collect();
    partial.sort_by(|a, b| a.path.cmp(&b.path));
    for p in partial {
        writeln!(
            w,
            "partial\t{}\t{}\t{}\t{}\t{}\t",
            p.language,
            p.error_nodes,
            p.missing_nodes,
            p.first_error_line
                .map(|l| l.to_string())
                .unwrap_or_default(),
            p.path
        )?;
    }

    let mut failed: Vec<&FailedFile> = stats.failed.iter().collect();
    failed.sort_by(|a, b| a.path.cmp(&b.path));
    for f in failed {
        writeln!(
            w,
            "failed\t{}\t\t\t\t{}\t{}",
            f.language,
            f.path,
            f.reason.replace(['\t', '\n', '\r'], " ")
        )?;
    }
    w.flush()
}
