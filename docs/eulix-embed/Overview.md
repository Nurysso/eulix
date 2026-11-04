# eulix-embed

`eulix-embed` is the Python tool that turns the parser's knowledge base into vectors, and answers "what vector is this question?" while you chat.

> **Crate / path:** `eulix-embed/` (entry point `main.py`, version `0.4.2`)  
> **Language:** Python 3.11  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

Keyword search finds code that shares words with your question. Embeddings find code that *means* something similar. An embedding model turns a piece of text into a list of numbers (a vector) such that texts with similar meaning get vectors that point in similar directions. `eulix-embed` does this for every function, method, class and file in a repository, and saves the vectors to disk so that the `eulix` program can search them later.

It has two jobs:

1. **Indexing** (`embed` command, run by `eulix analyze`): read `kb.json`, cut it into *chunks*, embed each chunk, and write `embeddings.bin` and `vectors.bin`.
2. **Query embedding** (`server` command, started by `eulix chat` and `eulix mcp`): stay running with the model loaded, and turn each question into a vector in a few milliseconds.

Only the default retrieval engine, ETIYA, uses these files. The EHNAS engine does not ([Engines](../eulix-cli/query/retrieval/Engines.md)).

This folder has five more pages:

| Page | Covers |
| --- | --- |
| [Chunking](Chunking.md) | How the knowledge base becomes text "cards", and what the cards contain. |
| [Pipeline](Pipeline.md) | Streaming, length bucketing, batching, disk checks. |
| [Embedders](Embedders.md) | The ONNX and PyTorch backends, devices, models. |
| [Binary Format](Binary-Format.md) | The layout of `embeddings.bin` and `vectors.bin`, and SQ8 quantization. |
| [Server](Server.md) | The line-based protocol the `eulix` program speaks to the daemon. |

## Why It Exists

Embedding models are Python libraries (ONNX Runtime, PyTorch, Hugging Face tokenizers). The rest of Eulix is Go and Rust. Keeping the model code in one small Python tool, driven over a pipe, lets the other programs stay free of Python and lets users swap models without rebuilding anything.

## How It Works

```text
kb.json ──stream──► chunker ──► token-length buckets ──► embedder (ONNX or torch) ──► embeddings.bin
 (parser)            (threads)       (batches)                 (CPU / GPU)             vectors.bin
```

1. **Stream.** `kb.json` can be gigabytes, so it is read in a single pass with `ijson`, one file entry at a time ([Pipeline](Pipeline.md)).
2. **Chunk.** Each file entry becomes chunks: one per function, class, method, and one summary per file ([Chunking](Chunking.md)).
3. **Embed.** Chunks are grouped by token length so batches need little padding, then run through the model ([Embedders](Embedders.md)).
4. **Write.** Vectors are written to disk as they are produced, so memory use is one batch, not the whole index ([Binary Format](Binary-Format.md)).

### Commands

Run as `python main.py <command>`; if the first argument is not a known command, `embed` is assumed.

| Command | What it does |
| --- | --- |
| `embed` | Build `embeddings.bin` and `vectors.bin` from a knowledge base. |
| `query -q "text"` | Embed one string and print it (`json`, or `binary`). |
| `server` | Long-running daemon for query embedding ([Server](Server.md)). |
| `compare <emb> <vec>` | Check the two binary files for consistency: duplicate ids, norms, dimension. |
| `ijson-backend` | Report which JSON parsing backend is active. |
| `version` | Print the version. |

### Main Components

| Folder or file | Responsibility |
| --- | --- |
| `main.py` | Argument parsing, dependency check, dispatch. |
| `cli/` | Command implementations and `compare`. |
| `chunking/` | Chunking and the text formatters. |
| `pipeline/` | The shared driver and the ONNX/torch subclasses. |
| `embedders/` | The two model backends. |
| `data_io/` | Streaming JSON reader, binary reader and writer, quantization. |
| `server/` | The daemon. |
| `utils/` | Constants (binary magic and version, bucket sizes), shared types. |

### Data In, Data Out

In: `kb.json`. Out: `embeddings.bin` (the vectors) and `vectors.bin` (the chunk ids, in the same order). `eulix analyze` calls it as `embed -k .eulix/kb.json -o .eulix -m <model> --quantize -e <engine>`.

## Key Algorithms

The interesting ones are length bucketing ([Pipeline](Pipeline.md)), mean pooling and normalization ([Embedders](Embedders.md)), and scalar quantization to bytes ([Binary Format](Binary-Format.md)).

## Configuration

`eulix.toml` sets `[embeddings] model`, `engine`, `dimension` and `debug` ([Config](../eulix-cli/Config.md)). Flags of `embed`:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-k, --kb-path` | `knowledge_base.json` | Input knowledge base. |
| `-o, --output` | `./embeddings` | Output directory. |
| `-m, --model` | `BAAI/bge-base-en-v1.5` | Hugging Face model name. |
| `-e, --engine` | `onnx` | `onnx` or `torch`. |
| `-d, --device` | auto | `cuda`, `rocm`, `mps` or `cpu`. |
| `-b, --batch-size` | auto | 64 on GPUs, 16 on CPU. |
| `--max-chunk` | 2000 | Longest chunk, in characters. |
| `--quantize` | off | Store vectors as bytes ([Binary Format](Binary-Format.md)). |

Recommended models: `BAAI/bge-base-en-v1.5` (768 dimensions, the default), `BAAI/bge-small-en-v1.5` and `sentence-transformers/all-MiniLM-L6-v2` (384), and `jinaai/jina-embeddings-v2-base-code` (768, long inputs up to 8,192 tokens).

Install the Python packages with `uv pip install -r requiremenents/<file>.txt` (the folder name is spelled that way) where the file is `onnx-nvidia`, `onnx-amd`, `torch-nvidia` or `torch-amd`. `eulix` does this for you on first run.

## Usage Examples

```bash
python main.py embed -k .eulix/kb.json -o .eulix -m BAAI/bge-base-en-v1.5 -e onnx --quantize
python main.py query -q "how does authentication middleware work" -e onnx
python main.py compare .eulix/embeddings.bin .eulix/vectors.bin
```

Expected result: a progress bar while embedding and a summary of chunks per second; `compare` ends with a pass or fail report.

## Performance Notes

The pipeline is designed so that peak memory is about one batch of vectors, and so that GPUs see few padding tokens. I found no current benchmark: the older `benchmark.md` in the project history describes an earlier Rust implementation and was not carried over.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The chunk text is much thinner than documented. | See [Chunking](Chunking.md) and [EMB-6 and EMB-7](../Known-Issues/Embeddings.md#emb-6-embedded-text-has-no-line-breaks). | None yet. |
| ROCm gives wrong vectors with ONNX. | See [EMB-1](../Known-Issues/Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings). | Use `engine = "ehnas"`. |
| Needs Python 3.11 and (first run) network access. | Model files are downloaded from Hugging Face. | Pre-download or point `-m` at a local model. |

## Related Docs

- [Chunking](Chunking.md), [Pipeline](Pipeline.md), [Embedders](Embedders.md), [Binary Format](Binary-Format.md), [Server](Server.md).
- [Embeddings (eulix-cli)](../eulix-cli/Embeddings.md): the Go side of the daemon.
- [Embedding issues](../Known-Issues/Embeddings.md).
