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
import shutil
import sys
import warnings

# NOTE: imports of cli.commands, cli.parser, server.server, or
# utils.constants is moved to main func cause if they exists at module scope here.
# Python will try to resolve those deps before check_engine_dependencies()
# below ever gets a chance to run and print a clean error message the process just
# dies with a raw ModuleNotFoundError/traceback instead.

warnings.filterwarnings("ignore", message="optimum is not installed")


def _build_parser() -> argparse.ArgumentParser:
    # cli.parser itself doesn't import anything heavy (argparse setup only),
    from cli.cli_arguments import parse_args

    return parse_args()


def print_help(parser: argparse.ArgumentParser) -> None:
    parser.print_help()
    DETAIL_COMMANDS = ("embed", "query", "serve", "compare")
    sub_action = next(a for a in parser._actions if isinstance(a, argparse._SubParsersAction))

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
            "ijson": "ijson",
            "huggingface_hub": "huggingface_hub",
        }
        engine_name = "PyTorch"
        packages = "torch transformers sentence-transformers tqdm numpy huggingface_hub ijson"

    elif engine == "onnx":
        required_packages = {
            "onnxruntime": "onnxruntime",
            "tokenizers": "tokenizers",
            "huggingface_hub": "huggingface_hub",
            "tqdm": "tqdm",
            "numpy": "numpy",
            "ijson": "ijson",
        }
        engine_name = "ONNX Runtime"
        packages = "onnxruntime tokenizers huggingface_hub tqdm numpy ijson"
    else:
        raise ValueError(f"Unknown engine: {engine}")

    # check if uv installed or not
    has_uv = shutil.which("uv") is not None
    if has_uv:
        install_cmd = f"uv pip install {packages}"
    else:
        install_cmd = f"pip install {packages}"

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

        if engine == "onnx":
            print("\nFor GPU support with ONNX Runtime:", file=sys.stderr)
            print(
                f"  NVIDIA: {install_cmd.replace('onnxruntime', 'onnxruntime-gpu')}",
                file=sys.stderr,
            )
            print(
                f"  AMD:    {install_cmd.replace('onnxruntime', 'onnxruntime-rocm')}",
                file=sys.stderr,
            )

        print("\nOr use the other engine if you have it installed:", file=sys.stderr)
        other_engine = "onnx" if engine == "torch" else "torch"
        print(f"  python eulix_embed.py --engine {other_engine} ...", file=sys.stderr)
        sys.exit(1)

    # onnxruntime usability check
    if engine == "onnx":
        try:
            import onnxruntime  # noqa: F401
        except Exception as e:
            print(
                f"❌ ERROR: 'onnxruntime' is installed but failed to load: {e}",
                file=sys.stderr,
            )
            if sys.platform == "win32":
                print(
                    "\nThis usually means the Microsoft Visual C++ Redistributable "
                    "(x64) is missing.\nDownload and install it from:\n"
                    "  https://aka.ms/vs/17/release/vc_redist.x64.exe",
                    file=sys.stderr,
                )
            sys.exit(1)

    print(f"  ✓ {engine_name} dependencies found", file=sys.stderr)


def main() -> None:
    parser = _build_parser()

    if len(sys.argv) == 1 or sys.argv[1] in ("-h", "--help", "help"):
        print_help(parser)
        sys.exit(0)

    if len(sys.argv) == 2 and sys.argv[1] in ("--version", "-V"):
        from utils.constants import Version

        print(f"{Version}")
        sys.exit(0)

    known_commands = {"embed", "query", "serve", "compare", "ijson-backend", "version"}

    # If first arg isn't a known subcommand, assume it's meant for
    # `embed` and inject it.
    if sys.argv[1] not in known_commands:
        sys.argv.insert(1, "embed")

    args = parser.parse_args()

    if args.command in ("embed", "query", "serve", "compare"):
        check_python_version()
        # Check dependencies BEFORE importing heavy ML libraries
        if args.command in ("embed", "query", "serve"):
            engine = getattr(args, "engine", "onnx")
            check_engine_dependencies(engine)

            from cli.commands import cmd_embed, cmd_query
            from server.server import cmd_serve

            if args.command == "embed":
                cmd_embed(args)
            elif args.command == "query":
                cmd_query(args)
            elif args.command == "serve":
                cmd_serve(args)
        else:
            from cli.commands import cmd_compare

            cmd_compare(args)
    elif args.command == "ijson-backend":
        from cli.commands import ijson_check

        ijson_check()
    elif args.command == "version":
        from utils.constants import Version, ijson_backend

        if hasattr(args, "short") and args.short:
            print(Version)
        else:
            print(f"eulix-embed version {Version}")
            print(f"Python: {sys.version.split()[0]}")
            print(f"ijson backend: {ijson_backend}")
    else:
        print_help(parser)
        sys.exit(1)


# checks current py version locked between 3.10-3.11 as some libs used aren't supported
# by other py versions
def check_python_version():
    """
    Hard version gate: some pinned ML dependencies (torch/transformers
    versions used elsewhere in this project) aren't validated against
    Python 3.12+, so we fail fast with actionable instructions rather than
    letting the user hit a confusing downstream import error.
    """
    MIN_VERSION = (3, 10)
    MAX_VERSION = (3, 11)

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
        print("If using 'uv', you can recreate the environment with the correct version:")
        print(f"  uv venv --python {MAX_VERSION[0]}.{MAX_VERSION[1]}")
        print("=" * 60)

        sys.exit(1)


if __name__ == "__main__":
    multiprocessing.freeze_support()
    main()
