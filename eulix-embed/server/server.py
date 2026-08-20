# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Experimental server mode for eulix_embed

import argparse
import json
import sys
from typing import Any

from embedders.onnx_embed import EmbeddingGeneratorOnnx
from embedders.torch_embed import EmbeddingGenerator
from utils.json_util import json_dumps


def cmd_serve(args: argparse.Namespace) -> None:
    """
    Long-lived embedding server.  Keeps the model loaded in memory and handles
    one query per line, making it far cheaper than spawning a new process per
    call (which would reload the model every time).

    Protocol — newline-delimited JSON on stdin / stdout:
      stdin  (one JSON object per line):
        {"query": "<text>"}
            → embed the query text; reply with the vector
        {"queries": ["<text>", ...]}
            → batch embed; reply with a list of vectors (same order)
        {"ping": true}
            → health-check; reply {"pong": true, "model": "...", "dim": N}
        {"shutdown": true}
            → flush stdout and exit cleanly (exit code 0)

      stdout (one JSON object per line per request):
        {"embedding":  [f32, ...], "dimension": N, "model": "..."}
        {"embeddings": [[f32, ...], ...], "dimension": N, "model": "..."}
        {"pong": true, "model": "...", "dim": N}
        {"error": "<message>"}          ← on any per-request failure

    Stderr receives all human-readable startup/progress messages so the parent
    process can separate them from the protocol stream.

    Usage:
        python eulix_embed.py serve -m sentence-transformers/all-MiniLM-L6-v2
    """
    # All diagnostic output goes to stderr — stdout is the protocol channel.
    _err = sys.stderr
    gen: EmbeddingGenerator | EmbeddingGeneratorOnnx

    print("  EULIX EMBED — SERVE MODE", file=_err)
    print(f"  Model:  {args.model}", file=_err)
    if args.device:
        print(f"  Device: {args.device}", file=_err)
    print("  Loading model…", file=_err)

    try:
        if args.engine == "onnx":
            gen = EmbeddingGeneratorOnnx(
                model_name=args.model,
                device=args.device,
                batch_size=args.batch_size,
            )
        else:
            gen = EmbeddingGenerator(
                model_name=args.model,
                device=args.device,
                batch_size=args.batch_size,
            )
    except SystemExit:
        sys.exit(1)

    # Signal readiness to the parent process — one line on stdout.
    _ready: dict[str, Any] = {
        "ready": True,
        "model": gen.model_name,
        "dim": gen.dimension,
    }
    print(json.dumps(_ready), flush=True)
    print(f"  ✓ Ready — listening on stdin (dim={gen.dimension})", file=_err)

    stdin = sys.stdin
    stdout = sys.stdout

    for raw_line in stdin:
        raw_line = raw_line.strip()
        if not raw_line:
            continue  # skip blank lines (e.g. keep-alive pings from some callers)

        # Parse request
        try:
            req: dict[str, Any] = json.loads(raw_line)
        except json.JSONDecodeError as exc:
            _reply: dict[str, Any] = {"error": f"invalid JSON: {exc}"}
            print(json_dumps(_reply), flush=True)
            continue

        # Dispatch
        try:
            # Health-check
            if req.get("ping"):
                resp: dict[str, Any] = {
                    "pong": True,
                    "model": gen.model_name,
                    "dim": gen.dimension,
                }
                print(json_dumps(resp), flush=True)
                continue

            # Clean shutdown
            if req.get("shutdown"):
                print(json_dumps({"shutdown": "ok"}), flush=True)
                stdout.flush()
                print("  Shutting down (received shutdown request).", file=_err)
                sys.exit(0)

            # Batch embed: {"queries": ["text1", "text2", ...]}
            if "queries" in req:
                queries = req["queries"]
                if not isinstance(queries, list) or not all(
                    isinstance(q, str) for q in queries
                ):
                    raise ValueError('"queries" must be a JSON array of strings')
                if not queries:
                    raise ValueError('"queries" array is empty')

                # _embed_batch handles lists natively
                embs = gen._embed_batch(queries)
                resp = {
                    "embeddings": [e.tolist() for e in embs],
                    "dimension": gen.dimension,
                    "model": gen.model_name,
                }
                print(json_dumps(resp), flush=True)
                continue

            # Single embed: {"query": "text"}
            if "query" in req:
                query = req["query"]
                if not isinstance(query, str):
                    raise ValueError('"query" must be a string')
                if not query:
                    raise ValueError('"query" is empty')

                emb = gen.embed_query(query)
                resp = {
                    "embedding": emb.tolist(),
                    "dimension": gen.dimension,
                    "model": gen.model_name,
                }
                print(json_dumps(resp), flush=True)
                continue

            # Unknown request shape
            raise ValueError(
                'request must contain "query", "queries", "ping", or "shutdown"'
            )

        except Exception as exc:
            err_resp: dict[str, Any] = {"error": str(exc)}
            print(json_dumps(err_resp), flush=True)
            print(f"  [WARN] request error: {exc}", file=_err)
            continue

    # stdin closed (parent process exited / pipe broken)
    print("  stdin closed — exiting.", file=_err)
