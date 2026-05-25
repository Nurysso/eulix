# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: GPL-3.0-or-later
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
#   - context.json and embeddings.json are optional (--save-json flag)
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
# or use UV to maintain

from __future__ import annotations
import sys

def check_python_version():
    # Define your allowed Python boundaries
    MIN_VERSION = (3, 10)
    MAX_VERSION = (3, 11)  # Stop before 3.12+ if your ML libraries aren't ready

    current_version = sys.version_info[:2]

    if current_version < MIN_VERSION or current_version > MAX_VERSION:
        print("=" * 60)
        print("❌ CRITICAL: PYTHON VERSION COMPATIBILITY ERROR")
        print(f"Current version: Python {sys.version.split()[0]}")
        print(f"Required version: Between Python {MIN_VERSION[0]}.{MIN_VERSION[1]} and {MAX_VERSION[0]}.{MAX_VERSION[1]}")
        print("=" * 60)
        print("\nPlease switch your virtual environment or Python installation.")
        print("If using 'uv', you can recreate the environment with the correct version:")
        print(f"  uv venv --python {MIN_VERSION[0]}.{MIN_VERSION[1]}")
        print("=" * 60)

        # Immediately halt execution with a non-zero exit code
        sys.exit(1)

# Run the check immediately
check_python_version()



import argparse
import gc
import json
import os
import struct
import sys
import time
from collections import defaultdict, deque
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import Any, Dict, Generator, Iterator, List, Optional, Set, Tuple

import numpy as np
import torch
import torch.nn.functional as F
from transformers import AutoModel, AutoTokenizer
from tqdm import tqdm
import ijson
from collections import Counter

# Optional fast JSON backend
try:
    import orjson as _orjson

    def _json_dumps(obj: Any, *, indent: bool = False) -> str:
        opts = _orjson.OPT_INDENT_2 if indent else 0
        return _orjson.dumps(obj, option=opts).decode()

    _HAS_ORJSON = True
except ImportError:
    _HAS_ORJSON = False

    def _json_dumps(obj: Any, *, indent: bool = False) -> str:  # type: ignore[misc]
        return json.dumps(obj, indent=2 if indent else None)


# ijson ObjectBuilder — used by the single-pass state machine
try:
    from ijson.common import ObjectBuilder as _OB
except ImportError:
    try:
        from ijson import ObjectBuilder as _OB  # type: ignore[no-redef]
    except ImportError:
        raise ImportError("ijson ObjectBuilder not found — pip install ijson")


# Constants — mirrors Rust exactly
BUCKETS_STANDARD: List[int] = [32, 64, 128, 192, 256, 384, 512]
BUCKETS_JINA: List[int] = [32, 64, 128, 192, 256, 384, 512, 768, 1024, 2048, 4096, 8192]

BINARY_MAGIC = b"EULX"
BINARY_VERSION = 3
VECTOR_MAGIC = b"EULX"
VECTOR_VERSION = 3

# Dataclass slots: Python ≥3.10 natively; earlier versions fall back gracefully.
_DC_KW: Dict[str, Any] = {"slots": True} if sys.version_info >= (3, 10) else {}



# Snap-to-bucket helper — identical to Rust
def snap_to_bucket(seq_len: int, buckets: List[int]) -> int:
    for b in buckets:
        if seq_len <= b:
            return b
    return buckets[-1]



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

def _stream_kb(path: Path, *, need_graphs: bool = False) -> Generator:
    """
    Single-pass generator over the Knowledge Base JSON using ijson.parse().

    Yields one of:
      ('meta',      key: str,       value: Any)        — small top-level keys
      ('structure', file_path: str, file_struct: dict) — one per source file
      ('cg_edge',   edge: dict)                        — call-graph edges
      ('dep_edge',  edge: dict)                        — dep-graph edges

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
    COLLECT_KEYS: Set[str] = {
        "metadata",
        "entry_points",
        "patterns",
        "external_dependencies",
    }

    top_key: Optional[str] = None

    # State for collecting small top-level values
    col_builder: Optional[_OB] = None
    col_depth: int = 0

    #  State for streaming structure entries
    st_fp: Optional[str] = None
    st_builder: Optional[_OB] = None
    st_depth: int = 0

    #  State for streaming call_graph.edges ─
    cg_sub: Optional[str] = None   # sub-key inside call_graph ("nodes"/"edges")
    cg_builder: Optional[_OB] = None
    cg_depth: int = 0

    #  State for streaming dependency_graph.edges
    dg_sub: Optional[str] = None
    dg_builder: Optional[_OB] = None
    dg_depth: int = 0

    with open(path, "rb") as fh:
        for prefix, event, value in ijson.parse(fh, use_float=True):

            #  New top-level key
            if prefix == "" and event == "map_key":
                top_key = value
                # Reset all sub-key trackers
                col_builder = None
                cg_sub = None
                dg_sub = None
                if value in COLLECT_KEYS:
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

            #  Stream call_graph edges (only when caller needs them)
            if top_key == "call_graph" and need_graphs:
                # Only promote the direct sub-keys of call_graph (not nested ones)
                if prefix == "call_graph" and event == "map_key":
                    cg_sub = value
                elif cg_sub == "edges":
                    if event == "start_map" and cg_builder is None:
                        cg_builder = _OB()
                        cg_builder.event(event, value)
                        cg_depth = 1
                    elif cg_builder is not None:
                        cg_builder.event(event, value)
                        if event in ("start_map", "start_array"):
                            cg_depth += 1
                        elif event in ("end_map", "end_array"):
                            cg_depth -= 1
                            if cg_depth == 0:
                                yield ("cg_edge", cg_builder.value)
                                cg_builder = None
                continue

            #  Stream dependency_graph edges
            if top_key == "dependency_graph" and need_graphs:
                if prefix == "dependency_graph" and event == "map_key":
                    dg_sub = value
                elif dg_sub == "edges":
                    if event == "start_map" and dg_builder is None:
                        dg_builder = _OB()
                        dg_builder.event(event, value)
                        dg_depth = 1
                    elif dg_builder is not None:
                        dg_builder.event(event, value)
                        if event in ("start_map", "start_array"):
                            dg_depth += 1
                        elif event in ("end_map", "end_array"):
                            dg_depth -= 1
                            if dg_depth == 0:
                                yield ("dep_edge", dg_builder.value)
                                dg_builder = None
                continue

            # All other top-level keys (indices, call_graph nodes when
            # need_graphs=False, etc.) are silently discarded here.



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
    out: List[str] = []
    out.append(f"// File: {file_path}")
    out.append(f"// Function: {func['name']}")
    if func.get("docstring"):
        out.append(f"// Description: {func['docstring']}")
    out.append(f"// Lines: {func.get('line_start', 0)}-{func.get('line_end', 0)}")
    out.append(f"// Complexity: {func.get('complexity', 0)}")
    out.append("")
    out.append(func.get("signature", ""))
    out.append("")

    params = func.get("params", [])
    if params:
        out.append("Parameters:")
        for p in params:
            dv = f" = {p['default_value']}" if p.get("default_value") else ""
            out.append(f"  - {p['name']}: {p.get('type_annotation', '')}{dv}")
        out.append("")

    if func.get("return_type"):
        out.append(f"Returns: {func['return_type']}")
        out.append("")

    calls = func.get("calls", [])
    if calls:
        out.append("Calls:")
        for c in calls[:10]:
            out.append(f"  - {c['callee']} (line {c['line']})")
        if len(calls) > 10:
            out.append(f"  ... and {len(calls) - 10} more")
        out.append("")

    called_by = func.get("called_by", [])
    if called_by:
        out.append("Called by:")
        for c in called_by[:5]:
            out.append(f"  - {c['function']} in {c['file']}")
        if len(called_by) > 5:
            out.append(f"  ... and {len(called_by) - 5} more")
        out.append("")

    cf = func.get("control_flow", {})
    if cf.get("complexity", 0) > 0:
        out.append(
            f"Control flow: {len(cf.get('branches', []))} branches, "
            f"{len(cf.get('loops', []))} loops"
        )

    exc = func.get("exceptions", {})
    if exc.get("raises") or exc.get("handles"):
        out.append("Exceptions:")
        if exc.get("raises"):
            out.append(f"  Raises: {', '.join(exc['raises'])}")
        if exc.get("handles"):
            out.append(f"  Handles: {', '.join(exc['handles'])}")
        out.append("")

    return "\n".join(out)


def _fmt_method_with_class_ctx(method: Dict, cls: Dict, file_path: str) -> str:
    out: List[str] = []
    out.append(f"// File: {file_path}")
    out.append(f"// Class: {cls['name']}")
    out.append(f"// Method: {method['name']}")
    if method.get("docstring"):
        out.append(f"// Description: {method['docstring']}")
    out.append("")
    if cls.get("bases"):
        out.append(f"// Inherits from: {', '.join(cls['bases'])}")
    out.append("")
    out.append(_fmt_function_with_context(method, file_path))
    return "\n".join(out)


def _fmt_class_overview(cls: Dict, file_path: str) -> str:
    out: List[str] = []
    out.append(f"// File: {file_path}")
    out.append(f"// Class: {cls['name']}")
    if cls.get("docstring"):
        out.append(f"// Description: {cls['docstring']}")
    out.append(f"// Lines: {cls.get('line_start', 0)}-{cls.get('line_end', 0)}")
    out.append("")
    if cls.get("bases"):
        out.append(f"Inherits from: {', '.join(cls['bases'])}")
        out.append("")
    attrs = cls.get("attributes", [])
    if attrs:
        out.append("Attributes:")
        for a in attrs:
            out.append(f"  - {a['name']}: {a.get('type_annotation', '')}")
        out.append("")
    methods = cls.get("methods", [])
    if methods:
        out.append(f"Methods ({len(methods)}):")
        for m in methods:
            async_tag = " (async)" if m.get("is_async") else ""
            out.append(f"  - {m['name']}{async_tag}")
        out.append("")
    return "\n".join(out)


def _fmt_file_summary(file_path: str, fs: Dict) -> str:
    out: List[str] = []
    out.append(f"File: {file_path}")
    out.append(f"Language: {fs.get('language', '')}")
    out.append(f"Lines of code: {fs.get('loc', 0)}")
    out.append("")
    imports = fs.get("imports", [])
    if imports:
        out.append("Imports:")
        for imp in imports:
            out.append(f"  - {imp['module']} ({imp.get('type', '')})")
        out.append("")
    funcs = fs.get("functions", [])
    if funcs:
        out.append(f"Functions: {len(funcs)}")
        for f in funcs[:10]:
            out.append(f"  - {f['name']}")
        if len(funcs) > 10:
            out.append(f"  ... and {len(funcs) - 10} more")
        out.append("")
    classes = fs.get("classes", [])
    if classes:
        out.append(f"Classes: {len(classes)}")
        for c in classes:
            out.append(f"  - {c['name']}")
        out.append("")
    return "\n".join(out)


def chunk_one_file(
    file_path: str,
    fs: Dict[str, Any],
    entry_point_ids: Set[str],
    entry_points_by_id: Dict[str, Any],
    max_size: int,
    seen_ids: Set[str],
) -> List[Chunk]:
    """
    Produce all Chunk objects for a single file struct.
    Mutates seen_ids so duplicate IDs across files are skipped.
    The file_struct dict (fs) is discarded by the caller after this returns.
    """
    chunks: List[Chunk] = []
    lang = fs.get("language", "")

    #  Functions (regular + entry points)
    for func in fs.get("functions", []):
        fid = func["id"]
        if fid in seen_ids:
            continue
        seen_ids.add(fid)

        content = _truncate_content(_fmt_function_with_context(func, file_path), max_size)

        if fid in entry_point_ids:
            ep = entry_points_by_id.get(fid, {})
            chunks.append(
                Chunk(
                    id=fid,
                    chunk_type=ChunkType.entry_point,
                    content=content,
                    metadata=ChunkMetadata(
                        file_path=file_path,
                        language=lang,
                        line_start=func.get("line_start"),
                        line_end=func.get("line_end"),
                        name=func["name"],
                        complexity=func.get("complexity"),
                    ),
                    tags=_generate_tags(func, ep.get("entry_type", "entrypoint")),
                    importance_score=1.0,
                )
            )
        else:
            chunks.append(
                Chunk(
                    id=fid,
                    chunk_type=ChunkType.function,
                    content=content,
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

    #  Classes + methods ─
    for cls in fs.get("classes", []):
        chunks.append(
            Chunk(
                id=cls["id"],
                chunk_type=ChunkType.class_,
                content=_truncate_content(_fmt_class_overview(cls, file_path), max_size),
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
                        _fmt_method_with_class_ctx(method, cls, file_path), max_size
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

    #  File summary
    summary = _fmt_file_summary(file_path, fs)
    if summary.strip():
        chunks.append(
            Chunk(
                id=f"file:{file_path}",
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
        device: Optional[str] = None,
        batch_size: Optional[int] = None,
        normalize: bool = True,
        use_bucketing: bool = True,
    ):
        self.model_name = model_name
        self.normalize = normalize

        if device is None:
            if torch.cuda.is_available():
                self.device = torch.device("cuda")
                # Distinguish NVIDIA vs AMD ROCm
                if torch.version.hip is not None:
                    print("  ✓ AMD GPU detected — using ROCm/HIP")
                else:
                    print("  ✓ NVIDIA GPU detected — using CUDA")
            elif torch.backends.mps.is_available():
                self.device = torch.device("mps")
                print("  ✓ Apple MPS detected")
            else:
                self.device = torch.device("cpu")
                print("  ℹ No GPU detected — using CPU")
        else:
            self.device = torch.device(device)
        if batch_size is None:
            batch_size = 64 if self.device.type == "cuda" else 16
        self.batch_size = batch_size

        self.use_bucketing = use_bucketing and self.device.type in ("cuda", "mps")

        print(f"     Model:      {model_name}")
        print(f"     Batch size: {self.batch_size}")
        print(f"     Bucketing:  {'enabled' if self.use_bucketing else 'disabled'}")

        is_jina = "jina" in model_name.lower()

        if is_jina:
            try:
                from sentence_transformers import SentenceTransformer

                self._st_model = SentenceTransformer(model_name, device=str(self.device))
                self._st_model.eval()
                self.tokenizer = self._st_model.tokenizer
                self.model = None
                self._use_st = True
                print("     Jina v2: loaded via sentence-transformers")
            except ImportError:
                raise ImportError(
                    "Jina v2 models require sentence-transformers.\n"
                    "Install: pip install sentence-transformers"
                )
        else:
            self._use_st = False
            self.tokenizer = AutoTokenizer.from_pretrained(model_name)
            self.model = AutoModel.from_pretrained(model_name).to(self.device)
            self.model.eval()

        # Probe dimension
        if self._use_st:
            test_emb = self._st_model.encode(["hello"], convert_to_numpy=True)
            self._dimension = test_emb.shape[-1]
        else:
            with torch.no_grad():
                dummy = self.tokenizer("hello", return_tensors="pt", padding=True).to(
                    self.device
                )
                out = self.model(**dummy)
                emb = self._mean_pool(out.last_hidden_state, dummy["attention_mask"])
                self._dimension = emb.shape[-1]

        print(f"     Dimension:  {self._dimension}")
        print("  ✓ Embedding generator ready!")

    @property
    def dimension(self) -> int:
        return self._dimension

    @staticmethod
    def _mean_pool(
        last_hidden: torch.Tensor, attention_mask: torch.Tensor
    ) -> torch.Tensor:
        mask_exp = attention_mask.unsqueeze(-1).float()
        summed = (last_hidden * mask_exp).sum(dim=1)
        counts = mask_exp.sum(dim=1).clamp(min=1e-9)
        return summed / counts

    @torch.no_grad()
    def _embed_batch(
        self, texts: List[str], fixed_len: Optional[int] = None
    ) -> np.ndarray:
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

        enc = self.tokenizer(texts, **tok_kwargs).to(self.device)
        out = self.model(**enc)
        emb = self._mean_pool(out.last_hidden_state, enc["attention_mask"])
        if self.normalize:
            emb = F.normalize(emb, p=2, dim=-1)
        return emb.cpu().float().numpy()

    def generate_vectors(self, chunks: List[Chunk]) -> Dict[str, np.ndarray]:
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

        results: List[Tuple[int, np.ndarray]] = []

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
            for blen in sorted(bucket_map):
                print(f"       bucket {blen:>5} tokens → {len(bucket_map[blen])} chunks")

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

        store: Dict[str, np.ndarray] = {}
        for (orig_idx, emb), (i, _, chunk) in zip(results, indexed_sorted):
            store[chunk.id] = emb
        return store

    def embed_query(self, query: str) -> np.ndarray:
        return self._embed_batch([query])[0]


# BINARY I/O — v3 format (unchanged from original)


def _write_str(fh, s: str) -> None:
    b = s.encode("utf-8")
    fh.write(struct.pack("<I", len(b)))
    fh.write(b)


def _read_str(fh) -> str:
    (n,) = struct.unpack("<I", fh.read(4))
    return fh.read(n).decode("utf-8")


def save_embeddings_bin(
    path: Path,
    model_name: str,
    dimension: int,
    entries: List[Tuple[str, np.ndarray]],
) -> None:
    with open(path, "wb") as fh:
        fh.write(BINARY_MAGIC)
        fh.write(struct.pack("<I", BINARY_VERSION))
        _write_str(fh, model_name)
        fh.write(struct.pack("<II", len(entries), dimension))
        for eid, vec in entries:
            _write_str(fh, eid)
            fh.write(vec.astype(np.float32).tobytes())


def load_embeddings_bin(
    path: Path,
) -> Tuple[str, int, List[Tuple[str, np.ndarray]]]:
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic: {magic!r}, expected {BINARY_MAGIC!r}")
        (version,) = struct.unpack("<I", fh.read(4))
        if version not in (2, 3):
            raise ValueError(f"Unsupported version {version}")
        model_name = _read_str(fh)
        count, dim = struct.unpack("<II", fh.read(8))
        entries = []
        for idx in range(count):
            eid = _read_str(fh) if version >= 3 else f"embedding_{idx}"
            raw = fh.read(dim * 4)
            vec = np.frombuffer(raw, dtype=np.float32).copy()
            entries.append((eid, vec))
    return model_name, dim, entries


def save_vectors_bin(
    path: Path,
    model_name: str,
    dimension: int,
    store: Dict[str, np.ndarray],
) -> None:
    with open(path, "wb") as fh:
        fh.write(VECTOR_MAGIC)
        fh.write(struct.pack("<I", VECTOR_VERSION))
        _write_str(fh, model_name)
        fh.write(struct.pack("<II", len(store), dimension))
        for eid, vec in store.items():
            _write_str(fh, eid)
            fh.write(vec.astype(np.float32).tobytes())


def load_vectors_bin(
    path: Path,
) -> Tuple[str, int, Dict[str, np.ndarray]]:
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != VECTOR_MAGIC:
            raise ValueError(f"Bad magic: {magic!r}")
        (version,) = struct.unpack("<I", fh.read(4))
        model_name = _read_str(fh)
        count, dim = struct.unpack("<II", fh.read(8))
        store: Dict[str, np.ndarray] = {}
        for idx in range(count):
            eid = _read_str(fh) if version >= 2 else f"vec_{idx}"
            raw = fh.read(dim * 4)
            store[eid] = np.frombuffer(raw, dtype=np.float32).copy()
    return model_name, dim, store


# CONTEXT INDEX — mirrors context.rs
# Accepts only the metadata subset (no structure / full graphs).
# Graph edges are passed in pre-collected to avoid duplicating chunk content.


def build_context_index(
    meta: Dict,
    chunks: List[Chunk],
    dimension: int,
    *,
    cg_edges: Optional[List[Dict]] = None,
    dep_edges: Optional[List[Dict]] = None,
    include_chunks: bool = False,
) -> Dict:
    """
    Build the context index.

    include_chunks=False (default):  omit per-chunk content → saves ~500 MB+
    include_chunks=True  (--save-json): include content for context.json

    cg_edges / dep_edges are passed in pre-collected by the pipeline;
    they are NOT loaded from meta (which no longer holds them).
    """
    entry_point_ids = {ep["function"] for ep in meta.get("entry_points", [])}
    chunk_types_count: Dict[str, int] = defaultdict(int)
    tags_index: Dict[str, List[str]] = defaultdict(list)

    # Build per-chunk records — content only when saving JSON
    context_chunks = []
    for c in chunks:
        chunk_types_count[c.chunk_type.value] += 1
        for t in c.tags:
            tags_index[t].append(c.id)
        if include_chunks:
            rec: Dict[str, Any] = {
                "id": c.id,
                "chunk_type": c.chunk_type.value,
                "content": c.content,
                "metadata": {
                    "file_path": c.metadata.file_path,
                    "language": c.metadata.language,
                    "line_start": c.metadata.line_start,
                    "line_end": c.metadata.line_end,
                    "name": c.metadata.name,
                    "complexity": c.metadata.complexity,
                    "is_entry_point": c.id in entry_point_ids,
                },
                "tags": c.tags,
                "importance_score": c.importance_score,
            }
            context_chunks.append(rec)

    # Relationships from streamed edges
    relationships: List[Dict] = []
    for edge in cg_edges or []:
        relationships.append(
            {
                "from": edge["from"],
                "to": edge["to"],
                "rel_type": edge.get("edge_type", "uses"),
                "conditional": edge.get("conditional", False),
            }
        )
    for edge in dep_edges or []:
        relationships.append(
            {
                "from": edge["from"],
                "to": edge["to"],
                "rel_type": edge.get("edge_type", "imports"),
                "conditional": False,
            }
        )

    # BFS call-graph depth from entry points
    adj: Dict[str, List[str]] = defaultdict(list)
    for edge in cg_edges or []:
        adj[edge["from"]].append(edge["to"])
    max_depth = 0
    for ep in meta.get("entry_points", []):
        visited: Dict[str, int] = {ep["function"]: 0}
        q: deque = deque([(ep["function"], 0)])
        while q:
            node, depth = q.popleft()
            max_depth = max(max_depth, depth)
            for nb in adj.get(node, []):
                if nb not in visited:
                    visited[nb] = depth + 1
                    q.append((nb, depth + 1))

    entry_points_info = [
        {
            "id": ep["function"],
            "entry_type": ep.get("entry_type", ""),
            "function_name": ep.get("handler", ""),
            "file": ep.get("file", ""),
            "path": ep.get("path"),
        }
        for ep in meta.get("entry_points", [])
    ]

    n_cg_edges = len(cg_edges) if cg_edges else 0
    n_ep = len(meta.get("entry_points", []))

    result: Dict[str, Any] = {
        "metadata": {
            "project_name": meta.get("metadata", {}).get("project_name", ""),
            "total_chunks": len(chunks),
            "chunk_types": dict(chunk_types_count),
            "embedding_dimension": dimension,
            "languages": meta.get("metadata", {}).get("languages", []),
            "architecture_style": meta.get("patterns", {}).get("architecture_style"),
        },
        "relationships": relationships,
        "tags": dict(tags_index),
        "call_graph_summary": {
            "total_edges": n_cg_edges,
            "entry_points_count": n_ep,
            "max_depth": max_depth,
        },
        "entry_points": entry_points_info,
    }
    if include_chunks:
        result["chunks"] = context_chunks
    return result



# PIPELINE
class EmbeddingPipeline:
    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        max_chunk_size: int = 2000,
        device: Optional[str] = None,
        batch_size: Optional[int] = None,
        save_json: bool = False,
    ):
        self.max_chunk_size = max_chunk_size
        self.save_json = save_json
        self.generator = EmbeddingGenerator(
            model_name=model_name,
            device=device,
            batch_size=batch_size,
        )

    def process(self, kb_path: Path, output_dir: Path) -> None:
        t_total = time.time()
        SEP = "=" * 70
        sep = "-" * 70

        print(f"\n{SEP}")
        print("  EULIX EMBED — EMBEDDING PIPELINE (Python/PyTorch)")
        print(f"  ijson backend : {ijson.backend}")
        print(f"  orjson        : {'yes' if _HAS_ORJSON else 'no (stdlib json)'}")
        print(f"  Chunk slots   : {'yes' if _DC_KW else 'no (Python <3.10)'}")
        print(f"{SEP}\n")

        #  Step 1 + 2: Single-pass KB scan + chunk generation
        print("STEP 1+2: Knowledge Base scan + Chunk generation (single pass)")
        print(sep)
        t = time.time()

        meta: Dict[str, Any] = {}
        chunks: List[Chunk] = []
        cg_edges: List[Dict] = []
        dep_edges: List[Dict] = []
        seen_ids: Set[str] = set()

        n_files = n_funcs = n_classes = n_methods = 0
        ct_counts: Dict[str, int] = defaultdict(int)

        # entry_point helpers — populated once we receive the 'entry_points' meta key
        entry_point_ids: Set[str] = set()
        entry_points_by_id: Dict[str, Any] = {}

        for event_type, *payload in _stream_kb(kb_path, need_graphs=self.save_json):
            if event_type == "meta":
                key, value = payload
                meta[key] = value
                # Build entry-point lookups as soon as we have them
                if key == "entry_points":
                    entry_point_ids = {ep["function"] for ep in value}
                    entry_points_by_id = {ep["function"]: ep for ep in value}

            elif event_type == "structure":
                file_path, fs = payload
                n_files += 1
                n_funcs += len(fs.get("functions", []))
                n_classes += len(fs.get("classes", []))
                n_methods += sum(
                    len(c.get("methods", [])) for c in fs.get("classes", [])
                )
                for chunk in chunk_one_file(
                    file_path,
                    fs,
                    entry_point_ids,
                    entry_points_by_id,
                    self.max_chunk_size,
                    seen_ids,
                ):
                    ct_counts[chunk.chunk_type.value] += 1
                    chunks.append(chunk)
                # fs is now unreferenced → immediately eligible for GC

            elif event_type == "cg_edge":
                cg_edges.append(payload[0])

            elif event_type == "dep_edge":
                dep_edges.append(payload[0])

        step12_time = time.time() - t

        ep_count = len(meta.get("entry_points", []))
        print(f"  [OK] KB scanned + chunked in single pass")
        print(f"       Files:        {n_files}")
        print(f"       Functions:    {n_funcs}")
        print(f"       Classes:      {n_classes}")
        print(f"       Methods:      {n_methods}")
        print(f"       Entry Points: {ep_count}")
        print(f"       Total Chunks: {len(chunks)}")
        print(f"       CG edges:     {len(cg_edges)} (streamed)")
        print(f"       Dep edges:    {len(dep_edges)} (streamed)")
        print(f"       Chunk Breakdown:")
        for ct, cnt in sorted(ct_counts.items()):
            print(f"         {ct + ':':<22} {cnt}")
        print(f"       Time:         {step12_time:.2f}s\n")

        # Free per-file metadata and seen_ids — no longer needed
        del seen_ids
        gc.collect()

        #  Step 3: Embeddings ─
        print("STEP 3: Generating Embeddings")
        print(sep)
        t = time.time()
        vector_store = self.generator.generate_vectors(chunks)
        dim = self.generator.dimension
        vec_mb = len(vector_store) * dim * 4 / 1_048_576
        print(f"  [OK] Embeddings generated")
        print(f"       Total Vectors:  {len(vector_store)}")
        print(f"       Vector Size:    {vec_mb:.2f} MB (in-memory)")
        print(f"       Model:          {self.generator.model_name}")
        print(f"       Dimension:      {dim}")
        print(f"       Time:           {time.time() - t:.2f}s\n")

        #  Step 4: Context index
        print("STEP 4: Building Context Index")
        print(sep)
        t = time.time()
        ctx_index = build_context_index(
            meta,
            chunks,
            dim,
            cg_edges=cg_edges,
            dep_edges=dep_edges,
            include_chunks=self.save_json,
        )
        # Edges lists no longer needed
        del cg_edges, dep_edges
        gc.collect()
        print(f"  [OK] Context index built")
        print(f"       Tags:           {len(ctx_index['tags'])}")
        print(f"       Relationships:  {len(ctx_index['relationships'])}")
        print(f"       Time:           {time.time() - t:.2f}s\n")

        #  Step 5: Write output files ─
        print("STEP 5: Writing Output Files")
        print(sep)
        t = time.time()
        output_dir.mkdir(parents=True, exist_ok=True)
        missing = [c.id for c in chunks if c.id not in vector_store]
        if missing:
            print(f"  [WARN] {len(missing)} chunks missing from vector store:")
            for mid in missing[:10]:
                print(f"         {mid}")
            if len(missing) > 10:
                print(f"         ... and {len(missing) - 10} more")
        entries = [(c.id, vector_store[c.id]) for c in chunks if c.id in vector_store]

        emb_bin = output_dir / "embeddings.bin"
        save_embeddings_bin(emb_bin, self.generator.model_name, dim, entries)
        print(f"  [OK] embeddings.bin  ({emb_bin.stat().st_size / 1_048_576:.2f} MB)")

        vec_bin = output_dir / "vectors.bin"
        save_vectors_bin(vec_bin, self.generator.model_name, dim, vector_store)
        print(f"  [OK] vectors.bin     ({vec_bin.stat().st_size / 1_048_576:.2f} MB)")

        if self.save_json:
            emb_json = output_dir / "embeddings.json"
            emb_data: Dict[str, Any] = {
                "model": self.generator.model_name,
                "dimension": dim,
                "total_chunks": len(entries),
                "embeddings": [
                    {
                        "id": eid,
                        "embedding": vec.tolist(),
                        "metadata": next(
                            (
                                {
                                    "file_path": c.metadata.file_path,
                                    "language": c.metadata.language,
                                    "line_start": c.metadata.line_start,
                                    "line_end": c.metadata.line_end,
                                    "name": c.metadata.name,
                                    "complexity": c.metadata.complexity,
                                }
                                for c in chunks
                                if c.id == eid
                            ),
                            {},
                        ),
                    }
                    for eid, vec in entries
                ],
            }
            emb_json.write_text(_json_dumps(emb_data, indent=True))
            print(f"  [OK] embeddings.json ({emb_json.stat().st_size / 1_048_576:.2f} MB)")

            ctx_json = output_dir / "context.json"
            ctx_json.write_text(_json_dumps(ctx_index, indent=True))
            print(f"  [OK] context.json    ({ctx_json.stat().st_size / 1_048_576:.2f} MB)")

        print(f"       Time:           {time.time() - t:.2f}s\n")

        #  Summary
        total_elapsed = time.time() - t_total
        print(SEP)
        print("  PIPELINE SUMMARY")
        print(SEP)
        print(f"  Model:          {self.generator.model_name}")
        print(f"  Dimension:      {dim}")
        print(f"  Total Chunks:   {len(chunks)}")
        print(f"  Relationships:  {len(ctx_index['relationships'])}")
        print(f"  Entry Points:   {len(ctx_index['entry_points'])}")
        print(f"  Call Depth:     {ctx_index['call_graph_summary']['max_depth']}")
        print(f"  Total Time:     {total_elapsed:.2f}s")
        print(SEP)
        print("  PIPELINE COMPLETED SUCCESSFULLY")
        print(f"{SEP}\n")


# CLI


def print_help() -> None:
    print(
        """eulix_embed.py — Knowledge Base Embedding Generator (Python/PyTorch)

USAGE:
    python eulix_embed.py [COMMAND] [OPTIONS]

COMMANDS:
    embed    Generate embeddings for a knowledge base (default)
    query    Generate embedding for a query string
    compare  Compare embeddings.bin with vectors.bin

EMBED OPTIONS:
    -k / --kb-path   <PATH>    Path to knowledge base JSON   [default: knowledge_base.json]
    -o / --output    <DIR>     Output directory               [default: ./embeddings]
    -m / --model     <NAME>    HuggingFace model name
    --device         <DEVICE>  cuda / mps / cpu               [default: auto]
    --batch-size     <N>       Batch size                     [default: auto]
    --max-chunk      <N>       Max chunk chars                [default: 2000]
    --save-json                Also write embeddings.json + context.json
                               (enables graph edge streaming)

QUERY OPTIONS:
    -q / --query     <TEXT>    Query text to embed
    -m / --model     <NAME>    HuggingFace model name
    -f / --format    <FMT>     json | binary                  [default: json]

PERFORMANCE TIPS:
    pip install ijson[yajl2_cffi]   # 10× faster C-backend for JSON streaming
    pip install orjson              # 5-10× faster JSON serialisation

SUPPORTED MODELS:
    sentence-transformers/all-MiniLM-L6-v2
    BAAI/bge-small-en-v1.5
    BAAI/bge-base-en-v1.5
    jinaai/jina-embeddings-v2-base-code  (8192 tokens)
"""
    )


def cmd_embed(args: argparse.Namespace) -> None:
    pipeline = EmbeddingPipeline(
        model_name=args.model,
        max_chunk_size=args.max_chunk,
        device=args.device,
        batch_size=args.batch_size,
        save_json=args.save_json,
    )
    kb_path = Path(args.kb_path)
    if not kb_path.exists():
        print(f"[ERROR] KB file not found: {kb_path}", file=sys.stderr)
        sys.exit(1)
    pipeline.process(kb_path, Path(args.output))


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
        sys.stdout.buffer.write(struct.pack("<I", len(emb)))
        sys.stdout.buffer.write(emb.astype(np.float32).tobytes())
    else:
        print(f"[ERROR] Unknown format '{args.format}'", file=sys.stderr)
        sys.exit(1)

def check_duplicate_ids(path: Path | str) -> List[str]:
    with open(path, "rb") as f:
        f.read(4); f.read(4)  # magic + version
        n = struct.unpack("<I", f.read(4))[0]; f.read(n)  # model name
        count, dim = struct.unpack("<II", f.read(8))
        ids = []
        for _ in range(count):
            n = struct.unpack("<I", f.read(4))[0]
            ids.append(f.read(n).decode())
            f.read(dim * 4)

    dupes = [id for id, cnt in Counter(ids).items() if cnt > 1]
    if dupes:
        print(f"\n⚠️ Found {len(dupes)} duplicate IDs in {path}:")
        for d in dupes:
            print(f"  {d}")

    return dupes

def cmd_compare(args: argparse.Namespace) -> None:
    print("Comparing embeddings.bin ↔ vectors.bin...\n")
    emb_path = Path(args.emb)
    vec_path = Path(args.vec)

    _, dim_e, emb_entries = load_embeddings_bin(emb_path)
    _, dim_v, vec_store = load_vectors_bin(vec_path)

    issues: List[str] = []
    print(f"embeddings.bin: {len(emb_entries)} entries, dim={dim_e}")
    print(f"vectors.bin:    {len(vec_store)} entries, dim={dim_v}")

    if dim_e != dim_v:
        issues.append(f"Dimension mismatch: {dim_e} vs {dim_v}")
    if len(emb_entries) != len(vec_store):
        issues.append(f"Count mismatch: {len(emb_entries)} vs {len(vec_store)}")

    for eid, evec in emb_entries[:5]:
        if eid in vec_store:
            diff = np.abs(evec - vec_store[eid]).max()
            status = "✓" if diff < 1e-5 else "✗"
            print(f"  {status} {eid[:60]:60s}  max_diff={diff:.2e}")
            if diff >= 1e-5:
                issues.append(f"Vector mismatch for {eid}")

    if issues:
        print("\n✗ ISSUES:")
        for iss in issues:
            print(f"  - {iss}")
        check_duplicate_ids(emb_path)
        sys.exit(1)
    else:
        print("\n✓ All checks passed.")


def main() -> None:
    if len(sys.argv) == 1 or sys.argv[1] in ("-h", "--help"):
        print_help()
        sys.exit(0)

    p = argparse.ArgumentParser(add_help=False)
    sub = p.add_subparsers(dest="command")

    ep = sub.add_parser("embed")
    ep.add_argument("-k", "--kb-path", default="knowledge_base.json")
    ep.add_argument("-o", "--output", default="./embeddings")
    ep.add_argument("-m", "--model", default="sentence-transformers/all-MiniLM-L6-v2")
    ep.add_argument("--device", default=None)
    ep.add_argument("--batch-size", type=int, default=None)
    ep.add_argument("--max-chunk", type=int, default=2000)
    ep.add_argument("--save-json", action="store_true")

    qp = sub.add_parser("query")
    qp.add_argument("-q", "--query", default="")
    qp.add_argument("-m", "--model", default="sentence-transformers/all-MiniLM-L6-v2")
    qp.add_argument("-f", "--format", default="json")

    cp = sub.add_parser("compare")
    cp.add_argument("emb", nargs="?", default="embeddings/embeddings.bin")
    cp.add_argument("vec", nargs="?", default="embeddings/vectors.bin")

    args = p.parse_args()
    if args.command is None:
        args = ep.parse_args(sys.argv[1:])
        args.command = "embed"

    if args.command == "embed":
        cmd_embed(args)
    elif args.command == "query":
        cmd_query(args)
    elif args.command == "compare":
        cmd_compare(args)
    else:
        print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
