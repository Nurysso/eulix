# Architecture: System Overview

> **Purpose:** Shows the three-binary architecture at a glance — who owns what,
> which language each component is written in, and how the pieces fit together from
> the user's perspective. Start here if you're new to the codebase.
>
> **Limitation:** This is a static, conceptual view. It does not show data flow,
> message formats, or the runtime interaction sequences between components.
> See [02-data-pipeline.md](./02-data-pipeline.md) and [03-query-pipeline.md](./03-query-pipeline.md)
> for those details.

---

## Diagram

```mermaid
graph TB
    subgraph USER["👤 User"]
        Terminal["Terminal / Shell"]
    end

    subgraph CLI["eulix  ·  Go 1.23  ·  Cobra + Bubbletea"]
        direction TB
        Init["eulix init"]
        Analyze["eulix analyze"]
        Chat["eulix chat"]
        History["eulix history"]
        Cache["eulix cache *"]
        Glados["eulix glados"]
    end

    subgraph Parser["eulix_parser  ·  Rust  ·  Rayon"]
        direction TB
        PyParser["Python parser"]
        GoParser["Go parser"]
        CParser["C parser"]
        Analyzer["Call-graph analyzer"]
    end

    subgraph Embedder["eulix_embed  ·  Rust  ·  ONNX Runtime"]
        direction TB
        Chunker["Chunker"]
        EmbGen["Embedding generator"]
        VecStore["Vector store"]
        CtxIdx["Context index builder"]
    end

    subgraph Store[".eulix/  ·  Local files"]
        direction TB
        KB["kb.json\nknowledge base"]
        CallGraph["kb_call_graph.json"]
        Index["kb_index.json"]
        EmbBin["embeddings.bin"]
        EmbJson["embeddings.json"]
        VecBin["vectors.bin"]
        HistDB["history.db  (SQLite)"]
    end

    subgraph LLM["LLM Backend"]
        Ollama["Ollama  (local)"]
        Anthropic["Anthropic Claude  (remote)"]
    end

    Terminal -->|"eulix <cmd>"| CLI
    Analyze -->|"subprocess"| Parser
    Analyze -->|"subprocess"| Embedder
    Parser -->|"writes"| KB
    Parser -->|"writes"| CallGraph
    Parser -->|"writes"| Index
    Embedder -->|"reads"| KB
    Embedder -->|"writes"| EmbBin
    Embedder -->|"writes"| EmbJson
    Embedder -->|"writes"| VecBin
    Chat -->|"reads"| Store
    Chat -->|"HTTP"| LLM
    History -->|"reads"| HistDB
    Cache -->|"reads/writes"| HistDB
```

---

## Component Responsibilities

| Component | Language | Key responsibility |
|-----------|----------|--------------------|
| `eulix` CLI | Go | User interface, orchestration, config, caching, TUI |
| `eulix_parser` | Rust | Static analysis — symbols, call graphs, complexity |
| `eulix_embed` | Rust | Vector generation — ONNX models, binary/JSON output |
| `.eulix/` dir | — | All persistent state lives here (no server required) |
| LLM backend | — | Natural-language generation only; never sees raw source |

---

## Design Decisions & Trade-offs

| Decision | Rationale | Trade-off |
|----------|-----------|-----------|
| Three separate binaries | Independent release, language-appropriate tooling | More complex build; subprocess IPC instead of in-process calls |
| All state in local files | Zero infrastructure — works offline | No multi-user sharing; large codebases produce large JSON files |
| LLM only sees AST/semantic data | Prevents leaking raw source to remote APIs | LLM cannot see implementation logic; answers are structural only |
| Rust for parser + embedder | Performance — 9M LOC/40s parsing; ONNX GPU acceleration | Requires Rust toolchain on top of Go |
