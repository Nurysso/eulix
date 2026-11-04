# Binary Format

This page specifies `embeddings.bin` and `vectors.bin`, the two files `eulix-embed` writes and `eulix` reads.

> **Crate / path:** `eulix-embed/data_io/binary.py`, `eulix-embed/data_io/serialization.py`, `eulix-embed/utils/constants.py`  
> **Language:** Python (writer); Go (reader, in `eulix-cli`)  
> **Status:** stable, format version 5  
> **Last verified against:** `ad744e9`

## Overview

Two files hold an index. `embeddings.bin` holds the vectors; `vectors.bin` holds the chunk ids, in the same order. The *position* in the list is the link between them: id number *n* in `vectors.bin` belongs to vector number *n* in `embeddings.bin`. All numbers are little-endian.

## Why It Exists

JSON would be several times larger and slow to parse. A fixed layout lets a reader jump straight to vector *n* by arithmetic, or map the file into memory and treat it as an array.

## How It Works

### `embeddings.bin`

```text
4 bytes   magic           "EULX"
4 bytes   version         5   (u32)
4 bytes   model name len  n   (u32)
n bytes   model name      UTF-8
4 bytes   count           number of vectors (u32)
4 bytes   dimension       numbers per vector (u32)
1 byte    quantized       0 = float32, 1 = SQ8
then `count` records of equal size:
  float32 form:  dimension × 4 bytes
  SQ8 form:      4 bytes scale (float32) + dimension × 1 byte (int8)
```

Every record has the same size, so vector *i* starts at a fixed offset: `header + i × record_size`. The `eulix-embed` README now documents the same layout.

### `vectors.bin`

```text
4 bytes   magic           "EULX"
4 bytes   version         5
4 bytes   model name len, then the name
4 bytes   count           (u32)
then `count` entries:
  4 bytes  id length      (u32)
  n bytes  id             UTF-8
```

An id longer than 65,535 bytes is rejected. The reader accepts versions 1 to 5.

### SQ8 quantization

With `--quantize` (which `eulix analyze` always passes), each vector is stored as bytes. The largest absolute value in the vector, divided by 127, becomes the *scale*; every number is divided by the scale, rounded, and clamped to −127 to 127. To read it back, multiply each byte by the scale. A vector that is almost all zeros is stored as zeros with scale 1. The `eulix` loader multiplies back to floats and normalizes again.

The cost is some accuracy: the source says about 1% retrieval quality, and the README says under 1%; I did not measure it. The size shrinks to a quarter (plus 4 bytes per vector).

### Main Components

| Item | Responsibility |
| --- | --- |
| `save_embeddings_bin`, `save_vectors_bin` | Writers. |
| `load_vectors_bin` | Reads ids. |
| `FastEmbeddingsReader` | Random access to one vector; handles both forms. |
| `sq8_encode`, `sq8_decode` | Quantization. |
| `BINARY_VERSION` | Bump when the layout changes. |

### Data In, Data Out

In: the vector stream and the ordered ids. Out: the two files. The Go loader checks the magic and the version (it requires exactly 5) and rejects anything else; see [Index Loading](../eulix-cli/query/retrieval/Index-Loading.md).

## Key Algorithms

### Writing before the count is known

The writer is given the planned count. If duplicate ids reduce the real count, the pipeline reopens the file and patches the count field (at offset `12 + model-name length`), so the header is always right at the end.

## Configuration

`--quantize` only. The version number is a constant.

## Usage Examples

Check the pair of files:

```bash
python main.py compare .eulix/embeddings.bin .eulix/vectors.bin
```

It checks the header, duplicate ids, dimensions, vector norms (outliers) and spot-checks values. As [EMB-1](../Known-Issues/Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings) shows, it cannot detect vectors that are well-formed but wrong.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Changing the layout breaks old indices. | The Go loader requires version 5 exactly. | Re-run `eulix analyze` after an upgrade. |
| The repair tools in `eulix-cli` assume an older layout. | See [Fixers](../eulix-cli/Fixers.md). | They are unused. |

## Related Docs

- [Pipeline](Pipeline.md): who writes the files.
- [Index Loading](../eulix-cli/query/retrieval/Index-Loading.md): who reads them.
