# Copyright (c) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0
# Maintainer: Dawood (Nurysso) <nurysso@proton.me>

# Binary serialization of embeddings and vector IDs to compact, seekable formats.
# Uses a streaming writer to avoid holding all vectors in memory; SQ8 support
# reduces storage by 4x with negligible retrieval loss.

import io
import struct as _struct
from pathlib import Path
from typing import Any, cast

from core.constants import BINARY_MAGIC, BINARY_VERSION
from utils.req import require_numpy

from .serialization import sq8_decode, sq8_encode


def _write_str(fh: io.BufferedWriter, s: str) -> None:
    b = s.encode("utf-8")
    fh.write(_struct.pack("<I", len(b)))
    fh.write(b)


def _read_str(fh: Any) -> str:
    (n,) = _struct.unpack("<I", fh.read(4))
    return cast(bytes, fh.read(n)).decode("utf-8")


def save_embeddings_bin(
    path: Path,
    model_name: str,
    dimension: int,
    vectors: Any,  # Iterable[np.ndarray] or ArrayLike
    count: int | None = None,
    quantize: bool = False,
) -> None:
    """Stream-write fixed-width embeddings for O(1) byte offsets.

    File Layout (Little-Endian):
    ─────────────────────────────────────────────────────────
    4  bytes  magic          "EULX"
    4  bytes  version        BINARY_VERSION
    4+n bytes model_name     uint32 len + UTF-8
    4  bytes  count          number of embeddings
    4  bytes  dimension      vector length (floats)
    1  byte   quantized      0 = float32, 1 = SQ8 int8
    ─── per embedding (Fixed Length) ────────────────────────
    if quantized:
      4  bytes  scale        float32 (little-endian)
      dim bytes quantized    int8 * dim
    else:
      dim*4    vector        float32 * dim
    ─────────────────────────────────────────────────────────
    """
    np = require_numpy()
    if count is None:
        vectors = list(vectors)
        count = len(vectors)

    quant_flag = b"\x01" if quantize else b"\x00"

    with open(path, "wb") as fh:
        fh = io.BufferedWriter(fh, buffer_size=4 * 1024 * 1024)
        fh.write(BINARY_MAGIC)
        fh.write(_struct.pack("<I", BINARY_VERSION))
        _write_str(fh, model_name)
        fh.write(_struct.pack("<II", count, dimension))
        fh.write(quant_flag)

        for vec in vectors:
            arr = np.asarray(vec, dtype=np.float32)
            if arr.shape != (dimension,):
                raise ValueError(f"Shape mismatch: {arr.shape} vs ({dimension},)")
            if quantize:
                q, scale = sq8_encode(arr)
                fh.write(_struct.pack("<f", scale))
                fh.write(q.tobytes())
            else:
                fh.write(arr.tobytes())
        fh.flush()


def save_vectors_bin(
    path: Path,
    model_name: str,
    ids: list[str],  # ordered; position == vector index in embeddings.bin
) -> None:
    """Save a vector ID index mapping chunk IDs to vector indices in embeddings.bin.

    File Format (Little-Endian):
    =============================
    Offset   | Size     | Field       | Description
    ---------|----------|-------------|-----------------------------
    0        | 4        | magic       | "EULX" file signature
    4        | 4        | version     | Format version (BINARY_VERSION)
    8        | variable | model_name  | 4-byte len + UTF-8 bytes
    8+mlen   | 4        | count       | Number of IDs
    12+mlen  | variable | id_data     | Repeated for each ID:
             |          |   4 bytes   | id_len (uint32 LE)
             |          |   id_len    | UTF-8 chunk ID
    """
    with open(path, "wb") as fh:
        fh = io.BufferedWriter(fh, buffer_size=4 * 1024 * 1024)
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
        fh.flush()


def load_vectors_bin(path: Path) -> tuple[str, list[str]]:
    """Load a vector ID index.

    Returns:
        (model_name, ids) — ids[i] is the chunk ID corresponding to vector index i.
    """
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic: {magic!r} (expected {BINARY_MAGIC!r})")

        (version,) = _struct.unpack("<I", fh.read(4))
        if not (1 <= version <= BINARY_VERSION):
            raise ValueError(f"Unsupported vectors.bin version: {version}")

        model_name = _read_str(fh)
        (count,) = _struct.unpack("<I", fh.read(4))
        ids: list[str] = [_read_str(fh) for _ in range(count)]

    return model_name, ids


class FastEmbeddingsReader:
    """Provides O(1) random access reads for fixed-length embeddings.bin files."""

    def __init__(self, path: Path):
        self.np = require_numpy()
        self.path = path
        self.fh = open(path, "rb")

        # Parse Header
        magic = self.fh.read(4)
        if magic != BINARY_MAGIC:
            self.fh.close()
            raise ValueError(f"Bad magic: {magic!r}")

        (self.version,) = _struct.unpack("<I", self.fh.read(4))
        self.model_name = _read_str(self.fh)
        self.count, self.dimension = _struct.unpack("<II", self.fh.read(8))
        self.quantized = self.fh.read(1) == b"\x01"

        # Record header size offset
        self.header_size = self.fh.tell()

        # Compute fixed record length in bytes
        if self.quantized:
            self.record_size = 4 + self.dimension  # 4 bytes scale + dim bytes
        else:
            self.record_size = self.dimension * 4  # dim * 4 bytes float32

    def get_vector(self, idx: int, dequantize: bool = True) -> Any:
        """O(1) retrieval of a single vector by index."""
        if not (0 <= idx < self.count):
            raise IndexError(f"Index {idx} out of range (0 to {self.count - 1})")

        offset = self.header_size + (idx * self.record_size)
        self.fh.seek(offset)

        if self.quantized:
            (scale,) = _struct.unpack("<f", self.fh.read(4))
            raw = self.fh.read(self.dimension)
            q = self.np.frombuffer(raw, dtype=self.np.int8).copy()
            return sq8_decode(q, scale) if dequantize else (q, scale)
        else:
            raw = self.fh.read(self.dimension * 4)
            return self.np.frombuffer(raw, dtype=self.np.float32).copy()

    def close(self) -> None:
        self.fh.close()

    def __enter__(self) -> "FastEmbeddingsReader":
        return self

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> None:
        self.close()


def load_vector_mmap(path: Path) -> tuple[str, Any]:
    """Memory-maps unquantized float32 embeddings directly into a NumPy matrix in O(1) time."""
    np = require_numpy()
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic: {magic!r}")
        (version,) = _struct.unpack("<I", fh.read(4))
        model_name = _read_str(fh)
        count, dim = _struct.unpack("<II", fh.read(8))
        quantized = fh.read(1) == b"\x01"
        header_size = fh.tell()

    if quantized:
        raise ValueError(
            "mmap helper is for raw float32 files; use FastEmbeddingsReader for SQ8 files."
        )

    # Memory-map file from header offset onward
    matrix = np.memmap(
        path,
        dtype=np.float32,
        mode="r",
        offset=header_size,
        shape=(count, dim),
    )
    return model_name, matrix
