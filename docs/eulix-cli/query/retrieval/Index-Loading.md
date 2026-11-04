# Retrieval Index Loading

This page explains how the retrieval engine turns the files in `.eulix/` into in-memory structures it can search quickly.

> **Crate / path:** `eulix-cli/internal/query/retrieval/` (`loader.go`, `kb.go`, `engine.go`, `boilerPlate.go`, `types.go`)  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

When the retrieval engine starts, it reads `kb.json`, `kb_call_graph.json` and, for ETIYA, `embeddings.bin` and `vectors.bin`. From these it builds the structures every search uses: a flat list of **chunks**, a symbol index, an inverted keyword index, a boilerplate detector, a directory tree, a call-graph lookup, and (ETIYA) a matrix of vectors. This happens once per process.

## Why It Exists

Searching needs random access to every chunk and its words. Building the right indices once, up front, is what lets each question run in milliseconds.

## How It Works

### Chunks

A **chunk** is the unit of retrieval: one function, one method, or one class. For each, `addChunksFromFile` builds a `Chunk` with an ID, name, file, line range, importance, a list of symbols (names it defines and calls), and a readable text made from the signature, docstring and call relationships. A class chunk also lists its methods and has importance 0.95, a little above functions, because of its structural weight.

### Streaming load of `kb.json`

The knowledge base can be several gigabytes. The old design decoded it fully into a large structure and then copied it into chunks, which could need 8 to 12 GB for a 4 GB source tree and run out of memory on 16 GB machines. The current design (`streamKBChunks`) never builds the whole structure:

- It memory-maps the file with sequential-read hints ([Mmap](Mmap.md)).
- It walks the JSON token by token with the standard decoder, which supports streaming.
- For each file entry, it decodes just that entry with the fast Sonic decoder, turns it into chunks, and lets the decoded data be freed before the next one.

The comment records an estimated peak of 6 to 8 GB for the same case, down from 8 to 12. Because the streaming path does not keep the source structure, chunk text is always held in memory (lazy text loading is turned off).

### Derived indices

`buildDerivedIndices` makes a single pass over the chunks and builds:

- the **boilerplate detector**: symbols whose document frequency exceeds a threshold that adapts to the repository size (used to avoid pinning or rewarding generic names);
- the **symbol index**: symbol name to chunk positions;
- the **set of files** that have chunks (used by the path gate);
- the **inverted index**: each word, split on identifier boundaries so `snake_case` and `camelCase` are separate words, mapped to the chunks that contain it, skipping boilerplate words. BM25 uses it.

The subsystem tree ([Search Pipeline](Search-Pipeline.md)) and the noise-path list are built at load time too.

### Call graph

`loadAndIndexCallGraph` decodes `kb_call_graph.json` and keeps only edges of type `call`. Each edge becomes two relationships: `calls` on the caller and `called_by` on the callee, both with distance 1. If the file is missing, the engine still works but without expansion, and logs it.

### Vectors (ETIYA only)

`embeddings.bin` has a header (magic `EULX`, version 5, the model name, a count and a dimension, and a flag for quantization) followed by the vectors. In the quantized form, which `analyze` produces, each vector is stored as a 4-byte scale followed by one signed byte per dimension; the loader multiplies back to floats and normalizes. The matrix is one flat allocation, aligned to 2 MiB huge pages on Linux. `vectors.bin` carries the chunk IDs in the same order, so a chunk ID can be mapped to its vector row. If it cannot be read, the engine rebuilds that mapping in memory from the chunk list.

If there are more than 50,000 vectors, an IVF index (256 clusters, five k-means iterations) is built **in the background** after loading, so the engine can answer immediately and switches to the faster search when it is ready. Until then it scans every vector.

### Degradation

A missing `embeddings.bin` makes ETIYA fall back to keyword-only retrieval and logs it. A missing call graph disables expansion. A failure to load chunks is fatal.

### Main Components

| Item | Responsibility |
| --- | --- |
| `loadChunks`, `streamKBChunks` | Read `kb.json` into chunks. |
| `addChunksFromFile`, `buildChunkFromKBFunction`, `buildChunkFromKBClass` | Build chunks. |
| `buildDerivedIndices`, `buildInvertedIndex` | Indices. |
| `loadAndIndexCallGraph` | Call-graph lookup. |
| `loadEmbeddings`, `loadVectorMap`, `buildIVFIndex` | ETIYA vectors. |
| `logFileLoad` | Logs the time and memory change of each load. |

### Data In, Data Out

In: files in `.eulix/`. Out: a ready `ContextBuilder`. The load log (`context_debug.log`) shows each file's load time and resident-memory change.

## Key Algorithms

### Streaming by file entry

**The problem.** Holding both the parsed JSON and the chunk list in memory doubles the peak.

**The approach.** Decode and convert one file entry at a time.

**Why this way.** The peak is then about the size of the chunks and indices, which are needed anyway.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `project.engine` | `eulix.toml` | `etiya` | Whether vectors are loaded at all. |
| `inverted_index_min_chunks` | `[retrievalConfig.ehnas]` | 5000 | EHNAS builds its extra inverted index only above this size. |
| IVF threshold, clusters, iterations | constants | 50,000 / 256 / 5 | Not configurable. |

## Usage Examples

With debugging on, the first lines of `.eulix/debug/context_debug.log` look like: `CONTEXT-BUILDER INITIALIZED (engine=etiya): N chunks, M subsystem nodes, K noise paths`.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Memory use grows with repository size. | Chunks, indices and vectors are all in memory. | Use `.euignore` to reduce the project. |
| The IVF index is rebuilt on every start. | It is not stored on disk. | None. |
| Both binary files must be version 5. | The loader checks it. | Re-run `eulix analyze` after upgrading. |
| `mmap_embeddings.go` is an unused experiment. | Its comment says so. | Ignore it. |

## Related Docs

- [Mmap](Mmap.md): the file-mapping helpers used here.
- [Engines](Engines.md): what each engine loads.
- [Output Files](../../../eulix-parser/Output-Files.md): the files read from the parser's side.
