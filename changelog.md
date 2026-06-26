# Changelog

## Eulix_parser [v0.7.2] - 2026-06-25

> Performance Notes (main.rs)

### OS I/O hints (FADV_SEQUENTIAL, readahead, FADV_DONTNEED) — DO NOT ADD

Attempted in v0.7.2, will be reverted in v0.7.3

The Linux kernel's own readahead handles sequential file access correctly
for this workload. Manual hints via posix_fadvise/readahead require statx
syscalls to check file size before issuing, adding ~32k extra syscalls
during parallel parse of the Linux kernel source tree. This was strictly
slower than letting the kernel manage it.

Measured cost: +3-4s on parse phase (32k files, 12 threads).

### posix_fallocate on writes — DO NOT ADD

Pre-allocating output files caused eager page zeroing before writes,
increasing minor page faults from ~326k to ~766k. Net negative.

### par_chunks in parse_directory — DO NOT ADD

Replacing par_iter with par_chunks + manual fold introduced Mutex
contention on the stats collector inside each chunk. par_iter with
a fold/reduce and no shared Mutex is both simpler and faster.

### Call graph edges must be wired through — CRITICAL

build*call_graph must pass resolved_edges into CallGraph { edges }.
Leaving `let * = resolved_edges` drops all 1.7M edges silently.

## Eulix [v0.7.0] - 2026-06-17

### Performance

#### Memory Optimization (16GB RAM target)

- **Streaming KB loader**: `kb.json` now streamed via mmap + `json.NewDecoder` instead of loading full `KnowledgeBaseRef` struct
- **Peak memory reduction**: ~14GB → ~6-8GB for 4GB source corpora
- **No double-loading**: Previous design loaded `kb.json` twice (once for KB struct, once for chunks) — eliminated
- **Lazy content disabled**: Streaming path always materializes content inline; no source struct to hydrate from

#### Platform-Specific mmap

- **Linux**: `MAP_POPULATE` + `MADV_SEQUENTIAL` + `MADV_HUGEPAGE` for 2MB THP
- **Windows**: `FILE_FLAG_SEQUENTIAL_ONLY` + `PrefetchVirtualMemory` (Win8+)
- **macOS**: `MAP_PRIVATE` + `MADV_SEQUENTIAL` via UBC
- **Fallback**: Buffered reader with 1MB read buffer when mmap unavailable

#### Boilerplate Detector

- **Fast-path normalization**: Clean identifiers (alnum+underscore) skip `strings.Replacer` allocation
- **TopBoilerplate min-heap**: `O(N log n)` vs previous `O(N log N)` for top-10 queries
- **Small-symbol dedup**: Chunks with ≤8 symbols use sorted slice instead of per-chunk map (reduces GC pressure on 1M+ chunk corpora)

### Code Organization

- **`buildDerivedIndices()`**: Single-pass construction of boilerplate, symbol index, inverted index
- **`loadChunks()`**: Replaces `loadKnowledgeBase()` + `loadChunksFromKB()` pair
- **Platform-specific mmap**: Split into `mmap_linux.go`, `mmap_darwin.go`, `mmap_windows.go`, `mmap_unix_fallback.go`
- **`buildInvertedIndex()`**: Now operates on `cb.chunks` directly (no KB dependency)

### Fixes

- `cb.hasKB` now correctly set to `true` in streaming path (was always false in previous refactor)
- `cb.lazyContent` forced false in streaming path (prevents hydration attempts on nil source)
- `loadVectorMap()`: Added sanity checks for implausible ID lengths and count values
- `decodeViaMmap`: Re-stat open FD to close stat-then-mmap TOCTOU race

### Internal

- `PreAllocate` constant: `320_000` for chunk slice capacity (adjustable per corpus)
- `mmapThreshold`: Lowered from 32MB → 4MB (empirical break-even on Linux/Windows)
- `buildBoilerplate()`: Streaming-path entry point (vs `buildBoilerplateFromKB` for non-streaming)

---

## [v0.6.9] - 2026-06-16

_(Previous changes: BM25, PathGate, explicit anchors, IVF parallelization)_
