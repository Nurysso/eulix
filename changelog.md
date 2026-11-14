# Changelog

## Eulix v0.9.0 - Unreleased

Changes since v0.8.3 (2026-10-14). Each component's changelog has the full list.

| Component    | Version         | Changelog                                              |
| ------------ | --------------- | ------------------------------------------------------ |
| eulix CLI    | v0.8.3 → v0.9.0 | [eulix-cli/changelog.md](eulix-cli/changelog.md)       |
| eulix_parser | 0.8.1 → 0.9.0   | [eulix-parser/changelog.md](eulix-parser/changelog.md) |
| libeulix     | 0.8.2 → 0.9.0   | [libeulix/changelog.md](libeulix/changelog.md)         |
| eulix-lsp    | 0.2.0 (unreleased, `make install lsp`) | [eulix-lsp/changelog.md](eulix-lsp/changelog.md) |
| eulix_embed  | 0.4.1 → 0.4.2   | [eulix-embed/changelog.md](eulix-embed/changelog.md)   |
| VizEulize    | → 0.2.0 (installed by `install.sh`) | [vizEulize/changelog.md](vizEulize/changelog.md) |

libeulix is published on crates.io.

### Highlights

- **PHP support** end to end: a PHP parser and PHP call resolution in libeulix, `php` in eulix_parser, comment stripping and signatures in retrieval, and references and call hierarchy in eulix-lsp.
- **Incremental parsing:** eulix_parser reuses unchanged files from `.eulix/parse_cache/`, and the output matches a full parse byte for byte.
- **EHNAS**, a new retrieval engine alongside etiya, with engine-specific configuration.
- **More accurate call graphs:** receiver-aware method calls for Python, TypeScript and Rust, nested functions with scoped IDs, and a deterministic PRISM v1. C++ templates and inline members, decorated Python methods, TypeScript functions bound to variables and methods of generic Rust impls are no longer dropped.
- **Steadier parsing:** every grammar walks its syntax tree without recursion, every output comes out in the same order on every run, and files with invalid UTF-8 are parsed as partial instead of failing.
- **Vector search fix:** embedding rows are matched to chunks by id. Before, every vector from the second file on was paired with the wrong chunk.
- **Security:** MCP and retrieval no longer read hidden, `.euignore`d or out-of-repo files, symlink and `.eulix` escapes are closed, and eulix_embed blocks `trust_remote_code`.
- **Windows:** a native build script, `.env` locked to its owner, and safer parser installs and file opening.

### Upgrade notes

- libeulix 0.9.0 changes its Rust API: a new `PhpInfo` variant, `BTreeMap`/`BTreeSet` in serialized types and a new `FunctionCall` field. `analyze_and_build` is deprecated.
- Symbol IDs changed: they use the path relative to the project root, TypeScript interface IDs include the file path, and nested functions get scoped IDs. The first `eulix analyze` after upgrading runs a full analysis.
- `eulix_parser --resume`/`-u` is gone; the parse cache replaces it.
- A repository's `.env` is no longer exported into the process environment; only the provider key is read from it.
- eulix_embed's default model is now `BAAI/bge-base-en-v1.5`.

### Build, CI and install

- Each build target builds only its own parser, so a Linux build no longer needs the Windows toolchain (#92).
- Makefile, release and script builds pass the right `-X` name (`assets.ParserHash`). The old name was accepted silently, so the hash stayed empty and the startup check of the embedded parser against the build manifest was skipped (#92, #94).
- Native Windows build with `build.ps1` (#161).
- Build and release workflow fixes; builds ignore `__init__.py` files (#138, #143).
- The installers download the latest tag (`9830743`) and use the right checksum file name (#163).
- CI: lint runs on `dev` and on libeulix-only changes (#122). A new `Test.yaml` runs every project's tests (Go and Rust on Linux, Windows and macOS), and `Lint.yaml` stays lint-only (`f9006ea`). A docs workflow checks the docs weekly (#163).
- `.gitattributes` keeps LF line endings in the working tree on every OS (#164).

### Docs

- Documentation rewritten from the current code, one folder per subsystem (libeulix, eulix-parser, eulix-embed, eulix-cli, vizEulize), plus Welcome, Installation, Architecture (spill to disk, incremental parsing) and Known-Issues pages. Known issues have IDs, severity, status and how each was verified, and fixed issues are recorded (#157).
- PHP is in the README's language lists and support table (#83).
- LSP architecture and benchmarking notes (#165).
- Component READMEs corrected: command tables, config reference, function names, license, binary and file names (#157).

### Benchmarks and scripts

- `make bench-compare A=... B=...` compares two versions with isolated builds and `HOME`, cached by commit. It measures accuracy, indexing and query time and memory, and parser quality (GPU with CPU fallback) on eulix, flask, gin, ripgrep, gson and Linux (`--large`), flags regressions over 10%, writes a Markdown report and exits 1. A v0.8.3 comparison is included (`f816dcd`).
- Scripts used to find bugs are under `scripts/` (#157).

## Eulix_embed v0.4.1 (2026-10-02)

- Linter fixes and lint.sh uses ruff for most of the lints
- compare command that was used for verification of few entries from embeddings.bin and vectors.bin can now verify entire of embeddings.bin and vectors.bin

## **_[Unreleased]_** - VizEulize Call Graph Visualization Tool Added

- **Local JSON File Loading:** Implemented client-side parsing for `kb_call_graph.json` and optional `kb_index.json` files via drag-and-drop or file selector, ensuring zero external data uploads.
- **Node Filtering Controls:** Added category and tag filtering dropdowns ("All categories", "All tags") to scope down large graph views.
- **Directional Traversal Toggle:** Added options to filter and highlight relationships by call direction (both ways, callees →, ← callers).
- **Depth/Hop Traversal Limits:** Added depth selection controls (1, 2, 3) to restrict neighborhood expansions.
- **Interactive Graph Canvas:**
  - Integrated pan/drag and scroll-to-zoom viewport controls.
  - Added click interactions for displaying node detail panels.
  - Added double-click interaction to isolate and focus on a node's immediate neighborhood.
- **Default View Logic:** Automated initial render to highlight the most-connected nodes upon dataset load.
- **Reset Controls:** Added a "Reset view" button to restore default layout, zoom, and active filters.

## Eulix CLI 0.8.2 - 2026-09-29

### Features (`6c5f9d4`)

- **hydration**: Updated the hydration algorithm within retrieval for improved performance and data processing.
- **checksum**: Updated `checksum` package to read directly from `eulix_parser` output instead of generating `checksum.json.zst`.
- **config**: Moved project-wide configuration constants into `utils/constants`.
- **tui**: TUI has been improved and bugs fix #68

### Performance & Parser Improvements

- **retrieval / parser** (`81294c30`): Updated parser to output smaller files for retrieval. This resulted in:
  - Faster retrieval speed without any loss in accuracy.
  - Reduced peak RAM/RSS usage when loading files into memory.
- **query**(`acef602`): Skipped semantic search optimizations for `callers` and `callees` intent queries.

### Refactoring & Cleanups

- **query**(`087a0f5`): Broken down monolithic query package into smaller, maintainable modules.
- **query**(`1e9a67f`): Rewrote MMR, added anchor pinning, and performed symbol cleanup.
- **kbstruct**(`63f6164`): Synced `kbstruct.go` to be a direct 1:1 match with the `eulix_parser` struct.
- **deprecations**(`6c5f9d4`): Deprecated `glados` and `aspirine` modules.

### Tests

- **query**(`63f6164`): Added test suites for classifier and router components.

## Eulix_parser v0.7.7 (2026-9-28)

### Summary

We identified which fields in kb.json were unnecessary for the downstream workload and dropped them during the file writing phase. Modeling the storage reduction on the Linux kernel codebase (~35M LOC) yielded a predicted size of ~650 MB, which closely matched our measured output of 658 MB (673,063 KB).

### Changed

- **Simplified `kb.json` output.** The knowledge base now serializes a slimmed-down
  view of each file (`FileDataSimple`, `FunctionSimple`, `ClassSimple`) instead of the
  full parsed structures. Fields that were rarely used downstream are no longer written:
  `calls`, `called_by`, `control_flow`, `exceptions`, `is_async`, `tags`, and
  function-level `decorators` / `lang_info`.
- Serialization is done through borrowing views (`StructureView`), so no intermediate
  copy of the structure map is allocated. Analysis still runs on the full in-memory
  data; only the written output is simplified.
- All the grammar files now uses `regex::Regex` instead of `regex::bytes::Regex`.
- parser/utils.rs created to store common shared functions across the grammar files.

### Performance

- `kb.json` size on the large benchmark codebase: **2.06 GB -> 0.67 GB (-67.3%)**.
- Filesystem write volume: 5,676,432 -> 2,900,712 blocks (**-48.9%**).
- Output phase (Phase 4): ~6.89s -> ~4.08s (**-2.81s**).
- Total wall-clock: 49.99s -> 48.98s (-1.01s).
- Peak RSS: 8.51 GB -> 8.32 GB (-2.2%).

### Unchanged

- `kb_index.json`, `kb_summary.json`, `kb_call_graph.json`, `kb_external_deps.json`,
  `kb_metrics.json`, `kb_entry_points.json`, and `kb_patterns.json` are the same size
  as before. Call relationships remain available through `kb_call_graph.json`.

### Notes

- Any downstream consumer previously relying on calls, called_by, control_flow, exceptions, is_async, or tags in kb.json must now ingest kb_call_graph.json or update its parsing logic.

- Minor page faults increased (829k → 1.64M) and read blocks rose slightly (710k → 897k) during Phase 1 caching churn, but neither impacted overall runtime.

## Eulix v0.7.2 (2026-06-28)

### Retrieval Quality

- **Dynamic subsystem detection** — Queries now automatically identify relevant
  subdirectories based on token overlap with the repository tree. Chunks in
  matching subsystems receive proportional score boosts; chunks in unrelated
  subsystems are penalized. Eliminates hardcoded project-specific paths.
- **Noise path detection** — Directories dominated by test fixtures, vendor
  code, shared utilities, and config/plugin directories are automatically
  identified and penalized during retrieval. Prevents test code and
  infrastructure noise from crowding out relevant results on large monorepos.
- **BM25 term proximity bonus** — Chunks where multiple query terms appear
  close together (within 300 characters) now receive an additional score
  bonus. Improves precision for multi-word technical queries like
  "PCI passthrough filter claiming".

### Memory

- **Streaming embeddings loader** — `embeddings.bin` is now read via buffered
  IO (4MB buffer) instead of `os.ReadFile`, eliminating a large intermediate
  allocation. Reduces peak heap by ~800MB on codebases with 270K+ embeddings.
- **Single-pass inverted index construction** — Removed intermediate
  `[]rawTF` allocation (306K elements on OpenStack-scale repos). Term
  frequency maps are now built and immediately converted to postings,
  becoming GC-eligible per chunk.
- **Explicit FileData cleanup** — `types.FileData` structs are zeroed after
  each file during streaming load, releasing references immediately rather
  than waiting for loop-scope GC.
- **Hydrate index map initialization fix** — Fixed nil map panic that would
  occur on lazy content hydration for file paths not seen during load.

### Performance

- **Query latency reduced 29%** (680ms → 485ms on 306K chunk corpus) due to
  better candidate filtering from subsystem detection, reducing downstream
  work in graph expansion and MMR selection.
- **dTLB misses reduced 58%** (30M → 12.7M) from improved memory locality
  when accessing subsystem-grouped chunks.
- **Minor page faults reduced 31%** (0.74M → 0.51M).

### Internal

- `ContextBuilder` gains `subsystemTree` and `noisePaths` fields (read-only
  after `buildDerivedIndices`).
- `buildDerivedIndices()` now calls `buildSubsystemTree()` and
  `detectNoisePatterns()` before constructing the symbol and inverted indices.
- `multiStrategySearch()` replaces `boostBySubsystemPath()` with
  `detectQuerySubsystems()` + `boostByDetectedSubsystems()`.
- `loadEmbeddings()` rewritten to use `bufio.Reader` streaming instead of
  `os.ReadFile` + manual offset tracking.
- `buildInvertedIndex()` collapsed from two-pass to single-pass construction.

---

## Eulix v0.7.1 (2026-06-26)

### Memory

- **Streaming kb.json loader** — Replaced full `sonic.Unmarshal` of the
  entire knowledge base with a streaming JSON decoder. Reduces peak memory
  for kb.json from ~2.75 GB to ~554 MB on large codebases.
- **Max RSS reduced 44%** (4.68 GB → 2.63 GB) on OpenStack-scale monorepo
  (~20M LOC, 306K chunks).
- **Lazy content support** — Chunks beyond `lazyContentLimit` (50K) store
  only metadata; full content is built on-demand during hydration.

### Performance

- Cold start increased ~51% (17s → 26s) due to streaming overhead. This is
  a one-time cost; subsequent queries are faster due to reduced memory
  pressure and better cache locality.

### Breaking

- `kb.json` is no longer fully materialized in memory. Callers depending on
  `cb.kbData.Structure` must migrate to `cb.chunks` / `cb.kbIdx`.
- `hydrateIdx` is nil after load when `lazyContent` is false (streaming
  path never populates it).

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
