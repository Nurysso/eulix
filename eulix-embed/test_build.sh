#!/bin/bash
set -e

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║              EULIX EMBED - Build Test Script                ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print status
print_status() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $2"
    else
        echo -e "${RED}✗${NC} $2"
        exit 1
    fi
}

# Function to print info
print_info() {
    echo -e "${YELLOW}→${NC} $1"
}

# Check Rust installation
print_info "Checking Rust installation..."
if command -v rustc &> /dev/null; then
    RUST_VERSION=$(rustc --version)
    print_status 0 "Rust installed: $RUST_VERSION"
else
    print_status 1 "Rust not found. Install from https://rustup.rs"
fi

# Check Cargo
print_info "Checking Cargo..."
if command -v cargo &> /dev/null; then
    CARGO_VERSION=$(cargo --version)
    print_status 0 "Cargo installed: $CARGO_VERSION"
else
    print_status 1 "Cargo not found"
fi

echo ""
print_info "Cleaning previous builds..."
cargo clean
print_status $? "Clean completed"

echo ""
print_info "Testing build without features (dummy backend only)..."
cargo build --release --no-default-features 2>&1 | tail -20
if [ $? -eq 0 ]; then
    print_status 0 "Build successful (no features)"
    BINARY_SIZE=$(du -h target/release/eulix_embed | cut -f1)
    echo "  Binary size: $BINARY_SIZE"
else
    print_status 1 "Build failed (no features)"
fi

echo ""
print_info "Testing build with CPU features..."
cargo build --release --features candle-cpu 2>&1 | tail -20
if [ $? -eq 0 ]; then
    print_status 0 "Build successful (candle-cpu)"
    BINARY_SIZE=$(du -h target/release/eulix_embed | cut -f1)
    echo "  Binary size: $BINARY_SIZE"
else
    print_status 1 "Build failed (candle-cpu)"
    echo ""
    echo "This is expected if Candle dependencies have issues."
    echo "You can still use the dummy backend for testing."
fi

echo ""
print_info "Testing CLI help..."
./target/release/eulix_embed --help
if [ $? -eq 0 ]; then
    print_status 0 "CLI working"
else
    print_status 1 "CLI failed"
fi

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                      Build Summary                           ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

if [ -f target/release/eulix_embed ]; then
    echo -e "${GREEN}✓ Binary created successfully${NC}"
    echo "  Location: target/release/eulix_embed"
    echo "  Size: $(du -h target/release/eulix_embed | cut -f1)"
    echo ""
    echo "Test with dummy backend:"
    echo "  ./target/release/eulix_embed --input kb.json --backend dummy --precompute-embeddings"
else
    echo -e "${RED}✗ Binary not found${NC}"
fi

echo ""
