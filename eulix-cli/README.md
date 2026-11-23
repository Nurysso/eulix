# eulix-cli

> The `eulix` binary: indexes your repository, routes every question, and serves ranked context to AI agents over MCP.

| Language | Version | Status | Docs |
| :-- | :-- | :-- | :-- |
| Go 1.26 | 0.9.0 | Stable; MCP server and chat screen in beta | [docs/eulix-cli](../docs/eulix-cli/Overview.md) |

`eulix` is the program you install and run. It carries [`eulix_parser`](../eulix-parser/) and [`eulix_embed`](../eulix-embed/) inside it, runs them during `eulix analyze`, and answers questions through one of two retrieval engines. It assembles context within a token budget and hands it to an AI agent over MCP, to a language model in the terminal chat, or straight to your terminal.

## Features

- **Two retrieval engines.** `etiya` (default) combines exact, keyword, call-graph and vector search. `ehnas` uses no vectors at all: exact matching plus twin search, a Monte Carlo walk and novelty packing, so its scoring can be recomputed. Choose one with `project.engine`. See [Engines](../docs/eulix-cli/query/retrieval/Engines.md).
- **MCP server.** `eulix mcp` serves retrieval to AI coding agents over the Model Context Protocol. See [MCP](../docs/eulix-cli/mcp/Overview.md) and the [package README](internal/mcp/README.md).
- **Maximal Marginal Relevance selection.** Picks a relevant, non-redundant set of chunks under the token budget.
- **Source hydration.** Balances real source code against compact signatures using `code_to_ast_ratio`, and strips comments to save tokens.
- **Interactive terminal chat.** A full-screen interface built on Bubble Tea, with a Markdown renderer (headings, lists, code blocks, tables), collapsible reasoning blocks, a scrollable transcript and a history browser. Replies appear when the model has finished; they are not streamed. See [TUI](../docs/eulix-cli/tui/Overview.md).
- **Universal LLM client.** Local models (Ollama, LM Studio, vLLM) and hosted providers (Anthropic, OpenAI, Google Gemini, DeepSeek, Groq, Mistral, Together, OpenRouter, Fireworks), plus any OpenAI-compatible server through `baseURL`.
- **Call-graph explorer.** `eulix vizeulize` opens the graph in your browser. See [vizEulize](../vizEulize/).
- **Change detection.** A fast checksum of file sizes and modification times decides whether `analyze` has anything to do, without reading file contents.

## Install

The install script is the easy way; see [Installation](../docs/Installation.md):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh)
```

### Build from source

`eulix` embeds the parser binary and the Python embedding scripts with `go:embed`, so build those first. `../build.sh` (or `make build-single` from the repository root) does every step; the manual steps follow.

**1. Build the parser** (in `eulix-parser/`):

```bash
cargo build --release
```

For host-native CPU extensions, add `RUSTFLAGS="-C target-cpu=native"`. Such a binary may not run on other machines.

**2. Package the embedding scripts**, excluding virtual environments and caches:

```bash
zip -r eulix-embed.zip eulix-embed/ -x "*/.venv/*" "*/.mypy_cache/*" "*/.git/*" "*/__pycache__/*" "*.pyc" ".codespell-ignore"
```

**3. Stage both in `internal/assets/bins/`:**

```text
internal/assets/bins/
├── eulix_parser_darwin       # macOS (Mach-O), embedded via assets_mac.go
├── eulix_parser_linux        # Linux (ELF), embedded via assets_linux.go
├── eulix_parser_windows.exe  # Windows (PE), embedded via assets_windows.go
└── eulix-embed.zip           # OS-agnostic archive of the Python scripts
```

**4. Compile**, passing the parser's hash so `eulix` can verify it at run time:

```bash
PARSER_HASH=$(sha256sum internal/assets/bins/eulix_parser_linux | awk '{print $1}')

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags="-s -w -X 'eulix/internal/assets.embed_requirements=onnx-amd.txt' -X 'eulix/internal/assets.ParserHash=${PARSER_HASH}'" \
  -o eulix ./cmd/eulix/main.go
```

Set `GOOS` (`darwin`, `linux`, `windows`) and `GOARCH` (`amd64`, `arm64`) for your target, and pick `onnx-nvidia.txt` or `onnx-amd.txt` for the embedder's requirements.

## Usage

```text
eulix [command] [flags]
```

### Core commands

| Command | Arguments | Description |
| :-- | :-- | :-- |
| `init` | `fix`, `force` | Create `.eulix/`, `.euignore` and `eulix.toml` with an interactive wizard. `fix` creates only what is missing; `force` rewrites everything except `.env`. |
| `analyze` | | Run `eulix_parser`, and `eulix_embed` when the engine is ETIYA, but only when files changed or settings differ from the last complete run. |
| `chat` | | Open the interactive terminal interface. Refuses to start if the code changed by more than 30% since the last analysis, and asks first above 10%. |
| `query` | `<question>` | Answer factual questions (locations, usage, call graphs, entry points, dependencies, metrics, TODOs, file structure) directly, and print the retrieved context and reasoning instructions for everything else. It does **not** call a language model. |
| `mcp` | `[path]` | Start the MCP server on standard input and output. Flags: `--repo`, `--timeout`, `--cot`. Beta. |
| `history` | `list`, `show <id>`, `view`, `delete <id>`, `clear` | Browse recorded questions and answers. `--no-tui` prints a plain list. `cache` is deprecated and only prints a notice. |
| `vizeulize` | `--graph <path>` | Open the call-graph explorer in your browser. |
| `version` | | Show the versions of `eulix`, `eulix_parser` and `eulix_embed`. |

Placeholders: `config` prints "coming soon", and `checksum` is deprecated and prints a notice.

### Component management

| Command | Arguments | Description |
| :-- | :-- | :-- |
| `getEmbedDeps` | | Install the Python dependencies for `eulix_embed` into `~/.Eulix/.venv` with `uv`. |
| `printEmbedDeps` | | Print the dependency list for this version. |
| `verifyBins` | | Check the extracted parser against its build-time hash; prints nothing if it is correct. |
| `embed` | `[args...]` | Pass-through to `eulix_embed` inside the managed environment. |
| `parser` | `[args...]` | Pass-through to the embedded `eulix_parser`. |

## Configuration

`eulix init` writes `eulix.toml`. Every key is optional; missing values take the defaults shown, and numbers outside their valid range are clamped silently. API keys should come from environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` and so on, or `LLM_API_KEY`) or from a `.env` file in the project, not from this file. Full reference: [Config](../docs/eulix-cli/Config.md).

```toml
[project]
  path = "/home/user/foo"       # Absolute path of the repository. Update it if you move the project.
  engine = "etiya"              # Retrieval engine: "etiya" (default, uses vectors) or "ehnas" (no vectors)
  Max_Lines = 100               # Longest span of real source shown for one chunk before it becomes a skeleton
  DebugConfig = false           # Write traces and logs to .eulix/debug/

[parser]
  threads = 4                   # Worker threads for eulix_parser
  prismVersion = 2              # PRISM call graph: 1 (fast, name-based) or 2 (scope-aware)
  verbose = true                # Pass --verbose to the parser

[embeddings]                    # Used by the etiya engine only
  model = "BAAI/bge-base-en-v1.5" # Embedding model
  dimension = 768               # Vector size
  engine = "onnx"               # "onnx" or "torch"; must match between analyze and querying
  debug = false                 # Stream the embedder's log output

[llm]
  local = true                  # With a blank provider, true means Ollama
  provider = "ollama"           # ollama, openai, anthropic, gemini, deepseek, groq, mistral, together, openrouter, fireworks, lm-studio, vllm
  model = "llama3.2:3b"         # Model name (required)
  api_key = ""                  # Prefer environment variables or .env
  max_tokens = 8192             # Total budget for context, question and reply
  temperature = 0.2             # 0.0 to 2.0
  baseURL = "http://localhost:11434" # Override the provider's address
  endpoint = ""                 # Override the request path

[retrievalConfig]
  code_to_ast_ratio = 0.85      # Share of the context budget spent on real code
  top_k_candidates = 150        # Base candidate count; scaled up for big repositories
  max_context_chunks = 30       # Most chunks in the final context
  mmr_diversity_factor = 0.85   # 1.0 = pure relevance, 0.0 = maximum diversity
  pre_mmr_score_floor_ratio = 0.05 # Drop candidates scoring below this share of the best
  semantic_min_similarity = 0.15 # Smallest cosine similarity for a vector hit (etiya)
  max_graph_expansion_depth = 1 # Call-graph hops: 0 = off, 1 = direct, 2+ = transitive
  max_graph_expansions = 15     # Most call relationships followed
  max_exact_anchors = 2         # Most exact symbol matches pinned
  enable_subsystem_boosting = true # Favour directories the question points at
  apply_cross_root_isolation = true # Demote code outside the main module (see note below)
  cross_root_penalty = 0.32     # Score multiplier for that code
  test_file_penalty = 0.30      # Score multiplier for test files when the question is not about tests

  [retrievalConfig.etiya]       # Optional per-engine overrides (a missing key inherits the value above)
    # pre_mmr_score_floor_ratio, mmr_diversity_factor, max_context_chunks, semantic_min_similarity
    # lexical_exclude = ["backup/", "*.min.js"]  # paths the MCP text-search fallback skips

  [retrievalConfig.ehnas]
    # pre_mmr_score_floor_ratio, mmr_diversity_factor, max_context_chunks (overrides)
    mmr_stop_floor = 0.0
    novelty_gain = 0.85
    mc_seed_min_score = 60
    mc_walkers = 256
    mc_seed_cap = 16
    common_symbol_df = 200
    inverted_index_min_chunks = 5000

[cache]
  enable = true                 # Keep a query history
  path = ".eulix/history.db"    # An embedded bbolt database

[checksum]
  change_threshold = 0.10       # Read but not used yet: `chat` uses fixed limits of 10% and 30%
  force_reanalyze_threshold = 0.30
```

Cross-root isolation currently applies only to files whose top-level folder starts with `charm-` or is named `requirements`. For other layouts the two `cross_root` settings have no effect.

## How it works

A question is classified first. Factual questions are answered straight from the knowledge base; the rest go through retrieval:

```text
question
   │
   ▼
anchors and path gate      explicit references such as file.go:42 or a/b/c
   │
   ▼
intent and token budget    callers, callees, debug, flow, concept, exact symbol
   │
   ▼
engine search              etiya: grep, exact, partial, keyword, semantic (vectors)
                           ehnas: the same core, plus twin search and a Monte Carlo walk
   │
   ▼
subsystem boost, test-file and scope demotion, pre-selection score floor
   │
   ▼
call-graph expansion       callers and callees of the best candidates
   │
   ▼
selection                  etiya: query-aware MMR    ehnas: novelty packing + structural MMR
   │
   ▼
source hydration           real code in place of signatures, within the code budget
   │
   ▼
prompt                     context + reasoning instructions and an honesty contract
```

Details: [Retrieval Overview](../docs/eulix-cli/query/retrieval/Overview.md), [Search Pipeline](../docs/eulix-cli/query/retrieval/Search-Pipeline.md), [Selection And Hydration](../docs/eulix-cli/query/retrieval/Selection-And-Hydration.md). The EHNAS design is also described in [`internal/query/retrieval/README.md`](internal/query/retrieval/README.md).

## Development

```bash
gofmt -l .
go vet ./...
go test ./...
```

## Related components

| Component | Language | Role |
| :-- | :-- | :-- |
| [libeulix](../libeulix/) | Rust | Analysis engine: parsing, PRISM call graphs, indexes |
| [eulix-parser](../eulix-parser/) | Rust | CLI that runs libeulix over a repository and writes `.eulix/` |
| [eulix-embed](../eulix-embed/) | Python | Embedding pipeline for the ETIYA engine |
| [eulix-cli](../eulix-cli/) | Go | The `eulix` binary: retrieval engines, MCP server, chat |
| [eulix-lsp](../eulix-lsp/) | Rust | Language server built on libeulix (beta) |
| [vizEulize](../vizEulize/) | HTML/JS | Call-graph explorer in your browser |

## License

Apache 2.0, like every Eulix component. See [LICENSE](../LICENSE).
