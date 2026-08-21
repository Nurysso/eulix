# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Utils module holds shared code like dynamic imports.


# Snap-to-bucket helper — identical to Rust
def snap_to_bucket(seq_len: int, buckets: list[int]) -> int:
    """Return the smallest bucket size >= seq_len, or the largest bucket
    if seq_len exceeds all of them (sequence gets truncated at inference
    time rather than raising an error)."""
    for b in buckets:
        if seq_len <= b:
            return b
    return buckets[-1]
