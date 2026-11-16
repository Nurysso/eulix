# Incremental Parsing

This page explains how `eulix-parser` avoids re-parsing files that have not changed since the previous run, and how it keeps that shortcut safe.

> **Crate / path:** `eulix-parser/src/cache.rs`, `eulix-parser/src/parse/file.rs`, `eulix-parser/src/parse/directory.rs`  
> **Language:** Rust  
> **Status:** stable; introduced in commit `d16bd7c`  
> **Last verified against:** `ad744e9`

## Overview

Parsing is the most expensive phase of a run. On the Linux kernel it took about 38 of the 49 seconds in the v0.8.1 benchmark. But between two runs on the same repository, almost every file is exactly the same. Incremental parsing takes advantage of that: the parser remembers what it produced for each file last time, and when a file has not changed, it reuses that result instead of parsing it again.

Only the parsing is skipped. The relationship analysis (the call graph, indices and so on) is rebuilt from scratch on every run, because one changed file can alter edges anywhere in the project. The result of an incremental run is meant to be byte-for-byte identical to a fresh full parse, and a test checks exactly that.

After reading this page you will understand how the parser decides a file is unchanged, what is stored between runs, and what happens when something goes wrong.

## Why It Exists

Most developers re-run the parser many times against a repository that changes a little at a time. Re-parsing 60,000 files because three of them changed wastes almost all of the work. The design goal was to make the repeat run cost roughly "walk the tree, check each file, and parse only what changed", while never producing a different answer than a full parse would.

That second half of the goal drives most of the decisions below. A cache that is fast but sometimes serves stale data would be worse than no cache, so every shortcut has a guard.

## How It Works

Each run keeps a cache in `<output directory>/parse_cache/`. It is made of three files that share a **generation number** (a nanosecond timestamp):

| File | Contents |
| --- | --- |
| `index.<gen>.json.zst` | A compressed table with one entry per file: size, modification time, content hash, syntax health, external imports, and the location of that file's two records. It also holds the cache key and the time the run started. |
| `emit.<gen>.spill` | The detailed `kb.json` fragment for every file. |
| `slim.<gen>.spill` | The slim `FileData` for every file, used as input to analysis. |

The two spill files are the same ones described in [Spill to Disk](Spill-To-Disk.md). The cache is simply those files, kept after the run instead of thrown away, plus the index that says where to find each file's records.

### The decision for each file

For every discovered file, `parse_file` decides between three outcomes:

1. **Reuse without reading the file.** The parser asks the operating system for the file's size and modification time (this is called *stat*). If both match the cached entry, and the modification time is safely older than the cached run, the cached result is used and the file's contents are never read.
2. **Reuse after hashing.** If the size or time differs, the parser reads the file and computes a 128-bit `xxh3` hash of its bytes. If the hash matches the cached hash, the content is unchanged (for example, the file was touched or re-checked-out), so the cached result is used.
3. **Parse.** Otherwise the file is parsed from the bytes already in memory, like a normal run.

Reused and freshly parsed files then take the same path into the run's totals, so the counts, languages, syntax report and external dependencies come out exactly as in a full parse.

### What "reuse" does

Reusing a file means reading its two records from the old spill files, which are memory-mapped, checking each record against its CRC32 checksum, decoding the slim `FileData`, and **appending both records into the new generation's spill files**. Copying them forward is what makes each generation self-contained, so older generations can be deleted safely.

### Main Components

| Component | Responsibility |
| --- | --- |
| `ParseCache::open` | Finds the newest generation, loads its index, and maps its spill files. Starts a new generation. |
| `ParseCache::lookup` | Finds a file's cached entry by relative path. |
| `ParseCache::fresh` | The "can I trust size and time" check. |
| `ParseCache::reuse` | Reads, verifies and copies a cached file's records into the new generation. |
| `ParseCache::commit` | Writes the new index and removes old generations. |
| `Cached` | The per-file cache entry. |

### Data In, Data Out

The cache is private to the parser. Nothing outside `eulix-parser` reads it. If you delete the `parse_cache` directory, the next run is simply a full parse.

## Key Algorithms

### Is this file unchanged? (and the "racy timestamp" problem)

**The problem.** Comparing size and modification time is cheap, but it can be fooled. Some filesystems record times in steps as coarse as two seconds. If a file is edited right after the previous run read it, the edit can leave both the size and the visible time unchanged, and the cache would serve the old version.

**The approach.** A size-and-time match only counts if the file's modification time is also **more than two seconds older than the moment the cached run started** (`RACY_NANOS`). A file modified close to the previous run is never trusted by time alone, so it falls through to the hash check, which reads the actual content. The parser also takes the file's size and time *before* reading it, so an edit made during the read leaves a newer time that the next run will catch.

**Why this way.** It keeps the fast path (no file read) for the vast majority of files, while closing the one known gap in timestamp-based change detection. The cost is that files edited in the last two seconds before a run are re-read, which is cheap.

### Never trust a cache from a different program

**The problem.** If the parser itself changes (a new grammar, a fixed bug, a new field), old cached results may no longer match what the new code would produce.

**The approach.** The index stores a **cache key** built from a format number, the parser version, the git commit hash, and the size and modification time of the running executable. If the key does not match, the whole old cache is ignored.

**Why this way.** Including the executable's size and time means that *any* rebuild invalidates the cache, even uncommitted edits to a grammar that leave the version and git hash unchanged. That favours correctness over reuse during development, and in normal use it costs nothing, since released binaries do not change.

### Crash safety and concurrent runs

**The problem.** A run might crash halfway, or two runs might use the same output directory at the same time.

**The approach.** A run writes only to *new* generation files, so it never modifies the previous generation. At the end, it flushes its spill files, writes the new index to a temporary file, and renames it into place, which is atomic on common filesystems. Only after that does it delete files belonging to generations up to the one *it loaded*. Generations newer than that, which could belong to a run that started later, are left alone. Spill files are created with "must not already exist", so two runs can never write into the same file.

**Why this way.** A crashed run leaves behind an index-less generation that nobody reads, and the previous complete generation is still there for the next run. The cache never gets into a half-written state that a reader could mistake for valid.

### The cache never fails a run

If the index cannot be read, the key does not match, a spill file is missing, or a record fails its CRC check or fails to decode, that file simply falls back to a normal parse. A bad cache costs time, never correctness.

## Configuration

Incremental parsing is always on, and it has no flag.

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| Cache directory | `<output directory>/parse_cache/` | follows `--output` | Choose a different `--output` path to use a different cache. |
| `RACY_NANOS` | `cache.rs` | 2 seconds | The safety margin for coarse filesystem timestamps. Not configurable at run time. |
| Index compression | `cache.rs` | zstd level 3 | Compression of the on-disk index. |

To force a full parse, delete the `parse_cache` directory, or rebuild the parser.

## Usage Examples

Run the parser twice on the same repository and compare the verbose output:

```bash
eulix_parser --root ./my_project --prism 2 --output .eulix/kb.json --verbose
# edit one file, then run it again
eulix_parser --root ./my_project --prism 2 --output .eulix/kb.json --verbose
```

Expected result: the first run prints `Reused 0 cached files`. The second prints `Reused N cached files`, where N is the number of files that did not change. For the integration test's 363-file project with one file added, two modified and one deleted, the expected count is 360.

## Performance Notes

I did not find a published timing for an incremental run. What the design guarantees is the shape of the saving: unchanged files skip reading (when size and time match) and skip parsing (always), while discovery, hashing of the project metadata, analysis and output still run in full.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Analysis is not incremental. | The call graph and indices are rebuilt every run. | None yet. |
| Any rebuild of the parser empties the cache. | The cache key includes the executable's size and time. | Expected; the first run after an upgrade is a full parse. |
| Files that failed to parse are not cached. | Only successful results are recorded, so a failing file is retried each run. | Fix or `.euignore` the file. |
| The cache only remembers the files seen in the last run. | The new index is built from the files visited this run. Files filtered out (for example by `--languages`) are dropped from it. | Keep `--languages` consistent between runs. |
| The cache directory can grow while runs overlap. | Old generations are removed only up to the one a run loaded. | Delete `parse_cache` if it grows unexpectedly. |
| A crashed run starts over from the last committed generation. | The earlier `--resume` option was removed in the same change. | None needed. |

## Related Docs

- [Spill to Disk](Spill-To-Disk.md): the files the cache is built from.
- [eulix-parser Overview](../eulix-parser/Overview.md): the whole tool.
- [Parsing Pipeline](../eulix-parser/Parsing-Pipeline.md): where `parse_file` sits in a run.
