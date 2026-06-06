<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="120" />

**Turn your codebase into a searchable book.**

[![License: GPLv3](https://img.shields.io/badge/license-Appache-blue.svg?style=for-the-badge)](eulix-embed/LICENSE)
[![License: Apachev2](https://img.shields.io/badge/license-GPLv3-blue.svg?style=for-the-badge)](LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Rust](https://img.shields.io/badge/Rust-orange?style=for-the-badge&logo=rust&logoColor=white)](https://www.rust-lang.org)
[![Python](https://img.shields.io/badge/Python-3670A0?style=for-the-badge&logo=python&logoColor=ffdd54)](https://www.python.org/)

[Overview](#overview) · [Install](#installation) · [Usage](#usage) · [Docs](docs/)

</div>

---

> [!IMPORTANT]
> **🚧 Beta Release Notice**
> This is a **beta version** – features are stable but subject to improvement.
> We expect **no breaking changes** for the next few releases.
> The **first stable release** is scheduled for **late June / early July 2026**.
> Please report issues on GitHub – contributions to documentation are especially welcome.

---

Eulix transforms your codebase into a structured, searchable knowledge base. **Ask questions about your code — get accurate answers** grounded in actual source structure, not hallucinations.

Using **ML algorithms and LLMs**, Eulix analyzes your code's architecture (symbols, call graphs, control flow), then intelligently retrieves relevant context to answer questions with precision.

---

## How It Works

### 1. Index Your Codebase

Eulix analyzes your source code and creates a structured knowledge base:

- **Symbol Index** — Maps all functions, classes, variables, and their locations
- **Call Graphs** — Tracks which code calls what (dependencies, relationships)
  _Note: uses **PRISM** (Polyglot Resolution via inverted Symbol Map) – an approximation algorithm. Call graphs are precise enough for most queries but may have false positives._
- **Control Flow** — Captures structure, complexity, and error handling
- **Embeddings** — Generates semantic vectors for each code unit

Result: Your codebase becomes a "book" with chapters (files), sections (classes), and indexed content (functions).

### 2. Answer Questions Accurately

When you ask a question, Eulix:

1. **Finds relevant code** using multi-layer retrieval (symbol lookup → keyword search → semantic search → call graph traversal)
2. **Builds precise context** — Only includes code that matters, with accurate relationships
3. **Feeds to LLM** — Local model explains based on grounded facts, not guesses

### Architecture

Three focused binaries working in concert:

| Component      | Language | Role                                                                                   | License    |
| -------------- | -------- | -------------------------------------------------------------------------------------- | ---------- |
| `eulix`        | Go       | **Orchestrator** — CLI, config, and the retrieval pipeline                             | GPLv3      |
| `eulix_parser` | Rust     | **Static Analyzer** — Extracts symbols, call graphs, and complexity                    | GPLv3      |
| `eulix_embed`  | Python   | **Embedder** — Runs transformers via PyTorch with GPU acceleration (ROCm/CUDA support) | Apache 2.0 |

---

## Why Eulix

**Accurate answers grounded in your actual code.** Most AI code tools hallucinate because they guess at context. Eulix builds structured knowledge of your codebase first, so answers are precise.

**Works offline, keeps your code private.** All parsing, embedding, and reasoning happen locally. No code exposure.

**Fast on small models.** With accurate context, a local 7B model explains code as well as ChatGPT-4. No API costs, no latency, no rate limits.

**Production‑ready.** Handles millions of lines of code. Built for large teams, legacy systems, and complex architectures.

---

## Features

- **Multi-Language Parsing** — Python, Go, C, C++, Rust. Extract structure, not just text.
- **PRISM Call Graph Approximation** — Fast, polyglot call graph resolution (with documented limitations – see [docs](docs/known-issues.md)).
- **Local Intelligence** – All analysis runs on your machine. No cloud dependency(Cloud llm can be used and code will be sent).
- **GPU Acceleration** — CUDA/ROCm support for fast embedding generation.
- **MCP Integration** — Plugs into any editor or tool via Model Context Protocol (coming soon).
- **Anti-Hallucination Design** — Retrieval-augmented answering grounded in actual code structure.

### Supported Languages

**Stable:** Python · Go · C · C++ · Rust

**Coming soon:** TypeScript · JavaScript · Java

### Use Cases

- **Onboarding new engineers** — Explain "what does this module do?" in seconds
- **Debugging unfamiliar code** — Trace execution flow and dependencies
- **Refactoring legacy systems** — Understand impact of changes before making them
- **Security audits** — Find all callers of sensitive functions
- **Architecture decisions** — Explore how components interact

---

## Installation

#### Requirements

- Go 1.23+
- Rust (stable)
- Python 3.10-3.11
- `uv`
  > Install PyTorch for your platform from: https://pytorch.org/

#### Linux/Mac

```bash
curl -fsSL https://raw.githubusercontent.com/nurysso/eulix/main/install.sh | bash
```

#### Windows

> USERS WILL NEED VISUAL STUDIO CODE FOR C++ LINKER USED BY RUST

```ps1
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/nurysso/eulix/main/install.ps1" -OutFile "$env:TEMP\install.ps1"
```

## or look at [install doc](docs/installation.md)

## Usage

### 1. Initialize a project

```bash
cd fooProj
eulix init
```

### 2. Analyze the codebase

```bash
eulix analyze
```

This triggers the parser and embedding pipeline, generating a `.eulix` folder which becomes the knowledge base for the LLM.

### 3. Chat with your code

```bash
eulix chat
```

Opens an interactive session to query your codebase using the multi-layer retrieval pipeline.

---

## CLI Reference

### `eulix` (Go)

- `init` : Initialize eulix in current directory
- `analyze` : Analyze codebase and generate knowledge base
- `chat` : Start interactive chat interface
- `cache` : Manage cache entries
- `config` : Manage eulix configuration
- `history` : View query history interactively
- `embed`: Run the eulix_embed pipeline (Python venv)
- `version` : Displays version of eulix, eulix_parser, and eulix_embed
- `checksum`: Creates checksum without running analyze
- `aspirine` : Attempts to fix `embeddings.bin` and KB (meant for testing)
- `glados` : Checks for errors in knowledge base and embeddings size (testing)

### `eulix_parser` (Rust)

Fast static analysis tool.

- `-V, --version` : Parser version
- `-r, --root` : Project root directory
- `-o, --output` : Output file for knowledge base [default: knowledge_base.json]
- `-t, --threads` : Number of threads for parallel parsing [default: 4]
- `-v, --verbose` : Verbose output
- `-l, --languages` : Languages to parse (comma-separated, or "all") [default: all]
- `--no-analyze` : Skip analysis phase (faster, only parse files)
- `--euignore` : Path to custom .euignore file (defaults to <root>/.euignore)
- `-h, --help` : Print help
- `-V, --version` : Print version

### `eulix_embed` (Python)

Vector generation via PyTorch. Supports `sentence-transformers/all-MiniLM-L6-v2`, `BAAI/bge-small-en-v1.5`, `BAAI/bge-base-en-v1.5`, and more. Native CUDA/ROCm support.

```bash
eulix_embed [COMMAND] [OPTIONS]
```

**COMMANDS:**

- `embed` : Generate embeddings for knowledge base (default)
- `query` : Generate embedding for a query string
- `compare` : Compare embeddings.bin with vectors.bin

**EMBED OPTIONS:**

- `-k, --kb-path` : Path to knowledge base JSON file
- `-o, --output` : Output directory for embeddings
- `-m, --model` : HuggingFace model name or local path

**QUERY OPTIONS:**

- `-q, --query` : Query text to embed
- `-m, --model` : HuggingFace model name or local path
- `-f, --format` : Output format: json (default) or binary

- `-h, --help` : Show help
- `-v, --version` : Show version

---

## Documentation

Full documentation is available in the [`docs/`](docs/) directory:

- **[Architecture Overview](docs/architecture/01-system-overview.md)** – System design and data flow
- **[Parser Internals](docs/architecture/07-parser-internals.md)** – How `eulix_parser` works
- **[Context Builder](docs/architecture/context-builder.md)** – Retrieval and MMR selection
- **[Classifier](docs/architecture/06-classifier.md)** – Query intent recognition
- **[Cache Architecture](docs/architecture/05-cache-architecture.md)** – Redis/SQL caching
- **[Eulix Embed](docs/eulix-embed/architecture.md)** – Embedding pipeline
- **[Parser Benchmarks](docs/eulix-parser/benchmark.md)** – Performance numbers
- **[Known Issues](docs/known-issues.md)** – Current limitations (including PRISM call graph approximation)
- **[Installation Guide](docs/installation.md)** – Detailed setup
- **[Models to Use](docs/models-to-use.md)** – Recommended embedding/LLM models

> **Contributions to docs are highly welcome** – many sections are old and needs time to be updated.

---

## Contributing

Contributions are welcome! Please open an issue before submitting a pull request for significant changes.

---

## License

- **`eulix` (Go CLI) and `eulix_parser` (Rust)** – [GNU General Public License v3.0](LICENSE)
- **`eulix_embed` (Python embedding module)** – [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0)

---

**Happy code understanding with Eulix!**
