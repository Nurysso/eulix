# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Retrieval accuracy: does `query_without_cot` return the expected file, and how high?

Every question gets a fresh `eulix mcp` server, so session memory can't leak
between questions. Results are stored per question hash inside one document per
(version, corpus, engine), so adding a question runs only that question; the
aggregate numbers are recomputed from the per-question entries when comparing.
"""
import re
import statistics

from .. import corpora, mcp, runner, store, suite
from ..common import BenchError, say

METHOD_VERSION = 1


def fields(side, corpus, engine: str) -> dict:
    return {"group": "retrieval", "method_version": METHOD_VERSION, "sha": side.rev.sha, "device": side.device,
            "corpus_id": corpus.id, "engine": engine, "config_hash": corpora.config_hash(corpus),
            "env_hash": side.env_hash}


def rank_of(files: list[str], expect) -> int | None:
    return next((i for i, f in enumerate(files, 1) if f.startswith(tuple(expect))), None)


def ask(side, work, corpus, q: suite.Question, engine: str, timeout: float) -> dict:
    entry = {"id": q.id, "style": q.style, "component": q.component, "text": q.text, "expect": list(q.expect),
             "rank": None, "bypassed": False, "error": None, "chosen": None, "trace": None,
             "context_tokens": None, "top10": [], "latency_ms": None}
    log = side.log(f"retrieval-{corpus.name}-{engine}-{q.id}")
    with mcp.Server(side.sandbox, work, log) as s:
        s.initialize()
        try:
            text, dt, is_error = s.query(q.text, timeout)
        except TimeoutError:
            entry["error"] = f"timeout after {timeout:.0f}s"
            return entry
    stderr = log.read_text(errors="replace")
    entry["latency_ms"] = round(dt * 1000, 1)
    if is_error:
        entry["error"] = text.strip()[:300]
        return entry
    results = mcp.HEADER.findall(text)
    files = [f for f, _a, _b, _s in results]
    entry["top10"] = [f"{f}:{a}-{b}" for f, a, b, _s in results[:10]]
    entry["rank"] = rank_of(files, q.expect)
    entry["bypassed"] = not results
    trace = mcp.parse_trace(stderr)
    entry["trace"] = trace
    if trace:
        m = re.search(r"chosen=(\S+)", trace)
        entry["chosen"] = m[1] if m else None
    m = mcp.CONTEXT.search(text)
    entry["context_tokens"] = int(m[3]) if m else None
    debug = (work / ".eulix" / "debug" / "context_debug.log")
    if debug.exists():
        seen = mcp.ENGINE.findall(debug.read_text(errors="replace"))
        if seen and seen[-1].lower() != engine:
            raise BenchError(f"asked for engine {engine} but eulix ran {seen[-1]}; check the generated eulix.toml")
    return entry


def aggregate(entries: list[dict]) -> list[dict]:
    """Hit@1, recall@5/10, MRR and friends, overall and per style."""
    out = []
    scopes = [("", entries)] + [(f".{s}", [e for e in entries if e.get("style") == s])
                                for s in sorted({e.get("style") for e in entries if e.get("style")})]
    for suffix, es in scopes:
        n = len(es)
        if not n:
            continue
        ranks = [e["rank"] for e in es]

        def frac(k, _ranks=ranks, _n=n):
            hits = sum(1 for r in _ranks if r is not None and r <= k)
            return {"value": round(hits / _n, 3), "count": hits, "of": _n}

        for name, k in (("hit@1", 1), ("recall@5", 5), ("recall@10", 10), ("found", 10 ** 9)):
            m = store.metric(name + suffix, "", "higher", "accuracy")
            m.update(frac(k))
            out.append(m)
        m = store.metric("mrr" + suffix, "", "higher", "accuracy",
                         value=round(sum(1 / r for r in ranks if r) / n, 3))
        out.append(m)
        if not suffix:
            for name, pred in (("grep_fallback", lambda e: e.get("chosen") == "lexical"),
                               ("bypassed", lambda e: e.get("bypassed")),
                               ("errors", lambda e: e.get("error"))):
                c = sum(1 for e in es if pred(e))
                m = store.metric(name, "", "none" if name == "grep_fallback" else "lower", "count", value=c)
                m.update({"count": c, "of": n})
                out.append(m)
            toks = [e["context_tokens"] for e in es if e.get("context_tokens")]
            if toks:
                out.append(store.metric("context_tokens_p50", "tokens", "none", "count",
                                        value=statistics.median(toks)))
    return out


def current_entries(doc: dict, corpus_name: str, questions) -> list[dict]:
    """The stored entries for the questions in the current file (old questions drop out)."""
    qs = doc["details"]["questions"]
    return [qs[q.qhash(corpus_name)] for q in questions if q.qhash(corpus_name) in qs]


def run(side, corpus, questions, timeout: float, rerun: bool = False) -> list[dict]:
    graded = suite.graded(questions)
    docs = []
    work = None
    for engine in side.engines:
        f = fields(side, corpus, engine)
        doc = None if rerun else store.load(side, "retrieval", f)
        done = dict(doc["details"]["questions"]) if doc else {}
        todo = [q for q in graded if q.qhash(corpus.name) not in done]
        if not todo:
            say(f"  {corpus.label}: retrieval [{engine}] cached {len(graded)}/{len(graded)}")
            docs.append(doc)
            continue
        work = work or runner.ensure_fixture(side, corpus)
        corpora.write_config(work, engine)
        say(f"  {corpus.label}: retrieval [{engine}] running {len(todo)}, cached {len(graded) - len(todo)}")
        try:
            for i, q in enumerate(todo, 1):
                e = ask(side, work, corpus, q, engine, timeout)
                done[q.qhash(corpus.name)] = e
                rank = e["rank"] if e["rank"] else ("err" if e["error"] else "-")
                say(f"    {i:>3}/{len(todo)}  rank {rank!s:>4}  {q.id}")
        finally:
            corpora.write_config(work, None)
        doc = store.new_doc(side, "retrieval", METHOD_VERSION, f, corpus, engine)
        doc["details"] = {"questions": done}
        doc["metrics"] = aggregate([done[q.qhash(corpus.name)] for q in graded])
        store.save(side, doc)
        docs.append(doc)
    return docs
