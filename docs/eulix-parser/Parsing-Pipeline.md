# Parsing Pipeline

This page walks through what `eulix-parser` does during a run, phase by phase, and explains the reasoning behind the choices that are not obvious from the code.

> **Crate / path:** `eulix-parser/src/`  
> **Language:** Rust  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

A run is four phases: discovery and parsing, analysis, summary, and output. Phase 1 does the most work and has the most moving parts, so most of this page is about it. The goal of the whole pipeline is a set of JSON files that describe the repository, produced quickly, with memory use that grows slowly, and with the same result every time.

If you are new to the project, read this page after the [Overview](Overview.md). It is the best map of where each source file fits.

## Why It Exists

Each phase is separate because each one has a different shape of work. Parsing is independent per file, so it spreads across threads. Call-graph building needs every file at once. Output writing is mostly copying. Keeping them separate lets each use the approach that suits it, and lets `--no-analyze` skip the middle two entirely.

## How It Works

### Phase 1: discovery and parsing

**Finding the files.** `collect_source_files_and_hash` walks the repository with `libeulix`'s `FileWalker`, which honours `.euignore`. It keeps only files whose extension belongs to a requested language. Extensions are compared case-insensitively, so `UPPER.PY` is parsed. A `.inc` file is accepted only if it contains a PHP opening tag, because C, C++ and assembler projects use that extension too. In the same walk, the tool computes a project hash from every file's path, size and modification time, and writes `checksum.json.zst` into the output directory. The file list is then sorted and de-duplicated so the order never depends on the file system.

**Spreading the work.** The files are cut into chunks, and the chunk size is `files ÷ (threads × 8)`, limited to between 16 and 200. Chunks keep the scheduling overhead low, and having several times more chunks than threads keeps the threads evenly busy when some files are much larger than others. Each worker keeps its own accumulator (`Acc`) with the results for its files, and the accumulators are merged at the end. Because nothing is shared while parsing, there is no lock to wait on for results. (Only the spill writer takes a lock, briefly, as described in [Spill to Disk](../Architecture/Spill-To-Disk.md).)

**Parsing one file.** `parse_file` does these steps for each file:

1. Check the [incremental cache](../Architecture/Incremental-Parsing.md). If the file is unchanged, reuse the stored result and stop here.
2. Read the file's bytes.
3. If the bytes are not valid UTF-8, replace the bad bytes and keep going. Old code often has Latin-1 comments or strings, and failing the whole file for that would lose useful symbols. The file is recorded as *partial*, with the line of the first bad byte, so the loss is still visible.
4. Parse with `libeulix::grammar_files::parse_source`.
5. Write the detailed view to the spill, collect external imports, slim the in-memory copy, and write the slim view to the second spill. These steps are explained in [Spill to Disk](../Architecture/Spill-To-Disk.md).

**Clean, partial and failed.** Every file ends in one of three states, and the run reports all three:

| State | Meaning | In the knowledge base? |
| --- | --- | --- |
| Clean | Tree-sitter produced a tree with no error or missing nodes. | Yes |
| Partial | A tree was produced, but it contains `ERROR` or `MISSING` nodes, or the file had invalid UTF-8. The file is extracted on a best-effort basis. | Yes |
| Failed | No usable tree: an I/O error, an unsupported language, or a parser error. | No |

This matters because Tree-sitter never refuses to parse. It always returns a tree and marks the parts it could not fit. A "partial" count is therefore an honest measure of how much syntax the grammars did not understand. `--parse-report` writes the full list as a TSV, sorted by path, so two runs can be compared with an ordinary `diff`.

**The thread pool.** All parallel work runs on one `Engine`-owned pool, built with the `--threads` value, and not on rayon's global pool. Each worker gets a 64 MiB stack. The main tree walker in `libeulix` (`walk` in `grammar_files/utils.rs`) is iterative now, so the large stack is a safety margin for any code path that still recurses on a very deep tree (typical of generated or minified code), where overflowing the default 2 MiB stack would abort the whole process. The extra stack is only used by files that go that deep. A test parses a 10,000-term expression to guard this.

**Metadata.** After merging, the tool fills in `Metadata`: version, git hash (embedded at build time), project name, project hash, parse time, languages and the totals. It also turns the import records collected during parsing into the external-dependency list at this point. That is why the parser's analysis phase does not run `libeulix`'s own dependency pass.

### Phase 2: analysis

Unless `--no-analyze` is given, `analyze_and_build` runs on the same thread pool. It mirrors the function of the same name in `libeulix`, with two differences: it does not run the dependency pass (already done in phase 1), and it is not deprecated here. In order:

- With PRISM v1: `resolve_call_locations`, then `build_call_graph`, then `populate_called_by`.
- With PRISM v2: `build_call_graph_v2`, then `populate_called_by_from_graph`.
- `detect_patterns` and `find_entry_points`.

Above 100,000 files the call-graph step is skipped, and the tool says so when verbose. The details of the PRISM steps are in [PRISM v1](../libeulix/PRISM-V1.md) and [PRISM v2](../libeulix/PRISM-V2.md). Note that this phase reads the *slim* data kept in memory, which is why the slim view keeps exactly the fields PRISM needs.

### Phase 3: summary

`generate_summary` builds the project summary, and `generate_metrics` keeps the 20 most complex functions (`TOP_K = 20`). See [Analysis Passes](../libeulix/Analysis-Passes.md).

### Phase 4: output

The tool computes the indices, builds borrowed views, and streams each artifact to disk. `kb.json` is assembled from the spill; the others are serialized directly from the views. Every file is described in [Output Files](Output-Files.md).

### Main Components

| Component | Location | Responsibility |
| --- | --- | --- |
| `parse_directory` | `parse/directory.rs` | Runs phase 1 end to end and builds the knowledge base shell. |
| `parse_file` | `parse/file.rs` | Handles one file: cache, read, parse, spill, slim. |
| `Acc` | `parse/acc.rs` | Per-worker accumulator; merged after parsing. |
| `slim`, `strip_emitted` | `parse/slim.rs` | Remove data that is on disk or not needed later. |
| `Engine` | `engine.rs` | The thread pool. |
| `ParseStats` | `stats.rs` | Clean, partial and failed counts, per language. |

### Data In, Data Out

Phase 1 returns three things: a knowledge base whose `structure` holds slim data, the parse statistics, and a `SpillIndex` locating each file's detailed record. The later phases consume them.

## Key Algorithms

### Making the output the same every time

**The problem.** Parallel code finishes in a different order on every run. If that order leaks into the output, two runs on identical input produce different files, which makes diffs and caches useless.

**The approach.** Wherever order could matter, the tool sorts: the file list, the spill index, the partial and failed lists, and the maps in the knowledge base (`BTreeMap`). PRISM sorts files before it chooses anything. An integration test, `tests/determinism.rs`, runs the real binary with 1 thread and with 8 and checks that all eight JSON files are identical once the three time-dependent fields (`parsed_at`, `project_hash`, `git_hash`) are blanked. A second test checks that an incremental run matches a fresh run the same way.

**Why this way.** Reproducible output is what lets the rest of Eulix trust and cache the files.

### Two process-level safeguards in `main`

The binary uses the `rpmalloc` memory allocator, and on Unix it ends with `_exit` instead of a normal shutdown. The source comment explains why: rpmalloc's exit-time cleanup unmaps memory while detached rayon workers are still cleaning up their own, which crashed on large runs, and the operating system reclaims the memory anyway. It also ignores `SIGPIPE`, so piping the output into a program that closes early does not kill the run.

## Configuration

See the option table in [Overview](Overview.md). Two internal values shape phase 1: the chunk size (16 to 200 files) and the worker stack size (64 MiB).

## Usage Examples

To see what the parser could not fully understand, ask for a report and look at it:

```bash
eulix_parser -r ./proj -p 2 -o out/kb.json --parse-report out/parse.tsv
head out/parse.tsv
```

Expected result: a header line `status language error_nodes missing_nodes first_error_line path reason` (tab-separated), followed by one line per partial or failed file.

## Performance Notes

On Linux, the parser gives the kernel read hints: it asks for read-ahead on the first 1,000 files, marks each opened file as sequential, and drops large files (over 10 MiB) from the page cache once read. macOS and Windows get their own equivalents or nothing. See [Performance Notes](Performance-Notes.md) for an open question about these hints.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| A file with invalid UTF-8 is always partial. | The bad bytes are replaced, and the loss is reported deliberately. | Convert the file to UTF-8. |
| A failed file is retried every run. | Only successful files enter the cache. | Fix it or add it to `.euignore`. |
| Phase 2 holds every file's slim data in memory. | The call graph needs all of it. | `--no-analyze`. |
| The `main.rs` header comment is out of date. | It describes four artifacts named `kb.json`, `kb_index.json` and so on, and a narrower language table. | Trust this page and [Output Files](Output-Files.md). |

## Related Docs

- [Overview](Overview.md): options and examples.
- [Output Files](Output-Files.md): the artifacts.
- [Spill to Disk](../Architecture/Spill-To-Disk.md) and [Incremental Parsing](../Architecture/Incremental-Parsing.md): how phase 1 stays fast and light.
- [Analysis Passes](../libeulix/Analysis-Passes.md): what phases 2 and 3 compute.
