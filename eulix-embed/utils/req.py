# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Utils module holds shared code like dynamic imports.


def require_ml():
    """
    Import and return (np, torch, F, AutoModel, AutoTokenizer, tqdm).
    Called once inside EmbeddingGenerator.__init__; results are stored on the
    instance so no re-import penalty on subsequent calls.
    """
    import numpy as np
    import torch
    import torch.nn.functional as F
    from tqdm import tqdm
    from transformers import AutoModel, AutoTokenizer

    torch.cuda.set_per_process_memory_fraction(0.85)

    return np, torch, F, AutoModel, AutoTokenizer, tqdm


def require_ml_onnx():
    """
    Import and return (np, ort, tokenizers, tqdm).

    Inference runs entirely through ONNX Runtime (`ort`).
    Tokenization is handled by the `tokenizers` library directly
    (the Rust-backed core that transformers wraps internally).
    This removes the transformers dependency entirely.
    """
    import numpy as np
    import onnxruntime as ort
    import tokenizers as tklib  # type: ignore[import-untyped, unused-ignore]
    from tqdm import tqdm

    return np, ort, tklib, tqdm


def require_numpy():
    """Lightweight path: only numpy (used by save_*/load_* bin helpers)."""
    import numpy as np

    return np
