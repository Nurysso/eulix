"""Load corpora.toml and materialise a snapshot directory per corpus."""

from __future__ import annotations

import shutil
import subprocess
import tomllib
from pathlib import Path

from .refs import HERE


def load(path: Path | None = None) -> list[dict]:
    data = tomllib.loads((path or HERE / "corpora.toml").read_text())
    out = []
    for c in data["corpus"]:
        c.setdefault("subdir", "")
        c.setdefault("exclude", [])
        c.setdefault("ref_args", [])
        out.append(c)
    return out


def key(c: dict) -> str:
    if c["source"] == "micro":
        return c["name"]
    return f'{c["name"]}-{c["commit"][:12]}'


def snapshot(c: dict, home: Path, log) -> Path:
    """Directory both eulix and the reference analyse."""
    if c["source"] == "micro":
        dst = home / "corpora" / c["name"]
        if dst.exists():
            shutil.rmtree(dst)
        shutil.copytree(HERE / c["path"], dst)
        return dst
    dst = home / "corpora" / key(c)
    ready = dst / ".cg-ready"
    if not ready.exists():
        if dst.exists():
            shutil.rmtree(dst)
        dst.mkdir(parents=True)
        log(f"  fetching {c['source']} @ {c['commit'][:12]}")
        git = lambda *a: subprocess.run(["git", "-C", str(dst), *a], check=True, capture_output=True)
        git("init", "-q")
        git("remote", "add", "origin", c["source"])
        git("fetch", "-q", "--depth", "1", "origin", c["commit"])
        git("checkout", "-q", "FETCH_HEAD")
        shutil.rmtree(dst / ".git")
        for ex in c["exclude"]:
            p = dst / ex
            if p.is_dir():
                shutil.rmtree(p)
            elif p.exists():
                p.unlink()
        ready.write_text(c["commit"] + "\n")
    root = dst / c["subdir"] if c["subdir"] else dst
    if not root.is_dir():
        raise RuntimeError(f"{c['name']}: subdir {c['subdir']!r} not in the checkout")
    return root
