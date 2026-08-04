# Copyright (c) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer: Dawood (Nurysso) <nurysso@proton.me>
# Scalar quantization (SQ8) to reduce embedding storage size by 4× with
# minimal quality loss (~1% retrieval drop). Enables handling of billion‑scale
# vector sets on limited disk.

import numpy as np
# from utils.req import require_numpy

def sq8_encode(vec: "np.ndarray") -> tuple["np.ndarray", float]:
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
    # np = require_numpy()
    amax = np.abs(vec).max()
    if amax < 1e-9:
        return np.zeros(len(vec), dtype=np.int8), 1.0
    scale = float(amax) / 127.0
    q = np.clip(np.round(vec / scale), -127, 127).astype(np.int8)
    return q, scale


def sq8_decode(q: "np.ndarray", scale: float) -> "np.ndarray":
    """Dequantize int8 → float32."""
    return q.astype(np.float32) * scale
