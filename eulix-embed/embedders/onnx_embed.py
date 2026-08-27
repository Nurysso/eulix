# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# embedder module is responsible for running onnx based embedder to embed json files

from __future__ import annotations

import os
import re
import sys
import time
from collections import defaultdict
from pathlib import Path
from typing import TYPE_CHECKING, Any

from utils.buckets import snap_to_bucket
from utils.constants import BUCKETS_JINA, BUCKETS_STANDARD
from utils.req import require_ml_onnx, require_numpy
from utils.types import Chunk

if TYPE_CHECKING:
    import tokenizers  # type: ignore[import-untyped, unused-ignore]

# ONNX RUNTIME EMBEDDER
# No PyTorch anywhere in this section — model inference runs entirely
# through onnxruntime.InferenceSession. CPU / CUDA / ROCm are all just
# different Execution Providers to the same session; which ones are
# actually usable depends on which onnxruntime wheel is installed:
#   pip install onnxruntime        -> CPUExecutionProvider only
#   pip install onnxruntime-gpu    -> + CUDAExecutionProvider (NVIDIA)
#   pip install onnxruntime-rocm   -> + ROCMExecutionProvider (AMD ROCm/HIP)
# (only one of onnxruntime / onnxruntime-gpu / onnxruntime-rocm should be
# installed at a time — they conflict on the same import name).

_PROVIDER_ALIASES: dict[str, list[str]] = {
    "cpu": ["CPUExecutionProvider"],
    "cuda": ["CUDAExecutionProvider", "CPUExecutionProvider"],
    "gpu": [
        "CUDAExecutionProvider",
        "ROCMExecutionProvider",
        "MIGraphXExecutionProvider",
        "CPUExecutionProvider",
    ],
    "rocm": [
        "ROCMExecutionProvider",
        "MIGraphXExecutionProvider",
        "CPUExecutionProvider",
    ],
    "hip": [
        "ROCMExecutionProvider",
        "MIGraphXExecutionProvider",
        "CPUExecutionProvider",
    ],
    "migraphx": ["MIGraphXExecutionProvider", "CPUExecutionProvider"],
}

# Where locally-exported ONNX models get cached (used only when a model
# has no pre-exported ONNX weights on the Hub — see _resolve_onnx_model).
_ONNX_CACHE_DIR = Path(os.environ.get("EULIX_ONNX_CACHE", str(Path.home() / ".cache" / "eulix-embed" / "onnx")))

# Preference order for locating pre-exported ONNX weights inside a model
# repo / local directory. Many sentence-transformers / feature-extraction
# repos on the Hub already ship one of these.
_ONNX_CANDIDATE_FILES: list[str] = [
    "onnx/model.onnx",
    "model.onnx",
    "onnx/model_fp16.onnx",
    "onnx/model_quantized.onnx",
]


def _resolve_providers(ort: Any, device: str | None) -> tuple[list[str], str]:
    """
    Turn a user-requested device ("cpu" / "cuda" / "rocm" / None) into an
    ordered list of onnxruntime Execution Providers plus a short label used
    for batch-size / bucketing heuristics.

    When `device` is None, auto-detects the best provider actually present
    in `ort.get_available_providers()` — CUDA, then ROCm/MIGraphX, then CPU.
    """
    available = set(ort.get_available_providers())

    if device is not None:
        key = device.lower()
        if key not in _PROVIDER_ALIASES:
            raise ValueError(f"Unknown device '{device}'. Expected one of: {', '.join(_PROVIDER_ALIASES.keys())}")

        # Find the first requested provider that is actually available on the system
        wanted_providers = [p for p in _PROVIDER_ALIASES[key] if p in available]

        if not wanted_providers or (
            len(wanted_providers) == 1 and wanted_providers[0] == "CPUExecutionProvider" and key != "cpu"
        ):
            print(
                f"  ⚠ Requested provider for '{device}' is not available in this "
                f"onnxruntime build (available: {sorted(available)}); "
                f"falling back to CPUExecutionProvider.",
                file=sys.stderr,
            )
            return ["CPUExecutionProvider"], "cpu"

        active_provider = wanted_providers[0]
        label = {
            "CUDAExecutionProvider": "cuda",
            "ROCMExecutionProvider": "rocm",
            "MIGraphXExecutionProvider": "rocm",
        }.get(active_provider, "cpu")

        if label != "cpu":
            print(f"  ✓ Using {active_provider}", file=sys.stderr)

        # Ensure CPU fallback is always attached at the end of the provider list
        if "CPUExecutionProvider" not in wanted_providers:
            wanted_providers.append("CPUExecutionProvider")

        return wanted_providers, label

    # --- Auto-detection Order ---
    if "CUDAExecutionProvider" in available:
        print("  ✓ NVIDIA GPU detected — using CUDAExecutionProvider", file=sys.stderr)
        return ["CUDAExecutionProvider", "CPUExecutionProvider"], "cuda"

    if "ROCMExecutionProvider" in available:
        print(
            "  ✓ AMD GPU detected — using ROCMExecutionProvider (ROCm/HIP)",
            file=sys.stderr,
        )
        return ["ROCMExecutionProvider", "CPUExecutionProvider"], "rocm"

    if "MIGraphXExecutionProvider" in available:
        print(
            "  ✓ AMD GPU detected — using MIGraphXExecutionProvider (ROCm)",
            file=sys.stderr,
        )
        return ["MIGraphXExecutionProvider", "CPUExecutionProvider"], "rocm"

    print(
        "ℹ No GPU execution provider available — using CPUExecutionProvider",
        file=sys.stderr,
    )
    return ["CPUExecutionProvider"], "cpu"


def _pick_embedding_output(
    output_names: list[str],
) -> tuple[str | None, str | None]:
    """
    Given the ONNX graph's output names, pick which one to actually use for
    embeddings, and how.

    Returns (output_name, kind) where kind is:
      "token"  — a [batch, seq_len, hidden] tensor (e.g. last_hidden_state)
                 that still needs masked mean-pooling, or
      "pooled" — an already-pooled [batch, hidden] sentence vector
                 (e.g. pooler_output / sentence_embedding).

    Returns (None, None) if the graph exposes no usable embedding output at
    all — e.g. a base-checkpoint export that only has a fill-mask / NSP head
    (vocab-size logits), which is common for plain BERT-style repos that
    were exported without specifying the feature-extraction task.
    """
    by_lower = {n.lower(): n for n in output_names}

    if "last_hidden_state" in by_lower:
        return by_lower["last_hidden_state"], "token"
    hs = next((n for n in output_names if "hidden_state" in n.lower()), None)
    if hs is not None:
        return hs, "token"

    for cand in ("sentence_embedding", "pooler_output"):
        if cand in by_lower:
            return by_lower[cand], "pooled"

    return None, None


def _resolve_onnx_model(model_name: str, trust_remote_code: bool = False, force_export: bool = False) -> Path:
    """
    Resolve `model_name` to a local .onnx file path, trying in order:

      1. `model_name` is already a local .onnx file.
      2. `model_name` is a local directory containing one of
         _ONNX_CANDIDATE_FILES.
      3. A previously-exported ONNX file is already cached locally under
         _ONNX_CACHE_DIR from an earlier run of this function.
      4. A pre-exported ONNX file exists in the HF Hub repo (most
         sentence-transformers / feature-extraction models ship one under
         `onnx/model.onnx`) — downloaded via huggingface_hub, including a
         sibling `*.onnx_data` file for large (>2GB) models.
      5. Nothing usable exists yet: export on the fly via `optimum`, forcing
         the feature-extraction task so the graph is guaranteed to expose
         last_hidden_state rather than a fill-mask/pretraining head. This is
         the only place PyTorch may be touched (`optimum` needs it to trace
         the source model once) — it happens exactly once per model, the
         result is cached under _ONNX_CACHE_DIR, and every subsequent run
         (and every inference call) uses pure onnxruntime.

    `force_export=True` skips straight to step 5 and overwrites the local
    cache — used when a previously-resolved ONNX file turned out not to
    expose a usable embedding output (see EmbeddingGeneratorOnnx.__init__).
    """
    cache_dir = _ONNX_CACHE_DIR / re.sub(r"[^A-Za-z0-9_.-]+", "_", model_name)
    onnx_out = cache_dir / "model.onnx"

    if not force_export:
        local = Path(model_name)
        if local.is_file() and local.suffix == ".onnx":
            return local
        if local.is_dir():
            for pat in _ONNX_CANDIDATE_FILES:
                cand = local / pat
                if cand.exists():
                    return cand

        if onnx_out.exists():
            return onnx_out

        try:
            from huggingface_hub import hf_hub_download, list_repo_files

            repo_files = set(list_repo_files(model_name))
            for pat in _ONNX_CANDIDATE_FILES:
                if pat in repo_files:
                    onnx_path = Path(hf_hub_download(model_name, pat))  # nosec B615
                    data_pat = f"{pat}_data"
                    if data_pat in repo_files:
                        hf_hub_download(model_name, data_pat)  # nosec B615
                    return onnx_path
        except Exception as e:  # noqa: BLE001
            print(
                f"     [i] No pre-exported ONNX weights found on the Hub for "
                f"'{model_name}' ({e}); will export locally.",
                file=sys.stderr,
            )

    print(
        f"     Exporting '{model_name}' to ONNX (feature-extraction task) — "
        f"one-time step, needs 'optimum' + 'torch' installed (inference "
        f"itself will not use them)...",
        file=sys.stderr,
    )
    try:
        from optimum.onnxruntime import ORTModelForFeatureExtraction
    except ImportError:
        raise ImportError(
            f"No usable pre-exported ONNX weights were found for "
            f"'{model_name}', so it needs to be exported once. That "
            f"one-time step requires:\n"
            f"    pip install optimum[exporters] torch\n"
            f"(only for this export — regular inference stays pure onnxruntime)."
        )

    cache_dir.mkdir(parents=True, exist_ok=True)
    ort_model = ORTModelForFeatureExtraction.from_pretrained(
        model_name, export=True, trust_remote_code=trust_remote_code
    )
    ort_model.save_pretrained(cache_dir)

    exported = next(cache_dir.glob("*.onnx"))
    if exported.name != "model.onnx":
        exported.rename(onnx_out)
        exported = onnx_out
    print(f"     ✓ Exported ONNX model cached at {cache_dir}", file=sys.stderr)
    return exported


class EmbeddingGeneratorOnnx:
    """
    Thin wrapper around an ONNX Runtime InferenceSession for batched
    embedding generation. No PyTorch or transformers dependency at
    inference time tokenization is handled directly by the `tokenizers`
    Rust library (same backend transformers wraps), and the forward pass
    runs entirely inside onnxruntime on whichever Execution Provider is
    selected: CPUExecutionProvider, CUDAExecutionProvider (NVIDIA), or
    ROCMExecutionProvider (AMD ROCm/HIP). The provider is auto-detected
    from the installed onnxruntime build unless pinned via
    `device="cpu" | "cuda" | "rocm"`.

    All heavy imports (onnxruntime, tokenizers, tqdm) are deferred to
    require_ml_onnx() and called exactly once here, so importing this
    module doesn't pull in the ML stack for callers that only need the
    binary I/O helpers (save/load embeddings.bin).
    """

    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        device: str | None = None,  # "cpu" | "cuda" | "rocm" | None (auto)
        batch_size: int | None = None,
        normalize: bool = True,
        use_bucketing: bool = True,
        trust_remote_code: bool = False,
    ) -> None:
        np, ort, tklib, tqdm = require_ml_onnx()
        self._np = np
        self._ort = ort
        self._tqdm = tqdm

        self.model_name = model_name
        self.normalize = normalize
        self._dimension: int = 0

        try:
            self.providers, self.device = _resolve_providers(ort, device)

            if batch_size is None:
                batch_size = 64 if self.device in ("cuda", "rocm") else 16
            self.batch_size = batch_size

            self.use_bucketing = use_bucketing and self.device in ("cuda", "rocm")
            print("Using ONNX-RUNTIME")
            print(f"     Model:      {model_name}", file=sys.stderr)
            print(f"     Provider:   {self.providers[0]}", file=sys.stderr)
            print(f"     Batch size: {self.batch_size}", file=sys.stderr)
            print(
                f"     Bucketing:  {'enabled' if self.use_bucketing else 'disabled'}",
                file=sys.stderr,
            )

            # Tokenizer — load directly from the HuggingFace Hub using the
            # `tokenizers` Rust library, bypassing transformers entirely.
            #
            # Hub layout for most sentence-transformers / BERT-family models:
            #   tokenizer.json          — fast tokenizer spec (always present
            #                             for any model with a fast tokenizer)
            #   tokenizer_config.json   — metadata (add_prefix_space, etc.)
            #
            # huggingface_hub.hf_hub_download handles caching, auth tokens,
            # and gated-model access identically to AutoTokenizer.from_pretrained,
            # with zero dependency on transformers.
            try:
                from huggingface_hub import hf_hub_download

                tokenizer_json_path = hf_hub_download(
                    repo_id=model_name,
                    filename="tokenizer.json",
                )  # nosec B615
                self.tokenizer: "tokenizers.Tokenizer" = tklib.Tokenizer.from_file(tokenizer_json_path)
                # Match transformers' default padding/truncation behaviour:
                #   - pad to the longest sequence in the batch (overridden per
                #     call when fixed_len is set for bucketed inference)
                #   - truncate at the model's declared max length (usually 512)
                self.tokenizer.enable_padding(
                    direction="right",
                    pad_id=self.tokenizer.token_to_id("[PAD]") or 0,
                    pad_token="[PAD]",
                )  # nosec B106
                self.tokenizer.enable_truncation(max_length=512)
            except Exception as e:  # noqa: BLE001
                raise RuntimeError(
                    f"\033[1;31;40m Failed to load tokenizer for '{model_name}'.\n\033[0m"
                    f"\033[1;31;40m Possible reasons:\n\033[0m"
                    f"\033[1;31;40m  - Model ID is incorrect (check https://huggingface.co/models)\n\033[0m"
                    f"\033[1;31;40m  - The model has no tokenizer.json (no fast tokenizer)\n\033[0m"
                    f"\033[1;31;40m  - You need to login: `huggingface-cli login`\n\033[0m"
                    f"\033[1;31;40m  - Model is gated and you lack permissions\n\033[0m"
                    f"\033[1;31;40m  - Network issue\n\033[0m"
                    f"Original error: {e}"
                )

            def _load_session(path: Path) -> Any:
                so = ort.SessionOptions()
                so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
                # Disable memory-pattern optimizer: it caches a separate
                # allocation plan per input shape and never frees them for the
                # life of the session. With 7-12 sequence-length buckets this
                # causes GPU memory to climb indefinitely. Turning it off
                # trades a small per-call planning cost for bounded,
                # shape-independent memory usage.
                so.enable_mem_pattern = False

                provider_options: list[dict[str, str]] = []
                for p in self.providers:
                    if p in ("CUDAExecutionProvider", "ROCMExecutionProvider"):
                        provider_options.append(
                            {
                                # kSameAsRequested prevents the arena from
                                # rounding allocations up to the next power of
                                # two and holding that memory forever — the
                                # other half of the "ORT never gives GPU memory
                                # back" behaviour.
                                "arena_extend_strategy": "kSameAsRequested",
                            }
                        )
                    else:
                        provider_options.append({})

                return ort.InferenceSession(
                    str(path),
                    sess_options=so,
                    providers=self.providers,
                    provider_options=provider_options,
                )

            try:
                onnx_path = _resolve_onnx_model(model_name, trust_remote_code=trust_remote_code)
                self.session = _load_session(onnx_path)
            except (OSError, RuntimeError, ValueError) as e:
                raise RuntimeError(
                    f"\033[1;31;40m Failed to load ONNX model weights for '{model_name}'.\n\033[0m"
                    f"Original error: {e}"
                )

            self._input_names = {i.name for i in self.session.get_inputs()}
            all_output_names = [o.name for o in self.session.get_outputs()]
            target, kind = _pick_embedding_output(all_output_names)

            if target is None:
                print(
                    f"     ⚠ Resolved ONNX graph has no usable embedding output "
                    f"(only found: {all_output_names}); forcing a fresh "
                    f"feature-extraction export instead...",
                    file=sys.stderr,
                )
                onnx_path = _resolve_onnx_model(
                    model_name,
                    trust_remote_code=trust_remote_code,
                    force_export=True,
                )
                self.session = _load_session(onnx_path)
                self._input_names = {i.name for i in self.session.get_inputs()}
                all_output_names = [o.name for o in self.session.get_outputs()]
                target, kind = _pick_embedding_output(all_output_names)
                if target is None:
                    raise RuntimeError(
                        f"Exported ONNX graph for '{model_name}' still has no "
                        f"last_hidden_state/pooler_output-style output "
                        f"(found: {all_output_names}). This model may not be "
                        f"exportable for embedding extraction via ONNX."
                    )

            if target != "last_hidden_state":
                print(
                    f"     ⚠ Graph has no 'last_hidden_state' output; using "
                    f"'{target}' ({kind}) instead (available: {all_output_names})",
                    file=sys.stderr,
                )

            # Only ever request this single output — avoids forcing ORT to
            # compute bundled task heads (e.g. MLM decoder) that we don't need.
            self._target_output_name = target
            self._output_kind = kind  # "token" (needs mean-pool) or "pooled"

            try:
                dummy_emb = self._embed_batch(["hello"])
                self._dimension = dummy_emb.shape[-1]
            except Exception as e:  # noqa: BLE001
                raise RuntimeError(f"Failed to probe embedding dimension: {e}")

            print(f"     Dimension:  {self._dimension}", file=sys.stderr)
            print("  ✓ Embedding generator ready!", file=sys.stderr)

        except RuntimeError as e:
            print(f"\nError: {e}", file=sys.stderr)
            print("\nTips:", file=sys.stderr)
            print(
                "  - Use a valid model name from Hugging Face (e.g., 'sentence-transformers/all-MiniLM-L6-v2')",
                file=sys.stderr,
            )
            print("  - Check your internet connection", file=sys.stderr)
            print(
                "  - Run `huggingface-cli login` if the model is private/gated",
                file=sys.stderr,
            )
            print(
                "  - For CUDA/ROCm, install the matching onnxruntime build "
                "(onnxruntime-gpu for CUDA, onnxruntime-rocm for AMD)",
                file=sys.stderr,
            )
            sys.exit(1)

    @property
    def dimension(self) -> int:
        return self._dimension

    def batch_size_for(self, seq_len: int) -> int:
        """
        Bucket-aware batch size. self.batch_size is tuned for a reference
        sequence length of 512 tokens; scale down linearly for longer
        buckets (attention memory scales with sequence length), floor at 1.
        """
        ref_len = 512
        if seq_len <= ref_len:
            return self.batch_size
        return max(1, (self.batch_size * ref_len) // seq_len)

    @staticmethod
    def _mean_pool(last_hidden: Any, attention_mask: Any) -> Any:
        """
        Masked average over token embeddings (numpy), matching the
        sentence-transformers mean-pooling convention these models were
        trained with (as opposed to taking the [CLS] token).
        """
        np = require_numpy()
        mask_exp = attention_mask[:, :, None].astype(np.float32)
        summed = (last_hidden * mask_exp).sum(axis=1)
        counts = np.clip(mask_exp.sum(axis=1), 1e-9, None)
        return summed / counts

    @staticmethod
    def _l2_normalize(vec: Any, axis: int = -1, eps: float = 1e-12) -> Any:
        np = require_numpy()
        norm = np.linalg.norm(vec, axis=axis, keepdims=True)
        return vec / np.clip(norm, eps, None)

    def _encode_batch(self, texts: list[str], fixed_len: int | None = None) -> dict[str, Any]:
        """
        Tokenize a list of texts with the `tokenizers` Rust library and
        return a dict of numpy arrays keyed by the standard HF input names
        (input_ids, attention_mask, token_type_ids when present).

        fixed_len forces padding to that exact length (for bucketed
        inference); otherwise the tokenizer pads to the longest sequence
        in the batch.
        """
        np = self._np

        # Temporarily override padding/truncation length for this batch.
        if fixed_len is not None:
            self.tokenizer.enable_padding(
                direction="right",
                pad_id=self.tokenizer.token_to_id("[PAD]") or 0,
                pad_token="[PAD]",
                length=fixed_len,
            )  # nosec B106
            self.tokenizer.enable_truncation(max_length=fixed_len)
        else:
            # Restore batch-longest padding (no fixed length).
            self.tokenizer.enable_padding(
                direction="right",
                pad_id=self.tokenizer.token_to_id("[PAD]") or 0,
                pad_token="[PAD]",
            )  # nosec B106
            self.tokenizer.enable_truncation(max_length=512)

        encodings = self.tokenizer.encode_batch(texts)

        input_ids = np.array([enc.ids for enc in encodings], dtype=np.int64)
        attention_mask = np.array([enc.attention_mask for enc in encodings], dtype=np.int64)

        result: dict[str, Any] = {
            "input_ids": input_ids,
            "attention_mask": attention_mask,
        }

        # token_type_ids are present for BERT-family models; sentence-
        # transformers exports may omit them from the ONNX graph entirely.
        # Only include them when the graph actually declares that input.
        if "token_type_ids" in self._input_names:
            result["token_type_ids"] = np.array([enc.type_ids for enc in encodings], dtype=np.int64)

        return result

    def _embed_batch(self, texts: list[str], fixed_len: int | None = None) -> Any:
        """
        Embed a batch of texts in one ONNX Runtime forward pass.

        fixed_len, when given, forces tokenization/padding to that exact
        length instead of padding to the batch's longest sequence — this is
        what makes sequence-length bucketing in generate_vectors() effective,
        since it guarantees every batch within a bucket shares the same
        tensor shape (important for onnxruntime's kernel/graph caching on
        GPU).
        """
        np = self._np

        enc = self._encode_batch(texts, fixed_len=fixed_len)
        # Only feed inputs the ONNX graph actually declares.
        ort_inputs = {k: v for k, v in enc.items() if k in self._input_names}

        outputs = self.session.run([self._target_output_name], ort_inputs)
        raw = outputs[0]

        if self._output_kind == "pooled":
            emb = raw
        else:
            emb = self._mean_pool(raw, enc["attention_mask"])

        if self.normalize:
            emb = self._l2_normalize(emb)

        return emb.astype(np.float32)

    def generate_vectors(self, chunks: list[Chunk]) -> dict[str, Any]:
        """
        Embed all chunks and return {chunk_id: vector}.

        When bucketing is enabled (CUDA/ROCm only), chunks are grouped by
        estimated token length into fixed buckets and each bucket is
        embedded with a fixed padding length. This trades a small amount of
        wasted padding for far fewer distinct tensor shapes, speeding up GPU
        inference in onnxruntime.

        Original chunk order is restored after cross-bucket processing, and
        duplicate chunk IDs are collapsed with a warning.
        """
        tqdm = self._tqdm
        total = len(chunks)
        print(f" Processing {total} chunks " f"(batch={self.batch_size}, bucketing={self.use_bucketing})...")
        t0 = time.time()

        is_jina = "jina" in self.model_name.lower()
        buckets = BUCKETS_JINA if is_jina else BUCKETS_STANDARD

        indexed: list[tuple[int, int, Chunk]] = [(i, max(len(c.content) // 4, 1), c) for i, c in enumerate(chunks)]

        results: list[tuple[int, Any]] = []

        bar = tqdm(
            total=total,
            unit="chunk",
            desc="     Embedding",
            dynamic_ncols=True,
            bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{remaining}, {rate_fmt}]",
        )

        if self.use_bucketing:
            bucket_map: dict[int, list[tuple[int, Chunk]]] = defaultdict(list)
            for orig_idx, est_tokens, chunk in indexed:
                b = snap_to_bucket(est_tokens, buckets)
                bucket_map[b].append((orig_idx, chunk))

            print(f"     Shape buckets active: {len(bucket_map)}")
            for blen in sorted(bucket_map):
                print(f"       bucket {blen:>5} tokens → {len(bucket_map[blen])} chunks")

            for blen in sorted(bucket_map):
                items = bucket_map[blen]
                bsz = self.batch_size_for(blen)
                bar.set_postfix(bucket=blen, batch=bsz, refresh=False)
                for start in range(0, len(items), bsz):
                    batch_items = items[start : start + bsz]
                    texts = [c.content for _, c in batch_items]
                    embs = self._embed_batch(texts, fixed_len=blen)
                    for (orig_idx, _), emb in zip(batch_items, embs):
                        results.append((orig_idx, emb))
                    bar.update(len(batch_items))
        else:
            indexed.sort(key=lambda x: x[1], reverse=True)
            for start in range(0, len(indexed), self.batch_size):
                batch_3tuple = indexed[start : start + self.batch_size]
                texts = [c.content for _, _, c in batch_3tuple]
                embs = self._embed_batch(texts)
                for (orig_idx, _, _), emb in zip(batch_3tuple, embs):
                    results.append((orig_idx, emb))
                bar.update(len(batch_3tuple))

        bar.close()

        elapsed = time.time() - t0
        print(f"  ✓ Completed all embeddings in {elapsed:.2f}s")
        print(f"     Average: {total / max(elapsed, 1e-6):.1f} chunks/sec")

        results.sort(key=lambda x: x[0])
        indexed_sorted = sorted(indexed, key=lambda x: x[0])

        store: dict[str, Any] = {}
        duplicates: list[str] = []
        for (orig_idx, emb), (i, _, chunk) in zip(results, indexed_sorted):
            if chunk.id in store:
                duplicates.append(chunk.id)
            store[chunk.id] = emb

        if duplicates:
            print(f"  [WARN] {len(duplicates)} duplicate chunk IDs collapsed in vectors.bin:")
            for did in duplicates[:10]:
                print(f"         {did}")
            if len(duplicates) > 10:
                print(f"         ... and {len(duplicates) - 10} more")

        return store

    def embed_query(self, query: str) -> Any:
        return self._embed_batch([query])[0]
