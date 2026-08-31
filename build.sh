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
# rustup target list | grep -q "aarch64-apple-darwin (installed)" || print_warn "aarch64-apple-darwin target not installed"

# Check for zig (required for aarch64-apple-darwin)
# if ! command -v zig &> /dev/null; then
#     print_error "zig is not installed. Required for aarch64-apple-darwin builds."
#     exit 1
# fi

# Check for sha256sum (used to hash parser binaries for -X embeddedParserHash)
if ! command -v sha256sum &> /dev/null; then
    print_error "sha256sum is not installed. Required to hash parser binaries."
    exit 1
fi

# Build eulix-parser
print_info "Building eulix-parser..."

cd eulix-parser/

print_info "Building for Linux (x86_64)..."
cargo build --release --target x86_64-unknown-linux-gnu

print_info "Building for Windows (x86_64)..."
cargo build --release --target x86_64-pc-windows-gnu

# print_info "Building for macOS ARM (aarch64)..."
# cargo zigbuild --release --target aarch64-apple-darwin

# Copy binaries
print_info "Copying binaries..."
cp target/x86_64-pc-windows-gnu/release/eulix_parser.exe ../eulix-cli/internal/assets/bins/eulix_parser_windows.exe
cp target/x86_64-unknown-linux-gnu/release/eulix_parser ../eulix-cli/internal/assets/bins/eulix_parser_linux
# cp target/aarch64-apple-darwin/release/eulix_parser ../eulix-cli/internal/assets/bins/eulix_parser_macos_arm

cd ../

# Hash the parser binaries. Each Go build embeds exactly one of these (via
# build tags on embed_linux.go / embed_darwin.go / embed_windows.go), so the
# -X embeddedParserHash ldflag passed to a given Go build below must match
# the parser binary that build actually embeds.
print_info "Hashing parser binaries..."
PARSER_DIR="eulix-cli/internal/assets/bins"
HASH_LINUX=$(sha256sum "${PARSER_DIR}/eulix_parser_linux" | awk '{print $1}')
HASH_WINDOWS=$(sha256sum "${PARSER_DIR}/eulix_parser_windows.exe" | awk '{print $1}')
# HASH_MACOS_ARM=$(sha256sum "${PARSER_DIR}/eulix_parser_macos_arm" | awk '{print $1}')
print_info "eulix_parser_linux:        ${HASH_LINUX}"
print_info "eulix_parser_windows.exe:  ${HASH_WINDOWS}"
# print_info "eulix_parser_macos_arm:    ${HASH_MACOS_ARM}"

# NOTE: eulix_macos_intel is built below from the same darwin parser build
# tag as eulix_macos_arm (there is no separate x86_64-apple-darwin parser
# target in this script). If that ever changes, hash the Intel parser
# binary separately and use it for the eulix_macos_intel builds instead.
# HASH_MACOS_INTEL="${HASH_MACOS_ARM}"

# Create eulix-embed.zip
print_info "Creating eulix-embed.zip..."
zip -r eulix-embed.zip eulix-embed/ -x "*/.venv/*" "*/.mypy_cache/*" "*/.git/*" "*/__pycache__/*" "*.pyc" ".codespell-ignore"

cp eulix-embed.zip eulix-cli/internal/assets/bins/eulix-embed.zip

# Build eulix Go binaries
print_info "Building eulix Go binaries..."
cd eulix-cli

# Check Go version
go_version=$(go version | awk '{print $3}')
print_info "Using Go version: $go_version"

# ONNX backend variants: each OS is built twice, once per requirements file,
# via -X eulix/internal/assets.embed_requirements. Each build also gets
# -X eulix/internal/assets.embeddedParserHash set to the sha256 of whichever
# parser binary that OS's Go build embeds, so Hashes() can verify the
# compiled-in parser at runtime.
VARIANTS=("onnx-amd.txt:amd" "onnx-nvidia.txt:nvidia")

build_variant() {
  local goos="$1" goarch="$2" out="$3" parser_hash="$4" req_file="$5" ldflags_extra="$6"

  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
    -ldflags="-s -w ${ldflags_extra} -X 'eulix/internal/assets.embed_requirements=${req_file}' -X 'eulix/internal/assets.embeddedParserHash=${parser_hash}'" \
    -trimpath -o "$out" ./cmd/eulix/main.go
}

for variant in "${VARIANTS[@]}"; do
  req_file="${variant%%:*}"
  tag="${variant##*:}"

  print_info "Building ONNX '${tag}' variant (embed_requirements=${req_file})..."

  print_info "  Linux (amd64)..."
  build_variant linux amd64 "eulix_linux_${tag}" "$HASH_LINUX" "$req_file" ""

  print_info "  Windows (amd64)..."
  build_variant windows amd64 "eulix_windows_${tag}.exe" "$HASH_WINDOWS" "$req_file" ""

#   print_info "  macOS Intel (amd64)..."
#   build_variant darwin amd64 "eulix_macos_intel_${tag}" "$HASH_MACOS_INTEL" "$req_file" ""

#   print_info "  macOS ARM (aarch64)..."
#   build_variant darwin arm64 "eulix_macos_arm_${tag}" "$HASH_MACOS_ARM" "$req_file" ""
done

print_info "Build complete! All binaries have been generated."
for variant in "${VARIANTS[@]}"; do
  tag="${variant##*:}"
  print_info "ONNX '${tag}' variant:"
  print_info "  Linux:       eulix_linux_${tag}"
  print_info "  Windows:     eulix_windows_${tag}.exe"
#   print_info "  macOS Intel: eulix_macos_intel_${tag}"
#   print_info "  macOS ARM:   eulix_macos_arm_${tag}"
done
print_info "Parser binaries are in internal/assets/bins/"
