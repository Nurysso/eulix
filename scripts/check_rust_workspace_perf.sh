#!/usr/bin/env bash
# Reproduce the PRISM v2 slowdown on large Rust workspaces (#152).
#   Every crate defines `Error` and calls its sibling crate's `Error::new()`
#   through `use`, so each call site sees one same-named type per crate.
#
# Usage: ./check_rust_workspace_perf.sh [/path/to/eulix_parser] [crates] [modules]
#   parser defaults to <repo>/eulix-parser/target/release/eulix_parser; output in <repo>/script-test-dump/
#   Defaults: 80 crates x 160 modules (dev 0.20 s, #152 at dcc1633 42.7 s).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
PARSER=${1:-$ROOT/eulix-parser/target/release/eulix_parser}
CRATES=${2:-80}
MODULES=${3:-160}
HERE=$(cd "$(dirname "$0")" && pwd)
DUMP=$ROOT/script-test-dump/rust_workspace_perf
REPO=$DUMP/dummy-workspace
OUT=$DUMP/out-workspace

rm -rf "$REPO" "$OUT"
mkdir -p "$OUT"

python3 - "$REPO" "$CRATES" "$MODULES" <<'EOF'
import os, sys
root, crates, modules = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
for c in range(crates):
    src = f"{root}/crates/c{c}/src"
    os.makedirs(src)
    lib = "".join(f"pub mod m{k};\n" for k in range(modules))
    lib += "pub struct Error;\nimpl Error { pub fn new() -> Self { Error } }\n"
    open(f"{src}/lib.rs", "w").write(lib)
    for k in range(modules):
        open(f"{src}/m{k}.rs", "w").write(
            f"use c{(c + 1) % crates}::Error;\npub fn f{k}() {{ Error::new(); }}\n"
        )
EOF

start=$(date +%s.%N)
"$PARSER" --root "$REPO" --output "$OUT/kb.json" --prism 2 >/dev/null
end=$(date +%s.%N)

python3 - "$OUT" "$start" "$end" "$CRATES" "$MODULES" <<'EOF'
import json, sys
out, start, end, crates, modules = sys.argv[1:]
edges = json.load(open(f"{out}/kb_call_graph.json"))["edges"]
print(f"{crates} crates x {modules} modules: {float(end) - float(start):.2f}s, {len(edges)} edges")
EOF
echo "outputs in $OUT"
