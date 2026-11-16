"""Turn scored runs into report.md and a printed summary."""

from __future__ import annotations

from pathlib import Path

from .aggregate import repo_summary

COLS = ("TP", "FP", "FN", "TN", "precision", "recall", "recall_all", "f1", "accuracy", "specificity", "strict_precision")


def pct(x) -> str:
    return "–" if x is None else f"{x * 100:.1f}%"


def cell(k, v) -> str:
    return pct(v) if k in ("precision", "recall", "recall_all", "f1", "accuracy", "specificity", "strict_precision") else str(v)


def summary_rows(results: list[dict], per_site: bool = False) -> list[list[str]]:
    rows = []
    for r in results:
        if "error" in r:
            rows.append([r["corpus"], r["lang"], f'v{r["prism"]}', "ERROR"] + [""] * (len(COLS) - 1))
            continue
        s = r["score"]["per_call_site"]["summary"] if per_site else repo_summary(r)
        rows.append([r["corpus"], r["lang"], f'v{r["prism"]}', *(cell(k, s.get(k)) for k in COLS)])
    return rows


def table(head: list[str], rows: list[list[str]]) -> str:
    lines = ["| " + " | ".join(head) + " |", "|" + "|".join("---" for _ in head) + "|"]
    lines += ["| " + " | ".join(str(x).replace("|", "\\|") for x in r) + " |" for r in rows]
    return "\n".join(lines)


def text_table(head: list[str], rows: list[list[str]]) -> str:
    w = [max(len(str(x)) for x in col) for col in zip(head, *rows)]
    fmt = lambda r: "  ".join(str(x).ljust(n) for x, n in zip(r, w))
    return "\n".join([fmt(head), fmt(["-" * n for n in w]), *map(fmt, rows)])


HEAD = ["corpus", "lang", "prism", "TP", "FP", "FN", "TN", "precision", "recall", "recall all", "F1", "accuracy",
        "specificity", "strict P"]

LANG_HEAD = ["lang", "prism", "repos", "mean P", "mean R", "mean R all", "mean F1", "R min–max",
             "pooled TP", "FP", "FN", "TN", "pooled P", "pooled R", "pooled R all", "pooled F1", "pooled strict P"]


def language_rows(langs: list[dict]) -> list[list[str]]:
    rows = []
    for g in langs:
        m, p, mm = g["mean_of_repos"], g["pooled"], g["min_max"]["recall"]
        rows.append([g["lang"], f'v{g["prism"]}', str(len(g["repos"])),
                     pct(m["precision"]), pct(m["recall"]), pct(m.get("recall_all")), pct(m["f1"]),
                     f"{pct(mm[0])}–{pct(mm[1])}" if mm else "–",
                     str(p["TP"]), str(p["FP"]), str(p["FN"]), str(p["TN"]),
                     pct(p["precision"]), pct(p["recall"]), pct(p["recall_all"]), pct(p["f1"]),
                     pct(p["strict_precision"])])
    return rows


DISPATCH_HEAD = ["corpus", "prism", "static P", "static R", "virtual P", "virtual R", "virtual share of TP"]


def dispatch_rows(results: list[dict]) -> list[list[str]]:
    """Static calls alone, beside virtual ones (where any override counts as right)."""
    rows = []
    for r in results:
        if "error" in r:
            continue
        d = r["score"]["by_dispatch"]
        st, vi = d.get("static", {}), d.get("virtual", {})
        tp = (st.get("TP") or 0) + (vi.get("TP") or 0)
        rows.append([r["corpus"], f'v{r["prism"]}', pct(st.get("precision")), pct(st.get("recall")),
                     pct(vi.get("precision")), pct(vi.get("recall")),
                     pct((vi.get("TP") or 0) / tp) if tp else "–"])
    return rows


def run_rows(results: list[dict]) -> list[list[str]]:
    rows = []
    for r in results:
        runs = r.get("runs")
        if not runs:
            continue
        spread = max((v for v in runs["summary_spread"].values() if v is not None), default=0)
        secs = runs["parser_seconds"]
        rows.append([r["corpus"], f'v{r["prism"]}', str(runs["n"]),
                     "yes" if runs["identical_graphs"] else "**no**", str(spread),
                     f'{runs["parser_seconds_mean"]:.3f}', f"{min(secs):.3f}–{max(secs):.3f}"])
    return rows


def write(results: list[dict], meta: dict, out: Path, langs: list[dict] | None = None) -> Path:
    md = [f"# Call-graph accuracy", "",
          f"Parser `{meta['parser']}` ({meta['parser_version']}), run {meta['started']}.", "",
          "Headline numbers count unique caller → callee pairs (PRISM v2 keeps one edge per pair); see "
          "bench/callgraph/README.md. TN = a caller's call to an external function (stdlib, dependency, builtin) that "
          "eulix correctly left unlinked. Strict P also counts eulix edges the reference cannot confirm as FPs.", "",
          "## Summary (caller → callee pairs)", "",
          *([f"Each graph was built and scored {meta['runs']} times; the numbers are the means over those runs "
             "(see Repeated runs)."] if meta.get("runs", 1) > 1 else []), "",
          table(HEAD, summary_rows(results)), "",
          *(["## Per language (real corpora)", "",
             "**Mean of repos**: the mean of the per-project precision, recall and F1, each project weighing the "
             "same (a project whose value is undefined is left out of that mean). **Pooled**: the counts of all "
             "projects of the language summed, then the metrics computed once, so large projects weigh more.", "",
             table(LANG_HEAD, language_rows(langs)), ""] if langs else []),
          *(["## Repeated runs", "",
             "PRISM is deterministic, so every run should give a byte-identical graph and a spread of 0. "
             "Spread is the largest max − min over the summary numbers.", "",
             table(["corpus", "prism", "runs", "identical graphs", "spread", "parser s (mean)", "parser s (min–max)"],
                   run_rows(results)), ""] if meta.get("runs", 1) > 1 else []),
          "**recall all** also counts as misses the project calls eulix cannot see: calls outside every eulix "
          "function, and calls to a callee with no eulix node. **precision** is judged on eulix's side: correct "
          "eulix pairs / (correct + wrong).", "",
          "## Static and virtual calls", "",
          "A virtual call (through an interface or an overridable method) accepts any override as correct, which "
          "is lenient. Static calls alone have one right answer.", "",
          table(DISPATCH_HEAD, dispatch_rows(results)), "",
          "## Per call site", "", "Every call counted separately; v2 is penalised for keeping one edge per pair.", "",
          table(HEAD, summary_rows(results, per_site=True)), ""]
    errs = [r for r in results if "error" in r]
    if errs:
        md += ["## Errors", "", *[f"- {r['corpus']} v{r['prism']}: {r['error']}" for r in errs], ""]
    for r in results:
        if "error" in r:
            continue
        sc = r["score"]
        md += [f"## {r['corpus']} ({r['lang']}) PRISM v{r['prism']}", "",
               f"Reference: {r['ref']['tool']} ({r['ref']['seconds']} s{', cached' if r['ref'].get('cached') else ''}). "
               f"Parser: {r['parser']['seconds']} s.", ""]
        kinds = [[k, *(cell(c, v[c]) for c in COLS)] for k, v in sc["by_kind"].items()]
        kinds += [[f"dispatch: {k}", *(cell(c, v[c]) for c in COLS)] for k, v in sc["by_dispatch"].items()]
        md += ["### By call kind", "", table(["kind", *HEAD[3:]], kinds), ""]
        e, ref = sc["eulix"], sc["reference"]
        cs = sc["per_call_site"]["summary"]
        md += ["### Counts", "",
               table(["what", "value"], [
                   ["reference call sites", ref["call_sites"]],
                   ["… resolved into the project / external / unknown",
                    f'{ref["by_status"].get("project", 0)} / {ref["by_status"].get("external", 0)} / {ref["by_status"].get("unknown", 0)}'],
                   ["… outside any eulix function (top-level code, skipped files)", ref["out_of_scope"]],
                   ["… callee has no eulix node (unmapped, not scored)", ref["unmapped_callee"]],
                   ["buckets", ", ".join(f"{k} {v}" for k, v in sorted(sc["buckets"].items()))],
                   ["eulix nodes (functions / methods / classes)", f'{e["nodes"]} ({e["functions"]} / {e["methods"]} / {e["classes"]})'],
                   ["eulix call edges (raw / unique / duplicates)", f'{e["call_edges_raw"]} / {e["call_edges_unique"]} / {e["duplicate_edges"]}'],
                   ["eulix edges the reference cannot confirm", e.get("unverifiable", sc["summary"]["unverifiable_edges"])],
                   ["eulix edges matched via a nearby line (multi-line calls)", e["fuzzy_line_matches"]],
                   ["correct pairs recorded at another call of the caller (v2 keeps one line per pair)", e.get("pairs_correct_for_another_call_of_the_caller", 0)],
                   ["extra targets on an otherwise correct call", e["extra_targets"]],
                   ["cross-language edges", e["cross_language_edges"]],
                   ["eulix ids shared by several definitions (overloads)", e.get("colliding_ids", 0)],
                   ["methods with ≥1 caller", f'{e["methods_with_caller"]} / {e["methods"]}'],
                   ["called project functions that eulix gives ≥1 caller", f'{e["expected_callees_with_any_eulix_caller"]} / {e["expected_callees"]}'],
                   ["per call site: TP / FP / FN / TN", f'{cs["TP"]} / {cs["FP"]} / {cs["FN"]} / {cs["TN"]}'],
                   ["per call site: precision / recall / F1", f'{pct(cs["precision"])} / {pct(cs["recall"])} / {pct(cs["f1"])}'],
                   ["eulix edges from files in other languages (not scored)", e["other_language_edges"]],
               ]), ""]
        if sc["worst_files"]:
            md += ["### Worst files (≥5 project calls)", "",
                   table(["file", "TP", "FP", "FN", "precision", "recall", "F1"],
                         [[w["file"], w["TP"], w["FP"], w["FN"], pct(w["precision"]), pct(w["recall"]), pct(w["f1"])]
                          for w in sc["worst_files"]]), ""]
        for title, key in (("False negatives (missed calls)", "FN"), ("Wrong target", "wrong_target"),
                           ("False positives on external calls", "FP_external"), ("Unverifiable eulix edges", "unverifiable")):
            ex = r["examples"].get(key) or []
            if not ex:
                continue
            md += [f"### {title}: {len(ex)} of {r['example_totals'].get(key, len(ex))}", ""]
            for x in ex:
                md.append(f"- `{x['where']}` **{x['name']}** {x['detail']}  \n  `{x['src']}`")
            md.append("")
    path = out / "report.md"
    path.write_text("\n".join(md))
    return path
