<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="120" />

**Local-first code intelligence for large codebases.**

[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://go.dev)
[![Rust](https://img.shields.io/badge/rust-stable-orange?logo=rust)](https://www.rust-lang.org)

[Overview](#overview) · [Install](#installation) · [Usage](#usage) · [Docs](docs/) · [Benchmarks](#benchmarks)

</div>

---

Eulix is a **local-first code intelligence system** designed to provide deep reasoning over massive repositories without compromising privacy or speed.

It orchestrates a high-performance pipeline of **Go** and **Rust** to bridge the gap between raw static analysis and LLM-powered insights. By combining a multi-layer retrieval strategy with rigorous anti-hallucination prompting, Eulix ensures that codebase answers are grounded in your actual source code.

---

## Overview

Eulix operates as three focused binaries that work in concert:

| Component      | Language | Role                                                                                               |
| -------------- | -------- | -------------------------------------------------------------------------------------------------- |
| `eulix`        | Go       | **Orchestrator** — Manages the CLI, config, and the RAG pipeline                                   |
| `eulix_parser` | Rust     | **Static Analyzer** — Extracts symbols, call graphs, and complexity                                |
| `eulix_embed`  | Rust     | **Embedder** — Runs transformer models via ONNX with GPU acceleration(supports both rocm and cuda) |

### Smart Multi-Layer Retrieval

Unlike simple RAG tools, Eulix uses a tiered retrieval pipeline to find the most relevant context before hitting an LLM:

1.  **Symbol Lookup:** Precision matching for functions, classes, and variables.
2.  **Keyword Search:** Traditional lexical matching for specific terms.
3.  **Semantic Vector Search:** Deep contextual matching using local embeddings.
4.  **Call-Graph Expansion:** Traverses relationships to pull in relevant upstream/downstream logic.

### Reliable Reasoning

Eulix is built with an **anti-hallucination discipline**. Our prompts are architected to force the model to cite its sources and strictly adhere to the provided context, minimizing "invented" logic or APIs.

---

## Features

- **Symbol Indexing** — Comprehensive mapping of functions, classes, and source locations.
- **Advanced Call Graphs** — Maps incoming and outgoing relationships across the entire project.
- **Knowledge Base** — Captures control-flow structures, error handling blocks, and cyclomatic complexity.
- **Local-First** — All parsing and embedding happens on your machine. No code leaves your infrastructure.
- **High Performance** — Rust-powered backend capable of parsing millions of lines in seconds.

### Supported languages

Python · Go · C

> [!NOTE]
> rust · Typecript · C++ will be supported soon

---

## Installation

> **Prerequisites:** Go 1.22+, Rust stable, a Hugging Face account (for model downloads)

### Build from source

```bash
git clone https://github.com/nurysso/eulix
cd eulix && make install

# Or you can do this if you want to test each bin
# Build the CLI
go build -o eulix ./cmd/eulix

# Build the parser
cd eulix-parser && cargo build --release

# Build embedder
# Go back to root of the project and
cd eulix-embed && cargo build --release --feature rocm
# use cuda instead of rocm if you have nvidia gpu

# try make help for other usefull commands during building or installing
```

---

## Usage

### 1\. Initialize a project

```bash
cd my-project
eulix init
```

### 2\. Analyze the codebase

```bash
eulix analyze
```

This triggers the parser and embedding pipeline, generating a `.eulix` folder which will be used as knowledge base for llm.

### 3\. Chat with your code

```bash
eulix chat
```

Open's an interactive session to query your codebase using the multi-layer retrieval pipeline.

---

## CLI Reference

### `eulix` (Go)

The main entry point for orchestration.

- ` init` : Initialize eulix in current directory
- ` analyze` : Analyze codebase and generate knowledge base
- ` chat` : Start interactive chat interface
- ` cache` : Manage cache entries
- ` config` : Manage eulix configuration
- ` history` : View query history interactively
- ` version` : Displays version of eulix and eulix_parser, eulix_embed
- ` glados` : Checks for errors in knowledge base and embeddings size
- ` aspirine` : tries to fix embedings.bin and kb MEANT TO BE USED IN TEST

### `eulix_parser` (Rust)

Fast static analysis tool.

- `-r, --root` : Project root directory
- `-v, --ver` : parser version
- `-o, --output` : Output file for knowledge base [default: knowledge_base.json]
- `-t, --threads` : Number of threads for parallel parsing [default: 4]
- `-v, --verbose` : Verbose output
- `-l, --languages` : Languages to parse (comma-separated, or "all") [default: all]
- `--no-analyze` : Skip analysis phase (faster, only parse files)
- `--euignore` : Path to custom .euignore file (defaults to <root>/.euignore)
- `  -h, --help` : Print help
- `  -V, --version` : Print version

### `eulix_embed` (Rust)

Vector generation via ONNX. Supports `sentence-transformers/all-MiniLM-L6-v2`, `BAAI/bge-small-en-v1.5`, `BAAI/bge-base-en-v1.5`, and more. Native CUDA/ROCm support for high-throughput embedding.
eulix_embed [COMMAND] [OPTIONS]

COMMANDS:

- `embed` : Generate embeddings for knowledge base (default)
- `query` : Generate embedding for a query string

EMBED OPTIONS:

- `-k, --kb-path` : Path to knowledge base JSON file
- `-o, --output` : Output directory for embeddings
- `-m, --model` : HuggingFace model name or local path

QUERY OPTIONS:

- `-q, --query ` : Query text to embed
- `-m, --model ` : HuggingFace model name or local path
- `-f, --format` : Output format: json (default) or binary

- `-h, --help` : Show this help message
- `-v, --version` : Show version

> Benchmarks will be added in docs soon.

<!-- **Parser:** Handles \~9 million lines of code in under 40 seconds on a single thread. Scales linearly with additional cores.

**Embedder:** Optimized for local inference via ONNX Runtime, significantly outperforming Python-based embedding scripts on the same hardware. -->

## Contributing

Contributions are welcome. Please open an issue before submitting a pull request for significant changes.

---

## [LICENSE](LICENSE)
