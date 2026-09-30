#!/usr/bin/env bash

# Exit immediately if a command exits with a non-zero status
set -euo pipefail

# Configuration
COVERAGE_DIR="coverage"
OUT_FILE="${COVERAGE_DIR}/coverage.out"
HTML_FILE="${COVERAGE_DIR}/coverage.html"

# Ensure output directory exists
mkdir -p "${COVERAGE_DIR}"

echo " Running tests and generating coverage profile..."
go test ./... -coverprofile="${OUT_FILE}" -coverpkg=./...

echo " Generating HTML coverage report..."
go tool cover -html="${OUT_FILE}" -o "${HTML_FILE}"

# Print total coverage summary to terminal
echo " Coverage Summary:"
go tool cover -func="${OUT_FILE}" | tail -n 1

# Open report safely without relying on broken xdg-open
open_browser() {
  local target="$1"

  if grep -q -i microsoft /proc/version 2>/dev/null; then
    wslview "${target}" &>/dev/null || cmd.exe /c start "" "${target}" &>/dev/null && return 0
  fi

  if command -v open &>/dev/null && [[ "$OSTYPE" == "darwin"* ]]; then
    open "${target}" && return 0
  fi

  for browser in firefox google-chrome chromium brave; do
    if command -v "$browser" &>/dev/null; then
      "$browser" "${target}" &>/dev/null &
      return 0
    fi
  done

  echo ""
  echo " Report ready! Open this URL in your browser:"
  echo "   file://$(pwd)/${target}"
}

echo ""
echo " Opening report..."
open_browser "${HTML_FILE}"
