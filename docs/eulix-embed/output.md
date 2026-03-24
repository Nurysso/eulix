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

## `embeddings.json`

Human-readable full index. Use this for debugging, tooling integration, or any consumer that needs chunk content alongside vectors.

```jsonc
{
  "model": "sentence-transformers/all-MiniLM-L6-v2",
  "dimension": 384,
  "total_chunks": 142,
  "embeddings": [
    {
      "id": "my_project::auth::login",
      "chunk_type": "entrypoint",
      "content": "// File: src/auth.py\n// Function: login\n...",
      "embedding": [0.0412, -0.1837, ...],   // Vec<f32>, length = dimension
      "metadata": {
        "file_path": "src/auth.py",
        "language": "python",
        "line_start": 42,
        "line_end": 78,
        "name": "login",
        "complexity": 5
      }
    }
  ]
}
```

**Fields:**

| Field                              | Type    | Description                                                  |
| ---------------------------------- | ------- | ------------------------------------------------------------ |
| `model`                            | string  | HuggingFace model identifier                                 |
| `dimension`                        | usize   | Embedding vector length                                      |
| `total_chunks`                     | usize   | Number of entries                                            |
| `embeddings[].id`                  | string  | Stable chunk identifier (function ID or `file:<path>`)       |
| `embeddings[].chunk_type`          | string  | `entrypoint \| function \| class \| method \| file \| other` |
| `embeddings[].content`             | string  | Formatted text that was embedded                             |
| `embeddings[].embedding`           | f32[]   | L2-normalized vector (if `normalize = true`)                 |
| `embeddings[].metadata.file_path`  | string? | Source file                                                  |
| `embeddings[].metadata.language`   | string? | Detected language                                            |
| `embeddings[].metadata.line_start` | usize?  | Start line in source                                         |
| `embeddings[].metadata.line_end`   | usize?  | End line in source                                           |
| `embeddings[].metadata.name`       | string  | Function/class/file name                                     |
| `embeddings[].metadata.complexity` | usize?  | Cyclomatic complexity (functions/methods only)               |

---

## `embeddings.bin`

Compact binary format for fast loading in production. Vectors are stored in the same order as `embeddings.json` but without IDs, content, or metadata — just the raw floats. Use alongside `context.json` to reconstruct full records by positional index.

**Format:**

```
[magic: 4 bytes]       "EULX"
[version: u32 LE]      2
[model_len: u32 LE]    length of model name string
[model: utf8 bytes]    model name
[count: u32 LE]        number of embeddings
[dimension: u32 LE]    vector length
[vectors: f32 LE]      count × dimension floats, packed
```

All multi-byte integers are **little-endian**. Each float is IEEE 754 `f32`.

**Loading example (Python):**

```python
import struct, numpy as np

with open("embeddings.bin", "rb") as f:
    magic = f.read(4)                          # b"EULX"
    version = struct.unpack("<I", f.read(4))[0]
    model_len = struct.unpack("<I", f.read(4))[0]
    model = f.read(model_len).decode()
    count = struct.unpack("<I", f.read(4))[0]
    dim = struct.unpack("<I", f.read(4))[0]
    vectors = np.frombuffer(f.read(count * dim * 4), dtype="<f4").reshape(count, dim)
```

---

## `vectors.bin`

Vector store format — same float data as `embeddings.bin` but each entry includes its string ID. Use this when you need ID-based lookup without loading the full JSON index.

**Format:**

```
[version: u32 LE]      1
[count: u64 LE]        number of vectors
[dimension: u32 LE]    vector length

For each vector:
  [id_len: u32 LE]     byte length of id string
  [id: utf8 bytes]     chunk id
  [floats: f32 LE]     dimension floats
```

---

## `context.json`

Metadata-only index — no embedding vectors. Designed to be loaded by the LLM query layer for context window assembly, relationship traversal, and tag-based filtering.

```jsonc
{
  "metadata": {
    "project_name": "my_project",
    "total_chunks": 142,
    "chunk_types": { "Function": 89, "Method": 31, "EntryPoint": 8, "Class": 9, "File": 5 },
    "created_at": "2026-03-20T10:42:00Z",
    "embedding_dimension": 384,
    "languages": ["python", "typescript"],
    "architecture_style": "layered",
  },
  "chunks": [
    {
      "id": "my_project::auth::login",
      "chunk_type": "entrypoint",
      "content": "...",
      "metadata": {
        "file_path": "src/auth.py",
        "language": "python",
        "line_start": 42,
        "line_end": 78,
        "name": "login",
        "complexity": 5,
        "is_entry_point": true,
      },
      "tags": ["entrypoint", "api", "async"],
      "importance_score": 1.0,
    },
  ],
  "relationships": [
    {
      "from": "my_project::auth::login",
      "to": "my_project::db::get_user",
      "rel_type": "calls",
      "conditional": false,
    },
  ],
  "tags": {
    "api": ["my_project::auth::login", "my_project::auth::logout"],
    "async": ["my_project::auth::login"],
  },
  "call_graph_summary": {
    "total_nodes": 97,
    "total_edges": 143,
    "entry_points_count": 8,
    "max_depth": 6,
  },
  "entry_points": [
    {
      "id": "my_project::auth::login",
      "entry_type": "api_endpoint",
      "function_name": "login",
      "file": "src/auth.py",
      "path": "/auth/login",
    },
  ],
}
```

**Key fields:**

| Field                              | Description                                                         |
| ---------------------------------- | ------------------------------------------------------------------- |
| `chunks[].importance_score`        | 1.0 = entry point, 0.7 = class, 0.5 = file, function scores from KB |
| `chunks[].metadata.is_entry_point` | `true` if this chunk is in the KB's entry points list               |
| `relationships[].rel_type`         | `calls \| called_by \| imports \| inherits \| contains \| uses`     |
| `tags`                             | Inverted index — look up all chunk IDs for a given tag in O(1)      |
| `call_graph_summary.max_depth`     | BFS depth from all entry points; useful for complexity assessment   |

---

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
