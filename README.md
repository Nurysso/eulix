<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="120" />

**Semantic code intelligence for large codebases.**

[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://go.dev)
[![Rust](https://img.shields.io/badge/rust-stable-orange?logo=rust)](https://www.rust-lang.org)

[Overview](#overview) · [Install](#installation) · [Usage](#usage) · [Docs](docs/) · [Benchmarks](#benchmarks)

</div>

---

Eulix is a high-performance code-intelligence system that parses large codebases, extracts rich semantic data, and generates vector embeddings — enabling fast semantic search, call-graph navigation, and LLM-powered codebase reasoning.

It ships as three focused binaries: a **Go CLI** for orchestration, a **Rust parser** for static analysis, and a **Rust embedder** for vector generation.

```
$ eulix analyze
$ eulix chat
> How does authentication flow through this codebase?
```

---

## Overview

| Component      | Language | Role                                                  |
| -------------- | -------- | ----------------------------------------------------- |
| `eulix`        | Go       | CLI — runs analyses, queries, config                  |
| `eulix_parser` | Rust     | Static analyzer — symbols, call graphs, complexity    |
| `eulix_embed`  | Rust     | Embedder — transformer models, CUDA/ROCm acceleration |

### What it extracts

- **Symbol index** — functions, classes, and their source locations
- **Call graphs** — incoming and outgoing call relationships across files
- **Docstrings & summaries** — extracted docstrings and synthesized descriptions
- **Knowledge base** — control-flow structures, try/except blocks, cyclomatic complexity

### Supported languages

Python · Go · C

---

## Installation

> **Prerequisites:** Go 1.22+, Rust stable, a Hugging Face account (for model downloads)

### Build from source

```bash
git clone https://github.com/nurysso/eulix
cd eulix

# Build the CLI
go build -o eulix ./cmd/eulix

# Build the parser and embedder
cargo build --release -p eulix_parser -p eulix_embed
```

For GPU acceleration, enable the appropriate feature flag:

```bash
# CUDA
cargo build --release --features cuda -p eulix_embed

# ROCm
cargo build --release --features rocm -p eulix_embed
```

<!-- See [docs/install.md](docs/install.md) for full setup instructions including model downloads. -->

---

## Usage

### Initialize a project

```bash
cd my-project
eulix init
```

This creates a `.eulix/` config directory in your project root.

### Analyze a codebase

```bash
eulix analyze
```

Runs the parser and embedder pipeline, producing a knowledge base and embeddings stored locally.

### Chat with your codebase

```bash
eulix chat
```

Opens an interactive session backed by semantic search over your knowledge base.

### Query history

```bash
eulix history
```

Browse past queries interactively.

---

## CLI Reference

### `eulix`

```
Commands:
  init        Initialize eulix in current directory
  analyze     Analyze codebase and generate knowledge base
  chat        Start interactive chat interface
  history     View query history interactively
  cache       Manage cache entries
  config      Manage eulix configuration
  glados      Validate knowledge base and embeddings integrity
  aspirine    Fix corrupted embeddings.bin and kb (testing utility)
```

### `eulix_parser`

```
Options:
  -r, --root <ROOT>              Project root directory (required)
  -o, --output <OUTPUT>          Output file for knowledge base
  -t, --threads <THREADS>        Parsing threads (default: 4)
  -l, --languages <LANGUAGES>    Languages to parse: "python,go,c" or "all"
      --no-analyze               Skip analysis phase for faster parsing
      --euignore <PATH>           Custom ignore file path
```

### `eulix_embed`

```
Commands:
  embed    Generate embeddings for a knowledge base
  query    Generate an embedding for a query string

Options:
  -m, --model <NAME>      HuggingFace model name or local path
  -f, --format <FORMAT>   Output format: json | binary
```

---

## Embedding Models

Eulix supports the following transformer models out of the box, downloaded via Hugging Face:

| Model                                     | Dimensions | Notes              |
| ----------------------------------------- | ---------- | ------------------ |
| `sentence-transformers/all-MiniLM-L6-v2`  | 384d       | Fast, lightweight  |
| `BAAI/bge-small-en-v1.5`                  | 384d       | Strong performance |
| `BAAI/bge-base-en-v1.5`                   | 768d       | Higher quality     |
| `sentence-transformers/all-mpnet-base-v2` | 768d       | High quality       |

All models run via ONNX. A dummy backend is available for testing and CI environments without GPU support.

---

## Benchmarks

> Full benchmark tables and methodology are in [docs/eulixparser](docs/eulix-parser/benchmark.md) and [docs/eulixembed](docs/eulix-embed/benchmark.md).

**Parser** — ~9 million lines of code in under 40 seconds on a single thread. Scales linearly with `-t` (threads).

**Embedder** — performance varies by model and hardware. CUDA and ROCm backends provide significant throughput gains over CPU for large codebases.

---

## Limitations

- Context window creation may struggle with certain function name patterns in edge cases

These are tracked and being addressed. See [docs/](docs/) for details.

---

## Documentation

Full documentation lives in [`docs/`](docs/):

- [Installation & Setup](docs/install.md)
- [Configuration](docs/config.md)
- [Parser Benchmarks](docs/eulix-parser/benchmark.md)
- [Embedder Benchmarks](docs/eulix-embed/benchmark.md)

---

## Contributing

Contributions are welcome. Please open an issue before submiting a pull request for significant changes.

---

## [LICENSE](LICENSE).
