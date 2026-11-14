# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Match two versions' results cell by cell and flag regressions.

A cell is (group, corpus, engine). Flag rules:
  retrieval  a question A found and B didn't, or B ranks lower, or B bypassed
             retrieval / errored where A answered
  timings    median(B) > 1.10 x median(A), min(B) > max(A) (outside the run-to-run
             spread), and the gap is above a floor; mirrored when higher is better
  parser     more files failed, call edges down more than 5%, or determinism lost
Everything else is reported as a difference only.
"""
import statistics
from dataclasses import dataclass, field

from . import corpora, store, suite
from .groups import indexing, parser, query, retrieval

THRESHOLD = 0.10
CALL_EDGE_DROP = 0.05
FLOORS = {"time": 0.05, "latency": 5.0, "memory": 10.0, "size": 64 * 1024}
ENGINE_ORDER = ["etiya", "ehnas"]
# The current direction and kind of each indexing metric. Saved documents carry the
# definition they were measured with; using today's means a rule change (say, a
# metric becoming informational) applies to old results without re-measuring.
CURRENT_DEFS = {name: {"unit": unit, "better": better, "kind": kind} for name, unit, better, kind in indexing.METRICS}


@dataclass
class Row:
    name: str
    unit: str
    kind: str
    a: dict | None
    b: dict | None
    delta: float | None = None  # B vs A: relative change; for accuracy, the absolute difference
    flag: str = ""


@dataclass
class Cell:
    group: str
    corpus: str
    engine: str | None
    status: str = "ok"  # ok | n/a | missing
    note: str = ""
    rows: list = field(default_factory=list)
    movers: list = field(default_factory=list)
    flags: list = field(default_factory=list)
    docs: tuple = (None, None)


def summarize(m: dict | None) -> dict | None:
    """Central value plus spread: median/min/max/n for samples, else the single value."""
    if m is None:
        return None
    s = m.get("samples")
    if s:
        return {"value": statistics.median(s), "min": min(s), "max": max(s), "n": len(s)}
    out = {"value": m.get("value"), "n": 1}
    if "count" in m:
        out.update(count=m["count"], of=m["of"])
    return out


def _rel(a, b):
    if a in (None, 0) or b is None or isinstance(a, bool) or isinstance(b, bool):
        return None
    return (b - a) / abs(a)


def judge(m: dict, a: dict, b: dict) -> str:
    """Return a flag reason, or "" when B is not worse than A by the rules."""
    name, better, kind = m["name"], m.get("better"), m.get("kind")
    av, bv = a.get("value"), b.get("value")
    if av is None or bv is None:
        return ""
    if kind == "bool":
        return "determinism lost" if av is True and bv is False else ""
    if name == "files_failed":
        return f"files failed {av} -> {bv}" if bv > av else ""
    if name == "call_edges":
        return f"call edges down {(av - bv) / av:.1%}" if av and bv < av * (1 - CALL_EDGE_DROP) else ""
    if kind not in FLOORS and not (kind == "ratio" and better == "higher" and m.get("samples")):
        return ""
    floor = FLOORS.get(kind, 0)
    amin, amax = a.get("min", av), a.get("max", av)
    bmin, bmax = b.get("min", bv), b.get("max", bv)
    why = "single run, no spread to compare" if a.get("n", 1) == 1 and b.get("n", 1) == 1 \
        else "outside run-to-run spread"
    if better == "lower" and bv > av * (1 + THRESHOLD) and bmin > amax and bv - av > floor:
        return f"{_rel(av, bv):+.1%}, {why}"
    if better == "higher" and bv < av * (1 - THRESHOLD) and bmax < amin and av - bv > floor:
        return f"{_rel(av, bv):+.1%}, {why}"
    return ""


def metric_rows(ma: list, mb: list) -> tuple[list, list]:
    a_by = {m["name"]: m for m in ma}
    b_by = {m["name"]: m for m in mb}
    rows, flags = [], []
    for name in list(a_by) + [n for n in b_by if n not in a_by]:
        m = {**(a_by.get(name) or b_by[name]), **CURRENT_DEFS.get(name, {})}
        a, b = summarize(a_by.get(name)), summarize(b_by.get(name))
        row = Row(name, m.get("unit", ""), m.get("kind", ""), a, b)
        if a and b:
            if row.kind in ("accuracy", "count") and "count" in a and "count" in b:
                row.delta = b["count"] - a["count"]  # questions gained or lost, not a percentage
            elif row.kind == "accuracy" and a.get("value") is not None and b.get("value") is not None:
                row.delta = round(b["value"] - a["value"], 3)
            else:
                row.delta = _rel(a.get("value"), b.get("value"))
            row.flag = judge(m, a, b)
            if row.flag:
                flags.append(f"{name}: {row.flag}")
        rows.append(row)
    return rows, flags


def _worse(ea: dict, eb: dict) -> str:
    def bad(e):
        return e.get("error") or e.get("bypassed")
    ra, rb = ea.get("rank"), eb.get("rank")
    if bad(eb) and not bad(ea):
        return "errored" if eb.get("error") else "skipped retrieval"
    if ra is not None and (rb is None or rb > ra):
        return f"rank {ra} -> {rb if rb is not None else 'not found'}"
    return ""


def retrieval_cell(cell: Cell, corpus, questions, da: dict, db: dict) -> None:
    graded = suite.graded(questions)
    qa, qb = da["details"]["questions"], db["details"]["questions"]
    common = [q for q in graded if q.qhash(corpus.name) in qa and q.qhash(corpus.name) in qb]
    if len(common) < len(graded):
        cell.note = f"{len(common)}/{len(graded)} questions measured on both sides"
    ea = [qa[q.qhash(corpus.name)] for q in common]
    eb = [qb[q.qhash(corpus.name)] for q in common]
    cell.rows, _ = metric_rows(retrieval.aggregate(ea), retrieval.aggregate(eb))
    for q, a, b in zip(common, ea, eb):
        reason = _worse(a, b)
        if reason or a.get("rank") != b.get("rank"):
            cell.movers.append({"id": q.id, "style": q.style, "text": q.text, "rank_a": a.get("rank"),
                                "rank_b": b.get("rank"), "top_b": (b.get("top10") or [""])[0],
                                "worse": bool(reason), "reason": reason or "better"})
            if reason:
                cell.flags.append(f"{q.id}: {reason}")
    cell.movers.sort(key=lambda m: (not m["worse"], m["id"]))


def _fields(group, side, corpus, engine, questions):
    if group == "indexing":
        return indexing.fields(side, corpus)
    if group == "parser":
        return parser.fields(side, corpus)
    if group == "retrieval":
        return retrieval.fields(side, corpus, engine)
    return query.fields(side, corpus, engine, questions)


def build_cells(side_a, side_b, selected, groups) -> list[Cell]:
    cells = []
    for c in selected:
        questions = suite.load(c.questions)
        for g in groups:
            if g not in c.measures or (g == "query" and not questions):
                continue
            engines = [None]
            if g in ("retrieval", "query"):
                engines = [e for e in ENGINE_ORDER if e in side_a.engines or e in side_b.engines]
            for e in engines:
                cell = Cell(g, c.label, e)
                cells.append(cell)
                lacking = [s.rev.label for s in (side_a, side_b) if e and e not in s.engines]
                if lacking:
                    cell.status, cell.note = "n/a", f"{', '.join(lacking)}: no {e} engine"
                    continue
                sa, sb = (side_a, side_b) if g != "retrieval" else (side_a.accuracy_side(), side_b.accuracy_side())
                da = store.load(sa, g, _fields(g, sa, c, e, questions))
                db = store.load(sb, g, _fields(g, sb, c, e, questions))
                cell.docs = (da, db)
                if not da or not db:
                    cell.status = "missing"
                    cell.note = "not measured: " + ", ".join(s.rev.label for s, d in ((side_a, da), (side_b, db))
                                                             if not d)
                    continue
                if g == "retrieval":
                    retrieval_cell(cell, c, questions, da, db)
                else:
                    cell.rows, cell.flags = metric_rows(da["metrics"], db["metrics"])
    return cells


def environment_warnings(cells) -> list[str]:
    """Timing results taken under different CPU power settings or load aren't comparable."""
    out = set()
    for cell in cells:
        da, db = cell.docs
        if cell.group not in ("indexing", "query") or not da or not db:
            continue
        ia, ib = da["machine"]["info"], db["machine"]["info"]
        for k in ("governor", "epp", "boost"):
            if ia.get(k) != ib.get(k):
                out.add(f"CPU {k} differed between the timing runs ({ia.get(k)} vs {ib.get(k)})")
        la, lb = ia.get("loadavg_start") or 0, ib.get("loadavg_start") or 0
        if max(la, lb) > 2 and abs(la - lb) > 1.5:
            out.add(f"system load differed at the start of the timing runs ({la:.1f} vs {lb:.1f})")
    return sorted(out)


def verdict(cells) -> int:
    if any(c.status == "missing" for c in cells):
        return 2
    return 1 if any(c.flags for c in cells) else 0


def corpus_list(opts):
    return corpora.select(corpora.load_all(), opts.corpus, opts.large)
