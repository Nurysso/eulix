# Eulix

<div align="center">

<img src="docs/assets/logo.jpg" alt="Eulix" width="120" />

### Local-first code navigation for large codebases.

**Find code. Trace relationships. Understand unfamiliar systems.**

[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg?style=for-the-badge)](LICENSE)
[![eulix-embed](https://img.shields.io/badge/eulix--embed-Apache%202.0-blue.svg?style=for-the-badge)](eulix-embed/LICENSE)
[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Rust](https://img.shields.io/badge/Rust-orange?style=for-the-badge&logo=rust&logoColor=white)](https://www.rust-lang.org)
[![Python](https://img.shields.io/badge/Python-3670A0?style=for-the-badge&logo=python&logoColor=ffdd54)](https://www.python.org/)

[Overview](#overview) · [Quickstart](#quickstart) · [How-it-works](#how-it-works) · [Benchmarks](#benchmarks) · [CLI](#cli) · [Architecture](#architecture) · [Docs](#documentation)

</div>

---

## Overview

Large codebases are hard for a simple reason:

**you usually don't know where the answer is.**

You can grep for a symbol.
You can jump through an LSP.
You can search filenames.
You can ask an LLM.

The hard part is following the relationships between them.

Eulix builds a local representation of your repository containing symbols, relationships, indexes, and optional semantic embeddings. Queries are then routed to the cheapest useful mechanism:

- direct repository data for simple questions
- structural and lexical retrieval for navigation
- semantic retrieval when meaning matters
- graph expansion when relationships matter
- an LLM when the query actually needs reasoning

The goal is not to make everything an LLM problem.

It's to make the codebase itself searchable, navigable, and useful.

---

## Why Eulix?

### Local-first

Your source code stays on your machine by default.

Local parsing, indexing, retrieval, and embedding are supported. Cloud LLMs are optional.

### Structure-aware

Eulix does more than search text.

It builds information about:

- functions
- methods
- classes
- symbols
- files
- dependencies
- call relationships
- project structure

### Hybrid retrieval

Different questions need different search strategies.

Eulix can combine:

- exact symbol lookup
- lexical / keyword search
- semantic search
- subsystem detection
- call-graph expansion
- relevance + diversity re-ranking

### Not every query needs an LLM

Questions such as:

```text
Where is X?
Who calls X?
What are the project metrics?
How is X used?
```

can be answered directly from the generated repository data.

No model call required.

### Built for large repositories

Eulix is designed around the assumption that the repository might be:

- millions of lines
- tens of thousands of files
- spread across multiple languages
- full of old code nobody wants to read line-by-line

---

# Quickstart

```bash
cd your-project

eulix init
eulix analyze
eulix chat
```

That's the normal workflow.

### What happens during `analyze`?

```text
source tree
    |
    v
parser
    |
    +--> symbols
    +--> relationships
    +--> indexes
    +--> project metadata
    |
    v
embedding pipeline
    |
    v
local retrieval data
```

Once indexing is complete:

```bash
eulix chat
```

and start asking questions about the repository.

---

# A quick example

Suppose you're dropped into a large codebase and need to understand:

```text
How does Nova schedule instances with PCI passthrough
requirements, and which filters participate in the claiming process?
```

Instead of manually hunting through thousands of files, Eulix can use repository structure and retrieval to surface things such as:

```text
nova/nova/scheduler/filters/pci_passthrough_filter.py
nova/nova/pci/devspec.py
nova/nova/pci/manager.py
nova/nova/pci/request.py
nova/nova/pci/stats.py
...
```

and follow relevant relationships between them.

The point is not just to find one file.

**The point is to find the path through the code.**

---

# How It Works

Eulix is split into three main components.

```text
                         +------------------+
                         |   Codebase       |
                         +--------+---------+
                                  |
                                  v
                         +------------------+
                         | eulix_parser     |
                         | Rust             |
                         +--------+---------+
                                  |
                    +-------------+-------------+
                    |             |             |
                    v             v             v
                 Symbols       Graphs       Indexes
                    |             |             |
                    +-------------+-------------+
                                  |
                                  v
                         +------------------+
                         | eulix_embed      |
                         | Python           |
                         +--------+---------+
                                  |
                                  v
                         +------------------+
                         | Embedding data   |
                         +--------+---------+
                                  |
                                  v
                         +------------------+
                         | eulix             |
                         | Go                |
                         | query engine      |
                         +--------+---------+
                                  |
                     +------------+------------+
                     |            |            |
                     v            v            v
                  direct      retrieve       LLM
                  lookup      context      reasoning
```

## 1. `eulix_parser`

The parser is written in Rust.

It handles:

- source discovery
- parsing
- symbol extraction
- relationship analysis
- call-graph construction
- reverse call graphs
- indexes
- project metrics
- entry points
- dependency analysis
- pattern detection

The parser is powered by **PRISM**, Eulix's approximate relationship-resolution system for large-scale code navigation.

PRISM is designed for retrieval and navigation rather than formal whole-program verification.

> paper coming soon :)

---

## 2. `eulix_embed`

The embedding pipeline is written in Python.

It currently supports:

- PyTorch
- ONNX Runtime
- CPU execution
- GPU execution
- streaming embedding generation
- configurable chunking
- optional INT8/SQ8 quantization
- long-lived embedding service mode

Embeddings are written to disk rather than requiring the entire corpus to live in memory.

---

## 3. `eulix`

The main CLI is written in Go.

It handles:

- repository initialization
- orchestration
- query routing
- retrieval
- context construction
- LLM integration
- configuration
- history
- validation
- caching
- user-facing CLI commands

---

# Query Routing

One of the important parts of Eulix is that **not every query goes through the same pipeline**.

A query is first classified into an intent/category.

For example:

```text
"Where is foo?"
        |
        v
  Location

"Who calls foo?"
        |
        v
  reverse call graph

"Whare project metrics?"
        |
        v
  project metrics

"How does this subsystem work?"
        |
        v
  retrieval + structural context

"Why does this happen?"
        |
        v
  retrieval + LLM reasoning
```

This keeps simple questions fast and reserves expensive context construction / model reasoning for questions that actually need it.

---

# Retrieval

For queries that need retrieval, Eulix can combine multiple signals:

```text
                            query
                              |
                    +---------+---------+
                    | Explicit Anchors  |
                    |   & Path Gate     |
                    +---------+---------+
                              |
        +---------------------+---------------------+
        |                     |                     |
  Symbol / Exact           Lexical               Semantic
 (kb_exact, grep,        (BM25/Keyword         (IVF Vector Search;
  exact, partial)       + Symbol Boost)       Skipped if Callers/
        |                     |              Callees/High Specificity)
        +---------------------+---------------------+
                              |
                    +---------+---------+
                    | Merge, Deduplicate|
                    |  & Multi-Boost    |
                    +---------+---------+
                              |
                    +---------+---------+
                    | Subsystem Signals |
                    |   & Noise Filter  |
                    +---------+---------+
                              |
                    +---------+---------+
                    |  Scope & Test-File|
                    |     Demotion      |
                    +---------+---------+
                              |
                    +---------+---------+
                    | Exact-First Sort  |
                    |  & TopK Truncate  |
                    +---------+---------+
                              |
                      scored chunks
```

The retrieval layer can be tuned through `eulix.toml`.

Important controls include:

- candidate count
- semantic similarity threshold
- graph expansion depth
- graph expansion limits
- exact anchor limits
- subsystem boosting
- cross-root isolation
- test-file penalties
- MMR diversity
- context chunk limits
- code/AST budget

See the configuration guide for the complete list.

---

# Benchmarks

## eulix_parser

These are current local measurements and are intended as engineering benchmarks, not universal guarantees.

> complete bench can be seen [here](./eulix-parser/README.md#performance)

**Command:**

```bash
eulix_parser --root . --output out/kb.json --threads 12 --verbose --prism 2
```

## Linux kernel

Parser benchmark on Linux kernel source:

```text
Files:                33,743
Failed Files:         0
Lines of code:        25,200,532
Functions:            612,250
Classes:              206,142
Methods:              3324

Graph nodes:          732,364
Graph edges:          1,301,640

Parser time:          37.83s
Analysis time:        6.10s
complete run:         51.39s
Peak RSS:             ~7.98 GB
```

This benchmark was run against `58717b2` a local checkout of the Linux kernel.

---

## OpenStack

Parser benchmark:

```text
Files processed:      29,623
Failed Files:         0
Lines of code:        6,936,415
Functions:            46,361
Classes:              51,520
Methods:              208,382

Graph nodes:          306,165
Graph edges:          757,025

Parser time:          7.93s
Analysis time:        1.91s
complete run:         12.59s
Peak RSS:             ~3.16GB
```

## eulix_embed:

> OpenStack

```text
Knowledge base:   ~864 MB
Chunks:           334,569
Backend:          ONNX Runtime
Quantization:     SQ8 / INT8
Time:             ~15 minutes.
```

Eulix_embed depends upon gpu compute platform and gpu architecture matters heavily for performance.
As i only have access to AMD Radeon RX 6700 XT time to embed ~850MB(kb.json) was 15 min on nvidia cards or amd cards that officially supports ROCm there should be a signiicant performance gains.

---

## Retrieval example

Repository: OpenStack

Question:

```text
How does Nova schedule instances with PCI passthrough
requirements and what filters are applied during the claiming process?
```

Recent retrieval run:

```text
Candidates:        625
Graph expansion:   43 chunks
Final selected:    15 chunks
Context budget:    30,000 tokens
Retrieval time:    ~800 ms
```

The retrieval surfaced the expected PCI-related scheduler and resource-management code along with supporting helper functions and loosely connected relationships.

Retrieval quality is still being evaluated manually across real repositories. These numbers are not presented as a formal accuracy benchmark.

---

# Supported Languages

Current parser support includes:

```text
C
C++
Go
Python
Rust
TypeScript
```

JavaScript and Java support are currently in development.

Language support and relationship resolution are still evolving, especially for language features such as dynamic dispatch, reflection, generated code, and other patterns that are difficult to resolve statically.

---

# CLI

```text
eulix [command]
```

## Main commands

| Command      | Purpose                                          |
| ------------ | ------------------------------------------------ |
| `init`       | Initialize Eulix in the current repository       |
| `analyze`    | Parse, analyze, and generate repository data     |
| `parser`     | Access the parser wrapper                        |
| `embed`      | Run the embedding pipeline                       |
| `chat`       | Start the interactive interface                  |
| `query`      | Build retrieval context or answer direct queries |
| `config`     | Manage Eulix configuration                       |
| `history`    | Browse previous queries                          |
| `checksum`   | Update repository checksums                      |
| `glados`     | Validate knowledge base and embedding state      |
| `verifyBins` | Verify bundled binary hashes                     |
| `version`    | Show component versions                          |

Development / diagnostic commands also exist, including `aspirine`.

---

# `eulix_parser`

```bash
eulix_parser [OPTIONS] --root <ROOT> --prism <PRISM>
```

Important options:

```text
-r, --root <ROOT>              Project root
-o, --output <OUTPUT>          Knowledge base output path
-t, --threads <THREADS>       Parser threads
-l, --languages <LANGUAGES>   Languages to parse
-p, --prism <PRISM>           PRISM version
    --no-analyze               Skip analysis phase
    --euignore <PATH>          Custom ignore file
-v, --verbose                  Verbose output
-V, --version                  Print version
```

---

# `eulix_embed`

```bash
eulix_embed <command>
```

Available commands:

```text
embed
query
server
compare
ijson-backend
version
```

Example:

```bash
eulix_embed embed \
  --kb-path .eulix/kb.json \
  --output .eulix/embeddings \
  --engine onnx \
  --device auto \
  --quantize
```

The `server` command provides a long-lived embedding process so repeated query embeddings don't require repeatedly loading the model.

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
top_k_candidates = 150
mmr_diversity_factor = 0.65
max_graph_expansion_depth = 1
semantic_min_similarity = 0.15
max_graph_expansions = 15
max_exact_anchors = 2
enable_subsystem_boosting = true
max_context_chunks = 30
```

The retrieval layer exposes additional controls for tuning context selection and graph traversal.

Full configuration reference:

[Configuration Guide](eulix-cli/README.md#configuration-reference-eulixtoml)

---

# Local-first by design

Eulix is designed around local execution.

By default:

```text
your source
   |
   v
parser
   |
   v
indexes / graph / embeddings
   |
   v
local query engine
```

Cloud LLM providers are supported, but they are explicitly configured by the user.

This distinction matters for proprietary codebases.

**If you send code to a remote model, that code is no longer local.**

Plan accordingly.

---

# Current limitations

Eulix is not a compiler and PRISM is not a formal program-analysis system.

Some relationships can be incomplete or approximate, especially around:

- dynamic dispatch
- reflection
- indirect calls
- generated code
- language-specific metaprogramming
- highly dynamic languages
- incomplete parser coverage

Retrieval quality also depends on:

- repository structure
- indexing configuration
- embedding model
- hardware
- context limits
- query type

We would rather document these limitations than hide them behind an LLM.

---

# Research

PRISM is one of the main research components behind Eulix.

The goal is simple:

> **Resolve enough code relationships to make large-scale retrieval useful without requiring prohibitively expensive whole-program analysis.**

PRISM uses a combination of structural information, identity metadata, language-aware heuristics, and approximate relationship resolution.

Research papers and technical notes will be published as the system evolves.

See:

- [PRISM documentation](docs/)
- [Parser internals](docs/architecture/07-parser-internals.md)
- [Query pipeline](docs/architecture/03-query-pipeline.md)

---

# Roadmap

The project is evolving around a simple idea:

**one code intelligence layer, multiple ways to use it.**

Planned / in-progress work includes:

- [ ] MCP server
- [ ] Interactive call-graph visualization
- [ ] Architecture-aware documentation generation
- [ ] LSP integration
- [ ] JavaScript support
- [ ] Java support
- [ ] More repository-scale benchmarks
- [ ] More real-world user stories

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

# Real repositories

Eulix is being tested against real, unpleasant codebases rather than only toy examples.

Current investigations include:

```text
OpenStack
Linux kernel
Go compiler
FFmpeg
Gecko / Firefox
```

More importantly, these are being treated as **engineering stress tests**.

We're interested in:

- what Eulix retrieves correctly
- where structural analysis breaks
- which queries are difficult
- how retrieval behaves as repositories grow
- how much context is actually useful

---

# Contributing

Eulix is still early.

Issues, benchmarks, documentation improvements, language support, parser work, retrieval experiments, and difficult real-world test cases are all useful.

For non-trivial changes, opening an issue first is appreciated so the approach can be discussed before implementation.

If you work on a large or particularly painful codebase, a great contribution is simply:

> **give us a hard question to answer.**

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
