# Copyright (C) 2026 Dawood K.
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
# Pipeline (base)

# Engine-agnostic base for the embedding pipeline. Subclasses (onnx_pipeline.py,
# torch_pipeline.py) only supply the generator and a few small hooks; everything
# else lives here. This file must not import torch or onnxruntime, so that
# selecting one engine never pays the import cost of the other.
# Step 1+2: one pass over the KB JSON using ijson (incremental parser) and chunk each
# file in a bounded thread pool (MAX_INFLIGHT) – this prevents queuing more work than
# we can hold and avoids sequential blocking. Step 3+4: embed chunks batch by batch and
# write the binary files incrementally (embeddings.bin, vectors.bin) so peak RAM is just
# one batch of vectors. Quantization (SQ8) and length bucketing (on real token counts)
# further reduce memory and speed up inference.

from __future__ import annotations

import argparse
import gc
import os
import shutil
import struct as _struct
import sys
import time
from abc import ABC, abstractmethod
from collections import defaultdict, deque
from collections.abc import Iterator, Sequence
from concurrent.futures import Future, ThreadPoolExecutor
from pathlib import Path
from typing import Any, Generic, Protocol, TypeVar

import ijson

from chunking.chunker import chunk_one_file
from chunking.cleaners import drop_docstrings
from data_io.binary import save_embeddings_bin, save_vectors_bin
from data_io.json_stream import stream_kb
from utils.buckets import snap_to_bucket
from utils.constants import BUCKETS_JINA, BUCKETS_STANDARD, DC_KW
from utils.json_util import HAS_ORJSON
from utils.types import Chunk

MAX_INFLIGHT = 32


class EmbeddingGenerator(Protocol):
    """The surface of a generator that the shared pipeline code relies on."""

    @property
    def model_name(self) -> str: ...
    @property
    def dimension(self) -> int: ...
    @property
    def batch_size(self) -> int: ...
    @property
    def use_bucketing(self) -> bool: ...
    @property
    def tokenizer(self) -> Any: ...

    def _embed_batch(self, texts: list[str], fixed_len: int | None = None) -> Sequence[Any]: ...


G = TypeVar("G", bound=EmbeddingGenerator)


class BaseEmbeddingPipeline(ABC, Generic[G]):
    """
    End-to-end driver: KB JSON -> chunks -> embeddings.bin + vectors.bin.

    Structured as 3 conceptual steps, executed as 2 physical passes:
      Step 1+2 (single pass): stream-parse the KB and chunk each file as it
        arrives, using a bounded thread pool (MAX_INFLIGHT futures).
      Step 3+4 (single pass): embed chunks and write both binary files
        directly from a generator, so peak RAM is one batch of vectors.

    Engines subclass this and override the hooks below.
    """

    ENGINE_LABEL: str = "?"  # banner, e.g. "ONNX Runtime"
    ENGINE_SHORT: str = "?"  # summary, e.g. "ONNX"
    CHECK_DISK: bool = True

    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        max_chunk_size: int = 2000,
        device: str | None = None,
        batch_size: int | None = None,
        save_json: bool = False,
        quantize: bool = False,
        debug: bool = False,
    ) -> None:
        self.max_chunk_size = max_chunk_size
        self.save_json = save_json
        self.quantize = quantize
        self.debug = debug
        self._tqdm: Any = self._load_tqdm()
        self.generator: G = self._make_generator(model_name, device, batch_size)

    # ==== hooks =========================
    @abstractmethod
    def _make_generator(self, model_name: str, device: str | None, batch_size: int | None) -> G: ...

    def _load_tqdm(self) -> Any:
        from tqdm import tqdm

        return tqdm

    def _batch_size_for(self, gen: G, blen: int) -> int:
        return gen.batch_size

    # ==== shared: token counting + embedding stream ================
    @staticmethod
    def _token_lengths(gen: EmbeddingGenerator, texts: list[str], step: int = 2048) -> list[int]:
        """Untruncated token counts, so bucketing sees the real length.

        Set EULIX_TOKEN_DEBUG=1 to print per-batch min/max and a final summary;
        this is how you catch padding/truncation regressions that silently
        collapse the bucket distribution.
        """

        tok = gen.tokenizer
        # transformers fast tokenizer -> underlying tokenizers.Tokenizer;
        # a raw tokenizers.Tokenizer (ONNX) has no backend_tokenizer and stays as is.
        raw = getattr(tok, "backend_tokenizer", tok)
        debug = os.getenv("EULIX_TOKEN_DEBUG") == "1"

        if debug:
            kind = "tokenizers.Tokenizer" if hasattr(raw, "encode_batch") else "slow/Python"
            print(f"  [tokens] backend: {kind}  (n_texts={len(texts)}, step={step})")

        out: list[int] = []

        if not hasattr(raw, "encode_batch"):
            # Slow (pure-Python) transformers tokenizer: use the public API.
            for i in range(0, len(texts), step):
                enc = tok(
                    texts[i : i + step],
                    add_special_tokens=True,
                    truncation=False,
                    padding=False,
                    return_attention_mask=False,
                    return_token_type_ids=False,
                    verbose=False,  # silences "sequence longer than max" warning
                )
                lens = [len(ids) for ids in enc["input_ids"]]
                if debug:
                    print(f"  [tokens] batch {i}:{i + step}  min={min(lens)} max={max(lens)}")
                out.extend(lens)
        else:
            prev_trunc = raw.truncation
            prev_pad = raw.padding
            if debug:
                print(f"  [tokens] truncation before: {prev_trunc}")
                print(f"  [tokens] padding    before: {prev_pad}")

            raw.no_truncation()
            raw.no_padding()
            try:
                for i in range(0, len(texts), step):
                    encs = raw.encode_batch(texts[i : i + step], add_special_tokens=True)
                    lens = [len(e.ids) for e in encs]
                    if debug:
                        print(f"  [tokens] batch {i}:{i + step}  min={min(lens)} max={max(lens)}")
                    out.extend(lens)
            finally:
                # The embedder shares this tokenizer, so put its settings back.
                if prev_trunc is not None:
                    raw.enable_truncation(
                        max_length=prev_trunc["max_length"],
                        stride=prev_trunc["stride"],
                        strategy=prev_trunc["strategy"],
                        direction=prev_trunc["direction"],
                    )
                if prev_pad is not None:
                    raw.enable_padding(
                        direction=prev_pad.get("direction", "right"),
                        pad_id=prev_pad["pad_id"],
                        pad_type_id=prev_pad["pad_type_id"],
                        pad_token=prev_pad["pad_token"],
                        length=prev_pad.get("length"),
                        pad_to_multiple_of=prev_pad.get("pad_to_multiple_of"),
                    )
                if debug:
                    print(f"  [tokens] truncation restored: {raw.truncation}")
                    print(f"  [tokens] padding    restored: {raw.padding}")

        if debug and out:
            s = sorted(out)
            n = len(s)
            print(
                f"  [tokens] total={n}  min={s[0]}  max={s[-1]}  "
                f"median={s[n // 2]}  mean={sum(s) / n:.1f}"
            )

        return out

    def generate_vectors_streaming(self, gen: G, chunks: list[Chunk]) -> Iterator[tuple[str, Any]]:
        buckets = BUCKETS_JINA if "jina" in gen.model_name.lower() else BUCKETS_STANDARD

        plan: list[tuple[int | None, int, list[Chunk]]]
        if gen.use_bucketing:
            lengths = self._token_lengths(gen, [c.content for c in chunks])

            max_len: int = getattr(gen, "max_length", 512)
            n_over = sum(1 for n in lengths if n > max_len)
            if n_over:
                print(
                    f"  [WARN] {n_over}/{len(chunks)} chunks exceed {max_len} tokens and will be truncated by the model"
                )

            bucket_map: dict[int, list[Chunk]] = defaultdict(list)
            for c, n in zip(chunks, lengths):
                bucket_map[snap_to_bucket(n, buckets)].append(c)
            plan = [(b, self._batch_size_for(gen, b), bucket_map[b]) for b in sorted(bucket_map)]
        else:
            ordered = sorted(chunks, key=lambda c: len(c.content), reverse=True)
            plan = [(None, gen.batch_size, ordered)]

        total = len(chunks)
        bar = self._tqdm(
            total=total,
            unit="chunk",
            desc="     Embedding",
            dynamic_ncols=True,
            bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{remaining}, {rate_fmt}]",
        )
        seen: set[str] = set()
        t0 = time.time()
        try:
            for blen, bsz, items in plan:
                if blen is not None:
                    bar.set_postfix(bucket=blen, batch=bsz, refresh=False)
                for start in range(0, len(items), bsz):
                    batch = items[start : start + bsz]
                    texts = [c.content for c in batch]
                    embs = gen._embed_batch(texts, fixed_len=blen) if blen is not None else gen._embed_batch(texts)
                    for chunk, emb in zip(batch, embs):
                        if chunk.id in seen:
                            continue
                        seen.add(chunk.id)
                        yield chunk.id, emb  # written to disk before next batch runs
                    bar.update(len(batch))
        finally:
            bar.close()

        elapsed = time.time() - t0
        print(f"  ✓ Completed all embeddings in {elapsed:.2f}s")
        print(f"     Average: {total / max(elapsed, 1e-6):.1f} chunks/sec")

    def _check_disk_space(self, output_dir: Path, kb_path: Path, n_chunks: int | None = None) -> None:
        """
        Estimate disk space requirements based on actual KB structure.

        For quantized: ~ (dimension + 4) bytes per vector + ID overhead
        For float32:   ~ dimension * 4 bytes per vector + ID overhead
        """
        free = shutil.disk_usage(output_dir.parent if not output_dir.exists() else output_dir).free

        chunk_count: int
        # Try to estimate from kb.json without full parse
        if n_chunks is None:
            try:
                chunk_count = 0
                with open(kb_path, "rb") as fh:
                    # Quick scan - count IDs without loading everything
                    parser = ijson.parse(fh)
                    for prefix, event, value in parser:
                        # Count function, class, method IDs
                        if prefix.endswith(
                            (
                                ".functions.item.id",
                                ".classes.item.id",
                                ".methods.item.id",
                            )
                        ):
                            chunk_count += 1
                        # Stop after reasonable sample or end
                        if chunk_count > 1000 and prefix and "item" in prefix:
                            # Estimate total file size ratio
                            file_size_mb = kb_path.stat().st_size / 1_048_576
                            # Rough heuristic: ~40KB per chunk in JSON
                            estimated_total = int(file_size_mb * 25)
                            chunk_count = max(chunk_count, estimated_total)
                            break
            except (OSError, ValueError):
                # Fallback: assume 100 chunks per MB of JSON
                chunk_count = kb_path.stat().st_size // 10_000
        else:
            chunk_count = n_chunks

        dim: int = self.generator.dimension

        if self.quantize:
            # SQ8: 1 byte per dimension + 4 bytes scale per vector
            bytes_per_vector = dim + 4
            quant_suffix = " (SQ8 int8)"
        else:
            # float32: 4 bytes per dimension
            bytes_per_vector = dim * 4
            quant_suffix = " (float32)"

        # ID overhead: avg ID length (~50 chars) + 4 bytes length prefix
        avg_id_len = 50
        bytes_per_id = avg_id_len + 4

        # Total space for embeddings + vectors + 20% headroom
        embeddings_size = chunk_count * bytes_per_vector
        vectors_size = chunk_count * bytes_per_id
        required = int((embeddings_size + vectors_size) * 1.2)

        # Add overhead for JSON output if enabled
        if self.save_json:
            required = int(required * 1.5)  # JSON is ~50% larger

        free_gb = free / 1_073_741_824
        req_gb = required / 1_073_741_824
        actual_gb = (embeddings_size + vectors_size) / 1_073_741_824

        print(f"  Estimated chunks: {chunk_count:,}")
        print(f"  Vector dimension: {dim}{quant_suffix}")
        print(f"  Estimated embeddings.bin: {embeddings_size / 1_048_576:.1f} MB")
        print(f"  Estimated vectors.bin:    {vectors_size / 1_048_576:.1f} MB")
        print(f"  Disk space required: ~{req_gb:.2f} GB (actual ~{actual_gb:.2f} GB)")
        print(f"  Disk space available: {free_gb:.2f} GB")

        if free < required:
            print("\n  [ERROR] Insufficient disk space!")
            print(f"          Need: ~{req_gb:.2f} GB")
            print(f"          Have:  {free_gb:.2f} GB")
            print(f"          Shortfall: {(required - free) / 1_073_741_824:.2f} GB")
            sys.exit(1)
        elif free < required * 1.1:
            print(f"  [WARN] Low disk space - only {(free_gb - req_gb):.2f} GB buffer")

    # ==== shared: steps 1+2 =========================
    def _chunk_kb(self, kb_path: Path) -> tuple[list[Chunk], dict[str, int], dict[str, int]]:
        meta: dict[str, Any] = {}
        chunks: list[Chunk] = []
        seen_ids: set[str] = set()
        ct_counts: dict[str, int] = defaultdict(int)
        stats: dict[str, int] = {"files": 0, "funcs": 0, "classes": 0, "methods": 0}
        inflight: deque[Future[list[Chunk]]] = deque()

        # Bounded producer/consumer: keep submitting file-chunking work as files
        # stream in, but once MAX_INFLIGHT futures are outstanding, drain half of
        # them before submitting more. This caps memory without serializing
        # chunking behind KB parsing.

        def _harvest(fut: Future[list[Chunk]]) -> None:
            for chunk in fut.result():
                if chunk.id not in seen_ids:
                    seen_ids.add(chunk.id)
                    ct_counts[chunk.chunk_type.value] += 1
                    chunks.append(chunk)

        def _drain(max_remaining: int = 0) -> None:
            while len(inflight) > max_remaining:
                _harvest(inflight.popleft())

        def _submit(executor: ThreadPoolExecutor, file_path: str, fs: dict[str, Any]) -> Future[list[Chunk]]:
            max_size = self.max_chunk_size

            def _work() -> list[Chunk]:
                drop_docstrings(fs)
                return chunk_one_file(file_path, fs, max_size, set())

            return executor.submit(_work)

        with ThreadPoolExecutor(max_workers=min(4, os.cpu_count() or 4)) as executor:
            for event_type, *payload in stream_kb(kb_path):
                if event_type == "meta":
                    key, value = payload
                    meta[key] = value
                elif event_type == "structure":
                    file_path, fs = payload
                    stats["files"] += 1
                    stats["funcs"] += len(fs.get("functions", []))
                    stats["classes"] += len(fs.get("classes", []))
                    stats["methods"] += sum(len(c.get("methods", [])) for c in fs.get("classes", []))
                    inflight.append(_submit(executor, file_path, fs))
                    if len(inflight) >= MAX_INFLIGHT:
                        _drain(MAX_INFLIGHT // 2)
            _drain(0)

        return chunks, stats, dict(ct_counts)

    # ==== shared: steps 3+4 =========================
    def _process_step3_step4(self, chunks: list[Chunk], output_dir: Path) -> int:
        """
        Merged Step 3 + Step 4: embed -> stream directly to disk.
        Peak RAM = one batch of embeddings (batch_size * dim * 4 bytes).

        Returns the dimension used (needed by process() summary block).
        """
        sep = "-" * 70
        print("STEP 3+4: Generating Embeddings → Writing to disk (streaming)")
        print(sep)
        t = time.time()

        dim: int = self.generator.dimension
        output_dir.mkdir(parents=True, exist_ok=True)
        emb_bin = output_dir / "embeddings.bin"
        vec_bin = output_dir / "vectors.bin"

        ordered_ids: list[str] = []
        n_written = 0

        def _vector_gen() -> Iterator[Any]:
            nonlocal n_written
            for cid, vec in self.generate_vectors_streaming(self.generator, chunks):
                if hasattr(vec, "detach"):  # torch tensor; no-op for numpy
                    vec = vec.detach().cpu().numpy()
                ordered_ids.append(cid)
                n_written += 1
                yield vec

        count = len(chunks)  # upper bound; dedup inside generator may reduce
        save_embeddings_bin(
            path=emb_bin,
            model_name=self.generator.model_name,
            dimension=dim,
            vectors=_vector_gen(),
            count=count,
            quantize=self.quantize,
        )

        # Patch count field in header if dedup reduced it
        if n_written != count:
            model_bytes = self.generator.model_name.encode("utf-8")
            count_offset = 4 + 4 + 4 + len(model_bytes)  # magic+ver+model_len+model
            with open(emb_bin, "r+b") as fh:
                fh.seek(count_offset)
                fh.write(_struct.pack("<I", n_written))

        quant_str = " (SQ8 int8)" if self.quantize else " (float32)"
        print(f"  [OK] embeddings.bin  ({emb_bin.stat().st_size / 1_048_576:.2f} MB){quant_str}")

        # ID index, in exactly the order the vectors were written
        save_vectors_bin(vec_bin, self.generator.model_name, ordered_ids)
        print(f"  [OK] vectors.bin     ({vec_bin.stat().st_size / 1_048_576:.2f} MB)")
        print(f"       Vectors written: {n_written}")
        print(f"       Time:            {time.time() - t:.2f}s\n")

        return dim

    # ==== shared: driver =========================
    def process(self, kb_path: Path, output_dir: Path, args: argparse.Namespace) -> None:
        t_total = time.time()
        SEP, sep = "=" * 70, "-" * 70
        quant = "SQ8 int8" if self.quantize else "float32"

        print(f"\n{SEP}\n  EULIX EMBED — EMBEDDING PIPELINE")
        print(f"  Engine : {self.ENGINE_LABEL}")
        print(f"  ijson backend : {ijson.backend}")
        print(f"  orjson        : {'yes' if HAS_ORJSON else 'no (stdlib json)'}")
        print(f"  Chunk slots   : {'yes' if DC_KW else 'no (Python <3.10)'}")
        print(f"  Quantization  : {quant}\n{SEP}\n")

        if self.CHECK_DISK:
            self._check_disk_space(output_dir, kb_path)

        print("STEP 1+2: Knowledge Base scan + Chunk generation (single pass)")
        print(sep)
        t = time.time()
        chunks, st, ct_counts = self._chunk_kb(kb_path)
        n_chunks = len(chunks)

        print("  [OK] KB scanned + chunked in single pass")
        print(f"       Files:        {st['files']}")
        print(f"       Functions:    {st['funcs']}")
        print(f"       Classes:      {st['classes']}")
        print(f"       Methods:      {st['methods']}")
        print(f"       Total Chunks: {n_chunks}")
        print("       Chunk Breakdown:")
        for ct, cnt in sorted(ct_counts.items()):
            print(f"         {ct + ':':<22} {cnt}")
        print(f"       Time:         {time.time() - t:.2f}s\n")
        gc.collect()

        dim = self._process_step3_step4(chunks, output_dir)

        print(f"{SEP}\n  PIPELINE SUMMARY\n{SEP}")
        print(f"  Engine: {self.ENGINE_SHORT}")
        print(f"  Model:          {self.generator.model_name}")
        print(f"  Quantization:   {quant}")
        print(f"  Dimension:      {dim}")
        print(f"  Total Chunks:   {n_chunks}")
        print(f"  Total Time:     {time.time() - t_total:.2f}s")
        print(f"{SEP}\n  PIPELINE COMPLETED SUCCESSFULLY\n{SEP}\n")
