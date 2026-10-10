<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="112" />

# Eulix

### Local-first code intelligence for large codebases.

**Navigate. Trace. Understand — at repository scale.**

Eulix builds a local, queryable index of your repository — symbols, call graphs, and optional semantic embeddings — so developers can find the _path through the code_, not just a file. Every query is routed to the cheapest mechanism that can answer it, and an LLM is used only where reasoning is actually required.

[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg?style=for-the-badge)](LICENSE)
[![eulix-embed](https://img.shields.io/badge/eulix--embed-Apache%202.0-blue.svg?style=for-the-badge)](eulix-embed/LICENSE)
[![Parser](https://img.shields.io/badge/parser-v0.7.7-8A2BE2?style=for-the-badge)](eulix-parser/README.md)
[![CLI](https://img.shields.io/badge/CLI-v0.8.2-8A2BE2?style=for-the-badge)](eulix-cli/README.md)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Rust](https://img.shields.io/badge/Rust-orange?style=for-the-badge&logo=rust&logoColor=white)](https://www.rust-lang.org/)
[![Python](https://img.shields.io/badge/Python-3670A0?style=for-the-badge&logo=python&logoColor=ffdd54)](https://www.python.org/)
<a href="https://deepwiki.com/Nurysso/eulix"><img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki" height="28"></a>

[Overview](#overview) · [Key Features](#key-features) · [Quick Start](#prerequisites--quick-start) · [Usage](#usage-examples) · [Configuration](#configuration) · [How It Works](#how-it-works) · [Benchmarks](#benchmarks) · [Development](#development--contributing) · [License](#license--acknowledgments)

</div>

---

## Overview

Large codebases are hard for a simple reason:

> **You usually don't know where the answer is.**

Grep finds strings. An LSP resolves a symbol you already know. An LLM answers confidently about code it has never seen. None of them cover the space **between** — callers, dependencies, types, subsystems, implementation paths, and the surrounding context that gives a symbol its meaning.

Eulix builds a local representation of a repository (structured symbols, relationships, call graphs, indexes, project metadata, and optional embeddings) and routes each query to the cheapest mechanism that can answer it:

```text
simple fact ───────────────► direct repository data
symbol / navigation ───────► exact + lexical retrieval
semantic question ─────────► vector retrieval
relationship question ─────► call-graph / structural expansion
reasoning question ────────► retrieved context + LLM
```

**Eulix does not treat code understanding as an LLM problem. It treats it as an indexing and retrieval problem — and uses an LLM only where reasoning is actually required.**

### At a glance

|                |                                                                                   |
| -------------- | --------------------------------------------------------------------------------- |
| **Scale**      | Linux kernel — **37.3M lines**, 64,460 source files parsed in **48 s**            |
| **Throughput** | **~770K lines/s** parser throughput on 12 threads                                 |
| **Graph**      | **774K nodes / 1.57M edges** extracted from the kernel, with **0 parse failures** |
| **Embeddings** | **838,755 chunks** embedded locally on a single consumer GPU                      |
| **Privacy**    | Parsing, indexing, retrieval and embedding run **entirely on-device**             |
| **Languages**  | C, C++, Go, Python, Rust, TypeScript                                              |

<table>
  <tr>
    <td>
      <img src="https://github.com/user-attachments/assets/be07896c-5726-401f-865c-2638e3583e6d" alt="Eulix terminal demo" width="100%" />
    </td>
  </tr>
  <tr>
    <td>
      <img src="https://github.com/user-attachments/assets/5a4a36d8-2804-4402-a75f-d4edb4d0a80e" alt="Eulix retrieval query" width="100%" />
    </td>
  </tr>
</table>

---

## Key Features

- **Local-first.** Parsing, indexing, retrieval, and embedding all run on your machine. Cloud LLMs are optional and must be explicitly configured.
- **Structure-aware.** Extracts functions, methods, classes, symbols, imports and dependencies, call and reverse-call relationships, project structure and metadata, entry points, patterns, and metrics.
- **Query routing.** A deterministic classifier sends each query to the cheapest retrieval path that can answer it. Simple questions stay fast and free.
- **Deterministic where possible.** Questions like _Where is foo?_, _Who calls foo?_, and _What are the project metrics?_ are answered directly from generated repository data — no model invoked.
- **Hybrid retrieval.** Exact symbol lookup, lexical search (BM25 / keywords), semantic search (IVF vectors), subsystem signals, graph expansion, and MMR relevance/diversity re-ranking in one configurable pipeline.
- **Repository scale.** Parses the Linux kernel (37.3M lines) in under a minute with zero parse failures.
- **Streaming embeddings.** PyTorch and ONNX Runtime backends, CPU and GPU execution, INT8 / SQ8 quantization, configurable chunking, and a long-lived embedding service mode.
- **Memory-mapped vector store.** A custom on-disk vector format built for integrity verification and memory-mapped access, so the corpus never has to be fully resident in RAM.
- **Optional LLM.** Use a local provider such as Ollama (nothing leaves your machine) or opt in to a remote provider.
- **Fully tunable.** Every retrieval stage is configurable through `eulix.toml`.
- **Languages.** C, C++, Go, Python, Rust, and TypeScript supported; JavaScript and Java experimental.

---

## Prerequisites & Quick Start

### Prerequisites

| Requirement           | Details                                                                                                                                      |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| **Operating system**  | Linux and Windows have installer scripts. macOS builds are not provided; build from source (see [Development](#development--contributing)).  |
| **Linux**             | `bash` and `curl` to run the installer.                                                                                                      |
| **Windows**           | PowerShell. May require **Visual Studio Build Tools with the C++ workload**.                                                                 |
| **Hardware**          | Embeddings run on CPU or GPU. The reference benchmark used an AMD Radeon RX 6700 XT (ROCm / HIP).                                            |
| **Memory and disk**   | Scale with repository size. Reference point: the Linux kernel (37.3M lines) peaks at ~8.8 GiB RSS and produces a ~2.2 GB `.eulix` directory. |
| **LLM (optional)**    | A local provider such as [Ollama](https://ollama.com) (the sample config uses `llama3.2:3b`), or a remote provider you explicitly configure. |
| **Target repository** | C, C++, Go, Python, Rust, or TypeScript (JavaScript and Java are experimental).                                                              |

### 1. Install

**Linux**

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh)
```

**Windows**

```powershell
irm https://raw.githubusercontent.com/Nurysso/eulix/main/install.ps1 | iex
```

> [!NOTE]
> **macOS** builds are not provided right now. You can try a manual build by running `build.sh` or following the [build docs](docs/installation.md#macos).

### 2. Analyze a repository

```bash
cd your-project

eulix init
eulix analyze
```

### 3. Query it

```bash
eulix chat
```

---

## Usage Examples

### Typical workflow

```bash
cd your-project

eulix init        # initialize Eulix in the repository (creates eulix.toml)
eulix analyze     # parse, analyze, embed, and generate repository data
eulix chat        # ask questions interactively
```

### What you can ask

Eulix classifies each query and picks the cheapest route that can answer it:

| Query                           | Route                          |
| ------------------------------- | ------------------------------ |
| `Where is foo?`                 | Location / exact lookup        |
| `Who calls foo?`                | Call graph                     |
| `What are the project metrics?` | Project metadata               |
| `How does this subsystem work?` | Retrieval + structural context |
| `Why does this happen?`         | Retrieval + LLM reasoning      |

Type any of these at the `eulix chat` prompt, or run a single query to test retrieval:

```bash
eulix query "Who calls foo?"
```

### Anchored vs. open-ended questions

Symbols, function names, and file paths act as **anchors** that drive exact lookup and graph expansion. Open-ended questions rely more heavily on semantic and subsystem signals.

**Anchored, multi-hop** (Django):

```text
When Expression.resolve_expression() processes an OuterRef, which specific Set attribute on the inner
Query object is mutated to track parent table aliases? How does SQLCompiler.get_from_clause()
use this set to ensure those outer tables are omitted from the subquery's FROM SQL block?
```

**Open-ended** (Django):

```text
How are table aliases handled when generating SQL for correlated subqueries?
```

**Cross-cutting** (OpenStack Nova):

```text
How does Nova schedule instances with PCI passthrough requirements,
and which filters participate in the claiming process?
```

> **Tip:** adding a symbol or file path to your query narrows the search and improves coverage. As with any retrieval system, completeness on deep multi-hop chains is not guaranteed.

### Other commands

```bash
eulix vizEulize     # Opens web application to view call graphs
eulix history       # browse previous queries
eulix version       # show component versions
eulix verifyBins    # verify bundled binary hashes
eulix checksum      # update repository checksums
eulix parser        # access the parser wrapper
eulix embed         # run the embedding pipeline on its own
```

| Command      | Purpose                                                              |
| ------------ | -------------------------------------------------------------------- |
| `init`       | Initialize Eulix in the current repository                           |
| `analyze`    | Parse, analyze, embed, and generate repository data                  |
| `parser`     | Access the parser wrapper                                            |
| `embed`      | Run the embedding pipeline                                           |
| `chat`       | Start the interactive interface                                      |
| `vizEulize`  | Opens web application to view call graphs                            |
| `query`      | Build retrieval context or answer direct queries (retrieval testing) |
| `history`    | Browse previous queries                                              |
| `checksum`   | Update repository checksums                                          |
| `verifyBins` | Verify bundled binary hashes                                         |
| `version`    | Show component versions                                              |

---

## Configuration

Eulix is configured through **`eulix.toml`** in the repository root. A minimal configuration:

```toml
[project]
  path = "/home/nurysso/eulix"       # This is absolute path of repo in file System so copying eulix.toml in a different project or moving root will cause issue

[parser]
  threads = 4                   # Number of worker threads for eulix_parser
  prismVersion = 2              # PRISM call graph mode: 1 (fast/direct) or 2 (precise/scope-aware)
  verbose = true                # Enable verbose logging during parsing

[embeddings]
  model = "BAAI/bge-base-en-v1.5" # HuggingFace embedding model name
  dimension = 768               # Embedding vector dimension
  engine = "onnx"               # Inference engine: "onnx" or "torch"

[llm]
  local = true                  # Set to false when using cloud providers
  provider = "ollama"           # "ollama", "openai", "anthropic", "gemini", "deepseek", etc.
  model = "llama3.2:3b"         # Target LLM model name
  max_tokens = 8192             # Maximum tokens for context + generation

[retrievalConfig]
  apply_cross_root_isolation = true  # Demote candidates outside detected primary module/subsystem
  code_to_ast_ratio = 0.85           # Fraction of token budget reserved for real code vs AST metadata
  cross_root_penalty = 0.32          # Score multiplier for candidates outside the primary root
  enable_subsystem_boosting = true   # Boost chunks belonging to query-matched directory paths
  mmr_diversity_factor = 0.65        # MMR lambda: 1.0 = pure relevance, 0.0 = maximum diversity
  max_graph_expansion_depth = 1      # Call graph hops: 0 = disabled, 1 = direct, 2+ = transitive
  max_graph_expansions = 15          # Max call-graph relationship edges to expand
  max_exact_anchors = 2              # Max exact symbol matches to pin into context
  top_k_candidates = 150             # Base candidate limit gathered across search strategies
```

The full option list is in the [configuration reference](eulix-cli/README.md#configuration-reference-eulixtoml).

### Generated data

`eulix analyze` writes its output to a `.eulix` directory in the repository. For the Linux kernel this is approximately **2.2 GB**:

```text
kb.json              ~353 MB
kb_index.json        ~391 MB
kb_call_graph.json   ~411 MB
kb_summary.json      ~2.9 MB
embeddings.bin       ~618 MB
vectors.bin          ~50 MB
other metadata       ~20 MB
```

---

## How It Works

Eulix is a pipeline of specialized components, each in the language best suited to its job.

```text
                              CODEBASE
                                  │
                                  ▼
                    ┌─────────────────────────┐
                    │     eulix_parser        │
                    │ Rust + Tree-sitter      │
                    └────────────┬────────────┘
                                 │
               ┌─────────────────┼─────────────────┐
               ▼                 ▼                 ▼
          symbols & AST      relationships      indexes
               │                 │                 │
               └─────────────────┼─────────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │      eulix_embed        │
                    │   PyTorch / ONNX        │
                    └────────────┬────────────┘
                                 │
                                 ▼
                    ┌─────────────────────────┐
                    │    local retrieval      │
                    │ exact + lexical +       │
                    │ semantic + graph        │
                    └────────────┬────────────┘
                                 │
                                 ▼
                    ┌─────────────────────────┐
                    │       eulix CLI         │
                    │ routing + context + LLM │
                    └─────────────────────────┘
```

### `eulix_parser` — Rust

A repository analysis and indexing engine built on Tree-sitter. It performs source discovery and parsing; function, method, class, and symbol extraction; relationship and call-graph analysis (including reverse call graphs); index generation; project metrics and entry-point detection; dependency and pattern analysis; and language-specific metadata extraction.

Relationship resolution is powered by **PRISM**, Eulix's approximate resolution engine, designed for large-scale code navigation and retrieval rather than formal whole-program verification. See [Parser Architecture](docs/eulix-parser/Architecture.md); a formal technical write-up is in preparation.

### `eulix_embed` — Python

A streaming embedding pipeline with PyTorch and ONNX Runtime backends, CPU and GPU execution, configurable chunking, INT8 / SQ8 quantization, a long-lived service mode, and a custom on-disk vector format built for integrity verification and memory-mapped access.

### `eulix` — Go

The main CLI and orchestration layer: repository initialization and pipeline orchestration, query classification and routing, retrieval and context construction, LLM integration, and configuration, history, validation, and caching.

#### ✨ Latest

### vizEulize: HTML/JS

The call graph visualizer: a single-file browser app that loads .eulix/kb_call_graph.json and .eulix/kb_index.json through a local server started by `eulix vizEulize` command, and shows them as an interactive, searchable graph with focus, filters, tags and categories. You can also load files manually.

### Retrieval pipeline

For retrieval-heavy queries, Eulix fuses several signals into a single ranked context:

```text
                           query
                             │
                             ▼
                  ┌─────────────────────┐
                  │ explicit anchors +  │
                  │      path gates     │
                  └──────────┬──────────┘
                             │
           ┌─────────────────┼─────────────────┐
           ▼                 ▼                 ▼
        exact            lexical           semantic
      / symbols        BM25 / keywords      IVF vectors
           │                 │                 │
           └─────────────────┼─────────────────┘
                             ▼
                  merge + deduplicate
                             │
                             ▼
                    subsystem signals
                             │
                             ▼
                     scope / test-file
                         demotion
                             │
                             ▼
                    MMR + exact-first
                         ranking
                             │
                             ▼
                       selected context
```

> [!NOTE]
> Embedding model choice affects semantic recall, particularly for cryptic identifiers and domain-specific vocabulary. The model is configurable, and newer code-oriented and long-context models are being evaluated at Linux-kernel scale. A stronger model improves ranking quality — retrieval remains a ranking system, not a proof system.

### Local-first by design

```text
        your source
             │
             ▼
          parser
             │
       ┌─────┴─────┐
       ▼           ▼
     graph      embeddings
       │           │
       └─────┬─────┘
             ▼
       local query engine
             │
             ▼
        optional LLM
```

The entire analysis and retrieval stack runs on your machine. The LLM stage is optional, and cloud providers are opt-in. With a local provider (for example Ollama), nothing leaves your environment. **With a remote provider, the retrieved code context for each query is sent to that provider** — choose accordingly for proprietary repositories.

---

## Benchmarks

> Measurements come from local engineering runs. Results vary with hardware, repository state, parser configuration, embedding model, and query shape.

### Parser — Linux kernel

Full parser run on a Linux-kernel-scale checkout, 12 threads, PRISM v2:

### Parser — Linux kernel

Full parser run on a Linux-kernel-scale checkout, 12 threads, PRISM v2:

| Metric                        |         Result |
| ----------------------------- | -------------: |
| Files scanned                 |     **94,063** |
| Source files found            |     **64,460** |
| Failed / skipped              |      **0 / 0** |
| Lines of code                 | **37,322,700** |
| Functions                     |    **647,365** |
| Classes                       |    **211,297** |
| Methods                       |      **6,246** |
| Graph nodes                   |    **773,072** |
| Graph edges                   |  **1,510,341** |
| Parse phase                   |    **52.23 s** |
| Call graphs by Prismv2        |     **1.34 s** |
| Summary / metrics phase       |     **0.34 s** |
| **Parser total**              |    **57.20 s** |
| Parse Phase RSS               |  **1963.2 MB** |
| Analysis Phase RSS            |      **803MB** |
| Summary / metrics Phase RSS   |        **3MB** |
| Peak RSS / Writting Phase RSS |   **~3.3 GiB** |

> [!NOTE]
> Parsing quality: 32,338 clean / 32,122 partial / 0 failed. The partials are overwhelmingly C/C++ and expected without a preprocessor pass.

_Serialization now uses a spill-to-disk architecture with improved slim repository views rather than the parser's full internal representation. This cuts peak RSS from ~8 GB to ~3.5 GB and reduces on-disk output to ~1.2 GB (down from ~2.2 GB in the previous slim format)._

### Embeddings — Linux kernel

> Ran on AMD Radeon RX 6700 XT (ROCm/HIP)

| Setting      | Value                              |
| ------------ | ---------------------------------- |
| Model        | `BAAI/bge-base-en-v1.5`            |
| Dimension    | 768                                |
| Quantization | SQ8 / INT8                         |
| Device       | AMD Radeon RX 6700 XT (ROCm / HIP) |
| Chunks       | 837,531                            |

| Metric                     |                                      Result |
| -------------------------- | ------------------------------------------: |
| KB scan + chunk generation |                                 **20.27 s** |
| Embedding generation       |                             **26 min 27 s** |
| Embedding throughput       |                          **527.6 chunks/s** |
| Embedding pipeline total   |                             **26 min 57 s** |
| Full parse + embedding     | **~26 min** (parse ~1 min; embedding-bound) |
| `embeddings.bin`           |                                  **617 MB** |
| `vectors.bin`              |                                   **50 MB** |

> Chunk breakdown: 121,227 class · 64,460 file · 645,670 function · 6,174 method.

Parsing accounts for under a minute of the end-to-end run; embedding is the throughput-bound stage at this scale and scales with accelerator capability. These figures were measured on a mid-range consumer GPU, and hardware-specific results are benchmarked separately rather than treating one GPU as a universal baseline.

---

## Scope and Accuracy

Eulix is a code navigation and retrieval layer. It is not a compiler, and PRISM is not a formal whole-program analysis system — it deliberately trades formal soundness for the speed and scalability required at repository scale.

Relationship resolution is best-effort in areas that are inherently difficult to resolve statically: dynamic dispatch, reflection and indirect calls, generated code, language-specific metaprogramming, highly dynamic languages, and parser coverage gaps in less-common syntax.

Retrieval quality also depends on query specificity, repository structure and indexing configuration, the embedding model, context limits, and language semantics.

Known constraints are tracked in [Known Issues](docs/known-issues.md).

### Supported languages

This version standardizes the terminology, uses a footnote to explain the caveat for C/C++, and keeps the table tidy.

| Language   | Status      | Notes                      |
| ---------- | ----------- | -------------------------- |
| C          | Supported\* | Limited by macro expansion |
| C++        | Supported\* | Limited by macro expansion |
| Go         | Supported   | —                          |
| Python     | Supported   | —                          |
| Rust       | Supported   | —                          |
| TypeScript | Supported   | —                          |
| JavaScript | Supported   | —                          |
| Java       | Supported   | —                          |

> \*_ Parsing may not reach 100% accuracy due to macro expansion._

---

## Roadmap

- **Launched in 0.8.3:** interactive call-graph visualization
- **Verfied on tomcat,openJDK,expressJS,ThreeJS:** JavaScript and Java validation
- **Planned for next release:** MCP server for exposing Eulix retrieval to coding agents
- **Planned for next release:** additional repository-scale benchmarks
- **Planned:** architecture-aware documentation generation
- **Planned:** LSP integration
- **Planned:** improved embedding models for difficult identifiers and deep semantic queries
- **Planned:** expanded user studies and hard query sets

---

## Documentation

**Core:** [About Eulix](docs/About-Eulix.md) · [Installation](docs/installation.md) · [Model Selection](docs/models-to-use.md)

**Architecture:** [System Overview](docs/architecture/01-system-overview.md) · [Query Pipeline](docs/architecture/03-query-pipeline.md) · [Parser Internals](docs/architecture/07-parser-internals.md) · [Context Builder](docs/architecture/08-context-builder.md) · [Cache Architecture](docs/architecture/05-cache-architecture.md) · [Embedding Pipeline](docs/eulix-embed/architecture.md)

**Research / internals:** [Classifier](docs/architecture/06-classifier.md) · [Query Package](docs/query-package.md) · [Parser Architecture](docs/eulix-parser/Architecture.md)

**Project health:** [Known Issues](docs/known-issues.md)

Documentation is being updated to reflect the latest architecture, and some internal design documents describe earlier versions of Eulix. See [#64](https://github.com/Nurysso/eulix/issues/64) for the ongoing refresh. In the meantime, you can browse auto-generated docs on [DeepWiki](https://deepwiki.com/Nurysso/eulix).

---

## Development & Contributing

Eulix is under active development. Issues, benchmarks, documentation improvements, language support, parser work, retrieval experiments, and difficult real-world test cases are all welcome.

> **Working in a large or particularly complex codebase? Open an issue with a query that's hard to answer — those cases drive the retrieval roadmap.**

For non-trivial changes, please **open an issue first** so the approach can be discussed before implementation.

### Repository layout

| Directory       | Component                          | Language | License    |
| --------------- | ---------------------------------- | -------- | ---------- |
| `eulix-parser/` | Parser and indexing engine (PRISM) | Rust     | GPLv3      |
| `eulix-embed/`  | Embedding pipeline                 | Python   | Apache 2.0 |
| `eulix-cli/`    | CLI, routing, retrieval, LLM layer | Go       | GPLv3      |

### Local setup

```bash
git clone https://github.com/Nurysso/eulix.git
cd eulix

# Build all components (see docs/installation.md for platform-specific notes)
bash build.sh

# Confirm the installed components and verify bundled binaries
eulix version
eulix verifyBins
```

### Tests, formatting, and linting

Each component uses its language's standard toolchain:

```bash
# Parser (Rust)
cd eulix-parser
cargo fmt --check
cargo clippy
cargo test

# CLI (Go)
cd eulix-cli
gofmt -l .
go vet ./...
go test ./...
```

### Contribution workflow

1. Open an issue describing the change (non-trivial changes) or a hard query you'd like Eulix to handle.
2. Fork the repository and create a feature branch.
3. Format and lint the component you changed, and run its tests.
4. Open a pull request that links the issue.

---

## License & Acknowledgments

### License

- `eulix` and `eulix_parser` — [GNU GPLv3](LICENSE)
- `eulix_embed` — [Apache 2.0](eulix-embed/LICENSE)

### Acknowledgments

Eulix builds on excellent open-source work:

- [Tree-sitter](https://tree-sitter.github.io/) — the parsing foundation of `eulix_parser`
- [PyTorch](https://pytorch.org/) and [ONNX Runtime](https://onnxruntime.ai/) — embedding backends
- [BAAI/bge-base-en-v1.5](https://huggingface.co/BAAI/bge-base-en-v1.5) — default embedding model
- [Ollama](https://ollama.com) — local LLM provider
- [DeepWiki](https://deepwiki.com/Nurysso/eulix) — auto-generated project documentation

Eulix is developed and stress-tested against large, real-world open-source repositories: the **Linux kernel**, the **Go compiler**, **FFmpeg**, **Gecko / Firefox**, and **Django**. Thanks to the maintainers of these projects for building codebases worth studying.

---

<div align="center">

**Eulix**

_Understand the code you didn't write._

</div>
