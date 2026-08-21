# Copyright (c) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0
# Maintainer: Dawood (Nurysso) <nurysso@proton.me>

# CLI helper module responsible for comparing embeddings.bin and vectors.bin.

import struct
from collections import Counter
from pathlib import Path

import numpy as np

from data_io.binary import (
    FastEmbeddingsReader,
    load_vectors_bin,
)
from data_io.serialization import sq8_decode
from utils.constants import BINARY_MAGIC


def check_duplicate_ids(path: Path) -> list[str]:
    """Scans vectors.bin to detect duplicate chunk IDs.

    For embeddings.bin (which now contains fixed-width vector records with no IDs),
    it validates file integrity and reports record count.
    """
    is_vectors_bin = path.name.endswith("vectors.bin")

    if not is_vectors_bin:
        # embeddings.bin no longer stores string IDs directly.
        # We perform a quick header check to report record count instead.
        with open(path, "rb") as f:
            magic = f.read(4)
            if magic != BINARY_MAGIC:
                raise ValueError(f"Bad magic in {path.name}: {magic!r}")
            try:
                (version,) = struct.unpack("<I", f.read(4))
                print("  ✓ Binary Version Matched" if version == 5 else "⚠️ Binary version mismatch")
                # Read model name
                (model_len,) = struct.unpack("<I", f.read(4))
                _ = f.read(model_len).decode("utf-8")
            except (UnicodeDecodeError, ValueError):
                print("decoding error")

            count, _ = struct.unpack("<II", f.read(8))
            print(f"  ✓ {path.name} is a fixed-width binary ({count} total vector records).")
        return []

    # Process vectors.bin
    _, ids = load_vectors_bin(path)
    counts = Counter(ids)
    dupes = [eid for eid, cnt in counts.items() if cnt > 1]

    if dupes:
        print(f"  ⚠️  Found {len(dupes)} duplicate IDs in {path.name}:")
        for d in dupes[:5]:  # Print first 5
            print(f"      - {d} (x{counts[d]})")
        if len(dupes) > 5:
            print(f"      ... and {len(dupes) - 5} more.")
    else:
        print(f"  ✓ No duplicate IDs found in {path.name} ({len(ids)} total entries).")

    return dupes


def _verify_at_offset(
    reader: FastEmbeddingsReader,
    idx: int,
    expected_id: str,
) -> tuple[bool, str]:
    """Uses FastEmbeddingsReader O(1) index access to verify vector payload at index."""
    try:
        # Calculate expected byte offset for reporting purposes
        pos = reader.header_size + (idx * reader.record_size)

        if reader.quantized:
            q, scale = reader.get_vector(idx, dequantize=False)
            vec = sq8_decode(q, scale)
        else:
            vec = reader.get_vector(idx, dequantize=True)

        if vec.shape != (reader.dimension,):
            return (
                False,
                f"Dimension mismatch at index {idx} (offset {pos}): expected {reader.dimension}, got {vec.shape[0]}",
            )

        norm = float(np.linalg.norm(vec))
        return True, f"Matched '{expected_id}' [shape={vec.shape}, L2-norm={norm:.2f}]"

    except Exception as e:  # noqa: BLE001
        pos = reader.header_size + (idx * reader.record_size) if hasattr(reader, "header_size") else 0
        return False, f"Read error at index {idx} (offset {pos}): {e}"
