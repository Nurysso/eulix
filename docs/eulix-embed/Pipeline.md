# Embedding Pipeline

This page walks through what `embed` does from the knowledge base to the two output files, and why it is built the way it is.

> **Crate / path:** `eulix-embed/pipeline/`, `eulix-embed/data_io/json_stream.py`, `eulix-embed/utils/`  
> **Language:** Python  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

`BaseEmbeddingPipeline` is the driver. The ONNX and PyTorch pipelines are tiny subclasses that only say which embedder to build. Everything else is shared: streaming the input, chunking, planning batches, embedding, and writing. The work is organized as four steps run in two passes.

## Why It Exists

Three costs dominate an embedding run: reading a huge JSON file, running a model over hundreds of thousands of texts, and wasting model time on padding. The pipeline attacks each: it never loads the whole file, it keeps the model busy with similar-length texts, and it writes results as they appear.

## How It Works

**Before anything: disk check.** `_check_disk_space` estimates the output size from the dimension, the vector format and an estimated chunk count, with a 20% margin (50% more if JSON output were enabled). If free space is short it prints the shortfall and exits; if the margin is under 10% it warns. The chunk count is estimated by scanning the file for function, class and method ids, switching to about 25 chunks per megabyte of input if the file is large.

**Steps 1 and 2: stream and chunk (one pass).** `stream_kb` reads `kb.json` once with `ijson`, which emits parse events instead of building the whole document. Small top-level keys (the metadata) are kept; the huge `structure` map is yielded one file entry at a time. Each file entry is handed to a thread pool (at most 4 workers) that drops docstrings and chunks the file ([Chunking](Chunking.md)). To bound memory, no more than 32 chunking jobs are in flight; when that limit is hit, half are collected before more are submitted. The old design read a 4.6 GB file twice and rebuilt the whole call graph in memory, which the comment says caused a ten- to twenty-fold blow-up over the file size.

**Steps 3 and 4: embed and write (one pass).** A generator yields `(id, vector)` pairs, and the writer consumes them directly, so only one batch of vectors exists at a time. Duplicate ids are skipped. If skipping makes the real count smaller than the planned one, the count field in the header is patched afterwards. `vectors.bin` is written last, from the ids in the exact order the vectors were written ([Binary Format](Binary-Format.md)).

Note that the **chunk texts are all held in memory** between the two passes (the list of `Chunk` records). Only the vectors are streamed.

### Length bucketing

**The problem.** A model processes a batch as a rectangle. If one text is 20 tokens and another 500, the short one is padded to 500, and most of the computation is on padding.

**The approach.** When the embedder supports it (always on CUDA, ROCm and MPS GPUs; off on CPU), the pipeline first counts every chunk's real token length, ignoring truncation. It then snaps each length up to the nearest *bucket* and batches chunks of the same bucket together:

- standard models: 32, 64, 128, 192, 256, 384, 512 tokens;
- Jina models, which accept long input: the same, then 768, 1,024, 2,048, 4,096, 8,192.

Each batch is padded to exactly its bucket size. Longer buckets get smaller batches: the batch size is tuned for 512 tokens and scaled by `512 / bucket` (never below 1). Chunks longer than the model's limit are counted, a warning says how many will be truncated, and they go into the largest bucket.

Without bucketing (CPU), chunks are sorted longest first and cut into fixed-size batches.

**Why this way.** Similar lengths in a batch mean little padding, and sorting by length is cheap compared with the model run. The scaling keeps memory flat, because attention cost grows with sequence length.

### Main Components

| Item | Responsibility |
| --- | --- |
| `BaseEmbeddingPipeline` | The driver; `process()` runs everything and prints a summary. |
| `OnnxPipeline`, `TorchPipeline` | Choose the embedder. |
| `stream_kb` | Single-pass streaming JSON reader. |
| `snap_to_bucket`, `BUCKETS_STANDARD`, `BUCKETS_JINA` | Bucket sizes. |
| `_check_disk_space` | Output-size estimate. |

### Data In, Data Out

In: `kb.json`. Out: `embeddings.bin`, `vectors.bin`, and a printed summary of files, functions, classes, methods, chunks per type, time and speed.

## Key Algorithms

Streaming and bucketing are described above.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `--batch-size` | flag | 64 GPU, 16 CPU | Base batch size for 512-token batches. |
| `--max-chunk` | flag | 2000 | Card length limit. |
| `EULIX_TOKEN_DEBUG=1` | environment | off | Prints token-length statistics per batch, to catch padding or truncation regressions. |
| `MAX_INFLIGHT` | constant | 32 | Chunking jobs in flight. |

## Usage Examples

```bash
EULIX_TOKEN_DEBUG=1 python main.py embed -k .eulix/kb.json -o .eulix -e torch -d cuda
```

Expected result: lines like `[tokens] total=… min=… max=… median=…`, then a progress bar that shows the current bucket and batch size.

## Performance Notes

Install `libyajl2` (Debian or Ubuntu: `sudo apt install libyajl2`) so `ijson` uses its C backend; the README says this speeds up parsing by about ten times. `orjson` is used for JSON if present. The summary line reports which backends were found.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| All chunk texts sit in memory. | Needed for the length plan. | Use a smaller project or `.euignore`. |
| The disk estimate assumes ids about 50 bytes long. | Parser ids can be longer. | Keep a margin. |
| No resume. | A failed run starts over. | None. |
| Truncated chunks lose their tail silently apart from a warning count. | Model length limits. | Shorter cards, or a long-context model such as Jina. |

## Related Docs

- [Chunking](Chunking.md), [Embedders](Embedders.md), [Binary Format](Binary-Format.md).
- [Cli](../eulix-cli/Cli.md): how `analyze` invokes this.
