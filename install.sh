#!/usr/bin/env bash
set -euo pipefail

# Colours
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}[INFO]${RESET}  $*"; }
success() { echo -e "${GREEN}[OK]${RESET}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${RESET}  $*"; }
die()     { echo -e "${RED}[ERROR]${RESET} $*" >&2; exit 1; }

# Paths
INSTALL_DIR="$HOME/.local/bin"
EULIX_DIR="$HOME/.Eulix"
EULIX_VENV="$EULIX_DIR/.venv"
EULIX_PARSER_PATH="$EULIX_DIR/bin/eulix_parser"
EULIX_EMBED_PATH="$EULIX_DIR/eulix_embed"

REPO="Nurysso/eulix"
RELEASE_TAG="v0.8.0"
RELEASE_BASE="https://github.com/${REPO}/releases/download/${RELEASE_TAG}"
DOC_URL="https://github.com/${REPO}/blob/main/docs/install.md"

IS_UV=false
DETECTED_OS=""
GPU_VARIANT=""   # amd | nvidia
ASSET_NAME=""

# Detect OS (linux/mac only — mac unsupported for now)
Check_OS() {
    info "Detecting operating system..."
    local os
    os="$(uname -s)"
    case "$os" in
        Linux*)
            DETECTED_OS="linux"
            success "Detected Linux."
            ;;
        Darwin*)
            echo -e "${RED}macOS detected.${RESET}"
            echo "Eulix currently doesn't ship a macOS binary."
            echo "Please build it manually — see the docs:"
            echo "  ${DOC_URL}"
            exit 1
            ;;
        CYGWIN*|MINGW*|MSYS*|Windows*)
            echo -e "${RED}Windows detected.${RESET}"
            echo "Please run install.ps1 instead, or follow the Eulix installation document:"
            echo "  ${DOC_URL}"
            exit 1
            ;;
        *)
            die "Unsupported OS: $os. See the Eulix installation document for manual steps."
            ;;
    esac
}

# Ask user which GPU/backend variant to download
choose_gpu_variant() {
    info "Select the ONNX backend variant to install:"
    echo "  1) amd     — CPU / AMD (onnx-amd)"
    echo "  2) nvidia  — NVIDIA GPU (onnx-nvidia)"
    local choice
    while true; do
        read -rp "Enter choice [1-2]: " choice
        case "$choice" in
            1) GPU_VARIANT="amd"; break ;;
            2) GPU_VARIANT="nvidia"; break ;;
            *) warn "Invalid choice, enter 1 or 2." ;;
        esac
    done
    success "Selected variant: $GPU_VARIANT"
}

# Check if uv is available
check_uv() {
    if command -v uv &>/dev/null; then
        IS_UV=true
        success "uv found: $(uv --version)"
    else
        warn "uv not found."
    fi
}

# Install uv
install_UV() {
    check_uv
    if [[ "$IS_UV" == true ]]; then
        info "uv already installed; skipping."
        return
    fi

    info "Installing uv..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"

    if command -v uv &>/dev/null; then
        IS_UV=true
        success "uv installed: $(uv --version)"
    else
        die "uv installation failed. Please install manually: https://github.com/astral-sh/uv"
    fi
}

# Create the Python venv used by Eulix
setup_venv() {
    info "Setting up Python venv at $EULIX_VENV..."
    mkdir -p "$EULIX_DIR"

    local existing_ver=""
    if [[ -x "$EULIX_VENV/bin/python" ]]; then
        existing_ver="$("$EULIX_VENV/bin/python" -c 'import sys; print("%d.%d" % sys.version_info[:2])' 2>/dev/null || true)"
    fi

    if [[ "$existing_ver" == "3.11" ]]; then
        success "Compatible virtual environment already exists (Python $existing_ver) — reusing it."
        return
    fi

    if [[ -d "$EULIX_VENV" ]]; then
        warn "Existing venv at $EULIX_VENV is missing or incompatible (found: ${existing_ver:-none}) — recreating."
    fi

    uv venv --python 3.11 "$EULIX_VENV"
    success "Virtual environment created at $EULIX_VENV."
}

# Download the release binary for the chosen OS/GPU variant, verify checksum, install it
download_binary() {
    ASSET_NAME="eulix-linux-amd64-onnx-${GPU_VARIANT}"
    local asset_url="${RELEASE_BASE}/${ASSET_NAME}"
    local checksums_url="${RELEASE_BASE}/checksums.txt"
    local tmp_bin="/tmp/${ASSET_NAME}"
    local tmp_checksums="/tmp/eulix_checksums.txt"

    info "Downloading ${ASSET_NAME} (${RELEASE_TAG})..."
    curl -fL --progress-bar "$asset_url" -o "$tmp_bin" \
        || die "Failed to download $asset_url"

    info "Downloading checksums.txt for verification..."
    curl -fL -sS "$checksums_url" -o "$tmp_checksums" \
        || die "Failed to download $checksums_url"

    info "Verifying checksum..."
    local expected actual
    expected="$(grep "  *${ASSET_NAME}\$" "$tmp_checksums" | awk '{print $1}')"
    if [[ -z "$expected" ]]; then
        warn "Could not find ${ASSET_NAME} in checksums.txt — skipping verification."
    else
        actual="$(sha256sum "$tmp_bin" | awk '{print $1}')"
        if [[ "$expected" != "$actual" ]]; then
            rm -f "$tmp_bin" "$tmp_checksums"
            die "Checksum mismatch for ${ASSET_NAME}! Expected ${expected}, got ${actual}."
        fi
        success "Checksum verified."
    fi
    rm -f "$tmp_checksums"

    mkdir -p "$INSTALL_DIR"
    chmod +x "$tmp_bin"
    mv "$tmp_bin" "$INSTALL_DIR/eulix"
    success "Installed binary → $INSTALL_DIR/eulix"
}

# Trigger first-run self-extraction (unpacks embedded parser + eulix_embed, sets up venv deps)
run_first_launch_setup() {
    info "Running first-launch setup (self-extracts parser + eulix_embed, installs deps)..."
    if ! "$INSTALL_DIR/eulix" --help &>/tmp/eulix_first_run.log; then
        warn "First-run setup exited non-zero — check /tmp/eulix_first_run.log for details."
    else
        success "First-launch setup complete."
    fi
}

# PATH reminder
ensure_path() {
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        warn "$INSTALL_DIR is not in your PATH."
        echo -e "  Add this to your shell rc file:\n"
        echo -e "    ${BOLD}export PATH=\"\$HOME/.local/bin:\$PATH\"${RESET}\n"
    fi
}

# Main
main() {
    echo -e "\n${BOLD}${CYAN}╔══════════════════════════════╗"
    echo -e "║   Eulix Installer ${RELEASE_TAG}      ║"
    echo -e "╚══════════════════════════════╝${RESET}\n"

    Check_OS
    choose_gpu_variant
    install_UV
    setup_venv
    download_binary
    run_first_launch_setup
    ensure_path

    echo -e "\n${GREEN}${BOLD}✓ Eulix installed successfully!${RESET}"
    echo -e "  Binary        : ${BOLD}$INSTALL_DIR/eulix${RESET}"
    echo -e "  Data dir      : ${BOLD}$EULIX_DIR${RESET}"
    echo -e "  Venv          : ${BOLD}$EULIX_VENV${RESET}"
    echo -e "  eulix_parser  : ${BOLD}$EULIX_PARSER_PATH${RESET}"
    echo -e "  eulix_embed   : ${BOLD}$EULIX_EMBED_PATH${RESET}"
    echo -e "  Run           : ${BOLD}eulix --help${RESET}\n"

    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        echo -e "  ${YELLOW}Note:${RESET} run the export line above (or restart your shell) before using 'eulix'.\n"
    fi
}

main "$@"
