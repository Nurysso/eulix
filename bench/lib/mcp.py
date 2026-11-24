# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""A minimal MCP client for `eulix mcp`: newline-delimited JSON-RPC over stdio.

Lifted from scripts/eval_mcp.py, plus per-call timing, timeouts, and a clean
kill of the whole process tree (etiya starts a Python embed server as a child).
"""
import json
import os
import re
import select
import signal
import subprocess
import time
from pathlib import Path

from . import proc
from .common import BenchError

# "[1] path:10-20 (score 0.90)" for retrieval results, "### path:10-20" for the grep fallback.
HEADER = re.compile(r"^(?:\[\d+\] |### )(\S+?):(\d+)-(\d+)(?: \(score ([\d.]+)\))?", re.M)
# slog quotes the value with strconv.Quote, so it may contain escaped quotes.
TRACE = re.compile(r'retrieval="((?:[^"\\]|\\.)*)"')
CONTEXT = re.compile(r"Context: (\d+) source chunks, (\d+) metadata, (\d+) tokens across (\d+) files")
ENGINE = re.compile(r"\[ENGINE\] (\w+) ready in")


def parse_trace(stderr: str) -> str | None:
    m = TRACE.findall(stderr)
    if not m:
        return None
    raw = m[-1]
    try:
        return json.loads(f'"{raw}"')
    except ValueError:
        return raw


class Server:
    def __init__(self, sandbox, repo: Path, stderr_path: Path):
        stderr_path.parent.mkdir(parents=True, exist_ok=True)
        self.stderr_path = stderr_path
        self._err = open(stderr_path, "w")
        self.t0 = time.monotonic()
        # query_without_cot is listed only with EULIX_MCP_LEGACY_TOOLS=1 since
        # 2026-11-24; older releases list it always and ignore the variable.
        env = {**sandbox.env, "EULIX_MCP_LEGACY_TOOLS": "1"}
        self.p = subprocess.Popen(sandbox.cmd("mcp", "--repo", repo, "--timeout", "2h"), cwd=repo,
                                  env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=self._err, start_new_session=True)
        self._buf = b""
        self._id = 0

    # --- transport ---------------------------------------------------------
    def _send(self, msg: dict) -> None:
        try:
            self.p.stdin.write((json.dumps(msg) + "\n").encode())
            self.p.stdin.flush()
        except BrokenPipeError:
            raise BenchError(f"eulix mcp exited early (code {self.p.poll()}); see {self.stderr_path}") from None

    def _readline(self, deadline: float) -> bytes:
        fd = self.p.stdout.fileno()
        while b"\n" not in self._buf:
            left = deadline - time.monotonic()
            if left <= 0:
                raise TimeoutError
            ready, _, _ = select.select([fd], [], [], left)
            if ready:
                chunk = os.read(fd, 1 << 16)
                if not chunk:
                    raise BenchError(f"eulix mcp closed stdout (exit {self.p.poll()}); see {self.stderr_path}")
                self._buf += chunk
        line, self._buf = self._buf.split(b"\n", 1)
        return line

    def request(self, method: str, params: dict, timeout: float):
        """Returns (result, seconds). Raises TimeoutError or BenchError."""
        self._id += 1
        rid = self._id
        t = time.monotonic()
        self._send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        deadline = t + timeout
        while True:
            line = self._readline(deadline)
            try:
                msg = json.loads(line)
            except ValueError:
                continue  # not protocol output; ignore
            if msg.get("id") == rid:
                dt = time.monotonic() - t
                if "error" in msg:
                    raise BenchError(f"{method} failed: {msg['error']}")
                return msg["result"], dt

    # --- protocol ----------------------------------------------------------
    def initialize(self, timeout: float = 60) -> float:
        _, dt = self.request("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                                            "clientInfo": {"name": "eulix-bench", "version": "1"}}, timeout)
        self._send({"jsonrpc": "2.0", "method": "notifications/initialized"})
        return dt

    def query(self, text: str, timeout: float):
        """Returns (answer text, seconds, is_error)."""
        result, dt = self.request("tools/call", {"name": "query_without_cot", "arguments": {"query": text}}, timeout)
        body = "".join(c.get("text", "") for c in result.get("content", []))
        return body, dt, bool(result.get("isError"))

    def tree_rss_mb(self) -> float:
        return proc.tree_rss_mb(self.p.pid)

    def own_rss_mb(self) -> float:
        return proc._rss_kb(self.p.pid)[1] / 1024

    def close(self) -> None:
        try:
            self.p.stdin.close()
        except OSError:
            pass
        try:
            self.p.wait(timeout=15)
        except subprocess.TimeoutExpired:
            pass
        try:  # leftovers such as etiya's embed server
            os.killpg(self.p.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        try:
            self.p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            pass
        self._err.close()

    def stderr(self) -> str:
        self._err.flush()
        return self.stderr_path.read_text(errors="replace")

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
