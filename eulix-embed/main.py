#!/usr/bin/env python

# Copyright (C) 2026 Dawood
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
#
# eulix_embed is a general purpose embedding for eulix project with support for ONNX/Torch

from __future__ import annotations

import argparse
import importlib.util
import multiprocessing
import sys
import warnings

from cli.commands import (
    check_python_version,
    cmd_compare,
    cmd_embed,
    cmd_query,
    ijson_check,
)
from cli.parser import parse_args
from server.server import cmd_serve
from utils.constants import Version, ijson_backend

warnings.filterwarnings("ignore", message="optimum is not installed")

_PARSER = parse_args()


def print_help() -> None:
    _PARSER.print_help()
    # print()
    DETAIL_COMMANDS = ("embed", "query", "serve", "compare")
    sub_action = next(a for a in _PARSER._actions if isinstance(a, argparse._SubParsersAction))

    for name, subparser in sub_action.choices.items():
        if name not in DETAIL_COMMANDS:
            continue
        print(f"{'=' * 60}")
        print(f"  {name.upper()}")
        print(f"{'=' * 60}")
        subparser.print_help()
        print()


def check_engine_dependencies(engine: str) -> None:
    """
    Check if the required dependencies for the selected engine are installed.
    This runs BEFORE any heavy imports, so we can fail fast with clear error messages.
    """
    if engine == "torch":
        required_packages = {
            "torch": "torch",
            "transformers": "transformers",
            "sentence_transformers": "sentence-transformers",
            "tqdm": "tqdm",
            "numpy": "numpy",
        }
        engine_name = "PyTorch"
        install_cmd = "pip install torch transformers sentence-transformers tqdm numpy"

        if importlib.util.find_spec("uv") is not None:
            install_cmd = "uv pip install torch transformers sentence-transformers tqdm numpy"

    elif engine == "onnx":
        required_packages = {
            "onnxruntime": "onnxruntime",
            "tokenizers": "tokenizers",
            "huggingface_hub": "huggingface_hub",
            "tqdm": "tqdm",
            "numpy": "numpy",
        }
        engine_name = "ONNX Runtime"
        install_cmd = "pip install onnxruntime transformers tqdm numpy"

        if importlib.util.find_spec("uv") is not None:
            install_cmd = "uv pip install onnxruntime transformers tqdm numpy"
    else:
        raise ValueError(f"Unknown engine: {engine}")

    missing = []
    for import_name, package_name in required_packages.items():
        if not importlib.util.find_spec(import_name):
            missing.append(f"{package_name} (imports as '{import_name}')")

    if missing:
        print(
            f"❌ ERROR: Missing required dependencies for {engine_name} engine",
            file=sys.stderr,
        )
        print(f"\nMissing packages: {', '.join(missing)}", file=sys.stderr)
        print("\nTo install all required dependencies:", file=sys.stderr)
        print(f"  {install_cmd}", file=sys.stderr)

        # Special note for ONNX GPU
        if engine == "onnx":
            print("\nFor GPU support with ONNX Runtime:", file=sys.stderr)
            print("  NVIDIA: pip install onnxruntime-gpu", file=sys.stderr)
            print("  AMD:    pip install onnxruntime-rocm", file=sys.stderr)

        print("\nOr use the other engine if you have it installed:", file=sys.stderr)
        other_engine = "onnx" if engine == "torch" else "torch"
        print(f"  python eulix_embed.py --engine {other_engine} ...", file=sys.stderr)
        sys.exit(1)

    print(f"  ✓ {engine_name} dependencies found", file=sys.stderr)


def main() -> None:
    if len(sys.argv) == 1 or sys.argv[1] in ("-h", "--help", "help"):
        print_help()
        sys.exit(0)

    if len(sys.argv) == 2 and sys.argv[1] in ("--version", "-V"):
        print(f"{Version}\n")
        sys.exit(0)

    args = _PARSER.parse_args()

    if args.command is None:
        # bare `eulix_embed.py` with no subcommand → default to embed
        embed_parser = next(
            a for a in _PARSER._subparsers._actions if hasattr(a, "_name_parser_map")  # type: ignore[union-attr]
        )._name_parser_map["embed"]
        args = embed_parser.parse_args(sys.argv[1:])
        args.command = "embed"

    if args.command in ("embed", "query", "serve", "compare"):
        check_python_version()

        # Check dependencies BEFORE importing heavy ML libraries
        # This only runs for commands that actually need the ML stack
        if args.command in ("embed", "query", "serve"):
            # Determine which engine to check
            engine = getattr(args, "engine", "torch")  # Default to torch if not specified
            check_engine_dependencies(engine)

            # Now it's safe to import the engine-specific code
            if args.command == "embed":
                cmd_embed(args)
            elif args.command == "query":
                cmd_query(args)
            elif args.command == "serve":
                cmd_serve(args)
        else:
            # compare command only needs numpy, not the full ML stack
            if args.command == "compare":
                cmd_compare(args)
    elif args.command == "ijson-backend":
        ijson_check()
    elif args.command == "version":
        if hasattr(args, "short") and args.short:
            print(Version)
        else:
            print(f"eulix-embed version {Version}")
            print(f"Python: {sys.version.split()[0]}")
            print(f"ijson backend: {ijson_backend}")
    else:
        print_help()
        sys.exit(1)


if __name__ == "__main__":
    multiprocessing.freeze_support()
    main()
