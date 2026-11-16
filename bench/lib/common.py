# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Small helpers shared by every part of the benchmark."""
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
BENCH = REPO / "bench"
SUITE = BENCH / "suite"
# Captured before any sandbox env exists: the harness itself always runs with the real HOME.
REAL_HOME = Path.home()


class BenchError(Exception):
    """A problem the user has to fix. Printed without a traceback; exit code 2."""


def H(obj) -> str:
    """sha256 of the canonical JSON of obj: the basis of every cache key."""
    return hashlib.sha256(json.dumps(obj, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def sha256_file(path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def write_json(path, obj) -> None:
    """Write atomically, so a crash never leaves half a result behind."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f".{path.name}.tmp-{os.getpid()}")
    tmp.write_text(json.dumps(obj, indent=1) + "\n")
    os.replace(tmp, path)


def read_json(path):
    return json.loads(Path(path).read_text())


def say(msg: str) -> None:
    """Progress goes to stderr so stdout stays clean for tables."""
    print(msg, file=sys.stderr, flush=True)


def run(cmd, cwd=None, env=None, check=True, capture=True) -> subprocess.CompletedProcess:
    p = subprocess.run([str(c) for c in cmd], cwd=cwd, env=env, text=True,
                       stdout=subprocess.PIPE if capture else None,
                       stderr=subprocess.PIPE if capture else None)
    if check and p.returncode != 0:
        detail = (p.stderr or p.stdout or "").strip().splitlines()[-5:] if capture else []
        raise BenchError(f"command failed ({p.returncode}): {' '.join(map(str, cmd))}\n  " + "\n  ".join(detail))
    return p
