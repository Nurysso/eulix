#!/usr/bin/env bash
set -euo pipefail

# ─── Config ───────────────────────────────────────────────────────────────────
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ─── Logging ──────────────────────────────────────────────────────────────────
info()    { printf '\033[0;36m[info]\033[0m  %s\n' "$*"; }
ok()      { printf '\033[0;32m[ ok ]\033[0m  %s\n' "$*"; }
warn()    { printf '\033[0;33m[warn]\033[0m  %s\n' "$*"; }
die()     { printf '\033[0;31m[err ]\033[0m  %s\n' "$*" >&2; exit 1; }

# ─── Dependency checks ────────────────────────────────────────────────────────
check_deps() {
    info "Checking dependencies..."
    command -v cargo &>/dev/null || die "Rust/Cargo not found. Install via https://rustup.rs"
    command -v go    &>/dev/null || die "Go not found. Install via https://go.dev"
    ok "cargo $(cargo --version | awk '{print $2}')"
    ok "go $(go version | awk '{print $3}')"
}

# ─── GPU Detection ────────────────────────────────────────────────────────────
detect_gpu() {
    info "Detecting hardware..."

    # macOS — distinguish Apple Silicon from Intel
    if [[ "$OSTYPE" == darwin* ]]; then
        local arch; arch="$(uname -m)"
        if [[ "$arch" == "arm64" ]]; then
            warn "Apple Silicon (arm64) detected — using Metal via CoreML."
            echo "apple_silicon"
        else
            warn "Intel Mac detected — building for CPU."
            echo "cpu"
        fi
        return
    fi

    # Linux — prefer tool-based detection, fall back to lspci
    if command -v nvidia-smi &>/dev/null && nvidia-smi --query-gpu=name --format=csv,noheader &>/dev/null; then
        local gpu_name; gpu_name="$(nvidia-smi --query-gpu=name --format=csv,noheader | head -1)"
        ok "NVIDIA GPU: $gpu_name"
        echo "cuda"
        return
    fi

    if command -v rocm-smi &>/dev/null && rocm-smi --showproductname &>/dev/null; then
        ok "AMD GPU (ROCm) detected."
        echo "rocm"
        return
    fi

    # lspci fallback — only if available
    if command -v lspci &>/dev/null; then
        local pci; pci="$(lspci 2>/dev/null)"
        if echo "$pci" | grep -qi 'nvidia'; then
            warn "NVIDIA GPU detected via lspci (nvidia-smi unavailable — CUDA drivers may be missing)."
            echo "cuda"
            return
        fi
        if echo "$pci" | grep -qiE 'amd|radeon'; then
            warn "AMD GPU detected via lspci (rocm-smi unavailable — ROCm may not be installed)."
            echo "rocm"
            return
        fi
    fi

    warn "No supported GPU detected — building for CPU."
    echo "cpu"
}

# ─── Build ────────────────────────────────────────────────────────────────────
build() {
    local gpu_type="$1"
    info "Starting build (GPU_TYPE=$gpu_type)..."

    mkdir -p "$INSTALL_DIR"

    make install \
        GPU_TYPE="$gpu_type" \
        INSTALL_DIR="$INSTALL_DIR" \
        || die "Build failed. See output above."

    ok "Binaries installed to $INSTALL_DIR"
}

# ─── PATH reminder ────────────────────────────────────────────────────────────
path_reminder() {
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        warn "$INSTALL_DIR is not in your PATH."
        warn "Add this to your shell rc:  export PATH=\"\$PATH:$INSTALL_DIR\""
    fi
}

# ─── Main ─────────────────────────────────────────────────────────────────────
main() {
    cd "$REPO_ROOT"
    check_deps
    local gpu_type; gpu_type="$(detect_gpu)"
    build "$gpu_type"
    path_reminder
    ok "Installation complete."
}

main "$@"