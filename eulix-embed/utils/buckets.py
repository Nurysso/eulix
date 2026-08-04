from typing import List
# Snap-to-bucket helper — identical to Rust
def snap_to_bucket(seq_len: int, buckets: List[int]) -> int:
    """Return the smallest bucket size >= seq_len, or the largest bucket
    if seq_len exceeds all of them (sequence gets truncated at inference
    time rather than raising an error)."""
    for b in buckets:
        if seq_len <= b:
            return b
    return buckets[-1]
