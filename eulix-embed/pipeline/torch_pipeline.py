# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
# Pipeline

# Responsible for orchestrates the entire embedding pipeline using torch with streaming to limit memory.
# Step 1+2: one pass over the KB JSON using ijson (incremental parser) and chunk each
# file in a bounded thread pool (MAX_INFLIGHT) – this prevents queuing more work than
# we can hold and avoids sequential blocking. Step 3+4: embed chunks and write binary
# files incrementally (embeddings.bin, vectors.bin) so peak RAM is just one batch of
# vectors. Quantization (SQ8) and bucketing further reduce memory and speed up inference.
# This is also the default engine of eulix_embed

import argparse
import gc
import os
import shutil
import struct as _struct
import sys
import time
from collections import defaultdict, deque
from concurrent.futures import Future, ThreadPoolExecutor
from pathlib import Path
from typing import TYPE_CHECKING, Any

import ijson

from chunking.chunker import chunk_one_file
from chunking.cleaners import drop_docstrings
from data_io.binary import save_embeddings_bin, save_vectors_bin
from data_io.json_stream import stream_kb
from embedders.torch_embed import EmbeddingGeneratorTorch
from utils.buckets import snap_to_bucket
from utils.constants import BUCKETS_JINA, BUCKETS_STANDARD, DC_KW
from utils.json_util import HAS_ORJSON
from utils.req import require_ml
from utils.types import Chunk

if TYPE_CHECKING:
    import numpy as np


class EmbeddingPipelineTorch:
    """
    End-to-end driver: KB JSON -> chunks -> embeddings.bin + vectors.bin.

    Structured as 3 conceptual steps, executed as 2 physical passes to
    minimize peak memory:
      Step 1+2 (single pass): stream-parse the KB and chunk each file as it
        arrives, using a bounded thread pool (MAX_INFLIGHT futures) so we
        never queue up more in-flight work than we can hold in memory, and
        never block sequentially on one file at a time either.
      Step 3+4 (single pass): embed chunks and write both binary files
        directly from a generator — embeddings.bin is written incrementally
        so peak RAM is one batch's worth of vectors, not all of them.
    """

    def __init__(
        self,
        model_name: str = "sentence-transformers/all-MiniLM-L6-v2",
        max_chunk_size: int = 2000,
        device: str | None = None,
        batch_size: int | None = None,
        save_json: bool = False,
        quantize: bool = False,
        debug: bool = False,
    ):
        self.max_chunk_size = max_chunk_size
        self.save_json = save_json
        self.quantize = quantize  # stored on self
        self.debug = debug
        self.generator = EmbeddingGeneratorTorch(
            model_name=model_name,
            device=device,
            batch_size=batch_size,
        )
        _, _, _, _, _, self._tqdm = require_ml()

    def generate_vectors_streaming(
        self,
        gen: "EmbeddingGeneratorTorch",
        chunks: "list[Chunk]",
    ) -> Any:

        total = len(chunks)
        is_jina = "jina" in gen.model_name.lower()
        buckets = BUCKETS_JINA if is_jina else BUCKETS_STANDARD

        # Build (orig_idx, est_tokens, chunk) triples
        indexed = [(i, max(len(c.content) // 4, 1), c) for i, c in enumerate(chunks)]

        # We need to yield in original order, but bucketing processes out-of-order.
        # Collect results[(orig_idx, vec)] then sort once — same as before but
        # we free them immediately after yielding.
        results: list[tuple[int, np.ndarray]] = []

        bar = self._tqdm(
            total=total,
            unit="chunk",
            desc="     Embedding",
            dynamic_ncols=True,
            bar_format="{l_bar}{bar}| {n_fmt}/{total_fmt} [{elapsed}<{remaining}, {rate_fmt}]",
        )

        import time as _time

        t0 = _time.time()

        if gen.use_bucketing:
            from collections import defaultdict as _dd

            bucket_map: dict = _dd(list)
            for orig_idx, est_tokens, chunk in indexed:
                b = snap_to_bucket(est_tokens, buckets)
                bucket_map[b].append((orig_idx, chunk))

            for blen in sorted(bucket_map):
                items = bucket_map[blen]
                bar.set_postfix(bucket=blen, refresh=False)
                for start in range(0, len(items), gen.batch_size):
                    batch = items[start : start + gen.batch_size]
                    texts = [c.content for _, c in batch]
                    embs = gen._embed_batch(texts, fixed_len=blen)
                    for (orig_idx, _), emb in zip(batch, embs):
                        results.append((orig_idx, emb))
                    bar.update(len(batch))
        else:
            indexed.sort(key=lambda x: x[1], reverse=True)
            for start in range(0, len(indexed), gen.batch_size):
                batch = indexed[start : start + gen.batch_size]
                texts = [c.content for _, _, c in batch]
                embs = gen._embed_batch(texts)
                for (orig_idx, _, _), emb in zip(batch, embs):
                    results.append((orig_idx, emb))
                bar.update(len(batch))

        bar.close()
        elapsed = _time.time() - t0
        print(f"  ✓ Completed all embeddings in {elapsed:.2f}s")
        print(f"     Average: {total / max(elapsed, 1e-6):.1f} chunks/sec")

        # Sort by original index → yield in chunk order (no dict needed)
        results.sort(key=lambda x: x[0])
        indexed_sorted = sorted(indexed, key=lambda x: x[0])

        seen: set = set()
        for (orig_idx, vec), (i, _, chunk) in zip(results, indexed_sorted):
            if chunk.id in seen:
                continue  # skip duplicates (same dedup as before)
            seen.add(chunk.id)
            yield chunk.id, vec

    def _check_disk_space(self, output_dir: Path, kb_path: Path, n_chunks: int | None = None) -> None:
        """
        Estimate disk space requirements based on actual KB structure.

        For quantized: ~ (dimension + 4) bytes per vector + ID overhead
        For float32:   ~ dimension * 4 bytes per vector + ID overhead
        """
        free = shutil.disk_usage(output_dir.parent if not output_dir.exists() else output_dir).free

        # Try to estimate from kb.json without full parse
        if n_chunks is None:
            try:
                import ijson

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
                            estimated_total = int(file_size_mb * 25)  # ~40KB per chunk
                            chunk_count = max(chunk_count, estimated_total)
                            break
            except (OSError, ValueError):
                # Fallback: assume 100 chunks per MB of JSON
                chunk_count = n_chunks or (kb_path.stat().st_size // 10_000)
        else:
            chunk_count = n_chunks

        dim = self.generator.dimension

        # Calculate actual required space
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

    def _process_step3_step4(
        self,
        chunks: list[Chunk],
        output_dir: Path,
    ) -> int:
        """
        Merged Step 3 + Step 4: embed → stream directly to disk.
        Peak RAM = one batch of embeddings (batch_size * dim * 4 bytes).

        Returns the dimension used (needed by process() summary block).
        """
        sep = "-" * 70

        print("STEP 3+4: Generating Embeddings → Writing to disk (streaming)")
        print(sep)
        t = time.time()

        dim = self.generator.dimension
        output_dir.mkdir(parents=True, exist_ok=True)

        emb_bin = output_dir / "embeddings.bin"
        vec_bin = output_dir / "vectors.bin"

        ordered_ids: list[str] = []
        n_written = 0

        def _vector_gen():
            nonlocal n_written
            for item in self.generate_vectors_streaming(self.generator, chunks):
                # Handle both tuple (cid, vec) and object/dataclass responses
                if isinstance(item, tuple) and len(item) == 2:
                    cid, vec = item
                else:
                    cid, vec = item.id, item.vector

                # PyTorch Tensor check: move GPU/autograd tensors to CPU numpy array
                if hasattr(vec, "detach"):
                    vec = vec.detach().cpu().numpy()

                ordered_ids.append(cid)
                n_written += 1
                yield vec  # Yield ONLY 1D vector payload for fixed-width binary

        count = len(chunks)  # upper bound; dedup inside generator may reduce

        # Stream fixed-width vectors to disk
        save_embeddings_bin(
            path=emb_bin,
            model_name=self.generator.model_name,
            dimension=dim,
            vectors=_vector_gen(),
            count=count,
            quantize=self.quantize,
        )

        # Patch count field in header if deduplication reduced total entries
        if n_written != count:
            model_bytes = self.generator.model_name.encode("utf-8")
            count_offset = 4 + 4 + 4 + len(model_bytes)  # magic+ver+model_len+model
            with open(emb_bin, "r+b") as fh:
                fh.seek(count_offset)
                fh.write(_struct.pack("<I", n_written))

        emb_size = emb_bin.stat().st_size / 1_048_576
        quant_str = " (SQ8 int8)" if self.quantize else " (float32)"
        print(f"  [OK] embeddings.bin  ({emb_size:.2f} MB){quant_str}")

        # Save metadata ID index matching exact vector index order
        save_vectors_bin(vec_bin, self.generator.model_name, ordered_ids)
        print(f"  [OK] vectors.bin     ({vec_bin.stat().st_size / 1_048_576:.2f} MB)")
        print(f"       Vectors written: {n_written}")
        print(f"       Time:            {time.time() - t:.2f}s\n")

        return dim  # returned so process() summary block can use it

    def process(self, kb_path: Path, output_dir: Path, args: argparse.Namespace) -> None:
        t_total = time.time()
        SEP = "=" * 70
        sep = "-" * 70

        print(f"\n{SEP}")
        print("  EULIX EMBED — EMBEDDING PIPELINE")
        print("  Engine : PyTorch")
        print(f"  ijson backend : {ijson.backend}")
        print(f"  orjson        : {'yes' if HAS_ORJSON else 'no (stdlib json)'}")
        print(f"  Chunk slots   : {'yes' if DC_KW else 'no (Python <3.10)'}")
        print(f"  Quantization  : {'SQ8 int8' if self.quantize else 'float32'}")
        print(f"{SEP}\n")

        self._check_disk_space(output_dir, kb_path)

        print("STEP 1+2: Knowledge Base scan + Chunk generation (single pass)")
        print(sep)
        t = time.time()

        meta: dict[str, Any] = {}
        chunks: list[Chunk] = []
        seen_ids: set[str] = set()
        n_files = n_funcs = n_classes = n_methods = 0
        ct_counts: dict[str, int] = defaultdict(int)

        MAX_INFLIGHT = 32
        inflight: deque[Future] = deque()
        # Bounded producer/consumer: keep submitting file-chunking work to
        # the thread pool as files stream in from stream_kb, but once
        # MAX_INFLIGHT futures are outstanding, drain half of them before
        # submitting more. This caps memory (each in-flight future holds
        # one file_struct + its future chunk list) without serializing
        # chunking behind KB parsing.

        def _submit(file_path: str, fs: dict) -> Future:
            max_size = self.max_chunk_size

            def _work():
                drop_docstrings(fs)
                local_seen: set[str] = set()
                return chunk_one_file(file_path, fs, max_size, local_seen)

            return executor.submit(_work)

        def _harvest(fut: Future) -> None:
            for chunk in fut.result():
                if chunk.id not in seen_ids:  # noqa: F821
                    seen_ids.add(chunk.id)  # noqa: F821
                    ct_counts[chunk.chunk_type.value] += 1
                    chunks.append(chunk)

        def _drain_inflight(max_remaining: int = 0) -> None:
            while len(inflight) > max_remaining:
                _harvest(inflight.popleft())

        N_WORKERS = min(4, (os.cpu_count() or 4))
        with ThreadPoolExecutor(max_workers=N_WORKERS) as executor:
            for event_type, *payload in stream_kb(kb_path):
                if event_type == "meta":
                    key, value = payload
                    meta[key] = value

                elif event_type == "structure":
                    file_path, fs = payload
                    n_files += 1
                    n_funcs += len(fs.get("functions", []))
                    n_classes += len(fs.get("classes", []))
                    n_methods += sum(len(c.get("methods", [])) for c in fs.get("classes", []))
                    inflight.append(_submit(file_path, fs))
                    if len(inflight) >= MAX_INFLIGHT:
                        _drain_inflight(max_remaining=MAX_INFLIGHT // 2)

            _drain_inflight(max_remaining=0)

        step12_time = time.time() - t
        n_chunks = len(chunks)

        print("  [OK] KB scanned + chunked in single pass")
        print(f"       Files:        {n_files}")
        print(f"       Functions:    {n_funcs}")
        print(f"       Classes:      {n_classes}")
        print(f"       Methods:      {n_methods}")
        print(f"       Total Chunks: {n_chunks}")
        print("       Chunk Breakdown:")
        for ct, cnt in sorted(ct_counts.items()):
            print(f"         {ct + ':':<22} {cnt}")
        print(f"       Time:         {step12_time:.2f}s\n")

        del seen_ids
        gc.collect()

        dim = self._process_step3_step4(chunks, output_dir)

        # Summary
        total_elapsed = time.time() - t_total
        print(SEP)
        print("  PIPELINE SUMMARY")
        print(SEP)
        print("  Engine: Torch")
        print(f"  Model:          {self.generator.model_name}")
        print(f"  Quantization:   {'SQ8 int8' if self.quantize else 'float32'}")
        print(f"  Dimension:      {dim}")
        print(f"  Total Chunks:   {n_chunks}")
        print(f"  Total Time:     {total_elapsed:.2f}s")
        print(SEP)
        print("  PIPELINE COMPLETED SUCCESSFULLY")
        print(f"{SEP}\n")
