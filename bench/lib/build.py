# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Build eulix at any supported commit, once, without touching the working tree.

The build runs `make build-linux-amd` in a detached git worktree outside the repo.
A worktree, not `git archive`: eulix-parser/build.rs runs `git rev-parse` and
stamps "unknown" outside a checkout, and that SHA ends up in kb.json metadata.
"""
import os
import shutil
import subprocess
import time
from datetime import datetime, timezone

from . import gitrev, paths
from .common import REPO, BenchError, read_json, run, say, sha256_file, write_json

# The Makefile hardcodes the cargo output path and joins ../$(BUILD_DIR), so
# these must not leak in from the user's shell.
UNSET_FOR_BUILD = ("CARGO_TARGET_DIR", "BUILD_DIR", "GPU", "OS_TARGET")
TOOLS = ("go", "cargo", "zip", "make", "uv")


def build_dir(rev: gitrev.Rev):
    return paths.sub("builds", "worktree" if rev.worktree else rev.sha)


def binary(rev: gitrev.Rev):
    return build_dir(rev) / "eulix"


def load(rev: gitrev.Rev) -> dict | None:
    f = build_dir(rev) / "build.json"
    if f.exists() and binary(rev).exists():
        return read_json(f)
    return None


def _check_tools():
    missing = [t for t in TOOLS if shutil.which(t) is None]
    if missing:
        raise BenchError(f"missing build tools: {', '.join(missing)}")


def _remove_worktree(src):
    run(["git", "-C", REPO, "worktree", "remove", "--force", src], check=False)
    shutil.rmtree(src, ignore_errors=True)
    run(["git", "-C", REPO, "worktree", "prune"], check=False)


def _copy_working_tree(src):
    """`.`: tracked plus untracked-but-not-ignored files, the same set a commit would hold."""
    shutil.rmtree(src, ignore_errors=True)
    src.mkdir(parents=True)
    files = run(["git", "-C", REPO, "ls-files", "-co", "--exclude-standard", "-z"]).stdout.split("\0")
    for rel in filter(None, files):
        s = REPO / rel
        if s.is_file():
            d = src / rel
            d.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(s, d)


def ensure(rev: gitrev.Rev, rebuild: bool = False) -> dict:
    """Return build.json for rev, building it first if needed."""
    if not rev.worktree and not rebuild:
        meta = load(rev)
        if meta:
            say(f"build {rev.label} ({rev.sha[:8]}): cached")
            return meta

    caps = gitrev.capabilities(rev)
    if not caps["mcp"]:
        raise BenchError(f"{rev.spec} has no `eulix mcp`; the oldest supported version is {gitrev.MIN_SUPPORTED}")
    _check_tools()

    out = build_dir(rev)
    out.mkdir(parents=True, exist_ok=True)
    src = paths.sub("builds", "worktree", "src") if rev.worktree else paths.sub("builds", "_src", rev.sha)
    log = out / "build.log"
    say(f"build {rev.label} ({rev.sha[:8]}): building, log at {log}")

    env = {k: v for k, v in os.environ.items() if k not in UNSET_FOR_BUILD}
    t0 = time.monotonic()
    try:
        if rev.worktree:
            _copy_working_tree(src)
        else:
            _remove_worktree(src)  # leftovers from an interrupted build
            src.parent.mkdir(parents=True, exist_ok=True)
            run(["git", "-C", REPO, "worktree", "add", "--detach", src, rev.sha])
        with open(log, "w") as f:
            p = subprocess.run(["make", "-C", str(src), "build-linux-amd"], env=env, stdout=f, stderr=subprocess.STDOUT)
        built = src / "build" / "eulix_linux_amd"
        # v0.8.3's Makefile does not stop on a failed `go build`, so check the file too.
        if p.returncode != 0 or not built.exists():
            raise BenchError(f"build of {rev.label} failed (exit {p.returncode}); see {log}")
        lock_drift = False
        if not rev.worktree:
            lock_drift = bool(run(["git", "-C", src, "status", "--porcelain", "--", "*Cargo.lock"]).stdout.strip())
        parser_bin = src / "eulix-cli" / "internal" / "assets" / "bins" / "eulix_parser_linux"
        tmp = out / ".eulix.tmp"
        shutil.copy2(built, tmp)
        os.chmod(tmp, 0o755)
        os.replace(tmp, binary(rev))
        meta = {
            "sha": rev.sha,
            "label": rev.label,
            "worktree": rev.worktree,
            "app_version": gitrev.app_version(rev),
            "capabilities": caps,
            "parser_sha256": sha256_file(parser_bin),
            "binary_sha256": sha256_file(binary(rev)),
            "embed_requirements": gitrev.show(rev, "eulix-embed/requiremenents/requirements.base.txt"),
            "go_version": run(["go", "version"]).stdout.strip(),
            "rustc_version": run(["rustc", "--version"]).stdout.strip(),
            "lock_drift": lock_drift,
            "built_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
            "build_seconds": round(time.monotonic() - t0, 1),
        }
        write_json(out / "build.json", meta)
    finally:
        if not rev.worktree:
            _remove_worktree(src)
    say(f"build {rev.label}: done in {meta['build_seconds']}s")
    return meta
