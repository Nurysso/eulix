# Eulix

<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="112" />

### Local-first code navigation for large codebases.

**Find code. Trace relationships. Understand unfamiliar systems.**

[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg?style=for-the-badge)](LICENSE)
[![eulix-embed](https://img.shields.io/badge/eulix--embed-Apache%202.0-blue.svg?style=for-the-badge)](eulix-embed/LICENSE)
[![Parser](https://img.shields.io/badge/parser-v0.7.7-8A2BE2?style=for-the-badge)](eulix-parser/README.md)
[![CLI](https://img.shields.io/badge/CLI-v0.8.2-8A2BE2?style=for-the-badge)](eulix-cli/README.md)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Rust](https://img.shields.io/badge/Rust-orange?style=for-the-badge&logo=rust&logoColor=white)](https://www.rust-lang.org/)
[![Python](https://img.shields.io/badge/Python-3670A0?style=for-the-badge&logo=python&logoColor=ffdd54)](https://www.python.org/)

[Overview](#overview) · [Quickstart](#quickstart) · [Demo](#demo) · [Benchmarks](#benchmarks) · [How it works](#how-it-works) · [Retrieval](#retrieval) · [Limitations](#known-limitations) · [Docs](#documentation)

</div>

---

## Overview

Large codebases are difficult for a simple reason:

> **You usually don't know where the answer is.**

You can grep for a symbol. Jump through an LSP. Search filenames. Ask an LLM.

The difficult part is the space **between** those things: callers, dependencies, types, subsystems, implementation paths, and the surrounding context that gives a symbol its meaning.

Eulix builds a local representation of a repository containing structured symbols, relationships, indexes, project metadata, and optional semantic embeddings. Queries are then routed to the cheapest mechanism that can answer them:

```text
simple fact ───────────────► direct repository data
symbol / navigation ───────► exact + lexical retrieval
semantic question ─────────► vector retrieval
relationship question ─────► call-graph / structural expansion
reasoning question ────────► retrieved context + LLM
```

**The goal is not to make everything an LLM problem.**

It is to make the codebase itself searchable, navigable, and useful.

---

## Why Eulix?

<table>
<tr>
<td width="50%" valign="top">

### Local-first

Your source code stays on your machine by default.

Parsing, indexing, retrieval, and embedding can all run locally. Cloud LLMs are optional and explicitly configured.

</td>
<td width="50%" valign="top">

### Structure-aware

Eulix does more than search text.

It builds information about:

- functions, methods, classes, and symbols
- dependencies and imports
- call and reverse-call relationships
- project structure and metadata
- entry points, patterns, and metrics

</td>
</tr>
<tr>
<td width="50%" valign="top">

### Hybrid retrieval

Different questions need different search strategies.

Eulix can combine exact symbol lookup, lexical search, semantic retrieval, subsystem signals, graph expansion, and relevance/diversity re-ranking.

</td>
<td width="50%" valign="top">

### Not every query needs an LLM

Questions such as:

```text
Where is foo?
Who calls foo?
What are the project metrics?
How is foo used?
```

can often be answered directly from generated repository data.

</td>
</tr>
</table>

---

## Quickstart

### 1. Install

#### Linux / macOS

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh)
```

#### Windows

> May require Visual Studio Build Tools with the C++ workload.

```powershell
irm https://raw.githubusercontent.com/Nurysso/eulix/main/install.ps1 | iex
```

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

## Demo

<div align="center">

<!-- Replace YOUTUBE_VIDEO_URL when the demo is published. -->
<!--
<a href="YOUTUBE_VIDEO_URL">
  <img src="docs/assets/demo-poster.png" alt="Eulix demo video" width="850" />
</a>

<sub>▶ Watch Eulix analyze and navigate a large repository.</sub>
-->

**Demo video coming soon.**

</div>

<table>
  <tr>
    <td colspan="2">
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

## A quick example

Suppose you're dropped into a large codebase and need to understand:

```text
How does Nova schedule instances with PCI passthrough
requirements, and which filters participate in the claiming process?
```

Instead of manually hunting through thousands of files, Eulix can surface relevant scheduler, PCI, resource-management, and helper code and then follow relationships between them.

The point is not just to find one file.

> **The point is to find the path through the code.**

---

# How it works

Eulix is built as a pipeline of specialized components rather than one monolithic search system.

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

## 1. `eulix_parser`

The parser is written in Rust and is closer to a **repository analysis and indexing engine** than a traditional parser.

It handles:

- source discovery and parsing
- function, method, class, and symbol extraction
- relationship and call-graph analysis
- reverse call graphs
- indexes and lookup structures
- project metrics and entry points
- dependency and pattern analysis
- language-specific metadata

The parser is powered by **PRISM**, Eulix's approximate relationship-resolution system for large-scale code navigation.

PRISM is designed for retrieval and navigation rather than formal whole-program verification.

> Research and technical notes are being prepared as the system evolves.

## 2. `eulix_embed`

The embedding pipeline is written in Python.

It supports:

- PyTorch
- ONNX Runtime
- CPU and GPU execution
- streaming embedding generation
- configurable chunking
- INT8 / SQ8 quantization
- long-lived embedding service mode
- a custom on-disk embedding/vector format designed for verification and memory-mapped access

Embeddings are written to disk rather than requiring the entire corpus to remain resident in memory.

## 3. `eulix`

The main CLI is written in Go.

It handles:

- repository initialization and orchestration
- query classification and routing
- retrieval and context construction
- LLM integration
- configuration and history
- validation and caching

---

# Query routing

One of Eulix's core ideas is that **not every query should go through the same pipeline**.

A deterministic classifier first routes a query toward an appropriate retrieval path.

```text
"Where is foo?"
       │
       └──► location / exact lookup

"Who calls foo?"
       │
       └──► call graph

"What are the project metrics?"
       │
       └──► project metadata

"How does this subsystem work?"
       │
       └──► retrieval + structural context

"Why does this happen?"
       │
       └──► retrieval + LLM reasoning
```

This keeps simple questions cheap and reserves expensive context construction and model reasoning for questions that actually need them.

---

# Retrieval

For retrieval-heavy queries, Eulix can combine several signals:

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

The retrieval layer can be tuned through `eulix.toml`, including candidate counts, semantic thresholds, graph expansion, anchor limits, subsystem boosting, cross-root isolation, test-file penalties, MMR diversity, and context budgets.

---

# Benchmarks

> These are local engineering measurements, not universal performance guarantees. Hardware, repository state, parser configuration, embedding model, and query shape all affect results.

## Linux kernel — parser

Latest full parser run on a Linux-kernel-scale checkout using **12 threads** and **PRISM v2**:

| Metric                     |         Result |
| -------------------------- | -------------: |
| Files scanned              |     **94,012** |
| Source files parsed        |     **64,460** |
| Failed / skipped           |      **0 / 0** |
| Lines of code              | **37,322,700** |
| Functions                  |    **648,407** |
| Classes                    |    **211,416** |
| Methods                    |      **6,243** |
| Graph nodes                |    **774,296** |
| Graph edges                |  **1,571,981** |
| Parse phase                |    **37.39 s** |
| Relationship / index phase |     **7.01 s** |
| Summary / metrics phase    |     **0.39 s** |
| Parser total               |    **48.43 s** |
| Peak RSS                   |   **~8.8 GiB** |

### Generated repository data

```text
kb.json              ~709 MB
kb_index.json        ~415 MB
kb_call_graph.json   ~432 MB
kb_summary.json      ~3.3 MB
other metadata       ~14 MB
```

The resulting `.eulix` directory for this run is approximately **2.2 GB**, down from roughly **4.6 GB** in the earlier representation — about a **52% reduction** in on-disk size.

The large reduction comes primarily from using slimmer serialized repository views rather than writing every field from the parser's richer internal representation.

---

## Linux kernel — embedding pipeline

The same Linux-kernel-scale analysis was then passed through the current embedding pipeline using:

```text
Model:       BAAI/bge-base-en-v1.5
Dimension:   768
Quantization SQ8 / INT8
Device:      AMD GPU (ROCm / HIP)
Chunks:      838,755
```

Measured on an **AMD Radeon RX 6700 XT**:

| Metric                         |           Result |
| ------------------------------ | ---------------: |
| KB scan + chunk generation     |      **48.68 s** |
| Embedding generation           | **~27 min 50 s** |
| Embedding pipeline total       | **~28 min 49 s** |
| Full `eulix analyze` wall time |  **29 min 50 s** |
| Max RSS                        |     **~8.8 GiB** |
| `embeddings.bin`               |    **~617.5 MB** |
| `vectors.bin`                  |     **~49.6 MB** |

The parser is not the current bottleneck at this scale; **embedding generation dominates end-to-end analysis time**.

Higher-end GPUs can materially change embedding throughput, but Eulix is intentionally hardware-portable. We benchmark hardware-specific performance separately rather than treating one GPU as a universal baseline.

---

## Retrieval quality: an explicit limitation

Eulix is designed to get **usefully close**, not to claim perfect retrieval for every possible question.

Retrieval quality depends on how much information the query provides. Concrete queries containing symbols, function names, file paths, or other codebase-specific terms give Eulix stronger anchors for its retrieval and relationship-resolution pipeline. More open-ended questions can still identify relevant subsystems and return useful context, but deeper analysis may miss parts of the underlying dependency chain.

For example, on Django:

```text
When Expression.resolve_expression() processes an OuterRef, which specific Set attribute on the inner
Query object is mutated to track parent table aliases? How does SQLCompiler.get_from_clause()
use this set to ensure those outer tables are omitted from the subquery's FROM SQL block?
```

#### This type of question depends on a very specific chain of behavior spanning multiple symbols and files.

Eulix can **retrieve** much of that chain, but **100% retrieval accuracy is not guaranteed**.

**Vague** questions can also produce useful results, but provide **fewer anchors** for the retrieval system:

```text
How does table aliases is handled when generating SQL for correlated subqueries?
```

In this example, Eulix can identify the relevant SQL subsystem and surface code from Django's SQL compiler and related structures, even without an explicit function or file name. However, deeper dependencies may still be missing from the final context.

More specific queries generally provide stronger retrieval anchors and better coverage.

The practical goal is:

Retrieve enough of the right code, quickly enough, to produce a useful answer.

On deep queries, Eulix can get close enough to provide a strong context window for an LLM while remaining explicit about the possibility of missing or mis-ranking parts of the chain.

Embedding quality is also part of the equation. Better embedding models can improve semantic retrieval, particularly for codebases with cryptic identifiers, domain-specific vocabulary, or relationships that are difficult to capture from short text alone. Eulix is actively testing newer code-and-language models and longer-context models at Linux-kernel scale.

> [!IMPORTAN]
> A better model can improve semantic retrieval — but it does not turn retrieval into a formal proof system.

---

# Known limitations

Eulix is not a compiler, and PRISM is not a formal whole-program analysis system.

Some relationships can be incomplete or approximate, especially around:

- dynamic dispatch
- reflection and indirect calls
- generated code
- language-specific metaprogramming
- highly dynamic languages
- incomplete parser coverage

Retrieval quality can also vary with:

- repository structure and indexing configuration
- query specificity
- embedding model
- hardware and embedding throughput
- context limits
- language semantics

We would rather document these constraints than hide them behind an LLM.

---

# Supported languages

Current parser support includes:

```text
C          C++          Go
Python     Rust         TypeScript
```

JavaScript and Java support are exists but havent be verified.

Language support and relationship resolution continue to evolve, especially for language features that are difficult to resolve statically.

---

# CLI

```text
eulix [command]
```

| Command      | Purpose                                             |
| ------------ | --------------------------------------------------- |
| `init`       | Initialize Eulix in the current repository          |
| `analyze`    | Parse, analyze, embed, and generate repository data |
| `parser`     | Access the parser wrapper                           |
| `embed`      | Run the embedding pipeline                          |
| `chat`       | Start the interactive interface                     |
| `query`      | Build retrieval context or answer direct queries    |
| `history`    | Browse previous queries                             |
| `checksum`   | Update repository checksums                         |
| `verifyBins` | Verify bundled binary hashes                        |
| `version`    | Show component versions                             |

> the `query` command is used for testing retrieval

---

# Configuration

Eulix uses `eulix.toml`.

A minimal configuration looks like:

```toml
[project]
path = "."

[parser]
threads = 12
prismVersion = 2

[embeddings]
model = "BAAI/bge-base-en-v1.5"
dimension = 768
engine = "onnx"

[llm]
local = true
provider = "ollama"
model = "llama3.2:3b"
max_tokens = 8192

[retrievalConfig]
code_to_ast_ratio = 0.95
apply_cross_root_isolation = true
cross_root_penalty = 0.32
pre_mmr_score_floor_ratio = 0.05
top_k_candidates = 150
mmr_diversity_factor = 0.65
max_graph_expansion_depth = 1
semantic_min_similarity = 0.15
max_graph_expansions = 15
max_exact_anchors = 2
test_file_penalty = 0.3
enable_subsystem_boosting = true
max_context_chunks = 30
```

See the [configuration guide](eulix-cli/README.md#configuration-reference-eulixtoml) for the complete reference.

---

# Local-first by design

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

Cloud LLM providers are supported, but explicitly configured by the user.

For proprietary repositories, understand the boundary clearly: **sending source code to a remote model means that code is no longer local.**

---

# Research

Eulix is being developed around a simple loop:

```text
research
   ↓
new representation / algorithm
   ↓
implementation
   ↓
real repository stress tests
   ↓
failure modes + measurements
   ↓
new research
```

One of the central research components is **PRISM**: an approximate relationship-resolution system intended to make large-scale structural retrieval useful without the cost of formal whole-program analysis.

The broader direction is to explore better computational representations of software — and then expose those representations through practical developer tooling.

> **AI is a consumer of the code intelligence layer, not the definition of the layer.**

---

# Real repositories

Eulix is tested against real, unpleasant codebases rather than only toy examples.

Current investigations include:

```text
Linux kernel
Go compiler
FFmpeg
Gecko / Firefox
Django
```

These repositories are treated as engineering stress tests for:

- large-scale parsing
- relationship resolution
- retrieval quality
- cryptic and domain-specific symbols
- deep cross-file queries
- scalability as repositories grow
- useful context construction

The goal is not to make one benchmark look good. It is to discover where the system breaks and then improve the system.

---

# Roadmap

The long-term idea is simple:

> **One code-intelligence layer, multiple ways to use it.**

Planned / in-progress work includes:

- [ ] MCP server for exposing Eulix retrieval to coding agents
- [ ] Interactive call-graph visualization
- [ ] Architecture-aware documentation generation
- [ ] LSP integration
- [ ] JavaScript support
- [ ] Java support
- [ ] More repository-scale benchmarks
- [ ] Better embedding models for difficult identifiers and deep semantic queries
- [ ] More real-world user studies and hard query sets

---

# Documentation

### Core

- [About Eulix](docs/About-Eulix.md)
- [Installation](docs/installation.md)
- [Model Selection](docs/models-to-use.md)

### Architecture

- [System Overview](docs/architecture/01-system-overview.md)
- [Query Pipeline](docs/architecture/03-query-pipeline.md)
- [Parser Internals](docs/architecture/07-parser-internals.md)
- [Context Builder](docs/architecture/08-context-builder.md)
- [Cache Architecture](docs/architecture/05-cache-architecture.md)
- [Embedding Pipeline](docs/eulix-embed/architecture.md)

### Research / internals

- [Classifier](docs/architecture/06-classifier.md)
- [Query Package](docs/query-package.md)
- [Parser Architecture](docs/eulix-parser/Architecture.md)

### Project health

- [Known Issues](docs/known-issues.md)

---

# Contributing

Eulix is still early.

Issues, benchmarks, documentation improvements, language support, parser work, retrieval experiments, and difficult real-world test cases are all useful.

For non-trivial changes, opening an issue first is appreciated so the approach can be discussed before implementation.

Working on a large or particularly painful codebase?

> **Give us a hard question to answer.**

---

# License

- `eulix` and `eulix_parser` — GNU GPLv3
- `eulix_embed` — Apache 2.0

See [LICENSE](LICENSE) and [eulix-embed/LICENSE](eulix-embed/LICENSE).

---

<div align="center">

**Eulix**

_Understand the code you didn't write._

</div>
