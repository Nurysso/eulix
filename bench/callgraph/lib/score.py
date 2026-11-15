"""Score an eulix call graph against a reference call-site list.

Unit of scoring is the call site. The reference tool lists every call in the
project's source, saying where it lands:

    status "project"   resolved to one or more definitions inside the corpus
    status "external"  resolved to the standard library, a dependency, a builtin
    status "unknown"   the tool could not resolve it (dynamic call, missing dep)

Each site is put in exactly one bucket:

    TP            eulix links the site to an acceptable target
    FN            project call that eulix does not link
    wrong_target  project call that eulix links only to the wrong function
                  (counts as one FP and one FN)
    FP_external   external call that eulix links to a project function
    TN            external call that eulix leaves alone
    ignored       unknown / out-of-scope / unmapped sites with no eulix edge

An eulix edge that lands on no reference site with that name is "unverifiable"
(macro body, a file the tool skipped, a line the tool reported differently).
Precision ignores it; strict precision counts it as an FP.
"""

from __future__ import annotations

import re
from collections import Counter, defaultdict
from dataclasses import dataclass, field

FUZZY_LINES = 15  # max distance between eulix's call line and the callee name line
CTOR_NAMES = {"constructor", "__init__", "__new__", "new"}


def short(name: str) -> str:
    """`XMLNode::Name` / `initMenu.makeTree` -> the last segment."""
    return re.split(r"::|\.", name)[-1] if name else name


@dataclass
class Node:
    id: str
    name: str
    kind: str  # function | method | class
    file: str
    start: int
    end: int
    more: list = field(default_factory=list)  # other spans with the same id (overloads collapse in eulix)


@dataclass
class Site:
    file: str
    line: int
    name: str
    kind: str  # function | method | constructor
    status: str  # project | external | unknown
    targets: list = field(default_factory=list)  # [{"file","line","name"}]
    lines: list = field(default_factory=list)  # extra lines eulix may report
    col: int = 0
    dispatch: str = "static"  # static | virtual
    # filled in by score()
    caller: str | None = None
    expected: set = field(default_factory=set)
    bucket: str = ""
    hit: list = field(default_factory=list)  # eulix target ids landing here

    @classmethod
    def from_json(cls, d: dict) -> "Site":
        return cls(
            file=d["file"],
            line=d["line"],
            name=d.get("name", ""),
            kind=d.get("kind", "function"),
            status=d.get("status", "unknown"),
            targets=d.get("targets", []),
            lines=d.get("lines", []),
            col=d.get("col", 0),
            dispatch=d.get("dispatch", "static"),
        )


class SpanIndex:
    """Innermost function/method/class node containing a (file, line)."""

    def __init__(self, nodes: dict[str, Node]):
        self.by_file: dict[str, list[Node]] = defaultdict(list)
        for n in nodes.values():
            self.by_file[n.file].append(n)
            for f, a, b in n.more:
                self.by_file[f].append(Node(n.id, n.name, n.kind, f, a, b))
        for lst in self.by_file.values():
            lst.sort(key=lambda n: (n.start, -n.end))

    def containing(self, file: str, line: int, kinds=("function", "method")) -> list[Node]:
        """All nodes of `kinds` whose span holds `line`, innermost first."""
        out = [n for n in self.by_file.get(file, ()) if n.kind in kinds and n.start <= line <= n.end]
        out.sort(key=lambda n: n.end - n.start)
        return out

    def enclosing(self, file: str, line: int) -> Node | None:
        c = self.containing(file, line)
        return c[0] if c else None


def map_target(t: dict, kind: str, spans: SpanIndex) -> set[str]:
    """Eulix ids acceptable for one reference target."""
    file, line, name = t["file"], t["line"], t.get("name", "")
    funcs = spans.containing(file, line)
    named = [n for n in funcs if short(n.name) == short(name)]
    pick = named[0] if named else (funcs[0] if funcs and funcs[0].start == line else None)
    out = {pick.id} if pick else set()
    if kind == "constructor":
        classes = spans.containing(file, line, kinds=("class",))
        if classes:
            cls = classes[0]
            out.add(cls.id)
            for n in spans.by_file.get(file, ()):
                if n.kind == "method" and cls.start <= n.start <= cls.end and (short(n.name) in CTOR_NAMES or short(n.name) == short(cls.name)):
                    out.add(n.id)
    return out


def node_name(nodes: dict[str, Node], nid: str) -> str:
    if nid in nodes:
        return short(nodes[nid].name)
    head = nid.rsplit("::", 1)[0]
    return short(head.split("_")[-1])


LANG_OF_EXT = {
    ".go": "go", ".java": "java", ".py": "python", ".rs": "rust", ".php": "php",
    ".js": "js", ".mjs": "js", ".cjs": "js", ".jsx": "js",
    ".ts": "ts", ".tsx": "ts", ".mts": "ts", ".cts": "ts",
    ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp",
}
LANG_GROUP = {"js": "jsts", "ts": "jsts", "c": "ccpp", "cpp": "ccpp"}


def lang_group(path: str) -> str:
    dot = path.rfind(".")
    lang = LANG_OF_EXT.get(path[dot:].lower(), "?") if dot >= 0 else "?"
    return LANG_GROUP.get(lang, lang)


def metrics(c: Counter) -> dict:
    tp, fp, fn, tn = c["TP"], c["FP"], c["FN"], c["TN"]
    unv = c["unverifiable"]
    div = lambda a, b: round(a / b, 4) if b else None
    p, r = div(tp, tp + fp), div(tp, tp + fn)
    return {
        "TP": tp, "FP": fp, "FN": fn, "TN": tn,
        "precision": p,
        "recall": r,
        "f1": div(2 * p * r, p + r) if p and r else (0.0 if p is not None and r is not None else None),
        "accuracy": div(tp + tn, tp + fp + fn + tn),
        "specificity": div(tn, tn + fp),
        "strict_precision": div(tp, tp + fp + unv),
        "unverifiable_edges": unv,
    }


def score(nodes: dict[str, Node], edges: list[tuple[str, str, int]], sites: list[Site], lang: str | None = None) -> dict:
    """lang: the corpus language; eulix edges from files of other languages are dropped."""
    spans = SpanIndex(nodes)

    # --- reference side ------------------------------------------------------
    site_counts = Counter()
    by_key: dict[tuple, list[Site]] = defaultdict(list)  # (caller, line) -> sites
    by_caller_name: dict[tuple, list[Site]] = defaultdict(list)  # (caller, name) -> sites
    for s in sites:
        s.caller = (n.id if (n := spans.enclosing(s.file, s.line)) else None)
        if s.caller is None:
            s.bucket = "out_of_scope"
        elif s.status == "project":
            for t in s.targets:
                s.expected |= map_target(t, s.kind, spans)
            if not s.expected:
                s.bucket = "unmapped_callee"
        site_counts[s.status] += 1
        if s.caller is None:
            continue
        for ln in {s.line, *s.lines}:
            by_key[(s.caller, ln)].append(s)
        by_caller_name[(s.caller, s.name)].append(s)
    for lst in by_caller_name.values():
        lst.sort(key=lambda s: s.line)

    # --- eulix side ------------------------------------------------------------
    raw = len(edges)
    uniq = list(dict.fromkeys(edges))
    duplicates = raw - len(uniq)
    other_lang = 0
    if lang:
        want = LANG_GROUP.get(lang, lang)
        kept = [e for e in uniq if e[0] in nodes and lang_group(nodes[e[0]].file) == want]
        other_lang = len(uniq) - len(kept)
        uniq = kept
    cross_lang = sum(
        1 for f, t, _ in uniq
        if f in nodes and t in nodes and lang_group(nodes[f].file) != lang_group(nodes[t].file)
    )

    unverifiable: list[dict] = []
    fuzzy = 0

    def names_match(site: Site, target: str) -> bool:
        tname = node_name(nodes, target)
        if site.name == tname:
            return True
        return site.kind == "constructor" and (tname in CTOR_NAMES or target in site.expected)

    def pick(cands: list[Site], target: str) -> Site | None:
        good = [s for s in cands if target in s.expected and not s.hit]
        if good:
            return good[0]
        good = [s for s in cands if target in s.expected]
        if good:
            return good[0]
        named = [s for s in cands if names_match(s, target)]
        fresh = [s for s in named if not s.hit]
        return (fresh or named or [None])[0]

    for frm, to, line in uniq:
        site = pick(by_key.get((frm, line), []), to)
        if site is None:
            # multi-line call chains: eulix may report the line where the
            # expression starts, the reference the line of the callee name.
            near = [
                s for s in by_caller_name.get((frm, node_name(nodes, to)), [])
                if 0 <= s.line - line <= FUZZY_LINES or 0 <= line - s.line <= 2
            ]
            near.sort(key=lambda s: abs(s.line - line))
            site = pick(near, to)
            if site is not None:
                fuzzy += 1
        if site is None:
            unverifiable.append({"from": frm, "to": to, "line": line, "why": "no reference call with this name here"})
            continue
        site.hit.append(to)

    # --- buckets -------------------------------------------------------------------
    total = Counter()
    by_kind: dict[str, Counter] = defaultdict(Counter)
    by_dispatch: dict[str, Counter] = defaultdict(Counter)
    buckets = Counter()
    per_file: dict[str, Counter] = defaultdict(Counter)
    extra_fp = 0
    for s in sites:
        b = s.bucket
        hit = s.hit
        if b in ("out_of_scope", "unmapped_callee") or s.status == "unknown":
            if hit:
                for to in hit:
                    unverifiable.append({"from": s.caller, "to": to, "line": s.line,
                                         "why": f"reference: {b or 'unknown'} ({s.name})"})
            s.bucket = b or "unknown"
            buckets[s.bucket] += 1
            continue
        c = Counter()
        if s.status == "project":
            good = [t for t in hit if t in s.expected]
            if good:
                c["TP"] += 1
                c["FP"] += len(hit) - 1  # extra (ambiguous) targets on top of the right one
                extra_fp += len(hit) - 1
                s.bucket = "TP"
            elif hit:
                c["FP"] += len(hit)
                c["FN"] += 1
                s.bucket = "wrong_target"
            else:
                c["FN"] += 1
                s.bucket = "FN"
        else:  # external
            if hit:
                c["FP"] += len(hit)
                s.bucket = "FP_external"
            else:
                c["TN"] += 1
                s.bucket = "TN"
        buckets[s.bucket] += 1
        total.update(c)
        by_kind[s.kind].update(c)
        by_dispatch[s.dispatch].update(c)
        per_file[s.file].update(c)
    total["unverifiable"] = len(unverifiable)

    # --- caller -> callee pairs (the headline numbers) ---------------------------
    # PRISM v2 keeps one edge per (caller, target), at the first call line, so a
    # function calling `free` three times is one fact, not three. Group the sites
    # the same way: project calls by (caller, expected targets), external calls by
    # (caller, name). A group takes its best bucket.
    groups: dict[tuple, list[Site]] = defaultdict(list)
    for s in sites:
        if s.bucket in ("TP", "FN", "wrong_target"):
            groups[("p", s.caller, frozenset(s.expected))].append(s)
        elif s.bucket in ("TN", "FP_external"):
            groups[("x", s.caller, s.name)].append(s)
    pair = Counter()
    pair_kind: dict[str, Counter] = defaultdict(Counter)
    pair_dispatch: dict[str, Counter] = defaultdict(Counter)
    for (side, _, _), members in groups.items():
        c = Counter()
        hits = {t for m in members for t in m.hit}
        if side == "p":
            exp = members[0].expected
            wrong = hits - exp
            if hits & exp:
                c["TP"] += 1
            else:
                c["FN"] += 1
            c["FP"] += len(wrong)
        else:
            if hits:
                c["FP"] += len(hits)
            else:
                c["TN"] += 1
        pair.update(c)
        pair_kind[members[0].kind].update(c)
        pair_dispatch[members[0].dispatch].update(c)
    pair["unverifiable"] = len({(u["from"], u["to"]) for u in unverifiable})

    # --- node coverage ----------------------------------------------------------------
    called = {t for _, t, _ in uniq}
    meth = [n for n in nodes.values() if n.kind == "method"]
    expected_targets = {t for s in sites if s.bucket in ("TP", "FN", "wrong_target") for t in s.expected}

    worst = []
    for f, c in per_file.items():
        m = metrics(c)
        if c["TP"] + c["FN"] >= 5:
            worst.append({"file": f, **{k: m[k] for k in ("TP", "FP", "FN", "precision", "recall", "f1")}})
    worst.sort(key=lambda r: (r["f1"] if r["f1"] is not None else 0, -(r["FN"] + r["FP"])))

    return {
        "summary": metrics(pair),
        "by_kind": {k: metrics(v) for k, v in sorted(pair_kind.items())},
        "by_dispatch": {k: metrics(v) for k, v in sorted(pair_dispatch.items())},
        "per_call_site": {
            "summary": metrics(total),
            "by_kind": {k: metrics(v) for k, v in sorted(by_kind.items())},
            "by_dispatch": {k: metrics(v) for k, v in sorted(by_dispatch.items())},
        },
        "buckets": dict(buckets),
        "reference": {
            "call_sites": len(sites),
            "by_status": dict(site_counts),
            "out_of_scope": buckets["out_of_scope"],
            "unmapped_callee": buckets["unmapped_callee"],
            "files": len({s.file for s in sites}),
        },
        "eulix": {
            "nodes": len(nodes),
            "functions": sum(n.kind == "function" for n in nodes.values()),
            "methods": len(meth),
            "classes": sum(n.kind == "class" for n in nodes.values()),
            "call_edges_raw": raw,
            "call_edges_unique": len(uniq),
            "duplicate_edges": duplicates,
            "other_language_edges": other_lang,
            "colliding_ids": sum(1 for n in nodes.values() if n.more),
            "cross_language_edges": cross_lang,
            "fuzzy_line_matches": fuzzy,
            "extra_targets": extra_fp,
            "methods_with_caller": sum(n.id in called for n in meth),
            "expected_callees": len(expected_targets),
            "expected_callees_with_any_eulix_caller": len(expected_targets & called),
            "files": len({n.file for n in nodes.values()}),
        },
        "worst_files": worst[:10],
        "_unverifiable": unverifiable,
    }
