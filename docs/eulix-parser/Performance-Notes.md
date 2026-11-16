# Performance Notes

This page collects the measured performance of `eulix_parser` and the lessons recorded while tuning it.

> **Crate / path:** `eulix-parser/`  
> **Language:** Rust  
> **Status:** reference; numbers are from version 0.9.0 (re-measured 2026-11-15)  
> **Last verified against:** `5ecd16d` (code); figures from `scripts/parser_perf.sh`

## Overview

The parser is built to handle repository-scale code on ordinary hardware. This page gives the one current benchmark, the sizes of the outputs, and a short list of tuning attempts that did not pay off, so nobody has to repeat them. All figures are measurements on one machine. They depend on hardware, storage, repository layout, thread count and `.euignore`, and they should not be read as guarantees.

## Why It Exists

Performance decisions in this project were made from measurements, and some of them were surprising (a "standard" optimization made things slower). Writing the results down keeps the reasons next to the code they explain.

## How It Works

The benchmark below is the median of three cold runs on the Linux kernel, on an AMD Ryzen 5 5600X with 12 parser threads and PRISM v2 (eulix-parser v0.9.0):

```bash
scripts/parser_perf.sh -t 12 -p 2 linux
```

| Measure | Result |
| --- | --- |
| Files discovered and parsed | 64,460 (none failed; 32,122 partial, almost all C) |
| Lines of code | 37,322,695 |
| Functions / classes / methods | 647,514 / 211,297 / 7,978 |
| Parse time | 48.69 s |
| Relationships and indices | 1.99 s |
| Summary and metrics | 0.26 s |
| Total reported by the parser | 53.85 s (runs: 52.18, 53.85, 54.66 s) |
| Total from `/usr/bin/time` | 54.44 s |
| Call graph | 774,876 nodes, 1,506,441 edges |
| Peak memory | 3,749 MB (range 3,688–3,749 MB) |

The headline reading is that parsing dominates (about 90% of the total), which is why [incremental parsing](../Architecture/Incremental-Parsing.md) targets it. The figures describe a full parse of the repository. They say nothing about the cost of a repeat run.

On smaller repositories with the same flags: gin (99 files, 24,515 lines) parses in 0.12 s at 85–93 MB, and the JDK (20,058 files, 6.76M lines) in 11.84–12.42 s at 1,349–1,396 MB.

### Main Components

Output sizes for the same run:

| File | Size |
| --- | --- |
| `kb.json` | about 353 MB |
| `kb_index.json` | about 332 MB |
| `kb_call_graph.json` | about 434 MB |
| `kb_external_deps.json` | about 10 MB |
| `kb_summary.json` | about 3 MB |
| `kb_entry_points.json` | about 237 KB |
| `kb_metrics.json`, `kb_patterns.json` | under 5 KB each |

### Data In, Data Out

The history of `kb.json` on this repository: about 2.06 GB in v0.7.6, about 0.67 GB in v0.7.7 (a 67.3% cut, with write volume down 48.9% and the output phase from 6.89 s to 4.08 s), about 353 MB in v0.8.1, and still about 353 MB in v0.9.0 (on a slightly larger kernel tree). The v0.7.7 change targeted the serialized form only. The changelog is explicit that in-memory structures were not reduced.

## Key Algorithms

### Tuning attempts that did not help

These come from the project's performance notes, written during v0.7.2 and v0.7.3.

**Operating-system read hints were slower.** Calling `posix_fadvise` and `readahead` for every file needed an extra `stat` call per file to check its size, which added about 32,000 system calls on the kernel tree, and parse time rose by 3 to 4 seconds. The kernel's own read-ahead already handles this access pattern.

**Pre-allocating output files was slower.** Calling `posix_fallocate` made the kernel zero pages eagerly before they were written, and minor page faults rose from about 326,000 to about 766,000.

**A lock on shared statistics was slower.** An earlier chunked design had every chunk update one mutex-protected statistics collector, which caused contention. The fix was per-worker accumulators that are merged afterward, and the current code uses that design (`fold` and `reduce`, with `Acc`).

**A bug worth remembering.** In the call-graph builder, the resolved edges must actually be placed in the returned `CallGraph`. A line that discarded them once silently dropped about 1.7 million edges.

### An open question: the read hints are back in the code

The notes above say the read hints were reverted in v0.7.3 and should not be added. But the current code has them. `os_io.rs` and `parse/directory.rs` do read-ahead on the first 1,000 files on Linux, `open_source_file` marks files as sequential, and `parse_file` drops files over 10 MiB from the page cache. I found no measurement of whether they help or hurt now. If you work on this area, measure first, and then either update this page or remove the hints.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `--threads` | command line | 4 | More threads speed up parsing, with diminishing returns; the benchmark used 12 and reached 694% CPU use. |
| `--no-analyze` | command line | off | Skips the call graph and analysis, and so saves the memory and time of phases 2 and 3. |
| `--prism` | command line | required | `1` is cheaper than `2`; see [PRISM v1](../libeulix/PRISM-V1.md). |

## Usage Examples

Measure peak memory and wall time yourself:

```bash
/usr/bin/time -v eulix_parser -r ./proj -p 2 -o out/kb.json --verbose 2> time.txt
grep -E 'Maximum resident|Elapsed' time.txt
```

Expected result: the peak resident set size and the elapsed time. With `--verbose` the parser also prints its own peak memory after each phase.

## Performance Notes

An older document in the project history described version 0.5.1 with figures such as "28,551 files in 13.9 s". It predates the spill and cache designs, the PRISM v2 resolver and the slimmer output, so it is not reproduced here.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Only one machine and one repository are benchmarked. | No other benchmark is current. | Run your own measurements. |
| There is no published timing for an incremental run. | It was not measured. | Run twice and compare, as in [Incremental Parsing](../Architecture/Incremental-Parsing.md). |
| Peak memory is high on very large repositories (about 8.3 GB for the kernel). | The slim data for every file is held for the call graph. | `--no-analyze`. |

## Related Docs

- [Overview](Overview.md): options.
- [Spill to Disk](../Architecture/Spill-To-Disk.md) and [Incremental Parsing](../Architecture/Incremental-Parsing.md): the designs that address memory and repeat-run time.
- [Output Files](Output-Files.md): what is written.
