#!/usr/bin/env python3
# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Run the checks from .github/workflows/Docs.yaml locally.

  dead links  lychee over docs/**/*.md: relative links, #anchors, external URLs
  spelling    codespell in docs/ (config: docs/.codespellrc)

Usage: scripts/check_docs.py [--links | --spelling] [--offline]

  --links     only the dead-link check
  --spelling  only the spelling check
  --offline   skip external URLs (fast; still checks files and #anchors)

Needs lychee (`pacman -S lychee`, `brew install lychee` or `cargo install lychee`)
and codespell or uv (`uvx codespell`, as CI runs it). For external GitHub links,
lychee uses $GITHUB_TOKEN, or `gh auth token` if gh is logged in, to avoid rate limits.
Exit status 1 if any check fails.
"""

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOCS = ROOT / "docs"


def run(name: str, cmd: list[str], cwd: Path, env: dict | None = None) -> bool:
    print(f"\n== {name}: {' '.join(cmd)}", flush=True)
    ok = subprocess.run(cmd, cwd=cwd, env=env).returncode == 0
    print(f"== {name}: {'ok' if ok else 'FAILED'}", flush=True)
    return ok


def links(offline: bool) -> bool:
    lychee = shutil.which("lychee")
    if not lychee:
        print("lychee not found: install it with `pacman -S lychee`, `brew install lychee` "
              "or `cargo install lychee`", file=sys.stderr)
        return False
    # Same arguments as Docs.yaml. lychee expands the quoted glob itself.
    cmd = [lychee, "--no-progress", "--include-fragments", "--root-dir", str(DOCS),
           "--max-retries", "3", "--accept", "200,202,206,429",
           "--exclude-path", "docs/_build", "docs/**/*.md"]
    if offline:
        cmd.insert(1, "--offline")
    env = dict(os.environ)
    if not env.get("GITHUB_TOKEN") and not offline and shutil.which("gh"):
        token = subprocess.run(["gh", "auth", "token"], capture_output=True, text=True)
        if token.returncode == 0 and token.stdout.strip():
            env["GITHUB_TOKEN"] = token.stdout.strip()
    return run("dead links", cmd, ROOT, env)


def spelling() -> bool:
    if shutil.which("uvx"):
        cmd = ["uvx", "codespell"]
    elif shutil.which("codespell"):
        cmd = ["codespell"]
    else:
        print("codespell not found: install uv (CI uses `uvx codespell`) or codespell",
              file=sys.stderr)
        return False
    return run("spelling", cmd, DOCS)


def main() -> int:
    p = argparse.ArgumentParser(description="Run the Docs workflow checks locally.")
    only = p.add_mutually_exclusive_group()
    only.add_argument("--links", action="store_true", help="only the dead-link check")
    only.add_argument("--spelling", action="store_true", help="only the spelling check")
    p.add_argument("--offline", action="store_true", help="skip external URLs")
    a = p.parse_args()

    results = {}
    if not a.spelling:
        results["dead links"] = links(a.offline)
    if not a.links:
        results["spelling"] = spelling()

    print("\nSummary: " + ", ".join(f"{k}: {'ok' if v else 'FAILED'}" for k, v in results.items()))
    return 0 if all(results.values()) else 1


if __name__ == "__main__":
    sys.exit(main())
