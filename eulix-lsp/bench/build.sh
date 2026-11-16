#!/usr/bin/env bash
# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
#
# Build eulixlsp at a git revision without touching the working tree, so two
# commits can be benchmarked side by side. Prints the binary path.
#
#   bench/build.sh bb695d1   ->  target/bench/eulixlsp-bb695d1
set -euo pipefail

lsp=$(cd "$(dirname "$0")/.." && pwd)
repo=$(git -C "$lsp" rev-parse --show-toplevel)
rev=$(git -C "$repo" rev-parse --short "${1:-HEAD}")
out="$lsp/target/bench/eulixlsp-$rev"

if [ ! -x "$out" ]; then
  src=$(mktemp -d)
  trap 'rm -rf "$src"' EXIT
  git -C "$repo" archive "$rev" libeulix eulix-lsp | tar -x -C "$src"
  CARGO_TARGET_DIR="$lsp/target/bench/cargo" \
    cargo build --release --quiet --manifest-path "$src/eulix-lsp/Cargo.toml"
  cp "$lsp/target/bench/cargo/release/eulixlsp" "$out"
fi
echo "$out"
