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
REPO_URL="https://github.com/nurysso/eulix"
CLONE_DIR="$HOME/eulix"

IS_UV=false
# SUPPORTED_OS=false
DETECTED_OS=""
COMPUTE_PLATFORM=""   # cuda126 | cuda130 | cuda132 | rocm72 | cpu | mac
PKG_MGR=""
GO_OS=""
GO_ARCH=""

# Check OS
Check_OS() {
    info "Detecting operating system..."
    local os
    os="$(uname -s)"
    case "$os" in
        Linux*)
            DETECTED_OS="linux"
            # SUPPORTED_OS=true
            # Detect distro family for package manager selection
            if   command -v apt-get  &>/dev/null; then PKG_MGR="apt"
            elif command -v dnf      &>/dev/null; then PKG_MGR="dnf"
            elif command -v yum      &>/dev/null; then PKG_MGR="yum"
            elif command -v pacman   &>/dev/null; then PKG_MGR="pacman"
            elif command -v zypper   &>/dev/null; then PKG_MGR="zypper"
            elif command -v apk      &>/dev/null; then PKG_MGR="apk"
            else
                warn "No supported package manager found; will fall back to upstream installers."
                PKG_MGR="none"
            fi
            success "Detected Linux (package manager: ${PKG_MGR})."
            ;;
        Darwin*)
            DETECTED_OS="mac"
            # SUPPORTED_OS=true
            if command -v brew &>/dev/null; then
                PKG_MGR="brew"
            else
                warn "Homebrew not found. Installing it now..."
                /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
                # Add brew to PATH for Apple Silicon
                [[ -f /opt/homebrew/bin/brew ]] && eval "$(/opt/homebrew/bin/brew shellenv)"
                PKG_MGR="brew"
            fi
            success "Detected macOS (package manager: ${PKG_MGR})."
            ;;
        CYGWIN*|MINGW*|MSYS*|Windows*)
            echo -e "${RED}Windows detected.${RESET}"
            echo "Please run install.ps1 instead, or follow the Eulix installation document:"
            echo "  https://github.com/nurysso/eulix/blob/main/docs/install.md"
            exit 1
            ;;
        *)
            die "Unsupported OS: $os. See the Eulix installation document for manual steps."
            ;;
    esac
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

# Verify existing deps (python / rust / go)
check_deps() {
    check_uv
    info "Checking dependencies..."
    local missing=()

    if ! command -v python3 &>/dev/null; then
        missing+=("python3")
    else
        success "python3 found: $(python3 --version)"
    fi

    if ! command -v cargo &>/dev/null; then
        missing+=("cargo/rust")
    else
        success "cargo found: $(cargo --version)"
    fi

    if ! command -v go &>/dev/null; then
        missing+=("go")
    else
        success "go found: $(go version)"
    fi

    if [[ ${#missing[@]} -gt 0 ]]; then
        warn "Missing: ${missing[*]} — will install via install_deps."
        install_deps
    fi
}


# Package manager helpers

# pkg_install <pkg-apt> <pkg-dnf/yum> <pkg-pacman> <pkg-zypper> <pkg-apk> <pkg-brew>
# Pass "-" to skip a slot for a given manager.
pkg_install() {
    local apt_pkg="$1" dnf_pkg="$2" pac_pkg="$3" zyp_pkg="$4" apk_pkg="$5" brew_pkg="$6"
    case "$PKG_MGR" in
        apt)     [[ "$apt_pkg"  != "-" ]] && sudo apt-get install -y "$apt_pkg"  ;;
        dnf)     [[ "$dnf_pkg"  != "-" ]] && sudo dnf     install -y "$dnf_pkg"  ;;
        yum)     [[ "$dnf_pkg"  != "-" ]] && sudo yum     install -y "$dnf_pkg"  ;;
        pacman)  [[ "$pac_pkg"  != "-" ]] && sudo pacman  -S --noconfirm "$pac_pkg" ;;
        zypper)  [[ "$zyp_pkg"  != "-" ]] && sudo zypper  install -y "$zyp_pkg"  ;;
        apk)     [[ "$apk_pkg"  != "-" ]] && sudo apk     add        "$apk_pkg"  ;;
        brew)    [[ "$brew_pkg" != "-" ]] && brew install            "$brew_pkg" ;;
    esac
}

# Ensure the package index is up to date (run at most once per session)
_PKG_UPDATED=false
pkg_update() {
    [[ "$_PKG_UPDATED" == true ]] && return
    info "Updating package index..."
    case "$PKG_MGR" in
        apt)    sudo apt-get update -y  ;;
        dnf)    sudo dnf     check-update -y || true ;;
        yum)    sudo yum     check-update    || true ;;
        pacman) sudo pacman  -Sy             ;;
        zypper) sudo zypper  refresh -y      ;;
        apk)    sudo apk     update          ;;
        brew)   brew update                  ;;
    esac
    _PKG_UPDATED=true
}

# Install Individual Tools

install_rust() {
    info "Installing Rust (stable)..."

    if [[ "$PKG_MGR" != "none" ]]; then
        pkg_update
        # rustup is the canonical way on every platform; distro packages are
        # often stale, so prefer rustup but install curl first if needed.
        if ! command -v curl &>/dev/null; then
            pkg_install curl curl curl curl curl curl
        fi
    fi

    # Use rustup on all platforms for a current, PATH-managed toolchain
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
        | sh -s -- -y --default-toolchain stable
    # shellcheck source=/dev/null
    source "$HOME/.cargo/env"
    success "Rust installed: $(cargo --version)"
}

install_go() {
    info "Installing Go..."

    if [[ "$PKG_MGR" != "none" ]]; then
        pkg_update
        case "$PKG_MGR" in
            apt)
                # golang-go in older Ubuntu/Debian can be very stale;
                # use the backports PPA on Ubuntu if available, else fall through
                local go_pkg="golang-go"
                if command -v lsb_release &>/dev/null; then
                    local distro
                    distro="$(lsb_release -is)"
                    if [[ "$distro" == "Ubuntu" ]]; then
                        if ! grep -qr "longsleep/golang-backports" /etc/apt/sources.list.d/ 2>/dev/null; then
                            info "Adding golang-backports PPA for a recent Go release..."
                            sudo add-apt-repository -y ppa:longsleep/golang-backports
                            sudo apt-get update -y
                        fi
                        go_pkg="golang-go"
                    fi
                fi
                sudo apt-get install -y "$go_pkg"
                ;;
            dnf|yum) sudo "${PKG_MGR}" install -y golang ;;
            pacman)  sudo pacman -S --noconfirm go       ;;
            zypper)  sudo zypper  install -y   go        ;;
            apk)     sudo apk     add          go        ;;
            brew)    brew install go                      ;;
            none)    _install_go_upstream ;;
        esac

        if command -v go &>/dev/null; then
            success "Go installed via ${PKG_MGR}: $(go version)"
            return
        fi

        warn "System Go install did not produce a working binary; falling back to upstream tarball."
    fi

    _install_go_upstream
}

# Upstream tarball fallback (mirrors original logic, version bumped to 1.24)
_install_go_upstream() {
    local go_version="1.24.0"
    local arch go_url go_tar
    arch="$(uname -m)"

    if [[ "$DETECTED_OS" == "mac" ]]; then
        [[ "$arch" == "arm64" ]] \
            && go_url="https://go.dev/dl/go${go_version}.darwin-arm64.tar.gz" \
            || go_url="https://go.dev/dl/go${go_version}.darwin-amd64.tar.gz"
    else
        [[ "$arch" == "aarch64" || "$arch" == "arm64" ]] \
            && go_url="https://go.dev/dl/go${go_version}.linux-arm64.tar.gz" \
            || go_url="https://go.dev/dl/go${go_version}.linux-amd64.tar.gz"
    fi

    go_tar="/tmp/go_install.tar.gz"
    curl -sSL "$go_url" -o "$go_tar"
    sudo tar -C /usr/local -xzf "$go_tar"
    rm -f "$go_tar"
    export PATH="$PATH:/usr/local/go/bin"
    success "Go installed (upstream): $(go version)"
}

install_python() {
    info "Installing Python 3..."

    if [[ "$PKG_MGR" != "none" ]]; then
        pkg_update
        # pkg_install <apt>          <dnf/yum>   <pacman>  <zypper>  <apk>       <brew>
        pkg_install   python3        python3      python    python3   python3     python@3.12

        if command -v python3 &>/dev/null; then
            success "Python installed via ${PKG_MGR}: $(python3 --version)"
            return
        fi
        warn "System Python install did not produce a working binary; falling back to uv."
    fi

    # uv fallback
    if [[ "$IS_UV" == true ]]; then
        info "Installing Python 3.10 via uv..."
        uv python install 3.10
        success "Python 3.10 installed via uv."
    else
        die "python3 not found and no suitable installer is available. Install Python 3.10+ manually."
    fi
}

# Install python / rust / go if absent
install_deps() {
    info "Installing missing dependencies..."

    command -v cargo   &>/dev/null || install_rust
    command -v go      &>/dev/null || install_go
    command -v python3 &>/dev/null || install_python
}

# Install uv
install_UV() {
    if [[ "$IS_UV" == true ]]; then
        info "uv already installed; skipping."
        return
    fi

    info "Installing uv..."
    if [[ "$DETECTED_OS" == "mac" ]] || [[ "$DETECTED_OS" == "linux" ]]; then
        curl -LsSf https://astral.sh/uv/install.sh | sh
        export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"
    fi

    if command -v uv &>/dev/null; then
        IS_UV=true
        success "uv installed: $(uv --version)"
    else
        die "uv installation failed. Please install manually: https://github.com/astral-sh/uv"
    fi
}

# Detect GPU / compute platform
compute_platform() {
    info "Detecting compute platform..."

    if [[ "$DETECTED_OS" == "mac" ]]; then
        COMPUTE_PLATFORM="mac"
        success "macOS — will use MPS/CPU PyTorch build."
        return
    fi

    # NVIDIA
    if command -v nvidia-smi &>/dev/null; then
        local cuda_ver
        cuda_ver="$(nvidia-smi | grep -oP 'CUDA Version: \K[0-9]+\.[0-9]+')" || true

        if [[ -z "$cuda_ver" ]]; then
            warn "nvidia-smi found but could not parse CUDA version; defaulting to CPU."
            COMPUTE_PLATFORM="cpu"
            return
        fi

        info "NVIDIA GPU detected — CUDA $cuda_ver"
        local major minor
        IFS='.' read -r major minor <<< "$cuda_ver"
        local ver_int=$(( major * 10 + minor ))

        if   (( ver_int >= 132 )); then COMPUTE_PLATFORM="cuda132"
        elif (( ver_int >= 130 )); then COMPUTE_PLATFORM="cuda130"
        elif (( ver_int >= 126 )); then COMPUTE_PLATFORM="cuda126"
        else
            warn "CUDA $cuda_ver is below 12.6; falling back to CPU build."
            COMPUTE_PLATFORM="cpu"
        fi
        success "Compute platform: $COMPUTE_PLATFORM"
        return
    fi

    # AMD / ROCm
    if command -v rocminfo &>/dev/null || [[ -d /opt/rocm ]]; then
        local rocm_ver=""
        if command -v rocminfo &>/dev/null; then
            rocm_ver="$(rocminfo 2>/dev/null | grep -oP 'ROCm Version: \K[0-9]+\.[0-9]+')" || true
        fi
        if [[ -z "$rocm_ver" ]] && [[ -f /opt/rocm/.info/version ]]; then
            rocm_ver="$(cat /opt/rocm/.info/version | grep -oP '^[0-9]+\.[0-9]+')" || true
        fi

        info "AMD GPU detected — ROCm ${rocm_ver:-unknown}"
        COMPUTE_PLATFORM="rocm72"
        success "Compute platform: rocm72"
        return
    fi

    # CPU fallback
    warn "No GPU detected; using CPU-only PyTorch."
    COMPUTE_PLATFORM="cpu"
}

# Detect GOOS / GOARCH for the Go build
detect_go_target() {
    info "Detecting Go build target..."

    if [[ "$DETECTED_OS" == "mac" ]]; then
        GO_OS="darwin"
    else
        GO_OS="linux"
    fi

    local arch
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64)   GO_ARCH="amd64" ;;
        aarch64|arm64)  GO_ARCH="arm64" ;;
        *) die "Unsupported architecture for Go build: $arch" ;;
    esac

    success "Go target: ${GO_OS}/${GO_ARCH}"
}

# Clone repo
clone_repo() {
    info "Cloning eulix into $CLONE_DIR..."
    if [[ -d "$CLONE_DIR" ]]; then
        warn "$CLONE_DIR already exists — pulling latest instead of cloning..."
        git -C "$CLONE_DIR" pull --ff-only
    else
        git clone --depth 1 "$REPO_URL" "$CLONE_DIR"
    fi
    success "Repository ready at $CLONE_DIR."
}

# Build binaries
build() {
    info "Building binaries..."
    mkdir -p "$INSTALL_DIR" "$EULIX_DIR"

    # eulix-parser (Rust)
    info "  cargo build --release (eulix-parser)..."
    (
        cd "$CLONE_DIR/eulix-parser"
        RUSTFLAGS="-C target-cpu=native" cargo build --release
        cp target/release/eulix_parser "$INSTALL_DIR/eulix_parser"
    )
    success "  eulix-parser → $INSTALL_DIR/eulix_parser"

    # eulix CLI (Go)
    info "  go build (eulix) for ${GO_OS}/${GO_ARCH}..."
    (
        cd "$CLONE_DIR"
        GOOS="$GO_OS" GOARCH="$GO_ARCH" CGO_ENABLED=1 \
            go build -ldflags="-s -w" -trimpath -o "$INSTALL_DIR/eulix" ./cmd/eulix/main.go
    )
    success "  eulix → $INSTALL_DIR/eulix"

    # eulix_embed (Python package — copy the whole folder)
    info "  copying eulix-embed → $EULIX_DIR/eulix_embed..."
    rm -rf "$EULIX_DIR/eulix_embed"
    cp -r "$CLONE_DIR/eulix-embed" "$EULIX_DIR/eulix_embed"
    success "  eulix_embed → $EULIX_DIR/eulix_embed"
}

# Create venv and install Python deps
install_venv_deps() {
    info "Setting up Python venv at $EULIX_VENV..."

    local existing_ver=""
    if [[ -x "$EULIX_VENV/bin/python" ]]; then
        existing_ver="$("$EULIX_VENV/bin/python" -c 'import sys; print("%d.%d" % sys.version_info[:2])' 2>/dev/null || true)"
    fi

    if [[ "$existing_ver" == "3.10" || "$existing_ver" == "3.11" ]]; then
        success "Compatible virtual environment already exists (Python $existing_ver) — reusing it."
    else
        if [[ -d "$EULIX_VENV" ]]; then
            warn "Existing venv at $EULIX_VENV is missing or incompatible (found: ${existing_ver:-none}) — recreating."
        fi
        uv venv --python 3.10 "$EULIX_VENV" --clear
        success "Virtual environment created."
    fi

    info "Installing Python requirements..."
    uv pip install --python "$EULIX_VENV/bin/python" \
        -r "$EULIX_DIR/eulix_embed/requirements.txt"
    success "requirements.txt installed."

    info "Installing PyTorch for platform: $COMPUTE_PLATFORM..."
    case "$COMPUTE_PLATFORM" in
        cuda126)
            uv pip install --python "$EULIX_VENV/bin/python" \
                torch torchvision \
                --index-url https://download.pytorch.org/whl/cu126
            ;;
        cuda130)
            uv pip install --python "$EULIX_VENV/bin/python" \
                torch torchvision
            ;;
        cuda132)
            uv pip install --python "$EULIX_VENV/bin/python" \
                torch torchvision \
                --index-url https://download.pytorch.org/whl/cu132
            ;;
        rocm72)
            uv pip install --python "$EULIX_VENV/bin/python" \
                torch torchvision \
                --index-url https://download.pytorch.org/whl/rocm7.2
            ;;
        cpu)
            uv pip install --python "$EULIX_VENV/bin/python" \
                torch torchvision \
                --index-url https://download.pytorch.org/whl/cpu
            ;;
        mac)
            uv pip install --python "$EULIX_VENV/bin/python" \
                torch torchvision
            ;;
        *)
            die "Unknown compute platform: $COMPUTE_PLATFORM"
            ;;
    esac
    success "PyTorch installed."
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
    echo -e "║     Eulix Installer v1.0     ║"
    echo -e "╚══════════════════════════════╝${RESET}\n"

    Check_OS
    install_UV
    check_deps
    compute_platform
    detect_go_target
    clone_repo
    build
    install_venv_deps
    ensure_path

    echo -e "\n${GREEN}${BOLD}✓ Eulix installed successfully!${RESET}"
    echo -e "  Binaries : ${BOLD}$INSTALL_DIR${RESET}"
    echo -e "  Data dir : ${BOLD}$EULIX_DIR${RESET}"
    echo -e "  Venv     : ${BOLD}$EULIX_VENV${RESET}"
    echo -e "  Clone    : ${BOLD}$CLONE_DIR${RESET}"
    echo -e "  Run      : ${BOLD}eulix --help${RESET}\n"
}

main "$@"
