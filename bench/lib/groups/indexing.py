# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Indexing speed and memory: cold `eulix analyze` runs on a frozen corpus.

Each repeat deletes .eulix/ first, so both versions index from nothing (v0.8.3
skips analyze when its checksum is unchanged, HEAD also keeps a parse cache).
Corpora with embed = false run the parser alone with analyze's exact flags.
"""
import re
import subprocess

from .. import corpora, kbfb, machine, proc, runner, sandbox, store
from ..common import BenchError, say

METHOD_VERSION = 1
GPU_PROVIDERS = ("ROCMExecutionProvider", "CUDAExecutionProvider", "MIGraphXExecutionProvider",
                 "CoreMLExecutionProvider")
OOM_FRACTION = 0.85


def _num(pattern, text, cast=float, last=False):
    found = re.findall(pattern, text, re.M)
    if not found:
        return None
    return cast(found[-1] if last else found[0])


def parse_output(text: str) -> dict:
    """Best effort: a line a version doesn't print becomes None, never an error."""
    out = {
        "parse_s": _num(r"^\s*Total:\s+([\d.]+)s", text),
        "parser_self_peak_mb": _num(r"Peak Rss:\s+([\d.]+) MB", text),
        "files_clean": _num(r"^\s*Clean:\s+(\d+) files", text, int),
        "files_partial": _num(r"^\s*Partial:\s+(\d+) files", text, int),
        "files_failed": _num(r"^\s*Failed:\s+(\d+) files", text, int),
        "embed_s": _num(r"^\s*Total Time:\s+([\d.]+)s", text, last=True),
        "chunks": _num(r"^\s*Total Chunks:\s+(\d+)", text, int, last=True),
        "chunks_per_s": _num(r"Average:\s+([\d.]+) chunks/sec", text, last=True),
        "providers": sorted(set(re.findall(r"Provider:\s+(\w+)", text))),
    }
    m = re.search(r"Parsed (\d+) files \((\d+) clean, (\d+) partial, (\d+) failed; (\d+) LOC\) in ([\d.]+)s", text)
    if m:  # non-verbose summary line
        out["files_clean"] = out["files_clean"] if out["files_clean"] is not None else int(m[2])
        out["files_partial"] = out["files_partial"] if out["files_partial"] is not None else int(m[3])
        out["files_failed"] = out["files_failed"] if out["files_failed"] is not None else int(m[4])
        out["parse_s"] = out["parse_s"] if out["parse_s"] is not None else float(m[6])
    return out


def check_device(text: str, device: str, parsed: dict) -> None:
    providers = parsed["providers"]
    if device == "cpu":
        if not providers:
            raise BenchError("the embedder printed no `Provider:` line, so the CPU run can't be confirmed")
        if any(p != "CPUExecutionProvider" for p in providers) or "GPU detected" in text:
            raise BenchError(f"a CPU run used {providers}; the GPU must not be reachable (EMB-1)")
    elif not any(p in GPU_PROVIDERS for p in providers):
        raise BenchError(f"a --gpu run used {providers or 'no provider'}, not a GPU")


def parser_cmd(side, work, out) -> list[str]:
    """The parser step as `eulix analyze` runs it (analyze.go), writing to `out`.

    Parsers that write FlatBuffers also get --emit-json, so the determinism check
    can hash the JSON copies of outputs that embed run-specific metadata."""
    args = ["parser", "--root", work, "-o", out, "--threads", "8", "--prism", "2", "--verbose"]
    if _supports_emit_json(side):
        args.append("--emit-json")
    return side.sandbox.cmd(*args)


_EMIT_JSON_SUPPORT: dict[str, bool] = {}


def _supports_emit_json(side) -> bool:
    key = str(side.sandbox.binary)
    if key not in _EMIT_JSON_SUPPORT:
        try:
            r = side.sandbox.run("parser", "--help", capture_output=True, text=True, timeout=60)
            _EMIT_JSON_SUPPORT[key] = "--emit-json" in (r.stdout + r.stderr)
        except (OSError, subprocess.SubprocessError):
            _EMIT_JSON_SUPPORT[key] = False
    return _EMIT_JSON_SUPPORT[key]


def index_cmd(side, corpus, work) -> list[str]:
    if corpus.embed:
        return side.sandbox.cmd("analyze")
    return parser_cmd(side, work, work / ".eulix" / "kb.json")


def index_once(side, corpus, work, log):
    """One cold index. Returns (proc.Measured, parsed output)."""
    corpora.write_config(work, None)
    eulix_dir = corpora.reset_index(work)
    limit = machine.mem_total_bytes() * OOM_FRACTION / (1 << 20) or None
    with sandbox.InstallGuard():
        m = proc.run_measured(index_cmd(side, corpus, work), cwd=work, env=side.sandbox.env, log_path=log,
                              mem_limit_mb=limit)
    text = log.read_text(errors="replace")
    if m.oom_guard:
        raise BenchError(f"indexing {corpus.label} went over {OOM_FRACTION:.0%} of RAM and was stopped; see {log}")
    if m.returncode != 0:
        raise BenchError(f"indexing {corpus.label} with {side.rev.label} failed (exit {m.returncode}); see {log}")
    if "No changes detected" in text or not kbfb.kb_path(eulix_dir).exists():
        raise BenchError(f"indexing {corpus.label} did not run cold; see {log}")
    parsed = parse_output(text)
    if corpus.embed:
        check_device(text, side.device, parsed)
    return m, parsed


def _sizes(eulix_dir) -> dict:
    index = sum(corpora.du(eulix_dir, p) for p in ("kb*.json", "*.bin"))
    cache = corpora.size_tree(eulix_dir / "parse_cache") + corpora.du(eulix_dir, "checksum.*")
    return {"index_bytes": index, "cache_bytes": cache}


METRICS = [  # name, unit, better, kind
    ("total_s", "s", "lower", "time"),
    ("parse_s", "s", "lower", "time"),
    ("embed_s", "s", "lower", "time"),
    ("overhead_s", "s", "lower", "time"),
    ("chunks_per_s", "chunks/s", "higher", "ratio"),
    ("peak_rss_tree_mb", "MB", "lower", "memory"),
    ("peak_rss_parser_mb", "MB", "lower", "memory"),
    ("peak_rss_embed_mb", "MB", "lower", "memory"),
    ("maxrss_single_mb", "MB", "lower", "memory"),
    ("index_bytes", "B", "lower", "size"),
    ("cache_bytes", "B", "none", "size"),  # parse cache + checksum: a deliberate speed/disk trade-off
    ("chunks", "", "none", "count"),
]


def metrics_from_runs(runs: list[dict]) -> list[dict]:
    out = []
    for name, unit, better, kind in METRICS:
        samples = [r[name] for r in runs if r.get(name) is not None]
        if samples:
            out.append(store.metric(name, unit, better, kind, samples=samples))
    return out


def fields(side, corpus) -> dict:
    return {"group": "indexing", "method_version": METHOD_VERSION, "sha": side.rev.sha, "device": side.device,
            "corpus_id": corpus.id, "config_hash": corpora.config_hash(corpus), "env_hash": side.env_hash,
            "machine_fp": side.machine_fp}


def run(side, corpus, repeats: int, rerun: bool = False) -> dict:
    f = fields(side, corpus)
    doc = None if rerun else store.load(side, "indexing", f)
    runs = list(doc["details"]["runs"]) if doc else []
    if len(runs) >= repeats:
        say(f"  {corpus.label}: indexing cached ({len(runs)} runs)")
        return doc
    work = corpora.ensure_work(corpus, side.rev.sha12, side.device)
    while len(runs) < repeats:
        n = len(runs) + 1
        say(f"  {corpus.label}: indexing run {n}/{repeats}")
        m, parsed = index_once(side, corpus, work, side.log(f"indexing-{corpus.name}-{n}"))
        r = {
            "total_s": m.wall_s,
            "peak_rss_tree_mb": m.peak_tree_mb,
            "peak_rss_parser_mb": m.peak_by_comm_mb.get("eulix_parser"),
            "peak_rss_embed_mb": max((v for k, v in m.peak_by_comm_mb.items() if k.startswith("python")),
                                     default=None),
            "maxrss_single_mb": m.maxrss_single_mb,
            **{k: v for k, v in parsed.items() if k != "providers"},
            "providers": parsed["providers"],
            **_sizes(work / ".eulix"),
        }
        if r["parse_s"] is not None:
            r["overhead_s"] = round(r["total_s"] - r["parse_s"] - (r["embed_s"] or 0), 3)
        runs.append(r)
    runner.mark_fixture(side, corpus, work, parsed)
    doc = store.new_doc(side, "indexing", METHOD_VERSION, f, corpus, None)
    doc["details"] = {"runs": runs, "embed": corpus.embed, "threads": 8}
    doc["metrics"] = metrics_from_runs(runs)
    store.save(side, doc)
    return doc
