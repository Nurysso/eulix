# mmap

The `mmap` sub-package reads very large files without copying them into memory, with a separate implementation for each operating system.

> **Crate / path:** `eulix-cli/internal/query/retrieval/mmap/`  
> **Language:** Go  
> **Status:** stable on Linux (the author notes testing on kernel 6.18 LTS); other platforms have simpler fallbacks  
> **Last verified against:** `ad744e9`

## Overview

*Memory mapping* asks the operating system to make a file appear as if it were part of memory. Pages are loaded from disk only when touched, and the operating system can drop them again when memory is tight. For a multi-gigabyte `kb.json`, that is far better than reading the whole file into a Go slice.

The package offers two functions the rest of Eulix uses, and one allocator for the vector matrix.

## Why It Exists

`os.ReadFile` on a 4 GB file allocates 4 GB of heap. Mapping it costs almost none; only the parsed result takes space. Without this package, loading a big repository could need much more memory than the final structures.

## How It Works

**`DecodeJSONFile(path, &value)`** decodes a file into a Go value as fast as it can:

- Files of **4 MiB or more** are mapped, and decoded with the Sonic JSON library straight from the mapping.
- Smaller files use a normal buffered reader (1 MiB buffer) and Sonic's streaming decoder, because setting up a mapping costs more than it saves for small files.
- If mapping fails (a sandbox, an unusual file system, low memory), the code falls back to the buffered reader without comment. The same open will then fail again if the file is truly unreadable, which surfaces the real cause.

**`OpenForSequentialRead(path)`** returns a reader and a cleanup function. It maps large files with sequential-read hints and uses a buffered reader otherwise. The caller must always call the cleanup function. This is what the streaming loader uses ([Index Loading](Index-Loading.md)).

**Sonic set-up.** At start-up, the package asks Sonic to precompile its decoders for the knowledge-base, index and call-graph types, recursing to depth 8. The results are kept and written to the debug log later. Failures are printed to standard error but do not stop the program. Strings are copied out of the input (`CopyString`) so they stay valid after the mapping is released.

### Linux specifics

- `MAP_POPULATE` fills the page tables at mapping time, avoiding a burst of page faults on first access. For multi-gigabyte files the I/O is done up front, where the kernel can parallelize it.
- `MADV_SEQUENTIAL` tells the kernel the file will be read once, front to back, so it reads ahead (up to about 2 MiB) and releases pages already consumed. The comment estimates under 100 MB of page cache even for a 4 GB scan.
- `MADV_HUGEPAGE` asks for 2 MiB pages, reducing the pressure on the processor's address-translation cache by a large factor. It is best-effort.
- `AllocEmbeddingMatrixOwned` carves the vector matrix out of a 2 MiB-aligned anonymous mapping and returns a release function that unmaps the whole thing, for deterministic freeing.

### Other systems

macOS uses its own advice calls (read-ahead on, caching off) and the Windows file has its own mapping and prefetch code, referencing `PrefetchVirtualMemory`. A generic Unix fallback and a "none of the above" file keep the package compiling everywhere.

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
| A mapped file that is truncated while mapped can crash the process. | A general property of memory mapping. | Do not edit `.eulix/` files while `eulix` runs. |

## Related Docs

- [Index Loading](Index-Loading.md): the main user.
- [Overview](Overview.md): the engine.
