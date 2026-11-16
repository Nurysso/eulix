#!/usr/bin/env bash
# PARSE-2: .tsx / .jsx are rejected with "Unsupported language".
# Usage: ./check_tsx.sh [workdir]   (default: <repo>/script-test-dump/check_tsx)
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
W=${1:-$ROOT/script-test-dump/check_tsx}; mkdir -p "$W"
echo 'export const a = () => <div/>;' > "$W/x.tsx"
echo 'const a = () => <div/>;'        > "$W/x.jsx"
cd "$HERE/probe"
CARGO_TARGET_DIR=${CARGO_TARGET_DIR:-$W/target} cargo run -q -- "$W/x.tsx" "$W/x.jsx"
