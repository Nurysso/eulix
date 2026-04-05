# Output Files

> This file explains the output of eulix_embed

Running `eulix_embed embed` produces four files in the output directory (default: `./embeddings/`).

---

## File Summary

| File              | Format | Contains                                         |
| ----------------- | ------ | ------------------------------------------------ |
| `embeddings.json` | JSON   | Full index: ids, content, metadata, vectors      |
| `embeddings.bin`  | Binary | Vectors only, with model name header             |
| `vectors.bin`     | Binary | Raw id → vector map                              |
| `context.json`    | JSON   | Metadata index, relationships, tags (no vectors) |

---

## `context.json` — `ContextIndex` (no vectors, LLM-readable)

```json
{
  "metadata": {
    "project_name": "my-project",
    "total_chunks": 87,
    "chunk_types": { "Function": 50, "Method": 20, "Class": 10, "File": 5, "EntryPoint": 2 },
    "created_at": "2026-04-15T10:00:00Z",
    "embedding_dimension": 384,
    "languages": ["python", "rust"],
    "architecture_style": "layered"
  },
  "chunks": [
    {
      "id": "func::src/api.py::handle_request",
      "chunk_type": "entrypoint",
      "content": "// File: src/api.py\n// Function: handle_request\n...",
      "metadata": {
        "file_path": "src/api.py",
        "language": "python",
        "line_start": 10,
        "line_end": 45,
        "name": "handle_request",
        "complexity": 5,
        "is_entry_point": true // ← extra field vs embeddings.json
      },
      "tags": ["entrypoint", "async", "api"],
      "importance_score": 1.0
    }
  ],
  "relationships": [
    {
      "from": "func::src/api.py::handle_request",
      "to": "func::src/db.py::query",
      "rel_type": "calls",
      "conditional": false
    },
    {
      "from": "src/api.py",
      "to": "src/db.py",
      "rel_type": "imports",
      "conditional": false
    }
  ],
  "tags": {
    "async": ["func::src/api.py::handle_request", "func::src/ws.py::connect"],
    "api": ["func::src/api.py::handle_request"],
    "complex": ["func::src/parser.py::parse_tree"]
  },
  "call_graph_summary": {
    "total_nodes": 120,
    "total_edges": 340,
    "entry_points_count": 3,
    "max_depth": 8
  },
  "entry_points": [
    {
      "id": "func::src/api.py::handle_request",
      "entry_type": "api_endpoint",
      "function_name": "handle_request",
      "file": "src/api.py",
      "path": "/api/v1/query"
    }
  ]
}
```

---

## `embeddings.json` — `EmbeddingIndex` (vectors + everything)

```json
{
  "model": "sentence-transformers/all-MiniLM-L6-v2",
  "dimension": 384,
  "total_chunks": 87,
  "embeddings": [
    {
      "id": "func::src/api.py::handle_request",
      "chunk_type": "entrypoint",
      "content": "// File: src/api.py\n// Function: handle_request\n...",
      "embedding": [0.0231, -0.1847, 0.0593, ...],  // 384 floats
      "metadata": {
        "file_path": "src/api.py",
        "language": "python",
        "line_start": 10,
        "line_end": 45,
        "name": "handle_request",
        "complexity": 5
        // ← no is_entry_point here, unlike context.json
      }
    }
  ]
}
```

---

## `vectors.bin` — `VectorStore::save_binary()` (ID-keyed vectors)

This one **stores IDs alongside vectors**, so lookups by ID are possible after loading:

```
┌──────────────────────────────────────────────┐
│ HEADER                                       │
│  [4 bytes]  version = 1  (u32 LE)            │
│  [8 bytes]  count   = 87 (u64 LE)            │
│  [4 bytes]  dimension = 384 (u32 LE)         │
├──────────────────────────────────────────────┤
│ ENTRY 0                                      │
│  [4 bytes]  id_len = 38 (u32 LE)             │
│  [38 bytes] id = "func::src/api.py::handle…" │
│  [384 × 4 bytes] vector floats (f32 LE)      │
├──────────────────────────────────────────────┤
│ ENTRY 1                                      │
│  [4 bytes]  id_len = 29                      │
│  [29 bytes] id = "func::src/db.py::query"    │
│  [384 × 4 bytes] vector floats               │
├──────────────────────────────────────────────┤
│  ... × 87 entries                            │
└──────────────────────────────────────────────┘
```

---

## `embeddings.bin` — `EmbeddingIndex::save_binary()` (anonymous vectors only)

This one **strips all IDs and metadata** — pure float arrays. When loaded back, IDs become `embedding_0`, `embedding_1`, etc.:

```
┌─────────────────────────────────────────────┐
│ HEADER                                      │
│  [4 bytes]  magic = "EULX"                  │
│  [4 bytes]  version = 2  (u32 LE)           │
│  [4 bytes]  model_name_len = 43 (u32 LE)    │
│  [43 bytes] model = "sentence-transformers…"│
│  [4 bytes]  count = 87 (u32 LE)             │
│  [4 bytes]  dimension = 384 (u32 LE)        │
├─────────────────────────────────────────────┤
│ VECTORS (no IDs, no gaps)                   │
│  [384 × 4 bytes] embedding 0 floats         │
│  [384 × 4 bytes] embedding 1 floats         │
│  ...  × 87 entries                          │
└─────────────────────────────────────────────┘
```

---

## Quick comparison

| File              | Has IDs | Has content | Has vectors | Has relationships | Size     |
| ----------------- | ------- | ----------- | ----------- | ----------------- | -------- |
| `context.json`    | ✅      | ✅          | ❌          | ✅                | Medium   |
| `embeddings.json` | ✅      | ✅          | ✅          | ❌                | Large    |
| `vectors.bin`     | ✅      | ❌          | ✅          | ❌                | Small    |
| `embeddings.bin`  | ❌      | ❌          | ✅          | ❌                | Smallest |

`context.json` is the human/LLM-facing index; `embeddings.json` is the full searchable index; the two `.bin` files are fast-loading vector stores, with `vectors.bin` being the richer one since it preserves IDs.

## `query` Command Output

When using `eulix_embed query`, output goes to **stdout** (suitable for piping).

**JSON format** (`-f json`, default):

```json
{
  "query": "how does login work",
  "model": "sentence-transformers/all-MiniLM-L6-v2",
  "dimension": 384,
  "embedding": [0.0412, -0.1837, ...]
}
```

**Binary format** (`-f binary`):

```
[dimension: u32 LE]   4 bytes
[floats: f32 LE]      dimension × 4 bytes
```

Diagnostic messages are written to **stderr** so they don't corrupt binary output when piping.

---

## Typical Output Sizes

These are approximate values for a mid-size project (~5k LOC, ~150 chunks, 384-dimensional model):

| File              | Approx. size |
| ----------------- | ------------ |
| `embeddings.json` | 3–8 MB       |
| `embeddings.bin`  | 0.2–0.3 MB   |
| `vectors.bin`     | 0.3–0.4 MB   |
| `context.json`    | 0.5–2 MB     |

`embeddings.json` is the largest because it stores chunk content as text alongside vectors. `embeddings.bin` stores only raw floats and is typically 10–20× smaller.
