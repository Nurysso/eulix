# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""One side of a benchmark: a built version, its sandbox, and what identifies its results."""
import json
import time
from datetime import datetime

from . import build, corpora, machine, paths, sandbox, venv
from .common import REAL_HOME, REPO, H, BenchError, run, say
from .gitrev import Rev

GPU_PROVIDERS = ("ROCMExecutionProvider", "CUDAExecutionProvider", "MIGraphXExecutionProvider",
                 "CoreMLExecutionProvider")
REAL_VENV = REAL_HOME / ".Eulix" / ".venv"


def resolve_device(force_cpu: bool) -> str:
    """GPU by default, through the user's own eulix venv. CPU is the fallback: with
    --cpu, or when that venv has no GPU execution provider."""
    if force_cpu:
        return "cpu"
    py = venv.python(REAL_VENV)
    if not py.exists():
        say(f"no eulix venv at {REAL_VENV}; falling back to CPU")
        return "cpu"
    p = run([py, "-c", "import onnxruntime as o; print(' '.join(o.get_available_providers()))"], check=False)
    if not any(g in p.stdout.split() for g in GPU_PROVIDERS):
        say(f"{REAL_VENV} offers no GPU provider ({p.stdout.strip() or p.stderr.strip()[:80]}); falling back to CPU")
        return "cpu"
    return "gpu"


class Side:
    def __init__(self, rev: Rev, device: str = "cpu", rebuild: bool = False):
        self.rev, self.device = rev, device
        self.meta = build.ensure(rev, rebuild=rebuild)
        cpu_venv = venv.ensure_cpu(self.meta.get("embed_requirements") or "")
        self.seed = venv.seed_model(cpu_venv, sandbox.hf_env())
        if device == "gpu":
            venv_path = REAL_VENV
            if not venv.python(venv_path).exists():
                raise BenchError(f"a GPU run needs your eulix venv at {venv_path}")
            freeze = run(["uv", "pip", "freeze", "--python", venv.python(venv_path)]).stdout
            self.env_info = {"device": "gpu", "venv_hash": H(freeze)[:16], "gpus": machine.gpus(),
                             "model": self.seed["model"], "model_revision": self.seed["revision"]}
        else:
            venv_path = cpu_venv
            self.env_info = {"device": "cpu", "venv_hash": venv.freeze_hash(cpu_venv),
                             "onnxruntime": venv.package_version(cpu_venv, "onnxruntime"),
                             "model": self.seed["model"], "model_revision": self.seed["revision"],
                             "onnx_sha256": self.seed["onnx_sha256"]}
        self.env_hash = H(self.env_info)[:16]
        self.sandbox = sandbox.Sandbox(rev, self.meta, venv_path, device)
        self.machine = machine.info()
        self.machine_fp = machine.fingerprint(gpu=device == "gpu")
        self.machine["fingerprint"] = self.machine_fp
        self.run_id = datetime.now().strftime("%Y%m%d-%H%M%S")
        self.log_dir = paths.sub("logs", self.run_id, f"{rev.sha12}-{device}")
        self.worktree_docs: dict = {}
        bench_commit = run(["git", "-C", REPO, "rev-parse", "HEAD"]).stdout.strip()
        dirty = bool(run(["git", "-C", REPO, "status", "--porcelain", "--", "bench"]).stdout.strip())
        self.harness = {"bench_commit": bench_commit, "dirty": dirty}
        self._cpu_twin = None

    def accuracy_side(self) -> "Side":
        """Retrieval accuracy always runs on a CPU-built index: ROCm vectors are wrong
        (EMB-1), so GPU-built accuracy would depend on the machine."""
        if self.device == "cpu":
            return self
        if self._cpu_twin is None:
            self._cpu_twin = Side(self.rev, "cpu")
        return self._cpu_twin

    @property
    def engines(self) -> list[str]:
        return self.meta["capabilities"]["engines"]

    def log(self, name: str):
        return self.log_dir / f"{name}.log"


def fixture_marker(work):
    return work / ".bench-fixture.json"


def has_fixture(side: Side, corpus, work) -> bool:
    """An index built by this exact binary and config is still in the work dir."""
    m = fixture_marker(work)
    if not m.exists() or not (work / ".eulix" / "kb.json").exists():
        return False
    data = json.loads(m.read_text())
    return data.get("binary_sha256") == side.meta["binary_sha256"] and data.get("config_hash") == \
        corpora.config_hash(corpus) and data.get("env_hash") == side.env_hash


COUNT_KEYS = ("files_clean", "files_partial", "files_failed")


def mark_fixture(side: Side, corpus, work, parsed: dict) -> None:
    fixture_marker(work).write_text(json.dumps({
        "binary_sha256": side.meta["binary_sha256"], "config_hash": corpora.config_hash(corpus),
        "env_hash": side.env_hash, "created": time.time(), "counts": {k: parsed.get(k) for k in COUNT_KEYS}}))


def fixture_counts(work) -> dict:
    """Parse counts (clean/partial/failed files) the parser printed when the fixture was built."""
    return json.loads(fixture_marker(work).read_text()).get("counts", {})


def ensure_fixture(side: Side, corpus):
    """Return the work dir with an index in it, indexing once (untimed) if needed."""
    from .groups import indexing  # late import: indexing imports this module
    work = corpora.ensure_work(corpus, side.rev.sha12, side.device)
    if not has_fixture(side, corpus, work):
        say(f"  {corpus.label}: building index for {side.rev.label}")
        _m, parsed = indexing.index_once(side, corpus, work, side.log(f"fixture-{corpus.name}"))
        mark_fixture(side, corpus, work, parsed)
    return work
