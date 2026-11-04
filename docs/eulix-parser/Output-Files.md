# Output Files

This page describes every file that `eulix_parser` writes, what it contains, and which tools read it.

> **Crate / path:** `eulix-parser/src/output.rs`, `eulix-parser/src/views.rs`  
> **Language:** Rust  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

A normal run writes eight JSON files, plus a checksum file and a cache directory. They are all written into the directory that contains your `--output` path, and the seven secondary files are named after the main file's stem. With `--output .eulix/kb.json`, the files are `.eulix/kb.json`, `.eulix/kb_index.json`, `.eulix/kb_call_graph.json`, and so on. With the default `knowledge_base.json`, they are `knowledge_base_index.json` and so on.

The files are split by purpose, so that a tool needing only one kind of information can read only that file. A retrieval engine, for example, can load the call graph without loading every function's signature.

## Why It Exists

One giant file would force every consumer to load everything, and for a large repository that can be gigabytes. Splitting the data lets each consumer load what it needs, and lets the parser write each piece in a streaming way, straight from borrowed views of the in-memory data, without building a second copy.

## How It Works

Phase 4 of the [pipeline](Parsing-Pipeline.md) writes the files in this order: the knowledge base, then the index, summary, call graph, metrics, entry points, external dependencies and patterns. `kb.json` is assembled from the [spill file](../Architecture/Spill-To-Disk.md). The others are serialized directly with `serde_json`, some pretty-printed and some compact.

### Main Components

| File (with default stem `kb`) | Format | Contents |
| --- | --- | --- |
| `kb.json` | compact | `metadata` and `structure`: for every file, its language, line count, functions (id, name, signature, parameters, return type, docstring, line range), classes (with methods, attributes, bases, decorators) and TODOs. |
| `kb_index.json` | compact | Lookup indices: functions by name (`path:line`), the callers of each callee, functions by tag, types by name, and files by category. |
| `kb_call_graph.json` | compact | Call-graph `nodes` and `edges`, plus a `tags` map (node id to tags) and a `file_categories` map (file to category). Each map is left out when empty. |
| `kb_summary.json` | pretty | Project name, file and line totals, languages, files grouped by category, key features from docstrings, entry points, dependencies split into standard library and third party, and detected patterns. |
| `kb_metrics.json` | pretty | The metadata and the 20 most complex functions, each with complexity, importance score and line range. |
| `kb_entry_points.json` | pretty | `main`-style functions, HTTP routes and CLI commands. |
| `kb_external_deps.json` | compact | Each external package, its source (`stdlib` or `external`), the files that import it, and the import count. |
| `kb_patterns.json` | pretty | Naming convention, structure type and architecture style. |
| `checksum.json.zst` | zstd-compressed JSON | Per-file size and modification time, and the project hash. Written during file discovery. |
| `parse_cache/` | binary and zstd | The [incremental cache](../Architecture/Incremental-Parsing.md). Private to the parser. |

With `--no-analyze`, only `kb.json`, `checksum.json.zst` and `parse_cache/` are written.

### Data In, Data Out

The shapes of the nodes, edges and other records are the types described in [Knowledge Base Schema](../libeulix/Knowledge-Base-Schema.md). The one important difference is `kb.json`: it is **not** a full dump of those types.

## Key Algorithms

### Why `kb.json` is smaller than the in-memory data

**The problem.** The full per-file structures include control flow, exception details, variables, call arguments and more. That data multiplies the file size, and most consumers never read it.

**The approach.** `kb.json` is written through `FileDataSimpleView`, `FunctionSimpleView` and `ClassSimpleView`. These are borrowed views that serialize only a chosen set of fields. Calls, `called_by`, control flow, exceptions, variables, tags, complexity, importance, imports and most language-specific data are left out. Call relationships are available in `kb_call_graph.json` instead, and tags in the call graph's `tags` map.

**Why this way.** In version 0.7.7 this cut `kb.json` for the Linux kernel from about 2.06 GB to about 0.67 GB, and the README reports about 353 MB for v0.8.1. The analysis itself still uses the full relationships in memory, so nothing is lost for the call graph.

**What to remember.** Any consumer that used to read `calls`, `called_by`, `control_flow`, `exceptions`, `is_async` or `tags` from `kb.json` must read `kb_call_graph.json` or other artifacts instead.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `--output` | command line | `knowledge_base.json` | Sets the main file path, and therefore the directory and the stem of every other file. |
| `--no-analyze` | command line | off | Writes only `kb.json`. |

## Usage Examples

```bash
eulix_parser -r ./proj -p 2 -o .eulix/kb.json
ls .eulix
```

Expected result: `kb.json`, `kb_call_graph.json`, `kb_entry_points.json`, `kb_external_deps.json`, `kb_index.json`, `kb_metrics.json`, `kb_patterns.json`, `kb_summary.json`, `checksum.json.zst` and the `parse_cache` directory.

Reading just the call graph:

```bash
jq '.edges | length' .eulix/kb_call_graph.json
```

## Performance Notes

For the Linux kernel at v0.8.1, the README lists these sizes: `kb.json` about 353 MB, `kb_index.json` about 380 MB, `kb_call_graph.json` about 364 MB, `kb_external_deps.json` about 12 MB, `kb_summary.json` about 3 MB, and the rest under 50 KB each. Output generation took about 4 seconds. These are single-machine measurements, not guarantees.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Output paths are not separately configurable. | Everything derives from `--output`. | Pick the output path with the directory you want. |
| The seven secondary artifacts do not exist after `--no-analyze`. | They come from the analysis phase. | Run without `--no-analyze`. |
| `kb.json` has no imports. | `FileDataSimpleView` omits them. | Use `kb_external_deps.json` for external imports. |
| The external-dependency `version` is always null. | Versions live in manifests that are not read. | Read manifests separately. |
| `dependency_graph` is not written anywhere. | It is never populated. | None. |

## Related Docs

- [Parsing Pipeline](Parsing-Pipeline.md): where each file is produced.
- [Knowledge Base Schema](../libeulix/Knowledge-Base-Schema.md): the underlying types.
- [Spill to Disk](../Architecture/Spill-To-Disk.md): how `kb.json` is assembled.
