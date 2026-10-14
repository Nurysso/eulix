<div align="center">

<picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://github.com/user-attachments/assets/fe2f0f29-7fc1-4f6e-9a6d-0a0a1268a171">
    <img src="https://github.com/user-attachments/assets/3218952d-3a97-4606-af4a-327589df6ebc" alt="Local-first code intelligence for large codebases" width="800" />
</picture>

<div align="left">
Eulix parses your repository into a queryable index — symbols, call graphs, and optional embeddings — and routes every question to the cheapest mechanism that can actually answer it. The LLM is optional, and only used where reasoning is genuinely required.
</div>

<!-- [▶ Watch Demo](https://www.youtube.com/watch?v=ID) ·  -->

[🚀 Quick Start](#quick-start) · [📖 Documentation](#documentation)

<br>

[![License](https://img.shields.io/badge/License-Apache--2.0-A78BFA?style=for-the-badge&labelColor=1A2030)](./LICENSE)
[![libeulix](https://img.shields.io/badge/libeulix-5EEAD4?style=for-the-badge&logo=rust&logoColor=E7E9EE&labelColor=1A2030)](https://crates.io/crates/libeulix)
[![CLI](https://img.shields.io/badge/CLI-v0.8.3-A78BFA?style=for-the-badge&labelColor=1A2030)](eulix-cli/README.md)
[![Parser](https://img.shields.io/badge/Parser-v0.8.1-5EEAD4?style=for-the-badge&labelColor=1A2030)](eulix-parser/README.md)
[![LSP](https://img.shields.io/badge/LSP-v0.1.2_beta-A78BFA?style=for-the-badge&labelColor=1A2030)](./eulix-lsp/README.md)
[![MCP](https://img.shields.io/badge/MCP-BETA-5EEAD4?style=for-the-badge&labelColor=1A2030)](./eulix-cli/internal/mcp/README.md)

[![Go](https://img.shields.io/badge/Go-5EEAD4?style=flat-square&logo=go&logoColor=1A2030&labelColor=A78BFA)](https://go.dev)
[![Rust](https://img.shields.io/badge/Rust-5EEAD4?style=flat-square&logo=rust&logoColor=1A2030&labelColor=A78BFA)](https://www.rust-lang.org)
[![Python](https://img.shields.io/badge/Python-5EEAD4?style=flat-square&logo=python&logoColor=1A2030&labelColor=A78BFA)](https://www.python.org)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/Nurysso/eulix)

</div>

---

## The problem

Large codebases are hard for a simple reason:

> **You usually don't know where the answer is.**

Grep finds strings. An LSP resolves a symbol you already know. An LLM answers confidently about code it has never seen. None of them cover the space **in between** — callers, dependencies, types, subsystems, implementation paths, and the surrounding context that gives a symbol its meaning.

### How Eulix compares

| Capability                            | `ripgrep`      | IDE / LSP             | LLM chat                       | **Eulix**                         |
| :------------------------------------ | :------------- | :-------------------- | :----------------------------- | :-------------------------------- |
| **Find exact text**                   | ✅ Yes         | ✅ Yes                | ⚠️ Context-dependent           | ✅ Indexed search                 |
| **Navigate symbols**                  | ⚠️ Text-based  | ✅ Yes                | ⚠️ Context-dependent           | ✅ Symbol index                   |
| **Trace callers & callees**           | ❌ No          | ⚠️ Partial            | ⚠️ Context-dependent           | ✅ Call graph                     |
| **Understand subsystems**             | ❌ No          | ⚠️ Manual exploration | ⚠️ Requires context            | ✅ Hybrid retrieval               |
| **Ground answers in repository code** | ✅ Matches     | ✅ Source references  | ⚠️ Depends on supplied context | ✅ Retrieved code context         |
| **Keep processing local**             | ✅ Yes         | ✅ Yes                | ⚠️ Provider-dependent          | ✅ Local-first                    |
| **Analyze repository-scale code**     | ✅ Text search | ⚠️ Tool-dependent     | ⚠️ Context limits              | ✅ 37.3M LOC parsed in **57.2 s** |

<sub>Comparisons describe typical capabilities, not every tool or configuration. Eulix's benchmark reflects a local Linux kernel analysis run; parser performance does not imply complete semantic understanding. Remote LLM providers, when configured, may receive retrieved code context.</sub>

Eulix doesn't treat code understanding as an LLM problem. It treats it as an **indexing and retrieval problem** — and brings in an LLM only where reasoning is actually required.

---

## At a glance

| Metric         | Result                                                                 |
| -------------- | ---------------------------------------------------------------------- |
| **Scale**      | Linux kernel — **37.3M lines**, 64,460 source files parsed in **57 s** |
| **Throughput** | **~650K lines/s** end-to-end parser throughput on 12 threads           |
| **Graph**      | **773K nodes / 1.51M edges**, extracted with **0 parse failures**      |
| **Embeddings** | **837,531 chunks** embedded locally on a single consumer GPU           |
| **Privacy**    | Parsing, indexing, retrieval and embedding run **entirely on-device**  |
| **Languages**  | C, C++, Go, Java, JavaScript, Python, Rust, TypeScript                 |

---

## Key Features

<details>
<summary><strong>Structural code intelligence</strong></summary>

Extract functions, classes, methods, symbols, imports, dependencies, entry points, project metrics, and call relationships. Navigate callers and callees with structural context.

</details>

<details>
<summary><strong>Hybrid retrieval</strong></summary>

Combine exact symbol lookup, lexical search, semantic vector retrieval, subsystem signals, call-graph expansion, and MMR-based diversity ranking.

</details>

<details>
<summary><strong>Intelligent query routing</strong></summary>

Direct repository facts to deterministic lookups, navigation questions to structural data, and deeper semantic questions to retrieval and optional LLM reasoning.

</details>

<details>
<summary><strong>Local-first execution</strong></summary>

Run parsing, indexing, retrieval, and embedding locally. Use a local LLM or explicitly configure a remote provider.

</details>

<details>
<summary><strong>Developer ecosystem</strong></summary>

Integrate Eulix with coding agents through MCP, editors through the beta LSP server, custom Rust applications through libeulix, and your browser through the call-graph explorer.

</details>

<details>
<summary><strong>Configurable retrieval and embeddings</strong></summary>

Tune retrieval behavior through eulix.toml. Use PyTorch or ONNX Runtime, CPU or GPU execution, configurable chunking, and quantized embeddings.

</details>

Supported languages: C, C++, Go, Java, JavaScript, Python, Rust, and TypeScript. JSX and TSX are not currently supported.

<!-- ## Demo

<div align="center">

<a href="https://www.youtube.com/watch?v=ID">
<img src="https://img.youtube.com/vi/ID/maxresdefault.jpg" alt="Watch the Eulix demonstration" width="85%" />
</a>

Explore repository analysis, retrieval, and code navigation.

</div> -->

## Screenshots

<table>
<tr>
<td width="50%" align="center" valign="top">
  <img src="https://github.com/user-attachments/assets/be07896c-5726-401f-865c-2638e3583e6d" alt="Eulix terminal interface" height="300" />
  <br />
  <b>Chat With codeBase</b>
</td>
<td width="50%" align="center" valign="top">
  <img src="https://github.com/user-attachments/assets/5a4a36d8-2804-4402-a75f-d4edb4d0a80e" alt="Eulix retrieval query" height="300" />
  <br />
  <b>VizEulize</b>
</td>
</tr>
</table>

## Quick start

**Requires** Linux or Windows. Linux needs `bash` + `curl`; Windows needs PowerShell and may require Visual Studio Build Tools with the C++ workload.

> [!NOTE]
> **macOS** Binaries haven't been tested yet, if you encounter an issue raise a issue.

### 1. Install

```bash
# Linux
bash <(curl -fsSL https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh)
```

```powershell
# Windows
irm https://raw.githubusercontent.com/Nurysso/eulix/main/install.ps1 | iex
```

### 2. Analyze a repository

```bash
cd your-project

eulix init      # creates eulix.toml
eulix analyze   # parse + build the index
```

> [!TIP]
> `eulix analyze` is fast — **~650K lines/s**. On a 1M-line repo that's seconds. Semantic embeddings are the slow stage at scale and are only needed if you want open-ended questions answered. On the Linux kernel the graph builds in **57 s**; embedding 837K chunks took **~27 min** on RDNA2 GPU.

### 3. Ask

```bash
eulix chat      # interactive
eulix query "Who calls foo?"   # one-shot
```

That's it. No cloud account, no API key required.

---

## What you can ask

Each query is classified and routed to the cheapest path that can answer it:

| Query                           | Route                        | LLM?   |
| ------------------------------- | ---------------------------- | ------ |
| `Where is foo?`                 | Exact symbol lookup          | No     |
| `Who calls foo?`                | Call graph                   | No     |
| `What are the project metrics?` | Project metadata             | No     |
| `How does this subsystem work?` | Retrieval + structural edges | Rarely |
| `Why does this happen?`         | Retrieval + reasoning        | Yes    |

Most questions never invoke a model — they're answered straight from generated repository data. That's fast, free, and deterministic.

### Anchored vs. open-ended

Symbols, function names, and file paths act as **anchors** that drive exact lookup and graph expansion. Open-ended questions lean on semantic and subsystem signals instead.

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

> [!TIP]
> Adding a symbol or file path narrows the search and improves coverage. As with any retrieval system, completeness on deep multi-hop chains is not guaranteed.

### All commands

| Command      | Purpose                                              |
| ------------ | ---------------------------------------------------- |
| `init`       | Initialize Eulix in the current repository           |
| `analyze`    | Parse, analyze, embed, and generate repository data  |
| `chat`       | Start the interactive interface                      |
| `query`      | Build retrieval context or answer a direct query     |
| `mcp`        | Expose retrieval to coding agents (stdio MCP server) |
| `vizEulize`  | Open the call-graph explorer in your browser         |
| `parser`     | Access the parser wrapper                            |
| `embed`      | Run the embedding pipeline on its own                |
| `history`    | Browse previous queries                              |
| `version`    | Show component versions                              |
| `verifyBins` | Verify bundled binary hashes                         |
| `checksum`   | Update repository checksums                          |

---

## Ecosystem

The CLI is one front-end on a shared engine. Everything below reads the same local index.

| Surface                  | What it is                                        | Status |
| ------------------------ | ------------------------------------------------- | ------ |
| [`eulix`](#quick-start)  | CLI, query routing, retrieval, optional LLM layer | Stable |
| [`vizEulize`](#explorer) | Browser-based call-graph explorer                 | Stable |
| [`libeulix`](#library)   | The analysis engine as a standalone Rust crate    | Stable |
| [`eulix-lsp`](#editors)  | Multi-language language server                    | Beta   |
| [`eulix mcp`](#agents)   | MCP server so coding agents can query your repo   | Beta   |

### Agents

An MCP server (stdio) that lets agents such as Claude Code, Windsurf, Cline, Continue, and LM Studio query any analyzed repository and get ranked source context back. It keeps the knowledge base warm between queries, switches repositories on demand, and adapts retrieval to what the agent already knows (`known_symbols`).

```bash
eulix analyze                          # once per repository
eulix mcp --repo /path/to/repo         # stdio MCP server
```

```json
{
  "mcpServers": {
    "eulix": {
      "command": "eulix",
      "args": ["mcp"]
    }
  }
}
```

> [!TIP]
> Use an absolute path for `command` if your client doesn't inherit your shell `PATH`.

Tools exposed: `query`, `query_with_cot`, `query_without_cot`, `set_repo_path`. Full details in the [MCP README](eulix-cli/internal/mcp/README.md).

### Editors

`eulix-lsp` (beta) is a multi-language language server built on libeulix: document outline, hover, go-to-definition, find references, completion, rename (open buffers), semantic tokens, call hierarchy, folding ranges, workspace symbols, and workspace diagnostics.

```bash
cd eulix-lsp
cargo build --release
./target/release/eulix-lsp
```

Point your editor's LSP client at the built binary. See the [eulix-lsp README](eulix-lsp/README.md) for the full feature list and what is intentionally not implemented.

### Explorer

```bash
eulix analyze
eulix vizEulize
```

Starts a local server on `127.0.0.1` and opens an interactive, searchable call graph in your browser — focus, direction and depth controls, filters, tags, categories. Everything stays on your machine. See the [vizEulize README](vizEulize/README.md).

### Library

The parsing, PRISM call-graph, indexing, and metrics engine, shipped standalone:

```bash
cargo add libeulix
```

[`libeulix` on crates.io](https://crates.io/crates/libeulix) · source in [`libeulix/`](libeulix/)

---

## How it works

```text
                              CODEBASE
                                  │
                                  ▼
                    ┌─────────────────────────┐
                    │        libeulix         │
                    │   Rust + Tree-sitter    │
                    └────────────┬────────────┘
                                 │
               ┌─────────────────┼─────────────────┐
               ▼                 ▼                 ▼
          symbols & AST      relationships      indexes
               └─────────────────┼─────────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │    local retrieval      │
                    │ exact + lexical +       │
                    │ semantic + graph        │
                    └────────────┬────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │       eulix CLI         │
                    │ routing + context + LLM │
                    └─────────────────────────┘
```

Each component is written in the language best suited to its job:

| Component       | Language | Role                                                                                                                                    |
| --------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `libeulix/`     | Rust     | Analysis engine on Tree-sitter: source discovery, symbol extraction, relationship + call-graph analysis, indexes, metrics, entry points |
| `eulix-parser/` | Rust     | CLI front-end to libeulix; writes the knowledge base to `.eulix` using a spill-to-disk serializer to keep peak memory low               |
| `eulix-embed/`  | Python   | Streaming embedding pipeline: PyTorch / ONNX, CPU or GPU, INT8 / SQ8 quantization, service mode, memory-mapped vector store             |
| `eulix-cli/`    | Go       | Orchestration, query classification and routing, context construction, LLM integration, MCP server, `vizEulize` launcher                |
| `eulix-lsp/`    | Rust     | Language server, built on libeulix (beta)                                                                                               |
| `vizEulize/`    | HTML/JS  | Single-file call-graph explorer                                                                                                         |

Relationship resolution is powered by **PRISM**, Eulix's approximate resolution engine, built for large-scale code navigation rather than formal whole-program verification. See [Parser Architecture](docs/eulix-parser/Architecture.md).

### Retrieval pipeline

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
                             ▼
                    subsystem signals
                             ▼
                     scope / test-file
                         demotion
                             ▼
                    MMR + exact-first
                         ranking
                             ▼
                       selected context
```

### Privacy

```text
   your source → libeulix → local query engine → optional LLM
```

The entire analysis and retrieval stack runs on your machine. The LLM stage is optional, and cloud providers are opt-in. With a local provider such as [Ollama](https://ollama.com), **nothing leaves your environment**.

> [!WARNING]
> If you configure a **remote** provider, the retrieved code context for each query is sent to that provider. Choose accordingly for proprietary repositories.

---

## Configuration

Eulix is configured through **`eulix.toml`** in the repository root. A minimal starting point:

```toml
[project]
  path = "/absolute/path/to/your/repo"

[parser]
  threads = 8
  prismVersion = 2        # 1 = fast/direct, 2 = precise/scope-aware

[embeddings]
  model = "BAAI/bge-base-en-v1.5"
  dimension = 768
  engine = "onnx"         # "onnx" or "torch"

[llm]
  local = true            # set false to use a cloud provider
  provider = "ollama"     # ollama, openai, anthropic, gemini, deepseek, …
  model = "deepseek-r1:14b"
```

Everything else — MMR diversity, graph expansion depth, cross-root isolation, candidate limits, and the rest — is tunable under `[retrievalConfig]`. The full option list with comments is in the [configuration reference](eulix-cli/README.md#configuration-reference-eulixtoml).

> [!NOTE]
> `path` is absolute. If you move the repository or copy `eulix.toml` into another project, update it.

### Generated data

`eulix analyze` writes to a `.eulix` directory in the repository:

```text
.eulix/
├── kb.json              # knowledge base: symbols, functions, classes, methods
├── kb_index.json        # lookup indexes
├── kb_call_graph.json   # call graph and reverse call graph
├── kb_summary.json      # project summary and metrics
├── embeddings.bin       # SQ8-quantized embeddings (~617 MB on the kernel)
├── vectors.bin          # vector store (~50 MB on the kernel)
└── …                    # other metadata
```

Budget roughly **1.2 GB** for a kernel-scale checkout, and expect the parser to peak around **3.3 GiB RSS**.

---

## Benchmarks

> Measurements come from local engineering runs. Results vary with hardware, repository state, parser configuration, embedding model, and query shape.

### Parser — Linux kernel

**64,460 files · 37.3M lines · 0 failures · 57.2 seconds** (12 threads, PRISM v2)

| Metric             |         Result |
| ------------------ | -------------: |
| Files scanned      |     **94,063** |
| Source files found |     **64,460** |
| Failed / skipped   |      **0 / 0** |
| Lines of code      | **37,322,700** |
| Functions          |    **647,365** |
| Classes            |    **211,297** |
| Methods            |      **6,246** |
| Graph nodes        |    **773,072** |
| Graph edges        |  **1,510,341** |
| **Parser total**   |    **57.20 s** |

<details>
<summary>Phase timings and memory</summary>

| Phase                  |    Time |          RSS |
| ---------------------- | ------: | -----------: |
| Parse                  | 52.23 s |    1963.2 MB |
| Call graphs (PRISM v2) |  1.34 s |              |
| Summary / metrics      |  0.34 s |         3 MB |
| **Total**              | 57.20 s |              |
| Analysis phase         |         |       803 MB |
| **Peak (write phase)** |         | **~3.3 GiB** |

Serialization uses a spill-to-disk architecture with slim repository views rather than the parser's full internal representation. This cut peak RSS from ~8 GB to ~3.3 GiB and reduced on-disk output from ~2.2 GB to ~1.2 GB.

Parsing quality: 32,338 clean / 32,122 partial / 0 failed. The partials are overwhelmingly C/C++ and expected without a preprocessor pass.

</details>

### Embeddings — Linux kernel

> AMD Radeon RX 6700 XT (ROCm/HIP) · `BAAI/bge-base-en-v1.5` · 768-dim · SQ8/INT8

| Metric                       |                        Result |
| ---------------------------- | ----------------------------: |
| Chunks                       |                   **837,531** |
| Embedding generation         |               **26 min 27 s** |
| Embedding throughput         |            **527.6 chunks/s** |
| KB scan + chunk generation   |                   **20.27 s** |
| **Embedding pipeline total** |               **26 min 57 s** |
| **Full parse + embedding**   | **~28 min** (embedding-bound) |
| `embeddings.bin`             |                    **617 MB** |
| `vectors.bin`                |                     **50 MB** |

Parsing is about a minute of that run; embedding is the throughput-bound stage and scales with accelerator capability. These figures come from a mid-range consumer GPU — hardware-specific results are benchmarked separately rather than treating one GPU as a universal baseline.

> [!NOTE]
> Embedding model choice affects semantic recall, particularly for cryptic identifiers and domain-specific vocabulary. The model is configurable, and newer code-oriented and long-context models are being evaluated at kernel scale. A stronger model improves ranking quality — retrieval remains a ranking system, not a proof system.

### Validated on

Linux kernel · Go compiler · FFmpeg · Gecko / Firefox · Django · OpenStack Nova

---

## Scope and accuracy

Eulix is a code navigation and retrieval layer. **It is not a compiler**, and PRISM is not a formal whole-program analysis system — it deliberately trades formal soundness for the speed and scalability required at repository scale.

Relationship resolution is best-effort in areas that are inherently difficult to resolve statically: dynamic dispatch, reflection and indirect calls, generated code, language-specific metaprogramming, and parser coverage gaps in less-common syntax.

Retrieval quality also depends on query specificity, repository structure and indexing configuration, the embedding model, context limits, and language semantics.

Known constraints are tracked in [Known Issues](docs/known-issues.md).

### Supported languages

| Language   | Status      | Notes                                                      |
| ---------- | ----------- | ---------------------------------------------------------- |
| C          | Supported\* | Limited by macro expansion                                 |
| C++        | Supported\* | Limited by macro expansion                                 |
| Go         | Supported   | Validated on kubernetes and gin-gonic                      |
| Python     | Supported   | Validated on Django and Openstacks                         |
| Rust       | Supported   | Validated on RipGrep and Zed                               |
| TypeScript | Supported   | Validated on Deno. Can't parse tsx files                   |
| JavaScript | Supported   | Validated on Express.js and Three.js. Cant parse jsx files |
| Java       | Supported   | Validated on Tomcat and OpenJDK                            |

\* Parsing may not reach 100% accuracy for C and C++ because macros are not expanded. JSX and TSX files are not currently parsed.

---

## Roadmap

**Shipped**

- `vizEulize` — interactive call-graph visualization
- `eulix mcp` — MCP server exposing Eulix retrieval to coding agents
- `libeulix` — analysis engine extracted into a standalone, reusable crate
- `eulix-lsp` — multi-language language server built on libeulix (beta)
- JavaScript and Java validated on Tomcat, OpenJDK, Express.js, and Three.js

**Next**

- Additional repository-scale benchmarks
- Architecture-aware documentation generation
- Stronger embedding models for difficult identifiers and deep semantic queries
- Expanded user studies and hard query sets

---

## Documentation

**Core:** [About Eulix](docs/About-Eulix.md) · [Installation](docs/installation.md) · [Model Selection](docs/models-to-use.md)

**Architecture:** [System Overview](docs/architecture/01-system-overview.md) · [Query Pipeline](docs/architecture/03-query-pipeline.md) · [Parser Internals](docs/architecture/07-parser-internals.md) · [Context Builder](docs/architecture/08-context-builder.md) · [Cache Architecture](docs/architecture/05-cache-architecture.md) · [Embedding Pipeline](docs/eulix-embed/architecture.md)

**Research / internals:** [Classifier](docs/architecture/06-classifier.md) · [Query Package](docs/query-package.md) · [Parser Architecture](docs/eulix-parser/Architecture.md)

**Components:** [CLI](eulix-cli/README.md) · [MCP server](eulix-cli/internal/mcp/README.md) · [Parser](eulix-parser/README.md) · [LSP](eulix-lsp/README.md) · [vizEulize](vizEulize/README.md)

**Project health:** [Known Issues](docs/known-issues.md)

> Documentation is being updated to reflect the latest architecture, and some internal design documents describe earlier versions of Eulix. See [#64](https://github.com/Nurysso/eulix/issues/64) for the ongoing refresh. In the meantime, browse auto-generated docs on [DeepWiki](https://deepwiki.com/Nurysso/eulix).

---

## Contributing

Eulix is under active development. Issues, benchmarks, documentation improvements, language support, parser work, retrieval experiments, and difficult real-world test cases are all welcome.

> **Working in a large or particularly complex codebase? [Open an issue](https://github.com/Nurysso/eulix/issues) with a query that's hard to answer — those cases drive the retrieval roadmap.**

For non-trivial changes, please **open an issue first** so the approach can be discussed before implementation.

### Repository layout

| Directory       | Component                                                 | Language |
| --------------- | --------------------------------------------------------- | -------- |
| `eulix-cli/`    | CLI, routing, retrieval, LLM layer (includes `eulix mcp`) | Go       |
| `eulix-embed/`  | Embedding pipeline                                        | Python   |
| `eulix-parser/` | Parser CLI built on libeulix                              | Rust     |
| `libeulix/`     | Analysis engine (PRISM) — reusable in other programs      | Rust     |
| `eulix-lsp/`    | Multi-language language server (beta)                     | Rust     |
| `vizEulize/`    | Call-graph explorer (single-file web app)                 | HTML/JS  |

All components are licensed under Apache 2.0.

### Local setup

```bash
git clone https://github.com/Nurysso/eulix.git
cd eulix

bash build.sh       # build all components

eulix version
eulix verifyBins
```

### Tests, formatting, and linting

```bash
# Rust: libeulix, eulix-parser, eulix-lsp (run in each directory)
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

1. Open an issue describing the change, or a hard query you'd like Eulix to handle.
2. Fork the repository and create a feature branch.
3. Format and lint the component you changed, and run its tests.
4. Open a pull request that links the issue.

---

## License & acknowledgments

Eulix is released under the [Apache License 2.0](./LICENSE). This applies to every component in the repository: the CLI and MCP server, parser, `libeulix`, embedding pipeline, language server, and `vizEulize`.

Eulix builds on excellent open-source work:

- [Tree-sitter](https://tree-sitter.github.io/) — the parsing foundation of `libeulix`
- [PyTorch](https://pytorch.org/) and [ONNX Runtime](https://onnxruntime.ai/) — embedding backends
- [BAAI/bge-base-en-v1.5](https://huggingface.co/BAAI/bge-base-en-v1.5) — default embedding model
- [Ollama](https://ollama.com) — local LLM provider
- [DeepWiki](https://deepwiki.com/Nurysso/eulix) — auto-generated project documentation
- [tower-lsp](https://github.com/ebkalderon/tower-lsp) — LSP protocol implementation for `eulix-lsp`
- [go-sdk](https://github.com/modelcontextprotocol/go-sdk) — Model Context Protocol SDK for Go, used by `eulix mcp`

> [!NOTE]
> Eulix is developed and stress-tested against large, real-world open-source repositories: the **Linux kernel**, the **Go compiler**, **FFmpeg**, **Gecko / Firefox**, and **Django**. Thanks to the maintainers of these projects for building codebases worth studying.

---

<div align="center">

**Eulix** — _Understand the code you didn't write._

</div>
