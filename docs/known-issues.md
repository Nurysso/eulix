# Known Issues

This document tracks current known issues, bugs, and architectural limitations within the project. It is organized by subsystem, with each entry describing the symptom, cause, and current status or workaround where applicable.

---

## Parser

### Inaccurate call graphs (PRISM approximation)

Eulix uses **PRISM** (_Polyglot Resolution via inverted Symbol Map_), a call-graph approximation algorithm. Because PRISM infers call relationships from symbol resolution rather than a full type-aware compiler frontend, the resulting call graphs are inherently approximate — edges may be missing, over-approximated, or resolved to the wrong target in ambiguous cases.

Two algorithm versions are available. Both produce the **same node set** (defined by the parser); only **edges** differ.

| Version | Edges (same corpus) | Analysis time | Peak RSS (analysis) | Characteristics                                                                                                                                                                                                   |
| ------- | ------------------: | ------------: | ------------------: | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **v1**  |           2,093,881 |        2.79 s |          4,622.8 MB | Resolves call _locations_ first, then builds the graph. Higher raw edge count (recall-oriented), more over-approximation, higher memory.                                                                          |
| **v2**  |           1,510,341 |        1.34 s |          2,727.6 MB | Resolves calls **and inheritance together** via the inverted symbol map. ~28% fewer but better-qualified edges (precision-oriented), ~41% lower peak memory, and ~2× faster in the analysis phase on this corpus. |

Measured on a 64,460-file Linux-kernel-scale checkout, 12 threads, PRISM v2 parser output as input to both.

**Guidance:**

| If you care about…                                                      | Use              |
| ----------------------------------------------------------------------- | ---------------- |
| Precision, inheritance-aware call graphs, lower memory, faster analysis | **v2** (default) |
| Maximum raw edge coverage (recall), with downstream filtering           | v1               |

> v2 is ~2× faster in the analysis phase because v1's extra call-location-resolution sweep dominates its cost. v1's only advantage is higher raw edge count for recall-oriented pipelines.

**Status:** Known limitation. Approximation is inherent to PRISM; exact call graphs would require a full compiler frontend.

### Limited language detection

The parser currently detects and processes only:

`C`, `C++`, `Go`, `Java`, `JavaScript`, `Python`, `Rust`, `TypeScript`.

Other widely used languages — including **Ruby, PHP, and C#** — are **not** detected or parsed at this time.

**Status:** Known limitation. Additional language support is planned but unscheduled.

### C/C++ partial parse

On real-world production C/C++ codebases, files will **almost always** parse only partially. This is caused by complex preprocessor macros that cannot be expanded without a full pre-compiler. Achieving 100% parse fidelity would require building and maintaining a C preprocessor, which is **out of scope** for this project.

As a mitigation, macro-defined variables — which are rarely useful for retrieval — are intentionally skipped and excluded from analysis rather than being guessed at.

Observed on a Linux-kernel-scale checkout: **0 failed**, with partial parses confined almost entirely to C. Python and Rust were effectively clean; C++ sample size was too small to be meaningful.

**Impact:** Partial parses are expected and considered acceptable for C/C++. They do not represent a failure; extracted symbols remain usable for retrieval.

**Status:** By design. No fix planned.

---

## Embedder

### ROCm runtime warning: `(null): No such file or directory`

At application startup on ROCm systems, you may see:

```
(null): No such file or directory
```

This is a **known issue within the ROCm stack**, where the runtime fails to locate the `amdgpu.ids` file and incorrectly reports the error path as `(null)`.

**Impact:** None. This message is cosmetic and does **not** affect functional performance, embedding throughput, or output correctness.

**Status:** Upstream ROCm issue. No workaround required.

### Peak RSS not attributable to the embedding worker

Running `perf stat` (or `time -v`) against the `eulix embed` **CLI launcher** does **not** capture the memory footprint of the actual embedding process. The launcher is a short-lived supervisor (in one measurement: ~0.2 s user time, ~55 MB max RSS, exit 130) that forks/spawns the real Python/torch worker.

**Impact:** Reported "embedding pipeline peak RSS" from such a wrapper is meaningless. To get a real number, attach the profiler to the worker itself (e.g. the Python entrypoint or the spawned child PID), not the launcher.

**Status:** Measurement caveat. Not a product bug.

---

## Deep codebase queries

For queries requiring deep, multi-hop understanding of the codebase — for example:

> _"When `Expression.resolve_expression()` processes an `OuterRef`, which specific `Set` attribute on the inner `Query` object is mutated to track parent table aliases? How does `SQLCompiler.get_from_clause()` use this set to ensure those outer tables are omitted from the subquery's `FROM` SQL block?"_

Eulix can currently provide **good-enough retrieval**, but may **not** produce a completely accurate final answer.

### Why this happens

This is not necessarily a failure of retrieval or of the LLM. The primary limitation lies in **resource allocation during the source hydration phase** (`source.go`). Deeply coupled queries often require following a chain of related functions, classes, and files. That chain can:

- Exceed the available source-token budget, or
- Cause important intermediate code to receive insufficient context.

### Mitigation

You can try increasing the maximum token limit in `eulix.toml`. Note that this **does not guarantee** an improvement in retrieval quality or in the accuracy of the final LLM-generated answer — it only relaxes the budget constraint.

**Status:** Under active work. We are improving source hydration and context allocation for deeply coupled queries.

---

## Summary

| Area     | Issue                                                                          | Severity | Status                                                                                     |
| -------- | ------------------------------------------------------------------------------ | -------- | ------------------------------------------------------------------------------------------ |
| Parser   | Approximate call graphs (PRISM); v1 vs v2 edge-count / speed / memory tradeoff | Medium   | Known limitation; **v2 recommended** (more precise, faster, lower memory on large corpora) |
| Parser   | Missing languages (Ruby, PHP, C#, …)                                           | Medium   | Unsupported; planned                                                                       |
| Parser   | C/C++ partial parse due to macros                                              | Low      | By design; out of scope                                                                    |
| Embedder | ROCm `(null)` warning                                                          | Cosmetic | Upstream ROCm issue                                                                        |
| Embedder | `perf`/`time` on CLI launcher misses worker RSS                                | Low      | Measurement caveat; attach to worker                                                       |
| Queries  | Deep multi-hop queries may be inaccurate                                       | Medium   | Under active work                                                                          |
