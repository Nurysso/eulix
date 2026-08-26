# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# just constants for eulix_embed

import sys
from typing import Any

try:
    import ijson.backends.python  # noqa: F401  (always available, pure Python)
except ImportError:
    pass
try:
    import ijson.backends.yajl2_c  # noqa: F401  (fast C backend, if compiled)
except ImportError:
    pass
try:
    import ijson.backends.yajl2_cffi  # noqa: F401
except ImportError:
    pass
try:
    import ijson.backends.yajl2  # noqa: F401
except ImportError:
    pass

import ijson.common

# Sequence-length "buckets" for embedding inference. Instead of padding every
# batch to the longest sequence (wasteful) or padding to one global max
# (also wasteful), we snap each chunk's estimated token length up to the
# nearest bucket. This keeps the number of distinct tensor shapes small
# (good for GPU kernel reuse / CUDA graph capture) while avoiding excess
# padding.
BUCKETS_STANDARD: list[int] = [32, 64, 128, 192, 256, 384, 512]
BUCKETS_JINA: list[int] = [32, 64, 128, 192, 256, 384, 512, 768, 1024, 2048, 4096, 8192]

BINARY_MAGIC = b"EULX"  # 4-byte file signature written at the start of embeddings.bin / vectors.bin
BINARY_VERSION = 5  # Bump this whenever the on-disk binary layout changes (see save_embeddings_bin docstring)
# VECTOR_MAGIC = b"EULX"
Version = "0.4.0"  # different from Binary and vector magic

# Dataclass slots: Python ≥3.10 natively; earlier versions fall back gracefully.
DC_KW: dict[str, Any] = {"slots": True} if sys.version_info >= (3, 10) else {}

ijson_backend = ijson.backend
