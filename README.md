<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="120" />

# Eulix

**Turn your codebase into a searchable knowledge base — ask questions, get grounded answers.**

[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg?style=for-the-badge)](LICENSE)
[![License: Apache 2.0](https://img.shields.io/badge/eulix--embed-Apache%202.0-blue.svg?style=for-the-badge)](eulix-embed/LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Rust](https://img.shields.io/badge/Rust-orange?style=for-the-badge&logo=rust&logoColor=white)](https://www.rust-lang.org)
[![Python](https://img.shields.io/badge/Python-3670A0?style=for-the-badge&logo=python&logoColor=ffdd54)](https://www.python.org/)

[Overview](#overview) · [Install](#installation) · [Quickstart](#quickstart) · [CLI Reference](#cli-reference) · [Architecture](#architecture) · [Docs](docs/)

</div>

---

> **Beta.** Core features are stable and the API is settling — no breaking changes planned for the next few releases. First stable release targeted for late July / early August 2026. [Known issues](docs/known-issues.md) are documented up front; contributions (especially docs) are welcome.

---

## Overview

Eulix builds a structured model of your codebase — symbols, call graphs, control flow, and semantic embeddings — then answers questions about it through a multi-layer retrieval pipeline, in well under a second once warm.

Every answer is grounded in actual code structure, not a nearest-neighbor guess. Retrieval gaps are surfaced explicitly instead of papered over with hallucination.

**Your code never leaves your machine.** Parsing, indexing, and reasoning all run locally by default. Cloud LLMs are supported, but strictly opt-in.

## Why Eulix

- **Grounded, not guessed.** A structured knowledge base — symbols, call graphs, control flow — comes before any retrieval, so answers are backed by real code structure.
- **Local-first.** Nothing leaves your machine unless you explicitly configure a cloud provider.
- **Any LLM.** OpenAI, Anthropic, Gemini, Ollama, LM Studio, or any OpenAI-compatible endpoint — one config line to switch.
- **Small models, real results.** With accurate retrieval as context, a local 7B model can perform exceptionally well for code understanding tasks
- **Built for scale.** Designed for multi-million-LOC repos, monorepos, and legacy systems spanning several languages.

## Performance

| Component                  | Benchmark                                                                    |
| -------------------------- | ---------------------------------------------------------------------------- |
| Parser (Rust, 12 threads)  | **Millions of LOC/min**, parallel parsing with Rayon                         |
| Embedder (Python, PyTorch) | **~35 min** for 1.5GB of parsed JSON, 768-dim model, CUDA/ROCm accelerated   |
| Embedding index (mmap)     | **O(1)** lookup via IVF — `vectors.bin` + `embeddings.bin` loaded at runtime |
| Retrieval (Go, warm)       | **<50ms** end-to-end, no model reload                                        |
| Retrieval (Go, cold)       | **~7.4s** first query — PyTorch init, model load, cache warmup               |

Tested on an AMD Ryzen 5 5600X: 7.4s cold query profile, 95.48% L1 hit rate, IPC 1.26, 16.5B instructions.

> **Cold start is a one-time cost, not your query latency.** Run the embedder as a persistent daemon (`eulix_embed serve`) or fire a dummy warm-up query at startup, and every subsequent query lands in under 50ms.

## How It Works

**1. Index your codebase**

```bash
eulix analyze
```

Three pipelines run over your source:

1. **Symbol index** — every function, class, variable, and its location
2. **Call graphs** — inter-procedural call relationships with cross-file resolution
3. **Semantic embeddings** — per-symbol vectors, written as `embeddings.bin` (tensors) + `vectors.bin` (IVF index for O(1) mmap lookup)

**2. Ask questions**

```bash
eulix chat
```

Every query runs through a four-stage retrieval strategy — exact symbol lookup, BM25 keyword search, semantic vector search, and call graph expansion — then results are re-ranked with MMR, budget-allocated for context, and passed to the LLM with a structured chain-of-thought prompt.

## Architecture

Three binaries, one pipeline:

| Binary         | Language | Role                                                                  | License    |
| -------------- | -------- | --------------------------------------------------------------------- | ---------- |
| `eulix`        | Go       | Orchestrator — CLI, config, retrieval pipeline, LLM integration, TUI  | GPLv3      |
| `eulix_parser` | Rust     | Static analyzer — symbols, call graphs, control flow, complexity      | GPLv3      |
| `eulix_embed`  | Python   | Embedder — transformer models via PyTorch, CUDA/ROCm, bucket sharding | Apache 2.0 |

Deeper dives: [system overview](docs/architecture/01-system-overview.md) · [parser internals](docs/architecture/07-parser-internals.md) · [query pipeline](docs/architecture/03-query-pipeline.md) · [query package](docs/query-package.md) · [cache architecture](docs/architecture/05-cache-architecture.md) · [embedder internals](docs/eulix-embed/architecture.md)

### Supported Languages

**Stable:** Python · Go · C · C++ · Rust · TypeScript
**Not Supported:** JavaScript · Perl · PHP · Java

### Use Cases

- **Onboarding** — understand what a module does without reading every file
- **Debugging** — trace execution flow and caller/callee chains through unfamiliar code
- **Refactoring** — see the blast radius of a change before you make it
- **Security audits** — find every caller of a sensitive function
- **Architecture review** — map how components actually interact at the call graph level

## Installation

**Requirements**

- Go 1.23+
- Rust (stable)
- Python 3.10–3.11
- [`uv`](https://github.com/astral-sh/uv) (for venv creation and Python version management)
- [PyTorch](https://pytorch.org/) installed for your platform, before running the installer

**Linux / macOS**

```bash
curl -fsSL https://raw.githubusercontent.com/nurysso/eulix/main/install.sh | bash
```

**Windows** _(requires Visual Studio Build Tools, C++ workload, for the Rust linker)_

```powershell
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/nurysso/eulix/main/install.ps1" -OutFile "$env:TEMP\install.ps1"
powershell -ExecutionPolicy Bypass -File "$env:TEMP\install.ps1"
```

Full platform-by-platform setup: [docs/installation.md](docs/installation.md)

## Quickstart

```bash
cd your-project
eulix init          # initialize eulix in the current directory
eulix analyze        # parse + embed the codebase, build the knowledge base
eulix chat           # ask questions, interactively
```

## CLI Reference

### `eulix` — Go orchestrator

```
Turn your codebase into a searchable book. Ask questions about your code,
get accurate answers using local/cloud ML and LLMs.
```

| Command    | Description                                                                                              |
| ---------- | -------------------------------------------------------------------------------------------------------- |
| `init`     | Initialize eulix in the current directory                                                                |
| `analyze`  | Analyze codebase and generate the knowledge base                                                         |
| `chat`     | Start the interactive chat interface                                                                     |
| `query`    | Build a context prompt for LLM queries, or answer non-LLM queries directly                               |
| `config`   | Manage eulix configuration (LLM provider, model, paths)                                                  |
| `history`  | Browse past queries interactively                                                                        |
| `cache`    | Manage cache entries                                                                                     |
| `checksum` | Generate a checksum without running a full analyze                                                       |
| `glados`   | Validate the knowledge base and embedding sizes for errors                                               |
| `embed`    | Run the `eulix_embed` pipeline via the Python venv                                                       |
| `version`  | Show versions of `eulix`, `eulix_parser`, and `eulix_embed`                                              |
| `aspirine` | Attempt to repair `embeddings.bin` and the knowledge base — diagnostic/test tooling, not for routine use |

> `aspirine` is a recovery tool for a corrupted knowledge base or embedding file during development and testing. It isn't part of the normal workflow — if you find yourself reaching for it in production, please [open an issue](../../issues).

### `eulix_parser` — Rust static analyzer

```
eulix_parser [OPTIONS] --root <ROOT> --prism <PRISM>
```

| Flag                          | Description                                                         |
| ----------------------------- | ------------------------------------------------------------------- |
| `-r, --root <ROOT>`           | Project root directory                                              |
| `-o, --output <OUTPUT>`       | Output path for the knowledge base [default: `knowledge_base.json`] |
| `-t, --threads <THREADS>`     | Parallel threads [default: 4]                                       |
| `-l, --languages <LANGUAGES>` | Languages to parse, comma-separated or `all` [default: all]         |
| `-p, --prism <1/2>`           | Call graph algorithm version                                        |
| `--no-analyze`                | Parse only, skip the analysis phase (faster)                        |
| `--euignore <PATH>`           | Custom `.euignore` file [default: `<root>/.euignore`]               |
| `-v, --verbose`               | Verbose output                                                      |
| `-V, --version`               | Print version                                                       |

### `eulix_embed` — Python embedder

```
eulix_embed <COMMAND> [OPTIONS]
```

| Command         | Description                                                                     |
| --------------- | ------------------------------------------------------------------------------- |
| `embed`         | Generate embeddings for a knowledge base (default)                              |
| `query`         | Embed a single query string, one-shot                                           |
| `serve`         | Long-lived stdin/stdout embedding server — avoids reloading the model per query |
| `compare`       | Validate `embeddings.bin` against `vectors.bin`                                 |
| `ijson-backend` | Report the active `ijson` C-backend                                             |
| `version`       | Print version information                                                       |

**`embed` options**

| Flag                       | Description                                                           |
| -------------------------- | --------------------------------------------------------------------- |
| `-k, --kb-path <PATH>`     | Path to knowledge base JSON [default: `knowledge_base.json`]          |
| `-o, --output <DIR>`       | Output directory [default: `./embeddings`]                            |
| `-m, --model <NAME>`       | HuggingFace model name                                                |
| `-d, --device <DEVICE>`    | `cuda` / `mps` / `cpu` [default: auto]                                |
| `-b, --batch-size <N>`     | Batch size [default: auto]                                            |
| `--max-chunk <N>`          | Max chunk size in characters [default: 2000]                          |
| `--quantize`               | SQ8 int8 quantization — 4x smaller `embeddings.bin`, ~1% quality loss |
| `-rcom, --remove-comments` | Strip comments/docstrings/license headers before embedding            |
| `--save-json`              | Also write `embeddings.json` (enables graph edge streaming)           |
| `--debug`                  | Print debug info during the pipeline                                  |

**`query` options**

| Flag                 | Description                        |
| -------------------- | ---------------------------------- |
| `-q, --query <TEXT>` | Query text to embed                |
| `-m, --model <NAME>` | HuggingFace model name             |
| `-f, --format <FMT>` | `json` \| `binary` [default: json] |

**`serve` options**

| Flag                    | Description                                          |
| ----------------------- | ---------------------------------------------------- |
| `-m, --model <NAME>`    | HuggingFace model name [default: `all-MiniLM-L6-v2`] |
| `-d, --device <DEVICE>` | `cuda` / `mps` / `cpu` [default: auto]               |
| `-b, --batch-size <N>`  | Batch size for batched requests [default: auto]      |

Supported embedding models: `sentence-transformers/all-MiniLM-L6-v2` · `BAAI/bge-small-en-v1.5` · `BAAI/bge-base-en-v1.5` — see the [model selection guide](docs/models-to-use.md).

## Roadmap

- [ ] MCP server — plug Eulix into any editor or agent via Model Context Protocol
- [ ] Interactive call graph visualization
- [ ] Architecture-aware documentation generation, grounded in call flow
- [ ] Code navigation — symbol jump, reference finder, caller/callee explorer
- [ ] JavaScript and Java support

## Documentation

| Doc                                                              | Description                                   |
| ---------------------------------------------------------------- | --------------------------------------------- |
| [basic introduction and config](docs/About-Eulix.md) | overview of how to configure and general summary of project |
| [Architecture Overview](docs/architecture/01-system-overview.md) | System design and data flow                   |
| [Parser Internals](docs/architecture/07-parser-internals.md)     | How `eulix_parser` works                      |
| [Query Pipeline](docs/architecture/03-query-pipeline.md)         | End-to-end query flow and retrieval           |
| [Query Package](docs/query-package.md)                           | Comprehensive query system documentation      |
| [Context Builder](docs/architecture/08-context-builder.md)       | Multi-layer retrieval and MMR selection       |
| [Classifier](docs/architecture/06-classifier.md)                 | Intent recognition and routing                |
| [Cache Architecture](docs/architecture/05-cache-architecture.md) | Caching layer design                          |
| [Embedding Pipeline](docs/eulix-embed/architecture.md)           | Embedder internals                            |
| [Parser Architecture](docs/eulix-parser/Architecture.md)        | Detailed parser design and implementation      |
| [Known Issues](docs/known-issues.md)                             | Current limitations                           |
| [Installation Guide](docs/installation.md)                       | Detailed platform setup                       |
| [Model Selection](docs/models-to-use.md)                         | Recommended embedding and LLM models          |

## Contributing

Open an issue before submitting a pull request for anything non-trivial — happy to align on approach first. Documentation contributions are especially welcome; several sections are still catching up to the code.

## License

- **`eulix`** and **`eulix_parser`** — [GNU General Public License v3.0](LICENSE)
- **`eulix_embed`** — [Apache License 2.0](eulix-embed/LICENSE)
