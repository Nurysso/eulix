#!/usr/bin/env bash
# Reproduce the two #152 review findings with the real eulix_parser.
#   Issue 1: a call into a sibling workspace crate loses its edge.
#   Issue 2: same-line calls on different receivers list the caller twice
#            in kb_index.fb -> functions_calling.
#
# Usage: ./check_prism_v2_methods.sh [/path/to/eulix_parser]
#   parser defaults to <repo>/eulix-parser/target/release/eulix_parser; output in <repo>/script-test-dump/
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
PARSER=${1:-$ROOT/eulix-parser/target/release/eulix_parser}
DUMP=$ROOT/script-test-dump/check_prism_v2_methods
PY=${PYTHON:-python3}  # needs the flatbuffers package (bench/requirements.txt)
REPO=$DUMP/dummy
OUT=$DUMP/out

rm -rf "$REPO" "$OUT"
mkdir -p "$OUT"
mkdir -p "$REPO/crates/app/src" "$REPO/crates/util/src"

# --- Issue 1: Rust workspace, crate `app` calls crate `util` -------------
cat >"$REPO/Cargo.toml" <<'EOF'
[workspace]
members = ["crates/app", "crates/util"]
EOF
cat >"$REPO/crates/app/Cargo.toml" <<'EOF'
[package]
name = "app"
version = "0.1.0"
edition = "2021"

[dependencies]
util = { path = "../util" }
EOF
cat >"$REPO/crates/util/Cargo.toml" <<'EOF'
[package]
name = "util"
version = "0.1.0"
edition = "2021"
EOF
cat >"$REPO/crates/app/src/lib.rs" <<'EOF'
use util::run;

pub fn start() {
    run();
}
EOF
cat >"$REPO/crates/util/src/lib.rs" <<'EOF'
pub fn run() {}
EOF

# --- Issue 2: two calls to `get` on one line, different receivers --------
cat >"$REPO/pair.py" <<'EOF'
def total(a, b):
    return a.get() + b.get()
EOF
cat >"$REPO/pair.ts" <<'EOF'
export function total(a: any, b: any) { return a.get() + b.get(); }
EOF

"$PARSER" --root "$REPO" --output "$OUT/kb.json" --prism 2 >/dev/null

"$PY" - "$ROOT" "$OUT" <<'EOF'
import sys
sys.path.insert(0, f"{sys.argv[1]}/bench/lib")
import kbfb
out = sys.argv[2]

edges = kbfb.load_call_graph(f"{out}/kb_call_graph.fb")["edges"]
want = ("func_start::crates/app/src/lib.rs", "func_run::crates/util/src/lib.rs")
found = any((e["from"], e["to"]) == want for e in edges)
print(f"Issue 1  start -> util::run edge: {'present' if found else 'MISSING'}")

calling = kbfb.load_index(f"{out}/kb_index.fb")["functions_calling"]
callers = calling.get("get", [])
dupes = len(callers) != len(set(callers))
print(f"Issue 2  functions_calling['get'] = {callers}")
print(f"         duplicates: {'YES' if dupes else 'no'}")
EOF
echo "outputs in $OUT"

