# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Cli module is responsible for cli related things args,operation yadayada
# sever mode isnt in this module and is part of seprate package called server
# why cause thats still experimental and only used in chat mode of eulix cli

# This file is responsible for helping cmd_compare to run

import argparse
import random
import struct
from collections import Counter
from pathlib import Path
from typing import List

import numpy as np

from core.constants import BINARY_MAGIC, BINARY_VERSION
from utils.req import require_numpy
from data_io.serialization import sq8_decode
from data_io.binary import load_vectors_bin

def check_duplicate_ids(path: Path) -> List[str]:
    """Scans embeddings.bin or vectors.bin headers and IDs to detect duplicates.

    Handles v3, v4, and v5 binary formats without reading vector float payloads into memory.
    """
    ids = []
    with open(path, "rb") as f:
        magic = f.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic in {path.name}: {magic!r}")

        (version,) = struct.unpack("<I", f.read(4))

        # Read model name
        (model_len,) = struct.unpack("<I", f.read(4))
        model_name = f.read(model_len).decode("utf-8")

        is_vectors_bin = path.name.endswith("vectors.bin")

        if is_vectors_bin:
            (count,) = struct.unpack("<I", f.read(4))
            for _ in range(count):
                (id_len,) = struct.unpack("<I", f.read(4))
                ids.append(f.read(id_len).decode("utf-8"))
        else:
            count, dim = struct.unpack("<II", f.read(8))

            quantized = False
            if version in (4, 5):
                quantized = f.read(1) == b"\x01"

            payload_bytes = (4 + dim) if quantized else (dim * 4)

            for _ in range(count):
                (id_len,) = struct.unpack("<I", f.read(4))
                ids.append(f.read(id_len).decode("utf-8"))
                f.seek(payload_bytes, 1)

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
    fh,
    expected_id: str,
    dim: int,
    quantized: bool
) -> tuple[bool, str]:
    """Seeks and reads entry at current position to check if ID and vector payload match."""
    pos = fh.tell()
    try:
        (id_len,) = struct.unpack("<I", fh.read(4))
        read_id = fh.read(id_len).decode("utf-8")

        if quantized:
            (scale,) = struct.unpack("<f", fh.read(4))
            raw = fh.read(dim)
            vec = sq8_decode(np.frombuffer(raw, dtype=np.int8), scale)
        else:
            raw = fh.read(dim * 4)
            vec = np.frombuffer(raw, dtype=np.float32)

        expected_payload_len = dim if quantized else (dim * 4)
        if len(raw) != expected_payload_len:
            return False, f"Truncated payload at offset {pos}"

        if read_id != expected_id:
            return False, f"ID mismatch at offset {pos}: expected '{expected_id}', found '{read_id}'"

        return True, f"Found '{read_id}' [shape={vec.shape}, L2-norm={np.linalg.norm(vec):.2f}]"

    except Exception as e:
        return False, f"Read error at offset {pos}: {e}"
