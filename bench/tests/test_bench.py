# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for the benchmark harness: parsers, keys, flag rules. No eulix needed.

    python3 -m unittest discover -s bench/tests     (or: make bench-test)
"""
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from lib import compare, corpora, kbstats, mcp, store, suite  # noqa: E402
from lib.common import BenchError  # noqa: E402
from lib.groups import indexing, retrieval  # noqa: E402

# Trimmed from a real `eulix analyze` log (v0.8.3, parser --verbose, ONNX on CPU).
ANALYZE_LOG = """\
EXECUTION TIME
   Total:                  0.22s
   Peak Rss:               109.0 MB
   Clean:             281 files (100.00%)
   Partial:             2 files (0.70%)  syntax errors; extracted best-effort, kept in KB
   Failed:              1 files (0.35%)  no usable tree; NOT in KB
ℹ No GPU execution provider available — using CPUExecutionProvider
     Provider:   CPUExecutionProvider
       Total Chunks: 3044
     Average: 27.2 chunks/sec
  Total Chunks:   3044
  Total Time:     111.97s
Took 1m53s
"""


class OutputParsing(unittest.TestCase):
    def test_analyze_log(self):
        p = indexing.parse_output(ANALYZE_LOG)
        self.assertEqual(p["parse_s"], 0.22)
        self.assertEqual(p["parser_self_peak_mb"], 109.0)
        self.assertEqual((p["files_clean"], p["files_partial"], p["files_failed"]), (281, 2, 1))
        self.assertEqual(p["embed_s"], 111.97)
        self.assertEqual(p["chunks"], 3044)
        self.assertEqual(p["chunks_per_s"], 27.2)
        self.assertEqual(p["providers"], ["CPUExecutionProvider"])

    def test_missing_lines_are_none(self):
        p = indexing.parse_output("nothing useful")
        self.assertIsNone(p["parse_s"])
        self.assertIsNone(p["embed_s"])

    def test_non_verbose_summary_line(self):
        p = indexing.parse_output("✓ Parsed 12 files (10 clean, 1 partial, 1 failed; 900 LOC) in 0.31s → kb.json")
        self.assertEqual((p["files_clean"], p["files_failed"], p["parse_s"]), (10, 1, 0.31))

    def test_cpu_run_passes_device_check(self):
        indexing.check_device(ANALYZE_LOG, "cpu", indexing.parse_output(ANALYZE_LOG))

    def test_gpu_line_aborts_cpu_run(self):
        text = "  ✓ AMD GPU detected — using ROCMExecutionProvider (ROCm/HIP)\n     Provider:   ROCMExecutionProvider\n"
        with self.assertRaises(BenchError):
            indexing.check_device(text, "cpu", indexing.parse_output(text))

    def test_cpu_line_fails_gpu_run(self):
        with self.assertRaises(BenchError):
            indexing.check_device(ANALYZE_LOG, "gpu", indexing.parse_output(ANALYZE_LOG))

    def test_mcp_headers_and_trace(self):
        text = "Context: 3 source chunks, 1 metadata, 812 tokens across 2 files\n" \
               "[1] a/b.go:10-20 (score 0.90)\n[2] c.rs:1-5 (score 0.50)\n### d.py:3-4\n"
        files = [f for f, *_ in mcp.HEADER.findall(text)]
        self.assertEqual(files, ["a/b.go", "c.rs", "d.py"])
        self.assertEqual(mcp.CONTEXT.search(text)[3], "812")
        stderr = 'level=INFO msg="query served" retrieval="anchors=[\\"x\\"] path=[open-ended(0.62)] chosen=lexical"'
        self.assertEqual(mcp.parse_trace(stderr), 'anchors=["x"] path=[open-ended(0.62)] chosen=lexical')


class KbStats(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())

    def _cg(self, nodes, edges, tags=None):
        p = self.tmp / "kb_call_graph.json"
        p.write_text(json.dumps({"nodes": nodes, "edges": edges, "tags": tags or {}, "file_categories": {}}))
        return p

    def test_stream_and_counts(self):
        nodes = [{"id": "m1", "node_type": "method", "file": "a.go"},
                 {"id": "m2", "node_type": "method", "file": "a.go"},
                 {"id": "f1", "node_type": "function", "file": "b.py"}]
        edges = [{"from": "f1", "to": "m1", "edge_type": "call"},
                 {"from": "m1", "to": "m2", "edge_type": "calls_into"}]
        old = kbstats.CHUNK
        kbstats.CHUNK = 7  # force many refills mid-object
        try:
            s = kbstats.callgraph_stats(self._cg(nodes, edges))
        finally:
            kbstats.CHUNK = old
        self.assertEqual(s["nodes"], 3)
        self.assertEqual(s["call_edges"], 1)
        self.assertEqual(s["methods_with_caller"], {".go": {"with_caller": 1, "methods": 2}})
        self.assertEqual(s["cross_lang_edges"], {"py->go": 1})

    def test_semantic_hash_ignores_order(self):
        n = [{"id": "a", "node_type": "function", "file": "x.rs"}, {"id": "b", "node_type": "function", "file": "x.rs"}]
        h1 = kbstats.callgraph_stats(self._cg(n, [], {"t": 1, "u": 2}))["semantic_hash"]
        h2 = kbstats.callgraph_stats(self._cg(list(reversed(n)), [], {"u": 2, "t": 1}))["semantic_hash"]
        self.assertEqual(h1, h2)

    def test_kb_hash_ignores_run_metadata(self):
        a = self.tmp / "a.json"
        b = self.tmp / "b.json"
        a.write_text('{"metadata":{"version":"1","parsed_at":"t1","git_hash":"x"},"structure":{"f":1}}')
        b.write_text('{"metadata":{"version":"1","parsed_at":"t2","git_hash":"y"},"structure":{"f":1}}')
        self.assertEqual(kbstats.kb_hash(a), kbstats.kb_hash(b))
        b.write_text('{"metadata":{"version":"1","parsed_at":"t2"},"structure":{"f":2}}')
        self.assertNotEqual(kbstats.kb_hash(a), kbstats.kb_hash(b))

    def test_metadata_files_detected(self):
        a = self.tmp / "m.json"
        a.write_text('{\n  "metadata": {"parsed_at": "x"}, "top": []}')
        self.assertTrue(kbstats.starts_with_metadata(a))
        a.write_text('{"nodes": [], "metadata": 1}')
        self.assertFalse(kbstats.starts_with_metadata(a))


class ConfigAndKeys(unittest.TestCase):
    def test_etiya_omits_engine_line(self):
        import tomllib
        self.assertNotIn("engine", tomllib.loads(corpora.render_config("etiya"))["project"])
        self.assertNotIn("engine", tomllib.loads(corpora.render_config(None))["project"])
        self.assertEqual(tomllib.loads(corpora.render_config("ehnas"))["project"]["engine"], "ehnas")

    def test_keys_change_with_fields(self):
        self.assertEqual(store.key({"a": 1, "b": 2}), store.key({"b": 2, "a": 1}))
        self.assertNotEqual(store.key({"a": 1}), store.key({"a": 2}))

    def test_question_hash_ignores_id(self):
        q1 = suite.Question("x", "How does it work", ("a.go",), "code")
        q2 = suite.Question("y", "How does it work", ("a.go",), "plain")
        self.assertEqual(q1.qhash("eulix"), q2.qhash("eulix"))
        self.assertNotEqual(q1.qhash("eulix"), q1.qhash("other"))

    def test_shipped_suite_loads(self):
        cs = corpora.load_all()
        self.assertIn("eulix", [c.name for c in cs])
        for c in cs:
            suite.load(c.questions)

    def test_bad_commit_rejected(self):
        p = Path(tempfile.mkdtemp()) / "c.toml"
        p.write_text('schema = 1\n[[corpus]]\nname="x"\nsource="self"\ncommit="abc"\nmeasures=["indexing"]\n')
        with self.assertRaises(BenchError):
            corpora.load_all(p)


def m(name, samples=None, value=None, better="lower", kind="time", unit="s"):
    return store.metric(name, unit, better, kind, samples=samples, value=value)


class FlagRules(unittest.TestCase):
    def flag(self, ma, mb):
        rows, flags = compare.metric_rows([ma], [mb])
        return rows[0].flag

    def test_overlapping_ranges_not_flagged(self):
        self.assertEqual(self.flag(m("t", [10, 11, 12]), m("t", [11.6, 11.1, 13])), "")

    def test_clear_slowdown_flagged(self):
        self.assertTrue(self.flag(m("t", [10, 10.2, 10.1]), m("t", [11.3, 11.2, 11.4])))

    def test_small_slowdown_not_flagged(self):
        self.assertEqual(self.flag(m("t", [10, 10, 10]), m("t", [10.8, 10.8, 10.8])), "")

    def test_floor_ignores_tiny_absolute_change(self):
        self.assertEqual(self.flag(m("t", [0.01, 0.01]), m("t", [0.03, 0.03])), "")

    def test_higher_is_better_mirrored(self):
        a = m("rate", [30, 31, 30], better="higher", kind="ratio", unit="chunks/s")
        b = m("rate", [20, 21, 22], better="higher", kind="ratio", unit="chunks/s")
        self.assertTrue(self.flag(a, b))

    def test_parser_rules(self):
        self.assertTrue(self.flag(m("files_failed", value=0, kind="count"), m("files_failed", value=2, kind="count")))
        self.assertTrue(self.flag(m("call_edges", value=1000, better="higher", kind="count"),
                                  m("call_edges", value=900, better="higher", kind="count")))
        self.assertEqual(self.flag(m("call_edges", value=1000, better="higher", kind="count"),
                                   m("call_edges", value=980, better="higher", kind="count")), "")
        self.assertTrue(self.flag(m("byte_identical", value=True, better="higher", kind="bool"),
                                  m("byte_identical", value=False, better="higher", kind="bool")))

    def test_rank_changes(self):
        self.assertTrue(compare._worse({"rank": 1}, {"rank": 2}))
        self.assertTrue(compare._worse({"rank": 3}, {"rank": None}))
        self.assertTrue(compare._worse({"rank": None}, {"rank": None, "error": "timeout"}))
        self.assertEqual(compare._worse({"rank": 2}, {"rank": 1}), "")
        self.assertEqual(compare._worse({"rank": None}, {"rank": None}), "")

    def test_aggregate(self):
        es = [{"rank": 1, "style": "code"}, {"rank": 4, "style": "code"}, {"rank": None, "style": "plain"}]
        got = {x["name"]: x for x in retrieval.aggregate(es)}
        self.assertEqual((got["hit@1"]["count"], got["hit@1"]["of"]), (1, 3))
        self.assertEqual(got["recall@5"]["count"], 2)
        self.assertEqual(got["mrr"]["value"], round((1 + 0.25) / 3, 3))
        self.assertEqual(got["hit@1.plain"]["count"], 0)

    def test_missing_engine_is_na_not_flagged(self):
        tmp = tempfile.mkdtemp()
        os.environ["EULIX_BENCH_HOME"] = tmp
        try:
            def side(label, engines):
                s = SimpleNamespace(rev=SimpleNamespace(label=label, sha12=label, worktree=False, sha=label * 3),
                                    engines=engines, device="cpu", env_hash="e", machine_fp="m")
                s.accuracy_side = lambda: s
                return s
            c = corpora.Corpus(name="eulix", source="self", commit="0" * 40, measures=("retrieval",),
                               questions="questions/eulix.toml")
            cells = compare.build_cells(side("old", ["etiya"]), side("new", ["etiya", "ehnas"]), [c], ["retrieval"])
            ehnas = [x for x in cells if x.engine == "ehnas"][0]
            self.assertEqual(ehnas.status, "n/a")
            self.assertEqual(ehnas.flags, [])
            self.assertEqual(compare.verdict([ehnas]), 0)
        finally:
            del os.environ["EULIX_BENCH_HOME"]


if __name__ == "__main__":
    unittest.main()
