# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Run a command and measure wall time and memory of its whole process tree.

`time -v` on eulix would only see the Go process; the parser and the Python
embedder run as children. A thread samples /proc every 100 ms, sums VmRSS over
the tree and keeps a peak per command name. os.wait4 adds ru_maxrss, the largest
single process the kernel saw (it covers spikes shorter than a sample).
"""
import os
import signal
import subprocess
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path

INTERVAL = 0.1


def _children_map() -> dict[int, list[int]]:
    kids: dict[int, list[int]] = {}
    for d in os.listdir("/proc"):
        if not d.isdigit():
            continue
        try:
            with open(f"/proc/{d}/stat") as f:
                ppid = int(f.read().rsplit(")", 1)[1].split()[1])
        except (OSError, IndexError, ValueError):
            continue
        kids.setdefault(ppid, []).append(int(d))
    return kids


def _tree(root: int) -> list[int]:
    kids = _children_map()
    out, todo = [], [root]
    while todo:
        pid = todo.pop()
        out.append(pid)
        todo.extend(kids.get(pid, ()))
    return out


def _rss_kb(pid: int) -> tuple[str, int]:
    """(comm, VmRSS in kB) or ("", 0) when the process is gone."""
    try:
        comm = Path(f"/proc/{pid}/comm").read_text().strip()
        with open(f"/proc/{pid}/status") as f:
            for line in f:
                if line.startswith("VmRSS:"):
                    return comm, int(line.split()[1])
    except OSError:
        pass
    return "", 0


def tree_rss_mb(root: int) -> float:
    return sum(_rss_kb(p)[1] for p in _tree(root)) / 1024


@dataclass
class Measured:
    returncode: int
    wall_s: float
    peak_tree_mb: float
    peak_by_comm_mb: dict = field(default_factory=dict)
    maxrss_single_mb: float = 0.0
    oom_guard: bool = False
    timed_out: bool = False


class Sampler(threading.Thread):
    def __init__(self, root: int, mem_limit_mb: float | None = None, on_limit=None):
        super().__init__(daemon=True)
        self.root, self.limit, self.on_limit = root, mem_limit_mb, on_limit
        self.peak = 0.0
        self.by_comm: dict[str, float] = {}
        self.tripped = False
        self._stop = threading.Event()

    def run(self):
        while not self._stop.is_set():
            total, comms = 0, {}
            for pid in _tree(self.root):
                comm, kb = _rss_kb(pid)
                if comm:
                    total += kb
                    comms[comm] = comms.get(comm, 0) + kb
            mb = total / 1024
            self.peak = max(self.peak, mb)
            for c, kb in comms.items():
                self.by_comm[c] = max(self.by_comm.get(c, 0.0), kb / 1024)
            if self.limit and mb > self.limit and not self.tripped:
                self.tripped = True
                if self.on_limit:
                    self.on_limit()
            self._stop.wait(INTERVAL)

    def stop(self):
        self._stop.set()
        self.join()


def run_measured(cmd, cwd, env, log_path, timeout: float | None = None,
                 mem_limit_mb: float | None = None) -> Measured:
    """Run cmd with stdout+stderr to log_path. Kills the whole tree on timeout or OOM guard."""
    log_path = Path(log_path)
    log_path.parent.mkdir(parents=True, exist_ok=True)
    with open(log_path, "w") as log:
        t0 = time.monotonic()
        p = subprocess.Popen([str(c) for c in cmd], cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT,
                             stdin=subprocess.DEVNULL, start_new_session=True)

        def kill():
            try:
                os.killpg(p.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass

        s = Sampler(p.pid, mem_limit_mb, kill)
        s.start()
        timed_out = False
        while True:
            pid, status, ru = os.wait4(p.pid, os.WNOHANG)
            if pid:
                break
            if timeout and time.monotonic() - t0 > timeout:
                timed_out = True
                kill()
            time.sleep(0.05)
        wall = time.monotonic() - t0
        s.stop()
    p.returncode = os.waitstatus_to_exitcode(status)
    return Measured(returncode=p.returncode, wall_s=round(wall, 3), peak_tree_mb=round(s.peak, 1),
                    peak_by_comm_mb={k: round(v, 1) for k, v in s.by_comm.items()},
                    maxrss_single_mb=round(ru.ru_maxrss / 1024, 1), oom_guard=s.tripped, timed_out=timed_out)
