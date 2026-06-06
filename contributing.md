# Contributing to Eulix

First off, thank you for considering contributing to Eulix! 🎉

Eulix is a complex project spanning Go, Rust, and Python. Your help – whether fixing bugs, improving docs, or adding features – is genuinely appreciated.

## 📋 Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [How Can I Contribute?](#how-can-i-contribute)
  - [Reporting Bugs](#reporting-bugs)
  - [Suggesting Features](#suggesting-features)
  - [Improving Documentation](#improving-documentation)
  - [Code Contributions](#code-contributions)
- [Development Setup](#development-setup)
- [Project Structure](#project-structure)
- [Coding Guidelines](#coding-guidelines)
- [Pull Request Process](#pull-request-process)
- [Community](#community)

---

## [Code of Conduct](CODE_OF_CONDUCT.MD)

---

## Getting Started

### Before You Start

Eulix is in **beta** (stable release: late June/July 2026). Things may change, but we aim to keep breaking changes minimal.

### Quick Links

- [README](README.md) – Overview and installation
- [Documentation](docs/) – Architecture guides
- [Known Issues](docs/known-issues.md) – Current limitations
- [Issue Tracker](https://github.com/nurysso/eulix/issues)

---

## How Can I Contribute?

### Reporting Bugs

**Before submitting a bug report:**

- Check the [known issues](docs/known-issues.md) – your bug might already be documented
- Search existing issues – someone may have reported it already

**When reporting a bug, include:**

- **Environment:** OS, Go/Rust/Python versions, GPU (if relevant)
- **Steps to reproduce:** Minimal, concrete steps
- **Expected vs actual behavior**
- **Logs/stack traces** – include `eulix chat` with `DEBUG=1` if possible
- **Codebase size** – number of files, LOC (helps with performance issues)

**Example:**

```markdown
**Bug:** `eulix analyze` crashes on project with 50k+ files

**Environment:**

- OS: Ubuntu 22.04
- Go: 1.22.5
- Rust: 1.79.0
- Files: 51,234
- LOC: ~3.2M

**Steps:**

1. `cd large-project`
2. `eulix init`
3. `eulix analyze`

**Result:** Panic after ~30 seconds (trace attached)
```

### Suggesting Features

We welcome feature ideas! Please include:

- **Problem statement** – what limitation are you hitting?
- **Proposed solution** – how would it work?
- **Alternatives considered** – if any
- **Priority** – nice-to-have vs blocking

**Current focus areas (help wanted):**

- Incremental parsing (file watching): TOO BIG AND DIFICULT MAYBE IN V2
- TypeScript/JavaScript support
- Bugs in context retrival
- Better symbol disambiguation (path-based scoring)
- Data flow analysis (taint tracking)
- More embedding models (CodeBERT, StarCoder embeddings)

### Improving Documentation

Documentation is **especially welcome** – many sections are still maturing.

**Areas needing help:**

- Architecture diagrams (Mermaid/ASCII)
- Tutorials (real-world examples)
- Performance benchmarks (different languages/sizes)
- Troubleshooting guides

**Docs structure:**

```
docs/
├── architecture/     # System design (outdated, but gives a general idea)
├── eulix-embed/      # Embedder docs (outdated[based on old rust-onnx code] – needs refresh)
├── eulix-parser/     # Parser docs (good shape, add more benchmarks and explain prism )
├── installation.md   # Setup guide
├── known-issues.md   # Limitations (things i noticed and was not worth adding in issue)
└── models-to-use.md  # Model recommendations
```

### Code Contributions

**Good first issues (look for `good-first-issue` label):**

- Add tests code
- Add nil checks for `debugLog` in `context_builder.go`
- Add path-based scoring in symbol search
- Improve regex patterns in `classifier.go`

**Medium complexity:**

- Rust language parser improvements
- TypeScript language parser improvements
- Java language parser
- JavaScript language parser
- Better error messages in CLI

**Advanced:**

- Incremental parsing support
- Data flow analysis
- Distributed parsing
- Custom embedding model fine-tuning

---

## Development Setup

### Prerequisites

- Go 1.22+
- Rust (stable)
- Python 3.10+ with `uv`
- Hugging Face account (for model downloads)
- (Optional) NVIDIA/AMD GPU for fast embeddings

### Running Tests

> DONT HAVE ALOT OF TESTS

```bash
# Go tests
go test ./...

# Rust tests
cd eulix-parser && cargo test

# Python tests
pytest tests/
```

### Debug Mode

```bash
# Enable debug logging
# vim and add DebugConfig = true in Project Config
eulix chat

# Check context builder logs
tail -f .eulix/context_debug.log
```

---

## Project Structure

```
eulix/
├── cmd/
│   └── eulix/           # CLI entrypoint (Go)
├── internal/
│   ├── cache/           # Redis/SQL cache
│   ├── checksum/        # Checksum
│   ├── cli/             # Command implementations
│   ├── config/          # Config manager
│   ├── embeddings/      # Embedder client
│   ├── llm/             # LLM client abstraction
│   ├── query/           # Classifier, router, context builder
│   ├── Tui/             # TUI and text formating in response
│   └── types/           # Shared types
├── eulix-parser/        # Rust static analyzer
│   ├── src/
│   │    └── kb/         # output json file structrure (needs changes)
│   │    └── parser/     # Tree-sitter grammars and PRISM code in analyze.rs
│   │    └── utils/      # File walking with .euignore
│   └── Cargo.toml
├── eulix-embed/         # Python embedder
│   ├── eulix_embed.py
│   └── requirements.txt
├── docs/                # Documentation
├── Makefile
└── README.md
```

---

## Coding Guidelines

### Go

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Use `gofmt` and `go vet`
- Add comments for exported functions/types
- Prefer explicit error handling over panics (except initialization)

### Rust

- Follow [Rust API Guidelines](https://rust-lang.github.io/api-guidelines/)
- Use `cargo fmt` and `cargo clippy`
- Document public APIs with `///` comments

### Python

- Follow [PEP 8](https://peps.python.org/pep-0008/)
- Use type hints (Python 3.10+)
- Keep embedder module lean – heavy lifting in Go/Rust

### Commit Messages

```
Just be clear on what the comit is about
```

**Types:** `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`

**Example:**

```
fix(classifier): add word boundary to 'loc' in metrics pattern

Fixes issue where 'location' queries were misclassified as metrics.

Closes #42
```

---

## Pull Request Process

1. **Open an issue first** – Discuss significant changes before coding
2. **Fork the repo** and create a branch
3. **Write tests** for bug fixes or new features
4. **Run tests locally** – `make test` (if available) or component-specific tests
5. **Update documentation** – especially for user-facing changes
6. **Submit PR** – link to related issue
7. **Request review** – maintainer will review within 3-5 days

### PR Checklist

- [ ] Code follows style guidelines
- [ ] Tests pass locally
- [ ] Documentation updated
- [ ] No new compiler warnings
- [ ] PR focused on single change (no unrelated fixes)

---

## Community

- **Maintainer:** Dawood (Nurysso) – [nurysso@proton.me](mailto:nurysso@proton.me)
- **Issues:** [GitHub Issues](https://github.com/nurysso/eulix/issues)
- **Discussions:** [GitHub Discussions](https://github.com/nurysso/eulix/discussions) (coming soon)

---

**Thank you for contributing to Eulix!** 🚀
