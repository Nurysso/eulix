<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="120" />

**Turn your codebase into a searchable book.**

[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Rust](https://img.shields.io/badge/Rust-orange?style=for-the-badge&logo=rust&logoColor=white)](https://www.rust-lang.org)
[![Python](https://img.shields.io/badge/Python-3670A0?style=for-the-badge&logo=python&logoColor=ffdd54)](https://www.python.org/)

[Overview](#overview) · [Install](#installation) · [Usage](#usage) · [Docs](docs/)

</div>

---

Eulix transforms your codebase into a structured, searchable knowledge base. **Ask questions about your code — get accurate answers** grounded in actual source structure, not hallucinations.

Using **ML algorithms and LLMs**, Eulix analyzes your code's architecture (symbols, call graphs, control flow), then intelligently retrieves relevant context to answer questions with precision. Everything stays on your machine. No code leaves your infrastructure.

---

## How It Works

### 1. Index Your Codebase

Eulix analyzes your source code and creates a structured knowledge base:

- **Symbol Index** — Maps all functions, classes, variables, and their locations
- **Call Graphs** — Tracks which code calls what (dependencies, relationships)
- **Control Flow** — Captures structure, complexity, and error handling
- **Embeddings** — Generates semantic vectors for each code unit (local, no cloud)

Result: Your codebase becomes a "book" with chapters (files), sections (classes), and indexed content (functions).

### 2. Answer Questions Accurately

When you ask a question, Eulix:

1. **Finds relevant code** using multi-layer retrieval (symbol lookup → keyword search → semantic search → call graph traversal)
2. **Builds precise context** — Only includes code that matters, with accurate relationships
3. **Feeds to LLM** — Local model explains based on grounded facts, not guesses

Unlike generic "ask ChatGPT about your code", Eulix's context is structured and accurate. Your 7B local model answers like a 14B cloud model.

### Architecture

Three focused binaries working in concert:

| Component      | Language | Role                                                                                   |
| -------------- | -------- | -------------------------------------------------------------------------------------- |
| `eulix`        | Go       | **Orchestrator** — CLI, config, and the retrieval pipeline                             |
| `eulix_parser` | Rust     | **Static Analyzer** — Extracts symbols, call graphs, and complexity                    |
| `eulix_embed`  | Python   | **Embedder** — Runs transformers via Pytorch with GPU acceleration (ROCm/CUDA support) |

---

## Why Eulix

**Accurate answers grounded in your actual code.** Most AI code tools hallucinate because they guess at context. Eulix builds structured knowledge of your codebase first, so answers are precise.

**Works offline, stays private.** All parsing, embedding, and reasoning happen locally. Zero code exposure. Perfect for proprietary or regulated codebases.

**Fast on small models.** With accurate context, a local 7B model explains code as well as ChatGPT-4. No API costs, no latency, no rate limits.

**Production-ready.** Handles millions of lines of code. Built for large teams, legacy systems, and complex architectures.

---

## Features

- **Multi-Language Parsing** — Python, Go, C, and more. Extract structure, not just text.
- **Precise Call Graphs** — Understand which code calls what, across the entire project.
- **Local Intelligence** — All analysis happens on your machine. No cloud dependency, no privacy concerns.
- **GPU Acceleration** — CUDA/ROCm support for fast embedding generation.
- **MCP Integration** — Plugs into any editor or tool via Model Context Protocol (coming soon).
- **Anti-Hallucination Design** — Retrieval-augmented answering grounded in actual code structure.

### Supported Languages

**Stable:** Python · Go · C

**Coming soon:** Rust · TypeScript · C++

### Use Cases

- **Onboarding new engineers** — Explain "what does this module do?" in seconds
- **Debugging unfamiliar code** — Trace execution flow and dependencies
- **Refactoring legacy systems** — Understand impact of changes before making them
- **Security audits** — Find all callers of sensitive functions
- **Architecture decisions** — Explore how components interact

---

## Installation

> **Prerequisites:** Go 1.22+, Rust stable, a Hugging Face account (for model downloads)

### Build from source

#### Requirements

- Go 1.22+
- Rust (stable)
- Python 3.10+
- `uv`

#### Clone the repository

```bash
git clone https://github.com/nurysso/eulix
cd eulix
```

#### Install everything

```bash
make install
```

#### Optional: build binaries manually

```bash
# Build CLI
go build -o eulix ./cmd/eulix

# Build parser
cd eulix-parser
cargo build --release
cd ..
```

#### Setup Python dependencies

```bash
uv venv 3.10 # or you can also go with 3.11
source .venv/bin/activate

uv pip install -r requirements.txt
```

Install PyTorch for your platform from:

https://pytorch.org/

---

## Usage

### 1\. Initialize a project

```bash
cd fooPro
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

Vector generation via Pytorch. Supports `sentence-transformers/all-MiniLM-L6-v2`, `BAAI/bge-small-en-v1.5`, `BAAI/bge-base-en-v1.5`, and more. Native CUDA/ROCm support for high-throughput embedding.
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

**Embedder:** Optimized for local inference via python and Pytorch, significantly outperforming Rust-based embedding on the same hardware. -->

## Contributing

Contributions are welcome. Please open an issue before submitting a pull request for significant changes.

---

## [LICENSE](LICENSE)
