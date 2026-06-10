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
# Python port of eulix_embed (Rust) — check embed branch for implementation.
# Differences from Rust version:
#   - PyTorch instead of ONNX Runtime
#   - Single-pass ijson streaming via parse() state machine — constant RAM
#     regardless of KB size. Peak ≈ max(one_file_struct, one_graph_edge).
#   - embeddings.bin / vectors.bin: binary format v3
#     header: magic(4) + version(4) + model_name_len(4) + model_name +
#             count(4) + dimension(4) + [id_len(4) + id_bytes + f32*dim] * N
#   - embeddings.json are optional (--save-json flag)
#   - Bucketing logic mirrors Rust exactly
#
# PERFORMANCE vs original Python port:
#   Old: two full passes over the KB file → ~2× I/O, ~14 GB RAM
#        (call_graph + dep_graph loaded entirely into Python dicts;
#         context_chunks was a full duplicate of all chunk content)
#   New: one pass, ObjectBuilder state machine streams structure entries,
#        call/dep edges; dataclass(slots=True) cuts Chunk RAM ~40%;
#        orjson gives 5-10× faster JSON serialisation.
#
# INSTALL:
#   pip install ijson[yajl2_cffi] orjson  # fast C backends (optional)
#   pip install ijson numpy torch transformers tqdm
# or use UV to maintain [RECOMENDED APPROACH]

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

# Optional fast JSON backend
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


# Define the wrapper function once
def _json_dumps(obj: Any, *, indent: bool = False) -> str:
    """Fast JSON dumps – uses orjson when available, falls back to json."""
    if indent:
        return _json_dumps_indent(obj)
    else:
        return _json_dumps_no_indent(obj)


# ijson ObjectBuilder — used by the single-pass state machine
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

    return np, torch, F, AutoModel, AutoTokenizer, tqdm


def _require_numpy():
    """Lightweight path: only numpy (used by save_*/load_* bin helpers)."""
    import numpy as np

    return np


# Constants — mirrors Rust exactly
BUCKETS_STANDARD: List[int] = [32, 64, 128, 192, 256, 384, 512]
BUCKETS_JINA: List[int] = [32, 64, 128, 192, 256, 384, 512, 768, 1024, 2048, 4096, 8192]

BINARY_MAGIC = b"EULX"
BINARY_VERSION = 4
# VECTOR_MAGIC = b"EULX"
Version = "0.3.7"  # different from Binary and vector magic

# Dataclass slots: Python ≥3.10 natively; earlier versions fall back gracefully.
_DC_KW: Dict[str, Any] = {"slots": True} if sys.version_info >= (3, 10) else {}


# Snap-to-bucket helper — identical to Rust
def snap_to_bucket(seq_len: int, buckets: List[int]) -> int:
    for b in buckets:
        if seq_len <= b:
            return b
    return buckets[-1]


def _sq8_encode(vec: "np.ndarray") -> tuple["np.ndarray", float]:
    """
    Scalar-quantize a float32 vector to int8.

    Returns (int8_vec, scale) where:
        dequantized ≈ int8_vec.astype(float32) * scale
        scale       = max(|vec|) / 127.0
    Handles zero vectors safely.
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


# ChunkType
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
    Single-pass generator over the Knowledge Base JSON using ijson.parse().

    Yields one of:
      ('meta',      key: str,       value: Any)        — small top-level keys
      ('structure', file_path: str, file_struct: dict) — one per source file

    Memory profile

      Collected:  metadata + entry_points + patterns + external_dependencies
                  These are intentionally small (no structure / graphs).
      Streaming:  one file_struct at a time  (a few KB–MB each)
                  one edge dict at a time    (a few hundred bytes)
      Peak ≈ size of a single large file_struct + a few hundred bytes per edge.

    Why not two passes?
    ─
      The old code made two full passes (load_kb_metadata → stream_kb_structure).
      For a 4.6 GB file that is 9.2 GB of disk I/O and loads call_graph /
      dependency_graph entirely into Python dicts (10–20× RAM overhead).
      Here we read the file exactly once and skip or stream-parse every key.
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

            #  Stream structure entries one file at a time ─
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


def _fmt_function_with_context(func: Dict, file_path: str) -> str:
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
def strip_comments(content: str, lang: str) -> str:
    """Remove comments from source code based on language."""
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
    Produce all Chunk objects for a single file struct.
    Docstrings are stripped by _drop_docstrings() in _submit before this
    is called, so they never appear in chunk content or stay in RAM.
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


# PYTORCH EMBEDDER


class EmbeddingGenerator:
    """Wraps a HuggingFace model for batched embedding generation.
    Mirrors the Rust EmbeddingGenerator including MIGraphX bucket logic."""

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

                    self._st_model = SentenceTransformer(model_name, device=self.device)
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
                    self.tokenizer = AutoTokenizer.from_pretrained(model_name)
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
            # Probe dimension
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


# BINARY I/O — v3 format (unchanged from original)


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
    Save embeddings binary.  v4 format adds a 1-byte quant flag
    immediately after the dimension field.

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
        fh = io.BufferedWriter(raw, buffer_size=4*1024*1024)
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
    Load embeddings.bin — handles v3 (float32) and v4 (float32 or SQ8).

    Returns list of (id, float32_array) when dequantize=True (default),
    or (id, int8_array, scale) when dequantize=False and file is quantized.
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
    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        max_chunk_size: int = 2000,
        device: Optional[str] = None,
        batch_size: Optional[int] = None,
        save_json: bool = False,
        quantize: bool = False,  # fixed typo: quanize → quantize
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
        print("  EULIX EMBED — EMBEDDING PIPELINE (Python/PyTorch)")
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
        gen = EmbeddingGenerator(
            model_name=args.model,
            device=args.device,
            batch_size=args.batch_size,
        )
    except SystemExit:
        # EmbeddingGenerator already printed the error to stderr
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


def print_help() -> None:
    print("""eulix_embed.py — Knowledge Base Embedding Generator (Python/PyTorch)

USAGE:
    python eulix_embed.py [COMMAND] [OPTIONS]

COMMANDS:
    embed    Generate embeddings for a knowledge base (default)
    query    Generate embedding for a query string (one-shot)
    serve    Long-lived stdin/stdout embedding server (avoids model reload)
    compare  Compare embeddings.bin with vectors.bin

EMBED OPTIONS:
    -k / --kb-path   <PATH>    Path to knowledge base JSON   [default: knowledge_base.json]
    -o / --output    <DIR>     Output directory               [default: ./embeddings]
    -m / --model     <NAME>    HuggingFace model name
    --device         <DEVICE>  cuda / mps / cpu               [default: auto]
    --batch-size     <N>       Batch size                     [default: auto]
    --max-chunk      <N>       Max chunk chars                [default: 2000]
    --save-json                Also write embeddings.json
                               (enables graph edge streaming)

QUERY OPTIONS:
    -q / --query     <TEXT>    Query text to embed
    -m / --model     <NAME>    HuggingFace model name
    -f / --format    <FMT>     json | binary                  [default: json]

SERVE OPTIONS:
    -m / --model     <NAME>    HuggingFace model name         [default: all-MiniLM-L6-v2]
    -d / --device    <DEVICE>  cuda / mps / cpu               [default: auto]
    -b / --batch-size <N>      Batch size for "queries" reqs  [default: auto]

    Protocol — newline-delimited JSON on stdin/stdout:
      Send:  {"query": "some text"}
      Recv:  {"embedding": [...], "dimension": 384, "model": "..."}

      Send:  {"queries": ["text1", "text2"]}
      Recv:  {"embeddings": [[...], [...]], "dimension": 384, "model": "..."}

      Send:  {"ping": true}
      Recv:  {"pong": true, "model": "...", "dim": 384}

      Send:  {"shutdown": true}
      Recv:  {"shutdown": "ok"}   (process exits cleanly)

    On startup, the server emits one ready line to stdout before accepting input:
      {"ready": true, "model": "...", "dim": 384}

    All human-readable logs (model loading, warnings) go to stderr.

PERFORMANCE TIPS:
    pip install ijson[yajl2_cffi]   # 10× faster C-backend for JSON streaming
    pip install orjson              # 5-10× faster JSON serialisation

SUPPORTED MODELS:
    sentence-transformers/all-MiniLM-L6-v2
    BAAI/bge-small-en-v1.5
    BAAI/bge-base-en-v1.5
    jinaai/jina-embeddings-v2-base-code  (8192 tokens)
""")


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
    pipeline = EmbeddingPipeline(
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
    gen = EmbeddingGenerator(model_name=args.model)
    emb = gen.embed_query(args.query)
    if args.format == "json":
        out = {
            "query": args.query,
            "model": gen.model_name,
            "dimension": gen.dimension,
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


def main() -> None:
    if len(sys.argv) == 1 or sys.argv[1] in ("-h", "--help"):
        print_help()
        sys.exit(0)

    if len(sys.argv) == 2 and sys.argv[1] in ("--version", "-V"):
        print(f"{Version}\n")
        sys.exit(0)

    command = sys.argv[1]
    valid_commands = ("embed", "query", "serve", "compare", "ijson-backend", "version")
    if command not in valid_commands:
        print_help()
        sys.exit(1)

    # Check Python version only for commands that need ML libraries
    if command in ("embed", "query", "serve", "compare"):
        check_python_version()

    p = argparse.ArgumentParser(add_help=False)
    sub = p.add_subparsers(dest="command")

    ep = sub.add_parser("embed")
    ep.add_argument("-k", "--kb-path", default="knowledge_base.json")
    ep.add_argument("-o", "--output", default="./embeddings")
    ep.add_argument("-m", "--model", default="sentence-transformers/all-MiniLM-L6-v2")
    ep.add_argument("-d", "--device", default=None)
    ep.add_argument("-b", "--batch-size", type=int, default=None)
    ep.add_argument(
        "--quantize",
        action="store_true",
        help="SQ8 int8 quantization: 4x smaller embeddings.bin, ~1%% quality loss",
    )
    ep.add_argument(
        "-rcom",
        "--remove-comments",
        action="store_true",
        help="Removes comments, docstring, license headers from embedding and vidx bins",
    )
    ep.add_argument("--max-chunk", type=int, default=2000)
    ep.add_argument(
        "--save-json",
        action="store_true",
        help="[Use for Debugging] Save a json copy of embedder.bin and vector.bin",
    )
    ep.add_argument(
        "--debug", action="store_true", help="Print debug info during pipeline"
    )

    qp = sub.add_parser("query")
    qp.add_argument("-q", "--query", default="")
    qp.add_argument("-m", "--model", default="sentence-transformers/all-MiniLM-L6-v2")
    qp.add_argument("-f", "--format", default="json")

    sp = sub.add_parser("serve")
    sp.add_argument(
        "-m",
        "--model",
        default="sentence-transformers/all-MiniLM-L6-v2",
        help="HuggingFace model name",
    )
    sp.add_argument(
        "-d",
        "--device",
        default=None,
        help="cuda / mps / cpu  (default: auto-detect)",
    )
    sp.add_argument(
        "-b",
        "--batch-size",
        type=int,
        default=None,
        help="Batch size for 'queries' requests (default: auto)",
    )

    cp = sub.add_parser("compare")
    cp.add_argument("emb", nargs="?", default="embeddings/embeddings.bin")
    cp.add_argument("vec", nargs="?", default="embeddings/vectors.bin")

    tp = sub.add_parser("ijson-backend")

    # Add version subparser
    vp = sub.add_parser("version")
    vp.add_argument("--short", action="store_true", help="Print only version number")

    args = p.parse_args()

    if args.command is None:
        args = ep.parse_args(sys.argv[1:])
        args.command = "embed"

    if args.command == "embed":
        cmd_embed(args)
    elif args.command == "query":
        cmd_query(args)
    elif args.command == "serve":
        cmd_serve(args)
    elif args.command == "compare":
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
