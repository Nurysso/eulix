# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Query speed: how fast `eulix mcp` answers cold and warm, and what it holds in memory.

cold  3 fresh servers, each timed from spawn to `initialize` done and to the
      first answer (the first query loads the index lazily).
warm  one server: a warm-up question, then every question of the corpus three
      times in order. The p50/p95 of each round are the samples.
"""
import re
import statistics
import time

from .. import corpora, mcp, proc, runner, store, suite
from ..common import BenchError, say

METHOD_VERSION = 1
COLD_RUNS = 3
ROUNDS = 3
GPU_PROVIDERS = ("ROCMExecutionProvider", "CUDAExecutionProvider", "MIGraphXExecutionProvider")


def pct(xs, q):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(q * (len(xs) - 1) + 0.5))] if xs else None


def fields(side, corpus, engine, questions) -> dict:
    return {"group": "query", "method_version": METHOD_VERSION, "sha": side.rev.sha, "device": side.device,
            "corpus_id": corpus.id, "engine": engine, "config_hash": corpora.config_hash(corpus),
            "env_hash": side.env_hash, "machine_fp": side.machine_fp,
            "latency_set": suite.latency_set_hash(corpus.name, questions)}


def _check_providers(stderr: str, device: str) -> list[str]:
    providers = sorted(set(re.findall(r"Provider:\s+(\w+)", stderr)))
    if device == "cpu" and (any(p in GPU_PROVIDERS for p in providers) or "GPU detected" in stderr):
        raise BenchError(f"the query-time embedder used {providers}; CPU runs must not reach the GPU")
    return providers


def _measure(side, corpus, work, engine, questions, timeout) -> dict:
    first = questions[0].text
    cold_init, cold_answer, providers = [], [], set()
    for i in range(COLD_RUNS):
        log = side.log(f"query-{corpus.name}-{engine}-cold{i + 1}")
        with mcp.Server(side.sandbox, work, log) as s:
            s.initialize()
            init_s = time.monotonic() - s.t0
            try:
                s.query(first, timeout)
            except TimeoutError:
                raise BenchError(f"cold query on {corpus.label} [{engine}] timed out; see {log}") from None
            answer_s = time.monotonic() - s.t0
        cold_init.append(round(init_s, 3))
        cold_answer.append(round(answer_s, 3))
        providers.update(_check_providers(log.read_text(errors="replace"), side.device))

    log = side.log(f"query-{corpus.name}-{engine}-warm")
    rounds, errors = [], 0
    with mcp.Server(side.sandbox, work, log) as s:
        sampler = proc.Sampler(s.p.pid)
        sampler.start()
        try:
            s.initialize()
            s.query(first, timeout)  # warm-up: loads the index
            for _ in range(ROUNDS):
                lat = []
                for q in questions:
                    try:
                        _text, dt, is_error = s.query(q.text, timeout)
                    except TimeoutError:
                        raise BenchError(f"warm query on {corpus.label} [{engine}] timed out; see {log}") from None
                    errors += is_error
                    lat.append(dt * 1000)
                rounds.append(lat)
        finally:
            sampler.stop()
    providers.update(_check_providers(log.read_text(errors="replace"), side.device))
    allms = [x for r in rounds for x in r]
    go_rss = max((v for k, v in sampler.by_comm.items() if k.startswith("eulix")), default=None)
    return {
        "cold_init_s": cold_init, "cold_first_answer_s": cold_answer,
        "warm_p50_ms": [round(statistics.median(r), 1) for r in rounds],
        "warm_p95_ms": [round(pct(r, 0.95), 1) for r in rounds],
        "warm_max_ms": round(max(allms), 1),
        "server_rss_mb": go_rss, "server_tree_rss_mb": round(sampler.peak, 1),
        "errors": errors, "questions": len(questions), "providers": sorted(providers),
    }


def run(side, corpus, questions, timeout: float, rerun: bool = False) -> list[dict]:
    if not questions:
        say(f"  {corpus.label}: no questions, skipping query speed")
        return []
    docs, work = [], None
    for engine in side.engines:
        f = fields(side, corpus, engine, questions)
        doc = None if rerun else store.load(side, "query", f)
        if doc:
            say(f"  {corpus.label}: query [{engine}] cached")
            docs.append(doc)
            continue
        work = work or runner.ensure_fixture(side, corpus)
        corpora.write_config(work, engine)
        say(f"  {corpus.label}: query [{engine}] {COLD_RUNS} cold starts, {ROUNDS} warm rounds × {len(questions)}")
        try:
            r = _measure(side, corpus, work, engine, questions, timeout)
        finally:
            corpora.write_config(work, None)
        doc = store.new_doc(side, "query", METHOD_VERSION, f, corpus, engine)
        doc["metrics"] = [
            store.metric("cold_init_s", "s", "lower", "time", samples=r["cold_init_s"]),
            store.metric("cold_first_answer_s", "s", "lower", "time", samples=r["cold_first_answer_s"]),
            store.metric("warm_p50_ms", "ms", "lower", "latency", samples=r["warm_p50_ms"]),
            store.metric("warm_p95_ms", "ms", "lower", "latency", samples=r["warm_p95_ms"]),
            store.metric("warm_max_ms", "ms", "none", "latency", value=r["warm_max_ms"]),
            store.metric("server_rss_mb", "MB", "lower", "memory", value=r["server_rss_mb"]),
            store.metric("server_tree_rss_mb", "MB", "lower", "memory", value=r["server_tree_rss_mb"]),
        ]
        doc["details"] = {k: r[k] for k in ("errors", "questions", "providers")}
        store.save(side, doc)
        docs.append(doc)
    return docs
