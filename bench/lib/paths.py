# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Layout of the benchmark's state directory and the lock that guards it.

    ~/.Eulix/bench/            (EULIX_BENCH_HOME overrides)
      builds/<sha>/            eulix binary + build.json, built once per commit
      builds/_src/<sha>/       transient git worktree used while building
      homes/<sha12>[-gpu]/     sandboxed HOME each version runs with
      venvs/cpu-<key>/         CPU-only Python env shared by all sandboxes
      cache/onnx/, cache/xdg/  model and misc caches shared by all sandboxes
      corpora/<name>-…/        frozen, read-only corpus snapshots
      work/<sha12>/<corpus>/   per-version copy of a corpus with its .eulix index
      results/<group>/<sha12>/ one JSON document per measured cell
      reports/                 Markdown reports written by `compare`
      logs/<run-id>/           raw output of every command the harness ran
"""
import fcntl
import os
import sys
from pathlib import Path

from .common import REAL_HOME, BenchError


def home() -> Path:
    return Path(os.environ.get("EULIX_BENCH_HOME") or REAL_HOME / ".Eulix" / "bench")


def sub(*parts) -> Path:
    return home().joinpath(*parts)


class Lock:
    """One benchmark at a time: two runs would fight over CPU and skew each other's timings."""

    def __enter__(self):
        path = sub(".lock")
        path.parent.mkdir(parents=True, exist_ok=True)
        self.f = open(path, "a+")
        try:
            fcntl.flock(self.f, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            self.f.seek(0)
            holder = self.f.read().strip() or "unknown process"
            self.f.close()
            raise BenchError(f"another benchmark is running ({holder}). The lock is released when it exits.")
        self.f.seek(0)
        self.f.truncate()
        self.f.write(f"pid {os.getpid()}: {' '.join(sys.argv)}\n")
        self.f.flush()
        return self

    def __exit__(self, *exc):
        self.f.close()
