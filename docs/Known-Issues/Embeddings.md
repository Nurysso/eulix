# Embedding Issues

Known problems in the embedding tool (`eulix-embed`) and the query-time embedder in `eulix-cli`.

> **Scope:** `eulix-embed`, `eulix-cli/internal/embeddings`  
> **Last verified against:** `ad744e9` (2026-11-03)

None of these entries was reproduced on 2026-11-03: the machine I used has no ROCm GPU, and the others need an end-to-end run. They are marked *Carried over* unless I read the current code. Read [Overview](Overview.md) for the legend.

---

## EMB-1: ONNX on ROCm produces wrong embeddings

**Severity:** High · **Status:** Partly fixed · **Verification:** Carried over (the fix status was read in code)

**Symptom.** On machines where onnxruntime offers `ROCMExecutionProvider`, the ONNX embedder's vectors are wrong, both when indexing and when embedding a question. Semantic search in the ETIYA engine then returns noise: in the maintainers' benchmark, 0 of 8 plain-language questions ranked the right file first, for an index built with either ONNX or torch. `eulix embed compare` still passes, because the vectors are finite and unit length.

**Cause, part 1 (not fixed).** The ROCm provider computes wrong vectors. Embedding the same texts with `BAAI/bge-base-en-v1.5` on CPU and on ROCm (onnxruntime 1.22.2):

| Path | Cosine to the CPU vector | Lowest similarity between different texts |
| --- | ---: | ---: |
| CPU, padded or not | 1.000 | 0.40 (normal) |
| ROCm, unpadded (the query path) | 0.53 to 0.62 | 0.94 (every text gets almost the same vector) |
| ROCm, padded to 128 (the indexing path on GPUs) | −0.56 to −0.47 | 0.67 |

Indexing on a GPU uses length bucketing, while questions are embedded one at a time without it. On ROCm the two paths are wrong in different ways, so even an index and queries both built on ROCm cannot match each other. Whether the fault lies in the onnxruntime build, the driver or the GPU is not known.

**Cause, part 2 (fixed).** The query server used to start without `-e` (engine) or `-d` (device), so it always used ONNX on the automatically chosen device, even with `engine = "torch"` in `eulix.toml`. Commit `6e1050a` makes `VectorWeaver` pass the configured engine. I confirmed that in the current code (`python main.py server -m <model> -e <engine>`). It still passes no device, so ONNX still picks ROCm when it is available.

**Why `embed compare` passes.** It checks structure only: matching ids and counts, finite values, no zero vectors, L2 norm near 1. A wrong but unit-length vector passes. What does reveal a broken index is the distribution of similarities between stored vectors: a ROCm-built index had 31.2% negative pairs (median cosine 0.88), a torch-built index had 0% (median 0.74). Real text vectors from bge almost never have a negative cosine.

**Evidence the query side matters.** With a torch-built index and the query server on CPU, a chunk's own text found its stored vector first for 12 of 26 samples (median rank 1 to 2); on ROCm the rank was random (161 to 2500). Raw vector search for two ETIYA questions moved from 1742nd to 2nd and from 493rd to 21st. Some questions remain hard ("secret token for the language model provider" still ranks the right function 919th).

**Workaround.** Use `engine = "ehnas"` in `[project]`, which reads no vectors.

**Fix direction.**
1. Pass a device as well as the engine when starting the query server, so queries use the same backend as indexing. (The engine half is done.)
2. Do not auto-select ROCm for ONNX until verified. Either default ONNX to CPU, or embed one short text on GPU and CPU at start-up and fall back to CPU with a warning if the cosine is below about 0.99.
3. Optionally make `embed compare` report the share of negative pairwise cosines on a sample and fail above a few percent.
4. Re-index afterwards and re-run the benchmark with `engine = "etiya"`.

**To check an index yourself** (run with the eulix virtual environment, or any Python with numpy):

```python
import struct, sys, numpy as np
b = open(sys.argv[1], "rb").read(); o = 8
n = struct.unpack_from("<I", b, o)[0]; o += 4 + n
cnt, dim = struct.unpack_from("<II", b, o); o += 8; quant = b[o]; o += 1
if quant:
    rec = np.frombuffer(b, dtype=[("s", "<f4"), ("q", "i1", (dim,))], count=cnt, offset=o)
    x = rec["q"].astype(np.float32) * rec["s"][:, None]
else:
    x = np.frombuffer(b, dtype="<f4", count=cnt * dim, offset=o).reshape(cnt, dim).copy()
x /= np.linalg.norm(x, axis=1, keepdims=True)
v = (x @ x.T)[np.triu_indices(cnt, 1)]
print(f"negative pairs {100 * (v < 0).mean():.1f}%  p5/p50/p95", np.percentile(v, [5, 50, 95]).round(3))
```

```sh
~/.Eulix/.venv/bin/python check.py .eulix/embeddings.bin
```

**To compare CPU with ROCm on the same texts:**

```python
import sys, os
sys.path.insert(0, os.path.expanduser("~/.Eulix/eulix_embed"))
from embedders.onnx_embed import EmbeddingGeneratorOnnx
texts = ["def chunk_file(path): ...", "fn hover(&self) -> Option<Hover>", "class CacheManager: stores previous answers"]
cpu = EmbeddingGeneratorOnnx("BAAI/bge-base-en-v1.5", device="cpu")._embed_batch(texts)
gpu = EmbeddingGeneratorOnnx("BAAI/bge-base-en-v1.5", device="rocm")
print("unpadded", (cpu * gpu._embed_batch(texts)).sum(1).round(3))
print("padded  ", (cpu * gpu._embed_batch(texts, fixed_len=128)).sum(1).round(3))
```

On a working GPU both lines print values close to 1.0.

---

## EMB-2: A repository still chooses which embedding model is downloaded

**Severity:** Medium · **Status:** Open · **Verification:** Carried over

Running arbitrary code from a downloaded model is closed off (see [Fixed Issues](Fixed-Issues.md)), but a repository's `eulix.toml` can still name any Hugging Face model, and it will be downloaded and loaded on the first semantic question. Pickle-format weights are only safe if torch's `weights_only` default applies (torch 2.6 and later). Consider an allowlist, or forcing safetensors. This follows from the trust problem in [MCP-1](CLI-MCP-And-TUI.md#mcp-1-a-repositorys-own-eulix-and-eulixtoml-are-trusted). **Workaround:** only query repositories you trust, or use the EHNAS engine, which loads no model.

---

## EMB-3: ROCm prints `(null): No such file or directory`

**Severity:** Low · **Status:** Limitation · **Verification:** Carried over

On ROCm systems you may see `(null): No such file or directory` at start-up. The ROCm runtime cannot find its `amdgpu.ids` file and prints the path as `(null)`. It is cosmetic and affects neither speed nor output. It is an upstream ROCm issue.

---

## EMB-4: Profiling the launcher misses the embedding worker's memory

**Severity:** Low · **Status:** Limitation · **Verification:** Carried over

`perf stat` or `time -v` on the `eulix embed` launcher measures a short-lived supervisor (about 0.2 s of CPU, 55 MB peak in one run), not the Python worker it starts. A "peak memory of the embedding pipeline" taken that way is meaningless. Attach the profiler to the worker process.

---

## EMB-5: Unused and unsafe `Embedder` methods

**Severity:** Low · **Status:** Open · **Verification:** Carried over

Only `VectorWeaver`, `EmbedQueryBinary` and `Close` are used. `IsAlive` likely reports a killed but not yet reaped process as alive. `BatchEmbedBatch` can exceed the 4 MB line limit of the reply reader, which would permanently break the connection. `BatchEmbed`, `Embed`, `VerifyConsistency` and `GetModelInfo` have no callers. Delete or fix them before anyone uses them.

---

## EMB-6: Embedded text has no line breaks

**Severity:** High · **Status:** Open · **Verification:** Reproduced

**Symptom.** Every chunk "card" is a single line: `// File: app/models.py// Function: helper// Lines: 10-11// Complexity: 0def helper()`. Headers run into each other and into the code.

**Cause.** The formatters (`chunking/formatters.py`) write each line with `buf.write`, which adds no newline.

**Impact.** The embedding model sees one run-on string, which blurs the structure the cards were designed to show. It probably lowers ETIYA retrieval quality, though I did not measure it.

**Fix direction.** Write `line + "\n"` in each formatter. Re-run `eulix analyze` afterwards, since existing vectors were made from the old text. Re-run the retrieval benchmark to measure the change.

---

## EMB-7: Cards omit calls, complexity, exceptions, imports and docstrings

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced

**Symptom.** The formatters are designed to include outgoing calls, callers, complexity, control flow, exceptions and file imports. In practice cards hold only name, line range, signature, parameters, return type, class bases and method names, and every function says `Complexity: 0`.

**Cause.** The knowledge base the parser writes is a slim view with those fields removed (v0.7.7), and the chunker also deliberately deletes docstrings before building cards.

**Impact.** The meaning in the vectors comes from names and signatures only. That helps queries that share words with identifiers and hurts conceptual queries. This may contribute to the weak ETIYA results in [EMB-1](#emb-1-onnx-on-rocm-produces-wrong-embeddings).

**Fix direction.** Decide which fields embeddings should use. Either have the parser emit them (for example, docstrings and a short call list), or have the chunker read `kb_call_graph.json`. Keep docstrings unless an evaluation shows they hurt.

---

## Related Docs

- [Embeddings (eulix-cli)](../eulix-cli/Embeddings.md): the query-time embedder.
- [Engines](../eulix-cli/query/retrieval/Engines.md): which engine needs vectors.
- [Fixed Issues](Fixed-Issues.md): embedding fixes already shipped.
