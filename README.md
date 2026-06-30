<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="120" />

# Eulix

**Local code intelligence. Ask questions about any codebase, get accurate answers.**

[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg?style=for-the-badge)](LICENSE)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue.svg?style=for-the-badge)](eulix-embed/LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Rust](https://img.shields.io/badge/Rust-orange?style=for-the-badge&logo=rust&logoColor=white)](https://www.rust-lang.org)
[![Python](https://img.shields.io/badge/Python-3670A0?style=for-the-badge&logo=python&logoColor=ffdd54)](https://www.python.org/)

[Overview](#overview) · [Install](#installation) · [Usage](#usage) · [Docs](docs/) · [Known Issues](docs/known-issues.md)

</div>

---

> [!IMPORTANT]
> **🚧 Beta Release**
> Core features are stable. Breaking changes are not expected for the next few releases.
> First stable release scheduled for **late June / early July 2026**.
> Please report issues — documentation contributions especially welcome.

---

Eulix builds a structured knowledge base from your source code — symbols, call graphs, control flow, and semantic embeddings — then uses a multi-layer retrieval pipeline to answer questions about it with sub-second latency, grounded in actual code structure rather than guesses.

**Your code never leaves your machine.** All parsing, indexing, and reasoning run locally. Cloud LLM providers are supported as an opt-in.

---

## Performance

| Component                      | Benchmark                                                    |
| ------------------------------ | ------------------------------------------------------------ |
| Parser (Rust, 12 threads)      | 26M LOC/min · with approximate call graphs                   |
| Embedder (Python, AMD Navi 22) | 1.5GB JSON · 768-dim model · ~35 min                         |
| Retrieval (Go, 2GB codebase)   | **~300ms** end-to-end including re-ranking                   |
| PRISM call graph               | ~6s · ~35-75% approximation accuracy depending upon language |

---

## How It Works

### 1. Index

```bash
eulix analyze
```

Eulix runs three focused pipelines over your source code:

- **Symbol index** — every function, class, variable, and its location
- **PRISM call graphs** — polyglot call graph approximation via inverted symbol map. Fast, documented tradeoffs — see [known issues](docs/known-issues.md)
- **Semantic embeddings** — per-symbol vectors via your choice of local or remote model

### 2. Query

```bash
eulix chat
```

Every query runs through a four-stage retrieval strategy:

1. Exact symbol lookup
2. Keyword search (BM25)
3. Semantic vector search (IVF index, O(1) lookup via mmap)
4. Call graph expansion

Results are re-ranked via MMR and budget-allocated before being passed to the LLM with a structured CoT prompt. Answers surface related code that wasn't retrieved — reducing hallucination risk rather than hiding it.

---

## Architecture

Three binaries, one pipeline:

| Binary         | Language | Role                                                                 | License    |
| -------------- | -------- | -------------------------------------------------------------------- | ---------- |
| `eulix`        | Go       | Orchestrator — CLI, config, retrieval pipeline, LLM integration, TUI | GPLv3      |
| `eulix_parser` | Rust     | Static analyzer — symbols, call graphs, control flow, complexity     | GPLv3      |
| `eulix_embed`  | Python   | Embedder — transformers via PyTorch, CUDA/ROCm, bucket sharding      | Apache 2.0 |

---

## Why Eulix

**Grounded answers, not guesses.** Eulix builds a structured model of your codebase before answering anything. Retrieval is multi-layer and re-ranked, not a nearest-neighbor gamble.

**Local-first, privacy by default.** Parse, embed, and reason entirely on your machine. No code is sent anywhere unless you configure a cloud LLM.

**Any LLM provider.** OpenAI, Anthropic, Gemini, Ollama, LM Studio, or any OpenAI-compatible endpoint. One config line to switch.

**Small models, big results.** Accurate context means a local 7B model answers as well as GPT-4 on code understanding tasks. No API costs, no rate limits.

**Handles real codebases.** Built for multi-million LOC repos, monorepos, and legacy systems across multiple languages.

---

## Features

- **Multi-language parsing** — Python, Go, C, C++, Rust. Structural extraction, not regex over text
- **PRISM call graph approximation** — fast polyglot call graph resolution with documented tradeoffs
- **Multi-layer retrieval** — symbol → BM25 → semantic → call graph, with MMR re-ranking
- **GPU acceleration** — CUDA and ROCm support for embedding generation
- **Any LLM provider** — OpenAI · Anthropic · Gemini · Ollama · LM Studio · OpenAI-compatible
- **Anti-hallucination design** — surfaces retrieval gaps explicitly rather than guessing

### Supported Languages

**Stable:** Python · Go · C · C++ · Rust

**Coming soon:** TypeScript · JavaScript · Java

### Use Cases

- **Onboarding** — understand what any module does without reading every file
- **Debugging unfamiliar code** — trace execution flow and caller/callee chains
- **Refactoring** — understand impact across the codebase before changing anything
- **Security audits** — find every caller of a sensitive function
- **Architecture review** — map how components interact at the call graph level

---

## Roadmap

- [ ] **MCP server** — plug Eulix into any editor or agent via Model Context Protocol
- [ ] **Call graph visualization** — interactive dependency graph from PRISM output
- [ ] **Doc generation** — architecture-aware documentation grounded in call flow
- [ ] **Code navigation** — symbol jump, reference finder, caller/callee explorer
- [ ] **TypeScript / JavaScript / Java** support

---

## Installation

### Requirements

- Go 1.23+
- Rust (stable)
- Python 3.10–3.11
- `uv` (only for venv creation and python version management)

> Install PyTorch for your platform first: https://pytorch.org/

### Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/nurysso/eulix/main/install.sh | bash
```

### Windows

> Requires Visual Studio Build Tools (C++ workload) for the Rust linker.

```powershell
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/nurysso/eulix/main/install.ps1" -OutFile "$env:TEMP\install.ps1"
powershell -ExecutionPolicy Bypass -File "$env:TEMP\install.ps1"
```

→ Full setup guide: [docs/installation.md](docs/installation.md)

---

## Usage

```bash
# 1. Initialize in your project root
cd your-project
eulix init

# 2. Build the knowledge base
eulix analyze

# 3. Ask questions
eulix chat
```

### CLI Reference

#### `eulix` (Go orchestrator)

| Command   | Description                                        |
| --------- | -------------------------------------------------- |
| `init`    | Initialize Eulix in the current directory          |
| `analyze` | Parse and embed the codebase, build knowledge base |
| `chat`    | Start interactive query session                    |
| `config`  | Manage configuration (LLM provider, model, paths)  |
| `history` | Browse past queries interactively                  |
| `cache`   | Manage the query cache                             |
| `embed`   | Run the embedding pipeline directly                |
| `version` | Show versions of all three components              |

#### `eulix_parser` (Rust static analyzer)

```
eulix_parser [OPTIONS]

Options:
  -r, --root       Project root directory
  -o, --output     Output path for knowledge base JSON [default: knowledge_base.json]
  -t, --threads    Parallel threads [default: 4]
  -l, --languages  Languages to parse, comma-separated or "all" [default: all]
  --no-analyze     Parse only, skip analysis phase (faster)
  --euignore       Path to custom .euignore file
  -v, --verbose    Verbose output
  -V, --version    Print version
```

#### `eulix_embed` (Python embedder)

```
eulix_embed <COMMAND> [OPTIONS]

Commands:
  embed    Generate embeddings for a knowledge base (default)
  query    Embed a single query string
  compare  Validate embeddings.bin against vectors.bin

Embed options:
  -k, --kb-path   Path to knowledge base JSON
  -o, --output    Output directory
  -m, --model     HuggingFace model name or local path

Supported models: sentence-transformers/all-MiniLM-L6-v2 · BAAI/bge-small-en-v1.5 · BAAI/bge-base-en-v1.5
```

→ Model selection guide: [docs/models-to-use.md](docs/models-to-use.md)

---

## Documentation

| Doc                                                              | Description                                   |
| ---------------------------------------------------------------- | --------------------------------------------- |
| [Architecture Overview](docs/architecture/01-system-overview.md) | System design and data flow                   |
| [Parser Internals](docs/architecture/07-parser-internals.md)     | How `eulix_parser` works                      |
| [PRISM Algorithm](docs/architecture/prism.md)                    | Call graph approximation design and tradeoffs |
| [Retrieval Pipeline](docs/architecture/context-builder.md)       | Multi-layer retrieval and MMR selection       |
| [Query Classifier](docs/architecture/06-classifier.md)           | Intent recognition and routing                |
| [Cache Architecture](docs/architecture/05-cache-architecture.md) | Redis/SQL caching layer                       |
| [Embedding Pipeline](docs/eulix-embed/architecture.md)           | Embedder internals                            |
| [Parser Benchmarks](docs/eulix-parser/benchmark.md)              | Performance numbers                           |
| [Known Issues](docs/known-issues.md)                             | Current limitations including PRISM accuracy  |
| [Installation Guide](docs/installation.md)                       | Detailed platform setup                       |
| [Model Selection](docs/models-to-use.md)                         | Recommended embedding and LLM models          |

> Documentation contributions are especially welcome — many sections need updating.

---

## Contributing

Open an issue before submitting a pull request for significant changes.

---

## License

- **`eulix` and `eulix_parser`** — [GNU General Public License v3.0](LICENSE)
- **`eulix_embed`** — [Apache License 2.0](eulix-embed/LICENSE)
