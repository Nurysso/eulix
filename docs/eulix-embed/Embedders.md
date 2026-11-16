# Embedders

This page describes the two model backends, ONNX Runtime and PyTorch, how each picks a device, and what turns model output into a vector.

> **Crate / path:** `eulix-embed/embedders/` (`onnx_embed.py`, `torch_embed.py`, `mixins.py`)  
> **Language:** Python  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

An *embedder* loads a model and offers `_embed_batch(texts)`, which returns one vector per text, plus `embed_query(text)` for a single text. Two classes implement this. `EmbeddingGeneratorOnnx` uses ONNX Runtime and needs no PyTorch at inference time. `EmbeddingGeneratorTorch` uses PyTorch and the Hugging Face libraries. They produce comparable vectors for the same model, and you choose one with `engine` in `eulix.toml`.

## Why It Exists

ONNX Runtime is lighter and faster to start, which matters for a daemon that starts with every chat. PyTorch supports more devices (including Apple's MPS) and models that need custom code. Offering both lets users pick what works on their hardware.

## How It Works

### Turning text into a vector

Both backends do the same three things.

1. **Tokenize.** Text is split into tokens by the model's tokenizer, truncated to the model's maximum (512 tokens by default), and padded to a bucket length during indexing ([Pipeline](Pipeline.md)).
2. **Run the model.** The model returns one vector per token.
3. **Pool and normalize.** The token vectors are averaged, ignoring padding (*mean pooling*, the convention sentence-transformers models were trained with), and the result is scaled to length 1 (*L2 normalization*). If an ONNX model already returns a pooled sentence vector (`sentence_embedding` or `pooler_output`), that is used directly.

Normalization is what lets the retrieval side compare vectors with a plain dot product.

**No query prefix.** `embed_query` embeds the question as is. Some models (the BGE family, for example) recommend a special instruction prefix for queries. The code adds none. I did not test whether adding one would help.

### ONNX backend

**Finding the model file.** `_resolve_onnx_model` tries, in order: a local `.onnx` file; a local directory holding `onnx/model.onnx`, `model.onnx`, `onnx/model_fp16.onnx` or `onnx/model_quantized.onnx`; a copy exported earlier and cached; a pre-exported file in the Hugging Face repository (with its `.onnx_data` sibling for models over 2 GB); and finally an export on the fly with `optimum`, which needs PyTorch once and caches the result. After that, inference uses only ONNX Runtime.

**Choosing a device.** With no device requested, the backend picks the first available execution provider in this order: CUDA, ROCm, MIGraphX (AMD), CoreML, then CPU. A requested device that is not available falls back with a warning. GPU batches default to 64 and CPU batches to 16, and bucketing is on only for CUDA and ROCm.

> **Caution.** Automatic ROCm selection is the source of [EMB-1](../Known-Issues/Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings): on some machines ROCm vectors are wrong. Pass `-d cpu` to avoid it.

### PyTorch backend

Picks CUDA (which also covers ROCm builds of PyTorch), then Apple MPS, then CPU, with the same default batch sizes. Bucketing is on for CUDA and MPS.

**Remote code.** Some models, such as Jina v2, need Python code from their repository (`trust_remote_code`). The `eulix.toml` of a cloned repository controls the model name, so this is dangerous. `trusts_remote_code` therefore allows it **only** for names starting with `jinaai/` that are not a local path. Any other name loads without remote code. See [Fixed Issues](../Known-Issues/Fixed-Issues.md). Jina models are then loaded through sentence-transformers with eager attention.

### Main Components

| Item | Responsibility |
| --- | --- |
| `EmbeddingGeneratorOnnx` | ONNX backend: model resolution, providers, tokenizer, pooling. |
| `EmbeddingGeneratorTorch` | PyTorch backend. |
| `BucketBatchMixin` | Bucket-aware batch size. |
| `_resolve_providers` | Execution-provider choice. |
| `trusts_remote_code` | The remote-code allowlist. |

### Data In, Data Out

In: a list of strings. Out: an array of L2-normalized float vectors, one per string. `dimension` is read from the model by running a dummy input, not hard-coded.

## Key Algorithms

### Mean pooling with a mask

**The problem.** Padded positions would drag the average toward the padding token.

**The approach.** Multiply each token vector by its attention mask (1 for real tokens, 0 for padding), sum, and divide by the number of real tokens.

**Why this way.** The result is the same whether a text is padded or not, so indexing in buckets and querying one text at a time agree.

## Configuration

`[embeddings] model` and `engine` in `eulix.toml`; `-d/--device` on the command line (not set by `eulix analyze`). Dependencies are listed in `requiremenents/` for NVIDIA and AMD, ONNX and torch.

## Usage Examples

```bash
python main.py query -q "token validation" -m BAAI/bge-small-en-v1.5 -e torch -f json
```

Expected result: a JSON object with the vector and its dimension.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Wrong vectors on some ROCm systems. | See [EMB-1](../Known-Issues/Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings). | `-d cpu`, or `engine = "ehnas"`. |
| First use of a model with no ONNX file needs PyTorch and optimum. | One-time export. | Install the torch requirements first, or choose a model with an ONNX file. |
| A fine-tuned Jina model under another organization cannot use remote code. | Security rule. | Use the ONNX engine. |
| Query and indexing must use the same engine and model. | Different engines give slightly different vectors. | Keep `eulix.toml` consistent; `eulix` passes the engine to both. |

## Related Docs

- [Pipeline](Pipeline.md): batching and bucketing.
- [Server](Server.md): how queries reach an embedder.
- [Embedding issues](../Known-Issues/Embeddings.md).
