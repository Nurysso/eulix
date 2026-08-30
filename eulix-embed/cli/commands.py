# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Cli module is responsible for cli related things args,operation yadayada
# sever mode isn't in this module and is part of separate package called server
# why cause that's still experimental and only used in chat mode of eulix cli

# This file is responsible for managing what args do

import argparse
import random
import struct as _struct
import sys
from pathlib import Path

from data_io.binary import (
    FastEmbeddingsReader,
    load_vectors_bin,
)
from embedders.onnx_embed import EmbeddingGeneratorOnnx
from embedders.torch_embed import EmbeddingGeneratorTorch
from pipeline.onnx_pipeline import EmbeddingPipelineOnnx
from pipeline.torch_pipeline import EmbeddingPipelineTorch
from utils.json_util import json_dumps

from .compare import _verify_at_offset, check_duplicate_ids


# cmd_embed handles embed arg and switching of engine
def cmd_embed(args: argparse.Namespace) -> None:
    PipelineClass: type[EmbeddingPipelineTorch | EmbeddingPipelineOnnx]

    if args.engine == "torch":
        PipelineClass = EmbeddingPipelineTorch
    else:
        PipelineClass = EmbeddingPipelineOnnx

    pipeline = PipelineClass(
        model_name=args.model,
        max_chunk_size=args.max_chunk,
        device=args.device,
        batch_size=args.batch_size,
        save_json=args.save_json,
        quantize=args.quantize,
        debug=args.debug,
    )
    kb_path = Path(args.kb_path)
    if not kb_path.exists():
        print(f"[ERROR] KB file not found: {kb_path}", file=sys.stderr)
        sys.exit(1)
    pipeline.process(kb_path, Path(args.output), args)


# cmd_query handles query arg and engine
def cmd_query(args: argparse.Namespace) -> None:
    GeneratorClass: type[EmbeddingGeneratorTorch | EmbeddingGeneratorOnnx]
    if not args.query:
        print("[ERROR] --query is required", file=sys.stderr)
        sys.exit(1)

    if args.engine == "torch":
        GeneratorClass = EmbeddingGeneratorTorch
    else:
        GeneratorClass = EmbeddingGeneratorOnnx

    gen = GeneratorClass(model_name=args.model)
    emb = gen.embed_query(args.query)

    if args.format == "json":
        out = {
            "query": args.query,
            "model": gen.model_name,
            "dimension": gen.dimension,
            "engine": args.engine,
            "embedding": emb.tolist(),
        }
        print(json_dumps(out, indent=True))
    elif args.format == "binary":
        sys.stdout.buffer.write(_struct.pack("<I", len(emb)))
        sys.stdout.buffer.write(emb.astype("float32").tobytes())
    else:
        print(f"[ERROR] Unknown format '{args.format}'", file=sys.stderr)
        sys.exit(1)


# cmd_compare handles comparison of embeddings.bin and vectors.bin,
# it is used to check whether generated files are correct or not
def cmd_compare(args: argparse.Namespace) -> None:
    print("==================================================================")
    print("          COMPARING embeddings.bin ↔ vectors.bin                  ")
    print("==================================================================\n")
    emb_path = Path(args.emb)
    vec_path = Path(args.vec)

    print("[1/3] CHECKING FOR DUPLICATE IDs")
    print("  • Checking vectors.bin...")
    vec_dupes = check_duplicate_ids(vec_path)
    print("  • Checking embeddings.bin...")
    check_duplicate_ids(emb_path)

    print("\n[2/3] LOADING INDEX & METADATA")
    vec_model, vec_ids = load_vectors_bin(vec_path)
    total_entries = len(vec_ids)

    with FastEmbeddingsReader(emb_path) as reader:
        print("uses FastEmbeddingsReader")
        print(f"  • vectors.bin    : {total_entries} IDs (model: '{vec_model}')")
        print(
            f"  • embeddings.bin : {reader.count} entries, dim={reader.dimension}, "
            f"quantized={reader.quantized} (model: '{reader.model_name}')"
        )

        # Alignment Checks
        if vec_model != reader.model_name:
            print("  ⚠️  [MISMATCH] Model names differ!")
        else:
            print("  ✓ Model names match.")

        if reader.count != total_entries:
            print(
                f"  ⚠️  [MISMATCH] Count mismatch: vectors.bin has {total_entries}, "
                f"embeddings.bin has {reader.count}"
            )
        else:
            print("  ✓ Total entry counts match.")

        if total_entries < 9:
            print("\n[WARNING] At least 9 entries required for spot check sampling. Skipping step 3.")
            return

        print("\n[3/3] MAPPING FILE OFFSETS & SPOT CHECKING")

        # Pick 3 head, 3 random mid, 3 tail indices
        rng = random.SystemRandom()
        head_indices = [0, 1, 2]
        tail_indices = [total_entries - 3, total_entries - 2, total_entries - 1]
        mid_indices = sorted(rng.sample(range(3, total_entries - 3), 3))
        sample_targets = [
            ("FIRST 3", head_indices),
            ("RANDOM MID 3", mid_indices),
            ("LAST 3", tail_indices),
        ]

        passed = 0
        total_checks = 0

        for group_label, indices in sample_targets:
            print(f"\n  --- Category: {group_label} ---")
            for idx in indices:
                total_checks += 1
                expected_id = vec_ids[idx]

                # Compute exact byte offset mathematically (O(1))
                target_offset = reader.header_size + (idx * reader.record_size)

                ok, msg = _verify_at_offset(reader, idx, expected_id)

                status = "[OK]" if ok else "[FAIL]"
                print(f"  Index {idx:5d} @ Offset {target_offset:8d} | {status} {msg}")
                if ok:
                    passed += 1

        print("\n==================================================================")
        print(f"SUMMARY: Spot Checks Passed: {passed}/{total_checks} | " f"Duplicates in vectors.bin: {len(vec_dupes)}")
        print("==================================================================")


# Checks ijson backend
def ijson_check():
    """Check which ijson backend is active."""
    try:
        import ijson

        print(f"ijson backend: {ijson.backend}")
        if ijson.backend == "yajl2_c":
            print("  ✓ Fast C backend (yajl2_c) - optimal performance")
        elif ijson.backend == "yajl2_cffi":
            print("  ✓ C backend via CFFI - good performance")
        else:
            print("  ⚠ Pure Python backend - slower, consider: pip install 'ijson[yajl2_cffi]'")
    except ImportError:
        print("❌ ijson not installed")
        print("   Install with: uv pip install ijson")
