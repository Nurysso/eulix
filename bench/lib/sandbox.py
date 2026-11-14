# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Run a given eulix build with its own HOME, so it never touches the user's install.

Every eulix binary unpacks its parser and Python scripts into $HOME/.Eulix at
start-up and exits when the parser there belongs to another build
(assets.VerifyOrExtract). Pointing HOME at a per-version folder keeps versions
apart. `.Eulix/.initialized` is pre-written so start-up skips the uv/venv checks
and `uv pip install`, and `.Eulix/.venv` links to the benchmark's venv.
"""
import json
import os
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

from . import build, paths, venv
from .common import REAL_HOME, BenchError, sha256_file
from .gitrev import Rev

UNSET = ("debug", "EULIX_MMR_WATCH", "EULIX_FIELD_REPORT", "VIRTUAL_ENV", "PYTHONPATH", "PYTHONHOME",
         "CONDA_PREFIX")
GPU_VARS = ("CUDA_VISIBLE_DEVICES", "HIP_VISIBLE_DEVICES", "ROCR_VISIBLE_DEVICES")


def home_dir(rev: Rev, device: str) -> Path:
    return paths.sub("homes", rev.sha12 + ("-gpu" if device == "gpu" else ""))


def real_hf_home() -> str:
    return os.environ.get("HF_HOME") or str(REAL_HOME / ".cache" / "huggingface")


def hf_env() -> dict:
    """Env for the harness's own Hugging Face calls (seeding), with the real caches."""
    return {**os.environ, "HF_HOME": real_hf_home(), "HF_HUB_DISABLE_TELEMETRY": "1"}


def prepare(rev: Rev, meta: dict, venv_path: Path, device: str = "cpu") -> Path:
    home = home_dir(rev, device)
    dot = home / ".Eulix"
    marker = home / ".bench-binary"
    # A rebuilt binary (or any worktree run) finds the old parser here and exits, so start clean.
    if rev.worktree or not marker.exists() or marker.read_text().strip() != meta["binary_sha256"]:
        for d in ("bin", "eulix_embed"):
            shutil.rmtree(dot / d, ignore_errors=True)
    dot.mkdir(parents=True, exist_ok=True)
    link = dot / ".venv"
    if link.is_symlink() or link.exists():
        if link.is_symlink() and Path(os.readlink(link)) == venv_path:
            pass
        elif link.is_symlink():
            link.unlink()
        else:
            raise BenchError(f"{link} is a real directory; remove it (the sandbox expects a symlink)")
    if not link.is_symlink():
        link.symlink_to(venv_path)
    init = dot / ".initialized"
    if not init.exists():
        init.write_text(json.dumps({"app_name": "eulix", "version": meta.get("app_version"),
                                    "installed_at": datetime.now(timezone.utc).isoformat()}))
    marker.write_text(meta["binary_sha256"] + "\n")
    return home


def env(home: Path, device: str = "cpu") -> dict:
    e = {k: v for k, v in os.environ.items() if k not in UNSET and not k.startswith("EULIX_BENCH")}
    e.update({
        "HOME": str(home),
        "HF_HOME": real_hf_home(),
        "HF_HUB_OFFLINE": "1",
        "HF_HUB_DISABLE_TELEMETRY": "1",
        "EULIX_ONNX_CACHE": str(venv.onnx_cache()),
        "XDG_CACHE_HOME": str(paths.sub("cache", "xdg")),
        "PYTHONUNBUFFERED": "1",
        "NO_COLOR": "1",
    })
    if device == "cpu":
        e.update({v: "" for v in GPU_VARS})
    return e


class InstallGuard:
    """Abort loudly if anything touched the user's real ~/.Eulix parser while we ran."""

    def __init__(self):
        self.path = REAL_HOME / ".Eulix" / "bin" / "eulix_parser"

    def _hash(self):
        return sha256_file(self.path) if self.path.exists() else None

    def __enter__(self):
        self.before = self._hash()
        return self

    def __exit__(self, *exc):
        if self._hash() != self.before:
            raise BenchError(f"{self.path} changed during the benchmark; the sandbox leaked. Reinstall eulix.")


class Sandbox:
    """Everything needed to run one build: binary, HOME, env."""

    def __init__(self, rev: Rev, meta: dict, venv_path: Path, device: str = "cpu"):
        self.rev, self.meta, self.device = rev, meta, device
        self.binary = build.binary(rev)
        self.home = prepare(rev, meta, venv_path, device)
        self.env = env(self.home, device)

    def cmd(self, *args) -> list[str]:
        return [str(self.binary), *map(str, args)]

    def run(self, *args, cwd=None, **kw) -> subprocess.CompletedProcess:
        return subprocess.run(self.cmd(*args), cwd=cwd, env=self.env, **kw)
