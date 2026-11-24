# Changelog

## Unreleased

### Changed

- **The parse cache format is 4.** Cached `kb.fb` records replace the JSON fragments, the index is a FlatBuffer and the slim records are FlexBuffers (39% smaller than JSON, 20% lower peak memory on a cached Kubernetes re-run, which takes 0.16 s longer), so caches from earlier builds are discarded, and their files deleted, on the next run.
- **Every output is FlatBuffers.** The knowledge base is `kb.fb`: a header (metadata, each file's path, offset and length) and one record per source file, so it can pass 2 GiB. It is always `<stem>.fb` next to `--output`, whatever extension `--output` has; `kb.json` is now only the `--emit-json` copy. `kb_index`, `kb_call_graph`, `kb_summary`, `kb_metrics`, `kb_entry_points`, `kb_external_deps` and `kb_patterns` are now written as `.fb` files (schemas in `schema/`). Both large files store each distinct string once and are about a third the size of the old compact JSON (Kubernetes: index 85 → 26 MB, call graph 66 → 21 MB) with the same write time. The checksum (`checksum.fb.zst`) and the parse-cache index (`parse_cache/index.<gen>.fb.zst`) are FlatBuffers too, both still zstd-compressed; see `docs/Architecture/Binary-Knowledge-Base.md`.
- **Outputs are replaced atomically.** Every output is written under a `.tmp` name and renamed into place, so a reader with the old file open keeps the old contents and a failed run leaves the previous output intact (fixes RET-10 on Linux and macOS).

### Added

- **New dependencies:** `flatbuffers` and `flexbuffers`, both 25.12.19.
- **`--emit-json`.** Also writes a JSON copy of every `.fb` output, `checksum.json` and `parse_cache/index.json`, decoded from the binary data, for inspecting changes. Without it, stale copies are deleted.
- **`--checksum fb|json|none`.** Picks the checksum baseline: `fb` (default) writes `checksum.fb.zst`, `json` writes the pre-FlatBuffers `checksum.json.zst`, and `none` skips hashing and writes nothing (the knowledge base's `project_hash` is then empty). The baseline of the other format is removed. The `--emit-json` copy `checksum.json` is only written for `fb`.

## Eulix_parser v0.9.0 - 2026-11-17

Changes since v0.8.1, the version that shipped with Eulix v0.8.3. Built on libeulix 0.9.0, so it also gets the PHP parser, the iterative tree walks and the call-graph fixes listed in [libeulix/changelog.md](../libeulix/changelog.md).

### Added

- **Incremental parsing** (#149). Unchanged files are no longer re-parsed. Each run stores every file's `kb.json` fragment and its slimmed `FileData` in `.eulix/parse_cache/` (`index.<gen>.json.zst`, `emit.<gen>.spill`, `slim.<gen>.spill`).
  - A file whose size and mtime match, with an mtime more than 2 s before the cached run started, is reused without being read. Any other file is read and hashed with xxh3-128, and parsed only when the hash differs.
  - Reused and parsed files go through the same accumulator, so totals, languages, the syntax report and external dependencies match a full parse byte for byte. The call graph is still rebuilt from scratch.
  - The cache key is the format number, the parser version, the git SHA, and the size and mtime of the running binary, so any rebuild invalidates it, uncommitted grammar edits included.
  - A bad index, a key mismatch or a record that fails its CRC falls back to parsing that file; the cache never fails a run. The index is written to a temp file and renamed, and only generations up to the one this run loaded are deleted, so a concurrent run's files survive.
- **PHP** (#83): `php` in `--languages` and in `all` (`.php`, `.phtml`, `.inc`). A `.inc` file is collected only when it holds PHP code, since C, C++ and assembler projects use the extension too. Slimming keeps the imports, namespace, `extends` class, traits and `static` flag that PRISM v2 needs until the call graph is built. A PHP file's `{main}` holds its top-level code and is not counted in `total_functions`.
- **Go and Java method calls in PRISM v2** (#167). The parser reads the module path from every `go.mod` above a parsed Go file and passes it to PRISM v2 (`CallGraphOptions::go_modules`), so Go imports map to package directories. Slimming keeps the names and declared types of Go and Java variables and fields, and the name and type of a Go method's receiver, until the call graph is built.
- A determinism test: a 300-file Python/Rust/JS fixture plus a Go fixture, parsed with `-t 1` and `-t 8`, with every JSON output compared byte for byte (#149).

### Changed

- Symbol IDs are built from the path relative to `--root` (`func_name::src/a.py`). They used to carry the absolute path, so they embedded the user's home directory, changed when the repository moved and didn't match the structure keys (#120).
- Every JSON output comes out in the same order on every run (ordered maps in libeulix), and `rel_path` always uses `/`, so keys and order match on Windows (#149).
- `-t`/`--threads` now also sizes file discovery (#147) and the analysis phase. Analysis used to start rayon's global pool with one thread per core, so `-t 2` on a 32-thread machine ran Phase 2 on 35 threads (#127).
- Changed files are detected from size and mtime instead of a content hash, so each file is read once instead of twice (#147).
- eulix_parser runs its own analysis pipeline instead of libeulix's deprecated `analyze_and_build` (`7cc429f`).
- Slimming keeps every parameter's name and clears only its type and default, so PRISM v2 can tell a call to a parameter from a call to a global function (#135).
- Depends on libeulix by path, keeping the version for crates.io. Publishing to crates.io needs a libeulix release with the matching version (#83).
- Removed unused Windows-specific code (#161).

### Removed

- `--resume`/`-u`, its spill rehydration and `SpillGuard`. A crashed run now starts over from the last committed parse-cache generation (#149).

### Fixed

- External dependencies were lost because `slim()` cleared the imports before they were read. Imports are now collected per file before slimming (`775f357`).
- A long expression chain in generated or minified code (about 2,400 to 2,650 terms) overflowed rayon's 2 MiB stack and aborted the whole process. Worker threads now get a 64 MiB stack, which gets through 77,000 to 85,000 terms with peak RSS and time within run-to-run noise (#127).
- Extensions match in any case, so `UPPER.PY` and `Main.JAVA` are collected (#127).
- One byte of invalid UTF-8, like a Latin-1 `é` in a comment, failed the whole file. Invalid bytes are now replaced with U+FFFD and the file is reported as partial, with an error at the line of the first bad byte, so `--parse-report` still shows it (#127).
- A file that couldn't be read aborted the walk. It is now recorded as failed and the walk goes on (#147).

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

_(Previous changes: were not tracked )_
