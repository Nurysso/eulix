#!/usr/bin/bash

# Exit on error
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Print colored messages
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check required targets (uncomment if you want to verify)
print_info "Checking Rust targets..."
rustup target list | grep -q "x86_64-unknown-linux-gnu (installed)" || print_warn "x86_64-unknown-linux-gnu target not installed"
rustup target list | grep -q "x86_64-pc-windows-gnu (installed)" || print_warn "x86_64-pc-windows-gnu target not installed"
rustup target list | grep -q "aarch64-apple-darwin (installed)" || print_warn "aarch64-apple-darwin target not installed"

# Check for zig (required for aarch64-apple-darwin)
if ! command -v zig &> /dev/null; then
    print_error "zig is not installed. Required for aarch64-apple-darwin builds."
    exit 1
fi

# Build eulix-parser
print_info "Building eulix-parser..."

cd eulix-parser/

print_info "Building for Linux (x86_64)..."
cargo build --release --target x86_64-unknown-linux-gnu

print_info "Building for Windows (x86_64)..."
cargo build --release --target x86_64-pc-windows-gnu

print_info "Building for macOS ARM (aarch64)..."
cargo zigbuild --release --target aarch64-apple-darwin

# Copy binaries
print_info "Copying binaries..."
cp target/x86_64-pc-windows-gnu/release/eulix_parser.exe ../eulix/internal/assets/bins/eulix_parser_windows.exe
cp target/x86_64-unknown-linux-gnu/release/eulix_parser ../eulix/internal/assets/bins/eulix_parser_linux
cp target/aarch64-apple-darwin/release/eulix_parser ../eulix/internal/assets/bins/eulix_parser_macos_arm

cd ../

# Create eulix-embed.zip
print_info "Creating eulix-embed.zip..."
zip -r eulix-embed.zip eulix-embed/ -x "*/.venv/*" "*/.mypy_cache/*" "*/.git/*" "*/__pycache__/*" "*.pyc" ".codespell-ignore"

cp eulix-embed.zip eulix/internal/assets/bins/eulix-embed.zip

# Build eulix Go binaries
print_info "Building eulix Go binaries..."
cd eulix

# Check Go version
go_version=$(go version | awk '{print $3}')
print_info "Using Go version: $go_version"

# Build for Linux
print_info "Building for Linux (amd64)..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o eulix_linux ./cmd/eulix/main.go

# Build for Windows
print_info "Building for Windows (amd64)..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o eulix_windows.exe ./cmd/eulix/main.go

# Build for macOS (Intel)
print_info "Building for macOS Intel (amd64)..."
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o eulix_macos_intel ./cmd/eulix/main.go

# Optional: Build for macOS ARM (M1/M2)
print_info "Building for macOS ARM (aarch64)..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o eulix_macos_arm ./cmd/eulix/main.go

print_info "Build complete! All binaries have been generated."
print_info "Linux: eulix_linux"
print_info "Windows: eulix_windows.exe"
print_info "macOS Intel: eulix_macos_intel"
print_info "macOS ARM: eulix_macos_arm"
print_info "Parser binaries are in internal/assets/bins/"
