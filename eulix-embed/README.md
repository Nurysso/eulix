# eulix-embed

> The embedding pipeline behind the ETIYA engine: turns the knowledge base into quantized vectors for semantic retrieval.

| Language | Version | Status | Docs |
| :-- | :-- | :-- | :-- |
| Python 3.11 | 0.4.2 | Beta | [docs/eulix-embed](../docs/eulix-embed/Overview.md) |

`eulix_embed` reads the knowledge base (`kb.json`) written by [`eulix-parser`](../eulix-parser/), splits the code into chunks, embeds them with a Transformer model, and writes memory-mapped vector files (`embeddings.bin` and `vectors.bin`) for fast retrieval. `eulix analyze` runs it only when the retrieval engine is ETIYA; the EHNAS engine needs no embeddings. The `eulix` binary ships these scripts inside it and installs them into `~/.Eulix/`, so you only need this README to run or develop the pipeline yourself.

## Features

- **Two inference engines.** ONNX Runtime (`--engine onnx`, default): lightweight, high-throughput inference with no PyTorch overhead, on CPU or GPU. PyTorch (`--engine torch`): CUDA, ROCm/HIP (AMD) and Apple Silicon (MPS). See [Embedders](../docs/eulix-embed/Embedders.md).
- **Persistent daemon mode.** `server` runs as a long-lived stdin/stdout process for the Go CLI, so the model loads once and queries embed in under 50 ms.
- **Single-pass streaming.** `ijson` with its C extension (`libyajl2`) and `orjson` stream multi-gigabyte knowledge-base files with little memory.
- **SQ8 int8 quantization.** `--quantize` shrinks embeddings 4× with under 1% loss in retrieval quality. `eulix analyze` always passes it.
- **Token bucketing.** Chunks are grouped by sequence length before batching on GPU or CPU, which removes most padding and raises throughput.
- **Compact chunk text.** Each function, method, class and file becomes a short text card: name, lines, signature, parameters and class bases. Docstrings are removed before embedding, and the parser's slim `kb.json` carries no calls or complexity, so cards hold only structure and names. See [Chunking](../docs/eulix-embed/Chunking.md).

## Install

Set the pipeline up with [uv](https://github.com/astral-sh/uv):

```bash
cd eulix-embed
uv venv --python 3.11
source .venv/bin/activate   # Windows: .venv\Scripts\activate
```

Then install the requirements for your engine and hardware:

| Engine | Hardware | Command |
| :-- | :-- | :-- |
| ONNX Runtime (default) | NVIDIA (CUDA) | `uv pip install -r requiremenents/onnx-nvidia.txt` |
| ONNX Runtime | AMD (ROCm), CPU-only, Mac | `uv pip install -r requiremenents/onnx-amd.txt` |
| PyTorch | NVIDIA (CUDA 13.0) | `uv pip install -r requiremenents/torch-nvidia.txt` |
| PyTorch | AMD (ROCm) | `uv pip install -r requiremenents/torch-amd.txt` |

For other CUDA versions, pick the matching torch build from the PyTorch website. For 10× faster JSON parsing through `ijson`'s C backend, install `libyajl2` (`sudo apt install libyajl2` on Ubuntu/Debian).

## Usage

```text
python main.py <command> [options]
```

### `embed`: embed a knowledge base

```bash
uv run main.py embed \
  --kb-path .eulix/kb.json \
  --output .eulix \
  --model BAAI/bge-base-en-v1.5 \
  --engine onnx \
  --device auto
```

| Flag | Default | Description |
| :-- | :-- | :-- |
| `-k, --kb-path <PATH>` | `knowledge_base.json` | Input knowledge base |
| `-o, --output <DIR>` | `./embeddings` | Directory for the vector files |
| `-m, --model <NAME>` | `BAAI/bge-base-en-v1.5` | Hugging Face model name |
| `-e, --engine <ENGINE>` | `onnx` | `onnx` or `torch` |
| `-d, --device <DEVICE>` | `auto` | `cuda`, `rocm`, `mps` or `cpu` |
| `-b, --batch-size <N>` | auto | 64 on GPU, 16 on CPU |
| `--max-chunk <N>` | `2000` | Maximum characters per chunk |
| `--quantize` | off | SQ8 int8 quantization (4× smaller) |

### `query`: embed one query

Embeds a query string and prints the vector dimension or raw JSON.

```bash
uv run main.py query \
  -q "how does authentication middleware work" \
  -m BAAI/bge-base-en-v1.5 \
  --engine onnx
```

| Flag | Default | Description |
| :-- | :-- | :-- |
| `-q, --query <TEXT>` | required | Query string to embed |
| `-m, --model <NAME>` | `BAAI/bge-base-en-v1.5` | Hugging Face model name |
| `-e, --engine <ENGINE>` | `onnx` | `onnx` or `torch` |
| `-f, --format <FMT>` | `json` | `json` or `binary` |

### `server`: persistent daemon

The embedding server `eulix chat` and the MCP server use with ETIYA, so queries embed without restarting the model. See [Server](../docs/eulix-embed/Server.md).

```bash
uv run main.py server \
  -m BAAI/bge-base-en-v1.5 \
  --engine onnx \
  --device auto
```

### `compare`: check output consistency

Checks entry counts and dimensions, and spot-checks vector values between `embeddings.bin` and `vectors.bin`.

```bash
uv run main.py compare .eulix/embeddings.bin .eulix/vectors.bin
```

## Output files

| File | Description |
| :-- | :-- |
| `embeddings.bin` | Fixed-size vector records in chunk order, for O(1) access by index and memory mapping |
| `vectors.bin` | The chunk ids in the same order: id number n belongs to vector number n |

Binary format (little-endian), also documented in [Binary Format](../docs/eulix-embed/Binary-Format.md):

```text
embeddings.bin
  magic (4 bytes)          : "EULX"
  version (4 bytes)        : u32 (currently 5)
  model_name_len (4 bytes) : u32
  model_name (N bytes)     : UTF-8 string
  count (4 bytes)          : u32
  dimension (4 bytes)      : u32
  quantized (1 byte)       : 0 = float32, 1 = SQ8 int8
  then `count` fixed-size records:
    float32 form : dimension * 4 bytes
    SQ8 form     : 4 bytes scale (f32) + dimension bytes (int8)

vectors.bin
  magic, version, model_name_len, model_name : as above
  count (4 bytes)          : u32
  then `count` entries: id_len (u32) + id (UTF-8)
  # entry n is the id of vector n in embeddings.bin
```

A file written by a different version is rejected, so re-run `eulix analyze` after upgrading.

## Models

| Model | Dimension | Speed | Best for |
| :-- | :-- | :-- | :-- |
| `BAAI/bge-base-en-v1.5` | 768 | Fast | Default; strong semantic code retrieval |
| `BAAI/bge-small-en-v1.5` | 384 | Very fast | Constrained machines and quick local indexing |
| `jinaai/jina-embeddings-v2-base-code` | 768 | Moderate | Long-context code (up to 8,192 tokens) |

Query and indexing must use the same model and engine; `eulix` passes both from `eulix.toml`. Remote model code (`trust_remote_code`) is allowed only for `jinaai/` models.

## Development

```bash
uv pip install -r requiremenents/requirements-dev.txt
ruff check .
mypy .
```

Dev requirements also include `bandit`, `radon` and `codespell`.

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
