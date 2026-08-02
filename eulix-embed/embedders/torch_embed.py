from __future__ import annotations
from typing import Optional, List, Dict, Tuple, Any
from core.types import Chunk, ChunkType, ChunkMetadata
import sys
import time
from collections import defaultdict

from core.constants import BUCKETS_JINA, BUCKETS_STANDARD
from utils.buckets import snap_to_bucket
from utils.req import require_ml

class EmbeddingGenerator:
    """
    Thin wrapper around a HuggingFace encoder model for batched embedding
    generation. Auto-detects the best available accelerator (CUDA, ROCm/HIP
    on AMD, Apple MPS, or CPU fallback) and picks a sensible default batch
    size per device. Jina v2 models are routed through sentence-transformers
    instead of raw AutoModel/AutoTokenizer because they require custom
    remote code (trust_remote_code=True) that sentence-transformers handles
    for us.

    All heavy imports (torch, transformers, tqdm) are deferred to
    require_ml() and called exactly once here, so importing this module
    doesn't pull in the ML stack for callers that only need the binary
    I/O helpers (save/load embeddings.bin).
    """

    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        device: Optional[str] = None,  # type: ignore
        batch_size: Optional[int] = None,
        normalize: bool = True,
        use_bucketing: bool = True,
    ):
        np, torch, F, AutoModel, AutoTokenizer, tqdm = require_ml()
        # stash on self so _embed_batch / generate_vectors can use them
        # without re-importing (Python caches in sys.modules; this is free)
        self._np = np
        self._torch = torch
        self._F = F
        self._tqdm = tqdm

        self.model_name = model_name
        self.normalize = normalize
        try:
            if device is None:
                if torch.cuda.is_available():
                    # Distinguish NVIDIA vs AMD ROCm
                    if torch.version.hip is not None:
                        self.device = torch.device("cuda")
                        print("  ✓ AMD GPU detected — using ROCm/HIP", file=sys.stderr)
                    else:
                        self.device = torch.device("cuda")
                        print("  ✓ NVIDIA GPU detected — using CUDA", file=sys.stderr)
                elif torch.backends.mps.is_available():
                    self.device = torch.device("mps")
                    print("  ✓ Apple MPS detected", file=sys.stderr)
                else:
                    self.device = torch.device("cpu")
                    print("  ℹ No GPU detected — using CPU", file=sys.stderr)
            else:
                self.device = torch.device(device)
            if batch_size is None:
                batch_size = 64 if self.device.type == "cuda" else 16
            self.batch_size = batch_size

            self.use_bucketing = use_bucketing and self.device.type in ("cuda", "mps")

            print(f"     Model:      {model_name}", file=sys.stderr)
            print(f"     Batch size: {self.batch_size}", file=sys.stderr)
            print(
                f"     Bucketing:  {'enabled' if self.use_bucketing else 'disabled'}",
                file=sys.stderr,
            )

            is_jina = "jina" in model_name.lower()

            if is_jina:
                try:
                    from sentence_transformers import SentenceTransformer

                    self._st_model = SentenceTransformer(
                        model_name,
                        device=self.device,
                        trust_remote_code=True,
                        model_kwargs={"attn_implementation": "eager"},
                    )
                    self._st_model.eval()
                    self.tokenizer = self._st_model.tokenizer
                    self.model = None
                    self._use_st = True
                    print(
                        "     Jina v2: loaded via sentence-transformers",
                        file=sys.stderr,
                    )
                except ImportError:
                    raise ImportError(
                        "Jina v2 models require sentence-transformers.\n"
                        "Install: pip install sentence-transformers"
                    )
            else:
                self._use_st = False
                try:
                    self.tokenizer = AutoTokenizer.from_pretrained(model_name, clean_up_tokenization_spaces=True)
                except Exception as e:
                    raise RuntimeError(
                        f"\033[1;31;40m Failed to load tokenizer for '{model_name}'.\n\033[0m"
                        f"\033[1;31;40m Possible reasons:\n\033[0m"
                        f"\033[1;31;40m  - Model ID is incorrect (check https://hugginface.co/models)\n\033[0m"
                        f"\033[1;31;40m  - You need to login: `hugginface-cli login`\n\033[0m"
                        f"\033[1;31;40m  - Model is gated and you lack permissions\n\033[0m"
                        f"\033[1;31;40m  - Netowrk issue\n\033[0m"
                        f"Original error:{e}"
                    )
                try:
                    self.model = AutoModel.from_pretrained(model_name).to(self.device)
                    self.model.eval()
                except Exception as e:
                    raise RuntimeError(
                        f"\033[1;31;40m Failed to load model weights for \{model_name}.\n\033[0m"
                        f"Original error: {e}"
                    )
            # Probe the model's actual output dimension by running a single
            # dummy input through it. We can't trust a hardcoded dimension
            # per model name since users can pass in arbitrary HF model IDs.
            try:
                if self._use_st:
                    test_emb = self._st_model.encode(["hello"], convert_to_numpy=True)
                    self._dimension = test_emb.shape[-1]
                else:
                    with torch.no_grad():
                        dummy = self.tokenizer(
                            "hello", return_tensors="pt", padding=True
                        ).to(self.device)
                        out = self.model(**dummy)
                        emb = self._mean_pool(
                            out.last_hidden_state, dummy["attention_mask"]
                        )
                        self._dimension = emb.shape[-1]
            except Exception as e:
                raise RuntimeError(f"Failed to probe embedding dimension: {e}")

            print(f"     Dimension:  {self._dimension}", file=sys.stderr)
            print("  ✓ Embedding generator ready!", file=sys.stderr)
        except RuntimeError as e:
            print("f\nError: {e}", file=sys.stderr)
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
            sys.exit(1)

    @property
    def dimension(self) -> int:
        return self._dimension

    @staticmethod
    def _mean_pool(
        last_hidden: "torch.Tensor", attention_mask: "torch.Tensor"
    ) -> "torch.Tensor":
        mask_exp = attention_mask.unsqueeze(-1).float()
        summed = (last_hidden * mask_exp).sum(dim=1)
        counts = mask_exp.sum(dim=1).clamp(min=1e-9)
        return summed / counts

    def _embed_batch(
        self, texts: List[str], fixed_len: Optional[int] = None
    ) -> "np.ndarray":
        """
        Embed a batch of texts in one forward pass.

        fixed_len, when given, forces tokenization/padding to that exact
        length instead of padding to the batch's longest sequence — this is
        what makes the sequence-length bucketing in generate_vectors()
        effective, since it guarantees every batch within a bucket shares
        the same tensor shape.
        Mean-pooling (masked average over token embeddings) is used rather
        than a [CLS] token, matching the sentence-transformers convention
        these models were trained with.
        """

        torch = self._torch
        F = self._F
        np = self._np

        if self._use_st:
            encode_kwargs: Dict[str, Any] = dict(
                convert_to_numpy=True,
                normalize_embeddings=self.normalize,
                show_progress_bar=False,
            )
            if fixed_len is not None:
                old_max = self._st_model.max_seq_length
                self._st_model.max_seq_length = fixed_len
                result = self._st_model.encode(texts, **encode_kwargs)
                self._st_model.max_seq_length = old_max
            else:
                result = self._st_model.encode(texts, **encode_kwargs)
            return result.astype(np.float32)

        tok_kwargs: Dict[str, Any] = dict(
            return_tensors="pt", padding=True, truncation=True
        )
        if fixed_len is not None:
            tok_kwargs["max_length"] = fixed_len
            tok_kwargs["padding"] = "max_length"

        with torch.no_grad():
            enc = self.tokenizer(texts, **tok_kwargs).to(self.device)
            out = self.model(**enc)
            emb = self._mean_pool(out.last_hidden_state, enc["attention_mask"])
            if self.normalize:
                emb = F.normalize(emb, p=2, dim=-1)
            return emb.cpu().float().numpy()

    def generate_vectors(self, chunks: List[Chunk]) -> Dict[str, "np.ndarray"]:
        """
        Embed all chunks and return {chunk_id: vector}.

        When bucketing is enabled (GPU/MPS only — see use_bucketing), chunks
        are grouped by estimated token length into buckets, and each bucket
        is embedded with a fixed padding length. This trades a small amount
        of wasted padding for far fewer distinct tensor shapes, which
        significantly speeds up GPU inference (avoids shape-triggered
        kernel re-selection / graph recapture on every batch).

        Token length is estimated as len(content) // 4 (a rough
        chars-per-token heuristic) purely for bucket assignment — the real
        tokenizer still runs during _embed_batch and will truncate if the
        estimate was wrong.

        Original chunk order is restored at the end (results are processed
        out-of-order across buckets, then re-sorted by original index)
        before building the returned dict, and duplicate chunk IDs are
        collapsed with a warning rather than silently overwritten.
        """
        np = self._np
        tqdm = self._tqdm
        total = len(chunks)
        print(
            f" Processing {total} chunks (batch={self.batch_size},"
            f" bucketing={self.use_bucketing})..."
        )
        t0 = time.time()

        is_jina = "jina" in self.model_name.lower()
        buckets = BUCKETS_JINA if is_jina else BUCKETS_STANDARD

        indexed: List[Tuple[int, int, Chunk]] = [
            (i, max(len(c.content) // 4, 1), c) for i, c in enumerate(chunks)
        ]

        results: List[Tuple[int, "np.ndarray"]] = []

        bar = tqdm(
            total=total,
            unit="chunk",
            desc="     Embedding",
            dynamic_ncols=True,
            bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{remaining}, {rate_fmt}]",
        )

        if self.use_bucketing:
            bucket_map: Dict[int, List[Tuple[int, Chunk]]] = defaultdict(list)
            for orig_idx, est_tokens, chunk in indexed:
                b = snap_to_bucket(est_tokens, buckets)
                bucket_map[b].append((orig_idx, chunk))

            print(f"     Shape buckets active: {len(bucket_map)}")
            # blen is short for Bucket Length
            for blen in sorted(bucket_map):
                print(
                    f"       bucket {blen:>5} tokens → {len(bucket_map[blen])} chunks"
                )

            for blen in sorted(bucket_map):
                items = bucket_map[blen]
                bar.set_postfix(bucket=blen, refresh=False)
                for start in range(0, len(items), self.batch_size):
                    batch = items[start : start + self.batch_size]
                    texts = [c.content for _, c in batch]
                    embs = self._embed_batch(texts, fixed_len=blen)
                    for (orig_idx, _), emb in zip(batch, embs):
                        results.append((orig_idx, emb))
                    bar.update(len(batch))
        else:
            indexed.sort(key=lambda x: x[1], reverse=True)
            for start in range(0, len(indexed), self.batch_size):
                batch = indexed[start : start + self.batch_size]
                texts = [c.content for _, _, c in batch]
                embs = self._embed_batch(texts)
                for (orig_idx, _, _), emb in zip(batch, embs):
                    results.append((orig_idx, emb))
                bar.update(len(batch))

        bar.close()

        elapsed = time.time() - t0
        print(f"  ✓ Completed all embeddings in {elapsed:.2f}s")
        print(f"     Average: {total / max(elapsed, 1e-6):.1f} chunks/sec")

        results.sort(key=lambda x: x[0])
        indexed_sorted = sorted(indexed, key=lambda x: x[0])

        store: Dict[str, "np.ndarray"] = {}
        duplicates: List[str] = []  # track duplicates
        for (orig_idx, emb), (i, _, chunk) in zip(results, indexed_sorted):
            if chunk.id in store:
                duplicates.append(chunk.id)
            store[chunk.id] = emb

        if duplicates:  # surface them loudly
            print(
                f"  [WARN] {len(duplicates)} duplicate chunk IDs collapsed in vectors.bin:"
            )
            for did in duplicates[:10]:
                print(f"         {did}")
            if len(duplicates) > 10:
                print(f"         ... and {len(duplicates) - 10} more")

        return store

    def embed_query(self, query: str) -> "np.ndarray":
        return self._embed_batch([query])[0]
