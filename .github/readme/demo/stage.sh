#!/usr/bin/env bash
# Stage a pinned Flask checkout for the README demo recordings.
#
# Usage: stage.sh <workdir> [--no-mcp]
#
# Clones Flask at $FLASK_TAG into <workdir>/flask, replacing any earlier copy.
# Unless --no-mcp is given, it also registers eulix with Claude Code in
# project scope, which writes <workdir>/flask/.mcp.json and nothing in
# ~/.claude.json. The walkthrough passes --no-mcp because it runs that
# command on camera.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=demo.env
source "$here/demo.env"

work="${1:?usage: stage.sh <workdir> [--no-mcp]}"
mcp=1
[[ "${2:-}" == "--no-mcp" ]] && mcp=0

for tool in git eulix claude; do
  command -v "$tool" >/dev/null || { echo "stage.sh: $tool is not on PATH" >&2; exit 1; }
done

mkdir -p "$work"
work="$(cd "$work" && pwd)"
rm -rf "$work/flask"
git clone --quiet --depth 1 --branch "$FLASK_TAG" https://github.com/pallets/flask "$work/flask" 2>/dev/null
echo "Flask $FLASK_TAG at $(git -C "$work/flask" rev-parse --short HEAD) in $work/flask"

if (( mcp )); then
  (cd "$work/flask" && claude mcp add --scope project eulix -- eulix mcp >/dev/null)
  echo "Registered eulix in $work/flask/.mcp.json"
fi
