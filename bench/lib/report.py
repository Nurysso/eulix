# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Render a comparison as a terminal table and a Markdown report."""
from datetime import datetime

from .compare import Cell

GROUP_TITLES = {"retrieval": "Retrieval accuracy", "indexing": "Indexing speed and memory",
                "query": "Query speed", "parser": "Parser quality"}
SUMMARY = [  # (group, metric, label)
    ("retrieval", "hit@1", "Hit@1"), ("retrieval", "mrr", "MRR"),
    ("query", "warm_p50_ms", "warm p50"), ("query", "cold_first_answer_s", "cold first answer"),
    ("query", "server_tree_rss_mb", "server memory"), ("indexing", "total_s", "analyze total"),
]


def _human_bytes(v: float) -> str:
    for unit in ("B", "KB", "MB", "GB"):
        if abs(v) < 1024 or unit == "GB":
            return f"{v:.0f} {unit}" if unit == "B" else f"{v:.1f} {unit}"
        v /= 1024
    return f"{v:.1f} GB"


def fmt_num(v, unit: str, kind: str) -> str:
    if v is None:
        return "-"
    if isinstance(v, bool):
        return "yes" if v else "no"
    if kind == "size":
        return _human_bytes(v)
    if unit == "s":
        return f"{v:.2f} s" if v < 10 else f"{v:.1f} s"
    if unit == "ms":
        return f"{v:.1f} ms" if v < 100 else f"{v:.0f} ms"
    if unit == "MB":
        return f"{v:.0f} MB"
    if isinstance(v, float):
        return f"{v:.3f}" if kind in ("accuracy", "ratio") else f"{v:.1f}"
    return str(v)


def fmt_value(s: dict | None, unit: str, kind: str) -> str:
    if s is None:
        return "-"
    if "count" in s and kind in ("accuracy", "ratio"):
        return f"{s['count']}/{s['of']} ({fmt_num(s['value'], unit, kind)})"
    if "count" in s and kind == "count":
        return f"{s['count']}/{s['of']}"
    text = fmt_num(s.get("value"), unit, kind)
    if s.get("n", 1) > 1 and s.get("min") != s.get("max"):
        text += f" [{fmt_num(s['min'], unit, kind)}..{fmt_num(s['max'], unit, kind)}]"
    return text


def fmt_delta(d, kind: str = "") -> str:
    if d is None:
        return ""
    if kind in ("accuracy", "count") and isinstance(d, int):
        return f"{d:+d}" if d else "0"
    if kind == "accuracy":
        return f"{d:+.3f}"
    return f"{d:+.1%}"


def cell_title(c: Cell) -> str:
    return f"{GROUP_TITLES[c.group]}: {c.corpus}" + (f" [{c.engine}]" if c.engine else "")


# --- terminal ---------------------------------------------------------------
def terminal(cells: list[Cell], side_a, side_b, warnings: list[str]) -> str:
    a, b = side_a.rev.label, side_b.rev.label
    lines = []
    for c in cells:
        lines.append("")
        lines.append(f"== {cell_title(c)}")
        if c.status != "ok":
            lines.append(f"   {c.status}: {c.note}")
            continue
        if c.note:
            lines.append(f"   ({c.note})")
        table = [("metric", a, b, "change", "")]
        for r in c.rows:
            table.append((r.name, fmt_value(r.a, r.unit, r.kind), fmt_value(r.b, r.unit, r.kind),
                          fmt_delta(r.delta, r.kind), ("FLAG " + r.flag) if r.flag else ""))
        widths = [max(len(row[i]) for row in table) for i in range(4)]
        for row in table:
            lines.append("   " + "  ".join(row[i].ljust(widths[i]) for i in range(4)) + ("  " + row[4] if row[4] else ""))
        for m in [m for m in c.movers if m["worse"]][:10]:
            lines.append(f"   worse  {m['id']}: {m['reason']}  (B's top hit: {m['top_b'] or '-'})")
        better = [m for m in c.movers if not m["worse"]]
        if better:
            lines.append(f"   better {len(better)} question(s): " + ", ".join(m["id"] for m in better[:8])
                         + (" ..." if len(better) > 8 else ""))
    flags = [f"{cell_title(c)}: {f}" for c in cells for f in c.flags]
    lines.append("")
    for w in warnings:
        lines.append(f"warning: {w}")
    missing = [c for c in cells if c.status == "missing"]
    if missing:
        lines.append(f"{len(missing)} cell(s) missing results (run without --no-run to measure them)")
    lines.append(f"{len(flags)} regression flag(s)" + (":" if flags else ""))
    lines.extend(f"  - {f}" for f in flags)
    return "\n".join(lines)


# --- markdown ---------------------------------------------------------------
def _md_table(header, rows) -> list[str]:
    out = ["| " + " | ".join(header) + " |", "|" + "|".join("---" for _ in header) + "|"]
    out += ["| " + " | ".join(str(x).replace("|", "\\|") for x in r) + " |" for r in rows]
    return out


def _summary(cells: list[Cell], a: str, b: str) -> list[str]:
    by = {(c.group, c.corpus, c.engine): c for c in cells if c.status == "ok"}
    out = []
    for c in cells:
        if c.group != "retrieval" or c.status != "ok":
            continue
        rows = []
        for group, name, label in SUMMARY:
            src = by.get((group, c.corpus, c.engine if group != "indexing" else None))
            row = next((r for r in src.rows if r.name == name), None) if src else None
            if row:
                rows.append((label, fmt_value(row.a, row.unit, row.kind), fmt_value(row.b, row.unit, row.kind),
                             fmt_delta(row.delta, row.kind)))
        out += [f"**{c.corpus} [{c.engine}]**", ""] + _md_table(["", a, b, "change"], rows) + [""]
    return out


def markdown(cells: list[Cell], side_a, side_b, warnings: list[str], suite_info: dict) -> str:
    a, b = side_a.rev.label, side_b.rev.label
    flags = [(cell_title(c), f) for c in cells for f in c.flags]
    missing = [c for c in cells if c.status == "missing"]
    verdict = "missing data" if missing else (f"{len(flags)} regression flag(s)" if flags else "no regressions")
    m = side_b.machine
    out = [
        f"# eulix {a} vs {b}",
        "",
        f"- A: `{a}` ({side_a.rev.sha[:12]}, eulix {side_a.meta.get('app_version')})",
        f"- B: `{b}` ({side_b.rev.sha[:12]}, eulix {side_b.meta.get('app_version')})",
        f"- Date: {datetime.now().strftime('%Y-%m-%d %H:%M')}",
        f"- Machine: {m['cpu']}, {m['logical_cpus']} threads, {m['mem_gib']} GiB, "
        f"governor {m['info'].get('governor')}, device {side_b.device}",
        f"- Engines: {a}: {', '.join(side_a.engines)}; {b}: {', '.join(side_b.engines)}",
        f"- Bench: {side_b.harness['bench_commit'][:12]}{' (dirty)' if side_b.harness['dirty'] else ''}; "
        + "; ".join(f"{k} {v}" for k, v in suite_info.items()),
        "",
        f"## Verdict: {verdict}",
        "",
    ]
    out += [f"- {t}: {f}" for t, f in flags] or ["Nothing got worse by the flag rules."]
    out += [""] + [f"> Warning: {w}" for w in warnings] + ([""] if warnings else [])
    summary = _summary(cells, a, b)
    if summary:
        out += ["## Summary", ""] + summary
    for group in ("retrieval", "indexing", "query", "parser"):
        gcells = [c for c in cells if c.group == group]
        if not gcells:
            continue
        out += [f"## {GROUP_TITLES[group]}", ""]
        for c in gcells:
            out += [f"### {c.corpus}" + (f" [{c.engine}]" if c.engine else ""), ""]
            if c.status != "ok":
                out += [f"_{c.status}: {c.note}_", ""]
                continue
            if c.note:
                out += [f"_{c.note}_", ""]
            out += _md_table(["metric", a, b, "change", "flag"],
                             [(r.name, fmt_value(r.a, r.unit, r.kind), fmt_value(r.b, r.unit, r.kind),
                               fmt_delta(r.delta, r.kind), r.flag) for r in c.rows]) + [""]
            if c.movers:
                rows = [(m["id"], m["style"], m["rank_a"] or "-", m["rank_b"] or "-", m["reason"], m["top_b"])
                        for m in c.movers]
                table = _md_table(["question", "style", f"rank {a}", f"rank {b}", "change", f"{b} top hit"], rows)
                if len(rows) > 10:
                    out += ["<details><summary>Questions whose rank changed "
                            f"({sum(m['worse'] for m in c.movers)} worse, "
                            f"{sum(not m['worse'] for m in c.movers)} better)</summary>", ""] + table + ["", "</details>"]
                else:
                    out += table
                out += [""]
    out += ["## Environment", "",
            f"- {a}: {side_a.meta.get('go_version')}; {side_a.meta.get('rustc_version')}",
            f"- {b}: {side_b.meta.get('go_version')}; {side_b.meta.get('rustc_version')}",
            f"- Python env: {side_b.env_info}", ""]
    return "\n".join(out)
