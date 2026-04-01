# Architecture: Go Module Structure

> **Purpose:** Shows the internal Go package dependency graph for the `eulix` CLI.
> Useful when adding a new package or refactoring an existing one — it makes
> dependency cycles and single-responsibility violations immediately visible.
>
> **Limitation:** Covers the Go CLI only. The two Rust crates (`eulix-parser`,
> `eulix-embed`) are separate workspaces and are **not** shown here. Indirect
> third-party dependencies (Cobra, Bubbletea, etc.) are omitted; only first-party
> packages are shown.

---

## Package Dependency Graph

```mermaid
graph TD
    subgraph CMD["cmd/eulix/"]
        MAIN["main.go\nentry point"]
    end

    subgraph INT["internal/"]
        subgraph CLI_PKG["cli/"]
            CMDS["commands.go\nCobra command tree"]
            ANALYZE_CMD["analyze.go\nspawns parser + embedder"]
            CHAT_CMD["chat.go\nstarts TUI chat loop"]
            INIT_CMD["init.go\ncreates .eulix/ scaffold"]
            HIST_CMD["history.go\nlaunches history TUI"]
        end

        subgraph QUERY_PKG["query/"]
            ROUTER["router.go\nQueryTrafficController\n15 handler methods"]
            CLASSIFIER["classifier.go\nQuerySheriff\n3-level classification"]
            CTX_BUILD["context_builder.go\nContextWindowCreator\n5-strategy retrieval"]
            CTX_TYPES["contextTypes.go\nChunk · ScoredChunk\nKnowledgeBase · CallGraph"]
        end

        subgraph LLM_PKG["llm/"]
            CLIENT["client.go\nMouthClient\nAnthropic · Ollama"]
        end

        subgraph CACHE_PKG["cache/"]
            MANAGER["manager.go\nCacheController\nRedis · SQLite"]
        end

        subgraph CONFIG_PKG["config/"]
            CFG["config.go\nLoad · defaultConfig\nTOML schema"]
        end

        subgraph EMBED_PKG["embeddings/"]
            EMBEDDER["embedder.go\nVectorWeaver\nspawns eulix_embed subprocess"]
        end

        subgraph TYPES_PKG["types/"]
            CONTEXT["context.go\nContextWindow · ContextChunk"]
        end

        subgraph TUI_PKG["tui/"]
            APP["app.go\nBubbletea chat model"]
            HIST_TUI["history.go\nBubbletea history model"]
        end

        subgraph CHECK_PKG["checksum/"]
            CHECKSUM["(checksum logic)\nper-file SHA hashing"]
        end

        subgraph FIX_PKG["fixers/"]
            GLADOS["GLaDOS validator"]
            ASPIRINE["Aspirine rebuilder"]
        end
    end

    MAIN --> CMDS

    CMDS --> ANALYZE_CMD & CHAT_CMD & INIT_CMD & HIST_CMD
    CMDS --> CFG & MANAGER & FIX_PKG

    ANALYZE_CMD --> CFG & EMBEDDER & CHECKSUM
    CHAT_CMD --> CFG & CLIENT & MANAGER & ROUTER & APP
    HIST_CMD --> MANAGER & HIST_TUI

    ROUTER --> CLASSIFIER & CTX_BUILD & CLIENT & CACHE_PKG
    CTX_BUILD --> EMBEDDER & CFG & LLM_PKG & TYPES_PKG & CTX_TYPES
    CLASSIFIER --> CTX_TYPES

    CLIENT --> CFG & TYPES_PKG
    MANAGER --> CFG

    APP --> ROUTER & TYPES_PKG
```

---

## Package Responsibilities

| Package | Files | Single responsibility |
|---------|-------|----------------------|
| `cmd/eulix` | `main.go` | Binary entry point; delegates to `cli` |
| `internal/cli` | 5 files | Cobra command wiring; user-facing orchestration |
| `internal/query` | 4 files | Query classification, context retrieval, LLM routing |
| `internal/llm` | 1 file | HTTP client for Anthropic and Ollama |
| `internal/cache` | 1 file | SQLite + Redis dual-backend response cache |
| `internal/config` | 1 file | TOML config loading with sane defaults |
| `internal/embeddings` | 1 file | Subprocess wrapper around `eulix_embed` binary |
| `internal/types` | 1 file | Shared `ContextWindow` / `ContextChunk` types |
| `internal/tui` | 2 files | Bubbletea models for chat and history views |
| `internal/checksum` | — | File-level SHA hashing for change detection |
| `internal/fixers` | — | `glados` validation + `aspirine` repair utilities |

---

## Key Interfaces (Implicit — Not Yet Formalised)

The following types act as implicit interfaces but are currently concrete structs.
Extracting these to Go interfaces would allow unit testing without real files/network:

```go
// Proposed: llm/provider.go
type Provider interface {
    Query(ctx *types.ContextWindow, prompt string) (string, error)
}

// Proposed: cache/store.go
type Store interface {
    Get(query, checksum string) (string, bool, error)
    Set(query, response, checksum string) error
    Close() error
}

// Proposed: embeddings/embedder.go
type QueryEmbedder interface {
    EmbedQueryBinary(query string) ([]float32, error)
}
```

---

## Dependency Direction Rules

```mermaid
graph LR
    CMD -->|"allowed"| CLI_PKG
    CLI_PKG -->|"allowed"| QUERY_PKG
    CLI_PKG -->|"allowed"| TUI_PKG
    QUERY_PKG -->|"allowed"| LLM_PKG
    QUERY_PKG -->|"allowed"| CACHE_PKG
    QUERY_PKG -->|"allowed"| EMBED_PKG
    ALL_PKG -->|"allowed"| CONFIG_PKG
    ALL_PKG -->|"allowed"| TYPES_PKG

    TYPES_PKG -->|"❌ must NOT import"| QUERY_PKG
    CONFIG_PKG -->|"❌ must NOT import"| CLI_PKG
    LLM_PKG -->|"❌ must NOT import"| QUERY_PKG
```

> The `types` and `config` packages are leaf nodes — they must not import any
> other internal package. Violating this creates import cycles.

---

## Limitations

| Limitation | Impact |
|-----------|--------|
| All packages are flat — no sub-packages within `query/` | `context_builder.go` is 1 300 lines; retrieval strategies should be split into sub-packages |
| `cli/commands.go` contains helper functions (`truncateString`, `wrapText`) | These belong in an `internal/util` package |
| No Go interfaces — all deps are concrete structs | Cannot mock LLM/cache for unit tests without real infrastructure |
| `internal/fixers` is exposed as public CLI commands | Repair utilities (`glados`, `aspirine`) should be hidden behind a `dev` subcommand |
