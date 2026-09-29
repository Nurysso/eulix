# Changelog

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
- All the grammar files now uses `regex::bytes::Regex` instead of `regex::Regex`.
- parser/utils.rs created to store common shared functions accross the grammar files.

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
