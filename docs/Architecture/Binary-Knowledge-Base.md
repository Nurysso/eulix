# Binary Knowledge Base

This page records the design for replacing the JSON files that `eulix-parser` writes with FlatBuffers, while keeping an exact JSON copy for inspecting changes.

> **Crate / path:** `eulix-parser/src/output.rs`, `eulix-parser/src/spill.rs`, `eulix-parser/src/cache.rs`, `libeulix/src/utils/file_walker.rs` (writers); `eulix-cli/internal/query/`, `eulix-cli/internal/checksum/`, `eulix-embed/`, `bench/`, `scripts/` (readers)
> **Language:** Rust, Go, Python
> **Status:** implemented: every file the parser writes is FlatBuffers (the parse cache's slim records FlexBuffers); the JSON readers are removed, except the bench's fallbacks for older revisions
> **Last verified against:** `0a78aa7`

## Overview

Today every file the parser writes is JSON: the knowledge base (`kb.json`), its index and call graph, five small report files, the checksum, and the incremental parse cache. Every query starts by decoding the large ones. On big repositories those files reach hundreds of megabytes (the [mmap](../eulix-cli/query/retrieval/Mmap.md) notes quote a 178 MB `kb.json` and a 228 MB `kb_index.json`), so the decode dominates start-up time and memory.

The plan is to write every parser output as [FlatBuffers](https://flatbuffers.dev/), a binary format that a reader can use directly without parsing it first. JSON stays available through one parser flag, `--emit-json`, which writes a one-to-one JSON copy next to each binary file so a developer can open it in an editor and see exactly what changed.

After reading this page you will know which files change, how the large knowledge base is laid out, how the JSON copy is produced, and why each choice was made.

## Why It Exists

JSON costs three times: the parser spends time and memory writing it, every reader spends time and memory parsing it, and the files are larger than they need to be. The Go loader already works hard to soften this, with memory mapping, a fast decoder and a streaming walk over `kb.json` ([Index Loading](../eulix-cli/query/retrieval/Index-Loading.md)). A binary format with direct access removes the parse step instead of optimizing it.

JSON is still the easiest thing to read when checking a parser change, so it is kept as an opt-in debugging aid rather than removed outright.

## How It Works

### Files that change

Every file the parser writes moves to FlatBuffers. The binary files take a `.fb` extension. With `--emit-json`, the JSON copies keep today's names, so tools that only need a quick look still find them.

| Today                                                                                                     | Binary output                                                                                     | JSON copy with `--emit-json`                           | Main readers                                                   |
| --------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | ------------------------------------------------------ | -------------------------------------------------------------- |
| `kb.json`                                                                                                 | `kb.fb`                                                                                           | `kb.json`                                              | Go retrieval loader, Python embedder, bench, scripts           |
| `kb_index.json`                                                                                           | `kb_index.fb`                                                                                     | `kb_index.json`                                        | Go retrieval loader, classifier, router                        |
| `kb_call_graph.json`                                                                                      | `kb_call_graph.fb`                                                                                | `kb_call_graph.json`                                   | Go retrieval loader, router, vizEulize, bench call-graph tools |
| `kb_summary.json`, `kb_metrics.json`, `kb_entry_points.json`, `kb_external_deps.json`, `kb_patterns.json` | `kb_<name>.fb`                                                                                    | `kb_<name>.json`                                       | Go router (metrics, entry points, dependencies), bench         |
| `checksum.json.zst`                                                                                       | `checksum.fb.zst`                                                                                 | `checksum.json`                                        | Go checksum detector                                           |
| `parse_cache/` index and records                                                                          | index `index.<gen>.fb.zst` (zstd); emit records are `kb.fb` records; slim records are FlexBuffers | `parse_cache/index.json` (no copy of the slim records) | The parser itself                                              |

The optional parse report (`--parse-report`) is a tab-separated text report, not JSON, and stays as it is.

### One schema for every language

The schemas live in `schema/`: one file per output (FlatBuffers allows one root type and one file identifier per schema file) plus `common.fbs` for shared tables. Code for Rust (`libeulix/src/fb/`), Go (`eulix-cli/internal/fb/`) and Python (`eulix-embed/fb/`) is generated with a pinned `flatc` (25.12.19) by `scripts/gen_flatbuffers.sh` (`make schema`), and the generated code is committed so a normal build needs no extra tool. CI fails if it is out of date (`make schema-check`). Each file type gets its own four-letter file identifier, so a reader handed the wrong file fails at once with a clear message.

Field names in the schema match today's JSON keys. That keeps the JSON copy looking like the files people already know, and keeps the diff readable when the format changes.

### Layout of the knowledge base

A single FlatBuffer cannot be larger than 2 GiB, because its internal offsets are 32-bit. The knowledge base for a very large repository can exceed that, so `kb.fb` is not one buffer. It is:

1. a small header with the file identifier, the format version and the metadata;
2. an offset table with one entry per source file (path, position, length), in path order;
3. one size-prefixed FlatBuffer record per source file.

This fits the existing [spill](Spill-To-Disk.md) design. Each file's record is serialized once, during parsing, into the spill, and the final file is assembled by copying those records into place, exactly as `kb.json` is assembled today. A reader can walk the records in order (what the Go streaming loader and the Python embedder do) or jump straight to one file through the table.

The other outputs are small enough to be one buffer each. The parser stops with a clear error if one of them would pass the 2 GiB limit, instead of writing a broken file.

### Shared strings

Node ids, paths and caller names repeat across the index and the call graph, so the writer stores each distinct string once and points every use at it. It does not use the FlatBuffers builder's own `create_shared_string`: that keeps its pool in a sorted vector and inserts into it, which is quadratic in the number of distinct strings (2.4 s to encode the Kubernetes index, against 0.25 s with a hash map). The Go readers intern the same strings as they copy them out, so the saving carries over to memory.

### Readers

- **Go** reads the binary files through the existing memory mapping and converts them, at load time, into the same in-memory structures it builds today. Retrieval, ranking and the router do not change. Strings are copied out of the mapping, as they are now, so nothing points into the file after loading.
- **Python** reads `kb.fb` record by record (`eulix-embed/data_io/fb_stream.py`). The generated Python accessors turned out 2.7x slower than the old ijson path on Kubernetes (3.6 s against 1.3 s), so the embedder decodes records straight from the wire format with `struct`, which takes 0.7 s, about 1.8x faster than ijson. A test keeps the fast decoder equal to the generated code, and the chunker produced the same 12,078 chunks from both formats on Django. The bench and scripts use the generated code with the `flatbuffers` runtime pinned in `bench/requirements.txt`, and reuse the embedder's decoder for `kb.fb`.
- **vizEulize** decodes `kb_call_graph.fb` in the browser with a small hand-written reader in its worker (`decodeFB` in `vizeulize.html`), so the page stays one file with no FlatBuffers runtime. `eulix vizeulize` serves the file's bytes unchanged unless `.euignore` or hidden paths remove nodes; then Go writes a trimmed buffer. A Node test compares the decoder with the parser's JSON copy of a fixture.
- **The parser** reads its own cache records back with the Rust bindings.

### The JSON copy

`--emit-json`, a flag of `eulix_parser` only (run the parser directly to get the copies; `eulix analyze` never passes it), writes a JSON file next to each binary one. The copy is built by reading the finished binary buffer back through the generated accessors (`libeulix/src/fb_io.rs`), not from the parser's in-memory values, so it shows exactly what the file holds: fields in schema order, absent optional fields left out, and maps as sorted `{key, values}` lists. `scripts/check_fb_json.py` compares every copy with `flatc`'s own decoding of the `.fb` file, and CI runs it. A copy that disagreed with the binary file would make debugging worse than having none. Without the flag, stale copies from an earlier run are deleted.

The copy is for people. No Eulix component reads it, with one temporary exception listed under Limitations.

### Safe replacement

The parser writes each file under a temporary name and renames it into place when it is complete. A reader that still has the old file open keeps reading the old contents, instead of crashing because the file changed underneath it. This matters more with a binary format, because readers keep the mapping open while they work, and it also removes the cause of [RET-10](../Known-Issues/Retrieval-And-Query.md#ret-10-on-windows-eulix-analyze-can-fail-while-eulix-reads-the-same-knowledge-base).

### No backward compatibility

There are few users, so old JSON-only `.eulix/` folders are not supported. The analysis-complete marker records the format version. When it is missing or older, `eulix analyze` runs a full analysis, and `chat`, `query` and the MCP server ask for one, instead of failing on a file they cannot decode.

## Decisions

| Decision                                                                                               | Reason                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| ------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FlatBuffers rather than Cap'n Proto, MessagePack or a Rust-only format.                                | Direct access without parsing, official generators for Rust, Go and Python, and a schema that also documents the files. MessagePack would still need a full decode; Rust-only formats would need hand-written Go and Python readers.                                                                                                                                                                                                                                         |
| Every parser output moves, including the small report files, the checksum and the parse cache.         | One format and one toolchain everywhere, instead of two formats to maintain.                                                                                                                                                                                                                                                                                                                                                                                                 |
| The JSON copy is written by the parser, behind `--emit-json`. There is no separate conversion command. | Converting a large binary file to JSON after the fact can use a lot of memory. The parser already has the data in hand and can write the copy while it writes the binary file.                                                                                                                                                                                                                                                                                               |
| The JSON copy is a one-to-one image of the binary file, checked by a test.                             | Its only purpose is to show exactly what the binary file contains.                                                                                                                                                                                                                                                                                                                                                                                                           |
| `kb.fb` is a header, an offset table and per-file records.                                             | It avoids the 2 GiB limit of a single FlatBuffer and reuses the spill design.                                                                                                                                                                                                                                                                                                                                                                                                |
| Go converts to its existing structures at load time.                                                   | It confines the change to the loaders, so retrieval behaviour stays the same and can be compared directly against the JSON version.                                                                                                                                                                                                                                                                                                                                          |
| No backward compatibility; old indexes force a re-analysis.                                            | There are few users, and supporting both formats would double the reader code.                                                                                                                                                                                                                                                                                                                                                                                               |
| vizEulize reads `kb_call_graph.fb`, and hidden or ignored files are removed by Go, not by the page.    | Chrome and Edge cannot load a JSON copy past about 512 MB (Linux's is 591 MB), and in Firefox the `.fb` loads Linux's graph in 3.6 s against 5.6 s for the JSON. Trimming in Go means the browser never receives the hidden names, and the common case, nothing hidden, costs no re-encoding.                                                                                                                                                                                |
| The parse cache's slim records are FlexBuffers, not schema FlatBuffers.                                | They are the whole slimmed `FileData` (about 270 fields across 17 libeulix structs) and only the parser reads them; FlexBuffers, FlatBuffers' schemaless format, follows every struct change through serde with no schema to maintain. On Kubernetes the slim spill is 134 MB against 217 MB as JSON and a cached re-run peaks at 715 MB against 900 MB, but decoding is slower (0.81 s against 0.53 s on one thread), so a fully cached re-run takes 1.25 s against 1.09 s. |

## Measured results

Sizes of `.eulix/` from `du -sh`, the same repository parsed by the last JSON build and by the FlatBuffers build. Parse time and peak memory were the same for both.

| File                        | Kubernetes JSON | Kubernetes FlatBuffers | Linux kernel JSON | Linux kernel FlatBuffers |
| --------------------------- | --------------- | ---------------------- | ----------------- | ------------------------ |
| `kb`                        | 57 MB           | 51 MB                  | 474 MB            | 404 MB                   |
| `kb_index`                  | 86 MB           | 27 MB                  | 364 MB            | 164 MB                   |
| `kb_call_graph`             | 67 MB           | 21 MB                  | 591 MB            | 161 MB                   |
| `kb_external_deps`          | 4.6 MB          | 5.1 MB                 | 12 MB             | 15 MB                    |
| `kb_summary`                | 980 KB          | 988 KB                 | 3.3 MB            | 3.3 MB                   |
| `kb_entry_points`           | 20 KB           | 12 KB                  | 244 KB            | 140 KB                   |
| `kb_metrics`, `kb_patterns` | 4 to 8 KB       | 4 KB                   | 4 to 8 KB         | 4 KB                     |
| `checksum` (zstd)           | 216 KB          | 316 KB                 | 1.2 MB            | 1.7 MB                   |
| `parse_cache/`              | 275 MB          | 186 MB                 | 1.5 GB            | 1.1 GB                   |
| **Total**                   | **489 MB**      | **290 MB**             | **2.9 GB**        | **1.8 GB**               |

The index and call graph shrink the most, because they repeat the same IDs and paths and the binary files store each string once. `kb` shrinks least, since it was already slimmed and is mostly unique source text. Two files grow, for reasons not yet measured. The checksum probably compresses less well because zstd found more repetition in the JSON's keys and hex text than in the binary table. `kb_external_deps` is many small records, where each FlatBuffers table's vtable and alignment padding likely cost more than the JSON keys did.

## Limitations

| Limitation                                                                        | Why it happens                                                | Workaround                                                                                                                                                                                                   |
| --------------------------------------------------------------------------------- | ------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| The parser builds the index and call-graph buffers in memory before writing them. | FlatBuffers are built whole; the JSON writer streamed.        | On Kubernetes the peak after analysis rose from 735 MB to 790 MB, and the end-of-run peak stayed within run-to-run noise (about 840 to 920 MB). Those files can move to the per-record layout if this grows. |
| Bench results that hash output files are not comparable across the format change. | The hashes cover the encoded data.                            | Re-run baselines after the migration.                                                                                                                                                                        |
| Python reads are pure Python.                                                     | The official Python FlatBuffers library has no compiled core. | The no-slower benchmark gate on the embedder.                                                                                                                                                                |

## Related Docs

- [Spill to Disk](Spill-To-Disk.md): the per-file records the knowledge base is assembled from.
- [Incremental Parsing](Incremental-Parsing.md): the parse cache that also moves to FlatBuffers.
- [Output Files](../eulix-parser/Output-Files.md): today's JSON outputs.
- [Index Loading](../eulix-cli/query/retrieval/Index-Loading.md) and [mmap](../eulix-cli/query/retrieval/Mmap.md): the Go readers.
