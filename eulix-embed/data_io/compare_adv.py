# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Utils module holds shared code like dynamic imports.

import io
import struct as _struct
from collections.abc import Iterable
from pathlib import Path
from typing import Any, cast

import numpy as np

from utils.constants import BINARY_MAGIC, BINARY_VERSION
from utils.req import require_numpy

from .serialization import sq8_decode, sq8_encode


def save_embeddings_bin_fixed(
    path: Path,
    model_name: str,
    dimension: int,
    entries: Iterable[tuple[str, Any] | Any],  # Iterable of (id, vector) or just vectors
    count: int | None = None,
    quantize: bool = False,
) -> None:
    """Stream-writes a STRICTLY FIXED-WIDTH embeddings.bin file.

    String IDs are omitted from this file (they belong exclusively in vectors.bin).
    Every entry payload has an identical byte width, enabling O(1) disk seeking
    and direct numpy.memmap access without scanning.

    Fixed Entry Layout:
    ---------------------------------------------------------
    If quantized:   4 bytes scale (float32 LE) + dim bytes (int8)
    If float32:     dim * 4 bytes (float32 LE)
    ---------------------------------------------------------
    """
    np = require_numpy()

    if count is None:
        entries = list(entries)
        count = len(entries)

    quant_flag = b"\x01" if quantize else b"\x00"

    with open(path, "wb") as raw:
        fh = io.BufferedWriter(raw, buffer_size=4 * 1024 * 1024)
        fh.write(BINARY_MAGIC)
        fh.write(_struct.pack("<I", BINARY_VERSION))

        # Write model_name (4-byte len + UTF-8)
        b_name = model_name.encode("utf-8")
        fh.write(_struct.pack("<I", len(b_name)))
        fh.write(b_name)

        # Write count, dim, and quantized flag
        fh.write(_struct.pack("<II", count, dimension))
        fh.write(quant_flag)

        for item in entries:
            # Extract array (supports both (id, vec) tuples or raw vecs)
            vec = item[1] if isinstance(item, (tuple, list)) else item
            arr = np.asarray(vec, dtype=np.float32)

            if arr.shape != (dimension,):
                raise ValueError(f"Shape mismatch: {arr.shape} vs ({dimension},)")

            if quantize:
                q, scale = sq8_encode(arr)
                fh.write(_struct.pack("<f", scale))  # 4 bytes
                fh.write(q.tobytes())  # dim bytes
            else:
                fh.write(arr.tobytes())  # dim * 4 bytes

        fh.flush()


def get_embedding_header_info(path: Path) -> dict:
    """Reads fixed-width embeddings.bin header and calculates zero-scan metadata."""
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic: {magic!r}")

        (version,) = _struct.unpack("<I", fh.read(4))
        (model_len,) = _struct.unpack("<I", fh.read(4))
        model_name = fh.read(model_len).decode("utf-8")

        count, dim = _struct.unpack("<II", fh.read(8))
        quantized = fh.read(1) == b"\x01" if version in (4, 5) else False
        header_bytes = fh.tell()

    entry_bytes = (4 + dim) if quantized else (dim * 4)

    return {
        "version": version,
        "model_name": model_name,
        "count": count,
        "dim": dim,
        "quantized": quantized,
        "header_bytes": header_bytes,
        "entry_bytes": entry_bytes,
    }


def seek_vector_o1(path: Path, index: int, header_info: dict) -> Any:
    """O(1) Instant Vector Lookup via pure byte arithmetic (No scanning required)."""
    np = require_numpy()
    header_bytes = header_info["header_bytes"]
    entry_bytes = header_info["entry_bytes"]
    dim = header_info["dim"]
    quantized = header_info["quantized"]

    target_offset = header_bytes + (index * entry_bytes)

    with open(path, "rb") as fh:
        fh.seek(target_offset)

        if quantized:
            (scale,) = _struct.unpack("<f", fh.read(4))
            raw = fh.read(dim)
            return sq8_decode(cast(Any, np.frombuffer(raw, dtype=np.int8)), scale)
        else:
            raw = fh.read(dim * 4)
            return cast(Any, np.frombuffer(raw, dtype=np.float32).copy())


def mmap_embeddings_float32(path: Path, header_info: dict) -> np.ndarray:
    """Zero-Copy Memory Map for Float32 Fixed-Width embeddings.bin.

    Allows treating a multi-GB binary file on disk as a live 2D NumPy array.
    """
    if header_info["quantized"]:
        raise ValueError("mmap direct array access requires unquantized float32 binary format.")

    return np.memmap(
        path,
        dtype=np.float32,
        mode="r",
        offset=header_info["header_bytes"],
        shape=(header_info["count"], header_info["dim"]),
    )
