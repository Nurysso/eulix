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

# Absolute root so targets work no matter where make was started (sub-dir
# Makefiles forward here with `make -C ..` and pass CALLER_DIR).
ROOT_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
CALLER_DIR ?= $(CURDIR)
SCRIPTS_DIR := $(ROOT_DIR)/scripts
KI_DIR := $(SCRIPTS_DIR)/known_issues
PARSER_BIN ?= $(ROOT_DIR)/eulix-parser/target/release/eulix_parser
PYTHON ?= $(if $(wildcard $(HOME)/.Eulix/.venv/bin/python),$(HOME)/.Eulix/.venv/bin/python,python3)
# Scripts run from the directory you typed `make` in, so relative ARGS/paths resolve there.
RUN_IN_CALLER = cd "$(CALLER_DIR)" &&

# Directories
LIB_DIR := libeulix
PARSER_DIR := eulix-parser
LSP_DIR := eulix-lsp
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

.PHONY: build-windows-native \
        all help build-dir parsers parser-linux parser-windows embed-zip \
        build-linux-amd build-linux-nvidia build-win-amd build-win-nvidia \
        build-single build-all clean \
        lint lint-rust lint-go lint-python \
        test test-rust test-go test-python check \
        scripts-help script parser-release \
        repro-prism check-tsx perf-rust-workspace check-prism-v2 repro-all \
        eval-mcp kb-diff kb-field-usage kb-id-name-check vec-stats

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
	@echo "  make build-windows-native - Native Windows build via build.ps1 (GPU=amd|nvidia)"
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
	@echo "  make test-rust        - cargo test on libeulix, eulix-parser and eulix-lsp"
	@echo "  make test-go          - go test -cover ./... on eulix-cli"
	@echo "  make test-python      - pytest on eulix-embed"
	@echo "  make check            - Run lint and test"
	@echo ""
	@echo "Scripts: make scripts-help (works from any sub-directory)"

# Directory Creation Targets
$(BUILD_DIR) $(ASSETS_BINS_DIR):
ifeq ($(DETECTED_OS),Windows)
	@$(MKDIR) "$@" $(NULL) 2>&1 || echo. >$(NULL)
else
	@$(MKDIR) "$@"
endif

build-dir: $(BUILD_DIR) $(ASSETS_BINS_DIR)

# Build Rust Parsers (Linux + Windows)
parsers: parser-linux parser-windows
	@$(ECHO) "$(GREEN)✓ Rust parsers built successfully$(NC)"

parser-linux: | build-dir
	@$(ECHO) "$(BLUE)Building eulix-parser for Linux (x86_64)...$(NC)"
	cd $(PARSER_DIR) && cargo build --release --target x86_64-unknown-linux-gnu
	$(CP) $(PARSER_DIR)/target/x86_64-unknown-linux-gnu/release/eulix_parser $(PARSER_LINUX)

parser-windows: | build-dir
	@$(ECHO) "$(BLUE)Building eulix-parser for Windows (x86_64)...$(NC)"
	cd $(PARSER_DIR) && RUSTFLAGS="-C link-args=-ladvapi32" cargo build --release --target x86_64-pc-windows-gnu
	$(CP) $(PARSER_DIR)/target/x86_64-pc-windows-gnu/release/eulix_parser.exe $(PARSER_WINDOWS)

# Native Windows build (MSVC toolchain) via build.ps1. Usage: make build-windows-native [GPU=nvidia]
build-windows-native:
	powershell -NoProfile -ExecutionPolicy Bypass -File build.ps1 -Gpu $(GPU)

# Zip Eulix Embed Python Package
embed-zip: | build-dir
	@$(ECHO) "$(BLUE)Creating eulix-embed.zip...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	powershell -Command "Compress-Archive -Path '$(EMBED_DIR)\*' -DestinationPath 'eulix-embed.zip' -Force"
else
	zip -r eulix-embed.zip $(EMBED_DIR)/ -x "*/.venv/*" "*/tests/*" "*__init__*" "*/.ruff_cache/*" "*/.pytest_cache/*" "*/.mypy_cache/*" "*/.git/*" "*/__pycache__/*" "*.pyc" ".codespell-ignore"
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
		-ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=$${REQ_FILE}' -X 'eulix/internal/assets.ParserHash=$${PARSER_HASH}'" \
		-trimpath -o ../$${OUT_FILE} ./cmd/eulix/main.go || exit $$?; \
	$(ECHO) "$(GREEN)✓ Built $${OUT_FILE}$(NC)"
endef

# Individual Variant Targets
build-linux-amd: parser-linux embed-zip
	$(call BUILD_GO_VARIANT_CMD,linux,amd64,amd,$(PARSER_LINUX))

build-linux-nvidia: parser-linux embed-zip
	$(call BUILD_GO_VARIANT_CMD,linux,amd64,nvidia,$(PARSER_LINUX))

build-win-amd: parser-windows embed-zip
	$(call BUILD_GO_VARIANT_CMD,windows,amd64,amd,$(PARSER_WINDOWS))

build-win-nvidia: parser-windows embed-zip
	$(call BUILD_GO_VARIANT_CMD,windows,amd64,nvidia,$(PARSER_WINDOWS))

# Dynamic Single OS/GPU Target
ifeq ($(OS_TARGET),windows)
build-single: parser-windows embed-zip
	$(call BUILD_GO_VARIANT_CMD,windows,amd64,$(GPU),$(PARSER_WINDOWS))
else
build-single: parser-linux embed-zip
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
	@$(ECHO) "$(BLUE)Testing libeulix (cargo test)...$(NC)"
	cd $(LIB_DIR) && cargo test
	@$(ECHO) "$(BLUE)Testing eulix-parser (cargo test)...$(NC)"
	cd $(PARSER_DIR) && cargo test
	@$(ECHO) "$(BLUE)Testing eulix-lsp (cargo test)...$(NC)"
	cd $(LSP_DIR) && cargo test

test-go:
	@$(ECHO) "$(BLUE)Testing eulix-cli (go test -cover)...$(NC)"
	cd $(CLI_DIR) && go test -cover ./...

test-python:
	@$(ECHO) "$(BLUE)Testing eulix-embed (pytest)...$(NC)"
	cd $(EMBED_DIR) && python -m pytest; rc=$$?; [ $$rc -eq 0 ] || [ $$rc -eq 5 ]

# Lint + test everything
check: lint test

# ==============================================================================
# Scripts (scripts/). Usable from the root or any sub-dir Makefile.
# Parser-based repros use PARSER_BIN (default: eulix-parser release build).
# Pass extra arguments with ARGS="...".
# ==============================================================================
scripts-help:
	@echo "Script targets (run from anywhere in the repo):"
	@echo "  make repro-prism          PRISM-2..6 repro        [PARSER_BIN=..]"
	@echo "  make check-tsx            PARSE-2 (.tsx/.jsx rejected)"
	@echo "  make perf-rust-workspace  PRISM-8 slowdown        [ARGS='40 80']"
	@echo "  make check-prism-v2       #152 review findings"
	@echo "  make repro-all            all parser repros above"
	@echo "  make eval-mcp             retrieval eval          [ARGS=out.json]"
	@echo "  make kb-diff              ARGS='a.json b.json [--depth N]'"
	@echo "  make kb-field-usage       ARGS='--rust .. --go-dir ..'"
	@echo "  make kb-id-name-check     ARGS=path/to/kb.json (default .eulix/kb.json)"
	@echo "  make vec-stats            ARGS=path/to/embeddings.bin"
	@echo "  make script SCRIPT=name.py ARGS='...'   run any file in scripts/"

parser-release:
	cd $(PARSER_DIR) && cargo build --release

repro-prism: parser-release
	$(RUN_IN_CALLER) bash $(KI_DIR)/repro_prism.sh "$(PARSER_BIN)" $(ARGS)

check-tsx:
	$(RUN_IN_CALLER) bash $(KI_DIR)/check_tsx.sh $(ARGS)

perf-rust-workspace: parser-release
	$(RUN_IN_CALLER) bash $(KI_DIR)/perf_rust_workspace.sh "$(PARSER_BIN)" $(ARGS)

check-prism-v2: parser-release
	$(RUN_IN_CALLER) bash $(SCRIPTS_DIR)/check_prism_v2_methods.sh "$(PARSER_BIN)"

repro-all: repro-prism check-tsx check-prism-v2 perf-rust-workspace

eval-mcp:
	$(RUN_IN_CALLER) $(PYTHON) $(SCRIPTS_DIR)/eval_mcp.py $(ARGS)

kb-diff:
	$(RUN_IN_CALLER) uv run $(SCRIPTS_DIR)/kb_diff.py $(ARGS)

kb-field-usage:
	$(RUN_IN_CALLER) uv run $(SCRIPTS_DIR)/kb_field_usage.py $(ARGS)

kb-id-name-check:
	$(RUN_IN_CALLER) $(PYTHON) $(SCRIPTS_DIR)/kb_id_name_check.py  $(ARGS)

vec-stats:
	$(RUN_IN_CALLER) $(PYTHON) $(SCRIPTS_DIR)/vec_stats.py $(ARGS)

# Generic runner: make script SCRIPT=known_issues/cross_language_edges.py ARGS=kb_call_graph.json
script:
	@test -n "$(SCRIPT)" || { echo "usage: make script SCRIPT=<file in scripts/> ARGS='...'"; exit 2; }
	$(RUN_IN_CALLER) case "$(SCRIPT)" in \
		*.sh) bash "$(SCRIPTS_DIR)/$(SCRIPT)" $(ARGS) ;; \
		*.py) $(PYTHON) "$(SCRIPTS_DIR)/$(SCRIPT)" $(ARGS) ;; \
		*.go) go run "$(SCRIPTS_DIR)/$(SCRIPT)" $(ARGS) ;; \
		*) echo "unknown script type: $(SCRIPT)"; exit 2 ;; esac
