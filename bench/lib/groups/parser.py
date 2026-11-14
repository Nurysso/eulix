# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Parser quality: what the parser linked, what failed, and whether a second run agrees.

Counts come from the fixture's kb_call_graph.json, the index `eulix analyze`
built. Determinism runs the parser twice more the same way (analyze's flags, cold,
into separate directories) and compares the two: `byte_identical` over every
output file (minus run-specific metadata), and `semantic_identical` over the call
graph's nodes and edges in any order, since v0.8.3 writes the call graph's tags
from a HashMap and its bytes vary run to run. Comparing against the fixture
instead would mix in differences between how analyze and a direct call pass --root.
"""
import shutil

from .. import corpora, kbstats, proc, runner, sandbox, store
from ..common import H, BenchError, say
from . import indexing

METHOD_VERSION = 3  # 2: kb_metrics.json without parsed_at; 3: determinism from two direct parses


def fields(side, corpus) -> dict:
    # No device: parsing doesn't embed, so GPU and CPU runs share one result.
    return {"group": "parser", "method_version": METHOD_VERSION, "sha": side.rev.sha,
            "corpus_id": corpus.id, "parser_cfg": H({"threads": 8, "prism": 2, "euignore": corpus.euignore()})[:16]}


def run(side, corpus, rerun: bool = False) -> dict:
    f = fields(side, corpus)
    doc = None if rerun else store.load(side, "parser", f)
    if doc:
        say(f"  {corpus.label}: parser cached")
        return doc
    work = runner.ensure_fixture(side, corpus)
    eulix_dir = work / ".eulix"
    say(f"  {corpus.label}: parser stats")
    stats = kbstats.callgraph_stats(eulix_dir / "kb_call_graph.json")
    counts = runner.fixture_counts(work)
    details = {"stats": stats, "counts": counts}
    deterministic = {}
    if corpus.tier != "large":
        runs = [work.parent / f"{corpus.name}.parse{i}" for i in (1, 2)]
        try:
            for i, out in enumerate(runs, 1):
                shutil.rmtree(out, ignore_errors=True)
                out.mkdir(parents=True)
                log = side.log(f"parser-{corpus.name}-run{i}")
                with sandbox.InstallGuard():
                    m = proc.run_measured(indexing.parser_cmd(side, work, out / "kb.json"), cwd=work,
                                          env=side.sandbox.env, log_path=log)
                if m.returncode != 0:
                    raise BenchError(f"parse {i} of {corpus.label} failed; see {log}")
            h1, h2 = (kbstats.output_hashes(d) for d in runs)
            sem1, sem2 = (kbstats.callgraph_stats(d / "kb_call_graph.json")["semantic_hash"] for d in runs)
        finally:
            for d in runs:
                shutil.rmtree(d, ignore_errors=True)
        per_file = {name: h1.get(name) == h2.get(name) for name in sorted(set(h1) | set(h2))}
        deterministic = {"byte_identical": all(per_file.values()), "semantic_identical": sem1 == sem2}
        details["determinism"] = {"per_file": per_file, **deterministic}

    methods = sum(v["methods"] for v in stats["methods_with_caller"].values())
    with_caller = sum(v["with_caller"] for v in stats["methods_with_caller"].values())
    metrics = [
        store.metric("files_clean", "", "none", "count", value=counts.get("files_clean")),
        store.metric("files_partial", "", "lower", "count", value=counts.get("files_partial")),
        store.metric("files_failed", "", "lower", "count", value=counts.get("files_failed")),
        store.metric("nodes", "", "none", "count", value=stats["nodes"]),
        store.metric("edges", "", "none", "count", value=stats["edges"]),
        store.metric("call_edges", "", "higher", "count", value=stats["call_edges"]),
        store.metric("methods_with_caller", "", "higher", "ratio",
                     value=round(with_caller / methods, 3) if methods else None),
        store.metric("cross_lang_edges", "", "none", "count", value=sum(stats["cross_lang_edges"].values())),
    ]
    metrics[-2].update({"count": with_caller, "of": methods})
    for k, v in deterministic.items():
        metrics.append(store.metric(k, "", "higher", "bool", value=v))
    doc = store.new_doc(side, "parser", METHOD_VERSION, f, corpus, None)
    doc["metrics"] = metrics
    doc["details"] = details
    store.save(side, doc)
    return doc
