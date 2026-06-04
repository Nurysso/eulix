# Eulix Cross-Platform Makefile
# Works on Linux, macOS, and Windows (with make installed)

# Detect OS
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
    VENV_BIN := $(USER_HOME)\.Eulx\venv\Scripts
    PYTHON_VENV := $(VENV_BIN)\python.exe
    LAUNCHER_EXT := .bat
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
    MKDIR := mkdir -p
    CP := cp -f
    RM := rm -f
    RMDIR := rm -rf
    SEP := /
    NULL := /dev/null
    USER_HOME := $(HOME)
    VENV_BIN := $(USER_HOME)/.Eulix/venv/bin
    PYTHON_VENV := $(VENV_BIN)/python
    LAUNCHER_EXT :=
endif

# Directories
PARSER_DIR := eulix-parser
EMBED_DIR := eulix-embed
GO_DIR := .
BUILD_DIR := build

# Binary names
PARSER_BIN := eulix_parser$(EXE_EXT)
CLI_BIN := eulix$(EXE_EXT)
EMBED_LAUNCHER := eulix_embed$(LAUNCHER_EXT)

# Build paths
PARSER_BUILD := $(PARSER_DIR)$(SEP)target$(SEP)release$(SEP)$(PARSER_BIN)
CLI_BUILD := $(BUILD_DIR)$(SEP)$(CLI_BIN)

# Final build directory paths
BUILD_PARSER := $(BUILD_DIR)$(SEP)$(PARSER_BIN)
BUILD_CLI := $(BUILD_DIR)$(SEP)$(CLI_BIN)

# GPU backend selection (default: prompt)
# Override with: make GPU=cuda or make GPU=rocm to skip the prompt
GPU ?= prompt

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
	@echo "GPU Backend: $(GPU)"
	@echo "Install directory: $(INSTALL_DIR)"
	@echo "Build directory: $(BUILD_DIR)"
	@echo ""
	@echo "Targets:"
	@echo "  make build        - Build all binaries and copy to build/"
	@echo "  make install      - Build and install to $(INSTALL_DIR)"
	@echo "  make install-deps - Install build dependencies"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make test         - Run all tests"
	@echo "  make uninstall    - Remove installed binaries"
	@echo ""
	@echo "Individual targets:"
	@echo "  make parser       - Build eulix-parser only"
	@echo "  make embed        - Setup Python environment for eulix-embed"
	@echo "  make cli          - Build eulix CLI only"
	@echo ""
	@echo "GPU Backend Options (for embed):"
	@echo "  make embed            - Prompt interactively for GPU backend"
	@echo "  make embed GPU=cpu    - CPU-only PyTorch (skip prompt)"
	@echo "  make embed GPU=cuda   - NVIDIA CUDA PyTorch (skip prompt)"
	@echo "  make embed GPU=rocm   - AMD ROCm PyTorch (skip prompt)"
	@echo ""
	@echo "Installation:"
	@echo "  make install-parser  - Install parser only"
	@echo "  make install-embed   - Install embedder (requires 'make embed' first)"
	@echo "  make install-cli     - Install CLI only"

# Create build directory
.PHONY: build-dir
build-dir:
ifeq ($(DETECTED_OS),Windows)
	@$(MKDIR) "$(BUILD_DIR)" $(NULL) 2>&1 || echo. >$(NULL)
else
	@$(MKDIR) $(BUILD_DIR)
endif

# Build all and copy to build/
.PHONY: build
build: build-dir parser embed cli
	@$(ECHO) "$(BLUE)Copying binaries to $(BUILD_DIR)...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	$(CP) "$(PARSER_BUILD)" "$(BUILD_PARSER)" >$(NULL) 2>&1
else
	$(CP) $(PARSER_BUILD) $(BUILD_PARSER)
	chmod +x $(BUILD_PARSER)
	chmod +x $(BUILD_CLI)
endif
	@$(ECHO) "$(GREEN)✓ All binaries built and copied to $(BUILD_DIR)$(NC)"
	@echo ""
	@echo "Binaries available in $(BUILD_DIR):"
	@echo "  - $(PARSER_BIN)"
	@echo "  - $(CLI_BIN)"
	@echo "  (eulix_embed is Python-based, run 'make embed' first)"

# Build parser
.PHONY: parser
parser:
	@$(ECHO) "$(BLUE)Building eulix-parser...$(NC)"
	cd $(PARSER_DIR) && cargo build --release
	@$(ECHO) "$(GREEN)✓ Parser built: $(PARSER_BUILD)$(NC)"

# Build Go CLI
.PHONY: cli
cli: build-dir
	@$(ECHO) "$(BLUE)Building eulix CLI...$(NC)"
	go build -o $(CLI_BUILD) ./cmd/eulix/main.go
	@$(ECHO) "$(GREEN)✓ CLI built: $(CLI_BUILD)$(NC)"

# Internal helper: resolve GPU backend (prompt if GPU=prompt)
# Writes chosen backend to .eulix_gpu_choice, then reads it back.
.PHONY: _resolve-gpu
_resolve-gpu:
ifeq ($(GPU),prompt)
ifeq ($(DETECTED_OS),Windows)
	@echo.
	@echo Select PyTorch backend:
	@echo   1) cpu   - CPU only (default)
	@echo   2) cuda  - NVIDIA CUDA
	@echo   3) rocm  - AMD ROCm
	@set /p EULIX_GPU_CHOICE="Enter choice [1/2/3, default=1]: " && \
		if "!EULIX_GPU_CHOICE!"=="2" (echo cuda> .eulix_gpu_choice) else \
		if "!EULIX_GPU_CHOICE!"=="3" (echo rocm> .eulix_gpu_choice) else \
		(echo cpu> .eulix_gpu_choice)
else
	@echo ""
	@echo "Select PyTorch backend:"
	@echo "  1) cpu   - CPU only (default)"
	@echo "  2) cuda  - NVIDIA CUDA"
	@echo "  3) rocm  - AMD ROCm"
	@printf "Enter choice [1/2/3, default=1]: "; \
		read choice; \
		case "$choice" in \
			2) echo cuda > .eulix_gpu_choice ;; \
			3) echo rocm > .eulix_gpu_choice ;; \
			*) echo cpu  > .eulix_gpu_choice ;; \
		esac
endif
else
	@echo "$(GPU)" > .eulix_gpu_choice
endif

# Setup Python environment for eulix-embed
.PHONY: embed
embed: _resolve-gpu
	$(eval EULIX_GPU := $(shell cat .eulix_gpu_choice | tr -d '[:space:]'))
	@rm -f .eulix_gpu_choice
	@$(ECHO) "$(BLUE)Setting up eulix-embed Python environment...$(NC)"
	@$(ECHO) "$(BLUE)Using GPU backend: $(EULIX_GPU)$(NC)"
	@# Create .Eulix directory
ifeq ($(DETECTED_OS),Windows)
	@if not exist "$(USER_HOME)\.Eulix" mkdir "$(USER_HOME)\.Eulix"
else
	@mkdir -p $(USER_HOME)/.Eulix
endif
	@# Check/install uv
	@$(ECHO) "$(BLUE)Checking uv...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	@where uv > $(NULL) 2>&1 || ( \
		echo "$(RED)uv not found. Please install uv from https://github.com/astral-sh/uv $(NC)" && \
		exit 1 \
	)
else
	@command -v uv >/dev/null 2>&1 || ( \
		echo "$(RED)uv not found. Please install uv from https://github.com/astral-sh/uv $(NC)" && \
		exit 1 \
	)
endif
	@# Ensure Python 3.10 is available
	@$(ECHO) "$(BLUE)Ensuring Python 3.10 is available...$(NC)"
	@uv python install 3.10 || true
	@# Create venv if missing
	@if [ ! -d "$(USER_HOME)/.Eulix/venv" ]; then \
		$(ECHO) "$(BLUE)Creating virtual environment...$(NC)"; \
		uv venv $(USER_HOME)/.Eulix/venv --python 3.10; \
	fi
	@# Install requirements using system uv, targeting the venv
	@$(ECHO) "$(BLUE)Installing requirements from $(EMBED_DIR)/requirements.txt...$(NC)"
	uv pip install --python $(PYTHON_VENV) -r $(EMBED_DIR)/requirements.txt
	@# Install PyTorch based on resolved GPU backend
	@$(ECHO) "$(BLUE)Installing PyTorch ($(EULIX_GPU) backend)...$(NC)"
	@if [ "$(EULIX_GPU)" = "cuda" ]; then \
		uv pip install --python $(PYTHON_VENV) torch torchvision --index-url https://download.pytorch.org/whl/cu124; \
	elif [ "$(EULIX_GPU)" = "rocm" ]; then \
		uv pip install --python $(PYTHON_VENV) torch torchvision --index-url https://download.pytorch.org/whl/rocm5.6; \
	else \
		uv pip install --python $(PYTHON_VENV) torch torchvision --index-url https://download.pytorch.org/whl/cpu; \
	fi
	@# Copy embed script to .Eulix
	@$(ECHO) "$(BLUE)Copying eulix_embed.py to .Eulix...$(NC)"
	$(CP) $(EMBED_DIR)/eulix-embed.py $(USER_HOME)/.Eulix/eulix_embed.py
	@$(ECHO) "$(GREEN)✓ eulix-embed environment ready$(NC)"


# Install all components
.PHONY: install
install: parser cli install-embed
	@$(ECHO) "$(BLUE)Installing binaries to $(INSTALL_DIR)...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	$(MKDIR) "$(INSTALL_DIR)" $(NULL) 2>&1 || echo. >$(NULL)
	$(CP) "$(BUILD_PARSER)" "$(INSTALL_DIR)$(SEP)$(PARSER_BIN)" >$(NULL) 2>&1
	$(CP) "$(BUILD_CLI)" "$(INSTALL_DIR)$(SEP)$(CLI_BIN)" >$(NULL) 2>&1
else
	$(MKDIR) $(INSTALL_DIR)
	$(CP) $(BUILD_PARSER) $(INSTALL_DIR)/$(PARSER_BIN)
	$(CP) $(BUILD_CLI) $(INSTALL_DIR)/$(CLI_BIN)
	chmod +x $(INSTALL_DIR)/$(PARSER_BIN)
	chmod +x $(INSTALL_DIR)/$(CLI_BIN)
endif
	@$(ECHO) "$(GREEN)✓ Installation complete!$(NC)"
	@echo ""
	@$(ECHO) "$(YELLOW)Make sure $(INSTALL_DIR) is in your PATH:$(NC)"
ifeq ($(DETECTED_OS),Windows)
	@echo "  setx PATH \"%%PATH%%;$(INSTALL_DIR)\""
else
	@echo "  export PATH=\"$(INSTALL_DIR):\$$PATH\""
	@echo "  (Add to ~/.bashrc or ~/.zshrc to make permanent)"
endif

# Install parser only
.PHONY: install-parser
install-parser: parser
	@$(ECHO) "$(BLUE)Installing eulix-parser...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	$(MKDIR) "$(INSTALL_DIR)" $(NULL) 2>&1 || echo. >$(NULL)
	$(CP) "$(PARSER_BUILD)" "$(INSTALL_DIR)$(SEP)$(PARSER_BIN)"
else
	$(MKDIR) $(INSTALL_DIR)
	$(CP) $(PARSER_BUILD) $(INSTALL_DIR)/$(PARSER_BIN)
	chmod +x $(INSTALL_DIR)/$(PARSER_BIN)
endif
	@$(ECHO) "$(GREEN)✓ Parser installed$(NC)"

# Install embed launcher
.PHONY: install-embed
install-embed: embed
	@$(ECHO) "$(BLUE)Installing eulix-embed launcher...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	@$(MKDIR) "$(INSTALL_DIR)" $(NULL) 2>&1 || echo. >$(NULL)
	@echo "@echo off" > $(INSTALL_DIR)$(SEP)$(EMBED_LAUNCHER)
	@echo "$(PYTHON_VENV) \"$(USER_HOME)\\.Eulix\\eulix_embed.py\" %%*" >> $(INSTALL_DIR)$(SEP)$(EMBED_LAUNCHER)
else
	$(MKDIR) $(INSTALL_DIR)
	@echo "#!/bin/sh" > $(INSTALL_DIR)/$(EMBED_LAUNCHER)
	@echo "$(PYTHON_VENV) $(USER_HOME)/.Eulix/eulix_embed.py \"\$$@\"" >> $(INSTALL_DIR)/$(EMBED_LAUNCHER)
	chmod +x $(INSTALL_DIR)/$(EMBED_LAUNCHER)
endif
	@$(ECHO) "$(GREEN)✓ eulix-embed launcher installed as $(EMBED_LAUNCHER)$(NC)"

# Install CLI only
.PHONY: install-cli
install-cli: cli
	@$(ECHO) "$(BLUE)Installing eulix CLI...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	$(MKDIR) "$(INSTALL_DIR)" $(NULL) 2>&1 || echo. >$(NULL)
	$(CP) "$(BUILD_CLI)" "$(INSTALL_DIR)$(SEP)$(CLI_BIN)"
else
	$(MKDIR) $(INSTALL_DIR)
	$(CP) $(BUILD_CLI) $(INSTALL_DIR)/$(CLI_BIN)
	chmod +x $(INSTALL_DIR)/$(CLI_BIN)
endif
	@$(ECHO) "$(GREEN)✓ CLI installed$(NC)"

# Install dependencies (system)
.PHONY: install-deps
install-deps:
	@$(ECHO) "$(BLUE)Checking dependencies...$(NC)"
	@echo ""
	@echo "Required:"
	@echo "  - Rust (cargo) - for parser"
	@echo "  - Go - for CLI"
	@echo "  - Make - for this build system"
	@echo "  - Python 3.10 or 3.11 - for embed (managed via uv)"
	@echo "  - uv - Python package manager (https://github.com/astral-sh/uv)"
	@echo ""
ifeq ($(DETECTED_OS),Windows)
	@where cargo >$(NULL) 2>&1 && echo "  ✓ Rust installed" || echo "  ✗ Rust not found - install from https://rustup.rs"
	@where go >$(NULL) 2>&1 && echo "  ✓ Go installed" || echo "  ✗ Go not found - install from https://golang.org"
	@where make >$(NULL) 2>&1 && echo "  ✓ Make installed" || echo "  ✗ Make not found - install from https://gnuwin32.sourceforge.net/packages/make.htm"
	@where uv >$(NULL) 2>&1 && echo "  ✓ uv installed" || echo "  ✗ uv not found - install from https://github.com/astral-sh/uv"
else
	@command -v cargo >/dev/null 2>&1 && echo "  ✓ Rust installed" || echo "  ✗ Rust not found - install from https://rustup.rs"
	@command -v go >/dev/null 2>&1 && echo "  ✓ Go installed" || echo "  ✗ Go not found - install from https://golang.org"
	@command -v make >/dev/null 2>&1 && echo "  ✓ Make installed" || echo "  ✗ Make not found"
	@command -v uv >/dev/null 2>&1 && echo "  ✓ uv installed" || echo "  ✗ uv not found - install from https://github.com/astral-sh/uv"
endif
	@echo ""
	@echo "Optional (for GPU acceleration):"
	@echo "  - CUDA Toolkit (NVIDIA) - for GPU=cuda"
	@echo "  - ROCm (AMD) - for GPU=rocm"

# Clean
.PHONY: clean
clean:
	@$(ECHO) "$(BLUE)Cleaning build artifacts...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	cd $(PARSER_DIR) && cargo clean 2>$(NULL) || echo ""
	$(RMDIR) "$(BUILD_DIR)" 2>$(NULL) || echo ""
else
	cd $(PARSER_DIR) && cargo clean
	$(RMDIR) $(BUILD_DIR)
endif
	@$(ECHO) "$(GREEN)✓ Clean complete$(NC)"

# Uninstall binaries and remove Python environment (optional)
.PHONY: uninstall
uninstall:
	@$(ECHO) "$(BLUE)Uninstalling binaries...$(NC)"
ifeq ($(DETECTED_OS),Windows)
	$(RM) "$(INSTALL_DIR)$(SEP)$(PARSER_BIN)" 2>$(NULL) || echo ""
	$(RM) "$(INSTALL_DIR)$(SEP)$(CLI_BIN)" 2>$(NULL) || echo ""
	$(RM) "$(INSTALL_DIR)$(SEP)$(EMBED_LAUNCHER)" 2>$(NULL) || echo ""
else
	$(RM) $(INSTALL_DIR)/$(PARSER_BIN)
	$(RM) $(INSTALL_DIR)/$(CLI_BIN)
	$(RM) $(INSTALL_DIR)/$(EMBED_LAUNCHER)
endif
	@$(ECHO) "$(GREEN)✓ Uninstall complete$(NC)"
	@$(ECHO) "$(BLUE)To remove Python environment, run: rm -rf $(USER_HOME)/.Eulix$(NC)"

# Test (only parser and CLI; embed tests are Python-based)
.PHONY: test
test:
	@$(ECHO) "$(BLUE)Running tests...$(NC)"
	@echo ""
	@echo "Testing parser..."
	cd $(PARSER_DIR) && cargo test
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
	@if exist "$(INSTALL_DIR)$(SEP)$(CLI_BIN)" (echo "  ✓ eulix CLI") else (echo "  ✗ eulix CLI not found")
	@if exist "$(INSTALL_DIR)$(SEP)$(EMBED_LAUNCHER)" (echo "  ✓ eulix_embed launcher") else (echo "  ✗ eulix_embed not found")
else
	@test -f $(INSTALL_DIR)/$(PARSER_BIN) && echo "  ✓ eulix_parser" || echo "  ✗ eulix_parser not found"
	@test -f $(INSTALL_DIR)/$(CLI_BIN) && echo "  ✓ eulix CLI" || echo "  ✗ eulix CLI not found"
	@test -f $(INSTALL_DIR)/$(EMBED_LAUNCHER) && echo "  ✓ eulix_embed launcher" || echo "  ✗ eulix_embed not found"
endif
	@echo ""
	@echo "Checking Python environment..."
	@if [ -f "$(PYTHON_VENV)" ]; then \
		$(ECHO) "  ✓ Virtual environment found at $(USER_HOME)/.Eulix/venv"; \
	else \
		$(ECHO) "  ✗ Virtual environment missing, run 'make embed'"; \
	fi

# Development build (faster parser/cli only)
.PHONY: dev
dev: build-dir
	@$(ECHO) "$(BLUE)Building in development mode...$(NC)"
	cd $(PARSER_DIR) && cargo build
	go build -o $(CLI_BUILD) ./cmd/eulix/main.go
ifeq ($(DETECTED_OS),Windows)
	$(CP) "$(PARSER_DIR)$(SEP)target$(SEP)debug$(SEP)$(PARSER_BIN)" "$(BUILD_PARSER)" >$(NULL) 2>&1
else
	$(CP) $(PARSER_DIR)/target/debug/$(PARSER_BIN) $(BUILD_PARSER)
	chmod +x $(BUILD_PARSER)
	chmod +x $(BUILD_CLI)
endif
	@$(ECHO) "$(GREEN)✓ Development build complete in $(BUILD_DIR)$(NC)"

# Show build information
.PHONY: info
info:
	@echo "Eulix Build Information"
	@echo "======================="
	@echo ""
	@echo "Operating System: $(DETECTED_OS)"
	@echo "GPU Backend (embed): $(GPU)"
	@echo "Build Directory: $(BUILD_DIR)"
	@echo "Install Directory: $(INSTALL_DIR)"
	@echo "Executable Extension: $(EXE_EXT)"
	@echo "Launcher Extension: $(LAUNCHER_EXT)"
	@echo ""
	@echo "Binary Names:"
	@echo "  Parser: $(PARSER_BIN)"
	@echo "  CLI: $(CLI_BIN)"
	@echo "  Embed Launcher: $(EMBED_LAUNCHER)"
	@echo ""
	@echo "Paths:"
	@echo "  Parser build: $(PARSER_BUILD)"
	@echo "  CLI build: $(CLI_BUILD)"
	@echo "  Embed script: $(USER_HOME)/.Eulix/eulix_embed.py"
	@echo "  Embed venv: $(USER_HOME)/.Eulix/venv"
	@echo ""
	@echo "Available GPU Backends for embed:"
	@echo "  - cpu (default, PyTorch CPU)"
	@echo "  - cuda (NVIDIA CUDA)"
	@echo "  - rocm (AMD ROCm)"

# Rebuild everything
.PHONY: rebuild
rebuild: clean build

# Quick install (skip tests)
.PHONY: quick-install
quick-install: build install
