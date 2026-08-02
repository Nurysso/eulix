#!/usr/bin/env python


# Copyright (C) 2026 Dawood
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
#

from __future__ import annotations
import sys
import traceback
import argparse
import gc
import re
import json
import struct as _struct
import shutil
import time
import site
import importlib.util
from functools import partial
from collections import defaultdict, deque, Counter
from dataclasses import dataclass
from enum import Enum
import io
import os
from concurrent.futures import ThreadPoolExecutor, Future
import threading
from pathlib import Path
from typing import Any, Dict, Generator, List, Optional, Set, Tuple

import ijson.common

# Optional fast JSON backend: orjson is 2-10x faster than stdlib json for
# both parsing and serialization. We bind dumps() variants once at import
# time so hot loops (serve mode, batch writes) don't pay a per-call
# "is orjson available?" branch cost.
try:
    import orjson as _orjson

    # Pre-bind the serialization functions
    if hasattr(_orjson, "OPT_INDENT_2"):
        _json_dumps_no_indent = partial(_orjson.dumps, option=0)
        _json_dumps_indent = partial(_orjson.dumps, option=_orjson.OPT_INDENT_2)
    else:  # fallback for older orjson versions
        _json_dumps_no_indent = lambda obj: _orjson.dumps(obj).decode()
        _json_dumps_indent = lambda obj: _orjson.dumps(
            obj, option=_orjson.OPT_INDENT_2
        ).decode()
    _HAS_ORJSON = True
except ImportError:
    _HAS_ORJSON = False
    _json_dumps_no_indent = partial(json.dumps, indent=None)
    _json_dumps_indent = partial(json.dumps, indent=2)
import warnings

warnings.filterwarnings("ignore", message="optimum is not installed")


# Define the wrapper function once
def _json_dumps(obj: Any, *, indent: bool = False) -> str:
    """Fast JSON dumps – uses orjson when available, falls back to json."""
    if indent:
        return _json_dumps_indent(obj)
    else:
        return _json_dumps_no_indent(obj)


# ijson.common.ObjectBuilder incrementally assembles Python objects (dicts/
# lists/scalars) from a stream of (event, value) pairs. We use it instead of
# json.load() so we never hold the full multi-GB knowledge_base.json in RAM —
# see _stream_kb() below for the single-pass design this enables.
try:
    from ijson.common import ObjectBuilder as _OB
except ImportError:
    try:
        from ijson import ObjectBuilder as _OB  # type: ignore[no-redef]
    except ImportError:
        raise ImportError(
            'ijson ObjectBuilder not found — if using "uv" use uv pip install ijson'
        )


def _require_ml():
    """
    Import and return (np, torch, F, AutoModel, AutoTokenizer, tqdm).
    Called once inside EmbeddingGenerator.__init__; results are stored on the
    instance so no re-import penalty on subsequent calls.
    """
    import numpy as np
    import torch
    import torch.nn.functional as F
    from transformers import AutoModel, AutoTokenizer
    from tqdm import tqdm

    torch.cuda.set_per_process_memory_fraction(0.85)

    return np, torch, F, AutoModel, AutoTokenizer, tqdm

def _require_ml_onnx():
    """
    Import and return (np, ort, AutoTokenizer, tqdm).

    Inference runs entirely through
    ONNX Runtime (`ort`). AutoTokenizer from `transformers` is still used
    since it doesn't require torch/tensorflow to be installed (it's backed
    by the Rust `tokenizers` library).

    Called once inside EmbeddingGenerator.__init__; results are stored on
    the instance so no re-import penalty on subsequent calls.
    """
    import numpy as np
    import onnxruntime as ort
    from transformers import AutoTokenizer
    from tqdm import tqdm

    return np, ort, AutoTokenizer, tqdm


def _require_numpy():
    """Lightweight path: only numpy (used by save_*/load_* bin helpers)."""
    import numpy as np

    return np


# Sequence-length "buckets" for embedding inference. Instead of padding every
# batch to the longest sequence (wasteful) or padding to one global max
# (also wasteful), we snap each chunk's estimated token length up to the
# nearest bucket. This keeps the number of distinct tensor shapes small
# (good for GPU kernel reuse / CUDA graph capture) while avoiding excess
# padding.
BUCKETS_STANDARD: List[int] = [32, 64, 128, 192, 256, 384, 512]
BUCKETS_JINA: List[int] = [32, 64, 128, 192, 256, 384, 512, 768, 1024, 2048, 4096, 8192]

BINARY_MAGIC = b"EULX"  # 4-byte file signature written at the start of embeddings.bin / vectors.bin
BINARY_VERSION = 5  # Bump this whenever the on-disk binary layout changes (see save_embeddings_bin docstring)
# VECTOR_MAGIC = b"EULX"
Version = "0.3.9"  # different from Binary and vector magic

# Dataclass slots: Python ≥3.10 natively; earlier versions fall back gracefully.
_DC_KW: Dict[str, Any] = {"slots": True} if sys.version_info >= (3, 10) else {}


# Snap-to-bucket helper — identical to Rust
def snap_to_bucket(seq_len: int, buckets: List[int]) -> int:
    """Return the smallest bucket size >= seq_len, or the largest bucket
    if seq_len exceeds all of them (sequence gets truncated at inference
    time rather than raising an error)."""
    for b in buckets:
        if seq_len <= b:
            return b
    return buckets[-1]


def _sq8_encode(vec: "np.ndarray") -> tuple["np.ndarray", float]:
    """
    Scalar quantization to int8 (SQ8): shrinks a float32 vector to 1/4 the
    size at the cost of ~1% retrieval quality, by mapping the vector's
    [-max, max] range onto the int8 range [-127, 127].

    Returns (int8_vec, scale) where:
        dequantized ≈ int8_vec.astype(float32) * scale
        scale       = max(|vec|) / 127.0
    A near-zero vector is mapped to all-zeros with scale=1.0 to avoid
    dividing by ~0 (which would blow up the quantized values).
    """
    np = _require_numpy()
    amax = np.abs(vec).max()
    if amax < 1e-9:
        return np.zeros(len(vec), dtype=np.int8), 1.0
    scale = float(amax) / 127.0
    q = np.clip(np.round(vec / scale), -127, 127).astype(np.int8)
    return q, scale


def _sq8_decode(q: "np.ndarray", scale: float) -> "np.ndarray":
    """Dequantize int8 → float32."""
    return q.astype(np.float32) * scale


# The unit of retrieval: one Chunk == one embeddable "thing" extracted from
# source code (a function body, a class overview, a method, or a whole-file
# summary). ChunkType.entry_point exists in the enum for parity with the
# Rust side but isn't currently emitted by chunk_one_file().
class ChunkType(str, Enum):
    function = "function"
    class_ = "class"
    method = "method"
    file = "file"
    entry_point = "entrypoint"
    other = "other"


# Chunk / ChunkMetadata — mirrors Rust structs, with __slots__ for RAM


@dataclass(**_DC_KW)
class ChunkMetadata:
    file_path: Optional[str] = None
    language: Optional[str] = None
    line_start: Optional[int] = None
    line_end: Optional[int] = None
    name: str = ""
    complexity: Optional[int] = None


# __slots__ (via _DC_KW) matters here: a large codebase can produce hundreds
# of thousands of Chunk objects, and __slots__ removes the per-instance
# __dict__, cutting memory roughly in half for objects this simple.
@dataclass(**_DC_KW)
class Chunk:
    id: str
    chunk_type: ChunkType
    content: str
    metadata: ChunkMetadata
    tags: List[str]
    importance_score: float


# SINGLE-PASS KB STREAMING  (replaces load_kb_metadata + stream_kb_structure)


def _stream_kb(path: Path) -> Generator:
    """
    Single-pass streaming reader for the (potentially multi-GB) knowledge
    base JSON, replacing what used to be two full passes over the file.

    The KB JSON has two kinds of top-level content:
      - small keys (metadata, entry_points, patterns, external_dependencies)
        that are cheap to hold in memory whole
      - "structure": a huge map of {file_path: file_struct}, which is the
        part that doesn't fit in RAM for large repos

    ijson.parse() emits a flat stream of (prefix, event, value) events as it
    reads the file byte-by-byte, so we never materialize more than one
    "small key" or one "file_struct" at a time. We track two independent
    ObjectBuilder instances (col_builder for small keys, st_builder for the
    current file inside "structure") and yield as soon as each one is
    complete, then discard it.

    Yields:
      ('meta', key, value)              — one per small top-level key
      ('structure', file_path, struct)  — one per file, in file order

    Why this matters: the old two-pass approach read a 4.6GB KB file twice
    (9.2GB of I/O) and fully materialized nested call_graph/dependency_graph
    dicts, causing 10-20x memory blowup relative to the JSON's on-disk size.
    This version reads the file exactly once and never holds more than one
    file_struct in memory.
    """
    # Keys whose entire value is small enough to buffer in RAM.
    COLLECT_KEYS: Set[str] = {"metadata", "structure"}

    top_key: Optional[str] = None

    # State for collecting small top-level values
    col_builder: Optional[_OB] = None
    col_depth: int = 0

    #  State for streaming structure entries
    st_fp: Optional[str] = None
    st_builder: Optional[_OB] = None
    st_depth: int = 0

    with open(path, "rb") as fh:
        for prefix, event, value in ijson.parse(fh, use_float=True):

            #  top-level key
            if prefix == "" and event == "map_key":
                top_key = value
                # Reset all sub-key trackers
                col_builder = None
                if value == "metadata":
                    col_builder = _OB()
                    col_depth = 0
                continue

            #  Buffer small keys
            if col_builder is not None:
                col_builder.event(event, value)
                if event in ("start_map", "start_array"):
                    col_depth += 1
                elif event in ("end_map", "end_array"):
                    col_depth -= 1
                    if col_depth == 0:
                        yield ("meta", top_key, col_builder.value)
                        col_builder = None
                elif col_depth == 0:
                    # Scalar value at the root of this key
                    yield ("meta", top_key, col_builder.value)
                    col_builder = None
                continue

            # A "structure" map_key event means we've hit a new file path.
            # Start a fresh builder for it; the previous file's builder (if
            # any) was already yielded and dropped when its depth hit 0.
            if top_key == "structure":
                # prefix == "structure" + event == "map_key" → new file path
                if prefix == "structure" and event == "map_key":
                    st_fp = value
                    st_builder = _OB()
                    st_depth = 0
                elif st_builder is not None:
                    st_builder.event(event, value)
                    if event in ("start_map", "start_array"):
                        st_depth += 1
                    elif event in ("end_map", "end_array"):
                        st_depth -= 1
                        if st_depth == 0:
                            yield ("structure", st_fp, st_builder.value)
                            st_builder = None
                            st_fp = None
                continue


# CHUNKER — mirrors chunker.rs logic exactly


def _truncate_content(content: str, max_size: int) -> str:
    safe_max = min(max_size, 2000)
    if len(content) <= safe_max:
        return content
    trunc_at = max(safe_max - 3, 0)
    nl = content[:trunc_at].rfind("\n")
    cut = nl if nl != -1 else trunc_at
    return content[:cut] + "..."


def _generate_tags(func: Dict[str, Any], base_tag: str) -> List[str]:
    tags = [base_tag]
    if func.get("is_async"):
        tags.append("async")
    tags.extend(func.get("tags", []))
    if func.get("complexity", 0) > 10:
        tags.append("complex")
    for dec in func.get("decorators", []):
        if "api" in dec or "route" in dec:
            tags.append("api")
        if "test" in dec:
            tags.append("test")
    return sorted(set(tags))


# These _fmt_* functions render a parsed function/class/file dict back into
# a plain-text "card" that gets embedded and shown to the retriever. They're
# deliberately comment-like (// File:, // Function:) rather than JSON, since
# that reads naturally to both humans and the embedding model.
def _fmt_function_with_context(func: Dict, file_path: str) -> str:
    """Render one function as a text card: signature, params, return type,
    up to 10 outgoing calls and 5 incoming callers (truncated with a "...
    and N more" line to keep card size bounded), control-flow complexity,
    and any exceptions raised/handled."""
    buf = io.StringIO()
    w = buf.write
    w(f"// File: {file_path}")
    w(f"// Function: {func['name']}")
    w(f"// Lines: {func.get('line_start', 0)}-{func.get('line_end', 0)}")
    w(f"// Complexity: {func.get('complexity', 0)}")
    w("")
    w(func.get("signature", ""))
    w("")

    params = func.get("params", [])
    if params:
        w("Parameters:")
        for p in params:
            dv = f" = {p['default_value']}" if p.get("default_value") else ""
            w(f"  - {p['name']}: {p.get('type_annotation', '')}{dv}")
        w("")

    if func.get("return_type"):
        w(f"Returns: {func['return_type']}")
        w("")

    calls = func.get("calls", [])
    if calls:
        w("Calls:")
        for c in calls[:10]:
            w(f"  - {c['callee']} (line {c['line']})")
        if len(calls) > 10:
            w(f"  ... and {len(calls) - 10} more")
        w("")

    called_by = func.get("called_by", [])
    if called_by:
        w("Called by:")
        for c in called_by[:5]:
            w(f"  - {c['function']} in {c['file']}")
        if len(called_by) > 5:
            w(f"  ... and {len(called_by) - 5} more")
        w("")

    cf = func.get("control_flow", {})
    if cf.get("complexity", 0) > 0:
        w(
            f"Control flow: {len(cf.get('branches', []))} branches, "
            f"{len(cf.get('loops', []))} loops"
        )

    exc = func.get("exceptions", {})
    if exc.get("raises") or exc.get("handles"):
        w("Exceptions:")
        if exc.get("raises"):
            w(f"  Raises: {', '.join(exc['raises'])}")
        if exc.get("handles"):
            w(f"  Handles: {', '.join(exc['handles'])}")
        w("")

    return buf.getvalue()


def _fmt_method_with_class_ctx(method: Dict, cls: Dict, file_path: str) -> str:
    buf = io.StringIO()
    w = buf.write
    w(f"// File: {file_path}")
    w(f"// Class: {cls['name']}")
    w(f"// Method: {method['name']}")
    w("")
    if cls.get("bases"):
        w(f"// Inherits from: {', '.join(cls['bases'])}")
    w("")
    w(_fmt_function_with_context(method, file_path))
    return buf.getvalue()


def _fmt_class_overview(cls: Dict, file_path: str) -> str:
    buf = io.StringIO()
    w = buf.write
    w(f"// File: {file_path}")
    w(f"// Class: {cls['name']}")
    w(f"// Lines: {cls.get('line_start', 0)}-{cls.get('line_end', 0)}")
    w("")
    if cls.get("bases"):
        w(f"Inherits from: {', '.join(cls['bases'])}")
        w("")
    attrs = cls.get("attributes", [])
    if attrs:
        w("Attributes:")
        for a in attrs:
            w(f"  - {a['name']}: {a.get('type_annotation', '')}")
        w("")
    methods = cls.get("methods", [])
    if methods:
        w(f"Methods ({len(methods)}):")
        for m in methods:
            async_tag = " (async)" if m.get("is_async") else ""
            w(f"  - {m['name']}{async_tag}")
        w("")
    return buf.getvalue()


def _fmt_file_summary(file_path: str, fs: Dict) -> str:
    buf = io.StringIO()
    w = buf.write
    w(f"File: {file_path}")
    w(f"Language: {fs.get('language', '')}")
    w(f"Lines of code: {fs.get('loc', 0)}")
    w("")
    imports = fs.get("imports", [])
    if imports:
        w("Imports:")
        for imp in imports:
            w(f"  - {imp['module']} ({imp.get('type', '')})")
        w("")
    funcs = fs.get("functions", [])
    if funcs:
        w(f"Functions: {len(funcs)}")
        for f in funcs[:10]:
            w(f"  - {f['name']}")
        if len(funcs) > 10:
            w(f"  ... and {len(funcs) - 10} more")
        w("")
    classes = fs.get("classes", [])
    if classes:
        w(f"Classes: {len(classes)}")
        for c in classes:
            w(f"  - {c['name']}")
        w("")
    return buf.getvalue()


# USED FOR testing no comments and docstring in embedder and vector
# to see how it effects context window creation


# Stripping comments/boilerplate before embedding is a quality experiment:
# license headers, TODOs, and dense comment blocks add tokens that dilute
# the semantic signal of the actual code. These are best-effort, regex-based
# passes (not a real parser) good enough for embedding input, NOT safe to
# use for anything that needs correctness (e.g. don't use this to strip
# comments before executing code).
# It is no longer used and is a dead code kept for future reference
def strip_comments(content: str, lang: str) -> str:
    """
    Best-effort comment stripper, per language family.
    Python: removes '# ...' comments with a simple quote-tracking scanner
        (handles comments after string literals on the same line, but is
        not a full tokenizer — edge cases like triple-quoted strings
        containing '#' are not handled).
    C-like (c/cpp/java/go/javascript/typescript/rust): regex-strips //...
        and /* ... */ blocks. DOTALL means multi-line block comments are
        matched greedily; nested block comments are not supported (C-like
        languages don't nest them anyway).
    Any other `lang` value: returned unchanged.
    """
    if lang == "python":
        # Remove # comments (but not # inside strings – simple version)
        lines = []
        for line in content.splitlines():
            if "#" in line:
                # crude: remove from first # not in quotes
                in_string = False
                quote_char = None
                new_line = []
                for i, ch in enumerate(line):
                    if ch in ('"', "'") and (i == 0 or line[i - 1] != "\\"):
                        if not in_string:
                            in_string = True
                            quote_char = ch
                        elif ch == quote_char:
                            in_string = False
                    elif ch == "#" and not in_string:
                        break
                    new_line.append(ch)
                lines.append("".join(new_line).rstrip())
            else:
                lines.append(line)
        return "\n".join(lines)
    elif lang in ("c", "cpp", "java", "go", "javascript", "typescript", "rust"):
        # Remove // and /* */ comments
        # Simple regex (not perfect for strings, but good enough for KB)
        content = re.sub(r"//.*?$", "", content, flags=re.MULTILINE)
        content = re.sub(r"/\*.*?\*/", "", content, flags=re.DOTALL)
        return content
    return content


def clean_boilerplate(text: str) -> str:
    lines = text.splitlines()
    filtered = []
    for line in lines:
        lower = line.lower()
        if any(
            x in lower
            for x in ("license", "copyright", "todo", "fixme", "note:", "author:")
        ):
            continue
        if line.strip().startswith(("// SPDX", "/* SPDX")):
            continue
        filtered.append(line)
    return "\n".join(filtered)


def clean_content(content: str, lang: str) -> str:
    """Remove comments and boilerplate from content before truncation."""
    content = strip_comments(content, lang)  # remove //, /* */, # comments
    content = clean_boilerplate(content)  # remove license, TODO, etc.
    return content


def _drop_docstrings(fs: dict) -> None:
    """
    Mutate fs in-place, removing docstring fields before chunking.
    Called in the worker thread inside _submit — fs is exclusively owned
    by that closure so mutation is safe.
    """
    for func in fs.get("functions", []):
        func.pop("docstring", None)
    for cls in fs.get("classes", []):
        cls.pop("docstring", None)
        for method in cls.get("methods", []):
            method.pop("docstring", None)


def chunk_one_file(
    file_path: str,
    fs: Dict[str, Any],
    max_size: int,
    seen_ids: Set[str],
) -> List[Chunk]:
    """
    Turn one parsed file_struct into a flat list of Chunks: one per
    function, one per class (overview card) + one per method inside it,
    and one file-level summary card if the file has any content worth
    summarizing.

    `seen_ids` dedupes within this call (methods can't collide with
    functions since IDs are assigned upstream by the parser, but this guards
    against any accidental duplicate emission from malformed input).
    Docstrings must already be stripped from `fs` by the caller
    (_drop_docstrings) before this runs — this function doesn't do it
    itself so it can be called from a worker thread without re-touching
    fields other threads might be reading.
    """
    chunks: List[Chunk] = []
    lang = fs.get("language", "")

    # Functions
    for func in fs.get("functions", []):
        fid = func["id"]
        before = len(seen_ids)
        seen_ids.add(fid)
        if len(seen_ids) == before:
            continue

        chunks.append(
            Chunk(
                id=fid,
                chunk_type=ChunkType.function,
                content=_truncate_content(
                    _fmt_function_with_context(func, file_path),
                    max_size,
                ),
                metadata=ChunkMetadata(
                    file_path=file_path,
                    language=lang,
                    line_start=func.get("line_start"),
                    line_end=func.get("line_end"),
                    name=func["name"],
                    complexity=func.get("complexity"),
                ),
                tags=_generate_tags(func, "function"),
                importance_score=func.get("importance_score", 0.5),
            )
        )

    # Classes + methods
    for cls in fs.get("classes", []):
        cid = cls["id"]
        if cid in seen_ids:
            continue
        seen_ids.add(cid)

        chunks.append(
            Chunk(
                id=cid,
                chunk_type=ChunkType.class_,
                content=_truncate_content(
                    _fmt_class_overview(cls, file_path),
                    max_size,
                ),
                metadata=ChunkMetadata(
                    file_path=file_path,
                    language=lang,
                    line_start=cls.get("line_start"),
                    line_end=cls.get("line_end"),
                    name=cls["name"],
                    complexity=None,
                ),
                tags=["class", lang],
                importance_score=0.7,
            )
        )

        for method in cls.get("methods", []):
            mid = method["id"]
            if mid in seen_ids:
                continue
            seen_ids.add(mid)

            chunks.append(
                Chunk(
                    id=mid,
                    chunk_type=ChunkType.method,
                    content=_truncate_content(
                        _fmt_method_with_class_ctx(method, cls, file_path),
                        max_size,
                    ),
                    metadata=ChunkMetadata(
                        file_path=file_path,
                        language=lang,
                        line_start=method.get("line_start"),
                        line_end=method.get("line_end"),
                        name=f"{cls['name']}.{method['name']}",
                        complexity=method.get("complexity"),
                    ),
                    tags=_generate_tags(method, "method"),
                    importance_score=method.get("importance_score", 0.5),
                )
            )

    # File summary
    summary = _fmt_file_summary(file_path, fs)
    if summary.strip():
        fid = f"file:{file_path}"
        if fid not in seen_ids:
            seen_ids.add(fid)
            chunks.append(
                Chunk(
                    id=fid,
                    chunk_type=ChunkType.file,
                    content=_truncate_content(summary, max_size),
                    metadata=ChunkMetadata(
                        file_path=file_path,
                        language=lang,
                        line_start=1,
                        line_end=fs.get("loc"),
                        name=file_path,
                        complexity=None,
                    ),
                    tags=["file", lang],
                    importance_score=0.5,
                )
            )

    return chunks

# Helpers
# ONNX RUNTIME EMBEDDER
# No PyTorch anywhere in this section — model inference runs entirely
# through onnxruntime.InferenceSession. CPU / CUDA / ROCm are all just
# different Execution Providers to the same session; which ones are
# actually usable depends on which onnxruntime wheel is installed:
#   pip install onnxruntime        -> CPUExecutionProvider only
#   pip install onnxruntime-gpu    -> + CUDAExecutionProvider (NVIDIA)
#   pip install onnxruntime-rocm   -> + ROCMExecutionProvider (AMD ROCm/HIP)
# (only one of onnxruntime / onnxruntime-gpu / onnxruntime-rocm should be
# installed at a time — they conflict on the same import name).

_PROVIDER_ALIASES: Dict[str, List[str]] = {
    "cpu": ["CPUExecutionProvider"],
    "cuda": ["CUDAExecutionProvider", "CPUExecutionProvider"],
    "gpu": ["CUDAExecutionProvider", "CPUExecutionProvider"],
    "rocm": ["ROCMExecutionProvider", "CPUExecutionProvider"],
    "hip": ["ROCMExecutionProvider", "CPUExecutionProvider"],
}

# Where locally-exported ONNX models get cached (used only when a model
# has no pre-exported ONNX weights on the Hub — see _resolve_onnx_model).
_ONNX_CACHE_DIR = Path(
    os.environ.get("EULIX_ONNX_CACHE", str(Path.home() / ".cache" / "eulix-embed" / "onnx"))
)

# Preference order for locating pre-exported ONNX weights inside a model
# repo / local directory. Many sentence-transformers / feature-extraction
# repos on the Hub already ship one of these.
_ONNX_CANDIDATE_FILES: List[str] = [
    "onnx/model.onnx",
    "model.onnx",
    "onnx/model_fp16.onnx",
    "onnx/model_quantized.onnx",
]


def _resolve_providers(
    ort: Any, device: Optional[str]
) -> Tuple[List[str], str]:
    """
    Turn a user-requested device ("cpu" / "cuda" / "rocm" / None) into an
    ordered list of onnxruntime Execution Providers plus a short label used
    for batch-size / bucketing heuristics.

    When `device` is None, auto-detects the best provider actually present
    in `ort.get_available_providers()` — CUDA, then ROCm, then CPU.
    """
    available = set(ort.get_available_providers())

    if device is not None:
        key = device.lower()
        if key not in _PROVIDER_ALIASES:
            raise ValueError(
                f"Unknown device '{device}'. Expected one of: cpu, cuda, rocm"
            )
        wanted = _PROVIDER_ALIASES[key][0]
        if wanted not in available and wanted != "CPUExecutionProvider":
            print(
                f"  ⚠ Requested provider '{wanted}' is not available in this "
                f"onnxruntime build (available: {sorted(available)}); "
                f"falling back to CPUExecutionProvider.",
                file=sys.stderr,
            )
            return ["CPUExecutionProvider"], "cpu"
        label = {"CUDAExecutionProvider": "cuda", "ROCMExecutionProvider": "rocm"}.get(
            wanted, "cpu"
        )
        if label != "cpu":
            print(f"  ✓ Using {wanted}", file=sys.stderr)
        return _PROVIDER_ALIASES[key], label

    if "CUDAExecutionProvider" in available:
        print("  ✓ NVIDIA GPU detected — using CUDAExecutionProvider", file=sys.stderr)
        return ["CUDAExecutionProvider", "CPUExecutionProvider"], "cuda"
    if "ROCMExecutionProvider" in available:
        print(
            "  ✓ AMD GPU detected — using ROCMExecutionProvider (ROCm/HIP)",
            file=sys.stderr,
        )
        return ["ROCMExecutionProvider", "CPUExecutionProvider"], "rocm"
    print("  ℹ No GPU execution provider available — using CPUExecutionProvider", file=sys.stderr)
    return ["CPUExecutionProvider"], "cpu"


def _pick_embedding_output(output_names: List[str]) -> Tuple[Optional[str], Optional[str]]:
    """
    Given the ONNX graph's output names, pick which one to actually use for
    embeddings, and how.

    Returns (output_name, kind) where kind is:
      "token"  — a [batch, seq_len, hidden] tensor (e.g. last_hidden_state)
                 that still needs masked mean-pooling, or
      "pooled" — an already-pooled [batch, hidden] sentence vector
                 (e.g. pooler_output / sentence_embedding).

    Returns (None, None) if the graph exposes no usable embedding output at
    all — e.g. a base-checkpoint export that only has a fill-mask / NSP head
    (vocab-size logits), which is common for plain BERT-style repos that
    were exported without specifying the feature-extraction task.
    """
    by_lower = {n.lower(): n for n in output_names}

    if "last_hidden_state" in by_lower:
        return by_lower["last_hidden_state"], "token"
    hs = next((n for n in output_names if "hidden_state" in n.lower()), None)
    if hs is not None:
        return hs, "token"

    for cand in ("sentence_embedding", "pooler_output"):
        if cand in by_lower:
            return by_lower[cand], "pooled"

    return None, None


def _resolve_onnx_model(
    model_name: str, trust_remote_code: bool = False, force_export: bool = False
) -> Path:
    """
    Resolve `model_name` to a local .onnx file path, trying in order:

      1. `model_name` is already a local .onnx file.
      2. `model_name` is a local directory containing one of
         _ONNX_CANDIDATE_FILES.
      3. A previously-exported ONNX file is already cached locally under
         _ONNX_CACHE_DIR from an earlier run of this function.
      4. A pre-exported ONNX file exists in the HF Hub repo (most
         sentence-transformers / feature-extraction models ship one under
         `onnx/model.onnx`) — downloaded via huggingface_hub, including a
         sibling `*.onnx_data` file for large (>2GB) models.
      5. Nothing usable exists yet: export on the fly via `optimum`, forcing
         the feature-extraction task so the graph is guaranteed to expose
         last_hidden_state rather than a fill-mask/pretraining head. This is
         the only place PyTorch may be touched (`optimum` needs it to trace
         the source model once) — it happens exactly once per model, the
         result is cached under _ONNX_CACHE_DIR, and every subsequent run
         (and every inference call) uses pure onnxruntime.

    `force_export=True` skips straight to step 5 and overwrites the local
    cache — used when a previously-resolved ONNX file turned out not to
    expose a usable embedding output (see EmbeddingGeneratorOnnx.__init__).
    """
    cache_dir = _ONNX_CACHE_DIR / re.sub(r"[^A-Za-z0-9_.-]+", "_", model_name)
    onnx_out = cache_dir / "model.onnx"

    if not force_export:
        local = Path(model_name)
        if local.is_file() and local.suffix == ".onnx":
            return local
        if local.is_dir():
            for pat in _ONNX_CANDIDATE_FILES:
                cand = local / pat
                if cand.exists():
                    return cand

        if onnx_out.exists():
            return onnx_out

        try:
            from huggingface_hub import hf_hub_download, list_repo_files

            repo_files = set(list_repo_files(model_name))
            for pat in _ONNX_CANDIDATE_FILES:
                if pat in repo_files:
                    onnx_path = Path(hf_hub_download(model_name, pat))
                    data_pat = f"{pat}_data"
                    if data_pat in repo_files:
                        hf_hub_download(model_name, data_pat)
                    return onnx_path
        except Exception as e:
            print(
                f"     [i] No pre-exported ONNX weights found on the Hub for "
                f"'{model_name}' ({e}); will export locally.",
                file=sys.stderr,
            )

    print(
        f"     Exporting '{model_name}' to ONNX (feature-extraction task) — "
        f"one-time step, needs 'optimum' + 'torch' installed (inference "
        f"itself will not use them)...",
        file=sys.stderr,
    )
    try:
        from optimum.onnxruntime import ORTModelForFeatureExtraction
    except ImportError:
        raise ImportError(
            f"No usable pre-exported ONNX weights were found for "
            f"'{model_name}', so it needs to be exported once. That "
            f"one-time step requires:\n"
            f"    pip install optimum[exporters] torch\n"
            f"(only for this export — regular inference stays pure onnxruntime)."
        )

    cache_dir.mkdir(parents=True, exist_ok=True)
    ort_model = ORTModelForFeatureExtraction.from_pretrained(
        model_name, export=True, trust_remote_code=trust_remote_code
    )
    ort_model.save_pretrained(cache_dir)

    exported = next(cache_dir.glob("*.onnx"))
    if exported.name != "model.onnx":
        exported.rename(onnx_out)
        exported = onnx_out
    print(f"     ✓ Exported ONNX model cached at {cache_dir}", file=sys.stderr)
    return exported


# PYTORCH EMBEDDER


class EmbeddingGenerator:
    """
    Thin wrapper around a HuggingFace encoder model for batched embedding
    generation. Auto-detects the best available accelerator (CUDA, ROCm/HIP
    on AMD, Apple MPS, or CPU fallback) and picks a sensible default batch
    size per device. Jina v2 models are routed through sentence-transformers
    instead of raw AutoModel/AutoTokenizer because they require custom
    remote code (trust_remote_code=True) that sentence-transformers handles
    for us.

    All heavy imports (torch, transformers, tqdm) are deferred to
    _require_ml() and called exactly once here, so importing this module
    doesn't pull in the ML stack for callers that only need the binary
    I/O helpers (save/load embeddings.bin).
    """

    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        device: Optional[str] = None,  # type: ignore
        batch_size: Optional[int] = None,
        normalize: bool = True,
        use_bucketing: bool = True,
    ):
        np, torch, F, AutoModel, AutoTokenizer, tqdm = _require_ml()
        # stash on self so _embed_batch / generate_vectors can use them
        # without re-importing (Python caches in sys.modules; this is free)
        self._np = np
        self._torch = torch
        self._F = F
        self._tqdm = tqdm

        self.model_name = model_name
        self.normalize = normalize
        try:
            if device is None:
                if torch.cuda.is_available():
                    # Distinguish NVIDIA vs AMD ROCm
                    if torch.version.hip is not None:
                        self.device = torch.device("cuda")
                        print("  ✓ AMD GPU detected — using ROCm/HIP", file=sys.stderr)
                    else:
                        self.device = torch.device("cuda")
                        print("  ✓ NVIDIA GPU detected — using CUDA", file=sys.stderr)
                elif torch.backends.mps.is_available():
                    self.device = torch.device("mps")
                    print("  ✓ Apple MPS detected", file=sys.stderr)
                else:
                    self.device = torch.device("cpu")
                    print("  ℹ No GPU detected — using CPU", file=sys.stderr)
            else:
                self.device = torch.device(device)
            if batch_size is None:
                batch_size = 64 if self.device.type == "cuda" else 16
            self.batch_size = batch_size

            self.use_bucketing = use_bucketing and self.device.type in ("cuda", "mps")

            print(f"     Model:      {model_name}", file=sys.stderr)
            print(f"     Batch size: {self.batch_size}", file=sys.stderr)
            print(
                f"     Bucketing:  {'enabled' if self.use_bucketing else 'disabled'}",
                file=sys.stderr,
            )

            is_jina = "jina" in model_name.lower()

            if is_jina:
                try:
                    from sentence_transformers import SentenceTransformer

                    self._st_model = SentenceTransformer(
                        model_name,
                        device=self.device,
                        trust_remote_code=True,
                        model_kwargs={"attn_implementation": "eager"},
                    )
                    self._st_model.eval()
                    self.tokenizer = self._st_model.tokenizer
                    self.model = None
                    self._use_st = True
                    print(
                        "     Jina v2: loaded via sentence-transformers",
                        file=sys.stderr,
                    )
                except ImportError:
                    raise ImportError(
                        "Jina v2 models require sentence-transformers.\n"
                        "Install: pip install sentence-transformers"
                    )
            else:
                self._use_st = False
                try:
                    self.tokenizer = AutoTokenizer.from_pretrained(model_name, clean_up_tokenization_spaces=True)
                except Exception as e:
                    raise RuntimeError(
                        f"\033[1;31;40m Failed to load tokenizer for '{model_name}'.\n\033[0m"
                        f"\033[1;31;40m Possible reasons:\n\033[0m"
                        f"\033[1;31;40m  - Model ID is incorrect (check https://hugginface.co/models)\n\033[0m"
                        f"\033[1;31;40m  - You need to login: `hugginface-cli login`\n\033[0m"
                        f"\033[1;31;40m  - Model is gated and you lack permissions\n\033[0m"
                        f"\033[1;31;40m  - Netowrk issue\n\033[0m"
                        f"Original error:{e}"
                    )
                try:
                    self.model = AutoModel.from_pretrained(model_name).to(self.device)
                    self.model.eval()
                except Exception as e:
                    raise RuntimeError(
                        f"\033[1;31;40m Failed to load model weights for \{model_name}.\n\033[0m"
                        f"Original error: {e}"
                    )
            # Probe the model's actual output dimension by running a single
            # dummy input through it. We can't trust a hardcoded dimension
            # per model name since users can pass in arbitrary HF model IDs.
            try:
                if self._use_st:
                    test_emb = self._st_model.encode(["hello"], convert_to_numpy=True)
                    self._dimension = test_emb.shape[-1]
                else:
                    with torch.no_grad():
                        dummy = self.tokenizer(
                            "hello", return_tensors="pt", padding=True
                        ).to(self.device)
                        out = self.model(**dummy)
                        emb = self._mean_pool(
                            out.last_hidden_state, dummy["attention_mask"]
                        )
                        self._dimension = emb.shape[-1]
            except Exception as e:
                raise RuntimeError(f"Failed to probe embedding dimension: {e}")

            print(f"     Dimension:  {self._dimension}", file=sys.stderr)
            print("  ✓ Embedding generator ready!", file=sys.stderr)
        except RuntimeError as e:
            print("f\nError: {e}", file=sys.stderr)
            print("\nTips:", file=sys.stderr)
            print(
                "  - Use a valid model name from Hugging Face (e.g., 'sentence-transformers/all-MiniLM-L6-v2')",
                file=sys.stderr,
            )
            print("  - Check your internet connection", file=sys.stderr)
            print(
                "  - Run `huggingface-cli login` if the model is private/gated",
                file=sys.stderr,
            )
            sys.exit(1)

    @property
    def dimension(self) -> int:
        return self._dimension

    @staticmethod
    def _mean_pool(
        last_hidden: "torch.Tensor", attention_mask: "torch.Tensor"
    ) -> "torch.Tensor":
        mask_exp = attention_mask.unsqueeze(-1).float()
        summed = (last_hidden * mask_exp).sum(dim=1)
        counts = mask_exp.sum(dim=1).clamp(min=1e-9)
        return summed / counts

    def _embed_batch(
        self, texts: List[str], fixed_len: Optional[int] = None
    ) -> "np.ndarray":
        """
        Embed a batch of texts in one forward pass.

        fixed_len, when given, forces tokenization/padding to that exact
        length instead of padding to the batch's longest sequence — this is
        what makes the sequence-length bucketing in generate_vectors()
        effective, since it guarantees every batch within a bucket shares
        the same tensor shape.
        Mean-pooling (masked average over token embeddings) is used rather
        than a [CLS] token, matching the sentence-transformers convention
        these models were trained with.
        """

        torch = self._torch
        F = self._F
        np = self._np

        if self._use_st:
            encode_kwargs: Dict[str, Any] = dict(
                convert_to_numpy=True,
                normalize_embeddings=self.normalize,
                show_progress_bar=False,
            )
            if fixed_len is not None:
                old_max = self._st_model.max_seq_length
                self._st_model.max_seq_length = fixed_len
                result = self._st_model.encode(texts, **encode_kwargs)
                self._st_model.max_seq_length = old_max
            else:
                result = self._st_model.encode(texts, **encode_kwargs)
            return result.astype(np.float32)

        tok_kwargs: Dict[str, Any] = dict(
            return_tensors="pt", padding=True, truncation=True
        )
        if fixed_len is not None:
            tok_kwargs["max_length"] = fixed_len
            tok_kwargs["padding"] = "max_length"

        with torch.no_grad():
            enc = self.tokenizer(texts, **tok_kwargs).to(self.device)
            out = self.model(**enc)
            emb = self._mean_pool(out.last_hidden_state, enc["attention_mask"])
            if self.normalize:
                emb = F.normalize(emb, p=2, dim=-1)
            return emb.cpu().float().numpy()

    def generate_vectors(self, chunks: List[Chunk]) -> Dict[str, "np.ndarray"]:
        """
        Embed all chunks and return {chunk_id: vector}.

        When bucketing is enabled (GPU/MPS only — see use_bucketing), chunks
        are grouped by estimated token length into buckets, and each bucket
        is embedded with a fixed padding length. This trades a small amount
        of wasted padding for far fewer distinct tensor shapes, which
        significantly speeds up GPU inference (avoids shape-triggered
        kernel re-selection / graph recapture on every batch).

        Token length is estimated as len(content) // 4 (a rough
        chars-per-token heuristic) purely for bucket assignment — the real
        tokenizer still runs during _embed_batch and will truncate if the
        estimate was wrong.

        Original chunk order is restored at the end (results are processed
        out-of-order across buckets, then re-sorted by original index)
        before building the returned dict, and duplicate chunk IDs are
        collapsed with a warning rather than silently overwritten.
        """
        np = self._np
        tqdm = self._tqdm
        total = len(chunks)
        print(
            f" Processing {total} chunks (batch={self.batch_size},"
            f" bucketing={self.use_bucketing})..."
        )
        t0 = time.time()

        is_jina = "jina" in self.model_name.lower()
        buckets = BUCKETS_JINA if is_jina else BUCKETS_STANDARD

        indexed: List[Tuple[int, int, Chunk]] = [
            (i, max(len(c.content) // 4, 1), c) for i, c in enumerate(chunks)
        ]

        results: List[Tuple[int, "np.ndarray"]] = []

        bar = tqdm(
            total=total,
            unit="chunk",
            desc="     Embedding",
            dynamic_ncols=True,
            bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{remaining}, {rate_fmt}]",
        )

        if self.use_bucketing:
            bucket_map: Dict[int, List[Tuple[int, Chunk]]] = defaultdict(list)
            for orig_idx, est_tokens, chunk in indexed:
                b = snap_to_bucket(est_tokens, buckets)
                bucket_map[b].append((orig_idx, chunk))

            print(f"     Shape buckets active: {len(bucket_map)}")
            # blen is short for Bucket Length
            for blen in sorted(bucket_map):
                print(
                    f"       bucket {blen:>5} tokens → {len(bucket_map[blen])} chunks"
                )

            for blen in sorted(bucket_map):
                items = bucket_map[blen]
                bar.set_postfix(bucket=blen, refresh=False)
                for start in range(0, len(items), self.batch_size):
                    batch = items[start : start + self.batch_size]
                    texts = [c.content for _, c in batch]
                    embs = self._embed_batch(texts, fixed_len=blen)
                    for (orig_idx, _), emb in zip(batch, embs):
                        results.append((orig_idx, emb))
                    bar.update(len(batch))
        else:
            indexed.sort(key=lambda x: x[1], reverse=True)
            for start in range(0, len(indexed), self.batch_size):
                batch = indexed[start : start + self.batch_size]
                texts = [c.content for _, _, c in batch]
                embs = self._embed_batch(texts)
                for (orig_idx, _, _), emb in zip(batch, embs):
                    results.append((orig_idx, emb))
                bar.update(len(batch))

        bar.close()

        elapsed = time.time() - t0
        print(f"  ✓ Completed all embeddings in {elapsed:.2f}s")
        print(f"     Average: {total / max(elapsed, 1e-6):.1f} chunks/sec")

        results.sort(key=lambda x: x[0])
        indexed_sorted = sorted(indexed, key=lambda x: x[0])

        store: Dict[str, "np.ndarray"] = {}
        duplicates: List[str] = []  # track duplicates
        for (orig_idx, emb), (i, _, chunk) in zip(results, indexed_sorted):
            if chunk.id in store:
                duplicates.append(chunk.id)
            store[chunk.id] = emb

        if duplicates:  # surface them loudly
            print(
                f"  [WARN] {len(duplicates)} duplicate chunk IDs collapsed in vectors.bin:"
            )
            for did in duplicates[:10]:
                print(f"         {did}")
            if len(duplicates) > 10:
                print(f"         ... and {len(duplicates) - 10} more")

        return store

    def embed_query(self, query: str) -> "np.ndarray":
        return self._embed_batch([query])[0]

class EmbeddingGeneratorOnnx:
    """
    Thin wrapper around an ONNX Runtime InferenceSession for batched
    embedding generation. There is no PyTorch dependency at inference
    time — the forward pass runs entirely inside onnxruntime, on whichever
    Execution Provider is selected: CPUExecutionProvider, CUDAExecutionProvider
    (NVIDIA), or ROCMExecutionProvider (AMD ROCm/HIP). The provider is
    auto-detected from the installed onnxruntime build unless pinned via
    `device="cpu" | "cuda" | "rocm"`.

    All heavy imports (onnxruntime, transformers, tqdm) are deferred to
    _require_ml_onnx() and called exactly once here, so importing this module
    doesn't pull in the ML stack for callers that only need the binary
    I/O helpers (save/load embeddings.bin).
    """

    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        device: Optional[str] = None,  # "cpu" | "cuda" | "rocm" | None (auto)
        batch_size: Optional[int] = None,
        normalize: bool = True,
        use_bucketing: bool = True,
        trust_remote_code: bool = False,
    ):
        np, ort, AutoTokenizer, tqdm = _require_ml_onnx()
        # stash on self so _embed_batch / generate_vectors can use them
        # without re-importing (Python caches in sys.modules; this is free)
        self._np = np
        self._ort = ort
        self._tqdm = tqdm

        self.model_name = model_name
        self.normalize = normalize

        try:
            self.providers, self.device = _resolve_providers(ort, device)

            if batch_size is None:
                batch_size = 64 if self.device in ("cuda", "rocm") else 16
            self.batch_size = batch_size

            self.use_bucketing = use_bucketing and self.device in ("cuda", "rocm")

            print(f"     Model:      {model_name}", file=sys.stderr)
            print(f"     Provider:   {self.providers[0]}", file=sys.stderr)
            print(f"     Batch size: {self.batch_size}", file=sys.stderr)
            print(
                f"     Bucketing:  {'enabled' if self.use_bucketing else 'disabled'}",
                file=sys.stderr,
            )

            try:
                self.tokenizer = AutoTokenizer.from_pretrained(
                    model_name, trust_remote_code=trust_remote_code, clean_up_tokenization_spaces=True
                )
            except Exception as e:
                raise RuntimeError(
                    f"\033[1;31;40m Failed to load tokenizer for '{model_name}'.\n\033[0m"
                    f"\033[1;31;40m Possible reasons:\n\033[0m"
                    f"\033[1;31;40m  - Model ID is incorrect (check https://huggingface.co/models)\n\033[0m"
                    f"\033[1;31;40m  - You need to login: `huggingface-cli login`\n\033[0m"
                    f"\033[1;31;40m  - Model is gated and you lack permissions\n\033[0m"
                    f"\033[1;31;40m  - Network issue\n\033[0m"
                    f"Original error:{e}"
                )

            def _load_session(path: Path):
                so = ort.SessionOptions()
                so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
                # We deliberately run many distinct input shapes through this
                # one session (sequence-length bucketing, see
                # generate_vectors). ORT's memory-pattern optimizer caches a
                # separate allocation plan per shape it sees and never frees
                # them for the life of the session — with 7-12 buckets that
                # shows up as GPU memory that keeps climbing and never comes
                # back down (unlike PyTorch's allocator, which reuses/splits
                # blocks across shapes). It's meant for single-shape models;
                # turning it off trades a small per-call planning cost for
                # bounded, shape-independent memory usage.
                so.enable_mem_pattern = False

                provider_options: List[Dict[str, str]] = []
                for p in self.providers:
                    if p in ("CUDAExecutionProvider", "ROCMExecutionProvider"):
                        provider_options.append(
                            {
                                # Default is kNextPowerOfTwo, which rounds
                                # every new allocation up and keeps growing
                                # the arena instead of tightly reusing freed
                                # blocks — the other half of the "ORT doesn't
                                # give memory back" behavior.
                                "arena_extend_strategy": "kSameAsRequested",
                            }
                        )
                    else:
                        provider_options.append({})

                return ort.InferenceSession(
                    str(path),
                    sess_options=so,
                    providers=self.providers,
                    provider_options=provider_options,
                )

            try:
                onnx_path = _resolve_onnx_model(
                    model_name, trust_remote_code=trust_remote_code
                )
                self.session = _load_session(onnx_path)
            except Exception as e:
                raise RuntimeError(
                    f"\033[1;31;40m Failed to load ONNX model weights for '{model_name}'.\n\033[0m"
                    f"Original error: {e}"
                )

            self._input_names = {i.name for i in self.session.get_inputs()}
            all_output_names = [o.name for o in self.session.get_outputs()]
            target, kind = _pick_embedding_output(all_output_names)

            if target is None:
                # The resolved ONNX graph has no last_hidden_state / pooled
                # embedding output at all — e.g. it's a full fill-mask or
                # pretraining export whose only outputs are vocab-size
                # logits (a giant [batch, seq_len, vocab] tensor from an
                # MLM decoder head). Requesting *any* of those forces ORT to
                # materialize that tensor, which is what was OOMing. There's
                # no usable embedding to extract from this graph, so force a
                # fresh feature-extraction-task export instead of guessing.
                print(
                    f"     ⚠ Resolved ONNX graph has no usable embedding output "
                    f"(only found: {all_output_names}); forcing a fresh "
                    f"feature-extraction export instead...",
                    file=sys.stderr,
                )
                onnx_path = _resolve_onnx_model(
                    model_name, trust_remote_code=trust_remote_code, force_export=True
                )
                self.session = _load_session(onnx_path)
                self._input_names = {i.name for i in self.session.get_inputs()}
                all_output_names = [o.name for o in self.session.get_outputs()]
                target, kind = _pick_embedding_output(all_output_names)
                if target is None:
                    raise RuntimeError(
                        f"Exported ONNX graph for '{model_name}' still has no "
                        f"last_hidden_state/pooler_output-style output "
                        f"(found: {all_output_names}). This model may not be "
                        f"exportable for embedding extraction via ONNX."
                    )

            if target != "last_hidden_state":
                print(
                    f"     ⚠ Graph has no 'last_hidden_state' output; using "
                    f"'{target}' ({kind}) instead (available: {all_output_names})",
                    file=sys.stderr,
                )
            # IMPORTANT: only ever request this single output. Some graphs
            # bundle extra task heads (e.g. an MLM decoder) alongside the
            # embedding output; passing session.run() *all* output names
            # forces ONNX Runtime to compute those heads too. Requesting
            # exactly the one output we use lets ORT prune the rest.
            self._target_output_name = target
            self._output_kind = kind  # "token" (needs mean-pool) or "pooled"

            # Probe the model's actual output dimension by running a single
            # dummy input through it. We can't trust a hardcoded dimension
            # per model name since users can pass in arbitrary HF model IDs.
            try:
                dummy_emb = self._embed_batch(["hello"])
                self._dimension = dummy_emb.shape[-1]
            except Exception as e:
                raise RuntimeError(f"Failed to probe embedding dimension: {e}")

            print(f"     Dimension:  {self._dimension}", file=sys.stderr)
            print("  ✓ Embedding generator ready!", file=sys.stderr)
        except RuntimeError as e:
            print(f"\nError: {e}", file=sys.stderr)
            print("\nTips:", file=sys.stderr)
            print(
                "  - Use a valid model name from Hugging Face (e.g., 'sentence-transformers/all-MiniLM-L6-v2')",
                file=sys.stderr,
            )
            print("  - Check your internet connection", file=sys.stderr)
            print(
                "  - Run `huggingface-cli login` if the model is private/gated",
                file=sys.stderr,
            )
            print(
                "  - For CUDA/ROCm, install the matching onnxruntime build "
                "(onnxruntime-gpu for CUDA, onnxruntime-rocm for AMD)",
                file=sys.stderr,
            )
            sys.exit(1)

    @property
    def dimension(self) -> int:
        return self._dimension

    def batch_size_for(self, seq_len: int) -> int:
        """
        Bucket-aware batch size. self.batch_size is tuned for a "reference"
        sequence length (512 tokens); using it flat for every bucket means a
        512-token batch and an 8192-token batch (Jina buckets go up to
        8192) get the same batch size even though attention memory scales
        with sequence length. Scale down for longer buckets, floor at 1.
        """
        ref_len = 512
        if seq_len <= ref_len:
            return self.batch_size
        return max(1, (self.batch_size * ref_len) // seq_len)

    @staticmethod
    def _mean_pool(last_hidden: "np.ndarray", attention_mask: "np.ndarray") -> "np.ndarray":
        """Masked average over token embeddings (numpy), matching the
        sentence-transformers pooling convention these models were
        trained with (as opposed to taking the [CLS] token)."""
        np = _require_numpy()
        mask_exp = attention_mask[:, :, None].astype(np.float32)
        summed = (last_hidden * mask_exp).sum(axis=1)
        counts = np.clip(mask_exp.sum(axis=1), 1e-9, None)
        return summed / counts

    @staticmethod
    def _l2_normalize(vec: "np.ndarray", axis: int = -1, eps: float = 1e-12) -> "np.ndarray":
        np = _require_numpy()
        norm = np.linalg.norm(vec, axis=axis, keepdims=True)
        return vec / np.clip(norm, eps, None)

    def _embed_batch(
        self, texts: List[str], fixed_len: Optional[int] = None
    ) -> "np.ndarray":
        """
        Embed a batch of texts in one ONNX Runtime forward pass.

        fixed_len, when given, forces tokenization/padding to that exact
        length instead of padding to the batch's longest sequence — this is
        what makes the sequence-length bucketing in generate_vectors()
        effective, since it guarantees every batch within a bucket shares
        the same tensor shape (important for onnxruntime's kernel/graph
        caching on GPU, same rationale as CUDA graph capture in the old
        PyTorch version).
        """
        np = self._np

        tok_kwargs: Dict[str, Any] = dict(
            return_tensors="np", padding=True, truncation=True
        )
        if fixed_len is not None:
            tok_kwargs["max_length"] = fixed_len
            tok_kwargs["padding"] = "max_length"

        enc = self.tokenizer(texts, **tok_kwargs)
        # Only feed inputs the ONNX graph actually declares — some exports
        # omit token_type_ids, for instance.
        ort_inputs = {k: v for k, v in enc.items() if k in self._input_names}

        outputs = self.session.run([self._target_output_name], ort_inputs)
        raw = outputs[0]
        if self._output_kind == "pooled":
            # Already a [batch, hidden] sentence vector (pooler_output /
            # sentence_embedding) — nothing to pool over.
            emb = raw
        else:
            emb = self._mean_pool(raw, enc["attention_mask"])
        if self.normalize:
            emb = self._l2_normalize(emb)
        return emb.astype(np.float32)

    def generate_vectors(self, chunks: List[Chunk]) -> Dict[str, "np.ndarray"]:
        """
        Embed all chunks and return {chunk_id: vector}.

        When bucketing is enabled (CUDA/ROCm only — see use_bucketing),
        chunks are grouped by estimated token length into buckets, and each
        bucket is embedded with a fixed padding length. This trades a small
        amount of wasted padding for far fewer distinct tensor shapes, which
        speeds up GPU inference in onnxruntime (avoids shape-triggered
        kernel re-selection on every batch).

        Token length is estimated as len(content) // 4 (a rough
        chars-per-token heuristic) purely for bucket assignment — the real
        tokenizer still runs during _embed_batch and will truncate if the
        estimate was wrong.

        Original chunk order is restored at the end (results are processed
        out-of-order across buckets, then re-sorted by original index)
        before building the returned dict, and duplicate chunk IDs are
        collapsed with a warning rather than silently overwritten.
        """
        np = self._np
        tqdm = self._tqdm
        total = len(chunks)
        print(
            f" Processing {total} chunks (batch={self.batch_size},"
            f" bucketing={self.use_bucketing})..."
        )
        t0 = time.time()

        is_jina = "jina" in self.model_name.lower()
        buckets = BUCKETS_JINA if is_jina else BUCKETS_STANDARD

        indexed: List[Tuple[int, int, Chunk]] = [
            (i, max(len(c.content) // 4, 1), c) for i, c in enumerate(chunks)
        ]

        results: List[Tuple[int, "np.ndarray"]] = []

        bar = tqdm(
            total=total,
            unit="chunk",
            desc="     Embedding",
            dynamic_ncols=True,
            bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{remaining}, {rate_fmt}]",
        )

        if self.use_bucketing:
            bucket_map: Dict[int, List[Tuple[int, Chunk]]] = defaultdict(list)
            for orig_idx, est_tokens, chunk in indexed:
                b = snap_to_bucket(est_tokens, buckets)
                bucket_map[b].append((orig_idx, chunk))

            print(f"     Shape buckets active: {len(bucket_map)}")
            # blen is short for Bucket Length
            for blen in sorted(bucket_map):
                print(
                    f"       bucket {blen:>5} tokens → {len(bucket_map[blen])} chunks"
                )

            for blen in sorted(bucket_map):
                items = bucket_map[blen]
                bsz = self.batch_size_for(blen)
                bar.set_postfix(bucket=blen, batch=bsz, refresh=False)
                for start in range(0, len(items), bsz):
                    batch = items[start : start + bsz]
                    texts = [c.content for _, c in batch]
                    embs = self._embed_batch(texts, fixed_len=blen)
                    for (orig_idx, _), emb in zip(batch, embs):
                        results.append((orig_idx, emb))
                    bar.update(len(batch))
        else:
            indexed.sort(key=lambda x: x[1], reverse=True)
            for start in range(0, len(indexed), self.batch_size):
                batch = indexed[start : start + self.batch_size]
                texts = [c.content for _, _, c in batch]
                embs = self._embed_batch(texts)
                for (orig_idx, _, _), emb in zip(batch, embs):
                    results.append((orig_idx, emb))
                bar.update(len(batch))

        bar.close()

        elapsed = time.time() - t0
        print(f"  ✓ Completed all embeddings in {elapsed:.2f}s")
        print(f"     Average: {total / max(elapsed, 1e-6):.1f} chunks/sec")

        results.sort(key=lambda x: x[0])
        indexed_sorted = sorted(indexed, key=lambda x: x[0])

        store: Dict[str, "np.ndarray"] = {}
        duplicates: List[str] = []  # track duplicates
        for (orig_idx, emb), (i, _, chunk) in zip(results, indexed_sorted):
            if chunk.id in store:
                duplicates.append(chunk.id)
            store[chunk.id] = emb

        if duplicates:  # surface them loudly
            print(
                f"  [WARN] {len(duplicates)} duplicate chunk IDs collapsed in vectors.bin:"
            )
            for did in duplicates[:10]:
                print(f"         {did}")
            if len(duplicates) > 10:
                print(f"         ... and {len(duplicates) - 10} more")

        return store

    def embed_query(self, query: str) -> "np.ndarray":
        return self._embed_batch([query])[0]

# BINARY I/O
# Custom binary formats for embeddings, chosen over something like npy/
# parquet to (a) support append-free single-pass streaming writes from
# Python generators, (b) stay byte-compatible with the Rust implementation,
# and (c) keep per-vector overhead minimal (no per-row schema/metadata).


def _write_str(fh, s: str) -> None:
    b = s.encode("utf-8")
    fh.write(_struct.pack("<I", len(b)))
    fh.write(b)


def _read_str(fh) -> str:
    (n,) = _struct.unpack("<I", fh.read(4))
    return fh.read(n).decode("utf-8")


def save_embeddings_bin(
    path: "Path",
    model_name: str,
    dimension: int,
    entries,  # Iterable[Tuple[str, np.ndarray]]
    count: "Optional[int]" = None,
    quantize: bool = False,
) -> None:
    """
    Stream-write embeddings.bin without ever holding all vectors in memory
    at once — `entries` is consumed lazily as an iterator, so this can be
    fed directly from a generator (see EmbeddingPipeline.generate_vectors_streaming).

    v4 format adds a single quantization-flag byte after `dimension` so
    readers can distinguish float32 vs SQ8 payloads; v3 files (no flag byte)
    are still readable by load_embeddings_bin for backwards compatibility.
    v5 changed name of script and magicByte

    File Layout (Little-Endian):
    ─────────────────────────────────────────────────────────
    4  bytes  magic          "EULX"
    4  bytes  version        4
    4+n bytes model_name     uint32 len + UTF-8
    4  bytes  count          number of embeddings
    4  bytes  dimension      vector length (floats)
    1  byte   quantized      0 = float32, 1 = SQ8 int8
    ─── per embedding ───────────────────────────────────────
    4  bytes  id_len
    id_len    id             UTF-8
    if quantized:
      4  bytes  scale        float32 (little-endian)
      dim bytes quantized    int8 * dim
    else:
      dim*4    vector        float32 * dim
    ─────────────────────────────────────────────────────────
    """
    np = _require_numpy()
    if count is None:
        entries = list(entries)
        count = len(entries)

    quant_flag = b"\x01" if quantize else b"\x00"

    with open(path, "wb") as raw:
        fh = io.BufferedWriter(raw, buffer_size=4 * 1024 * 1024)
        fh.write(BINARY_MAGIC)
        fh.write(_struct.pack("<I", BINARY_VERSION))
        _write_str(fh, model_name)
        fh.write(_struct.pack("<II", count, dimension))
        fh.write(quant_flag)

        for eid, vec in entries:
            eid_bytes = eid.encode("utf-8")
            if len(eid_bytes) > 0xFFFF:
                raise ValueError(f"Chunk ID too long: {eid[:80]}...")
            fh.write(_struct.pack("<I", len(eid_bytes)))
            fh.write(eid_bytes)

            arr = np.asarray(vec, dtype=np.float32)
            if arr.shape != (dimension,):
                raise ValueError(
                    f"Shape mismatch for {eid}: {arr.shape} vs ({dimension},)"
                )
            if quantize:
                q, scale = _sq8_encode(arr)
                fh.write(_struct.pack("<f", scale))  # 4 bytes
                fh.write(q.tobytes())  # dim bytes (int8)
            else:
                fh.write(arr.tobytes())  # dim*4 bytes
            fh.flush()


def load_embeddings_bin(path: "Path", dequantize: bool = True):
    """
    Load embeddings.bin, transparently handling both v3 (always float32)
    and v4 (float32 or SQ8, flagged) files.

    On a corrupt/truncated entry, logs the error and stops reading rather
    than raising — callers get back whatever was successfully parsed plus
    a `count` mismatch they can detect by comparing len(entries) to the
    header's declared count.

    dequantize=True (default): always returns (id, float32_vector) pairs,
        transparently converting SQ8 data back to float32.
    dequantize=False: for SQ8 files, returns (id, int8_vector, scale)
        instead — useful if the caller wants to do quantized-domain math
        (e.g. int8 dot products) without paying the conversion cost.
    """
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic: {magic!r}")
        (version,) = _struct.unpack("<I", fh.read(4))
        model_name = _read_str(fh)
        count, dim = _struct.unpack("<II", fh.read(8))

        quantized = False
        if version == 4:
            quantized = fh.read(1) == b"\x01"
        elif version != 3:
            raise ValueError(f"Unsupported embeddings.bin version: {version}")

        print(f"  magic={magic}, version={version}, model={model_name}")
        print(f"  count={count}, dim={dim}, quantized={quantized}")

        entries = []
        for idx in range(count):
            pos = fh.tell()
            try:
                eid = _read_str(fh)
                if quantized:
                    (scale,) = _struct.unpack("<f", fh.read(4))
                    raw = fh.read(dim)
                    q = _np.frombuffer(raw, dtype=_np.int8).copy()
                    if dequantize:
                        entries.append((eid, _sq8_decode(q, scale)))
                    else:
                        entries.append((eid, q, scale))
                else:
                    raw = fh.read(dim * 4)
                    if len(raw) != dim * 4:
                        print(f"  [ERROR] entry {idx} @{pos}: short read")
                        break
                    vec = _np.frombuffer(raw, dtype=_np.float32).copy()
                    entries.append((eid, vec))
            except Exception as e:
                print(f"  [ERROR] entry {idx} @{pos}: {e}")
                break

        print(f"  successfully read {len(entries)} / {count} entries")
        return model_name, entries


def save_vectors_bin(
    path: Path,
    model_name: str,
    ids: List[str],  # ordered; position == vector index in embeddings.bin
) -> None:
    """
    Save a vector ID index (no float data).

    File Format (Little-Endian):
    =============================
    Offset   | Size     | Field       | Description
    ---------|----------|-------------|-----------------------------
    0        | 4        | magic       | "EULX" file signature
    4        | 4        | version     | Format version (currently 1)
    8        | variable | model_name  | 4-byte len + UTF-8 bytes
    8+mlen   | 4        | count       | Number of IDs
    12+mlen  | variable | id_data     | Repeated for each ID:
             |          |   4 bytes   | id_len (uint32 LE)
             |          |   id_len    | UTF-8 chunk ID
    """
    with open(path, "wb") as fh:
        fh.write(BINARY_MAGIC)
        fh.write(_struct.pack("<I", BINARY_VERSION))
        _write_str(fh, model_name)
        fh.write(_struct.pack("<I", len(ids)))
        for eid in ids:
            eid_bytes = eid.encode("utf-8")
            if len(eid_bytes) > 0xFFFF:
                raise ValueError(
                    f"Chunk ID too long ({len(eid_bytes)} bytes): {eid[:80]}..."
                )
            fh.write(_struct.pack("<I", len(eid_bytes)))
            fh.write(eid_bytes)


def load_vectors_bin(path: Path) -> Tuple[str, List[str]]:
    """
    Load a vector ID index.

    Returns:
        (model_name, ids)  — ids[i] is the chunk ID at vector index i.
    """
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic: {magic!r} (expected {BINARY_VERSION!r})")
        (version,) = _struct.unpack("<I", fh.read(4))
        if version != BINARY_VERSION:
            raise ValueError(f"Unsupported vectors.bin version: {version}")
        model_name = _read_str(fh)
        (count,) = _struct.unpack("<I", fh.read(4))
        ids: List[str] = [_read_str(fh) for _ in range(count)]
    return model_name, ids


# PIPELINE
class EmbeddingPipeline:
    """
    End-to-end driver: KB JSON -> chunks -> embeddings.bin + vectors.bin.

    Structured as 3 conceptual steps, executed as 2 physical passes to
    minimize peak memory:
      Step 1+2 (single pass): stream-parse the KB and chunk each file as it
        arrives, using a bounded thread pool (MAX_INFLIGHT futures) so we
        never queue up more in-flight work than we can hold in memory, and
        never block sequentially on one file at a time either.
      Step 3+4 (single pass): embed chunks and write both binary files
        directly from a generator — embeddings.bin is written incrementally
        so peak RAM is one batch's worth of vectors, not all of them.
    """

    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        max_chunk_size: int = 2000,
        device: Optional[str] = None,
        batch_size: Optional[int] = None,
        save_json: bool = False,
        quantize: bool = False,
        debug: bool = False,
    ):
        self.max_chunk_size = max_chunk_size
        self.save_json = save_json
        self.quantize = quantize  # stored on self
        self.debug = debug
        self.generator = EmbeddingGenerator(
            model_name=model_name,
            device=device,
            batch_size=batch_size,
        )

    def generate_vectors_streaming(
        self,
        gen: "EmbeddingGenerator",
        chunks: "List[Chunk]",
    ):
        import numpy as _np
        from tqdm import tqdm as _tqdm

        total = len(chunks)
        is_jina = "jina" in gen.model_name.lower()
        buckets = BUCKETS_JINA if is_jina else BUCKETS_STANDARD

        # Build (orig_idx, est_tokens, chunk) triples
        indexed = [(i, max(len(c.content) // 4, 1), c) for i, c in enumerate(chunks)]

        # We need to yield in original order, but bucketing processes out-of-order.
        # Collect results[(orig_idx, vec)] then sort once — same as before but
        # we free them immediately after yielding.
        results: "List[Tuple[int, np.ndarray]]" = []

        bar = _tqdm(
            total=total,
            unit="chunk",
            desc="     Embedding",
            dynamic_ncols=True,
            bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{remaining}, {rate_fmt}]",
        )

        import time as _time

        t0 = _time.time()

        if gen.use_bucketing:
            from collections import defaultdict as _dd

            bucket_map: dict = _dd(list)
            for orig_idx, est_tokens, chunk in indexed:
                b = snap_to_bucket(est_tokens, buckets)
                bucket_map[b].append((orig_idx, chunk))

            for blen in sorted(bucket_map):
                items = bucket_map[blen]
                bar.set_postfix(bucket=blen, refresh=False)
                for start in range(0, len(items), gen.batch_size):
                    batch = items[start : start + gen.batch_size]
                    texts = [c.content for _, c in batch]
                    embs = gen._embed_batch(texts, fixed_len=blen)
                    for (orig_idx, _), emb in zip(batch, embs):
                        results.append((orig_idx, emb))
                    bar.update(len(batch))
        else:
            indexed.sort(key=lambda x: x[1], reverse=True)
            for start in range(0, len(indexed), gen.batch_size):
                batch = indexed[start : start + gen.batch_size]
                texts = [c.content for _, _, c in batch]
                embs = gen._embed_batch(texts)
                for (orig_idx, _, _), emb in zip(batch, embs):
                    results.append((orig_idx, emb))
                bar.update(len(batch))

        bar.close()
        elapsed = _time.time() - t0
        print(f"  ✓ Completed all embeddings in {elapsed:.2f}s")
        print(f"     Average: {total / max(elapsed, 1e-6):.1f} chunks/sec")

        # Sort by original index → yield in chunk order (no dict needed)
        results.sort(key=lambda x: x[0])
        indexed_sorted = sorted(indexed, key=lambda x: x[0])

        seen: set = set()
        for (orig_idx, vec), (i, _, chunk) in zip(results, indexed_sorted):
            if chunk.id in seen:
                continue  # skip duplicates (same dedup as before)
            seen.add(chunk.id)
            yield chunk.id, vec

    def _check_disk_space(
        self, output_dir: Path, kb_path: Path, n_chunks: Optional[int] = None
    ) -> None:
        """
        Estimate disk space requirements based on actual KB structure.

        For quantized: ~ (dimension + 4) bytes per vector + ID overhead
        For float32:   ~ dimension * 4 bytes per vector + ID overhead
        """
        free = shutil.disk_usage(
            output_dir.parent if not output_dir.exists() else output_dir
        ).free

        # Try to estimate from kb.json without full parse
        if n_chunks is None:
            try:
                import ijson

                chunk_count = 0
                with open(kb_path, "rb") as fh:
                    # Quick scan - count IDs without loading everything
                    parser = ijson.parse(fh)
                    for prefix, event, value in parser:
                        # Count function, class, method IDs
                        if (
                            prefix.endswith(".functions.item.id")
                            or prefix.endswith(".classes.item.id")
                            or prefix.endswith(".methods.item.id")
                        ):
                            chunk_count += 1
                        # Stop after reasonable sample or end
                        if chunk_count > 0 and prefix and "item" in prefix:
                            # Rough estimate: sample first 1000, then extrapolate
                            if chunk_count >= 1000:
                                # Estimate total file size ratio
                                file_size_mb = kb_path.stat().st_size / 1_048_576
                                # Rough heuristic: ~40KB per chunk in JSON
                                estimated_total = int(
                                    file_size_mb * 25
                                )  # ~40KB per chunk
                                chunk_count = max(chunk_count, estimated_total)
                                break
            except Exception:
                # Fallback: assume 100 chunks per MB of JSON
                chunk_count = n_chunks or (kb_path.stat().st_size // 10_000)
        else:
            chunk_count = n_chunks

        dim = self.generator.dimension

        # Calculate actual required space
        if self.quantize:
            # SQ8: 1 byte per dimension + 4 bytes scale per vector
            bytes_per_vector = dim + 4
            quant_suffix = " (SQ8 int8)"
        else:
            # float32: 4 bytes per dimension
            bytes_per_vector = dim * 4
            quant_suffix = " (float32)"

        # ID overhead: avg ID length (~50 chars) + 4 bytes length prefix
        avg_id_len = 50
        bytes_per_id = avg_id_len + 4

        # Total space for embeddings + vectors + 20% headroom
        embeddings_size = chunk_count * bytes_per_vector
        vectors_size = chunk_count * bytes_per_id
        required = int((embeddings_size + vectors_size) * 1.2)

        # Add overhead for JSON output if enabled
        if self.save_json:
            required = int(required * 1.5)  # JSON is ~50% larger

        free_gb = free / 1_073_741_824
        req_gb = required / 1_073_741_824
        actual_gb = (embeddings_size + vectors_size) / 1_073_741_824

        print(f"  Estimated chunks: {chunk_count:,}")
        print(f"  Vector dimension: {dim}{quant_suffix}")
        print(f"  Estimated embeddings.bin: {embeddings_size / 1_048_576:.1f} MB")
        print(f"  Estimated vectors.bin:    {vectors_size / 1_048_576:.1f} MB")
        print(f"  Disk space required: ~{req_gb:.2f} GB (actual ~{actual_gb:.2f} GB)")
        print(f"  Disk space available: {free_gb:.2f} GB")

        if free < required:
            print(f"\n  [ERROR] Insufficient disk space!")
            print(f"          Need: ~{req_gb:.2f} GB")
            print(f"          Have:  {free_gb:.2f} GB")
            print(f"          Shortfall: {(required - free) / 1_073_741_824:.2f} GB")
            sys.exit(1)
        elif free < required * 1.1:
            print(f"  [WARN] Low disk space - only {(free_gb - req_gb):.2f} GB buffer")

    def _process_step3_step4(
        self,
        chunks: List[Chunk],
        output_dir: Path,
    ) -> int:
        """
        Merged Step 3 + Step 4: embed → stream directly to disk.
        Peak RAM = one batch of embeddings (batch_size * dim * 4 bytes).

        Returns the dimension used (needed by process() summary block).
        """
        sep = "-" * 70

        print("STEP 3+4: Generating Embeddings → Writing to disk (streaming)")
        print(sep)
        t = time.time()

        dim = self.generator.dimension
        output_dir.mkdir(parents=True, exist_ok=True)

        emb_bin = output_dir / "embeddings.bin"
        vec_bin = output_dir / "vectors.bin"

        ordered_ids: List[str] = []
        n_written = 0

        def _entry_gen():
            nonlocal n_written
            for cid, vec in self.generate_vectors_streaming(self.generator, chunks):
                ordered_ids.append(cid)
                n_written += 1
                yield cid, vec

        count = len(chunks)  # upper bound; dedup inside generator may reduce

        save_embeddings_bin(
            emb_bin,
            self.generator.model_name,
            dim,
            _entry_gen(),
            count=count,
            quantize=self.quantize,
        )

        # Patch count field in header if dedup reduced it
        if n_written != count:
            model_bytes = self.generator.model_name.encode()
            count_offset = 4 + 4 + 4 + len(model_bytes)  # magic+ver+model_len+model
            with open(emb_bin, "r+b") as fh:
                fh.seek(count_offset)
                fh.write(_struct.pack("<I", n_written))

        emb_size = emb_bin.stat().st_size / 1_048_576
        quant_str = " (SQ8 int8)" if self.quantize else " (float32)"
        print(f"  [OK] embeddings.bin  ({emb_size:.2f} MB){quant_str}")

        save_vectors_bin(vec_bin, self.generator.model_name, ordered_ids)
        print(f"  [OK] vectors.bin     ({vec_bin.stat().st_size / 1_048_576:.2f} MB)")
        print(f"       Vectors written: {n_written}")
        print(f"       Time:            {time.time() - t:.2f}s\n")

        return dim  # returned so process() summary block can use it

    def process(
        self, kb_path: Path, output_dir: Path, args: argparse.Namespace
    ) -> None:
        t_total = time.time()
        SEP = "=" * 70
        sep = "-" * 70

        print(f"\n{SEP}")
        print("  EULIX EMBED — EMBEDDING PIPELINE")
        print(f"  Engine : PyTorch")
        print(f"  ijson backend : {ijson.backend}")
        print(f"  orjson        : {'yes' if _HAS_ORJSON else 'no (stdlib json)'}")
        print(f"  Chunk slots   : {'yes' if _DC_KW else 'no (Python <3.10)'}")
        print(f"  Quantization  : {'SQ8 int8' if self.quantize else 'float32'}")
        print(f"{SEP}\n")

        self._check_disk_space(output_dir, kb_path)

        # Step 1+2: Single-pass KB scan + chunk generation
        print("STEP 1+2: Knowledge Base scan + Chunk generation (single pass)")
        print(sep)
        t = time.time()

        meta: Dict[str, Any] = {}
        chunks: List[Chunk] = []
        seen_ids: Set[str] = set()
        n_files = n_funcs = n_classes = n_methods = 0
        ct_counts: Dict[str, int] = defaultdict(int)

        MAX_INFLIGHT = 32
        inflight: deque[Future] = deque()
        # Bounded producer/consumer: keep submitting file-chunking work to
        # the thread pool as files stream in from _stream_kb, but once
        # MAX_INFLIGHT futures are outstanding, drain half of them before
        # submitting more. This caps memory (each in-flight future holds
        # one file_struct + its future chunk list) without serializing
        # chunking behind KB parsing.

        def _submit(file_path: str, fs: dict) -> Future:
            max_size = self.max_chunk_size

            def _work():
                _drop_docstrings(fs)
                local_seen: Set[str] = set()
                return chunk_one_file(file_path, fs, max_size, local_seen)

            return executor.submit(_work)

        def _harvest(fut: Future) -> None:
            for chunk in fut.result():
                if chunk.id not in seen_ids:
                    seen_ids.add(chunk.id)
                    ct_counts[chunk.chunk_type.value] += 1
                    chunks.append(chunk)

        def _drain_inflight(max_remaining: int = 0) -> None:
            while len(inflight) > max_remaining:
                _harvest(inflight.popleft())

        N_WORKERS = min(4, (os.cpu_count() or 4))
        with ThreadPoolExecutor(max_workers=N_WORKERS) as executor:
            for event_type, *payload in _stream_kb(kb_path):
                if event_type == "meta":
                    key, value = payload
                    meta[key] = value

                elif event_type == "structure":
                    file_path, fs = payload
                    n_files += 1
                    n_funcs += len(fs.get("functions", []))
                    n_classes += len(fs.get("classes", []))
                    n_methods += sum(
                        len(c.get("methods", [])) for c in fs.get("classes", [])
                    )
                    inflight.append(_submit(file_path, fs))
                    if len(inflight) >= MAX_INFLIGHT:
                        _drain_inflight(max_remaining=MAX_INFLIGHT // 2)

            _drain_inflight(max_remaining=0)

        step12_time = time.time() - t
        n_chunks = len(chunks)

        print(f"  [OK] KB scanned + chunked in single pass")
        print(f"       Files:        {n_files}")
        print(f"       Functions:    {n_funcs}")
        print(f"       Classes:      {n_classes}")
        print(f"       Methods:      {n_methods}")
        print(f"       Total Chunks: {n_chunks}")
        print(f"       Chunk Breakdown:")
        for ct, cnt in sorted(ct_counts.items()):
            print(f"         {ct + ':':<22} {cnt}")
        print(f"       Time:         {step12_time:.2f}s\n")

        del seen_ids
        gc.collect()

        # Step 3+4: Embed + write (streaming, no full vector_store dict)
        dim = self._process_step3_step4(chunks, output_dir)

        # Summary
        total_elapsed = time.time() - t_total
        print(SEP)
        print("  PIPELINE SUMMARY")
        print(SEP)
        print(f"  Engine: Torch")
        print(f"  Model:          {self.generator.model_name}")
        print(f"  Quantization:   {'SQ8 int8' if self.quantize else 'float32'}")
        print(f"  Dimension:      {dim}")
        print(f"  Total Chunks:   {n_chunks}")
        print(f"  Total Time:     {total_elapsed:.2f}s")
        print(SEP)
        print("  PIPELINE COMPLETED SUCCESSFULLY")
        print(f"{SEP}\n")

class EmbeddingPipelineOnnx:
    """
    End-to-end driver: KB JSON -> chunks -> embeddings.bin + vectors.bin.

    Structured as 3 conceptual steps, executed as 2 physical passes to
    minimize peak memory:
      Step 1+2 (single pass): stream-parse the KB and chunk each file as it
        arrives, using a bounded thread pool (MAX_INFLIGHT futures) so we
        never queue up more in-flight work than we can hold in memory, and
        never block sequentially on one file at a time either.
      Step 3+4 (single pass): embed chunks and write both binary files
        directly from a generator — embeddings.bin is written incrementally
        so peak RAM is one batch's worth of vectors, not all of them.
    """

    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        max_chunk_size: int = 2000,
        device: Optional[str] = None,
        batch_size: Optional[int] = None,
        save_json: bool = False,
        quantize: bool = False,
        debug: bool = False,
    ):
        self.max_chunk_size = max_chunk_size
        self.save_json = save_json
        self.quantize = quantize
        self.debug = debug
        self.generator = EmbeddingGeneratorOnnx(
            model_name=model_name,
            device=device,
            batch_size=batch_size,
        )

    def generate_vectors_streaming(
        self,
        gen: "EmbeddingGeneratorOnnx",
        chunks: "List[Chunk]",
    ):
        import numpy as _np
        from tqdm import tqdm as _tqdm

        total = len(chunks)
        is_jina = "jina" in gen.model_name.lower()
        buckets = BUCKETS_JINA if is_jina else BUCKETS_STANDARD

        # Build (orig_idx, est_tokens, chunk) triples
        indexed = [(i, max(len(c.content) // 4, 1), c) for i, c in enumerate(chunks)]

        # We need to yield in original order, but bucketing processes out-of-order.
        # Collect results[(orig_idx, vec)] then sort once — same as before but
        # we free them immediately after yielding.
        results: "List[Tuple[int, np.ndarray]]" = []

        bar = _tqdm(
            total=total,
            unit="chunk",
            desc="     Embedding",
            dynamic_ncols=True,
            bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{remaining}, {rate_fmt}]",
        )

        import time as _time

        t0 = _time.time()

        if gen.use_bucketing:
            from collections import defaultdict as _dd

            bucket_map: dict = _dd(list)
            for orig_idx, est_tokens, chunk in indexed:
                b = snap_to_bucket(est_tokens, buckets)
                bucket_map[b].append((orig_idx, chunk))

            for blen in sorted(bucket_map):
                items = bucket_map[blen]
                bsz = gen.batch_size_for(blen)
                bar.set_postfix(bucket=blen, batch=bsz, refresh=False)
                for start in range(0, len(items), bsz):
                    batch = items[start : start + bsz]
                    texts = [c.content for _, c in batch]
                    embs = gen._embed_batch(texts, fixed_len=blen)
                    for (orig_idx, _), emb in zip(batch, embs):
                        results.append((orig_idx, emb))
                    bar.update(len(batch))
        else:
            indexed.sort(key=lambda x: x[1], reverse=True)
            for start in range(0, len(indexed), gen.batch_size):
                batch = indexed[start : start + gen.batch_size]
                texts = [c.content for _, _, c in batch]
                embs = gen._embed_batch(texts)
                for (orig_idx, _, _), emb in zip(batch, embs):
                    results.append((orig_idx, emb))
                bar.update(len(batch))

        bar.close()
        elapsed = _time.time() - t0
        print(f"  ✓ Completed all embeddings in {elapsed:.2f}s")
        print(f"     Average: {total / max(elapsed, 1e-6):.1f} chunks/sec")

        # Sort by original index → yield in chunk order (no dict needed)
        results.sort(key=lambda x: x[0])
        indexed_sorted = sorted(indexed, key=lambda x: x[0])

        seen: set = set()
        for (orig_idx, vec), (i, _, chunk) in zip(results, indexed_sorted):
            if chunk.id in seen:
                continue  # skip duplicates (same dedup as before)
            seen.add(chunk.id)
            yield chunk.id, vec

    def _check_disk_space(
        self, output_dir: Path, kb_path: Path, n_chunks: Optional[int] = None
    ) -> None:
        """
        Estimate disk space requirements based on actual KB structure.

        For quantized: ~ (dimension + 4) bytes per vector + ID overhead
        For float32:   ~ dimension * 4 bytes per vector + ID overhead
        """
        free = shutil.disk_usage(
            output_dir.parent if not output_dir.exists() else output_dir
        ).free

        # Try to estimate from kb.json without full parse
        if n_chunks is None:
            try:
                import ijson

                chunk_count = 0
                with open(kb_path, "rb") as fh:
                    # Quick scan - count IDs without loading everything
                    parser = ijson.parse(fh)
                    for prefix, event, value in parser:
                        # Count function, class, method IDs
                        if (
                            prefix.endswith(".functions.item.id")
                            or prefix.endswith(".classes.item.id")
                            or prefix.endswith(".methods.item.id")
                        ):
                            chunk_count += 1
                        # Stop after reasonable sample or end
                        if chunk_count > 0 and prefix and "item" in prefix:
                            # Rough estimate: sample first 1000, then extrapolate
                            if chunk_count >= 1000:
                                # Estimate total file size ratio
                                file_size_mb = kb_path.stat().st_size / 1_048_576
                                # Rough heuristic: ~40KB per chunk in JSON
                                estimated_total = int(
                                    file_size_mb * 25
                                )  # ~40KB per chunk
                                chunk_count = max(chunk_count, estimated_total)
                                break
            except Exception:
                # Fallback: assume 100 chunks per MB of JSON
                chunk_count = n_chunks or (kb_path.stat().st_size // 10_000)
        else:
            chunk_count = n_chunks

        dim = self.generator.dimension

        # Calculate actual required space
        if self.quantize:
            # SQ8: 1 byte per dimension + 4 bytes scale per vector
            bytes_per_vector = dim + 4
            quant_suffix = " (SQ8 int8)"
        else:
            # float32: 4 bytes per dimension
            bytes_per_vector = dim * 4
            quant_suffix = " (float32)"

        # ID overhead: avg ID length (~50 chars) + 4 bytes length prefix
        avg_id_len = 50
        bytes_per_id = avg_id_len + 4

        # Total space for embeddings + vectors + 20% headroom
        embeddings_size = chunk_count * bytes_per_vector
        vectors_size = chunk_count * bytes_per_id
        required = int((embeddings_size + vectors_size) * 1.2)

        # Add overhead for JSON output if enabled
        if self.save_json:
            required = int(required * 1.5)  # JSON is ~50% larger

        free_gb = free / 1_073_741_824
        req_gb = required / 1_073_741_824
        actual_gb = (embeddings_size + vectors_size) / 1_073_741_824

        print(f"  Estimated chunks: {chunk_count:,}")
        print(f"  Vector dimension: {dim}{quant_suffix}")
        print(f"  Estimated embeddings.bin: {embeddings_size / 1_048_576:.1f} MB")
        print(f"  Estimated vectors.bin:    {vectors_size / 1_048_576:.1f} MB")
        print(f"  Disk space required: ~{req_gb:.2f} GB (actual ~{actual_gb:.2f} GB)")
        print(f"  Disk space available: {free_gb:.2f} GB")

        if free < required:
            print(f"\n  [ERROR] Insufficient disk space!")
            print(f"          Need: ~{req_gb:.2f} GB")
            print(f"          Have:  {free_gb:.2f} GB")
            print(f"          Shortfall: {(required - free) / 1_073_741_824:.2f} GB")
            sys.exit(1)
        elif free < required * 1.1:
            print(f"  [WARN] Low disk space - only {(free_gb - req_gb):.2f} GB buffer")

    def _process_step3_step4(
        self,
        chunks: List[Chunk],
        output_dir: Path,
    ) -> int:
        """
        Merged Step 3 + Step 4: embed → stream directly to disk.
        Peak RAM = one batch of embeddings (batch_size * dim * 4 bytes).

        Returns the dimension used (needed by process() summary block).
        """
        sep = "-" * 70

        print("STEP 3+4: Generating Embeddings → Writing to disk (streaming)")
        print(sep)
        t = time.time()

        dim = self.generator.dimension
        output_dir.mkdir(parents=True, exist_ok=True)

        emb_bin = output_dir / "embeddings.bin"
        vec_bin = output_dir / "vectors.bin"

        ordered_ids: List[str] = []
        n_written = 0

        def _entry_gen():
            nonlocal n_written
            for cid, vec in self.generate_vectors_streaming(self.generator, chunks):
                ordered_ids.append(cid)
                n_written += 1
                yield cid, vec

        count = len(chunks)  # upper bound; dedup inside generator may reduce

        save_embeddings_bin(
            emb_bin,
            self.generator.model_name,
            dim,
            _entry_gen(),
            count=count,
            quantize=self.quantize,
        )

        # Patch count field in header if dedup reduced it
        if n_written != count:
            model_bytes = self.generator.model_name.encode()
            count_offset = 4 + 4 + 4 + len(model_bytes)  # magic+ver+model_len+model
            with open(emb_bin, "r+b") as fh:
                fh.seek(count_offset)
                fh.write(_struct.pack("<I", n_written))

        emb_size = emb_bin.stat().st_size / 1_048_576
        quant_str = " (SQ8 int8)" if self.quantize else " (float32)"
        print(f"  [OK] embeddings.bin  ({emb_size:.2f} MB){quant_str}")

        save_vectors_bin(vec_bin, self.generator.model_name, ordered_ids)
        print(f"  [OK] vectors.bin     ({vec_bin.stat().st_size / 1_048_576:.2f} MB)")
        print(f"       Vectors written: {n_written}")
        print(f"       Time:            {time.time() - t:.2f}s\n")

        return dim  # returned so process() summary block can use it

    def process(
        self, kb_path: Path, output_dir: Path, args: argparse.Namespace
    ) -> None:
        t_total = time.time()
        SEP = "=" * 70
        sep = "-" * 70

        print(f"\n{SEP}")
        print("  EULIX EMBED — EMBEDDING PIPELINE")
        print(f"  Engine : ONNX Runtime")
        print(f"  ijson backend : {ijson.backend}")
        print(f"  orjson        : {'yes' if _HAS_ORJSON else 'no (stdlib json)'}")
        print(f"  Chunk slots   : {'yes' if _DC_KW else 'no (Python <3.10)'}")
        print(f"  Quantization  : {'SQ8 int8' if self.quantize else 'float32'}")
        print(f"{SEP}\n")

        self._check_disk_space(output_dir, kb_path)

        # Step 1+2: Single-pass KB scan + chunk generation
        print("STEP 1+2: Knowledge Base scan + Chunk generation (single pass)")
        print(sep)
        t = time.time()

        meta: Dict[str, Any] = {}
        chunks: List[Chunk] = []
        seen_ids: Set[str] = set()
        n_files = n_funcs = n_classes = n_methods = 0
        ct_counts: Dict[str, int] = defaultdict(int)

        MAX_INFLIGHT = 32
        inflight: deque[Future] = deque()
        # Bounded producer/consumer: keep submitting file-chunking work to
        # the thread pool as files stream in from _stream_kb, but once
        # MAX_INFLIGHT futures are outstanding, drain half of them before
        # submitting more. This caps memory (each in-flight future holds
        # one file_struct + its future chunk list) without serializing
        # chunking behind KB parsing.

        def _submit(file_path: str, fs: dict) -> Future:
            max_size = self.max_chunk_size

            def _work():
                _drop_docstrings(fs)
                local_seen: Set[str] = set()
                return chunk_one_file(file_path, fs, max_size, local_seen)

            return executor.submit(_work)

        def _harvest(fut: Future) -> None:
            for chunk in fut.result():
                if chunk.id not in seen_ids:
                    seen_ids.add(chunk.id)
                    ct_counts[chunk.chunk_type.value] += 1
                    chunks.append(chunk)

        def _drain_inflight(max_remaining: int = 0) -> None:
            while len(inflight) > max_remaining:
                _harvest(inflight.popleft())

        N_WORKERS = min(4, (os.cpu_count() or 4))
        with ThreadPoolExecutor(max_workers=N_WORKERS) as executor:
            for event_type, *payload in _stream_kb(kb_path):
                if event_type == "meta":
                    key, value = payload
                    meta[key] = value

                elif event_type == "structure":
                    file_path, fs = payload
                    n_files += 1
                    n_funcs += len(fs.get("functions", []))
                    n_classes += len(fs.get("classes", []))
                    n_methods += sum(
                        len(c.get("methods", [])) for c in fs.get("classes", [])
                    )
                    inflight.append(_submit(file_path, fs))
                    if len(inflight) >= MAX_INFLIGHT:
                        _drain_inflight(max_remaining=MAX_INFLIGHT // 2)

            _drain_inflight(max_remaining=0)

        step12_time = time.time() - t
        n_chunks = len(chunks)

        print(f"  [OK] KB scanned + chunked in single pass")
        print(f"       Files:        {n_files}")
        print(f"       Functions:    {n_funcs}")
        print(f"       Classes:      {n_classes}")
        print(f"       Methods:      {n_methods}")
        print(f"       Total Chunks: {n_chunks}")
        print(f"       Chunk Breakdown:")
        for ct, cnt in sorted(ct_counts.items()):
            print(f"         {ct + ':':<22} {cnt}")
        print(f"       Time:         {step12_time:.2f}s\n")

        del seen_ids
        gc.collect()

        # Step 3+4: Embed + write (streaming, no full vector_store dict)
        dim = self._process_step3_step4(chunks, output_dir)

        # Summary
        total_elapsed = time.time() - t_total
        print(SEP)
        print("  PIPELINE SUMMARY")
        print(SEP)
        print(f"  Engine: ONNX")
        print(f"  Model:          {self.generator.model_name}")
        print(f"  Quantization:   {'SQ8 int8' if self.quantize else 'float32'}")
        print(f"  Dimension:      {dim}")
        print(f"  Total Chunks:   {n_chunks}")
        print(f"  Total Time:     {total_elapsed:.2f}s")
        print(SEP)
        print("  PIPELINE COMPLETED SUCCESSFULLY")
        print(f"{SEP}\n")

# [EXPERIMENTAL FOR NOW]
# SERVE MODE — long-lived stdin/stdout embedding server


def cmd_serve(args: argparse.Namespace) -> None:
    """
    Long-lived embedding server.  Keeps the model loaded in memory and handles
    one query per line, making it far cheaper than spawning a new process per
    call (which would reload the model every time).

    Protocol — newline-delimited JSON on stdin / stdout:
      stdin  (one JSON object per line):
        {"query": "<text>"}
            → embed the query text; reply with the vector
        {"queries": ["<text>", ...]}
            → batch embed; reply with a list of vectors (same order)
        {"ping": true}
            → health-check; reply {"pong": true, "model": "...", "dim": N}
        {"shutdown": true}
            → flush stdout and exit cleanly (exit code 0)

      stdout (one JSON object per line per request):
        {"embedding":  [f32, ...], "dimension": N, "model": "..."}
        {"embeddings": [[f32, ...], ...], "dimension": N, "model": "..."}
        {"pong": true, "model": "...", "dim": N}
        {"error": "<message>"}          ← on any per-request failure

    Stderr receives all human-readable startup/progress messages so the parent
    process can separate them from the protocol stream.

    Usage:
        python eulix_embed.py serve -m sentence-transformers/all-MiniLM-L6-v2
    """
    # All diagnostic output goes to stderr — stdout is the protocol channel.
    _err = sys.stderr

    print("  EULIX EMBED — SERVE MODE", file=_err)
    print(f"  Model:  {args.model}", file=_err)
    if args.device:
        print(f"  Device: {args.device}", file=_err)
    print("  Loading model…", file=_err)

    try:
        if args.engine == "onnx":
            gen = EmbeddingGeneratorOnnx(
                model_name=args.model,
                device=args.device,
                batch_size=args.batch_size,
            )
        else:
            gen = EmbeddingGenerator(
                model_name=args.model,
                device=args.device,
                batch_size=args.batch_size,
            )
    except SystemExit:
        sys.exit(1)

    # Signal readiness to the parent process — one line on stdout.
    _ready: Dict[str, Any] = {
        "ready": True,
        "model": gen.model_name,
        "dim": gen.dimension,
    }
    print(json.dumps(_ready), flush=True)
    print(f"  ✓ Ready — listening on stdin (dim={gen.dimension})", file=_err)

    stdin = sys.stdin
    stdout = sys.stdout

    for raw_line in stdin:
        raw_line = raw_line.strip()
        if not raw_line:
            continue  # skip blank lines (e.g. keep-alive pings from some callers)

        # Parse request
        try:
            req: Dict[str, Any] = json.loads(raw_line)
        except json.JSONDecodeError as exc:
            _reply: Dict[str, Any] = {"error": f"invalid JSON: {exc}"}
            print(_json_dumps(_reply), flush=True)
            continue

        # Dispatch
        try:
            # Health-check
            if req.get("ping"):
                resp: Dict[str, Any] = {
                    "pong": True,
                    "model": gen.model_name,
                    "dim": gen.dimension,
                }
                print(_json_dumps(resp), flush=True)
                continue

            # Clean shutdown
            if req.get("shutdown"):
                print(_json_dumps({"shutdown": "ok"}), flush=True)
                stdout.flush()
                print("  Shutting down (received shutdown request).", file=_err)
                sys.exit(0)

            # Batch embed: {"queries": ["text1", "text2", ...]}
            if "queries" in req:
                queries = req["queries"]
                if not isinstance(queries, list) or not all(
                    isinstance(q, str) for q in queries
                ):
                    raise ValueError('"queries" must be a JSON array of strings')
                if not queries:
                    raise ValueError('"queries" array is empty')

                # _embed_batch handles lists natively
                embs = gen._embed_batch(queries)
                resp = {
                    "embeddings": [e.tolist() for e in embs],
                    "dimension": gen.dimension,
                    "model": gen.model_name,
                }
                print(_json_dumps(resp), flush=True)
                continue

            # Single embed: {"query": "text"}
            if "query" in req:
                query = req["query"]
                if not isinstance(query, str):
                    raise ValueError('"query" must be a string')
                if not query:
                    raise ValueError('"query" is empty')

                emb = gen.embed_query(query)
                resp = {
                    "embedding": emb.tolist(),
                    "dimension": gen.dimension,
                    "model": gen.model_name,
                }
                print(_json_dumps(resp), flush=True)
                continue

            # Unknown request shape
            raise ValueError(
                'request must contain "query", "queries", "ping", or "shutdown"'
            )

        except Exception as exc:
            err_resp: Dict[str, Any] = {"error": str(exc)}
            print(_json_dumps(err_resp), flush=True)
            print(f"  [WARN] request error: {exc}", file=_err)
            continue

    # stdin closed (parent process exited / pipe broken)
    print("  stdin closed — exiting.", file=_err)


def ijson_check():
    """Check which ijson backend is active."""
    try:
        import ijson

        print(f"ijson backend: {ijson.backend}")
        if ijson.backend == "yajl2_c":
            print("  ✓ Fast C backend (yajl2_c) - optimal performance")
        elif ijson.backend == "yajl2_cffi":
            print("  ✓ C backend via CFFI - good performance")
        else:
            print(
                "  ⚠ Pure Python backend - slower, consider: pip install 'ijson[yajl2_cffi]'"
            )
    except ImportError:
        print("❌ ijson not installed")
        print("   Install with: uv pip install ijson")

def cmd_embed(args: argparse.Namespace) -> None:
    if args.engine == "onnx":
        PipelineClass = EmbeddingPipelineOnnx
    else:
        PipelineClass = EmbeddingPipeline

    pipeline = PipelineClass(
        model_name=args.model,
        max_chunk_size=args.max_chunk,
        device=args.device,
        batch_size=args.batch_size,
        save_json=args.save_json,
        quantize=args.quantize,
        debug=args.debug,
    )
    kb_path = Path(args.kb_path)
    if not kb_path.exists():
        print(f"[ERROR] KB file not found: {kb_path}", file=sys.stderr)
        sys.exit(1)
    pipeline.process(kb_path, Path(args.output), args)


def cmd_query(args: argparse.Namespace) -> None:
    if not args.query:
        print("[ERROR] --query is required", file=sys.stderr)
        sys.exit(1)

    if args.engine == "onnx":
        GeneratorClass = EmbeddingGeneratorOnnx
    else:
        GeneratorClass = EmbeddingGenerator

    gen = GeneratorClass(model_name=args.model)
    emb = gen.embed_query(args.query)

    if args.format == "json":
        out = {
            "query": args.query,
            "model": gen.model_name,
            "dimension": gen.dimension,
            "engine": args.engine,
            "embedding": emb.tolist(),
        }
        print(_json_dumps(out, indent=True))
    elif args.format == "binary":
        sys.stdout.buffer.write(_struct.pack("<I", len(emb)))
        sys.stdout.buffer.write(emb.astype("float32").tobytes())
    else:
        print(f"[ERROR] Unknown format '{args.format}'", file=sys.stderr)
        sys.exit(1)


def check_duplicate_ids(path: Path) -> List[str]:
    with open(path, "rb") as f:
        f.read(4)
        f.read(4)  # magic + version
        n = _struct.unpack("<I", f.read(4))[0]
        f.read(n)  # model name
        count, dim = _struct.unpack("<II", f.read(8))
        ids = []
        for _ in range(count):
            n = _struct.unpack("<I", f.read(4))[0]
            ids.append(f.read(n).decode())
            f.read(dim * 4)

    dupes = [id for id, cnt in Counter(ids).items() if cnt > 1]
    if dupes:
        print(f"\n⚠️ Found {len(dupes)} duplicate IDs in {path}:")
        for d in dupes:
            print(f"  {d}")

    return dupes

def cmd_compare(args: argparse.Namespace) -> None:
    np = _require_numpy()
    print("Comparing embeddings.bin ↔ vectors.bin...\n")
    emb_path = Path(args.emb)
    vec_path = Path(args.vec)

    load_embeddings_bin(emb_path)
    load_vectors_bin(vec_path)

def check_python_version():
    """
    Hard version gate: some pinned ML dependencies (torch/transformers
    versions used elsewhere in this project) aren't validated against
    Python 3.12+, so we fail fast with actionable instructions rather than
    letting the user hit a confusing downstream import error.
    """
    # Define your allowed Python boundaries
    MIN_VERSION = (3, 10)
    MAX_VERSION = (3, 11)  # Stop before 3.12+ if your ML libraries aren't ready

    current_version = sys.version_info[:2]

    if current_version < MIN_VERSION or current_version > MAX_VERSION:
        print("=" * 60)
        print("❌ CRITICAL: PYTHON VERSION COMPATIBILITY ERROR")
        print(f"Current version: Python {sys.version.split()[0]}")
        print(
            f"Required version: Between Python {MIN_VERSION[0]}.{MIN_VERSION[1]} and {MAX_VERSION[0]}.{MAX_VERSION[1]}"
        )
        print("=" * 60)
        print("\nPlease switch your virtual environment or Python installation.")
        print(
            "If using 'uv', you can recreate the environment with the correct version:"
        )
        print(f"  uv venv --python {MIN_VERSION[0]}.{MIN_VERSION[1]}")
        print("=" * 60)

        # Immediately halt execution with a non-zero exit code
        sys.exit(1)

def _build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="eulix_embed.py",
        description="Knowledge Base Embedding Generator (Python/PyTorch)",
        add_help=False,
    )
    sub = p.add_subparsers(dest="command")
    ep = sub.add_parser(
        "embed",
        help="Generate embeddings for knowledge base generated by eulix_parser (default)",
    )
    ep.add_argument(
        "-k",
        "--kb-path",
        default="knowledge_base.json",
        metavar="PATH",
        help="Path to knowledge base JSON  [default: knowledge_base.json]",
    )
    ep.add_argument(
        "-o",
        "--output",
        default="./embeddings",
        metavar="DIR",
        help="Output directory             [default: ./embeddings]",
    )
    ep.add_argument(
        "-m",
        "--model",
        default="sentence-transformers/all-MiniLM-L6-v2",
        metavar="NAME",
        help="HuggingFace model name",
    )
    ep.add_argument(
        "-e",
        "--engine",
        default="torch",
        choices=["torch", "onnx"],
        metavar="ENGINE",
        help="Inference engine: torch or onnx  [default: torch]",
    )
    ep.add_argument(
        "-d",
        "--device",
        default=None,
        metavar="DEVICE",
        help="cuda / mps / cpu             [default: auto]",
    )
    ep.add_argument(
        "-b",
        "--batch-size",
        type=int,
        default=None,
        metavar="N",
        help="Batch size                   [default: auto]",
    )
    ep.add_argument(
        "--max-chunk",
        type=int,
        default=2000,
        metavar="N",
        help="Max chunk chars              [default: 2000]",
    )
    ep.add_argument(
        "--quantize",
        action="store_true",
        help="SQ8 int8 quantization: 4x smaller embeddings.bin, ~1%% quality loss",
    )
    ep.add_argument(
        "--no-comments",
        action="store_true",
        help="Remove comments/docstrings/license headers from embedding and vidx bins",
    )
    ep.add_argument(
        "--save-json",
        action="store_true",
        help="Also write embeddings.json (enables graph edge streaming)",
    )
    ep.add_argument(
        "--debug", action="store_true", help="Print debug info during pipeline"
    )
    qp = sub.add_parser(
        "query", help="Generate embedding for a query string (one-shot)"
    )
    qp.add_argument(
        "-q", "--query", default="", metavar="TEXT", help="Query text to embed"
    )
    qp.add_argument(
        "-m",
        "--model",
        default="sentence-transformers/all-MiniLM-L6-v2",
        metavar="NAME",
        help="HuggingFace model name",
    )
    qp.add_argument(
        "-e",
        "--engine",
        default="torch",
        choices=["torch", "onnx"],
        metavar="ENGINE",
        help="Inference engine: torch or onnx  [default: torch]",
    )
    qp.add_argument(
        "-f",
        "--format",
        default="json",
        metavar="FMT",
        help="json | binary  [default: json]",
    )
    sp = sub.add_parser(
        "serve", help="Long-lived stdin/stdout embedding server (avoids model reload)"
    )
    sp.add_argument(
        "-m",
        "--model",
        default="sentence-transformers/all-MiniLM-L6-v2",
        metavar="NAME",
        help="HuggingFace model name  [default: all-MiniLM-L6-v2]",
    )
    sp.add_argument(
        "-e", "--engine",
        default="torch",
        choices=["torch", "onnx"],
        metavar="ENGINE",
        help="Inference engine: torch or onnx  [default: torch]",
    )
    sp.add_argument(
        "-d",
        "--device",
        default=None,
        metavar="DEVICE",
        help="cuda / mps / cpu  [default: auto]",
    )
    sp.add_argument(
        "-b",
        "--batch-size",
        type=int,
        default=None,
        metavar="N",
        help="Batch size for 'queries' requests  [default: auto]",
    )
    cp = sub.add_parser("compare", help="Compare embeddings.bin with vectors.bin")
    cp.add_argument("emb", nargs="?", default="embeddings/embeddings.bin")
    cp.add_argument("vec", nargs="?", default="embeddings/vectors.bin")
    sub.add_parser("ijson-backend", help="Report active ijson C-backend")
    vp = sub.add_parser("version", help="Print version information")
    vp.add_argument("--short", action="store_true", help="Print only version number")

    return p

_PARSER = _build_parser()


def print_help() -> None:
    _PARSER.print_help()
    # print()
    DETAIL_COMMANDS = ("embed", "query", "serve", "compare")
    sub_action = next(
        a for a in _PARSER._actions if isinstance(a, argparse._SubParsersAction)
    )

    for name, subparser in sub_action.choices.items():
        if name not in DETAIL_COMMANDS:
            continue
        print(f"{'=' * 60}")
        print(f"  {name.upper()}")
        print(f"{'=' * 60}")
        subparser.print_help()
        print()

def check_engine_dependencies(engine: str) -> None:
    """
    Check if the required dependencies for the selected engine are installed.
    This runs BEFORE any heavy imports, so we can fail fast with clear error messages.
    """
    if engine == "torch":
        required_packages = {
            "torch": "torch",
            "transformers": "transformers",
            "sentence_transformers": "sentence-transformers",  # Package name vs import name
            "tqdm": "tqdm",
            "numpy": "numpy",
        }
        engine_name = "PyTorch"
        install_cmd = "pip install torch transformers sentence-transformers tqdm numpy"

        if importlib.util.find_spec("uv") is not None:
            install_cmd = "uv pip install torch transformers sentence-transformers tqdm numpy"

    elif engine == "onnx":
        required_packages = {
            "onnxruntime": "onnxruntime",
            "transformers": "transformers",
            "tqdm": "tqdm",
            "numpy": "numpy",
        }
        engine_name = "ONNX Runtime"
        install_cmd = "pip install onnxruntime transformers tqdm numpy"

        if importlib.util.find_spec("uv") is not None:
            install_cmd = "uv pip install onnxruntime transformers tqdm numpy"
    else:
        raise ValueError(f"Unknown engine: {engine}")

    missing = []
    for import_name, package_name in required_packages.items():
        if not importlib.util.find_spec(import_name):
            missing.append(f"{package_name} (imports as '{import_name}')")

    if missing:
        print(f"❌ ERROR: Missing required dependencies for {engine_name} engine", file=sys.stderr)
        print(f"\nMissing packages: {', '.join(missing)}", file=sys.stderr)
        print(f"\nTo install all required dependencies:", file=sys.stderr)
        print(f"  {install_cmd}", file=sys.stderr)

        # Special note for ONNX GPU
        if engine == "onnx":
            print("\nFor GPU support with ONNX Runtime:", file=sys.stderr)
            print("  NVIDIA: pip install onnxruntime-gpu", file=sys.stderr)
            print("  AMD:    pip install onnxruntime-rocm", file=sys.stderr)

        print("\nOr use the other engine if you have it installed:", file=sys.stderr)
        other_engine = "onnx" if engine == "torch" else "torch"
        print(f"  python eulix_embed.py --engine {other_engine} ...", file=sys.stderr)
        sys.exit(1)

    print(f"  ✓ {engine_name} dependencies found", file=sys.stderr)

def main() -> None:
    if len(sys.argv) == 1 or sys.argv[1] in ("-h", "--help", "help"):
        print_help()
        sys.exit(0)

    if len(sys.argv) == 2 and sys.argv[1] in ("--version", "-V"):
        print(f"{Version}\n")
        sys.exit(0)

    args = _PARSER.parse_args()

    if args.command is None:
        # bare `eulix_embed.py` with no subcommand → default to embed
        embed_parser = next(
            a
            for a in _PARSER._subparsers._actions  # type: ignore[union-attr]
            if hasattr(a, "_name_parser_map")
        )._name_parser_map["embed"]
        args = embed_parser.parse_args(sys.argv[1:])
        args.command = "embed"

    if args.command in ("embed", "query", "serve", "compare"):
        check_python_version()

        # Check dependencies BEFORE importing heavy ML libraries
        # This only runs for commands that actually need the ML stack
        if args.command in ("embed", "query", "serve"):
            # Determine which engine to check
            engine = getattr(args, 'engine', 'torch')  # Default to torch if not specified
            check_engine_dependencies(engine)

            # Now it's safe to import the engine-specific code
            if args.command == "embed":
                cmd_embed(args)
            elif args.command == "query":
                cmd_query(args)
            elif args.command == "serve":
                cmd_serve(args)
        else:
            # compare command only needs numpy, not the full ML stack
            if args.command == "compare":
                cmd_compare(args)
    elif args.command == "ijson-backend":
        ijson_check()
    elif args.command == "version":
        if hasattr(args, "short") and args.short:
            print(Version)
        else:
            print(f"eulix-embed version {Version}")
            print(f"Python: {sys.version.split()[0]}")
            print(f"ijson backend: {ijson.backend}")
    else:
        print_help()
        sys.exit(1)

if __name__ == "__main__":
    main()
