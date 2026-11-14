# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""The CPU-only Python environment every sandbox links to, and the embedding model seed.

Why CPU only: on ROCm the ONNX embedder can produce wrong vectors (EMB-1), and it
picks the GPU by itself both when indexing and when embedding a question. An
environment that only has the CPU build of onnxruntime cannot pick a GPU at all.

Why seed the model: on every start the embedder asks the Hugging Face Hub which
files the model has, unless `$EULIX_ONNX_CACHE/<model>/model.onnx` exists. Seeding
that file lets every run go offline (HF_HUB_OFFLINE=1), so network jitter never
shows up in a timing.
"""
import re
import shutil
from pathlib import Path

from . import paths
from .common import SUITE, H, BenchError, read_json, run, say, sha256_file, write_json

PYTHON = "3.11"  # eulix accepts 3.10 or 3.11 only (embeddings/venv.go, eulix-embed/main.py)
GPU_PROVIDERS = ("ROCMExecutionProvider", "CUDAExecutionProvider", "MIGraphXExecutionProvider")
MODEL = "BAAI/bge-base-en-v1.5"


def _normalize(text: str) -> str:
    lines = (ln.split("#", 1)[0].strip() for ln in text.splitlines())
    return "\n".join(sorted(ln for ln in lines if ln))


def overlay() -> str:
    return (SUITE / "venv-cpu.txt").read_text()


def key(base_requirements: str) -> str:
    return H({"base": _normalize(base_requirements), "overlay": _normalize(overlay()), "python": PYTHON})[:16]


def python(venv: Path) -> Path:
    return venv / "bin" / "python"


def ensure_cpu(base_requirements: str) -> Path:
    """Create (once) the CPU venv for this set of embed requirements and return its path."""
    venv = paths.sub("venvs", f"cpu-{key(base_requirements)}")
    if (venv / ".complete").exists():
        return venv
    say(f"venv: creating {venv.name} (Python {PYTHON}, CPU onnxruntime)")
    shutil.rmtree(venv, ignore_errors=True)
    venv.parent.mkdir(parents=True, exist_ok=True)
    base = venv.parent / f"{venv.name}.base.txt"
    base.write_text(base_requirements)
    run(["uv", "venv", "--quiet", "--python", PYTHON, venv])
    run(["uv", "pip", "install", "--quiet", "--python", python(venv), "-r", base, "-r", SUITE / "venv-cpu.txt"])
    (venv / "freeze.txt").write_text(run(["uv", "pip", "freeze", "--python", python(venv)]).stdout)
    providers = run([python(venv), "-c", "import onnxruntime as o; print(' '.join(o.get_available_providers()))"]).stdout.split()
    bad = [p for p in providers if p in GPU_PROVIDERS]
    if bad:
        raise BenchError(f"CPU venv {venv} offers GPU providers {bad}; it must hold the CPU onnxruntime only")
    (venv / ".complete").write_text("")
    return venv


def onnx_cache() -> Path:
    return paths.sub("cache", "onnx")


def seed_model(venv: Path, hf_env: dict, model: str = MODEL) -> dict:
    """Make the model available offline. Returns {model, revision, onnx_sha256}."""
    dest_dir = onnx_cache() / re.sub(r"[^A-Za-z0-9_.-]+", "_", model)  # same naming as onnx_embed.py
    meta_file = dest_dir / "seed.json"
    if meta_file.exists() and (dest_dir / "model.onnx").exists():
        return read_json(meta_file)
    say(f"model: seeding {model} for offline use")
    code = ("import sys; from huggingface_hub import hf_hub_download as d; "
            "print(d(sys.argv[1], 'onnx/model.onnx')); print(d(sys.argv[1], 'tokenizer.json'))")
    env = {**hf_env, "HF_HUB_OFFLINE": "0"}
    out = run([python(venv), "-c", code, model], env=env).stdout.split()
    onnx = Path(out[0]).resolve()
    m = re.search(r"/snapshots/([0-9a-f]+)/", out[0])
    dest_dir.mkdir(parents=True, exist_ok=True)
    tmp = dest_dir / ".model.onnx.tmp"
    shutil.copyfile(onnx, tmp)
    tmp.replace(dest_dir / "model.onnx")
    meta = {"model": model, "revision": m[1] if m else None, "onnx_sha256": sha256_file(dest_dir / "model.onnx")}
    write_json(meta_file, meta)
    return meta


def freeze_hash(venv: Path) -> str:
    return H((venv / "freeze.txt").read_text())[:16]


def package_version(venv: Path, name: str) -> str | None:
    for line in (venv / "freeze.txt").read_text().splitlines():
        if line.lower().startswith(f"{name.lower()}=="):
            return line.split("==", 1)[1]
    return None
