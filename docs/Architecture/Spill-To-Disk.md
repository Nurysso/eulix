# Spill to Disk

This page explains how `eulix-parser` keeps memory under control on very large repositories by writing each file's output to disk as soon as the file is parsed, and then assembling the final `kb.json` from those saved pieces.

> **Crate / path:** `eulix-parser/src/spill.rs`, `eulix-parser/src/parse/file.rs`, `eulix-parser/src/parse/slim.rs`, `eulix-parser/src/output.rs`  
> **Language:** Rust  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

When `eulix-parser` reads a file, it produces a lot of detail: signatures, parameter types, variables, control flow, docstrings, and more. For a repository the size of the Linux kernel (about 64,000 files and 36 million lines), holding all of that detail for every file in memory at once is very expensive. Most of it is only needed for one thing: being written into `kb.json`.

The spill design splits each file's result into two parts the moment parsing finishes. The **detailed part** is serialized and appended to a file on disk, and then it is dropped from memory. The **slim part** stays in memory, because the later analysis phase needs it. At the end of the run, `kb.json` is assembled by copying the saved detailed pieces straight into the output file, without serializing them again.

After reading this page you will know what a spill file is, what is kept in memory and what is not, and why the final write is so cheap.

## Why It Exists

Early versions of the parser held every parsed structure in memory until the end. The author's performance notes record that those versions ran out of memory on repositories over roughly two million lines of code, because copies of the syntax data were being kept alive while the call graph was built.

The key observation is that the output and the analysis need *different* data. The output wants signatures, types and docstrings. The call graph wants ids, names, calls and class bases. Once those two needs are separated, the output side does not have to live in memory at all. It can be written once, to disk, and read back only at the very end.

## How It Works

For every file, `parse_file` does the following, in this exact order. The order matters, as explained after the steps.

1. **Parse the file** into a full `FileData`, using `libeulix`.
2. **Write the detailed view to the "emit" spill.** The file's data is serialized with `FileDataSimpleView`, a view that borrows the full structure and writes only the fields that belong in `kb.json`. The resulting bytes are appended to the emit spill, and the parser remembers the position (offset and length) of that record.
3. **Collect the external imports.** The import list is copied out now, because the next step may clear it.
4. **Slim the in-memory copy.** `slim` and `strip_emitted` remove everything that has already been written out, or that no later phase needs.
5. **Write the slim view to the "slim" spill.** This copy is what the incremental cache uses on the next run (see [Incremental Parsing](Incremental-Parsing.md)).
6. **Keep the slim `FileData` in memory** for the analysis phase.

The order matters because step 2 must see the full data, so it has to run before anything is stripped. Step 3 has the same constraint for imports.

At the end of the run, after analysis, `write_kb_from_spill` produces `kb.json`. It writes the metadata, then for each file in path order it writes the path and copies that file's saved bytes into the output. The saved bytes are already valid JSON, so nothing is parsed or re-serialized.

### Main Components

| Component | Location | Responsibility |
| --- | --- | --- |
| `Spill` | `spill.rs` | An append-only file of records, safe to write from many threads. Returns the location of each record. |
| `SpillIndex` | `spill.rs` | The list of `(relative path, offset, length)` entries for the emit spill, kept in memory. |
| `FileDataSimpleView` | `views.rs` | The borrowed view that decides which fields reach `kb.json`. |
| `slim`, `strip_emitted` | `parse/slim.rs` | Strip data that is already on disk or not needed later. |
| `write_kb_from_spill` | `output.rs` | Builds `kb.json` by splicing saved records together. |

### Data In, Data Out

**What each version holds.**

| Version | Where it lives | Contains | Used for |
| --- | --- | --- | --- |
| Full `FileData` | memory, briefly | Everything the parser extracted. | Producing the other two. |
| Emit record | emit spill on disk | Language, line count, functions (id, name, signature, params, return type, docstring, lines), classes (with methods and attributes), TODOs. No imports. | The final `kb.json`. |
| Slim `FileData` | memory, and slim spill on disk | Ids, names, line ranges, call sites (callee, receiver, line, conditional flag), class bases, decorators, tags, complexity, importance, docstrings, parameter names. | Analysis (PRISM, entry points, indices, metrics, summary) and the incremental cache. |

The slim view drops, among other things, variables, control flow, exception details, call arguments, signatures, return types and parameter type annotations, global variables, security notes, and most language-specific information. It deliberately keeps what PRISM v2 needs: PHP namespaces, base classes and `static` flags, Python `staticmethod` and `classmethod` flags, and the import lists of PHP, Python, TypeScript and Rust files, which receiver-aware resolution reads.

**The record format.** Every record in a spill file has the same layout:

```text
[u16 path length][path bytes][u32 payload length][u32 CRC32][payload bytes]
```

`append` returns the offset where the payload starts, so the CRC sits in the four bytes just before it. Anyone holding an `(offset, length)` pair can read the payload straight from a memory-mapped file, and check it against its checksum.

## Key Algorithms

### Writing from many threads without slowing down

**The problem.** Parsing runs on many threads, and every thread wants to append to the same spill file. A naive design would make the threads wait for each other on every record.

**The approach.** Each `Spill` has one buffered writer (1 MiB buffer) behind a mutex. The expensive step, computing the CRC of the payload, is done *before* taking the lock, so the lock only covers a few small writes into a memory buffer. The buffer is flushed to disk every 512 records, and once more at the end of the run.

**Why this way.** The lock is held for a very short time, so contention stays low, and writing is sequential, which disks handle well. The cost is that record order in the file depends on which thread got there first. That is fine, because nothing relies on file order: the index records where each record is, and the output step sorts by path.

### Assembling `kb.json` without re-serializing

**The problem.** Serializing a whole knowledge base at the end is slow and needs the whole structure in memory.

**The approach.** The parser sorts the in-memory index by path, memory-maps the emit spill, and writes `{"metadata":...,"structure":{` followed by each `"path":` and the matching byte range copied from the mapping, separated by commas, and finally `}}`. The operating system pages the mapped file in as needed.

**Why this way.** Each fragment was serialized once, in parallel, while parsing. The final step is mostly copying bytes, which is why the output phase is short even for a 350 MB file. Sorting by path also makes the output identical from run to run, whatever order the threads finished in.

### Keeping the other artifacts separate

`kb.json` is the only artifact built from spill records. The call graph, indices, summary, metrics, entry points, dependencies and patterns are all produced from the slim in-memory structures after analysis, and written with streaming serializers through borrowed views (`CallGraphRef`, `IndexViewRef`, and so on), so no second copy is built.

## Configuration

The spill has no settings of its own. Its files live in `<output directory>/parse_cache/`, where the output directory is the parent of the `--output` path. The file names include a generation number; see [Incremental Parsing](Incremental-Parsing.md).

| Constant | Where | Value | Meaning |
| --- | --- | --- | --- |
| `FLUSH_EVERY` | `spill.rs` | 512 records | How often a spill's buffer is flushed to disk. |
| Writer buffer | `spill.rs` | 1 MiB | Size of each spill's write buffer. |
| Output buffer | `output.rs` | 256 KiB | Size of the buffer used while writing output files. |

## Usage Examples

There is nothing to switch on. A normal run uses spill files automatically:

```bash
eulix_parser --root ./my_project --prism 2 --output .eulix/kb.json --verbose
ls .eulix/parse_cache/
```

Expected result: the `parse_cache` directory contains `emit.<generation>.spill`, `slim.<generation>.spill` and `index.<generation>.json.zst`.

## Performance Notes

The changelog for v0.7.7 records the measured effect of trimming what `kb.json` contains: on the Linux kernel, the file dropped from about 2.06 GB to about 0.67 GB, and the output phase from about 6.89 s to about 4.08 s. That change is about *what is written*, and the changelog says it targets the serialized representation, not the parser's in-memory structures. I did not find a measurement that isolates the memory saved by the spill design itself, so I am not quoting one.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The in-memory slim structures still grow with repository size. | The analysis phase needs every file's slim data at once. | Use `--no-analyze` for the smallest footprint; it skips the call graph. |
| The final `kb.json` write does not re-check record checksums. | `write_kb_from_spill` copies bytes from the current run's emit spill without calling the CRC check; the check is used only when reading a previous run's cache. | None needed in normal use. A failed disk write would surface as an I/O error. |
| `kb.json` omits calls, `called_by`, control flow and exception data. | Those fields are not part of `FileDataSimpleView`. | Use `knowledge_base_call_graph.json` for call relationships. |
| Spill files use disk space. | They are the on-disk copy of every file's output. | They are cleaned up automatically, see [Incremental Parsing](Incremental-Parsing.md). |
| A spill path longer than 65,535 bytes is rejected. | The path length is stored in a `u16`. | Not a practical limit. |

## Related Docs

- [Incremental Parsing](Incremental-Parsing.md): how the same files make repeat runs fast.
- [eulix-parser Overview](../eulix-parser/Overview.md): the whole tool.
- [Parsing Pipeline](../eulix-parser/Parsing-Pipeline.md): the phases that use the spill.
- [Knowledge Base Schema](../libeulix/Knowledge-Base-Schema.md): the full structures that the slim and emit views are cut from.
