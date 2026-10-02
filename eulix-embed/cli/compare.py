# Copyright (c) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0
# Maintainer: Dawood (Nurysso) <nurysso@proton.me>

# CLI helper module responsible for comparing embeddings.bin and vectors.bin.

import os
import time
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Any

import numpy as np

from data_io.binary import load_vectors_bin
from data_io.compare_adv import get_embedding_header_info
from utils.constants import BINARY_VERSION

_CHUNK_BYTES = 64 * 1024 * 1024  # ~64 MB of payload per work unit
_MAX_REPORT = 5


def check_duplicate_ids(path: Path) -> list[str]:
    """Detect duplicate chunk IDs in vectors.bin.

    Fast path: len(set(ids)) == len(ids) is a single O(n) C-level pass.
    Counter is only built if duplicates actually exist.
    For embeddings.bin (no IDs) only header + file-size integrity is validated.
    """
    if not path.name.endswith("vectors.bin"):
        info = get_embedding_header_info(path)
        if info["version"] != BINARY_VERSION:
            print(f"  ⚠️  Binary version mismatch: wanted {BINARY_VERSION} got {info['version']}")
        else:
            print("  ✓ Binary version matched")
        expected = info["header_bytes"] + info["count"] * info["entry_bytes"]
        actual = path.stat().st_size
        if actual != expected:
            print(f"  ⚠️  Size mismatch: file is {actual} B, header implies {expected} B")
        else:
            print(f"  ✓ {path.name}: fixed-width, {info['count']} records, size matches header.")
        return []

    _, ids = load_vectors_bin(path)
    if len(set(ids)) == len(ids):
        print(f"  ✓ No duplicate IDs found in {path.name} ({len(ids)} total entries).")
        return []

    counts = Counter(ids)
    dupes = [i for i, c in counts.items() if c > 1]
    print(f"  ⚠️  Found {len(dupes)} duplicate IDs in {path.name}:")
    for d in dupes[:_MAX_REPORT]:
        print(f"      - {d} (x{counts[d]})")
    if len(dupes) > _MAX_REPORT:
        print(f"      ... and {len(dupes) - _MAX_REPORT} more.")
    return dupes


def _open_embeddings_memmap(path: Path, info: dict) -> Any:
    """mmap the whole payload. Float32 -> (count, dim); SQ8 -> structured (count,).

    SQ8 layout per record (matches save_embeddings_bin / sq8_encode):
        4  bytes  scale   float32 LE
        dim bytes quant   int8 (SQ8, one shared scale per vector)
    """
    count, dim = info["count"], info["dim"]

    expected = info["header_bytes"] + count * info["entry_bytes"]
    actual = path.stat().st_size
    if actual != expected:
        raise ValueError(
            f"File size {actual} != expected {expected} "
            f"({'truncated' if actual < expected else 'trailing garbage'}: {actual - expected:+d} bytes)"
        )
    if count == 0:
        return None

    if info["quantized"]:
        dt = np.dtype([("scale", "<f4"), ("q", "i1", (dim,))])  # itemsize = 4 + dim
        if dt.itemsize != info["entry_bytes"]:
            raise ValueError(
                f"Internal dtype size {dt.itemsize} != entry_bytes {info['entry_bytes']} "
                f"(dim={dim}); header parser may be reporting the wrong entry_bytes."
            )
        return np.memmap(path, dtype=dt, mode="r", offset=info["header_bytes"], shape=(count,))

    return np.memmap(path, dtype="<f4", mode="r", offset=info["header_bytes"], shape=(count, dim))


def _dequantize_block(q_block: np.ndarray, scales: np.ndarray) -> np.ndarray:
    """Dequantize a (n, dim) int8 block with per-row (n,) scales.

    Equivalent to applying sq8_decode row-wise, but vectorised:
        x[i] = q_block[i].astype(f4) * scales[i]
    """
    return q_block.astype(np.float32) * scales[:, None]  # type: ignore[no-any-return]


def _scan_chunk(mm: Any, start: int, stop: int, quantized: bool) -> dict:
    """Validate rows [start, stop). Fully vectorised; releases the GIL inside numpy."""
    block = mm[start:stop]

    with np.errstate(all="ignore"):
        if quantized:
            scales = np.ascontiguousarray(block["scale"])  # (n,) float32
            x = _dequantize_block(block["q"], scales)  # (n, dim) float32
            bad = (
                ~np.isfinite(scales)
                | (scales <= 0)  # scale==0 is invalid (encoder guards it)
                | ~np.isfinite(x).all(axis=1)
            )
            sq = np.einsum("ij,ij->i", x, x)
            norms = np.sqrt(sq)
        else:
            x = np.asarray(block)
            bad = ~np.isfinite(x).all(axis=1)
            sq = np.einsum("ij,ij->i", x, x)
            norms = np.sqrt(sq)

    zero = (~bad) & (sq == 0)
    good_norms = norms[~bad].astype(np.float64)

    return {
        "bad": np.flatnonzero(bad) + start,
        "zero": np.flatnonzero(zero) + start,
        "n_good": int(good_norms.size),
        "norm_sum": float(good_norms.sum()) if good_norms.size else 0.0,
        "norm_min": float(good_norms.min()) if good_norms.size else float("inf"),
        "norm_max": float(good_norms.max()) if good_norms.size else float("-inf"),
    }


def verify_all_embeddings(path: Path, info: dict, workers: int | None = None) -> dict:
    """Scan every record of embeddings.bin. Returns aggregated stats + bad indices."""
    mm = _open_embeddings_memmap(path, info)
    count, quantized = info["count"], info["quantized"]

    result = {
        "bad": np.empty(0, dtype=np.int64),
        "zero": np.empty(0, dtype=np.int64),
        "norm_min": float("inf"),
        "norm_max": float("-inf"),
        "norm_mean": float("nan"),
        "seconds": 0.0,
        "mm": mm,
        "quantized": quantized,
    }
    if mm is None:
        return result

    rows_per_chunk = max(1, _CHUNK_BYTES // info["entry_bytes"])
    ranges = [(s, min(s + rows_per_chunk, count)) for s in range(0, count, rows_per_chunk)]
    workers = workers or min(8, os.cpu_count() or 1)

    t0 = time.perf_counter()
    with ThreadPoolExecutor(max_workers=workers) as pool:
        parts = list(pool.map(lambda r: _scan_chunk(mm, r[0], r[1], quantized), ranges))
    result["seconds"] = time.perf_counter() - t0

    result["bad"] = np.concatenate([p["bad"] for p in parts])
    result["zero"] = np.concatenate([p["zero"] for p in parts])
    n_good = sum(p["n_good"] for p in parts)
    result["norm_min"] = min(p["norm_min"] for p in parts)
    result["norm_max"] = max(p["norm_max"] for p in parts)
    if n_good:
        result["norm_mean"] = sum(p["norm_sum"] for p in parts) / n_good
    return result


def find_norm_outliers(mm: Any, quantized: bool, expected: float, tol: float) -> Any:
    """Optional: indices whose L2 norm deviates from `expected` by more than `tol`."""
    count = mm.shape[0]
    row_bytes = mm.dtype.itemsize if quantized else mm.shape[1] * 4
    rows = max(1, _CHUNK_BYTES // row_bytes)

    out = []
    with np.errstate(all="ignore"):
        for s in range(0, count, rows):
            e = min(s + rows, count)
            if quantized:
                b = mm[s:e]
                scales = b["scale"].astype(np.float32)
                x = _dequantize_block(b["q"], scales)
                n = np.sqrt(np.einsum("ij,ij->i", x, x))
            else:
                x = np.asarray(mm[s:e])
                n = np.sqrt(np.einsum("ij,ij->i", x, x))
            out.append(np.flatnonzero(~(np.abs(n - expected) <= tol)) + s)
    return np.concatenate(out) if out else np.empty(0, dtype=np.int64)


def fmt_idx(indices: Any, ids: list[str]) -> str:
    shown = [f"{int(i)} ('{ids[int(i)]}')" if int(i) < len(ids) else str(int(i)) for i in indices[:_MAX_REPORT]]
    extra = f" ... and {len(indices) - _MAX_REPORT} more" if len(indices) > _MAX_REPORT else ""
    return ", ".join(shown) + extra
