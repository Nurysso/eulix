# eulix_embed — Architecture

> Python/PyTorch implementation of the eulix embedding system.
> Version: 0.3.8 · License: Apache-2.0 · Maintainer: Dawood (Nurysso)
> Updated Date: Jul 21, 2026

---

## 1. Mission & Scope

`eulix_embed` turns a structural knowledge base (a single JSON file emitted by
`eulix_parser`) into a searchable embedding store. It is the _retrieval-side_
half of the eulix pipeline: the parser extracts structure, the embedder
vectorises it, and downstream search / RAG / agent layers consume the vectors.

### What this build is

- A **general, cross-platform** embedder. If PyTorch runs there, this script
  runs there — Linux, Windows, macOS, WSL2, Docker, CI runners, laptops,
  workstations, headless servers.
- One-file CLI with three working modes (`embed`, `query`, `serve`) plus
  diagnostics (`compare`, `ijson-backend`, `version`).
- A pure-Python dependency surface (`ijson`, `numpy`, `torch`, `transformers`,
  `tqdm`, optional `orjson`, optional `sentence-transformers`) — no native
  compilation step required beyond PyTorch itself.

### What this build is **not**

This is the **portable baseline**. Performance-critical, vendor-specific builds
(MIGraphX/AMD, TensorRT/NVIDIA, MPS-CoreML/Apple, ONNX-Runtime, IPEX/Intel,
TPU/XLA) will live as **separate specialised executables** that share the same
KB / `.bin` contract. They will _not_ be added as feature flags inside this
file — vendor SDKs leak into each other, bloat the dependency graph, and turn
"install" into a yak-shave. The split is deliberate; see [#11](#11-compute-platform-strategy)

### Design Philosophy

The Python implementation prioritises:

- **Memory efficiency** through single-pass streaming and `dataclass(slots=True)`
- **Portability** through PyTorch instead of vendor-specific SDKs
- **Simplicity** through a single-file implementation with clear CLI interface
- **Performance** through bucketing, parallel chunking, and optional quantization

---

## 2. High-Level Pipeline

```
              ┌──────────────────────────────────────────────────────┐
              │                knowledge_base.json                   │
              │     (metadata, structure, call_graph, dep_graph)     │
              └─────────────────────┬────────────────────────────────┘
                                    │  ijson.parse() — one file_struct at a time
                                    ▼
   ┌────────────────────────────────────────────────────────────────────┐
   │  STAGE 1+2  (single pass, parallel chunk workers)                   │
   │   • _stream_kb() yields ('meta', key, value)  and                  │
   │     ('structure', file_path, file_struct)                          │
   │   • ThreadPoolExecutor (N_WORKERS = min(4, cpu_count))             │
   │     feeds _drop_docstrings() → chunk_one_file()                    │
   │   • MAX_INFLIGHT=32 deque caps in-flight work to bound RAM         │
   │   • seen_ids set deduplicates chunk IDs across the whole KB        │
   └────────────────────────────────────┬───────────────────────────────┘
                                        │  List[Chunk]
                                        ▼
   ┌────────────────────────────────────────────────────────────────────┐
   │  STAGE 3+4  (embed + stream-to-disk, fused)                        │
   │   • EmbeddingGenerator._embed_batch() — PyTorch forward pass       │
   │   • Bucket-by-estimated-token-count → GPU/MPS friendly padding     │
   │   • yield (chunk_id, vec) one batch at a time                      │
   │   • save_embeddings_bin() writes them in original order             │
   └────────────────────────────────────┬───────────────────────────────┘
                                        │
                                        ▼
              ┌──────────────────────────────────────────────────────┐
              │  embeddings.bin (v4, float32 or SQ8 int8)            │
              │  vectors.bin     (v4, chunk-id index)                │
              │  embeddings.json (optional, --save-json)             │
              └──────────────────────────────────────────────────────┘
```

The two stages are fused (`STEP 1+2`, `STEP 3+4`) because the KB file is never
needed again once chunks are produced there is no reason to keep the source
on disk or in RAM during embedding.

---

## 3. The Big Architectural Bets

These are the decisions that shaped everything else. They are listed in rough
order of "how much RAM/time you save if you keep them in mind".

### 3.1 Single-pass streaming (the headline win)

**Decision:** read `kb.json` exactly once via `ijson.parse()` and stream
`file_struct` objects to chunk workers. Do **not** build a separate
`load_kb_metadata()` + `stream_kb_structure()` two-pass pipeline.

**Why:**

- A 4.6 GB KB(Orignal parser output size on linux kernel) file costs ~9.2 GB of disk I/O and ~10–20× RAM overhead when
  loaded into Python `dict`s. The Rust original is fine because Rust strings
  are 24 bytes and the structures are arena-allocated; Python `dict[str, Any]`
  is a memory disaster.
- Every key we do not need (`call_graph`, `dependency_graph`, `patterns`,
  `entry_points`) is skipped at the parser level(Stored in seprate files) — never materialised.
- Peak RAM ≈ max(one file_struct, one edge dict). On a 4.6 GB file that
  collapsed ~14 GB working set to ~600 MB.
- The KB is parsed top-down: small top-level keys (`metadata`, structure-root)
  are buffered because they fit in RAM; the `structure` subtree is streamed
  because it does not.

**Trade-off accepted:** you cannot jump to "the 80% mark" without a separate
index. That is fine — re-running the embedder is cheap relative to embedding
itself.

### 3.2 PyTorch, not ONNX Runtime

**Decision:** use `transformers.AutoModel` + `torch`, not `onnxruntime`.

**Why:**

- HuggingFace gated / dynamic-graph models (Jina v2, Nomic, custom fine-tunes
  with `trust_remote_code`, `attn_implementation="eager"`) **require** the
  HF modelling code. ONNX export breaks on most of them.
- Model swapping is the dominant use case here. `AutoModel.from_pretrained`
  covers ~95% of the model zoo without additional compilation steps.
- The cost is "slower than ORT by 10–25% on stock hardware". That is the
  tax we pay for portability — and exactly the tax the upcoming
  vendor-specific builds will reclaim (see [#11](#11-compute-platform-strategy))

### 3.3 `dataclass(slots=True)` for every hot-path object

**Decision:** every per-chunk dataclass uses `slots=True` on Python 3.10+;
silently falls back to classic dataclasses on 3.9.

**Why:**

- A 4-million-chunk KB has 4 M `Chunk` instances. With `__dict__`, each carries
  a per-instance `dict` (~104 bytes empty on CPython 3.10). With `__slots__`,
  that drops to zero.
- Empirically: ~40% lower peak RAM on the chunk-holding list alone. Combined
  with streaming, the embedded working set stays well under what a 4 M chunk
  corpus would otherwise need.
- We pin Python to 3.10–3.11 (see (see [#3.13](#313-python-310311-pin)).) specifically because 3.10 introduced
  `slots=True` on dataclasses without a metaclass.

### 3.4 Bucketing (mirror of the Rust original)

**Decision:** group chunks by estimated token count (`len(content) // 4`) into
fixed buckets (`[32, 64, 128, 192, 256, 384, 512]`, extended for Jina), then
run one `_embed_batch(fixed_len=blen)` per bucket.

**Why:**

- Naïve batching pads every sequence in a batch to the longest. If you mix
  a 10-token function name with a 500-token class body, you pad the short one
  to 500. That is 50× wasted compute.
- Bucketing guarantees `padding="max_length"` only fills to the bucket, so
  every sequence in a batch is roughly the same length.
- GPU kernels (cuBLAS, MIGraphX, MPS) hit high utilisation when shapes are
  predictable. Bucketed shapes compile to the same kernel each time → fewer
  recompiles → higher steady-state throughput.
- Bucketing is opt-in (`use_bucketing=True`) and _only_ enabled on
  `cuda`/`mps`. On CPU the padding overhead is amortised differently and
  bucketing hurts more than it helps — see the gate in `EmbeddingGenerator.__init__`.

### 3.5 `MAX_INFLIGHT = 32` (bounded producer–consumer)

**Decision:** in `process()`, the streaming parser feeds a deque of chunking
futures. The deque is capped at 32; once full, the main thread drains the
oldest futures back to 16 before submitting more.

**Why:**

- ijson parsing is the producer. Chunking (with docstring stripping + dataclass
  materialisation) is the consumer. Without a bound, the producer can
  outrun the consumer by thousands of futures → unobserved RAM growth.
- 32 is empirically the smallest bound that hides producer/consumer jitter on
  NVMe + 4 workers; smaller values stall the parser, larger values trade RAM
  for no measurable speedup.
- The "drain to half" pattern (instead of "drain to zero") gives the producer
  a runway to keep moving while the consumer catches up.

### 3.6 ThreadPool size = `min(4, cpu_count)`

**Decision:** chunking uses at most 4 worker threads, regardless of how many
cores the box has.

**Why:**

- Chunking is dominated by short, bursty work — string slicing, dataclass
  construction, dict mutation. It is not compute-bound; it is
  allocator-bound.
- CPython's GIL is released for I/O and for many C-accelerated operations,
  but `dict` mutations and `str` slicing inside pure-Python helpers keep the
  GIL engaged more than you'd expect.
- Empirically, 4 workers fully saturates `chunk_one_file()` on a Ryzen 7 /
  M2 / Xeon Silver; more workers add contention without throughput.
- The _embedding_ stage is single-process (PyTorch already uses every core
  via MKL/oneDNN), so chunking parallelism and embedding parallelism do not
  compete for cores.

### 3.7 Streaming embeddings → disk (no in-RAM vector dict)

**Decision:** `generate_vectors_streaming()` yields `(chunk_id, vec)` tuples
one batch at a time. `save_embeddings_bin()` writes them directly. There is
**no** `Dict[str, np.ndarray]` held in RAM between embedding and disk.

**Why:**

- The original Rust binary did `vectors.insert(id, vec)` into a `HashMap`
  first, then wrote. In Python, that would mean holding the entire 384-d
  float32 corpus (~1.5 GB per million chunks) in RAM just to write it out.
- Streaming keeps peak embedding-stage RAM at _one batch_ (e.g. 64 chunks ×
  384 × 4 B ≈ 100 KB) regardless of corpus size.
- The "write in original chunk order" requirement is satisfied by sorting
  `(orig_idx, vec)` once after bucketing — O(N log N) sort of ints, not
  of vectors.

### 3.8 SQ8 int8 quantization (opt-in)

**Decision:** `--quantize` flips each float32 vector to int8 + a per-vector
scale, written into the same `embeddings.bin` v4 layout.

**Why:**

- `embeddings.bin` is usually the biggest artifact on disk. At 384 dims,
  float32 → 1.5 KB/vector; SQ8 → 0.4 KB/vector (4× shrink).
- Quality cost is ~1% on retrieval recall for typical code corpora. The
  per-vector scale preserves dynamic range; absolute similarity scores
  shift, but top-k ordering is preserved.
- Format version 4 reserves a 1-byte quant flag right after `dimension`,
  so v3 readers degrade gracefully (float32-only) and v4 readers know
  whether to read 4 bytes (scale) + `dim` bytes (int8) or `dim*4` bytes
  (float32).
- Quantization is **opt-in**, not default, because the dequantise step
  shifts raw cosine values — and a downstream consumer doing raw L2 distance
  comparisons needs to know to dequantise first. The format makes both
  states self-describing.

### 3.9 Binary v4 format with explicit model_name

**Decision:** `embeddings.bin` and `vectors.bin` carry the model name as a
length-prefixed UTF-8 string in the header, alongside magic, version, count,
dimension, and (v4+) the quant flag.

**Why:**

- A search-time reader has to know the model to know the dimension, the
  expected similarity metric, and whether SQ8 decode is needed. Storing the
  model in the file means downstream tools can validate compatibility
  without an external sidecar.
- Magic bytes (`EULX`) make the format self-identifying in `file(1)` and
  hex dumps — important when a stray file ends up in the wrong directory.
- The version field is forward-compatible: v3 → v4 is purely additive
  (one byte), so old readers still parse new files (as float32).
- Format docs live in the `save_embeddings_bin` / `save_vectors_bin`
  docstrings rather than only in this file — code is the source of truth.

### 3.10 Long-lived `serve` mode (Experimental not ready yet)

**Decision:** a third subcommand runs an NDJSON-over-stdin/stdout server that
keeps the model resident and handles one request per line.

**Why:**

- Cold-loading a `sentence-transformers/all-MiniLM-L6-v2` model is ~3–8 s on
  CPU, ~1–3 s on a discrete GPU. Spawning a fresh Python process per query
  means paying that cost on every call.
- A long-lived server amortises model load across thousands of queries.
  Throughput on a single query drops from ~0.3 q/s (cold spawn) to hundreds
  per second (warm).
- The protocol is deliberately boring — one JSON object per line, no
  keep-alive frames, no binary frames, no TLS. The transport is whatever
  the caller wants (Unix socket, named pipe, TCP, subprocess pipe). Security
  is the caller's problem; this script is a leaf tool, not a service.
- `{"shutdown": true}` is the only control plane. There is no auth, no
  rate limit, no telemetry — those belong in a wrapper that fronts this
  process.

### 3.11 `orjson` when available, stdlib `json` when not

**Decision:** detect `orjson` once at import, bind `_json_dumps_indent` and
`_json_dumps_no_indent` accordingly. Everything else uses a tiny wrapper,
`_json_dumps(obj, *, indent=False)`.

**Why:**

- `orjson` is 5–10× faster than `json` on serialisation, with zero accuracy
  loss for our payload shape (lists, dicts, strings, ints, floats, bools).
- It is optional. On a clean machine, `pip install` shouldn't fail because
  someone forgot to ship a CFFI backend. The wrapper degrades silently.
- Both code paths are pre-bound at import — no per-call branch on
  `HAS_ORJSON`. The branch cost matters at the rate we serialise progress
  messages in `serve` mode.

### 3.12 Disk-space pre-flight (Experimental not ready yet)

**Decision:** `_check_disk_space()` estimates required bytes from chunk count
× (dim bytes + ID overhead) × 1.2 safety margin, and `sys.exit(1)`s if the
output volume will not fit.

**Why:**

- People run this on laptops with 256 GB SSDs and 8 M-chunk corpora. There
  is no graceful failure halfway through writing `embeddings.bin` — you just
  fill the disk and the OS kills the writer.
- The estimate is cheap: a quick ijson scan counts IDs in the first 1000
  file_structs, then extrapolates from file size. Fallback heuristic is
  "1 chunk per 10 KB of KB JSON" if the scan fails.
- It surfaces the choice _before_ the slow work starts. A user with
  insufficient disk space can switch to `--quantize` (4× shrink) and retry
  instead of discovering the problem 30 minutes in.

### 3.13 Python 3.10–3.11 pin

**Decision:** `check_python_version()` rejects Python <3.10 and ≥3.12 with a
loud ASCII banner and a non-zero exit code.

**Why:**

- `dataclass(slots=True)` requires Python 3.10. Without it we lose ~40% of
  our per-chunk RAM savings [#3.3](#33-dataclassslotstrue-for-every-hot-path-object) — a regression we will not silently
  accept.
- Python 3.12+ breaks a few transitive pins we have hit in practice
  (torch + transformers ABI churn, sentence-transformers lazy-import paths).
  Until that stabilises we treat 3.12 as a known-bad range, not a banned
  range.
- The error message tells the user exactly how to recreate the env with
  `uv venv --python 3.10`. This is the only place we talk to the user with
  a "you're holding it wrong" voice, and we do it deliberately.

### 3.14 Embedding generators as objects, not modules

**Decision:** `EmbeddingGenerator` and `EmbeddingPipeline` are classes even
though neither holds much mutable state beyond the model itself.

**Why:**

- A second model in the same process (`serve` mode held alongside a search
  ranker, or unit tests) needs two generators. Modules force globals.
- Subclassing `EmbeddingGenerator` is the extension point used by the
  upcoming compute-platform-specific builds ([#11](#11-compute-platform-strategy)). They override
  `_embed_batch()` and inherit everything else — bucketing, the streaming
  protocol, the binary format, the CLI.

---

## 4. Data Flow in Detail

### 4.1 Stage 1+2 — KB → Chunks

1. `_stream_kb(kb_path)` opens the file as bytes and iterates
   `ijson.parse(fh, use_float=True)`. Every event is a `(prefix, event, value)`
   triple.
2. Top-level `map_key` events flip us into one of three modes:
   - **collect** (`metadata`, etc.): an `ijson.ObjectBuilder` accumulates the
     subtree until it closes. Result is yielded as `('meta', key, value)`.
   - **stream** (`structure`): each inner `map_key` becomes a new file path.
     A fresh `ObjectBuilder` collects that one file's subtree, yields
     `('structure', file_path, file_struct)`, then resets.
   - **skip** (everything else, e.g. `call_graph`): the parser consumes the
     events but we do nothing with them. They never become Python objects.
3. The main thread submits each `file_struct` to a `ThreadPoolExecutor`.
   Each worker calls `_drop_docstrings()` (in-place mutation, safe because
   the closure owns the dict) then `chunk_one_file()`.
4. Workers return `List[Chunk]`. The main thread harvests them, appends to a
   shared `chunks` list, and adds IDs to a `seen_ids` set for global dedup.

### 4.2 Stage 3+4 — Chunks → `embeddings.bin`

1. `EmbeddingGenerator.generate_vectors_streaming()` is a generator. The
   consumer (the `save_embeddings_bin()` helper) pulls embeddings as fast as
   it can write them.
2. Chunks are bucketed by estimated token count. Within a bucket they are
   processed in batch-sized chunks. Across buckets, the order is sorted
   (smallest bucket first) — this happens to align long-tail files last,
   which gives the best UX for the progress bar (it moves quickly at first
   and slows down only at the end).
3. Each batch goes through `_embed_batch(texts, fixed_len=blen)`. For the
   `sentence-transformers` backend this sets `max_seq_length`; for the HF
   AutoModel path this passes `max_length` and `padding="max_length"` to
   the tokenizer.
4. Results are accumulated as `(orig_idx, vec)` tuples, sorted by `orig_idx`
   to restore chunk order, then yielded to the disk writer.
5. `save_embeddings_bin()` writes the v4 header, then per-vector records.
   For SQ8 it stores the scale as a `float32` before the `int8` vector bytes.
6. `save_vectors_bin()` writes the same chunk IDs in the same order, so
   search-time code can use position-in-vectors.bin as the vector index in
   embeddings.bin without scanning.

### 4.3 Memory profile (4.6 GB KB → ~3.5 M chunks, MiniLM-L6, dim 384)

| Stage               | Peak RAM | Notes                                |
| ------------------- | -------: | ------------------------------------ |
| KB parse (ijson)    |  ~200 MB | One file_struct at a time            |
| Chunk worker pool   |  ~400 MB | 32 in-flight × ~12 KB file_struct    |
| Final `chunks` list |  ~1.5 GB | 3.5 M chunks × ~430 B/slot           |
| Embedding + write   |  ~1.7 GB | Chunks + model + 1 batch of vectors  |
| Post-write (final)  |  ~1.5 GB | Same chunks; vectors flushed to disk |

The old two-pass Python implementation peaked at ~14 GB on the same input.
That is the headline number the single-pass design buys us.

---

## 5. Chunking Semantics

`chunk_one_file()` produces, per source file:

- One `Chunk(chunk_type=function)` per top-level function.
- One `Chunk(chunk_type=class)` per class (the class overview, not its body).
- One `Chunk(chunk_type=method)` per method, with the parent class name
  prepended to the method name (`MyClass.__init__`).
- One `Chunk(chunk_type=file)` for the file summary, keyed `file:<path>`.

Every chunk carries a `ChunkMetadata` with file path, language, line range,
name, and (for functions/methods) cyclomatic complexity. Tags include the
base tag (`function`/`method`), `async` if applicable, the parser-supplied
extra tags, `complex` if `complexity > 10`, and `api`/`test` derived from
decorators. Importance score comes from the parser (`importance_score`,
defaulting to 0.5; class overviews use a fixed 0.7).

Comments and docstrings are **stripped in the worker thread before chunking**
(see `_drop_docstrings`). The whole `clean_content()` pipeline (comment
strip + boilerplate strip) is currently wired but not invoked — it sits as
a hook for the upcoming `--remove-comments` CLI flag, which is parsed but
not yet applied to the chunker. The data flow is correct; the wiring is the
last 5% of the feature.

---

## 6. Binary Format — Normative Spec

### 6.1 `embeddings.bin` (v4)

```
offset  size  field           type           notes
──────  ────  ──────────────  ─────────────  ───────────────────────────
0       4     magic           bytes          literal "EULX"
4       4     version         uint32 LE      4 (= BINARY_VERSION)
8       4     model_name_len  uint32 LE      bytes that follow
12      ml    model_name      utf-8 bytes
12+ml   4     count           uint32 LE      number of vectors
16+ml   4     dimension       uint32 LE      vector length
20+ml   1     quant_flag      uint8          0 = float32, 1 = SQ8

— per vector, count times —
        4     id_len          uint32 LE      ≤ 0xFFFF
        il    id              utf-8 bytes
        if quant_flag == 0:
            dim*4   vector    float32 LE × dim
        if quant_flag == 1:
            4       scale     float32 LE
            dim     qvec      int8 × dim      (signed; cast on read)
```

Total size = `20 + ml + count * (4 + il + (dim*4 or dim+4))`.

### 6.2 `vectors.bin` (v4)

```
offset  size  field           type           notes
──────  ────  ──────────────  ─────────────  ───────────────────────────
0       4     magic           bytes          "EULX"
4       4     version         uint32 LE      4
8       4     model_name_len  uint32 LE
12      ml    model_name      utf-8 bytes
12+ml   4     count           uint32 LE      number of IDs
— per ID, count times —
        4     id_len          uint32 LE      ≤ 0xFFFF
        il    id              utf-8 bytes
```

Position `i` in `vectors.bin` (zero-indexed by ID order) is the index of
that vector in `embeddings.bin`. Search-time readers load both, map
`query → vector`, then `vectors.bin` is a plain int→string lookup.

### 6.3 Versioning rules

- Bumping `BINARY_VERSION` is **breaking** — readers must refuse unknown
  major versions.
- Adding fields to the header (like the v4 quant flag) is **additive** if
  readers can detect them via version. We do that: v3 readers see a v4 file,
  read past the extra byte by reading exactly `count * (dim*4)` per vector,
  and silently ignore the scale. Quality loss: zero, since they ignore
  float32 data they cannot interpret. Wait — they _do_ interpret it, as
  float32 vectors of dimension `dim`, because the count in the file is the
  true count. v4 needs the reader to _also_ know to skip the scale byte.
  In practice we enforce v3-or-v4 at the loader.

---

## 7. CLI Surface

| Subcommand      | Purpose                                             | Notes                                 |
| --------------- | --------------------------------------------------- | ------------------------------------- |
| `embed`         | Full KB → embeddings.bin pipeline                   | Default if no subcommand given        |
| `query`         | One-shot embed a string, print JSON or binary       | Useful for sanity-checking a model    |
| `serve`         | Long-lived NDJSON stdin/stdout server               | Avoids per-call model reload          |
| `compare`       | Header sanity check on embeddings.bin + vectors.bin | Does _not_ compare vectors pairwise   |
| `ijson-backend` | Report which ijson C backend is active              | "yajl2_c" is what you want            |
| `version`       | Version + Python + ijson backend                    | `--short` for just the version string |
| `-h` / `help`   | Extended help per subcommand                        | Generated by `print_help()`           |

The CLI is intentionally **boring** — no config files, no env vars (beyond
`HF_TOKEN` if you point at a gated model), no plugin discovery, no telemetry.
Configuration goes on the command line or in your shell history. If you want
a config file, wrap this script in `make`.

---

## 8. Trade-offs & Known Limitations

| Decision                      | Cost                                       | Why we accept it                                                               |
| ----------------------------- | ------------------------------------------ | ------------------------------------------------------------------------------ |
| Single-pass streaming         | No random access into the KB after parse   | Re-running is cheap; RAM is the constraint                                     |
| No persistent chunk cache     | Re-chunk on every run                      | Chunks are deterministic from the parser                                       |
| `serve` mode has no auth      | Anyone with the pipe can embed             | This is a leaf tool, not a service                                             |
| Python 3.10–3.11 pin          | Blocks users on 3.12+                      | Avoids `slots=True` regression + torch ABI churn                               |
| HF AutoModel (vs ORT)         | ~10–25% slower per token on stock hardware | Portability; vendor builds reclaim this ([#11](#11-compute-platform-strategy)) |
| ijson pure-python fallback    | ~10× slower than `yajl2_c`                 | Installer simplicity; user can opt in                                          |
| ThreadPool capped at 4        | Sub-linear on 16+ core boxes               | Chunking is allocator-bound, not compute-bound                                 |
| No multiprocessing            | Single-process GIL                         | Embedding is already multi-threaded inside PyTorch                             |
| In-place `_drop_docstrings()` | Requires per-worker dict ownership         | Already guaranteed by the worker closure                                       |

The Python version pin, the lack of auth, and the no-multiprocessing choice
are the three a future maintainer is most likely to want to "fix". Resist
the urge on the version pin until torch + transformers stabilise on 3.12.
Resist the urge on auth — add it in a wrapper. Resist the urge on
multiprocessing unless you can show chunking is the bottleneck, which on
real hardware it never is.

---

## 9. Performance Expectations

On commodity hardware (Ryzen 7 / M2 / Xeon Silver, NVMe SSD, MiniLM-L6-v2):

- **KB scan + chunking**: 4–8 K source files/sec, dominated by ijson event
  dispatch and `dataclass` allocation. Pure I/O after the first ~1 GB
  because the kernel readahead window catches up.
- **Embedding (CPU, MPS)**: 50–200 chunks/sec depending on bucket
  distribution. Long-tail class bodies dominate wall time.
- **Embedding (CUDA, bucketed)**: 800–3 000 chunks/sec on a consumer GPU,
  5–15 K on a workstation card. Bucketed shapes hit steady-state kernels
  fast.
- **Disk write**: 200–600 MB/sec on NVMe for `embeddings.bin` (float32);
  60–150 MB/sec for SQ8 (more syscalls per byte). Disk is rarely the
  bottleneck.

These numbers are for guidance, not SLA. The upcoming vendor-specific builds
target 3–10× on their native hardware.

---

## 10. Extension Points

If you are forking this embedder (for a domain-specific corpus, a custom
model, a tighter security model), here is where to plug in:

| Goal                                      | Where to change                                                                |
| ----------------------------------------- | ------------------------------------------------------------------------------ |
| Custom chunker semantics                  | `chunk_one_file()` — same `Chunk` shape, different body                        |
| Add a new bucket scheme                   | `BUCKETS_STANDARD` / `BUCKETS_JINA`, `snap_to_bucket()`                        |
| Different binary format                   | `BINARY_MAGIC`, `BINARY_VERSION`, save/load functions                          |
| Custom model with a special path          | Subclass `EmbeddingGenerator`, override `_embed_batch()`                       |
| Plug in vendor SDK (CUDA/TPU/…)           | Subclass `EmbeddingGenerator`, override `_embed_batch()`; keep everything else |
| Skip a top-level KB key                   | `_stream_kb()` — adjust `COLLECT_KEYS` set or add a skip branch                |
| Replace disk-space check                  | `_check_disk_space()` — keep signature, swap math                              |
| Wire `--remove-comments` into the chunker | Apply `clean_content()` inside `_drop_docstrings()`                            |

The CLI parser is the only part that is intentionally not pluggable. Adding
flags is cheap; the cost is in the help text staying readable, which we
already pay in the per-subcommand detail blocks.

---

## 11. Compute Platform Strategy

This file ships the **general cross-platform embedder**. It is the lowest
common denominator — PyTorch, HF AutoModel, ijson, numpy. It runs on every
hardware + OS combination PyTorch supports. Performance is "good enough",
not optimal.

Upcoming **compute-platform-specific builds** (separate executables, same
binary contract) will replace the hot path only:

| Platform               | Subclass target                                       | Expected speedup          |
| ---------------------- | ----------------------------------------------------- | ------------------------- |
| NVIDIA CUDA + TensorRT | `EmbeddingGenerator._embed_batch()` → TensorRT engine | 1.5–3× over PyTorch eager |
| AMD ROCm + MIGraphX    | Same hook, MIGraphX parser/quantizer                  | 1.5–3× over stock ROCm    |
| Apple MPS + CoreML     | `embeddingViaCoreML` for static models                | 1.3–2× on M-series        |

<!-- MOST PROPBALLY NOT -->
<!-- | Intel IPEX             | `torch.xpu` + IPEX fusion                             | 1.2–1.8× on Xeon / Core Ultra      | -->
<!-- | ONNX Runtime (CPU)     | For air-gapped Linux servers w/o torch                | ~equal to torch-CPU, smaller image | -->
<!-- | TPU / XLA              | `torch_xla` branch                                    | 3–10× on v5e/v5p pods              | -->

These will **not** be feature flags inside this file. The reasons:

1. **Dependency surface explodes.** TensorRT wants `pycuda` + a CUDA toolchain.
   MIGraphX wants ROCm + AMDMIGraphX. CoreML wants macOS 13+ + `coremltools`.
   Bundling them as optional extras means `pip install eulix_embed[all]` is a
   2 GB download that fails on half the platforms it claims to support.
2. **The tests diverge.** Each backend has its own numerics quirks. SQ8 with
   MIGraphX is _not_ bit-identical to SQ8 with TensorRT. We need per-backend
   golden vectors in CI, not one CI matrix with conditional skips.
3. **The CLI diverges.** Each backend wants its own flags (`--trt-engine-path`,
   `--migraphx-fp16`, `--coreml-compute-units`). Stuffing them into one
   `--help` makes it unreadable.
4. **The failure modes diverge.** TensorRT engine build is slow and can fail
   at load time. MIGraphX wants pre-parseable models. None of this should be
   in the path of someone who just wants `pip install` and go.

So: **this script is the baseline.** The vendor builds are siblings that
import or re-implement the same `Chunk` shape, the same bucketing, the same
binary format, and the same CLI subcommands. They swap `_embed_batch()` and
load a different embedding model class. Everything else — chunking, streaming,
serve mode, disk check, format versioning — is shared.

If you are reading this from a vendor-specific build's repo, the architectural
decisions in §3 still apply unless that build's own ARCHITECTURE.md says
otherwise.

---

## 12. What a Maintainer Should Read First

If you are picking this up cold, read in this order:

1. `main()` and `_build_parser()` — the CLI surface is the entry point and
   it tells you which modes exist.
2. `cmd_serve()` — the protocol document for the long-lived server.
3. `_stream_kb()` and the `process()` loop — the single-pass design is the
   load-bearing idea; everything else hangs off it.
4. `EmbeddingGenerator._embed_batch()` — the model call. Everything around it
   is plumbing.
5. `save_embeddings_bin()` docstring — the format spec is normative; this
   file is the explainer.

If you change anything in §3, update this document in the same commit. The
code is the source of truth for _what_; this document is the source of truth
for _why_. Keep them in sync.

---

_End of architecture.md_
