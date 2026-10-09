# ==============================================================================
# Eulix Cross-Platform Root Makefile
# Aligned with build.sh release workflow
# ==============================================================================

# Detect Host OS & Executable Extensions
ifeq ($(OS),Windows_NT)
    DETECTED_OS := Windows
    EXE_EXT := .exe
    INSTALL_DIR := $(LOCALAPPDATA)\eulix\bin
    MKDIR := if not exist
    CP := copy /Y
    RM := del /F /Q
    RMDIR := rmdir /S /Q
    SEP := \\
    NULL := NUL
    USER_HOME := $(USERPROFILE)
else
    UNAME_S := $(shell uname -s)
    ifeq ($(UNAME_S),Linux)
        DETECTED_OS := Linux
    else ifeq ($(UNAME_S),Darwin)
        DETECTED_OS := macOS
    endif
    EXE_EXT :=
    INSTALL_DIR := $(HOME)/.local/bin
    MKDIR := mkdir -p
    CP := cp -f
    RM := rm -f
    RMDIR := rm -rf
    SEP := /
    NULL := /dev/null
    USER_HOME := $(HOME)
endif

# Directories
PARSER_DIR := eulix-parser
EMBED_DIR := eulix-embed
CLI_DIR := eulix-cli
ASSETS_BINS_DIR := $(CLI_DIR)/internal/assets/bins
BUILD_DIR := build
LINT_SCRIPT := lint.sh

# Parser Binaries
PARSER_LINUX := $(ASSETS_BINS_DIR)/eulix_parser_linux
PARSER_WINDOWS := $(ASSETS_BINS_DIR)/eulix_parser_windows.exe

# Target OS / GPU defaults (for single-build targets)
OS_TARGET ?= linux
GPU ?= amd

# Colors and Echo Setup
ifeq ($(DETECTED_OS),Windows)
    ECHO := echo
    BLUE :=
    GREEN :=
    YELLOW :=
    RED :=
    NC :=
else
    ECHO := echo -e
    BLUE := \033[0;34m
    GREEN := \033[0;32m
    YELLOW := \033[0;33m
    RED := \033[0;31m
    NC := \033[0m
endif

.PHONY: all help build-dir parsers embed-zip \
        build-linux-amd build-linux-nvidia build-win-amd build-win-nvidia \
        build-single build-all clean \
        lint lint-rust lint-go lint-python \
        test test-rust test-go test-python check

all: help

help:
	@echo "Eulix Build System"
	@echo "=================="
	@echo "Detected Host OS: $(DETECTED_OS)"
	@echo "Build Directory : $(BUILD_DIR)"
	@echo ""
	@echo "Full Pipeline Targets:"
	@echo "  make build-all        - Build all parser targets, zip embed, and build all Go CLI variants"
	@echo "  make build-single     - Build for single OS and GPU (default: OS_TARGET=linux GPU=amd)"
	@echo ""
	@echo "Specific Variant Targets:"
	@echo "  make build-linux-amd     - Build Linux AMD/CPU CLI binary"
	@echo "  make build-linux-nvidia  - Build Linux NVIDIA CLI binary"
	@echo "  make build-win-amd       - Build Windows AMD/CPU CLI binary"
	@echo "  make build-win-nvidia    - Build Windows NVIDIA CLI binary"
	@echo ""
	@echo "Asset Preparation Targets:"
	@echo "  make parsers          - Build Linux & Windows Rust parser binaries"
	@echo "  make embed-zip        - Package eulix-embed.zip into CLI assets"
	@echo ""
	@echo "Custom Parameters:"
	@echo "  make build-single OS_TARGET=windows GPU=nvidia"
	@echo "  make build-single OS_TARGET=linux GPU=amd"
	@echo ""
	@echo "Clean:"
	@echo "  make clean            - Remove build artifacts and assets"
	@echo ""
	@echo "Lint & Test Targets:"
	@echo "  make lint             - Run all linters (clippy, golangci-lint, lint.sh)"
	@echo "  make lint-rust        - cargo clippy on eulix-parser"
	@echo "  make lint-go          - golangci-lint run on eulix-cli"
	@echo "  make lint-python      - lint.sh on eulix-embed"
	@echo "  make test             - Run all tests"
	@echo "  make test-rust        - cargo test on eulix-parser"
	@echo "  make test-go          - go test -cover ./... on eulix-cli"
	@echo "  make test-python      - pytest on eulix-embed"
	@echo "  make check            - Run lint and test"

# Directory Creation Targets
$(BUILD_DIR) $(ASSETS_BINS_DIR):
ifeq ($(DETECTED_OS),Windows)
	@$(MKDIR) "$@" $(NULL) 2>&1 || echo. >$(NULL)
else
	@$(MKDIR) "$@"
endif

build-dir: $(BUILD_DIR) $(ASSETS_BINS_DIR)

# Build Rust Parsers (Linux + Windows)
parsers: | build-dir
	@$(ECHO) "$(BLUE)Building eulix-parser for Linux (x86_64)...$(NC)"
	cd $(PARSER_DIR) && cargo build --release --target x86_64-unknown-linux-gnu
	@$(ECHO) "$(BLUE)Building eulix-parser for Windows (x86_64)...$(NC)"
	cd $(PARSER_DIR) && RUSTFLAGS="-C link-args=-ladvapi32" cargo build --release --target x86_64-pc-windows-gnu
	@$(ECHO) "$(BLUE)Copying parser binaries to CLI assets...$(NC)"
	$(CP) $(PARSER_DIR)/target/x86_64-unknown-linux-gnu/release/eulix_parser $(PARSER_LINUX)
	$(CP) $(PARSER_DIR)/target/x86_64-pc-windows-gnu/release/eulix_parser.exe $(PARSER_WINDOWS)
	@$(ECHO) "$(GREEN)✓ Rust parsers built successfully$(NC)"

# Zip Eulix Embed Python Package
embed-zip: | build-dir
	@$(ECHO) "$(BLUE)Creating eulix-embed.zip...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	powershell -Command "Compress-Archive -Path '$(EMBED_DIR)\*' -DestinationPath 'eulix-embed.zip' -Force"
else
	zip -r eulix-embed.zip $(EMBED_DIR)/ -x "*/.venv/*" "*/__init__/*" "*/.ruff_cache/*" "*/.pytest_cache/*" "*/.mypy_cache/*" "*/.git/*" "*/__pycache__/*" "*.pyc" ".codespell-ignore"
endif
	$(CP) eulix-embed.zip $(ASSETS_BINS_DIR)/eulix-embed.zip
	@$(ECHO) "$(GREEN)✓ eulix-embed.zip created and copied to CLI assets$(NC)"

# Cross-platform macro for building Go CLI variants
define BUILD_GO_VARIANT_CMD
	@$(ECHO) "$(BLUE)Building Go CLI [OS=$(1) ARCH=$(2) GPU=$(3)]...$(NC)"; \
	REQ_FILE="onnx-$(3).txt"; \
	if [ "$(DETECTED_OS)" = "Windows" ]; then \
		PARSER_HASH=$$(powershell -Command "(Get-FileHash -Path '$(4)' -Algorithm SHA256).Hash.ToLower()"); \
		OUT_FILE="$(BUILD_DIR)/eulix_$(1)_$(3).exe"; \
	else \
		PARSER_HASH=$$(sha256sum $(4) 2>/dev/null | awk '{print $$1}'); \
		OUT_FILE="$(BUILD_DIR)/eulix_$(1)_$(3)$(if $(filter windows,$(1)),.exe,)"; \
	fi; \
	cd $(CLI_DIR) && GOOS=$(1) GOARCH=$(2) CGO_ENABLED=0 go build \
		-ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=$${REQ_FILE}' -X 'eulix/internal/assets.embeddedParserHash=$${PARSER_HASH}'" \
		-trimpath -o ../$${OUT_FILE} ./cmd/eulix/main.go; \
	$(ECHO) "$(GREEN)✓ Built $${OUT_FILE}$(NC)"
endef

# Individual Variant Targets
build-linux-amd: parsers embed-zip
	$(call BUILD_GO_VARIANT_CMD,linux,amd64,amd,$(PARSER_LINUX))

build-linux-nvidia: parsers embed-zip
	$(call BUILD_GO_VARIANT_CMD,linux,amd64,nvidia,$(PARSER_LINUX))

build-win-amd: parsers embed-zip
	$(call BUILD_GO_VARIANT_CMD,windows,amd64,amd,$(PARSER_WINDOWS))

build-win-nvidia: parsers embed-zip
	$(call BUILD_GO_VARIANT_CMD,windows,amd64,nvidia,$(PARSER_WINDOWS))

# Dynamic Single OS/GPU Target
build-single: parsers embed-zip
ifeq ($(OS_TARGET),windows)
	$(call BUILD_GO_VARIANT_CMD,windows,amd64,$(GPU),$(PARSER_WINDOWS))
else
	$(call BUILD_GO_VARIANT_CMD,linux,amd64,$(GPU),$(PARSER_LINUX))
endif

# Build All Variants
build-all: parsers embed-zip
	@$(ECHO) "$(BLUE)Building all cross-platform Go CLI binaries...$(NC)"
	$(call BUILD_GO_VARIANT_CMD,linux,amd64,amd,$(PARSER_LINUX))
	$(call BUILD_GO_VARIANT_CMD,linux,amd64,nvidia,$(PARSER_LINUX))
	$(call BUILD_GO_VARIANT_CMD,windows,amd64,amd,$(PARSER_WINDOWS))
	$(call BUILD_GO_VARIANT_CMD,windows,amd64,nvidia,$(PARSER_WINDOWS))
	@$(ECHO) "$(GREEN)✓ All release binaries built in $(BUILD_DIR)/$(NC)"

# Clean Build Artifacts
clean:
	@$(ECHO) "$(BLUE)Cleaning build artifacts...$(NC)"
	cd $(PARSER_DIR) && cargo clean 2>$(NULL) || true
	$(RM) eulix-embed.zip 2>$(NULL) || true
	$(RMDIR) $(BUILD_DIR) 2>$(NULL) || true
	$(RMDIR) $(ASSETS_BINS_DIR) 2>$(NULL) || true
	@$(ECHO) "$(GREEN)✓ Clean complete$(NC)"

lint: lint-rust lint-go lint-python
	@$(ECHO) "$(GREEN)✓ All linters passed$(NC)"

lint-rust:
	@$(ECHO) "$(BLUE)Linting eulix-parser (cargo clippy)...$(NC)"
	cd $(PARSER_DIR) && cargo clippy --all-targets -- -D warnings

lint-go:
	@$(ECHO) "$(BLUE)Linting eulix-cli (golangci-lint)...$(NC)"
	cd $(CLI_DIR) && golangci-lint run

lint-python:
	@$(ECHO) "$(BLUE)Linting eulix-embed (lint.sh)...$(NC)"
	cd $(EMBED_DIR) && bash ./$(LINT_SCRIPT) .

test: test-rust test-go test-python
	@$(ECHO) "$(GREEN)✓ All tests passed$(NC)"

test-rust:
	@$(ECHO) "$(BLUE)Testing eulix-parser (cargo test)...$(NC)"
	cd $(PARSER_DIR) && cargo test

test-go:
	@$(ECHO) "$(BLUE)Testing eulix-cli (go test -cover)...$(NC)"
	cd $(CLI_DIR) && go test -cover ./...

test-python:
	@$(ECHO) "$(BLUE)Testing eulix-embed (pytest)...$(NC)"
	cd $(EMBED_DIR) && python -m pytest; rc=$$?; [ $$rc -eq 0 ] || [ $$rc -eq 5 ]

# Lint + test everything
check: lint test
