# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Turn what the user typed (tag, branch, SHA, `.`) into a commit and what it can do."""
import re
import subprocess
from dataclasses import dataclass

from .common import REPO, BenchError, run

MIN_SUPPORTED = "v0.8.3"


@dataclass(frozen=True)
class Rev:
    spec: str       # what the user typed
    sha: str        # full commit SHA (HEAD's SHA for a working-tree run)
    label: str      # tag if the commit has one, else the short SHA
    worktree: bool  # `.`: the uncommitted working tree, never cached

    @property
    def sha12(self) -> str:
        return "worktree" if self.worktree else self.sha[:12]


def git(*args, check=True) -> str:
    return run(["git", "-C", REPO, *args], check=check).stdout.strip()


def resolve(spec: str) -> Rev:
    if spec == ".":
        head = git("rev-parse", "HEAD")
        dirty = bool(git("status", "--porcelain"))
        return Rev(spec, head, f"worktree-{head[:8]}{'-dirty' if dirty else ''}", True)
    p = run(["git", "-C", REPO, "rev-parse", "--verify", "--quiet", f"{spec}^{{commit}}"], check=False)
    if p.returncode != 0:
        raise BenchError(f"unknown revision: {spec}")
    sha = p.stdout.strip()
    tag = run(["git", "-C", REPO, "describe", "--tags", "--exact-match", sha], check=False).stdout.strip()
    return Rev(spec, sha, tag or sha[:8], False)


def show(rev: Rev, path: str) -> str | None:
    """A file's content at that revision (the working tree for `.`), or None if absent."""
    if rev.worktree:
        f = REPO / path
        return f.read_text() if f.is_file() else None
    p = subprocess.run(["git", "-C", str(REPO), "show", f"{rev.sha}:{path}"], capture_output=True, text=True)
    return p.stdout if p.returncode == 0 else None


def capabilities(rev: Rev) -> dict:
    """What this version supports, read from its source so nothing has to run.

    mcp:   `eulix mcp` exists (v0.8.3 and later); the benchmark talks to eulix through it.
    ehnas: the EHNAS engine exists. Writing `engine = "ehnas"` for a version without
           it is silently ignored and would mislabel an etiya run.
    """
    mcp = show(rev, "eulix-cli/internal/cli/mcp.go") is not None
    if rev.worktree:
        grep = run(["git", "-C", REPO, "grep", "-q", "EngineEHNAS", "--", "eulix-cli"], check=False)
    else:
        grep = run(["git", "-C", REPO, "grep", "-q", "EngineEHNAS", rev.sha, "--", "eulix-cli"], check=False)
    engines = ["etiya"] + (["ehnas"] if grep.returncode == 0 else [])
    return {"mcp": mcp, "engines": engines}


def app_version(rev: Rev) -> str | None:
    src = show(rev, "eulix-cli/internal/utils/constansts.go") or ""
    m = re.search(r'AppVersion\s*=\s*"([^"]+)"', src)
    return m[1] if m else None
