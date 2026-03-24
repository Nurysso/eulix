# Architecture

Eulix Embed is the embedding pipeline component of Eulix — a codebase understanding tool. It takes a structured knowledge base (JSON) produced by the Eulix parser, converts it into semantic vector embeddings, and writes a set of output artifacts that power similarity search and context retrieval.

---

## Module Overview

```
eulix_embed/
├── main.rs          — CLI entrypoint, pipeline orchestration
├── kb_loader.rs     — Knowledge base deserialization
├── chunker.rs       — KB → Chunk conversion
├── embedder.rs      — Embedding generation (backend abstraction)
├── onnx_backend.rs  — ONNX Runtime inference (CUDA / ROCm / CPU)
├── context.rs       — Context index + vector store
└── index.rs         — EmbeddingIndex (JSON + binary I/O)
```

---

## Data Flow

```
knowledge_base.json
       │
       ▼
  kb_loader.rs       — Deserializes into KnowledgeBase struct
       │
       ▼
  chunker.rs         — Produces Vec<Chunk> at 4 granularity levels
       │              (EntryPoint → Function → Class/Method → File)
       ▼
  embedder.rs        — Dispatches chunks to the selected backend
       │
       ▼
  onnx_backend.rs    — Tokenizes text, runs ONNX inference,
       │               applies mean pooling + L2 normalization
       ▼
  VectorStore        — In-memory id → Vec<f32> map
       │
       ├──▶ index.rs        → EmbeddingIndex  → embeddings.json / embeddings.bin
       ├──▶ context.rs      → ContextIndex    → context.json
       └──▶ context.rs      → VectorStore     → vectors.bin
```

---

## Module Responsibilities

### `kb_loader.rs`

Deserializes the JSON knowledge base produced by the Eulix parser into a rich type hierarchy:

| Type              | Purpose                                                               |
| ----------------- | --------------------------------------------------------------------- |
| `KnowledgeBase`   | Root struct — owns all other data                                     |
| `FileStructure`   | Per-file: imports, functions, classes, globals                        |
| `Function`        | Full function metadata including call graph, control flow, exceptions |
| `Class`           | Class with attributes and method list                                 |
| `CallGraph`       | Directed edges between functions                                      |
| `DependencyGraph` | File-level import relationships                                       |
| `EntryPoint`      | API endpoints, CLI commands, `main` functions                         |
| `Patterns`        | Architecture style, naming conventions                                |

Helper methods on `KnowledgeBase` provide convenient traversals: `all_functions()`, `get_function(id)`, `get_calls_from(id)`, `entry_points_by_type()`, etc.

---

### `chunker.rs`

Converts a `KnowledgeBase` into a flat `Vec<Chunk>`, applying a priority ordering:

1. **EntryPoint** (`importance_score = 1.0`) — functions identified as API routes, CLI handlers, or `main`
2. **Function** (`importance_score` from KB) — all other top-level functions
3. **Class + Method** — class overview chunks plus per-method chunks with class context injected
4. **File** (`importance_score = 0.5`) — file-level summaries (imports, function/class lists)

Each `Chunk` carries:

- `id` — stable identifier (function ID or `file:<path>`)
- `chunk_type` — `EntryPoint | Function | Class | Method | File | Other`
- `content` — formatted text sent to the embedder (truncated to `max_size`, default 2000 chars)
- `metadata` — file path, language, line range, complexity
- `tags` — derived from function properties (`async`, `complex`, `api`, `test`, ...)
- `importance_score` — used downstream for context window ranking

**Truncation strategy:** content is cut at the last newline boundary before the limit to avoid mid-line breaks.

---

### `embedder.rs`

Provides `EmbeddingGenerator`, which abstracts over backends:

| Backend    | Trigger                                    |
| ---------- | ------------------------------------------ |
| `OnnxCuda` | CUDA env vars / paths / `nvidia-smi` found |
| `OnnxRocm` | ROCm env vars / paths / `rocm-smi` found   |
| `OnnxCpu`  | Default fallback                           |
| `Dummy`    | Testing — hash-based deterministic vectors |

Backend selection is done at construction via `EmbeddingBackend::auto_detect()` or explicit `--backend` flag.

`generate_vectors()` processes chunks sequentially in configurable batches (128 for GPU, 32 for CPU). A parallel variant `generate_vectors_parallel()` uses Rayon for CPU-bound workloads.

---

### `onnx_backend.rs`

Handles the full ONNX inference path:

1. **Model download** — pulls `onnx/model.onnx` from HuggingFace Hub (cached via `hf_hub`)
2. **Tokenizer** — loads `tokenizer.json` via the `tokenizers` crate; truncates to 512 tokens
3. **Model type detection** — MPNet models skip `token_type_ids`; all others use the standard BERT input triple
4. **Inference** — runs via `ort` (ONNX Runtime); extracts `last_hidden_state` output
5. **Mean pooling** — attention-mask-weighted average over sequence dimension
6. **L2 normalization** — applied when `normalize = true` (default)
7. **Dynamic dimension** — actual hidden dimension is read from the model output on first inference and stored in an `AtomicUsize`, overriding the config estimate

Batch inference (`generate_embeddings_batch`) pads sequences to the batch maximum and processes all items in a single ONNX call.

---

### `context.rs`

Builds two structures:

**`ContextIndex`** (saved as `context.json`) — a metadata-only index for LLM context assembly:

- `chunks` — all `ContextChunk` records (no embedding vectors)
- `relationships` — extracted from `CallGraph` and `DependencyGraph` edges
- `tags` — inverted index: tag name → list of chunk IDs
- `entry_points` — flattened list of entry point info
- `call_graph_summary` — node/edge counts and max BFS depth from entry points

Query-time helpers: `get_by_tag()`, `get_related()`, `get_referencing_chunks()`, `build_context_window()` (token-budget-aware context assembly).

**`VectorStore`** — a lightweight `HashMap<String, Vec<f32>>` with binary serialization. Saved as `vectors.bin` with a fixed header: `[version: u32, count: u64, dimension: u32]` followed by `[id_len: u32, id_bytes, floats...]` per entry.

---

### `index.rs`

`EmbeddingIndex` stores the full embedding record (id + chunk_type + content + metadata + vector) and supports:

- **JSON** — `embeddings.json` via `serde_json` (human-readable, larger)
- **Binary** — `embeddings.bin` with magic bytes `EULX`, version field (v1/v2), model name, and raw `f32` vectors in little-endian

The `compare` subcommand does a field-by-field consistency check between a JSON and binary index file.

Search is cosine similarity over all stored vectors (`search()` and `search_filtered()`).

---

## CLI Interface

```
eulix_embed [COMMAND] [OPTIONS]

Commands:
  embed     Generate embeddings for a knowledge base (default)
  query     Embed a single query string
  compare   Validate consistency between .json and .bin index files
```

**embed:**

```
-k / --kb-path    Path to knowledge_base.json  (default: knowledge_base.json)
-o / --output     Output directory             (default: ./embeddings)
-m / --model      HuggingFace model name
```

**query:**

```
-q / --query      Query text
-m / --model      HuggingFace model name
-f / --format     json (default) | binary
```

---

## Supported Models

| Model                                    | Dimension | Notes                              |
| ---------------------------------------- | --------- | ---------------------------------- |
| `sentence-transformers/all-MiniLM-L6-v2` | 384       | Fast, good for development         |
| `BAAI/bge-small-en-v1.5`                 | 384       | Better quality                     |
| `BAAI/bge-base-en-v1.5`                  | 768       | High quality                       |
| Any ONNX-exported sentence transformer   | varies    | Dimension auto-detected at runtime |

---

## Key Design Decisions

- **No embeddings in `ContextIndex`** — vectors are kept separate so the context index stays lightweight and JSON-serializable for LLM prompt assembly without loading multi-MB float arrays.
- **Dual output format** — JSON for debuggability and tooling compatibility; binary for fast loading in production.
- **Graceful backend fallback** — ONNX failures downgrade to `DummyBackend` with a clear warning rather than crashing, so the pipeline remains testable without GPU/network.
- **Dynamic dimension correction** — the config dimension is an initial estimate; the actual value is read from the model output on first inference and propagated via `AtomicUsize`.
- **Chunk priority ordering** — entry points always appear first in the chunk list, ensuring they're embedded and indexed regardless of any early truncation in downstream consumers.
