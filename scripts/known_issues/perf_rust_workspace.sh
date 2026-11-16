#!/usr/bin/env bash
# PRISM-8: time PRISM v2 on a synthetic Rust workspace where every crate defines `Error`.
# Thin wrapper over scripts/check_rust_workspace_perf.sh with a smaller default size.
# Output goes to <repo>/script-test-dump/rust_workspace_perf/.
#
# Usage: ./perf_rust_workspace.sh [/path/to/eulix_parser] [crates=40] [modules=80]
#   parser defaults to <repo>/eulix-parser/target/release/eulix_parser
# Measured on 2026-11-03: 40x80 -> 2.60s, 80x160 -> 42.05s (0 edges both times).
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
exec bash "$HERE/../check_rust_workspace_perf.sh" "${1:-$ROOT/eulix-parser/target/release/eulix_parser}" "${2:-40}" "${3:-80}"
