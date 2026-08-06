# eulix_embed.py

eulix_Embed is a Python-based knowledge base embedding generator that processes kb json created by `eulix_parser` into semantic vector embeddings. It analyzes code structure, creates chunks, generates embeddings using AutoModel/AutoTokenizer from Hugging Face, and builds searchable indices for code understanding and retrieval.

> This is a python port of original [rust version](https://github.com/Nurysso/eulix/tree/Embedder-Rust-Onnx/eulix-embed)

## How It Works

The script reads a KB JSON file (typically `knowledge_base.json`) and performs a **single-pass stream** over it using `ijson`, chunking every function, method, class, and file into structured text representations, embedding them with a HuggingFace transformer model, and writing the results to binary files ready for vector search.

```
KB JSON ──► single-pass stream ──► chunks ──► embeddings ──► embeddings.bin
                                                           └──► vectors.bin
                                                           └──► context.json  (optional)
                                                           └──► embeddings.json (optional)
```

## Requirements

- Python 3.10 or 3.11
- PyTorch with CUDA (NVIDIA), ROCm/HIP (AMD), MPS (Apple Silicon), or CPU

## Installation

This project requires **Python 3.10** and can be set up quickly using [uv](https://github.com/astral-sh/uv), a fast Python package installer and resolver.

### 1. Environment Setup

Choose the installation flow that matches your operating system:

```bash
# Install uv (if you haven't already)
 # macOS/Linux
curl -LsSf [https://astral.sh/uv/install.sh](https://astral.sh/uv/install.sh) | sh

# Windows
powershell -c "irm [https://astral.sh/uv/install.ps1](https://astral.sh/uv/install.ps1) | iex"

# Create and activate a Python 3.10 virtual environment
uv venv --python 3.10
source .venv/bin/activate       # Linux/macOS
.\venv\Scripts\activate          # Windows

```

### 2. Install Project Dependencies

Install the core packages along with the recommended fast JSON and parsing backends:
Bash

```bash
uv pip install -r requirements.txt
```

> Note on JSON Performance: The project will automatically use orjson and ijson with C-extensions for maximum speed when parsing large datasets.

### 3. Install PyTorch

PyTorch installation commands vary depending on your Operating System and GPU availability. Run the command that matches your hardware setup:
|Platform / OS | Compute Target | Command |
| ------------------ | ---------------------------------------- | ------------------------------------------------------------------------------ |
| Linux / Windows| NVIDIA GPU (CUDA 12.1) | uv pip install torch --index-url https://download.pytorch.org/whl/cu121 |
| Linux / Windows| NVIDIA GPU (CUDA 11.8) | uv pip install torch --index-url https://download.pytorch.org/whl/cu118 | Linux / Windows | CPU Only | uv pip install torch --index-url https://download.pytorch.org/whl/cpu|
| mac OS | Apple Silicon | uv pip install torch|
| Linux | AMD GPU (ROCm 7.2+) | uv pip install torch torchvision --index-url https://download.pytorch.org/whl/rocm7.2 |

For other environments (like AMD ROCm), please consult the official PyTorch Start Guide.

### 4. Model-Specific Packages (Optional)

If you plan to use Jina v2 models, you will need to install the sentence-transformers package:
Bash

```bash
uv pip install sentence-transformers
```

---

## Usage

### `embed` — generate embeddings from a KB file

```bash
python eulix_embed.py embed [OPTIONS]
```

| Option             | Default                                  | Description                                                                    |
| ------------------ | ---------------------------------------- | ------------------------------------------------------------------------------ |
| `-k` / `--kb-path` | `knowledge_base.json`                    | Path to the KB JSON file                                                       |
| `-o` / `--output`  | `./embeddings`                           | Output directory                                                               |
| `-m` / `--model`   | `sentence-transformers/all-MiniLM-L6-v2` | HuggingFace model name                                                         |
| `--device`         | auto                                     | `cuda` / `mps` / `cpu`                                                         |
| `--batch-size`     | auto (64 GPU, 16 CPU)                    | Embedding batch size                                                           |
| `--max-chunk`      | `2000`                                   | Maximum characters per chunk                                                   |
| `--save-json`      | off                                      | Also write `embeddings.json` + `context.json`, and enable graph edge streaming |

**Examples:**

```bash
# Basic run — auto device, default model
uv run eulix_embed.py embed -k .eulix/kb.json -o .eulix

# Specific model, save JSON outputs
uv run  eulix_embed.py embed -k .eulix/kb.json -o .eulix -m BAAI/bge-base-en-v1.5 --save-json
```

---

### `query` — embed a single query string

Useful for testing similarity search at the CLI level.

```bash
uv run eulix_embed.py query -q "how does authentication work" -m BAAI/bge-base-en-v1.5
```

| Option            | Default            | Description                       |
| ----------------- | ------------------ | --------------------------------- |
| `-q` / `--query`  | _(required)_       | Query text to embed               |
| `-m` / `--model`  | `all-MiniLM-L6-v2` | HuggingFace model name            |
| `-f` / `--format` | `json`             | Output format: `json` or `binary` |

---

### `compare` — verify embeddings.bin matches vectors.bin

```bash
python eulix_embed.py compare [embeddings.bin] [vectors.bin]
```

Checks dimension consistency, entry count, and spot-checks vector values. Exits non-zero if mismatches are found.

---

## Output Files

| File              | Always written     | Description                                              |
| ----------------- | ------------------ | -------------------------------------------------------- |
| `embeddings.bin`  | ✓                  | Ordered list of `(id, f32[dim])` — preserves chunk order |
| `vectors.bin`     | ✓                  | `id → f32[dim]` map — for keyed lookup                   |
| `context.json`    | `--save-json` only | Relationships, tags, entry points, call graph summary    |
| `embeddings.json` | `--save-json` only | Human-readable embeddings with metadata                  |

### Binary format (v3)

```
magic(4)          — "EULX"
version(4)        — u32 little-endian, value = 3
model_name_len(4) — u32
model_name        — UTF-8 bytes
count(4)          — u32 number of entries
dimension(4)      — u32
[ id_len(4) + id_bytes + f32 * dimension ] × count
```

---

## Supported Models

| Model                                    | Dimension | Notes                               |
| ---------------------------------------- | --------- | ----------------------------------- |
| `sentence-transformers/all-MiniLM-L6-v2` | 384       | Fast, good general purpose baseline |
| `BAAI/bge-small-en-v1.5`                 | 384       | Small, strong retrieval performance |
| `BAAI/bge-base-en-v1.5`                  | 768       | Balanced quality/speed              |
| `jinaai/jina-embeddings-v2-base-code`    | 768       | 8192-token context, best for code   |

Jina v2 models require `sentence-transformers` and use an extended bucket schedule up to 8192 tokens.

---

## Chunk Types

The chunker mirrors the Rust `chunker.rs` logic exactly and produces five chunk types:

| Type         | Source                                     | Importance score      |
| ------------ | ------------------------------------------ | --------------------- |
| `entrypoint` | Functions marked as entry points in the KB | 1.0                   |
| `function`   | Top-level functions                        | from KB (default 0.5) |
| `method`     | Class methods                              | from KB (default 0.5) |
| `class`      | Class overview (attributes + method list)  | 0.7                   |
| `file`       | Per-file summary (imports, function list)  | 0.5                   |

---

## Performance Notes

- **ijson C backend**: install `libyajl2` (`apt install libyajl2` / `dnf install yajl`) for ~10× faster JSON streaming. Without it, set `IJSON_BACKEND=python` to suppress loader errors.
- **Bucketing**: on GPU, sequences are grouped into fixed-length buckets before batching. This avoids padding waste and significantly improves throughput.
- **orjson**: optional but recommended — install with `pip install orjson` for faster JSON output when using `--save-json`.
- **Graph edges**: call and dependency graph edges are only streamed when `--save-json` is passed. Without it, `Relationships: 0` in the summary is expected.

---

## Known Issues

- **648 chunks / 590 vectors discrepancy**: if chunk count exceeds vector count in the summary, duplicate chunk IDs are silently dropped from `vector_store`. This is most likely caused by class IDs not being tracked in `seen_ids` during chunking. A fix is to add `seen_ids.add(cls["id"])` before appending class chunks in `chunk_one_file`.
- **`(null): No such file or directory` at startup**: these messages come from the ROCm/libtorch stack trying to dlopen a library, not from this script. They are harmless and the pipeline runs correctly.

---

## License

Copyright (C) 2026 Dawood Khan
Licensed under the [GNU General Public License v3.0 or later](https://www.gnu.org/licenses/gpl-3.0.html).
