# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Cli module is responsible for cli related things args,operation yadayada
# sever mode isn't in this module and is part of separate package called server
# why cause that's still experimental and only used in chat mode of eulix cli

# This file is responsible for managing what args do

import argparse
import struct as _struct
import sys
from pathlib import Path

from data_io.binary import load_vectors_bin
from data_io.compare_adv import get_embedding_header_info
from embedders.onnx_embed import EmbeddingGeneratorOnnx
from embedders.torch_embed import EmbeddingGeneratorTorch
from pipeline.onnx_pipeline import EmbeddingPipelineOnnx
from pipeline.torch_pipeline import EmbeddingPipelineTorch
from utils.json_util import json_dumps

from .compare import (
    check_duplicate_ids,
    find_norm_outliers,
    fmt_idx,
    verify_all_embeddings,
)


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
        # save_json=args.save_json,
        quantize=args.quantize,
        # debug=args.debug,
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
    """Full verification of embeddings.bin <-> vectors.bin (every record, mmap-based)."""
    print("=" * 66)
    print("          COMPARING embeddings.bin ↔ vectors.bin (FULL SCAN)      ")
    print("=" * 66 + "\n")

    emb_path, vec_path = Path(args.emb), Path(args.vec)
    failures = 0

    # [1/3] duplicates / integrity
    print("[1/3] CHECKING FOR DUPLICATE IDs & FILE INTEGRITY")
    print("  • Checking vectors.bin...")
    vec_dupes = check_duplicate_ids(vec_path)
    failures += len(vec_dupes)
    print("  • Checking embeddings.bin...")
    check_duplicate_ids(emb_path)

    # [2/3] metadata alignment
    print("\n[2/3] LOADING INDEX & METADATA")
    vec_model, vec_ids = load_vectors_bin(vec_path)
    total = len(vec_ids)
    info = get_embedding_header_info(emb_path)

    print(f"  • vectors.bin    : {total} IDs (model: '{vec_model}')")
    print(
        f"  • embeddings.bin : {info['count']} entries, dim={info['dim']}, "
        f"quantized={info['quantized']} (model: '{info['model_name']}')"
    )

    if vec_model != info["model_name"]:
        print("  ⚠️  [MISMATCH] Model names differ!")
        failures += 1
    else:
        print("  ✓ Model names match.")

    if info["count"] != total:
        print(f"  ⚠️  [MISMATCH] Count mismatch: vectors.bin has {total}, embeddings.bin has {info['count']}")
        failures += 1
    else:
        print("  ✓ Total entry counts match.")

    empty_ids = [i for i, s in enumerate(vec_ids) if not s]
    if empty_ids:
        print(f"  ⚠️  {len(empty_ids)} empty IDs in vectors.bin (first at index {empty_ids[0]})")
        failures += 1

    # [3/3] full scan
    print(f"\n[3/3] FULL SCAN OF ALL {info['count']} EMBEDDING RECORDS (mmap, chunked)")
    try:
        res = verify_all_embeddings(emb_path, info, workers=getattr(args, "workers", None))
    except ValueError as e:
        print(f"  ⚠️  [FAIL] {e}")
        failures += 1
        res = None

    if res is not None and info["count"] > 0:
        n = info["count"]
        gb = n * info["entry_bytes"] / 1e9
        rate = gb / res["seconds"] if res["seconds"] > 0 else float("inf")
        print(f"  • Scanned {n} records ({gb:.2f} GB) in {res['seconds']:.2f}s ({rate:.2f} GB/s)")
        print(f"  • L2 norm: min={res['norm_min']:.4f}  mean={res['norm_mean']:.4f}  max={res['norm_max']:.4f}")

        if len(res["bad"]):
            failures += len(res["bad"])
            bad_str = fmt_idx(res["bad"], vec_ids)
            print(f"  ⚠️  [FAIL] {len(res['bad'])} records with NaN/Inf (or invalid scale): {bad_str}")
        else:
            print("  ✓ All records finite and well-formed.")

        if len(res["zero"]):
            failures += len(res["zero"])
            print(f"  ⚠️  [FAIL] {len(res['zero'])} all-zero vectors: {fmt_idx(res['zero'], vec_ids)}")
        else:
            print("  ✓ No all-zero vectors.")

        tol = getattr(args, "unit_norm", None)
        if tol is not None:
            out = find_norm_outliers(res["mm"], info["quantized"], 1.0, float(tol))
            if len(out):
                failures += len(out)
                print(f"  ⚠️  [FAIL] {len(out)} vectors with |norm-1| > {tol}: {fmt_idx(out, vec_ids)}")
            else:
                print(f"  ✓ All vectors have unit norm within ±{tol}.")

    print("\n" + "=" * 66)
    status = "PASSED" if failures == 0 else f"FAILED ({failures} issue(s))"
    print(f"SUMMARY: {status} | Records scanned: {info['count']} | Duplicates in vectors.bin: {len(vec_dupes)}")
    print("=" * 66)

    if failures:
        raise SystemExit(1)


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
