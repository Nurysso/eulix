#!/usr/bin/env python3
# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Agent token-savings benchmark: the same tasks, run by Claude Code with and without Eulix.

    python3 bench/agent/agent_bench.py prepare                 # fetch corpora, build the indexes
    python3 bench/agent/agent_bench.py run --reps 3 --jobs 3   # run every task x arm x rep
    python3 bench/agent/agent_bench.py report <run-dir>        # rebuild the Markdown report

Each run is one headless `claude -p` session in a fresh copy of the repo:

    plain       Bash, Read, Grep, Glob. The repo copy has no eulix.toml and no .eulix/.
    eulix       The same four tools plus the Eulix MCP server (`eulix mcp --repo <copy>`, EHNAS
                engine), exactly as `claude mcp add` installs it. Nothing tells the agent to use it.
    eulix-hint  As eulix, plus one line in the system prompt (HINT below) telling the agent to
                query Eulix first, as a CLAUDE.md line would.

Both arms load no user/project settings, CLAUDE.md, skills or MCP servers of the
machine (`--setting-sources ""`, `--strict-mcp-config`, `--disable-slash-commands`),
so the only difference between them is the Eulix server. Token counts and cost
come from Claude Code's own `result` event (list prices), tool calls are counted
from the stream, and the answer is graded against tasks.toml.
"""
import argparse
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
import threading
import time
import tomllib
import uuid
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))
from lib import paths  # noqa: E402
from lib.common import REPO, BenchError, say, write_json  # noqa: E402
from lib.corpora import EUIGNORE_DEFAULT, Corpus, ensure_snapshot, remove_tree, render_config  # noqa: E402

TASKS = HERE / "tasks.toml"
RESULTS = REPO / "bench" / "results" / "agent"
ARMS = ("plain", "eulix", "eulix-hint")
# The eulix-hint arm adds this line to the system prompt: the same effect as putting it in CLAUDE.md.
# It is the line `eulix mcp install` writes (agentHint in eulix-cli/internal/cli/mcp_install.go).
# Runs before 2026-11-20 used an older line naming the `query` tool; each run's meta.json records its hint.
HINT = ("This repository is indexed by Eulix (MCP server `eulix`). For who calls what, use its "
        "`callers` / `callees` tools; for where a symbol is defined, `definition`; for how one function reaches "
        "another across files, `trace`; for how a feature works, `search` (pass exact symbol names in "
        "known_symbols). Each reply starts with a `complete: yes|no` line: when it says yes, do not re-check "
        "with grep. Use grep for literal strings, config values and log messages.")
BASE_TOOLS = "Bash,Read,Grep,Glob"
PROMPT_SUFFIX = ("\n\nAnswer from the code in the current directory. Name the files and functions "
                 "involved. Do not modify any files.")


def state(*parts) -> Path:
    return paths.sub("agent", *parts)


def load_tasks(path: Path = TASKS):
    data = tomllib.loads(path.read_text())
    if data.get("schema") != 1:
        raise BenchError(f"{path}: unsupported schema")
    corpora = {c["name"]: Corpus(name=c["name"], source=c["source"], commit=c["commit"], measures=("query",),
                                 tag=c.get("tag", ""),
                                 exclude=("eulix.toml", ".euignore", ".eulix/", *c.get("exclude", ())))
               for c in data["corpus"]}
    tasks = data["task"]
    for t in tasks:
        if t["corpus"] not in corpora:
            raise BenchError(f"task {t['id']}: unknown corpus {t['corpus']}")
    return corpora, tasks


# ------------------------------------------------------------------ prepare

# Set by --eulix-max-tokens: the eulix arms then run on a copy of the indexed repo whose
# eulix.toml has that [llm] max_tokens. Eulix's reply budget is max_tokens - 2048 (kept
# for the answer) - ~140, floored at 512 (allocateBudget in eulix-cli retrieval/intent.go).
EULIX_MAX_TOKENS: int | None = None


def workdir(corpus: str, arm: str) -> Path:
    """Both eulix arms share the indexed copy: the MCP server only reads it."""
    if arm == "plain":
        return state("work", f"{corpus}-plain")
    return state("work", f"{corpus}-eulix" + (f"-t{EULIX_MAX_TOKENS}" if EULIX_MAX_TOKENS else ""))


def ensure_budget_variant(corpus: str) -> Path:
    """Copy the indexed repo once and set its reply budget; the index itself is reused."""
    src, dst = state("work", f"{corpus}-eulix"), workdir(corpus, "eulix")
    if not (dst / ".agent-bench").exists():
        remove_tree(dst)
        subprocess.run(["cp", "-r", str(src), str(dst)], check=True)
    toml = dst / "eulix.toml"
    text, n = re.subn(r"(?m)^max_tokens\s*=.*$", f"max_tokens = {EULIX_MAX_TOKENS}", toml.read_text())
    if n != 1:
        raise BenchError(f"{toml}: expected one max_tokens line, found {n}")
    toml.write_text(text)
    return dst


def prepare(corpora: dict, rebuild: bool = False) -> dict:
    """Two writable copies per corpus. Only the eulix copy gets a config and an index."""
    info = {}
    for name, c in corpora.items():
        snap = ensure_snapshot(c)
        for arm in ("plain", "eulix"):
            w = workdir(name, arm)
            if rebuild or not (w / ".agent-bench").exists():
                remove_tree(w)
                w.parent.mkdir(parents=True, exist_ok=True)
                subprocess.run(["cp", "-r", str(snap), str(w)], check=True)
                subprocess.run(["chmod", "-R", "u+w", str(w)], check=True)
                if arm == "eulix":
                    (w / "eulix.toml").write_text(render_config("ehnas"))
                    (w / ".euignore").write_text("".join(p + "\n" for p in EUIGNORE_DEFAULT))
                    (w / ".eulix").mkdir()
                    t0 = time.monotonic()
                    p = subprocess.run(["eulix", "analyze"], cwd=w, stdin=subprocess.DEVNULL,
                                       capture_output=True, text=True)
                    if p.returncode != 0:
                        raise BenchError(f"eulix analyze failed in {w}:\n{p.stdout[-2000:]}{p.stderr[-2000:]}")
                    (w / ".agent-index-seconds").write_text(f"{time.monotonic() - t0:.2f}\n")
                (w / ".agent-bench").write_text(c.commit + "\n")
            say(f"ready: {w}")
        idx = workdir(name, "eulix") / ".agent-index-seconds"
        info[name] = {"commit": c.commit, "tag": c.tag,
                      "index_seconds": float(idx.read_text()) if idx.exists() else None}
    return info


# ------------------------------------------------------------------ run

def claude_cmd(arm: str, corpus: str, prompt: str, model: str, mcp_cfg: Path, budget: float,
               effort: str | None = None, session: str | None = None, resume: bool = False) -> list[str]:
    """One `claude -p` call. With `session`, the call is a turn of a chained session: the
    first turn creates it (--session-id), later turns continue it (--resume)."""
    cmd = ["claude", "-p", prompt + PROMPT_SUFFIX,
           "--model", model,
           "--output-format", "stream-json", "--verbose"]
    if session:
        cmd += ["--resume" if resume else "--session-id", session]
    else:
        cmd += ["--no-session-persistence"]
    cmd += ["--setting-sources", "",
           "--disable-slash-commands",
           "--strict-mcp-config",
           "--permission-mode", "dontAsk",
           "--tools", BASE_TOOLS,
           "--max-budget-usd", str(budget)]
    if effort:
        cmd += ["--effort", effort]
    allowed = BASE_TOOLS
    if arm != "plain":
        cmd += ["--mcp-config", str(mcp_cfg)]
        allowed += ",mcp__eulix"
    if arm == "eulix-hint":
        cmd += ["--append-system-prompt", HINT]
    cmd += ["--allowedTools", allowed]
    return cmd


def parse_stream(lines: list[str]) -> dict:
    """Pull the metrics out of a stream-json transcript."""
    tools, eulix_calls, result, init = [], 0, None, None
    for line in lines:
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        if ev.get("type") == "system" and ev.get("subtype") == "init":
            init = ev
        elif ev.get("type") == "assistant":
            for block in ev.get("message", {}).get("content", []):
                if block.get("type") == "tool_use":
                    tools.append(block.get("name"))
                    if block.get("name", "").startswith("mcp__eulix__"):
                        eulix_calls += 1
        elif ev.get("type") == "result":
            result = ev
    if result is None:
        return {"error": "no result event"}
    inp = out = cache_read = cache_write = 0
    cost = 0.0
    for m in (result.get("modelUsage") or {}).values():
        inp += m.get("inputTokens", 0)
        out += m.get("outputTokens", 0)
        cache_read += m.get("cacheReadInputTokens", 0)
        cache_write += m.get("cacheCreationInputTokens", 0)
        cost += m.get("costUSD", 0.0)
    return {
        "ok": not result.get("is_error") and result.get("subtype") == "success",
        "subtype": result.get("subtype"),
        "answer": result.get("result") or "",
        "input_tokens": inp + cache_read + cache_write,  # everything the model read, cached or not
        "input_uncached": inp,
        "cache_read": cache_read,
        "cache_write": cache_write,
        "output_tokens": out,
        "cost_usd": round(cost or result.get("total_cost_usd", 0.0), 6),
        "turns": result.get("num_turns"),
        "tool_calls": len(tools),
        "eulix_calls": eulix_calls,
        "tools": tools,
        "duration_s": round(result.get("duration_ms", 0) / 1000, 2),
        "models": sorted((result.get("modelUsage") or {}).keys()),
        "mcp_status": [s.get("status") for s in (init or {}).get("mcp_servers", [])],
        "claude_version": (init or {}).get("claude_code_version"),
    }


def usage_limited(rec: dict) -> bool:
    """Claude Code reports an exhausted plan as an error result whose text names the limit."""
    return not rec.get("ok") and bool(re.search(r"hit your .*limit", rec.get("answer") or "", re.I))


def grade(task: dict, answer: str) -> dict:
    missed = [g for g in task["require"] if not any(re.search(p, answer, re.I) for p in g)]
    return {"correct": not missed, "missed": missed}


def run_one(task: dict, arm: str, rep: int, args, run_dir: Path, tdir: Path,
            session: str | None = None, resume: bool = False) -> dict:
    w = workdir(task["corpus"], arm)
    mcp_cfg = tdir / f"mcp-{task['corpus']}.json"
    cmd = claude_cmd(arm, task["corpus"], task["prompt"], args.model, mcp_cfg, args.budget, args.effort,
                     session, resume)
    t0 = time.monotonic()
    try:
        p = subprocess.run(cmd, cwd=w, stdin=subprocess.DEVNULL, capture_output=True, text=True,
                           timeout=args.timeout)
        lines, err = p.stdout.splitlines(), p.stderr
    except subprocess.TimeoutExpired as e:
        lines, err = (e.stdout or b"").decode(errors="replace").splitlines() if isinstance(e.stdout, bytes) \
            else (e.stdout or "").splitlines(), "timeout"
    wall = round(time.monotonic() - t0, 2)
    (tdir / f"{task['id']}.{arm}.{rep}.jsonl").write_text("\n".join(lines) + "\n")
    m = parse_stream(lines)
    rec = {"task": task["id"], "corpus": task["corpus"], "kind": task["kind"], "arm": arm, "rep": rep,
           "model": args.model, "wall_s": wall, **m}
    if session:
        rec["session"] = session
    if "error" in m:
        rec.update(ok=False, correct=False, missed=None, stderr=err[-1000:])
    else:
        rec.update(grade(task, m["answer"]))
    return rec


def chains(tasks: list, arms: list, reps: int) -> list:
    """Chain mode: one session per (corpus, arm, rep), each task one turn, in tasks.toml order
    so every arm gets the same order."""
    out = []
    for r in range(1, reps + 1):
        for corpus in dict.fromkeys(t["corpus"] for t in tasks):
            for a in arms:
                out.append((corpus, a, r, [t for t in tasks if t["corpus"] == corpus]))
    return out


def run_chain(corpus: str, arm: str, rep: int, tasks: list, args, run_dir: Path, tdir: Path,
              prior: list, emit, stop: threading.Event) -> None:
    """Run one chained session turn by turn, skipping turns an earlier try finished. Each
    `claude -p --resume` call reports its own usage, so every turn is costed on its own,
    with the conversation so far as cached context. Each call also starts a fresh MCP server,
    so Eulix's within-session dedup does not carry across turns."""
    done = {r["task"] for r in prior if r.get("ok")}
    # Continue the earlier session only if one of its turns finished; otherwise start clean.
    session = next((r["session"] for r in prior if r.get("ok") and r.get("session")), None)
    resume = session is not None
    session = session or str(uuid.uuid4())
    for t in tasks:
        if stop.is_set():
            return
        if t["id"] in done:
            continue
        rec = run_one(t, arm, rep, args, run_dir, tdir, session, resume)
        rec["turn"] = tasks.index(t) + 1
        emit(rec)
        if rec.get("ok"):
            resume = True
        elif usage_limited(rec):
            stop.set()
            return
        elif not resume:
            # A failed first turn may or may not have created the session: use a new id.
            session = str(uuid.uuid4())


def cmd_run(args):
    corpora, tasks = load_tasks()
    if args.tasks:
        tasks = [t for t in tasks if t["id"] in args.tasks.split(",")]
    if args.corpora:
        tasks = [t for t in tasks if t["corpus"] in args.corpora.split(",")]
    arms = args.arms.split(",")
    used = {t["corpus"] for t in tasks}
    info = prepare({k: v for k, v in corpora.items() if k in used})
    global EULIX_MAX_TOKENS
    EULIX_MAX_TOKENS = args.eulix_max_tokens
    if EULIX_MAX_TOKENS:
        for name in used:
            ensure_budget_variant(name)

    run_id = args.resume or datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    run_dir = RESULTS / run_id
    tdir = state("transcripts", run_id)
    tdir.mkdir(parents=True, exist_ok=True)
    for name in used:
        (tdir / f"mcp-{name}.json").write_text(json.dumps(
            {"mcpServers": {"eulix": {"command": "eulix", "args": ["mcp", "--repo", str(workdir(name, "eulix"))]}}}))

    runs_file = run_dir / "runs.jsonl"
    done, earlier = set(), []
    if runs_file.exists():
        for line in runs_file.read_text().splitlines():
            r = json.loads(line)
            earlier.append(r)
            if r.get("ok"):
                done.add((r["task"], r["arm"], r["rep"]))
    if args.resume and earlier and any("session" in r for r in earlier) != args.chain:
        raise BenchError(f"run {run_id} was {'' if args.chain else 'not '}run with --chain; resume it the same way")
    eulix_ver = subprocess.run(["eulix", "version"], capture_output=True, text=True).stdout.strip()
    claude_ver = subprocess.run(["claude", "--version"], capture_output=True, text=True).stdout.strip()
    meta = {"run_id": run_id, "model": args.model, "effort": args.effort or "default",
            "eulix_max_tokens": args.eulix_max_tokens or 8192, "reps": args.reps, "jobs": args.jobs, "arms": arms,
            "tools": BASE_TOOLS, "prompt_suffix": PROMPT_SUFFIX, "hint": HINT, "budget_usd": args.budget,
            "mode": "chain" if args.chain else "session-per-task",
            "eulix": eulix_ver, "claude_code": claude_ver, "corpora": info,
            "started": datetime.now(timezone.utc).isoformat(timespec="seconds")}
    write_json(run_dir / "meta.json", meta)

    run_dir.mkdir(parents=True, exist_ok=True)
    if args.chain:
        run_chained(args, tasks, arms, run_id, run_dir, tdir, runs_file, earlier)
    else:
        run_sessions(args, tasks, arms, run_id, run_dir, tdir, runs_file, done)
    meta["finished"] = datetime.now(timezone.utc).isoformat(timespec="seconds")
    write_json(run_dir / "meta.json", meta)
    report(run_dir)


def _say_rec(rec: dict) -> None:
    say(f"  {rec['task']:<36} {rec['arm']:<6} #{rec['rep']}  "
        f"{'OK ' if rec.get('correct') else 'MISS'}  {rec.get('input_tokens', 0):>8} in  "
        f"${rec.get('cost_usd', 0):.3f}  {rec.get('tool_calls', 0):>2} tools  {rec['wall_s']}s")


def run_chained(args, tasks, arms, run_id, run_dir, tdir, runs_file, earlier):
    todo = [(corpus, a, r, ts) for corpus, a, r, ts in chains(tasks, arms, args.reps)
            if not all(any(e["task"] == t["id"] and e["arm"] == a and e["rep"] == r and e.get("ok")
                           for e in earlier) for t in ts)]
    if args.max_sessions is not None:
        todo = todo[:args.max_sessions]
    say(f"run {run_id}: {len(todo)} chained sessions to go, {args.jobs} at a time")
    lock, stop = threading.Lock(), threading.Event()
    with open(runs_file, "a") as out:
        def emit(rec):
            with lock:
                out.write(json.dumps(rec) + "\n")
                out.flush()
                _say_rec(rec)
        with ThreadPoolExecutor(args.jobs) as ex:
            futs = [ex.submit(run_chain, corpus, a, r, ts, args, run_dir, tdir,
                              [e for e in earlier if e["corpus"] == corpus and e["arm"] == a and e["rep"] == r],
                              emit, stop)
                    for corpus, a, r, ts in todo]
            for f in futs:
                f.result()
    if stop.is_set():
        say(f"  usage limit reached; stopped. Resume later with --chain --resume {run_id}")


def run_sessions(args, tasks, arms, run_id, run_dir, tdir, runs_file, done):
    # Interleave arms and tasks so drift in API latency hits both arms alike.
    jobs = [(t, a, r) for r in range(1, args.reps + 1) for t in tasks for a in arms
            if (t["id"], a, r) not in done]
    if args.max_sessions is not None:
        jobs = jobs[:args.max_sessions]
    say(f"run {run_id}: {len(jobs)} sessions to go ({len(done)} already done), {args.jobs} at a time")
    with ThreadPoolExecutor(args.jobs) as ex, open(runs_file, "a") as out:
        futs = {ex.submit(run_one, t, a, r, args, run_dir, tdir): (t["id"], a, r) for t, a, r in jobs}
        for f in as_completed(futs):
            rec = f.result()
            out.write(json.dumps(rec) + "\n")
            out.flush()
            if usage_limited(rec):
                # Every later session would fail the same way and still count against the run.
                for other in futs:
                    other.cancel()
                say(f"  usage limit reached ({rec.get('answer', '').strip()[:80]}); "
                    f"stopping. Resume later with --resume {run_id}")
            _say_rec(rec)


# ------------------------------------------------------------------ report

def _latest(runs):
    """If a cell ran more than once (a failed try, then a resume), keep the last record."""
    last = {}
    for r in runs:
        last[(r["task"], r["arm"], r["rep"])] = r
    return list(last.values())


def _med(xs):
    xs = [x for x in xs if x is not None]
    return statistics.median(xs) if xs else None


def _pct(a, b):
    return None if not a or b is None else (b - a) / a * 100


def fmt_pct(x):
    return "n/a" if x is None else f"{x:+.0f}%"


def report(run_dir: Path) -> Path:
    run_dir = Path(run_dir)
    meta = json.loads((run_dir / "meta.json").read_text())
    runs = _latest(json.loads(l) for l in (run_dir / "runs.jsonl").read_text().splitlines() if l.strip())
    _, tasks = load_tasks()
    arms = [a for a in ARMS if any(r["arm"] == a for r in runs)]
    order = [t["id"] for t in tasks if any(r["task"] == t["id"] for r in runs)]
    kinds = {t["id"]: t["kind"] for t in tasks}
    keys = ("input_tokens", "output_tokens", "cost_usd", "tool_calls", "eulix_calls", "duration_s")

    def sel(task, arm):
        return [r for r in runs if r["task"] == task and r["arm"] == arm]

    med = {(t, a): {k: _med([r.get(k) for r in sel(t, a) if r.get("ok")]) for k in keys} for t in order for a in arms}
    # Sessions that never finished (usage limit, crash) are left out of every figure and listed below.
    ok = {(t, a): (sum(1 for r in sel(t, a) if r.get("correct")), sum(1 for r in sel(t, a) if r.get("ok")))
          for t in order for a in arms}
    # Totals compare arms only over tasks that every arm finished at least once.
    full = [t for t in order if all(ok[(t, a)][1] for a in arms)]
    tot = {a: {k: sum(med[(t, a)][k] or 0 for t in full) for k in keys} for a in arms}
    failed = [r for r in runs if not r.get("ok")]

    lines = [f"# Agent token savings: run {meta['run_id']}", "",
             f"Model `{meta['model']}` (effort: {meta.get('effort', 'default')}) · "
             f"Claude Code {meta['claude_code'].split()[0]} · "
             f"{meta['eulix'].splitlines()[0]} (EHNAS engine, max_tokens {meta.get('eulix_max_tokens', 8192)}) · " +
             (f"{meta['reps']} rep(s) · chained: one session per repo and arm, each task one turn in "
              "tasks.toml order, so later turns see the earlier ones as context · "
              if meta.get("mode") == "chain" else f"{meta['reps']} reps per task and arm · ") +
             f"{meta['jobs']} sessions at a time · base tools `{meta['tools']}`.", "",
             "- `plain`: no Eulix. `eulix`: Eulix MCP server added, nothing else. "
             "`eulix-hint`: Eulix MCP plus one system-prompt line telling the agent to query it first.",
             "- `input` is every input token the model read across the session (uncached + cache reads + "
             "cache writes). Cost is Claude Code's own figure at API list prices. Figures are medians of the reps; "
             "totals are sums of those medians. Δ is against `plain`; negative is a saving.",
             f"- Sessions finished: {len(runs) - len(failed)} of {len(runs)}. Totals cover the {len(full)} of "
             f"{len(order)} tasks that every arm finished at least once. Correct counts are out of finished sessions.",
             "", "## Totals", "",
             "| Arm | Input tokens | Δ | Output tokens | Cost (USD) | Δ | Tool calls | Eulix calls | Time | Correct |",
             "|:--|--:|--:|--:|--:|--:|--:|--:|--:|:-:|"]
    P = tot["plain"] if "plain" in tot else None
    for a in arms:
        T = tot[a]
        c = sum(ok[(t, a)][0] for t in order), sum(ok[(t, a)][1] for t in order)
        d_in = "" if a == "plain" or not P else fmt_pct(_pct(P["input_tokens"], T["input_tokens"]))
        d_c = "" if a == "plain" or not P else fmt_pct(_pct(P["cost_usd"], T["cost_usd"]))
        lines.append(f"| `{a}` | {T['input_tokens']:,.0f} | {d_in} | {T['output_tokens']:,.0f} | "
                     f"{T['cost_usd']:.3f} | {d_c} | {T['tool_calls']:g} | {T['eulix_calls']:g} | "
                     f"{T['duration_s']:.0f} s | {c[0]}/{c[1]} |")

    lines += ["", "## Per task", "",
              "| Task | Kind | Arm | Input tokens | Δ input | Cost (USD) | Δ cost | Tool calls | Eulix calls "
              "| Time | Correct |", "|:--|:--|:--|--:|--:|--:|--:|--:|--:|--:|:-:|"]
    for t in order:
        p = med.get((t, "plain"))
        for i, a in enumerate(arms):
            m = med[(t, a)]
            d_in = "" if a == "plain" or not p else fmt_pct(_pct(p["input_tokens"], m["input_tokens"]))
            d_c = "" if a == "plain" or not p else fmt_pct(_pct(p["cost_usd"], m["cost_usd"]))
            lines.append(f"| {t if i == 0 else ''} | {kinds[t] if i == 0 else ''} | `{a}` | "
                         f"{m['input_tokens'] or 0:,.0f} | {d_in} | {m['cost_usd'] or 0:.3f} | {d_c} | "
                         f"{m['tool_calls'] or 0:g} | {m['eulix_calls'] or 0:g} | {m['duration_s'] or 0:.0f} s | "
                         f"{ok[(t, a)][0]}/{ok[(t, a)][1]} |")

    lines += ["", "## Index build (Eulix arms, not counted above)", "",
              "| Corpus | Tag | `eulix analyze` |", "|:--|:--|--:|"]
    for name, c in meta["corpora"].items():
        s = c.get("index_seconds")
        lines.append(f"| {name} | {c.get('tag')} | {'n/a' if s is None else f'{s:.1f} s'} |")
    if failed:
        why = sorted({(r.get("answer") or r.get("error") or r.get("subtype") or "?").strip()[:90] for r in failed})
        lines += ["", "## Sessions that did not finish", "",
                  f"{len(failed)} sessions ended without an answer and are excluded above. Reasons: "
                  + "; ".join(f"`{w}`" for w in why) + "."]
    misses = [r for r in runs if r.get("ok") and not r.get("correct")]
    if misses:
        lines += ["", "## Answers that failed the rubric", "",
                  "| Task | Arm | Rep | Missing |", "|:--|:--|--:|:--|"]
        for r in sorted(misses, key=lambda r: (r["task"], r["arm"], r["rep"])):
            why = "; ".join(" or ".join(f"`{p}`" for p in g) for g in (r.get("missed") or []))
            lines.append(f"| {r['task']} | `{r['arm']}` | {r['rep']} | {why} |")
    out = run_dir / "report.md"
    out.write_text("\n".join(lines) + "\n")
    say(f"report: {out}")
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("prepare", help="fetch corpora and build the eulix indexes")
    p.add_argument("--rebuild", action="store_true")
    r = sub.add_parser("run", help="run the benchmark")
    r.add_argument("--model", default="sonnet")
    r.add_argument("--reps", type=int, default=3)
    r.add_argument("--jobs", type=int, default=3, help="sessions running at once")
    r.add_argument("--arms", default=",".join(ARMS))
    r.add_argument("--tasks", help="comma-separated task ids (default: all)")
    r.add_argument("--corpora", help="comma-separated corpus names (default: all)")
    r.add_argument("--budget", type=float, default=3.0, help="per-session --max-budget-usd")
    r.add_argument("--effort", choices=["low", "medium", "high", "xhigh", "max"],
                   help="Claude Code --effort for every session (default: the CLI's own)")
    r.add_argument("--eulix-max-tokens", type=int,
                   help="[llm] max_tokens for the eulix arms (default: the template's 8192)")
    r.add_argument("--max-sessions", type=int, help="run at most this many sessions, in interleaved order")
    r.add_argument("--timeout", type=int, default=900, help="per-session timeout, seconds")
    r.add_argument("--resume", metavar="RUN_ID", help="continue an earlier run, skipping finished sessions")
    r.add_argument("--chain", action="store_true",
                   help="one session per corpus, arm and rep; each task is one turn of it (far fewer sessions)")
    rp = sub.add_parser("report", help="rebuild report.md for a run")
    rp.add_argument("run_dir")
    a = ap.parse_args()
    try:
        if a.cmd == "prepare":
            corpora, _ = load_tasks()
            print(json.dumps(prepare(corpora, a.rebuild), indent=1))
        elif a.cmd == "run":
            for tool in ("claude", "eulix"):
                if not shutil.which(tool):
                    raise BenchError(f"`{tool}` is not on PATH")
            cmd_run(a)
        else:
            report(Path(a.run_dir))
    except BenchError as e:
        say(f"error: {e}")
        sys.exit(2)


if __name__ == "__main__":
    main()
