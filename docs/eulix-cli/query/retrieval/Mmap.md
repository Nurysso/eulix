# mmap

The `mmap` sub-package gives the knowledge-base readers fast access to large files: a memory mapping for whole-file reads, with a separate implementation for each operating system, and a plain buffered reader for one front-to-back pass.

> **Crate / path:** `eulix-cli/internal/query/retrieval/mmap/`  
> **Language:** Go  
> **Status:** stable on Linux (the author notes testing on kernel 6.18 LTS); other platforms have simpler fallbacks  
> **Last verified against:** `0a78aa7` plus the FlatBuffers migration ([Binary Knowledge Base](../../../Architecture/Binary-Knowledge-Base.md))

## Overview

*Memory mapping* asks the operating system to make a file appear as if it were part of memory. Pages are loaded from disk only when touched, and they stay in the process's memory until the mapping is released (the operating system can still reclaim them under pressure). For a large index or call-graph file read in one go, that beats copying the whole file into a Go slice first.

The package offers two functions the readers in `internal/kbfile` use, and one allocator for the vector matrix. It no longer decodes anything itself: the knowledge-base files are FlatBuffers, and `kbfile` reads them in place.

## Why It Exists

`os.ReadFile` on a 4 GB file allocates 4 GB of heap. Mapping it costs almost none; only the structures built from it take space. Without this package, loading a big repository could need much more memory than the final structures.

## How It Works

**`WithFileBytes(path, fn)`** calls `fn` with the whole file as one byte slice:

- Files of **4 MiB or more** are mapped read-only for the duration of `fn`, then unmapped.
- Smaller files are read with `os.ReadFile`, because setting up a mapping costs more than it saves for small files.
- If mapping fails (a sandbox, an unusual file system, a platform without mmap), the file is read instead. Only a failed mapping falls back; an error from `fn` is returned as is.

`fn` must not keep the slice: the `kbfile` readers copy every string they keep out of it (and intern repeated ones). The parser replaces its outputs by renaming a finished temporary file over them, so on Linux and macOS a file mapped here keeps its old contents even if `eulix analyze` runs meanwhile.

**`OpenForSequentialRead(path)`** returns a buffered reader (1 MiB buffer) and a cleanup function that closes the file. It never maps, whatever the size; see [Why the streaming reader doesn't map](#why-the-streaming-reader-doesnt-map). The caller must always call the cleanup function. `kbfile.WalkKB`, which reads `kb.fb` one record at a time ([Index Loading](Index-Loading.md)), uses it.

### Linux specifics

- The file is mapped `MAP_SHARED` and read-only. There is no `MAP_POPULATE`: preloading the whole file at mapping time gave no speedup (254 ms without, 276 ms with, cold cache, on a 228 MB index file), and it made the load wait for the full disk read before reading started.
- `MADV_SEQUENTIAL` tells the kernel the file is read once, front to back, so it reads ahead more aggressively and treats pages already passed as cheap to reclaim. It does not drop them: mapped pages stay resident until the mapping is released, so a whole-file read holds about the file's size in page cache at its peak.
- `MADV_HUGEPAGE` asks for 2 MiB pages, reducing the pressure on the processor's address-translation cache. It is best effort.
- `AllocEmbeddingMatrixOwned` carves the vector matrix out of a 2 MiB-aligned anonymous mapping and returns a release function that unmaps the whole thing, for deterministic freeing.

### Other systems

- **macOS** maps the file `MAP_PRIVATE` and advises `MADV_SEQUENTIAL`. For a read-only mapping, private and shared pages are the same pages in the unified buffer cache, so the flag has no effect on caching or on other processes.
- **Windows** opens the file with `FILE_FLAG_SEQUENTIAL_SCAN`, maps it, and calls `PrefetchVirtualMemory` over the whole view so Windows reads it in large I/Os before the reader starts. The flag is a read-ahead hint for ordinary reads and does little for a mapped view; it is kept because it is harmless. While a file is mapped, the parser cannot replace it ([RET-10](../../../Known-Issues/Retrieval-And-Query.md#ret-10-on-windows-eulix-analyze-can-fail-while-eulix-reads-the-same-knowledge-base)).
- A generic Unix fallback (`MAP_SHARED` with `MADV_SEQUENTIAL`) and a "none of the above" file, which never maps, keep the package compiling everywhere.

### Main Components

| File | Responsibility |
| --- | --- |
| `mmap_loaders.go` | `WithFileBytes`, `OpenForSequentialRead`, the size checks. |
| `mmap_unix.go`, `mmap_linux.go`, `mmap_darwin.go`, `mmap_windows.go`, `mmap_unix_fallback.go`, `mmap_others.go` | Per-platform mapping (`mapFile`, `mmapPlatform`). |
| `matrix_owned_other.go` | Matrix allocation without huge pages. |
| `mmap_embeddings.go` | An unused experiment for reading `embeddings.bin` here. |
| `export_test.go` | Exposes `mapFile` to the tests as `MapFile`, so a broken mapping is not hidden by the read fallback. |

### Data In, Data Out

In: a path. Out: the file's bytes for the length of a callback, or a reader plus a cleanup function.

## Key Algorithms

### Why a size threshold

**The problem.** Mapping has fixed costs: setting up page tables and the first-access faults.

**The approach.** Map only files of 4 MiB or more.

**Why this way.** Below that size, ordinary reads are as fast or faster, and simpler.

### Why the streaming reader doesn't map

**The problem.** `kbfile.WalkKB` reads `kb.fb` one record at a time, copying each record's fields out, so that only the parsed chunks stay on the heap.

**The approach.** `OpenForSequentialRead` reads the file through a 1 MiB buffer and never maps it.

**Why this way.** The walk copies every record anyway, so a mapping saves no copy, and every mapped page it passed would stay resident until the mapping is released. Measured on the JSON knowledge base this replaced: on a 178 MB file, the mapped walk took 966 ms and peaked at 212 MB; the buffered walk took 917 ms and peaked at 31 MB. A sequential-read hint (`posix_fadvise`) made no difference, so there is none. A buffered reader also can't crash when the file changes underneath it; the walk just reports an error.

## Configuration

None. The threshold and buffer sizes are constants.

## Usage Examples

```go
err := mmap.WithFileBytes(".eulix/kb_call_graph.fb", func(b []byte) error {
	g := fb.GetRootAsCallGraph(b, 0)
	n := g.NodesLength() // copy out anything kept beyond fn
	_ = n
	return nil
})
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| File sizes that overflow `int` are rejected on 32-bit systems. | Slices are indexed by `int`. | Use a 64-bit build. |
| Behaviour is best tuned for Linux. | Other platforms get simpler hints. | None. |
| On Windows, `eulix analyze` cannot replace a file that a query has mapped. Won't fix: [RET-10](../../../Known-Issues/Retrieval-And-Query.md#ret-10-on-windows-eulix-analyze-can-fail-while-eulix-reads-the-same-knowledge-base). | Windows refuses to rename over a mapped file. | Run `eulix analyze` again once the query has loaded. |
| On Linux, quitting chat during a vector search can crash. Won't fix: [RET-11](../../../Known-Issues/Retrieval-And-Query.md#ret-11-quitting-chat-during-a-vector-search-can-crash-on-linux). | `AllocEmbeddingMatrixOwned` unmaps the matrix on close, and chat closes it without waiting for the running question. | Let the question finish before quitting. |

## Related Docs

- [Index Loading](Index-Loading.md): the main user.
- [Binary Knowledge Base](../../../Architecture/Binary-Knowledge-Base.md): the file formats read through it.
- [Overview](Overview.md): the engine.
