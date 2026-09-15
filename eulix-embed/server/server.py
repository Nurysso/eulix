# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

import argparse
import json
import logging
import signal
import sys
import time
import traceback
from typing import Any

from embedders.onnx_embed import EmbeddingGeneratorOnnx
from embedders.torch_embed import EmbeddingGeneratorTorch
from utils.json_util import json_dumps

# Hard caps to protect against a malicious/broken caller sending an
# unbounded batch or an absurdly long line and OOMing the process.
MAX_BATCH_SIZE = 512
MAX_LINE_BYTES = 10 * 1024 * 1024  # 10 MB

log = logging.getLogger("eulix_embed.server")


class _Shutdown(Exception):
    """Raised internally to unwind the request loop on signal/shutdown."""


# Third-party libraries (httpx, huggingface_hub, urllib3, etc.) log at INFO
# by default and spam every HF Hub HEAD/GET check. Only our own logger
# should be INFO by default; libraries stay at WARNING unless -v/debug.
_NOISY_LOGGERS = ("httpx", "httpcore", "huggingface_hub", "urllib3", "filelock")


def _configure_logging(level: str) -> None:
    logging.basicConfig(
        stream=sys.stderr,
        level=getattr(logging, level.upper(), logging.INFO),
        format="%(asctime)s %(levelname)-7s %(name)s: %(message)s",
    )
    if level.upper() != "DEBUG":
        for name in _NOISY_LOGGERS:
            logging.getLogger(name).setLevel(logging.WARNING)


def _write(obj: dict[str, Any]) -> None:
    """Write one JSON line to stdout; stdout is the protocol channel."""
    try:
        print(json_dumps(obj), flush=True)
    except BrokenPipeError:
        # Parent went away nothing more we can do, let the caller exit.
        raise _Shutdown()


def cmd_serve(args: argparse.Namespace) -> None:
    """
    Long-lived embedding server. Keeps the model loaded in memory and handles
    one query per line, making it far cheaper than spawning a new process per
    call (which would reload the model every time).

    Protocol — newline-delimited JSON on stdin / stdout:
      stdin  (one JSON object per line):
        {"query": "<text>"}             → embed; reply with the vector
        {"queries": ["<text>", ...]}    → batch embed; reply with vectors
        {"ping": true}                  → health-check
        {"shutdown": true}              → flush stdout and exit (code 0)

      stdout (one JSON object per line per request):
        {"embedding":  [f32, ...], "dimension": N, "model": "..."}
        {"embeddings": [[f32, ...], ...], "dimension": N, "model": "..."}
        {"pong": true, "model": "...", "dim": N, "uptime_s": F, "requests": N}
        {"error": "<message>"}          ← on any per-request failure

    Stderr receives all human-readable/log output; stdout is protocol-only.

    Usage:
        python main.py server -m sentence-transformers/all-MiniLM-L6-v2
    """
    _configure_logging(getattr(args, "log_level", "info"))
    max_batch_size = getattr(args, "max_batch_size", MAX_BATCH_SIZE)

    gen: EmbeddingGeneratorTorch | EmbeddingGeneratorOnnx
    log.info(
        "starting server mode | model=%s engine=%s device=%s",
        args.model,
        args.engine,
        args.device,
    )

    try:
        cls = EmbeddingGeneratorTorch if args.engine == "torch" else EmbeddingGeneratorOnnx
        gen = cls(model_name=args.model, device=args.device, batch_size=args.batch_size)
    except Exception as exc:
        # Report failure on stdout too a parent process spawning
        # needs a structured reason, not just a nonzero exit code.
        _write({"ready": False, "error": f"model load failed: {exc}"})
        log.error("model load failed: %s\n%s", exc, traceback.format_exc())
        sys.exit(1)

    _write({"ready": True, "model": gen.model_name, "dim": gen.dimension})
    log.info("ready — listening on stdin (dim=%d)", gen.dimension)

    started_at = time.monotonic()
    request_count = 0

    def _handle_signal(signum: int, _frame: Any) -> None:
        log.info("received signal %s — shutting down", signal.Signals(signum).name)
        raise _Shutdown()

    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)

    try:
        for raw_line in sys.stdin:
            raw_line = raw_line.strip()
            if not raw_line:
                continue  # blank/keep-alive lines

            if len(raw_line.encode("utf-8", errors="ignore")) > MAX_LINE_BYTES:
                _write({"error": "request too large"})
                continue

            try:
                req = json.loads(raw_line)
            except json.JSONDecodeError as exc:
                _write({"error": f"invalid JSON: {exc}"})
                continue

            if not isinstance(req, dict):
                _write({"error": "request must be a JSON object"})
                continue

            request_count += 1

            try:
                if req.get("ping"):
                    _write(
                        {
                            "pong": True,
                            "model": gen.model_name,
                            "dim": gen.dimension,
                            "uptime_s": round(time.monotonic() - started_at, 3),
                            "requests": request_count,
                        }
                    )
                    continue

                if req.get("shutdown"):
                    _write({"shutdown": "ok"})
                    log.info("shutting down (shutdown request)")
                    raise _Shutdown()

                if "queries" in req:
                    queries = req["queries"]
                    if not isinstance(queries, list) or not all(isinstance(q, str) for q in queries):
                        raise ValueError('"queries" must be a JSON array of strings')
                    if not queries:
                        raise ValueError('"queries" array is empty')
                    if len(queries) > max_batch_size:
                        raise ValueError(f'"queries" exceeds max batch size of {max_batch_size}')

                    embs = gen._embed_batch(queries)
                    _write(
                        {
                            "embeddings": [e.tolist() for e in embs],
                            "dimension": gen.dimension,
                            "model": gen.model_name,
                        }
                    )
                    continue

                if "query" in req:
                    query = req["query"]
                    if not isinstance(query, str):
                        raise ValueError('"query" must be a string')
                    if not query:
                        raise ValueError('"query" is empty')

                    emb = gen.embed_query(query)
                    _write(
                        {
                            "embedding": emb.tolist(),
                            "dimension": gen.dimension,
                            "model": gen.model_name,
                        }
                    )
                    continue

                raise ValueError('request must contain "query", "queries", "ping", or "shutdown"')

            except _Shutdown:
                raise
            except (ValueError, TypeError) as exc:
                _write({"error": str(exc)})
                log.warning("bad request: %s", exc)
            except Exception as exc:
                _write({"error": f"internal error: {exc}"})
                log.error("request failed: %s\n%s", exc, traceback.format_exc())

    except _Shutdown:
        pass
    except KeyboardInterrupt:
        log.info("interrupted")
    finally:
        sys.stdout.flush()
        log.info(
            "exiting | requests_served=%d uptime_s=%.1f",
            request_count,
            time.monotonic() - started_at,
        )
