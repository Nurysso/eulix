# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for bench/agent/agent_bench.py: task file, grading and transcript parsing."""
import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "agent"))

import agent_bench  # noqa: E402


class TaskFile(unittest.TestCase):
    def test_tasks_load_and_ids_unique(self):
        corpora, tasks = agent_bench.load_tasks()
        ids = [t["id"] for t in tasks]
        self.assertEqual(len(ids), len(set(ids)))
        for t in tasks:
            self.assertIn(t["corpus"], corpora)
            self.assertTrue(t["require"], t["id"])

    def test_ground_truth_passes_its_own_rubric(self):
        # The hand-checked `truth` must satisfy the rubric, or the rubric is wrong.
        _, tasks = agent_bench.load_tasks()
        for t in tasks:
            g = agent_bench.grade(t, t["truth"])
            self.assertTrue(g["correct"], f"{t['id']}: truth misses {g['missed']}")

    def test_empty_answer_fails(self):
        _, tasks = agent_bench.load_tasks()
        for t in tasks:
            self.assertFalse(agent_bench.grade(t, "")["correct"], t["id"])


class ParseStream(unittest.TestCase):
    def lines(self):
        return [json.dumps(e) for e in [
            {"type": "system", "subtype": "init", "claude_code_version": "9.9.9",
             "mcp_servers": [{"name": "eulix", "status": "connected"}]},
            {"type": "assistant", "message": {"content": [
                {"type": "tool_use", "id": "1", "name": "mcp__eulix__query", "input": {}},
                {"type": "tool_use", "id": "2", "name": "Read", "input": {}}]}},
            {"type": "result", "subtype": "success", "is_error": False, "result": "answer",
             "num_turns": 3, "duration_ms": 4500,
             "modelUsage": {"m": {"inputTokens": 10, "outputTokens": 20, "cacheReadInputTokens": 300,
                                  "cacheCreationInputTokens": 40, "costUSD": 0.5}}},
        ]] + ["not json"]

    def test_metrics(self):
        m = agent_bench.parse_stream(self.lines())
        self.assertTrue(m["ok"])
        self.assertEqual(m["input_tokens"], 350)
        self.assertEqual(m["output_tokens"], 20)
        self.assertEqual(m["tool_calls"], 2)
        self.assertEqual(m["eulix_calls"], 1)
        self.assertEqual(m["duration_s"], 4.5)
        self.assertEqual(m["cost_usd"], 0.5)
        self.assertEqual(m["mcp_status"], ["connected"])

    def test_missing_result(self):
        self.assertIn("error", agent_bench.parse_stream(self.lines()[:2]))


class ClaudeCommand(unittest.TestCase):
    def test_arms_differ_only_in_eulix(self):
        cfg = Path("/x/mcp.json")
        plain = agent_bench.claude_cmd("plain", "flask", "q", "sonnet", cfg, 1.0)
        eulix = agent_bench.claude_cmd("eulix", "flask", "q", "sonnet", cfg, 1.0)
        hint = agent_bench.claude_cmd("eulix-hint", "flask", "q", "sonnet", cfg, 1.0)
        self.assertNotIn("--mcp-config", plain)
        self.assertIn("--mcp-config", eulix)
        self.assertNotIn("--append-system-prompt", eulix)
        self.assertIn(agent_bench.HINT, hint)
        for cmd in (plain, eulix, hint):
            self.assertIn("--strict-mcp-config", cmd)
            self.assertEqual(cmd[cmd.index("--setting-sources") + 1], "")

    def test_chained_turns(self):
        cfg = Path("/x/mcp.json")
        first = agent_bench.claude_cmd("plain", "flask", "q", "sonnet", cfg, 1.0, session="s1")
        later = agent_bench.claude_cmd("plain", "flask", "q", "sonnet", cfg, 1.0, session="s1", resume=True)
        self.assertEqual(first[first.index("--session-id") + 1], "s1")
        self.assertEqual(later[later.index("--resume") + 1], "s1")
        for cmd in (first, later):
            self.assertNotIn("--no-session-persistence", cmd)
        self.assertIn("--no-session-persistence", agent_bench.claude_cmd("plain", "flask", "q", "s", cfg, 1.0))


class Chains(unittest.TestCase):
    def test_one_session_per_corpus_arm_rep_in_task_order(self):
        _, tasks = agent_bench.load_tasks()
        cs = agent_bench.chains(tasks, ["plain", "eulix-hint"], 1)
        corpora = list(dict.fromkeys(t["corpus"] for t in tasks))
        self.assertEqual(len(cs), len(corpora) * 2)
        for corpus, _, _, ts in cs:
            self.assertEqual([t["id"] for t in ts], [t["id"] for t in tasks if t["corpus"] == corpus])

    def test_run_chain_resumes_and_skips_finished_turns(self):
        tasks = [{"id": f"t{i}", "corpus": "c", "kind": "k", "prompt": "p", "require": [["x"]]} for i in range(3)]
        calls = []

        def fake(task, arm, rep, args, run_dir, tdir, session=None, resume=False):
            calls.append((task["id"], session, resume))
            return {"task": task["id"], "ok": True, "session": session}

        orig, agent_bench.run_one = agent_bench.run_one, fake
        try:
            got = []
            prior = [{"task": "t0", "ok": True, "session": "old"}]
            agent_bench.run_chain("c", "plain", 1, tasks, None, None, None, prior, got.append,
                                  agent_bench.threading.Event())
        finally:
            agent_bench.run_one = orig
        self.assertEqual(calls, [("t1", "old", True), ("t2", "old", True)])
        self.assertEqual([r["turn"] for r in got], [2, 3])


if __name__ == "__main__":
    unittest.main()
