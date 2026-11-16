#!/usr/bin/env bash
# Eulix installer for Linux (x86-64) and macOS (Apple Silicon).
# Runs on bash 3.2+ (the bash macOS ships) and needs curl.
#
# Environment overrides:
#   EULIX_VERSION=v0.9.0   install this release tag instead of the latest
#   EULIX_GPU=amd|nvidia   skip the backend prompt (Linux only; macOS has one build)
set -euo pipefail

# Colours
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

# warn and die go to stderr so they never end up inside a $(...) capture.
info()    { echo -e "${CYAN}[INFO]${RESET}  $*"; }
success() { echo -e "${GREEN}[OK]${RESET}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${RESET}  $*" >&2; }
die()     { echo -e "${RED}[ERROR]${RESET} $*" >&2; exit 1; }

# Paths
INSTALL_DIR="$HOME/.local/bin"
EULIX_DIR="$HOME/.Eulix"
EULIX_BIN_DIR="$EULIX_DIR/bin"
EULIX_VENV="$EULIX_DIR/.venv"
EULIX_PARSER_PATH="$EULIX_BIN_DIR/eulix_parser"
EULIX_EMBED_PATH="$EULIX_DIR/eulix_embed"
VIZEULIZE_HTML_PATH="$EULIX_BIN_DIR/vizeulize.html"
FIRST_RUN_LOG="$EULIX_DIR/first_run.log"

REPO="Nurysso/eulix"
DOC_URL="https://github.com/${REPO}/blob/main/docs/Installation.md"

# Resolved at runtime inside main()
RELEASE_TAG=""
RELEASE_BASE=""

IS_UV=false
DETECTED_OS=""     # linux | darwin  (as in the release asset names)
DETECTED_ARCH=""   # amd64 | arm64
GPU_VARIANT=""     # amd | nvidia
ASSET_NAME=""
TMP_DIR=""

cleanup() {
    if [[ -n "$TMP_DIR" ]]; then
        rm -rf "$TMP_DIR"
    fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Fetch the latest release tag from the GitHub API.
# Falls back to following the /releases/latest redirect if the API is blocked
# (e.g. unauthenticated rate limit hit).
# ---------------------------------------------------------------------------
get_latest_release_tag() {
    local repo="$1"
    local api_url="https://api.github.com/repos/${repo}/releases/latest"
    local tag=""

    # Preferred: GitHub REST API
    tag=$(curl -fsSL "$api_url" 2>/dev/null \
        | grep '"tag_name":' \
        | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' \
        || true)

    # Fallback: resolve the /releases/latest HTML redirect
    if [[ -z "$tag" ]]; then
        warn "GitHub API lookup failed - falling back to redirect resolution."
        tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
            "https://github.com/${repo}/releases/latest" 2>/dev/null \
            | sed -E 's|.*/tag/||' \
            || true)
    fi

    if [[ -z "$tag" || "$tag" == *"/"* ]]; then
        die "Could not determine the latest release tag for ${repo}. Set EULIX_VERSION to pin one explicitly."
    fi

    echo "$tag"
}

# Detect OS and CPU architecture. Releases ship linux-amd64 and darwin-arm64.
Check_OS() {
    info "Detecting operating system..."
    local os arch
    os="$(uname -s)"
    arch="$(uname -m)"
    case "$os" in
        Linux*)
            case "$arch" in
                x86_64|amd64) ;;
                *)
                    echo -e "${RED}Linux on ${arch} detected.${RESET}" >&2
                    echo "Eulix releases only ship a Linux x86-64 binary." >&2
                    echo "Please build it from source - see the docs:" >&2
                    echo "  ${DOC_URL}#build-on-linux" >&2
                    exit 1
                    ;;
            esac
            DETECTED_OS="linux"
            DETECTED_ARCH="amd64"
            success "Detected Linux (x86-64)."
            ;;
        Darwin*)
            # A shell running under Rosetta reports x86_64 on an Apple Silicon Mac.
            if [[ "$arch" == "x86_64" ]] \
                && [[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" == "1" ]]; then
                arch="arm64"
            fi
            if [[ "$arch" != "arm64" ]]; then
                echo -e "${RED}Intel Mac detected.${RESET}" >&2
                echo "Eulix releases only ship a macOS binary for Apple Silicon (M series)." >&2
                echo "Please build it from source - see the docs:" >&2
                echo "  ${DOC_URL}#build-on-macos" >&2
                exit 1
            fi
            DETECTED_OS="darwin"
            DETECTED_ARCH="arm64"
            success "Detected macOS (Apple Silicon)."
            ;;
        CYGWIN*|MINGW*|MSYS*|Windows*)
            echo -e "${RED}Windows detected.${RESET}" >&2
            echo "Please run install.ps1 instead, or follow the Eulix installation document:" >&2
            echo "  ${DOC_URL}" >&2
            exit 1
            ;;
        *)
            die "Unsupported OS: $os. See the Eulix installation document for manual steps: ${DOC_URL}"
            ;;
    esac
}

# Ask user which GPU/backend variant to download
choose_gpu_variant() {
    if [[ "$DETECTED_OS" == "darwin" ]]; then
        # The release has a single macOS build. There is no CUDA on a Mac;
        # onnxruntime runs on the CPU or through CoreML.
        GPU_VARIANT="amd"
        success "macOS has a single ONNX build (onnx-amd: CPU / CoreML)."
        return
    fi

    if [[ -n "${EULIX_GPU:-}" ]]; then
        case "$EULIX_GPU" in
            amd|nvidia) GPU_VARIANT="$EULIX_GPU" ;;
            *) die "EULIX_GPU must be 'amd' or 'nvidia', got '$EULIX_GPU'." ;;
        esac
        success "Selected variant (EULIX_GPU): $GPU_VARIANT"
        return
    fi

    # With `curl ... | bash` the script itself is on stdin (BASH_SOURCE is
    # empty), so reading stdin would eat script lines: ask the terminal instead.
    local input="/dev/stdin"
    if [[ -z "${BASH_SOURCE[0]:-}" ]]; then
        if { : </dev/tty; } 2>/dev/null; then
            input="/dev/tty"
        else
            die "No terminal to ask for the GPU variant. Set EULIX_GPU=amd or EULIX_GPU=nvidia and run again."
        fi
    fi

    info "Select the ONNX backend variant to install:"
    echo "  1) amd     - CPU / AMD (onnx-amd)"
    echo "  2) nvidia  - NVIDIA GPU (onnx-nvidia)"
    local choice
    while true; do
        read -rp "Enter choice [1-2]: " choice <"$input" \
            || die "No answer received. Set EULIX_GPU=amd or EULIX_GPU=nvidia and run again."
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
        success "Compatible virtual environment already exists (Python $existing_ver) - reusing it."
        return
    fi

    if [[ -e "$EULIX_VENV" ]]; then
        warn "Existing venv at $EULIX_VENV is missing or incompatible (found: ${existing_ver:-none}) - recreating."
        # uv refuses to overwrite an existing venv without --clear, which older uv lacks.
        rm -rf "$EULIX_VENV"
    fi

    uv venv --python 3.11 "$EULIX_VENV"
    success "Virtual environment created at $EULIX_VENV."
}

# Portable SHA-256: coreutils on Linux, shasum on macOS.
sha256_of() {
    if command -v sha256sum &>/dev/null; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum &>/dev/null; then
        shasum -a 256 "$1" | awk '{print $1}'
    elif command -v openssl &>/dev/null; then
        openssl dgst -sha256 "$1" | awk '{print $NF}'
    else
        die "No SHA-256 tool found (need sha256sum, shasum or openssl)."
    fi
}

# Expected SHA-256 for a file name in checksums.txt, ignoring any directory
# prefix or binary-mode '*' marker on the name.
get_checksum() {
    local target_filename="$1"
    local checksums_file="$2"

    awk -v fn="$target_filename" '{
        f = $2; sub(/\r$/, "", f); sub(/^\*/, "", f)
        n = split(f, parts, "/")
        if (parts[n] == fn) { print $1; exit }
    }' "$checksums_file"
}

# Compare a downloaded file against its checksums.txt entry; die on mismatch.
verify_checksum() {
    local file="$1" name="$2" checksums_file="$3"
    local expected actual
    expected="$(get_checksum "$name" "$checksums_file")"
    [[ -n "$expected" ]] || die "Missing checksum entry for ${name}."
    actual="$(sha256_of "$file")"
    if [[ "$expected" != "$actual" ]]; then
        die "Checksum mismatch for ${name}! Expected ${expected}, got ${actual}."
    fi
    success "${name} checksum verified."
}

# Download the release binary for the chosen OS/GPU variant and vizeulize.html, verify checksums, install them
download_binary() {
    ASSET_NAME="eulix-${DETECTED_OS}-${DETECTED_ARCH}-onnx-${GPU_VARIANT}"
    local html_asset_name="vizeulize.html"

    local asset_url="${RELEASE_BASE}/${ASSET_NAME}"
    local html_url="${RELEASE_BASE}/${html_asset_name}"

    TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/eulix-install.XXXXXX")"
    local tmp_bin="$TMP_DIR/${ASSET_NAME}"
    local tmp_html="$TMP_DIR/${html_asset_name}"
    local tmp_checksums="$TMP_DIR/checksums.txt"

    info "Downloading ${ASSET_NAME} (${RELEASE_TAG})..."
    curl -fL --progress-bar "$asset_url" -o "$tmp_bin" \
        || die "Failed to download $asset_url"

    info "Downloading ${html_asset_name} (${RELEASE_TAG})..."
    curl -fL --progress-bar "$html_url" -o "$tmp_html" \
        || die "Failed to download $html_url"

    # Releases up to v0.8.3 named the file checksum.txt.
    info "Downloading checksums for verification..."
    curl -fsSL "${RELEASE_BASE}/checksums.txt" -o "$tmp_checksums" 2>/dev/null \
        || curl -fsSL "${RELEASE_BASE}/checksum.txt" -o "$tmp_checksums" \
        || die "Failed to download checksums.txt (or checksum.txt) from ${RELEASE_BASE}"

    info "Verifying checksums..."
    verify_checksum "$tmp_bin" "$ASSET_NAME" "$tmp_checksums"
    verify_checksum "$tmp_html" "$html_asset_name" "$tmp_checksums"

    # Install CLI binary
    mkdir -p "$INSTALL_DIR"
    chmod +x "$tmp_bin"
    if [[ "$DETECTED_OS" == "darwin" ]] && command -v xattr &>/dev/null; then
        xattr -d com.apple.quarantine "$tmp_bin" 2>/dev/null || true
    fi
    mv -f "$tmp_bin" "$INSTALL_DIR/eulix"
    success "Installed binary → $INSTALL_DIR/eulix"

    # Install vizeulize.html into ~/.Eulix/bin/
    mkdir -p "$EULIX_BIN_DIR"
    mv -f "$tmp_html" "$VIZEULIZE_HTML_PATH"
    success "Installed UI webapp → $VIZEULIZE_HTML_PATH"
}

# eulix unpacks its parser and eulix_embed scripts only when they are missing,
# then checks the parser's hash. On an upgrade the old files are still there,
# so the new binary would fail that check; and an existing .initialized marker
# would skip installing the Python packages for the variant just chosen.
# Remove all three so the first launch below sets them up fresh.
reset_extracted_assets() {
    local removed=false f
    for f in "$EULIX_PARSER_PATH" "$EULIX_PARSER_PATH".old-* "$EULIX_PARSER_PATH".tmp-* \
             "$EULIX_EMBED_PATH" "$EULIX_DIR/.initialized"; do
        if [[ -e "$f" ]]; then
            rm -rf "$f"
            removed=true
        fi
    done
    if [[ "$removed" == true ]]; then
        info "Removed files unpacked by a previous install; this version unpacks its own."
    fi
}

# Trigger first-run self-extraction (unpacks embedded parser + eulix_embed, sets up venv deps)
run_first_launch_setup() {
    info "Running first-launch setup (self-extracts parser + eulix_embed, installs Python packages)..."
    info "Downloading the Python packages can take a few minutes."
    mkdir -p "$EULIX_DIR"
    if ! "$INSTALL_DIR/eulix" --help &>"$FIRST_RUN_LOG"; then
        warn "First-run setup exited non-zero - check $FIRST_RUN_LOG for details."
        # On macOS ensure_macos_embed_deps runs next and handles the packages.
        [[ "$DETECTED_OS" == "darwin" ]] || warn "Retry the Python package install with: eulix getEmbedDeps"
    else
        success "First-launch setup complete."
    fi
}

# Releases up to v0.8.3 list onnxruntime-rocm in onnx-amd.txt, and it has no
# macOS build, so the first launch cannot install the embedding packages on a
# Mac. Install the plain onnxruntime wheel (CPU + CoreML) instead.
ensure_macos_embed_deps() {
    [[ "$DETECTED_OS" == "darwin" ]] || return 0
    if "$EULIX_VENV/bin/python" -c 'import onnxruntime, tokenizers' &>/dev/null; then
        return 0
    fi

    local base_req="$EULIX_EMBED_PATH/requiremenents/requirements.base.txt"
    if [[ ! -f "$base_req" ]]; then
        warn "Cannot find $base_req; skipping the macOS package fallback."
        return 0
    fi

    warn "The embedding packages are not installed; installing onnxruntime for macOS."
    if uv pip install --python "$EULIX_VENV" -r "$base_req" onnxruntime tokenizers; then
        success "Embedding packages installed."
    else
        warn "Package install failed. Retry with: uv pip install --python $EULIX_VENV -r $base_req onnxruntime tokenizers"
    fi
}

# PATH reminder
ensure_path() {
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        # The rc paths below are display text, so the '~' must stay literal.
        # shellcheck disable=SC2088
        local rc="your shell rc file"
        # shellcheck disable=SC2088
        case "${SHELL:-}" in
            */zsh)  rc="~/.zshrc" ;;
            */bash) if [[ "$DETECTED_OS" == "darwin" ]]; then rc="~/.bash_profile"; else rc="~/.bashrc"; fi ;;
        esac
        warn "$INSTALL_DIR is not in your PATH."
        echo -e "  Add this to ${rc}:\n"
        echo -e "    ${BOLD}export PATH=\"\$HOME/.local/bin:\$PATH\"${RESET}\n"
    fi
}

# Main
main() {
    # --- Resolve release tag (latest, or pinned via EULIX_VERSION) ---
    if [[ -n "${EULIX_VERSION:-}" ]]; then
        RELEASE_TAG="$EULIX_VERSION"
        success "Using pinned release: ${RELEASE_TAG}"
    else
        info "Resolving latest Eulix release..."
        RELEASE_TAG="$(get_latest_release_tag "$REPO")"
        success "Latest release: ${RELEASE_TAG}"
    fi
    RELEASE_BASE="https://github.com/${REPO}/releases/download/${RELEASE_TAG}"

    echo -e "\n${BOLD}${CYAN}╔════════════════════════════════════════╗"
    echo -e "║   Eulix Installer  (${RELEASE_TAG})"
    echo -e "╚════════════════════════════════════════╝${RESET}\n"

    Check_OS
    choose_gpu_variant
    install_UV
    setup_venv
    download_binary
    reset_extracted_assets
    run_first_launch_setup
    ensure_macos_embed_deps
    ensure_path

    echo -e "\n${GREEN}${BOLD}✓ Eulix installed successfully!${RESET}"
    echo -e "  Version       : ${BOLD}$RELEASE_TAG${RESET}"
    echo -e "  Binary        : ${BOLD}$INSTALL_DIR/eulix${RESET}"
    echo -e "  Data dir      : ${BOLD}$EULIX_DIR${RESET}"
    echo -e "  Venv          : ${BOLD}$EULIX_VENV${RESET}"
    echo -e "  eulix_parser  : ${BOLD}$EULIX_PARSER_PATH${RESET}"
    echo -e "  eulix_embed   : ${BOLD}$EULIX_EMBED_PATH${RESET}"
    echo -e "  vizeulize.html: ${BOLD}$VIZEULIZE_HTML_PATH${RESET}"
    echo -e "  Run           : ${BOLD}eulix --help${RESET}\n"

    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        echo -e "  ${YELLOW}Note:${RESET} run the export line above (or restart your shell) before using 'eulix'.\n"
    fi
}

main "$@"
