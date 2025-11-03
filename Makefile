# Eulix Cross-Platform Makefile
# Works on Linux, macOS, and Windows (with make installed)

# Detect OS
ifeq ($(OS),Windows_NT)
    DETECTED_OS := Windows
    EXE_EXT := .exe
    INSTALL_DIR := $(LOCALAPPDATA)\eulix\bin
    MKDIR := if not exist "$(INSTALL_DIR)" mkdir "$(INSTALL_DIR)"
    CP := copy /Y
    RM := del /F /Q
    RMDIR := rmdir /S /Q
    SEP := \\
    NULL := NUL
else
    UNAME_S := $(shell uname -s)
    ifeq ($(UNAME_S),Linux)
        DETECTED_OS := Linux
    endif
    ifeq ($(UNAME_S),Darwin)
        DETECTED_OS := macOS
    endif
    EXE_EXT :=
    INSTALL_DIR := $(HOME)/.local/bin
    MKDIR := mkdir -p $(INSTALL_DIR)
    CP := cp -f
    RM := rm -f
    RMDIR := rm -rf
    SEP := /
    NULL := /dev/null
endif

# Directories
PARSER_DIR := eulix-parser
EMBED_DIR := eulix-embed
GO_DIR := .

# Binary names
PARSER_BIN := eulix_parser$(EXE_EXT)
EMBED_BIN := eulix_embed$(EXE_EXT)
CLI_BIN := eulix$(EXE_EXT)

# Build paths
PARSER_BUILD := $(PARSER_DIR)$(SEP)target$(SEP)release$(SEP)$(PARSER_BIN)
EMBED_BUILD := $(EMBED_DIR)$(SEP)target$(SEP)release$(SEP)$(EMBED_BIN)
CLI_BUILD := $(CLI_BIN)

# Colors and echo command
ifeq ($(DETECTED_OS),Windows)
    ECHO := echo
    RED :=
    GREEN :=
    YELLOW :=
    BLUE :=
    NC :=
else
    ECHO := echo -e
    RED := \033[0;31m
    GREEN := \033[0;32m
    YELLOW := \033[0;33m
    BLUE := \033[0;34m
    NC := \033[0m
endif

# Default target
.PHONY: all
all: help

# Help target
.PHONY: help
help:
	@echo "Eulix Build System"
	@echo "=================="
	@echo ""
	@echo "Detected OS: $(DETECTED_OS)"
	@echo "Install directory: $(INSTALL_DIR)"
	@echo ""
	@echo "Targets:"
	@echo "  make build        - Build all binaries (parser, embed, CLI)"
	@echo "  make install      - Build and install to $(INSTALL_DIR)"
	@echo "  make install-deps - Install build dependencies"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make test         - Run all tests"
	@echo "  make uninstall    - Remove installed binaries"
	@echo ""
	@echo "Individual targets:"
	@echo "  make parser       - Build eulix-parser only"
	@echo "  make embed        - Build eulix-embed only"
	@echo "  make cli          - Build eulix CLI only"
	@echo ""
	@echo "Installation:"
	@echo "  make install-parser  - Install parser only"
	@echo "  make install-embed   - Install embedder only"
	@echo "  make install-cli     - Install CLI only"

# Build all
.PHONY: build
build: parser embed cli
	@$(ECHO) "$(GREEN)✓ All binaries built successfully$(NC)"

# Build parser
.PHONY: parser
parser:
	@$(ECHO) "$(BLUE)Building eulix-parser...$(NC)"
	cd $(PARSER_DIR) && cargo build --release
	@$(ECHO) "$(GREEN)✓ Parser built: $(PARSER_BUILD)$(NC)"

# Build embedder
.PHONY: embed
embed:
	@$(ECHO) "$(BLUE)Building eulix-embed...$(NC)"
	cd $(EMBED_DIR) && cargo build --release --features candle-cpu
	@$(ECHO) "$(GREEN)✓ Embedder built: $(EMBED_BUILD)$(NC)"

# Build Go CLI
.PHONY: cli
cli:
	@$(ECHO) "$(BLUE)Building eulix CLI...$(NC)"
	go build -o $(CLI_BIN) ./cmd/eulix/main.go
	@$(ECHO) "$(GREEN)✓ CLI built: $(CLI_BUILD)$(NC)"

# Install all
.PHONY: install
install: build install-dirs
	@$(ECHO) "$(BLUE)Installing binaries to $(INSTALL_DIR)...$(NC)"
	$(MKDIR)
ifeq ($(DETECTED_OS),Windows)
	$(CP) "$(PARSER_BUILD)" "$(INSTALL_DIR)$(SEP)$(PARSER_BIN)" >$(NULL) 2>&1
	$(CP) "$(EMBED_BUILD)" "$(INSTALL_DIR)$(SEP)$(EMBED_BIN)" >$(NULL) 2>&1
	$(CP) "$(CLI_BUILD)" "$(INSTALL_DIR)$(SEP)$(CLI_BIN)" >$(NULL) 2>&1
else
	$(CP) $(PARSER_BUILD) $(INSTALL_DIR)/$(PARSER_BIN)
	$(CP) $(EMBED_BUILD) $(INSTALL_DIR)/$(EMBED_BIN)
	$(CP) $(CLI_BUILD) $(INSTALL_DIR)/$(CLI_BIN)
	chmod +x $(INSTALL_DIR)/$(PARSER_BIN)
	chmod +x $(INSTALL_DIR)/$(EMBED_BIN)
	chmod +x $(INSTALL_DIR)/$(CLI_BIN)
endif
	@$(ECHO) "$(GREEN)✓ Installation complete!$(NC)"
	@echo ""
	@echo "Binaries installed to:"
	@echo "  $(INSTALL_DIR)$(SEP)$(PARSER_BIN)"
	@echo "  $(INSTALL_DIR)$(SEP)$(EMBED_BIN)"
	@echo "  $(INSTALL_DIR)$(SEP)$(CLI_BIN)"
	@echo ""
	@$(ECHO) "$(YELLOW)Make sure $(INSTALL_DIR) is in your PATH:$(NC)"
ifeq ($(DETECTED_OS),Windows)
	@echo "  setx PATH \"%%PATH%%;$(INSTALL_DIR)\""
else
	@echo "  export PATH=\"$(INSTALL_DIR):\$PATH\""
	@echo "  (Add to ~/.bashrc or ~/.zshrc to make permanent)"
endif

# Install individual components
.PHONY: install-parser
install-parser: parser install-dirs
	@$(ECHO) "$(BLUE)Installing eulix-parser...$(NC)"
	$(MKDIR)
ifeq ($(DETECTED_OS),Windows)
	$(CP) "$(PARSER_BUILD)" "$(INSTALL_DIR)$(SEP)$(PARSER_BIN)"
else
	$(CP) $(PARSER_BUILD) $(INSTALL_DIR)/$(PARSER_BIN)
	chmod +x $(INSTALL_DIR)/$(PARSER_BIN)
endif
	@$(ECHO) "$(GREEN)✓ Parser installed$(NC)"

.PHONY: install-embed
install-embed: embed install-dirs
	@$(ECHO) "$(BLUE)Installing eulix-embed...$(NC)"
	$(MKDIR)
ifeq ($(DETECTED_OS),Windows)
	$(CP) "$(EMBED_BUILD)" "$(INSTALL_DIR)$(SEP)$(EMBED_BIN)"
else
	$(CP) $(EMBED_BUILD) $(INSTALL_DIR)/$(EMBED_BIN)
	chmod +x $(INSTALL_DIR)/$(EMBED_BIN)
endif
	@$(ECHO) "$(GREEN)✓ Embedder installed$(NC)"

.PHONY: install-cli
install-cli: cli install-dirs
	@$(ECHO) "$(BLUE)Installing eulix CLI...$(NC)"
	$(MKDIR)
ifeq ($(DETECTED_OS),Windows)
	$(CP) "$(CLI_BUILD)" "$(INSTALL_DIR)$(SEP)$(CLI_BIN)"
else
	$(CP) $(CLI_BUILD) $(INSTALL_DIR)/$(CLI_BIN)
	chmod +x $(INSTALL_DIR)/$(CLI_BIN)
endif
	@$(ECHO) "$(GREEN)✓ CLI installed$(NC)"

# Create install directory
.PHONY: install-dirs
install-dirs:
	$(MKDIR)

# Install dependencies
.PHONY: install-deps
install-deps:
	@$(ECHO) "$(BLUE)Checking dependencies...$(NC)"
	@echo ""
	@echo "Required:"
	@echo "  - Rust (cargo) - for parser and embedder"
	@echo "  - Go - for CLI"
	@echo "  - Make - for this build system"
	@echo ""
ifeq ($(DETECTED_OS),Windows)
	@where cargo >$(NULL) 2>&1 && echo "  ✓ Rust installed" || echo "  ✗ Rust not found - install from https://rustup.rs"
	@where go >$(NULL) 2>&1 && echo "  ✓ Go installed" || echo "  ✗ Go not found - install from https://golang.org"
	@where make >$(NULL) 2>&1 && echo "  ✓ Make installed" || echo "  ✗ Make not found - install from https://gnuwin32.sourceforge.net/packages/make.htm"
else
	@command -v cargo >/dev/null 2>&1 && echo "  ✓ Rust installed" || echo "  ✗ Rust not found - install from https://rustup.rs"
	@command -v go >/dev/null 2>&1 && echo "  ✓ Go installed" || echo "  ✗ Go not found - install from https://golang.org"
	@command -v make >/dev/null 2>&1 && echo "  ✓ Make installed" || echo "  ✗ Make not found"
endif
	@echo ""
	@echo "Optional (for GPU acceleration):"
	@echo "  - CUDA Toolkit (NVIDIA)"
	@echo "  - ROCm (AMD)"

# Clean
.PHONY: clean
clean:
	@$(ECHO) "$(BLUE)Cleaning build artifacts...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	cd $(PARSER_DIR) && cargo clean 2>$(NULL) || echo ""
	cd $(EMBED_DIR) && cargo clean 2>$(NULL) || echo ""
	$(RM) $(CLI_BIN) 2>$(NULL) || echo ""
else
	cd $(PARSER_DIR) && cargo clean
	cd $(EMBED_DIR) && cargo clean
	$(RM) $(CLI_BIN)
endif
	@$(ECHO) "$(GREEN)✓ Clean complete$(NC)"

# Uninstall
.PHONY: uninstall
uninstall:
	@$(ECHO) "$(BLUE)Uninstalling binaries...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	$(RM) "$(INSTALL_DIR)$(SEP)$(PARSER_BIN)" 2>$(NULL) || echo ""
	$(RM) "$(INSTALL_DIR)$(SEP)$(EMBED_BIN)" 2>$(NULL) || echo ""
	$(RM) "$(INSTALL_DIR)$(SEP)$(CLI_BIN)" 2>$(NULL) || echo ""
else
	$(RM) $(INSTALL_DIR)/$(PARSER_BIN)
	$(RM) $(INSTALL_DIR)/$(EMBED_BIN)
	$(RM) $(INSTALL_DIR)/$(CLI_BIN)
endif
	@$(ECHO) "$(GREEN)✓ Uninstall complete$(NC)"

# Test
.PHONY: test
test:
	@$(ECHO) "$(BLUE)Running tests...$(NC)"
	@echo ""
	@echo "Testing parser..."
	cd $(PARSER_DIR) && cargo test
	@echo ""
	@echo "Testing embedder..."
	cd $(EMBED_DIR) && cargo test
	@echo ""
	@echo "Testing Go CLI..."
	go test ./...
	@echo ""
	@$(ECHO) "$(GREEN)✓ All tests passed$(NC)"

# Verify installation
.PHONY: verify
verify:
	@$(ECHO) "$(BLUE)Verifying installation...$(NC)"
	@echo ""
ifeq ($(DETECTED_OS),Windows)
	@if exist "$(INSTALL_DIR)$(SEP)$(PARSER_BIN)" (echo "  ✓ eulix_parser") else (echo "  ✗ eulix_parser not found")
	@if exist "$(INSTALL_DIR)$(SEP)$(EMBED_BIN)" (echo "  ✓ eulix_embed") else (echo "  ✗ eulix_embed not found")
	@if exist "$(INSTALL_DIR)$(SEP)$(CLI_BIN)" (echo "  ✓ eulix CLI") else (echo "  ✗ eulix CLI not found")
else
	@test -f $(INSTALL_DIR)/$(PARSER_BIN) && echo "  ✓ eulix_parser" || echo "  ✗ eulix_parser not found"
	@test -f $(INSTALL_DIR)/$(EMBED_BIN) && echo "  ✓ eulix_embed" || echo "  ✗ eulix_embed not found"
	@test -f $(INSTALL_DIR)/$(CLI_BIN) && echo "  ✓ eulix CLI" || echo "  ✗ eulix CLI not found"
endif
	@echo ""
	@echo "Checking PATH..."
ifeq ($(DETECTED_OS),Windows)
	@where $(PARSER_BIN) >$(NULL) 2>&1 && echo "  ✓ eulix_parser in PATH" || echo "  ✗ eulix_parser not in PATH"
	@where $(EMBED_BIN) >$(NULL) 2>&1 && echo "  ✓ eulix_embed in PATH" || echo "  ✗ eulix_embed not in PATH"
	@where $(CLI_BIN) >$(NULL) 2>&1 && echo "  ✓ eulix in PATH" || echo "  ✗ eulix not in PATH"
else
	@command -v $(PARSER_BIN) >/dev/null 2>&1 && echo "  ✓ eulix_parser in PATH" || echo "  ✗ eulix_parser not in PATH"
	@command -v $(EMBED_BIN) >/dev/null 2>&1 && echo "  ✓ eulix_embed in PATH" || echo "  ✗ eulix_embed not in PATH"
	@command -v $(CLI_BIN) >/dev/null 2>&1 && echo "  ✓ eulix in PATH" || echo "  ✗ eulix not in PATH"
endif

# Development builds (faster, with debug symbols)
.PHONY: dev
dev:
	@$(ECHO) "$(BLUE)Building in development mode...$(NC)"
	cd $(PARSER_DIR) && cargo build
	cd $(EMBED_DIR) && cargo build --features candle-cpu
	go build -o $(CLI_BIN) ./cmd/eulix/main.go
	@$(ECHO) "$(GREEN)✓ Development build complete$(NC)"

# Show build information
.PHONY: info
info:
	@echo "Eulix Build Information"
	@echo "======================="
	@echo ""
	@echo "Operating System: $(DETECTED_OS)"
	@echo "Install Directory: $(INSTALL_DIR)"
	@echo "Executable Extension: $(EXE_EXT)"
	@echo ""
	@echo "Binary Names:"
	@echo "  Parser: $(PARSER_BIN)"
	@echo "  Embedder: $(EMBED_BIN)"
	@echo "  CLI: $(CLI_BIN)"
	@echo ""
	@echo "Build Paths:"
	@echo "  Parser: $(PARSER_BUILD)"
	@echo "  Embedder: $(EMBED_BUILD)"
	@echo "  CLI: $(CLI_BUILD)"
	@echo ""
	@echo "Features:"
ifeq ($(DETECTED_OS),Windows)
	@echo "  - Windows compatibility: Yes"
	@echo "  - Path separator: Backslash (\\)"
	@echo "  - Install location: %%LOCALAPPDATA%%\eulix\bin"
else
	@echo "  - Unix compatibility: Yes"
	@echo "  - Path separator: Forward slash (/)"
	@echo "  - Install location: ~/.local/bin"
endif

# Rebuild everything
.PHONY: rebuild
rebuild: clean build
	@$(ECHO) "$(GREEN)✓ Rebuild complete$(NC)"

# Quick install (skip tests)
.PHONY: quick-install
quick-install: build install
	@$(ECHO) "$(GREEN)✓ Quick install complete$(NC)"
