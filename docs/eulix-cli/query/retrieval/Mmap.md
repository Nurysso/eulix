# mmap

The `mmap` sub-package decodes very large JSON files through a memory mapping, with a separate implementation for each operating system, and gives the streaming loader a plain buffered reader.

> **Crate / path:** `eulix-cli/internal/query/retrieval/mmap/`  
> **Language:** Go  
> **Status:** stable on Linux (the author notes testing on kernel 6.18 LTS); other platforms have simpler fallbacks  
> **Last verified against:** `ad744e9`

## Overview

*Memory mapping* asks the operating system to make a file appear as if it were part of memory. Pages are loaded from disk only when touched, and they stay in the process's memory until the mapping is released (the operating system can still reclaim them under pressure). For a large index or call-graph file that is decoded in one go, that beats reading the whole file into a Go slice first.

The package offers two functions the rest of Eulix uses, and one allocator for the vector matrix.

## Why It Exists

`os.ReadFile` on a 4 GB file allocates 4 GB of heap. Mapping it costs almost none; only the parsed result takes space. Without this package, loading a big repository could need much more memory than the final structures.

## How It Works

**`DecodeJSONFile(path, &value)`** decodes a file into a Go value as fast as it can:

- Files of **4 MiB or more** are mapped, and decoded with the Sonic JSON library straight from the mapping.
- Smaller files use a normal buffered reader (1 MiB buffer) and Sonic's streaming decoder, because setting up a mapping costs more than it saves for small files.
- If mapping fails (a sandbox, an unusual file system, low memory), the code falls back to the buffered reader without comment. The same open will then fail again if the file is truly unreadable, which surfaces the real cause.

**`OpenForSequentialRead(path)`** returns a buffered reader (1 MiB buffer) and a cleanup function that closes the file. It never maps, whatever the size; see [Why the streaming reader doesn't map](#why-the-streaming-reader-doesnt-map). The caller must always call the cleanup function. This is what the streaming loader uses ([Index Loading](Index-Loading.md)).

**Sonic set-up.** At start-up, the package asks Sonic to precompile its decoders for the knowledge-base, index and call-graph types, recursing to depth 8. The results are kept and written to the debug log later. Failures are printed to standard error but do not stop the program. Strings are copied out of the input (`CopyString`) so they stay valid after the mapping is released.

### Linux specifics

- The file is mapped `MAP_SHARED` and read-only. There is no `MAP_POPULATE`: preloading the whole file at mapping time gave no speedup (254 ms without, 276 ms with, cold cache, 228 MB `kb_index.json`), and it made the load wait for the full disk read before parsing started.
- `MADV_SEQUENTIAL` tells the kernel the file is read once, front to back, so it reads ahead more aggressively and treats pages already passed as cheap to reclaim. It does not drop them: mapped pages stay resident until the mapping is released, so a whole-file decode holds about the file's size in page cache at its peak.
- `MADV_HUGEPAGE` asks for 2 MiB pages, reducing the pressure on the processor's address-translation cache. It is best effort.
- `AllocEmbeddingMatrixOwned` carves the vector matrix out of a 2 MiB-aligned anonymous mapping and returns a release function that unmaps the whole thing, for deterministic freeing.

### Other systems

- **macOS** maps the file `MAP_PRIVATE` and advises `MADV_SEQUENTIAL`. For a read-only mapping, private and shared pages are the same pages in the unified buffer cache, so the flag has no effect on caching or on other processes.
- **Windows** opens the file with `FILE_FLAG_SEQUENTIAL_SCAN`, maps it, and calls `PrefetchVirtualMemory` over the whole view so Windows reads it in large I/Os before the parse starts. The flag is a read-ahead hint for ordinary reads and does little for a mapped view; it is kept because it is harmless. The file is opened with read sharing only (see [RET-10](../../../Known-Issues/Retrieval-And-Query.md#ret-10-rewriting-kbjson-while-eulix-reads-it-can-crash-the-reader)).
- A generic Unix fallback (`MAP_SHARED` with `MADV_SEQUENTIAL`) and a "none of the above" file, which never maps, keep the package compiling everywhere.

### Main Components

| File | Responsibility |
| --- | --- |
| `mmap_loaders.go` | `DecodeJSONFile`, `OpenForSequentialRead`, Sonic set-up. |
| `mmap_unix.go`, `mmap_linux.go`, `mmap_darwin.go`, `mmap_windows.go`, `mmap_unix_fallback.go`, `mmap_others.go` | Per-platform mapping. |
| `matrix_owned_other.go` | Matrix allocation without huge pages. |
| `mmap_embeddings.go` | An unused experiment for reading `embeddings.bin` here. |

### Data In, Data Out

In: a path. Out: a decoded value, or a reader plus a cleanup function.

## Key Algorithms

### Why a size threshold

**The problem.** Mapping has fixed costs: setting up page tables and the first-access faults.

**The approach.** Map only files of 4 MiB or more.

**Why this way.** Below that size, ordinary reads are as fast or faster, and simpler.

### Why the streaming reader doesn't map

**The problem.** The streaming loader walks `kb.json` with `json.Decoder`, one file entry at a time, so that only the parsed chunks stay on the heap.

**The approach.** `OpenForSequentialRead` reads the file through a 1 MiB buffer and never maps it.

**Why this way.** `json.Decoder` copies every byte into its own buffer, so a mapping saves no copy, and every mapped page the walk passes stays resident until the mapping is released. On a 178 MB `kb.json`, the mapped walk took 966 ms and peaked at 212 MB; the buffered walk took 917 ms and peaked at 31 MB. A sequential-read hint (`posix_fadvise`) made no difference, so there is none. A buffered reader also can't crash when the file is rewritten underneath it; the decode just reports an error.

## Configuration

None. The threshold and buffer sizes are constants.

## Usage Examples

```go
var cg utils.CallGraphRef
err := mmap.DecodeJSONFile(".eulix/kb_call_graph.json", &cg)
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| File sizes that overflow `int` are rejected on 32-bit systems. | Slices are indexed by `int`. | Use a 64-bit build. |
| Behaviour is best tuned for Linux. | Other platforms get simpler hints. | None. |
| Rewriting `kb_index.json`, `kb_call_graph.json` or `kb.json` while `DecodeJSONFile` has it mapped crashes the reader on Linux and macOS, and makes the write fail on Windows. The streaming load is not affected. Won't fix: [RET-10](../../../Known-Issues/Retrieval-And-Query.md#ret-10-rewriting-kbjson-while-eulix-reads-it-can-crash-the-reader). | A general property of memory mapping; the parser writes these files in place. | Don't run `eulix analyze` while a query on the same repository is loading. |
| On Linux, quitting chat during a vector search can crash. Won't fix: [RET-11](../../../Known-Issues/Retrieval-And-Query.md#ret-11-quitting-chat-during-a-vector-search-can-crash-on-linux). | `AllocEmbeddingMatrixOwned` unmaps the matrix on close, and chat closes it without waiting for the running question. | Let the question finish before quitting. |
| A corrupt file of 4 MiB or more is parsed twice before the error is reported. Won't fix: [RET-12](../../../Known-Issues/Retrieval-And-Query.md#ret-12-a-knowledge-base-file-that-fails-to-decode-is-parsed-twice). | `DecodeJSONFile` falls back to the buffered reader after any error. | Run `eulix analyze` again. |

## Related Docs

- [Index Loading](Index-Loading.md): the main user.
- [Overview](Overview.md): the engine.
