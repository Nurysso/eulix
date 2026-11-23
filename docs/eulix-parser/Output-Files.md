# Output Files

This page describes every file that `eulix_parser` writes, what it contains, and which tools read it.

> **Crate / path:** `eulix-parser/src/output.rs`, `eulix-parser/src/views.rs`  
> **Language:** Rust  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

A normal run writes eight FlatBuffers output files, plus a checksum file and a cache directory. They are all written into the directory that contains your `--output` path and named after its stem; the knowledge base is `<stem>.fb` whatever extension `--output` has. With `--output .eulix/kb.fb` (or `.eulix/kb.json`), the files are `.eulix/kb.fb`, `.eulix/kb_index.fb`, `.eulix/kb_call_graph.fb`, and so on. With the default `knowledge_base.json`, they are `knowledge_base_index.fb` and so on.

The files are split by purpose, so that a tool needing only one kind of information can read only that file. A retrieval engine, for example, can load the call graph without loading every function's signature.

## Why It Exists

One giant file would force every consumer to load everything, and for a large repository that can be gigabytes. Splitting the data lets each consumer load what it needs, and lets the parser write each piece in a streaming way, straight from borrowed views of the in-memory data, without building a second copy.

## How It Works

Phase 4 of the [pipeline](Parsing-Pipeline.md) writes the files in this order: the knowledge base, then the index, summary, call graph, metrics, entry points, external dependencies and patterns. `kb.fb` is assembled from the [spill file](../Architecture/Spill-To-Disk.md): a header with the metadata and an offset table, then one record per source file (see [Binary Knowledge Base](../Architecture/Binary-Knowledge-Base.md#layout-of-the-knowledge-base)). The index, the call graph and the five report files are FlatBuffers (`.fb`), described by the schemas in `schema/`.

The outputs are moving from JSON to FlatBuffers one group at a time; see [Binary Knowledge Base](../Architecture/Binary-Knowledge-Base.md). Every file is written under a temporary name and renamed into place, so a reader never sees a half-written file. With `--emit-json`, each `.fb` file also gets a JSON copy with the same stem, decoded from the binary file, for inspection only; nothing in Eulix reads the copies.

### Main Components

| File (with default stem `kb`) | Format | Contents |
| --- | --- | --- |
| `kb.fb` | FlatBuffers (header `EUKB`, records `EUFR`) | The metadata and, for every file, its language, line count, functions (id, name, signature, parameters, return type, docstring, line range), classes (with methods, attributes, bases, decorators) and TODOs. |
| `kb_index.fb` | FlatBuffers (`EUIX`) | Lookup indices: functions by name (`path:line`), the callers of each callee, functions by tag, types by name, and files by category, each a list sorted by key. Every distinct string is stored once; on Kubernetes the file is 26 MB against 85 MB as JSON. |
| `kb_call_graph.fb` | FlatBuffers (`EUCG`) | Call-graph `nodes` and `edges`, plus `tags` (node id to tags) and `file_categories` (file to category) as lists sorted by key. Node ids, files and edge endpoints are stored once each (shared strings); on this repository the file is about a third of the old compact JSON. |
| `kb_summary.fb` | FlatBuffers (`EUSM`) | Project name, file and line totals, languages, files grouped by category, key features from docstrings, entry points, dependencies split into standard library and third party, and detected patterns. |
| `kb_metrics.fb` | FlatBuffers (`EUMT`) | The metadata and the 20 most complex functions, each with complexity, importance score and line range. |
| `kb_entry_points.fb` | FlatBuffers (`EUEP`) | `main`-style functions, HTTP routes and CLI commands. |
| `kb_external_deps.fb` | FlatBuffers (`EUXD`) | Each external package, its source (`stdlib` or `external`), the files that import it, and the import count. |
| `kb_patterns.fb` | FlatBuffers (`EUPT`) | Naming convention, structure type and architecture style. |
| `checksum.fb.zst` | zstd-compressed FlatBuffers (`EUCS`) | Per-file size and modification time, and the project hash. Written during file discovery. |
| `parse_cache/` | spills and a zstd-compressed FlatBuffers index | The [incremental cache](../Architecture/Incremental-Parsing.md). Private to the parser. |

With `--no-analyze`, only `kb.fb`, `checksum.fb.zst` and `parse_cache/` are written.

### Data In, Data Out

The shapes of the nodes, edges and other records are the types described in [Knowledge Base Schema](../libeulix/Knowledge-Base-Schema.md). The one important difference is `kb.fb`: it is **not** a full dump of those types.

## Key Algorithms

### Why the knowledge base is smaller than the in-memory data

**The problem.** The full per-file structures include control flow, exception details, variables, call arguments and more. That data multiplies the file size, and most consumers never read it.

**The approach.** Each file's record (`fb_io::encode_file_record`) carries only a chosen set of fields; until the move to FlatBuffers this was the `FileDataSimpleView` serde views. Calls, `called_by`, control flow, exceptions, variables, tags, complexity, importance, imports and most language-specific data are left out. Call relationships are available in `kb_call_graph.fb` instead, and tags in the call graph's `tags` map.

**Why this way.** In version 0.7.7 this cut the (then JSON) knowledge base for the Linux kernel from about 2.06 GB to about 0.67 GB, and the README reports about 353 MB for v0.8.1. The analysis itself still uses the full relationships in memory, so nothing is lost for the call graph.

**What to remember.** Any consumer that used to read `calls`, `called_by`, `control_flow`, `exceptions`, `is_async` or `tags` from the knowledge base must read `kb_call_graph.fb` or other artifacts instead.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `--output` | command line | `knowledge_base.json` | Sets the main file path, and therefore the directory and the stem of every other file. |
| `--no-analyze` | command line | off | Writes only `kb.fb`. |

## Usage Examples

```bash
eulix_parser -r ./proj -p 2 -o .eulix/kb.fb
ls .eulix
```

Expected result: `kb.fb`, `kb_index.fb`, `kb_call_graph.fb`, `kb_entry_points.fb`, `kb_external_deps.fb`, `kb_metrics.fb`, `kb_patterns.fb`, `kb_summary.fb`, `checksum.fb.zst` and the `parse_cache` directory. Add `--emit-json` to also get a `.json` copy of each `.fb` file, `checksum.json` and `parse_cache/index.json`.

Reading just the call graph (with `--emit-json`, which writes the JSON copy):

```bash
jq '.edges | length' .eulix/kb_call_graph.json
```

## Performance Notes

For the Linux kernel at v0.8.1, when every output was JSON, the README lists these sizes: `kb.json` about 353 MB, `kb_index.json` about 380 MB, `kb_call_graph.json` about 364 MB, `kb_external_deps.json` about 12 MB, `kb_summary.json` about 3 MB, and the rest under 50 KB each. Output generation took about 4 seconds. These are single-machine measurements, not guarantees.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Output paths are not separately configurable. | Everything derives from `--output`. | Pick the output path with the directory you want. |
| The seven secondary artifacts do not exist after `--no-analyze`. | They come from the analysis phase. | Run without `--no-analyze`. |
| `kb.fb` has no imports. | The file records leave them out. | Use `kb_external_deps.fb` for external imports. |
| The external-dependency `version` is always null. | Versions live in manifests that are not read. | Read manifests separately. |
| `dependency_graph` is not written anywhere. | It is never populated. | None. |

## Related Docs

- [Parsing Pipeline](Parsing-Pipeline.md): where each file is produced.
- [Knowledge Base Schema](../libeulix/Knowledge-Base-Schema.md): the underlying types.
- [Spill to Disk](../Architecture/Spill-To-Disk.md): how `kb.fb` is assembled.
