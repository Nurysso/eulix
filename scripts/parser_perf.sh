#!/usr/bin/env bash
# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
#
# Time the currently built eulix_parser on one or more repos. Every run writes
# to a fresh output dir, so the parse cache is cold. Flags match `eulix analyze`.
#
# usage: scripts/parser_perf.sh [-n RUNS] [-t THREADS] [-p PRISM] REPO [REPO...]
#   PARSER_BIN=/path/to/eulix_parser overrides the binary.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${PARSER_BIN:-$ROOT/eulix-parser/target/release/eulix_parser}"
RUNS=3 THREADS=8 PRISM=2
while getopts "n:t:p:" o; do
  case $o in n) RUNS=$OPTARG ;; t) THREADS=$OPTARG ;; p) PRISM=$OPTARG ;; *) exit 2 ;; esac
done
shift $((OPTIND - 1))
[ $# -ge 1 ] || { sed -n '8,9p' "$0"; exit 2; }
[ -x "$BIN" ] || { echo "parser not built: $BIN (run: make parser-release)"; exit 1; }

echo "binary : $BIN"
echo "built  : $(date -r "$BIN" '+%F %T')   git: $(git -C "$ROOT" rev-parse --short HEAD)$(git -C "$ROOT" diff --quiet -- eulix-parser || echo '+dirty')"
echo "machine: $(nproc) cpus, $(awk '/MemTotal/{printf "%.1f GB", $2/1048576}' /proc/meminfo)"
echo "flags  : --threads $THREADS --prism $PRISM, $RUNS cold runs each"
echo

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
printf "%-24s %4s %10s %10s %12s %8s %8s %8s\n" repo run parse_s wall_s max_rss_mb clean partial failed
for repo in "$@"; do
  repo="$(cd "$repo" && pwd)"
  for i in $(seq "$RUNS"); do
    out="$TMP/run"; rm -rf "$out"; mkdir -p "$out"
    log="$TMP/log"
    /usr/bin/time -f "WALL %e RSS %M" -o "$TMP/time" \
      "$BIN" --root "$repo" -o "$out/kb.json" --threads "$THREADS" --prism "$PRISM" --verbose >"$log" 2>&1 \
      || { echo "parser failed on $repo:"; tail -20 "$log"; exit 1; }
    parse=$(grep -oP '^\s*Total:\s+\K[\d.]+(?=s)' "$log" | head -1 || true)
    clean=$(grep -oP '^\s*Clean:\s+\K\d+' "$log" | head -1 || true)
    partial=$(grep -oP '^\s*Partial:\s+\K\d+' "$log" | head -1 || true)
    failed=$(grep -oP '^\s*Failed:\s+\K\d+' "$log" | head -1 || true)
    read -r _ wall _ rss <"$TMP/time"
    printf "%-24s %4s %10s %10s %12s %8s %8s %8s\n" "$(basename "$repo")" "$i" \
      "${parse:--}" "$wall" "$((rss / 1024))" "${clean:--}" "${partial:--}" "${failed:--}"
  done
  # Keep the last run's full parser report for the details.
  mkdir -p "${TMPDIR:-/tmp}/parser_perf"
  cp "$log" "${TMPDIR:-/tmp}/parser_perf/$(basename "$repo").log"
done
echo
echo "full parser reports (last run per repo): ${TMPDIR:-/tmp}/parser_perf/"
