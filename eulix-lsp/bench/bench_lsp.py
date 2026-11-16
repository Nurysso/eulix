#!/usr/bin/env python3
# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Benchmark eulixlsp over stdio, the way an editor talks to it.

    python3 bench/bench_lsp.py BIN [--files 1000,5000] [--workspace PATH ...]

For each workspace it starts a fresh server and measures:
  scan       time to index, memory after the scan, request latency during it
  requests   p50/p95/max latency of every feature, on real symbol positions
  typing     a burst of 100 edits until diagnostics for the last one arrive
  churn      memory, threads and fds over repeated close/open/edit rounds
  watcher    one batch of changed files until all new symbols are searchable
  health     panics on stderr, JSON-RPC errors, non-protocol bytes on stdout,
             exit code after shutdown

Synthetic workspaces (--files) are generated in a temp dir. --workspace runs
on a real tree without writing to it: the watcher test, which edits files,
only runs on synthetic ones. Results go to bench/results/<label>.json.
Linux only: memory, CPU, threads and fds come from /proc.

For very large trees, cap memory so only the server gets killed, not the box:
    systemd-run --user --scope -p MemoryMax=8G -p MemorySwapMax=0 python3 bench/bench_lsp.py ...
"""
import argparse
import itertools
import json
import math
import os
import pathlib
import platform
import re
import select
import shutil
import subprocess
import sys
import tempfile
import threading
import time

HERE = pathlib.Path(__file__).resolve().parent
# Mirrors SUPPORTED_EXTENSIONS and the default ignore_dirs in the server.
EXTS = {".c", ".h", ".cpp", ".cc", ".cxx", ".hpp", ".hxx", ".h++", ".c++", ".go", ".java",
        ".js", ".mjs", ".cjs", ".php", ".phtml", ".py", ".pyw", ".pyi", ".rs", ".ts", ".mts", ".cts"}
IGNORED = {".git", ".eulix", "node_modules", "target", "dist", "build", "__pycache__",
           ".venv", "venv", ".mypy_cache", ".pytest_cache"}
KEYWORDS = {"if", "for", "while", "switch", "return", "match", "catch", "fn", "def",
            "function", "sizeof", "elif", "and", "or", "not", "in", "new", "super"}
LANG_ID = {".py": "python", ".rs": "rust", ".ts": "typescript", ".js": "javascript",
           ".go": "go", ".java": "java", ".php": "php", ".c": "c", ".h": "c"}
FUNCS_PER_FILE = 8

# --- synthetic workspace ---------------------------------------------------
# Every file defines `helper`, like `new` or `run` in real code, so one name
# has as many definitions as there are files. Calls point across files.
TEMPLATES = {
    ".py": ("class Svc{i}:\n    def start(self):\n        return helper(self)\n\n"
            "    def stop(self):\n        return f{i}_0(1)\n\n",
            "def f{i}_{k}(x):\n    # TODO: tune\n    return f{j}_{k}(x) + helper(x)\n\n",
            "def helper(x):\n    return x\n",
            "\ndef bench_watch_{w}(x):\n    return x\n"),
    ".rs": ("pub struct Svc{i};\n\nimpl Svc{i} {{\n    pub fn start(&self) -> i32 {{ helper(1) }}\n"
            "    pub fn stop(&self) -> i32 {{ f{i}_0(1) }}\n}}\n\n",
            "pub fn f{i}_{k}(x: i32) -> i32 {{\n    // TODO: tune\n    f{j}_{k}(x) + helper(x)\n}}\n\n",
            "pub fn helper(x: i32) -> i32 {{ x }}\n",
            "\npub fn bench_watch_{w}(x: i32) -> i32 {{ x }}\n"),
    ".ts": ("export class Svc{i} {{\n  start(): number {{ return helper(1); }}\n"
            "  stop(): number {{ return f{i}_0(1); }}\n}}\n\n",
            "export function f{i}_{k}(x: number): number {{\n  // TODO: tune\n"
            "  return f{j}_{k}(x) + helper(x);\n}}\n\n",
            "export function helper(x: number): number {{ return x; }}\n",
            "\nexport function bench_watch_{w}(x: number): number {{ return x; }}\n"),
}


def generate(root, n):
    exts = list(TEMPLATES)
    for i in range(n):
        ext = exts[i % len(exts)]
        head, func, tail, _ = TEMPLATES[ext]
        body = head.format(i=i) + "".join(
            func.format(i=i, k=k, j=(i * 31 + k * 7 + 1) % n) for k in range(FUNCS_PER_FILE)
        ) + tail
        path = root / f"pkg{i // 100:03d}" / f"m{i:05d}{ext}"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body)


def source_files(root):
    out = []
    for d, dirs, files in os.walk(root):
        dirs[:] = sorted(x for x in dirs if x not in IGNORED and not x.endswith(".egg-info"))
        out += [pathlib.Path(d, f) for f in sorted(files) if pathlib.Path(f).suffix.lower() in EXTS]
    return out


# --- client ------------------------------------------------------------------
# Not e2e_lsp.Client: that one reads a byte at a time and polls every 20 ms,
# which would swamp sub-millisecond request timings.
class ServerDied(Exception):
    pass


class StopRun(Exception):
    pass


class Server:
    def __init__(self, binary, stderr_path):
        self.err = open(stderr_path, "wb")
        self.p = subprocess.Popen([binary], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=self.err, env=dict(os.environ, RUST_LOG="warn", NO_COLOR="1"))
        self.fd = self.p.stdout.fileno()
        self.buf = bytearray()
        self.ids = itertools.count(1)
        self.responses = {}
        self.diags = {}  # uri -> [(version, time, count)]
        self.logs = []
        self.garbage = 0
        # Memory once a second, independent of requests that may be blocked,
        # so a run that dies still shows how memory got there.
        self.t0 = time.perf_counter()
        self.samples = []
        threading.Thread(target=self._sample, daemon=True).start()

    def _sample(self):
        while self.p.poll() is None:
            try:
                s = self.stats()
                self.samples.append({"t": round(time.perf_counter() - self.t0, 1),
                                     "rss_mb": s["rss_mb"], "cpu_s": s["cpu_s"]})
            except ServerDied:
                return
            time.sleep(1)

    def send(self, obj):
        data = json.dumps(obj).encode()
        try:
            self.p.stdin.write(b"Content-Length: %d\r\n\r\n" % len(data) + data)
            self.p.stdin.flush()
        except BrokenPipeError:
            raise ServerDied(self.p.wait()) from None

    def notify(self, method, params):
        self.send({"jsonrpc": "2.0", "method": method, "params": params})

    def _handle(self, msg):
        if "id" in msg and ("result" in msg or "error" in msg):
            self.responses[msg["id"]] = msg
        elif msg.get("method") == "textDocument/publishDiagnostics":
            p = msg["params"]
            self.diags.setdefault(p["uri"], []).append(
                (p.get("version"), time.perf_counter(), len(p["diagnostics"])))
        elif msg.get("method") == "window/logMessage":
            self.logs.append(msg["params"]["message"])

    def pump(self, timeout):
        """Handle what arrives within `timeout` s. False once the server is gone."""
        if not select.select([self.fd], [], [], max(timeout, 0))[0]:
            return True
        chunk = os.read(self.fd, 1 << 16)
        if not chunk:
            return False
        self.buf += chunk
        while (end := self.buf.find(b"\r\n\r\n")) >= 0:
            m = re.fullmatch(rb"(?i)content-length: *(\d+)(\r\ncontent-type:[^\r]*)?", self.buf[:end])
            if not m:
                self.garbage += 1  # bytes on stdout that are not a protocol frame
                found = self.buf.lower().find(b"content-length", 1)
                del self.buf[:found if found > 0 else end + 4]
                continue
            total = end + 4 + int(m.group(1))
            if len(self.buf) < total:
                break
            body = bytes(self.buf[end + 4:total])
            del self.buf[:total]
            try:
                self._handle(json.loads(body))
            except json.JSONDecodeError:
                self.garbage += 1
        return True

    def wait(self, pred, timeout):
        deadline = time.perf_counter() + timeout
        while not pred():
            left = deadline - time.perf_counter()
            if left <= 0 or not self.pump(left):
                return False
        return True

    def request(self, method, params=None, timeout=60):
        rid = next(self.ids)
        msg = {"jsonrpc": "2.0", "id": rid, "method": method}
        if params is not None:
            msg["params"] = params
        t = time.perf_counter()
        self.send(msg)
        if not self.wait(lambda: rid in self.responses, timeout):
            if self.p.poll() is not None:
                raise ServerDied(self.p.returncode)
            raise TimeoutError(f"{method} after {timeout}s")
        resp = self.responses.pop(rid)
        return resp.get("result"), resp.get("error"), time.perf_counter() - t

    def stats(self):
        try:
            st = {}
            with open(f"/proc/{self.p.pid}/status") as f:
                for line in f:
                    k, _, v = line.partition(":")
                    if k in ("VmRSS", "VmHWM", "Threads"):
                        st[k] = int(v.split()[0])
            with open(f"/proc/{self.p.pid}/stat") as f:
                fields = f.read().rsplit(")", 1)[1].split()
            return {"rss_mb": round(st["VmRSS"] / 1024, 1), "peak_rss_mb": round(st["VmHWM"] / 1024, 1),
                    "threads": st["Threads"], "fds": len(os.listdir(f"/proc/{self.p.pid}/fd")),
                    "cpu_s": round((int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK"), 2)}
        except (FileNotFoundError, ProcessLookupError, KeyError):  # gone, or a zombie with no memory
            raise ServerDied(self.p.poll()) from None


# --- measurements ------------------------------------------------------------
def pct(xs, q):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(q * (len(xs) - 1) + 0.5))] if xs else None


def size(res):
    """Result size: list length, or items/tokens/files for the dict shapes."""
    if isinstance(res, list):
        return len(res)
    if isinstance(res, dict):
        return next((len(res[k]) for k in ("items", "data", "changes") if k in res), 1)
    return 0


class Timings:
    def __init__(self):
        self.data = {}

    def call(self, srv, method, params, tag=None, timeout=60):
        key = f"{method} [{tag}]" if tag else method
        d = self.data.setdefault(key, {"ms": [], "errors": 0, "nulls": 0, "items": [], "timeouts": 0})
        try:
            res, err, dt = srv.request(method, params, timeout)
        except TimeoutError:
            d["timeouts"] += 1
            return None
        d["ms"].append(dt * 1000)
        if err:
            d["errors"] += 1
            d.setdefault("error_samples", []).append(err.get("message", "")[:120])
        elif not res:
            d["nulls"] += 1
        d["items"].append(size(res))
        return res

    def summary(self):
        return {m: {"n": len(d["ms"]), "p50_ms": round(pct(d["ms"], .5) or 0, 3),
                    "p95_ms": round(pct(d["ms"], .95) or 0, 3), "max_ms": round(max(d["ms"], default=0), 3),
                    "errors": d["errors"], "nulls": d["nulls"], "timeouts": d["timeouts"],
                    "avg_items": round(sum(d["items"]) / max(len(d["items"]), 1), 1),
                    **({"error_samples": d["error_samples"][:3]} if "error_samples" in d else {})}
                for m, d in self.data.items()}


def utf16_col(line, col):
    return len(line[:col].encode("utf-16-le")) // 2


def flatten(symbols):
    for s in symbols or []:
        yield s
        yield from flatten(s.get("children"))


def evenly(xs, k):
    return xs if len(xs) <= k else [xs[i * len(xs) // k] for i in range(k)]


def doc(uri):
    return {"textDocument": {"uri": uri}}


def at(uri, pos):
    return {"textDocument": {"uri": uri}, "position": pos}


def open_file(srv, path, text, version=1):
    srv.notify("textDocument/didOpen", {"textDocument": {
        "uri": path.as_uri(), "languageId": LANG_ID.get(path.suffix, "plaintext"),
        "version": version, "text": text}})


def wait_diag(srv, uri, version, since, timeout=30):
    def seen():
        return any(v == version and t >= since for v, t, _ in srv.diags.get(uri, []))
    return srv.wait(seen, timeout)


def run(binary, root, synthetic, args, log_dir, name):
    out = {"workspace": name, "health": {}}
    files = source_files(root)
    out["source_files"] = len(files)
    stderr_path = log_dir / f"{name}.stderr.log"
    srv = Server(binary, stderr_path)
    try:
        phases(srv, out, root, synthetic, files, args)
    except ServerDied as e:
        out["died"] = {"after_phase": list(out.get("phases", {"start": 0}))[-1], "exit_code": e.args[0]}
    except StopRun as e:
        out["stopped"] = str(e)
    return finish(srv, out, stderr_path)


def phases(srv, out, root, synthetic, files, args):
    t = time.perf_counter()
    _, err, _ = srv.request("initialize", {
        "processId": os.getpid(), "rootUri": root.as_uri(),
        "capabilities": {"textDocument": {"documentSymbol": {"hierarchicalDocumentSymbolSupport": True}}},
        "initializationOptions": {}})
    out["initialize_ms"] = round((time.perf_counter() - t) * 1000, 2)
    assert not err, err
    out["idle_stats"] = srv.stats()
    # Stats after each phase: peak RSS only rises, so the phase that raised it
    # is the one to look at.
    out["phases"] = {"initialize": out["idle_stats"]}

    def mark(phase):
        out["phases"][phase] = srv.stats()

    # Scan, with an editor's light traffic against it: one open buffer and a
    # workspace search, so lock contention with the index commit shows up.
    sample = evenly([f for f in files if f.stat().st_size < 256 * 1024], args.open)
    texts = {f: f.read_text(errors="replace") for f in sample}
    during = Timings()
    t = time.perf_counter()
    srv.notify("initialized", {})
    open_file(srv, sample[0], texts[sample[0]])
    done = lambda: any("indexed" in m for m in srv.logs)  # noqa: E731
    while not done() and time.perf_counter() - t < args.scan_timeout:
        during.call(srv, "textDocument/documentSymbol", doc(sample[0].as_uri()), timeout=args.scan_timeout)
        during.call(srv, "workspace/symbol", {"query": "f1"}, timeout=args.scan_timeout)
        srv.wait(done, 0.05)
    out["scan_s"] = round(time.perf_counter() - t, 3) if done() else None
    out["scan_log"] = next((m for m in srv.logs if "indexed" in m), "scan did not finish")
    out["requests_during_scan"] = during.summary()
    out["after_scan_stats"] = srv.stats()
    out["phases"]["scan"] = out["after_scan_stats"]
    if not done():
        # Index requests would queue behind the unfinished commit for minutes.
        raise StopRun(f"scan did not finish within {args.scan_timeout:g} s")

    # Open the sample like an editor restoring tabs; time each until its
    # diagnostics arrive.
    open_ms = []
    for f in sample[1:]:
        t = time.perf_counter()
        open_file(srv, f, texts[f])
        if wait_diag(srv, f.as_uri(), 1, t):
            open_ms.append((time.perf_counter() - t) * 1000)
    out["open_to_diagnostics_ms"] = {"n": len(open_ms), "p50": round(pct(open_ms, .5) or 0, 2),
                                     "max": round(max(open_ms, default=0), 2)}
    mark("open")

    # Requests at real positions: declarations from the outline, and call
    # sites of names declared elsewhere (the cross-file path).
    tm = Timings()
    decls, calls, sites = [], [], []
    for f in sample:
        uri = f.as_uri()
        tm.call(srv, "textDocument/semanticTokens/full", doc(uri))
        tm.call(srv, "textDocument/foldingRange", doc(uri))
        syms = list(flatten(tm.call(srv, "textDocument/documentSymbol", doc(uri))))
        names = {s["name"] for s in syms}
        decls += [(uri, s["selectionRange"]["start"], s["name"]) for s in syms]
        for ln, line in enumerate(texts[f].splitlines()):
            for m in re.finditer(r"\b([A-Za-z_]\w*)\s*\(", line):
                if m.group(1) in KEYWORDS:
                    continue
                site = (uri, {"line": ln, "character": utf16_col(line, m.start(1))}, m.group(1))
                sites.append(site)
                if m.group(1) not in names:
                    calls.append(site)
    def symbol_requests(uri, pos, tag):
        tm.call(srv, "textDocument/hover", at(uri, pos), tag)
        tm.call(srv, "textDocument/documentHighlight", at(uri, pos), tag)
        tm.call(srv, "textDocument/references", {**at(uri, pos), "context": {"includeDeclaration": True}}, tag)
        tm.call(srv, "textDocument/rename", {**at(uri, pos), "newName": "bench_renamed"}, tag)
        items = tm.call(srv, "textDocument/prepareCallHierarchy", at(uri, pos), tag)
        if items:
            tm.call(srv, "callHierarchy/incomingCalls", {"item": items[0]}, tag)
            tm.call(srv, "callHierarchy/outgoingCalls", {"item": items[0]}, tag)

    for uri, pos, _ in evenly(decls, args.targets):
        symbol_requests(uri, pos, "decl")
    for uri, pos, name in evenly(calls, args.targets):
        tm.call(srv, "textDocument/definition", at(uri, pos), "call")
        tm.call(srv, "textDocument/hover", at(uri, pos), "call")
        if len(name) > 2:
            tm.call(srv, "textDocument/completion", at(uri, {**pos, "character": pos["character"] + 2}), "call")
    mark("requests")
    # The most common call name (like `new` or `run`) has the most definitions
    # and callers, so cost that grows with either shows up here.
    counts = {}
    for _, _, name in sites:
        counts[name] = counts.get(name, 0) + 1
    if counts:
        hot = max(counts, key=counts.get)
        out["hot_name"] = {"name": hot, "call_sites_in_open_files": counts[hot]}
        for uri, pos, _ in evenly([s for s in sites if s[2] == hot], 10):
            tm.call(srv, "textDocument/definition", at(uri, pos), "hot")
            symbol_requests(uri, pos, "hot")
    mark("hot_requests")
    for _, _, name in evenly(decls, 20):
        tm.call(srv, "workspace/symbol", {"query": name[:3]})
    out["requests"] = tm.summary()

    # Typing: 100 alternating insert/delete edits at the top of the largest
    # open file, as fast as the pipe takes them.
    big = max(sample, key=lambda f: len(texts[f]))
    uri, edits = big.as_uri(), 100
    t0 = time.perf_counter()
    for v in range(2, 2 + edits):
        start, end, text = ({"line": 0, "character": 0}, {"line": 0, "character": 0}, "x") if v % 2 == 0 \
            else ({"line": 0, "character": 0}, {"line": 0, "character": 1}, "")
        srv.notify("textDocument/didChange", {
            "textDocument": {"uri": uri, "version": v},
            "contentChanges": [{"range": {"start": start, "end": end}, "text": text}]})
    t_last = time.perf_counter()
    ok = wait_diag(srv, uri, 1 + edits, t_last)
    publishes = sum(1 for v, t, _ in srv.diags.get(uri, []) if t >= t0 and v and v > 1)
    out["typing"] = {"edits": edits, "file_bytes": len(texts[big].encode()),
                     "send_ms": round((t_last - t0) * 1000, 2),
                     "last_edit_to_diagnostics_ms": round((srv.diags[uri][-1][1] - t_last) * 1000, 1) if ok else None,
                     "publishes_during_burst": publishes, "debounce_ms": 200}
    mark("typing")

    # Churn: close, reopen and edit every sample file each round. Memory or
    # threads that keep climbing across identical rounds point at a leak.
    rounds = []
    for r in range(args.rounds):
        t = time.perf_counter()
        for f in sample:
            u = f.as_uri()
            srv.notify("textDocument/didClose", doc(u))
            open_file(srv, f, texts[f])
            srv.notify("textDocument/didChange", {"textDocument": {"uri": u, "version": 2},
                                                  "contentChanges": [{"text": texts[f] + f"\n// {r}\n"}]})
        settled = all(wait_diag(srv, f.as_uri(), 2, t) for f in sample)
        rounds.append({"round": r, "ms": round((time.perf_counter() - t) * 1000, 1), "settled": settled,
                       **srv.stats()})
    out["churn"] = {"rounds": rounds,
                    "rss_growth_mb": round(rounds[-1]["rss_mb"] - rounds[0]["rss_mb"], 1),
                    "thread_growth": rounds[-1]["threads"] - rounds[0]["threads"],
                    "fd_growth": rounds[-1]["fds"] - rounds[0]["fds"]}
    mark("churn")

    # Watcher: one batch like a branch switch. Synthetic only: it edits files.
    if synthetic:
        opened = set(sample)
        targets = [f for f in files if f not in opened][:args.watch]
        for w, f in enumerate(targets):
            with open(f, "a") as fh:
                fh.write(TEMPLATES[f.suffix][3].format(w=w))
        t = time.perf_counter()
        srv.notify("workspace/didChangeWatchedFiles",
                   {"changes": [{"uri": f.as_uri(), "type": 2} for f in targets]})
        found = []

        def all_found():
            res, _, _ = srv.request("workspace/symbol", {"query": "bench_watch_"})
            found[:] = res or []
            return len(found) >= len(targets)
        while not all_found() and time.perf_counter() - t < 60:
            srv.pump(0.01)
        out["watcher"] = {"events": len(targets), "found": len(found),
                          "ms": round((time.perf_counter() - t) * 1000, 1)}
        mark("watcher")


def finish(srv, out, stderr_path):
    out["memory_timeline"] = srv.samples
    code = srv.p.poll()
    if code is None:
        try:
            out["final_stats"] = srv.stats()
            # Shutdown may queue behind a long index commit; that wait is a result too.
            _, _, dt = srv.request("shutdown", timeout=10)
            out["shutdown_ms"] = round(dt * 1000, 2)
            srv.notify("exit", None)
            code = srv.p.wait(timeout=5)
        except (TimeoutError, subprocess.TimeoutExpired):
            srv.p.kill()
            code = "hung, killed"
        except ServerDied as e:
            code = e.args[0]
    srv.err.close()
    stderr = re.sub(r"\x1b\[[0-9;]*m", "", stderr_path.read_text(errors="replace"))
    all_timings = [out.get("requests", {}), out.get("requests_during_scan", {})]
    out["health"] = {
        "exit_code": code, "stdout_garbage_frames": srv.garbage,
        "panics": stderr.count("panicked"), "stderr_errors": len(re.findall(r"\bERROR\b", stderr)),
        "stderr_warnings": len(re.findall(r"\bWARN\b", stderr)),
        "rpc_errors": sum(d["errors"] for s in all_timings for d in s.values()),
        "rpc_timeouts": sum(d["timeouts"] for s in all_timings for d in s.values()),
    }
    return out


def report(r):
    # A stopped or killed run has only the sections it reached.
    print(f"\n== {r['workspace']}  ({r['source_files']} source files)")
    for k in ("died", "stopped"):
        if k in r:
            print(f"  {k.upper()}: {r[k]}")
    if "scan_log" in r:
        print(f"  scan {r['scan_s']} s   {r['scan_log']}")
    tl = r["memory_timeline"]
    if tl:
        top = max(tl, key=lambda x: x["rss_mb"])
        print(f"  memory: highest sampled rss {top['rss_mb']} MB at {top['t']} s, last sample "
              f"{tl[-1]['rss_mb']} MB at {tl[-1]['t']} s, cpu {tl[-1]['cpu_s']} s")
    if "after_scan_stats" in r and "final_stats" in r:
        a, s = r["after_scan_stats"], r["final_stats"]
        print(f"  memory: idle {r['idle_stats']['rss_mb']} MB, after scan {a['rss_mb']} MB, "
              f"peak {s['peak_rss_mb']} MB, end {s['rss_mb']} MB   threads {s['threads']}")
    if "typing" in r:
        o, ty = r["open_to_diagnostics_ms"], r["typing"]
        print(f"  open->diagnostics p50 {o['p50']} ms, max {o['max']} ms   "
              f"typing: last edit->diagnostics {ty['last_edit_to_diagnostics_ms']} ms "
              f"(debounce {ty['debounce_ms']}), {ty['publishes_during_burst']} publish(es)")
    if "churn" in r:
        c = r["churn"]
        print(f"  churn x{len(c['rounds'])}: rss {c['rounds'][0]['rss_mb']} -> {c['rounds'][-1]['rss_mb']} MB "
              f"({c['rss_growth_mb']:+}), threads {c['thread_growth']:+}, fds {c['fd_growth']:+}")
    if "watcher" in r:
        w = r["watcher"]
        print(f"  watcher: {w['found']}/{w['events']} new symbols searchable after {w['ms']} ms")
    if "phases" in r:
        print("  peak rss by phase: " + ", ".join(f"{k} {v['peak_rss_mb']}" for k, v in r["phases"].items()))
    if "hot_name" in r:
        print(f"  hot name: {r['hot_name']['name']} ({r['hot_name']['call_sites_in_open_files']} call sites in open files)")
    print(f"  health: {r['health']}" + (f"   shutdown {r['shutdown_ms']} ms" if "shutdown_ms" in r else ""))
    for title, rows in (("during scan", r.get("requests_during_scan", {})), ("indexed", r.get("requests", {}))):
        if not rows:
            continue
        print(f"  {title:<46}{'n':>5}{'p50 ms':>9}{'p95 ms':>9}{'max ms':>9}{'err':>5}{'null':>6}{'items':>7}")
        for m, d in sorted(rows.items(), key=lambda kv: -kv[1]["p95_ms"]):
            print(f"    {m:<44}{d['n']:>5}{d['p50_ms']:>9.2f}{d['p95_ms']:>9.2f}{d['max_ms']:>9.2f}"
                  f"{d['errors']:>5}{d['nulls']:>6}{d['avg_items']:>7}")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("binary")
    ap.add_argument("--files", default="500,1000,2000,4000",
                    help="synthetic workspace sizes, comma separated ('' for none); doubling shows the growth rate")
    ap.add_argument("--workspace", action="append", default=[], help="real tree to bench (read-only)")
    ap.add_argument("--label", help="results name (default: binary file name)")
    ap.add_argument("--open", type=int, default=20, help="files opened as editor tabs")
    ap.add_argument("--targets", type=int, default=60, help="symbol positions per request kind")
    ap.add_argument("--rounds", type=int, default=10, help="churn rounds")
    ap.add_argument("--watch", type=int, default=100, help="files in the watcher batch (<= 200)")
    ap.add_argument("--scan-timeout", type=float, default=600)
    args = ap.parse_args()
    binary = str(pathlib.Path(args.binary).resolve())
    label = args.label or pathlib.Path(binary).name
    results_dir = HERE / "results"
    results_dir.mkdir(exist_ok=True)
    meta = {"label": label, "binary": binary, "date": time.strftime("%Y-%m-%d %H:%M:%S"),
            "machine": {"cpus": os.cpu_count(), "kernel": platform.release(), "python": platform.python_version()},
            "runs": []}
    jobs = [(int(n), None) for n in args.files.split(",") if n.strip()] + [(None, w) for w in args.workspace]
    for n, real in jobs:
        tmp = None
        if real:
            root, name = pathlib.Path(real).resolve(), f"real-{pathlib.Path(real).resolve().name}"
        else:
            tmp = pathlib.Path(tempfile.mkdtemp(prefix="eulix-bench-"))
            root, name = tmp / "ws", f"synthetic-{n}"
            generate(root, n)
        try:
            r = run(binary, root, real is None, args, results_dir, f"{label}-{name}")
            meta["runs"].append(r)
            report(r)
        finally:
            if tmp:
                shutil.rmtree(tmp, ignore_errors=True)
        path = results_dir / f"{label}.json"
        path.write_text(json.dumps(meta, indent=2))  # after every run, so a crash keeps the rest
    # Growth exponent between neighbouring sizes: ~1 is linear, ~2 quadratic.
    syn = [(r["source_files"], r["scan_s"]) for r in meta["runs"] if "-synthetic-" in r["workspace"] and r["scan_s"]]
    for (n1, t1), (n2, t2) in zip(syn, syn[1:]):
        print(f"scan {n1} -> {n2} files: {t1} -> {t2} s, growth exponent {math.log(t2 / t1) / math.log(n2 / n1):.2f}")
    print(f"\nsaved {path}")


if __name__ == "__main__":
    sys.exit(main())
