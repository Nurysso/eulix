import io
import sys
from pathlib import Path
import argparse
from typing import List
from collections import Counter
import struct as _struct

from pipeline.onnx_pipeline import EmbeddingPipelineOnnx
from pipeline.torch_pipeline import EmbeddingPipeline
from embedders.torch_embed import EmbeddingGenerator
from embedders.onnx_embed import EmbeddingGeneratorOnnx
from utils.req import require_numpy
from utils.json_util import json_dumps
from data_io.binary import load_embeddings_bin, load_vectors_bin


def cmd_embed(args: argparse.Namespace) -> None:
    if args.engine == "onnx":
        PipelineClass = EmbeddingPipelineOnnx
    else:
        PipelineClass = EmbeddingPipeline

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


def cmd_query(args: argparse.Namespace) -> None:
    if not args.query:
        print("[ERROR] --query is required", file=sys.stderr)
        sys.exit(1)

    if args.engine == "onnx":
        GeneratorClass = EmbeddingGeneratorOnnx
    else:
        GeneratorClass = EmbeddingGenerator

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

def cmd_compare(args: argparse.Namespace) -> None:
    np = require_numpy()
    print("Comparing embeddings.bin ↔ vectors.bin...\n")
    emb_path = Path(args.emb)
    vec_path = Path(args.vec)

    load_embeddings_bin(emb_path)
    load_vectors_bin(vec_path)

def check_python_version():
    """
    Hard version gate: some pinned ML dependencies (torch/transformers
    versions used elsewhere in this project) aren't validated against
    Python 3.12+, so we fail fast with actionable instructions rather than
    letting the user hit a confusing downstream import error.
    """
    # Define your allowed Python boundaries
    MIN_VERSION = (3, 10)
    MAX_VERSION = (3, 11)  # Stop before 3.12+ if your ML libraries aren't ready

    current_version = sys.version_info[:2]

    if current_version < MIN_VERSION or current_version > MAX_VERSION:
        print("=" * 60)
        print("❌ CRITICAL: PYTHON VERSION COMPATIBILITY ERROR")
        print(f"Current version: Python {sys.version.split()[0]}")
        print(
            f"Required version: Between Python {MIN_VERSION[0]}.{MIN_VERSION[1]} and {MAX_VERSION[0]}.{MAX_VERSION[1]}"
        )
        print("=" * 60)
        print("\nPlease switch your virtual environment or Python installation.")
        print(
            "If using 'uv', you can recreate the environment with the correct version:"
        )
        print(f"  uv venv --python {MIN_VERSION[0]}.{MIN_VERSION[1]}")
        print("=" * 60)

        # Immediately halt execution with a non-zero exit code
        sys.exit(1)

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
            print(
                "  ⚠ Pure Python backend - slower, consider: pip install 'ijson[yajl2_cffi]'"
            )
    except ImportError:
        print("❌ ijson not installed")
        print("   Install with: uv pip install ijson")

def check_duplicate_ids(path: Path) -> List[str]:
    with open(path, "rb") as f:
        f.read(4)
        f.read(4)  # magic + version
        n = _struct.unpack("<I", f.read(4))[0]
        f.read(n)  # model name
        count, dim = _struct.unpack("<II", f.read(8))
        ids = []
        for _ in range(count):
            n = _struct.unpack("<I", f.read(4))[0]
            ids.append(f.read(n).decode())
            f.read(dim * 4)

    dupes = [id for id, cnt in Counter(ids).items() if cnt > 1]
    if dupes:
        print(f"\n⚠️ Found {len(dupes)} duplicate IDs in {path}:")
        for d in dupes:
            print(f"  {d}")

    return dupes
