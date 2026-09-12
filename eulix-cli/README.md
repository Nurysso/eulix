# Eulix CLI (`eulix`)

The primary command-line orchestrator and terminal interface for the Eulix code intelligence platform. Written in Go, it coordinates static analysis (`eulix_parser`), semantic embedding generation (`eulix_embed`), multi-tier hybrid retrieval, context window assembly, and LLM inference.

---

## Features

- **Interactive Terminal UI (TUI)**: Rich interactive chat powered by Bubble Tea, featuring markdown rendering, streaming responses, query history navigation, and model selection.
- **4-Tier Hybrid Retrieval Pipeline**:
  1. **Exact Symbol Lookup**: Inverted AST index for function, class, and type definitions.
  2. **BM25 Keyword Search**: Term-frequency and inverse-document-frequency search across identifiers, docstrings, and comments.
  3. **IVF Vector Similarity Search**: Fast Inverted File (IVF) centroid search over memory-mapped `vectors.bin` and `embeddings.bin`.
  4. **Call Graph Traversal**: Inter-procedural call-tree expansion (callers, callees, and transitive dependencies) derived from the PRISM call graph.
- **Maximal Marginal Relevance (MMR) Re-Ranking**: Configurable diversity vs. relevance re-ranking ($\lambda$) to eliminate redundant snippets from the context window.
- **AST-Aware Source Code Hydration**: Dynamically balances the prompt context window between full real source code and compact AST structural signatures based on `code_to_ast_ratio`.
- **Universal LLM Client**: Built-in support for local inference (Ollama, LM Studio, vLLM) and cloud providers (Anthropic, OpenAI, Google Gemini, DeepSeek, Groq, Mistral, Together, OpenRouter, Fireworks).
- **Codebase Checksum & Delta Engine**: Fast hashing and difference tracking to detect codebase changes without running a full re-analysis.
- **Self-Healing & Diagnostics**: `glados` for knowledge base integrity validation, and `aspirine` for embedding recovery.

---

## Installation & Build

### Prerequisites

- **Go 1.26**

### Building from Source

eulix embeds platform-specific binary dependencies and Python embedding assets directly into the Go executable at build time using `go:embed`.

#### Prerequisites

Before building the main Go binary, you must generate two dependencies:

1. `eulix_parser` (Rust binary) [README](../eulix-parser/README.md)
2. `eulix-embed.zip` (Bundled Python runtime scripts) [README](../eulix-embed/README.md)

> **Quick Build:** You can automatically build and package these assets by running `../build.sh` or follow the manual build steps below.

---

#### Step 1: Manual Dependency Setup

##### 1. Build the Rust Parser

Build the release binary for `eulix_parser`:

```bash
cargo build --release
```

_Optional — Native CPU Optimization:_
To build with host-native vector and CPU instruction set extensions:

```bash
RUSTFLAGS="-C target-cpu=native" cargo build --release
```

> **Warning:** Binaries compiled with `target-cpu=native` are optimized specifically for the host architecture and may not execute on other machine configurations.

##### 2. Package the Embedding Assets

Archive the `eulix-embed` directory while excluding virtual environments and build caches:

```bash
zip -r eulix-embed.zip eulix-embed/ -x "*/.venv/*" "*/.mypy_cache/*" "*/.git/*" "*/__pycache__/*" "*.pyc" ".codespell-ignore"
```

##### 3. Stage Embedded Assets

Move the compiled artifacts into `internal/assets/bins/`. The target directory structure must match the following layout:

```text
internal/assets/bins/
├── eulix_parser_darwin       # macOS (Mach-O), embedded via embed_darwin.go
├── eulix_parser_linux        # Linux (ELF), embedded via embed_linux.go
├── eulix_parser_windows.exe  # Windows (PE), embedded via embed_windows.go
└── eulix-embed.zip           # OS-agnostic archive of Python scripts

```

---

#### Step 2: Compile the Go Binary

Set `GOOS` and `GOARCH` to match your target environment, calculate the parser hash, and compile with stripped debug symbols:

```bash
# Calculate SHA-256 hash of the target parser binary (example for Linux)
PARSER_HASH=$(sha256sum internal/assets/bins/eulix_parser_linux | awk '{print $1}')

# Build the main Go executable
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=onnx-amd.txt' -X 'eulix/internal/assets.ParserHash=${PARSER_HASH}'" \
  -o eulix ./cmd/eulix/main.go

```

> **Note:** Adjust `GOOS` (`darwin`, `linux`, `windows`) and `GOARCH` (`amd64`, `arm64`) according to your deployment platform.

---

## CLI Reference

```text
eulix [command] [flags]
```

### Core Commands

| Command    | Arguments                           | Description                                                                                      |
| ---------- | ----------------------------------- | ------------------------------------------------------------------------------------------------ |
| `init`     | `[--force]`                         | Initialize Eulix in the current directory, generating default `eulix.toml` and `.euignore`.      |
| `analyze`  |                                     | Run the complete analysis pipeline (`eulix_parser` + `eulix_embed`) to build the knowledge base. |
| `chat`     |                                     | Launch the interactive Terminal UI (TUI) for asking questions about your codebase.               |
| `query`    | `<question>`                        | One-shot query interface. Builds the retrieval context and queries the configured LLM directly.  |
| `checksum` |                                     | Compute or update codebase checksums (`.eulix/checksum.json.zst`) and display change ratios.     |
| `history`  | `[list \| show \| clear \| search]` | Browse, search, or inspect previous queries and answers from the local cache.                    |
| `config`   |                                     | Inspect or manage Eulix configuration values.                                                    |

### Component Management & Diagnostics

| Command          | Arguments   | Description                                                                                    |
| ---------------- | ----------- | ---------------------------------------------------------------------------------------------- |
| `getEmbedDeps`   |             | Install required Python dependencies for `eulix_embed` into `~/.Eulix/.venv` via `uv`.         |
| `printEmbedDeps` |             | Print the required Python dependency list for this version of Eulix.                           |
| `verifyBins`     |             | Check cryptographic hashes of managed `eulix_parser` and `eulix_embed` binaries.               |
| `version`        |             | Display the version of `eulix`, `eulix_parser`, and `eulix_embed`.                             |
| `embed`          | `[args...]` | Passthrough execution wrapper for running `eulix_embed` inside the managed Python environment. |
| `glados`         |             | Run automated diagnostic health checks on `.eulix/kb.json` and binary embedding files.         |
| `aspirine`       |             | Diagnostic recovery fixer: attempts to repair corrupted `embeddings.bin` and vector indices.   |

---

## Configuration Reference (`eulix.toml`)

When `eulix init` runs, it generates `eulix.toml`. All options fall back to sensible defaults if omitted.

```toml
[project]
  path = "."                    # Root path of project to analyze
  Max_Lines = 100               # Max lines of real code hydrated per chunk
  DebugConfig = false           # Enable writing detailed query traces to .eulix/debug/

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
  api_key = ""                  # API key (or set via environment variable)
  max_tokens = 8192             # Maximum tokens for context + generation
  temperature = 0.7             # Sampling temperature (0.0 to 2.0)
  baseURL = "http://localhost:11434" # Base URL for API calls
  endpoint = ""                 # Optional custom completion endpoint

[retrievalConfig]
  code_to_ast_ratio = 0.85      # Fraction of token budget reserved for real code vs AST metadata
  apply_cross_root_isolation = true # Demote candidates outside detected primary module/subsystem
  cross_root_penalty = 0.32     # Score multiplier for candidates outside the primary root
  pre_mmr_score_floor_ratio = 0.05 # Prune candidates scoring < 5% of top candidate before MMR
  top_k_candidates = 150        # Base candidate limit gathered across search strategies
  mmr_diversity_factor = 0.65   # MMR lambda: 1.0 = pure relevance, 0.0 = maximum diversity
  max_graph_expansion_depth = 1 # Call graph hops: 0 = disabled, 1 = direct, 2+ = transitive
  semantic_min_similarity = 0.15 # Minimum cosine similarity threshold for vector search hits
  max_graph_expansions = 15     # Max call-graph relationship edges to expand
  max_exact_anchors = 2         # Max exact symbol matches to pin into context
  test_file_penalty = 0.30      # Score multiplier for test files when query is non-testing
  enable_subsystem_boosting = true # Boost chunks belonging to query-matched directory paths
  max_context_chunks = 30       # Maximum chunks to include in the assembled prompt context

[cache]
  enable = true                 # Enable query history caching in SQLite
  path = ".eulix/history.db"    # Path to history database

[checksum]
  change_threshold = 0.10       # Codebase change ratio triggering incremental update
  force_reanalyze_threshold = 0.30 # Codebase change ratio forcing full re-analysis
```

---

## Retrieval Pipeline Architecture

```
User Query: "How does the token authenticator validate sessions?"
                           │
                           ▼
               ┌────────────────────────┐
               │ Intent Classifier      │ ──► Concept, Flow, Callers, Callees, or Direct
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Anchor & Gate Setup    │ ──► Extract explicit file/func/line anchors,
               │                        │     build path gate to constrain search
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Multi-Strategy Search  │ ──► 7 concurrent strategies, merged by
               │                        │     max-score + multi-hit boost
               │  ┌──────────────────┐  │
               │  │ 1. KB Exact      │  │  (if knowledge base loaded)  boost +2.5
               │  │ 2. Grep Symbol   │  │  (line-level textual symbol match)  boost +3.0
               │  │ 3. Exact AST     │  │  (precise symbol name match)  boost +2.0
               │  │ 4. Partial Ident │  │  (substring identifier match)  boost +1.5
               │  │ 5. BM25/Keyword  │  │  (inverted index, k1/b tuned)  boost +2.0
               │  │ 6. Semantic IVF  │  │  (HNSW/IVF vector search)  boost +1.5
               │  │ 7. Anchors       │  │  (explicit anchor hits, pre-search)  gated
               │  └──────────────────┘  │
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Strategy Result Merge  │ ──► Same chunk seen by N strategies:
               │                        │     score = max(prev, new) + boost
               │                        │     MatchDetails concatenated; IsExact OR-ed
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Subsystem Boosting     │ ──► detectQuerySubsystems() over query tokens;
               │                        │     boostByDetectedSubsystems() scales
               │                        │     scores by detected subsystem affinity
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Test/Doc Filter        │ ──► filterTestDocChunks() drops test & doc
               │                        │     chunks unless query intent requires them
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Scope Demotion         │ ──► Test-file penalty (default ×0.30)
               │                        │     Cross-root penalty (default ×0.32)
               │                        │     Exact/grep hits are PROTECTED
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Stable Sort + Truncate │ ──► Exact hits first, then by score;
               │                        │     cut to topK (adjusted upward when
               │                        │     high-confidence anchors exist)
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Pre-MMR Score Floor    │ ──► Drops noise (< 5% of max score)
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Call Graph Expansion   │ ──► Traverses PRISM caller/callee edges (1 to N hops)
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ MMR Selection (λ=0.65) │ ──► Balances relevance vs. redundancy
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Source Code Hydration  │ ──► Replaces AST signatures with real code up to budget
               └───────────┬────────────┘
                           ▼
               ┌────────────────────────┐
               │ Prompt Assembly        │ ──► Chain-of-thought prompt with Honesty Contract
               └────────────────────────┘
```

---

## License

Eulix CLI is open-source software licensed under the [GNU General Public License v3.0](https://www.gnu.org/licenses/gpl-3.0.html).
