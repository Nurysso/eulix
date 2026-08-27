#!/bin/bash
# Convenience wrapper that picks dev or production build.
# Usage:
#   ./build.sh              → uses dev build (fast, for testing)
#   ./build.sh prod         → uses production build (slow, fully optimized)
#   ./build.sh dev          → explicitly use dev build

MODE="${1:-dev}"

case "$MODE" in
    dev)
        echo "→ Running development build (PyInstaller, fast)..."
        exec ./scripts/build-dev.sh
        ;;
    prod)
        echo "→ Running production build (Nuitka, optimized)..."
        exec ./scripts/build-prod.sh
        ;;
    *)
        echo "Usage: ./build.sh [dev|prod]"
        echo ""
        echo "  dev    - PyInstaller build (fast, 5-30 seconds, ~200MB)"
        echo "  prod   - Nuitka build (slow, 5-15 minutes, ~150MB + smaller/faster at runtime)"
        exit 1
        ;;
esac
