"""Reference tools, one per language. Each writes call-site JSON lines (see score.Site)."""

from __future__ import annotations

import hashlib
import os
import shutil
import subprocess
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent  # bench/callgraph
REFS = HERE / "refs"

# lang -> (tool directory, human name)
TOOLS = {
    "go": ("go", "go/types via golang.org/x/tools/go/packages (CHA for interfaces)"),
    "java": ("java", "javac attribution (JavacTask, CHA for overrides)"),
    "python": ("python", "jedi 0.19.2 goto/infer"),
    "rust": ("rust", "rust-analyzer LSIF"),
    "ts": ("ts", "TypeScript 5.9.3 checker (CHA for overrides)"),
    "js": ("ts", "TypeScript 5.9.3 checker, allowJs (CHA for overrides)"),
    "c": ("clang", "libclang (system)"),
    "cpp": ("clang", "libclang (system, CHA for virtuals)"),
}


def tool_hash(lang: str) -> str:
    d = REFS / TOOLS[lang][0]
    h = hashlib.sha256(lang.encode())
    for p in sorted(d.rglob("*")):
        if p.is_file() and "node_modules" not in p.parts and p.suffix in (".go", ".mod", ".sum", ".java", ".py", ".mjs", ".json"):
            h.update(p.name.encode())
            h.update(p.read_bytes())
    return h.hexdigest()[:12]


def _ensure_go(home: Path) -> Path:
    exe = home / "bin" / f"go-ref-{tool_hash('go')}"
    if not exe.exists():
        exe.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["go", "build", "-o", str(exe), "."], cwd=REFS / "go", check=True)
    return exe


def _ensure_ts() -> None:
    if not (REFS / "ts" / "node_modules" / "typescript" / "package.json").exists():
        subprocess.run(["npm", "ci", "--no-audit", "--no-fund"], cwd=REFS / "ts", check=True, timeout=600)


def command(lang: str, root: Path, home: Path, ref_args: list[str]) -> tuple[list[str], dict]:
    env = dict(os.environ)
    if lang == "go":
        return [str(_ensure_go(home)), str(root), *ref_args], env
    if lang == "java":
        return ["java", "-Xmx4g", str(REFS / "java" / "RefCalls.java"), str(root), *ref_args], env
    if lang == "python":
        return ["uv", "run", "--no-project", "--python", "3.12", "--with", "jedi==0.19.2",
                "python", "-I", str(REFS / "python" / "refcalls.py"), str(root), *ref_args], env
    if lang == "rust":
        env["CARGO_TARGET_DIR"] = str(home / "cargo-target")
        return ["python3", "-I", str(REFS / "rust" / "refcalls.py"), str(root), *ref_args], env
    if lang in ("ts", "js"):
        _ensure_ts()
        return ["node", str(REFS / "ts" / "refcalls.mjs"), str(root), *ref_args], env
    if lang in ("c", "cpp"):
        # -I keeps the system clang bindings importable while ignoring the cwd
        return ["python3", "-I", str(REFS / "clang" / "refcalls.py"), str(root),
                "--lang", "c" if lang == "c" else "c++", *(["--", *ref_args] if ref_args else [])], env
    raise ValueError(f"no reference tool for {lang}")


def run(lang: str, root: Path, out: Path, home: Path, ref_args: list[str], log) -> dict:
    """Run the reference on a private copy of root (tools may write go.sum, Cargo.lock...)."""
    work = out.parent / (out.stem + "-root")
    if work.exists():
        shutil.rmtree(work)
    shutil.copytree(root, work, symlinks=True)
    cmd, env = command(lang, work, home, ref_args)
    log(f"  reference: {TOOLS[lang][1]}")
    t0 = time.perf_counter()
    tmp = out.with_suffix(".tmp")
    with open(tmp, "w") as fh, open(out.with_suffix(".log"), "w") as err:
        p = subprocess.run(cmd, stdout=fh, stderr=err, env=env, cwd=work)
    secs = time.perf_counter() - t0
    shutil.rmtree(work, ignore_errors=True)
    if p.returncode != 0:
        raise RuntimeError(f"reference tool for {lang} exited {p.returncode}; see {out.with_suffix('.log')}")
    tmp.replace(out)
    return {"seconds": round(secs, 2), "tool": TOOLS[lang][1], "cmd": cmd}
