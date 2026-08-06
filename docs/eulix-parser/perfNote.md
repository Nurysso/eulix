Building Eulix's retrieval pipeline on large codebases. The hard part wasn't any single piece it was optimizing them all together without breaking.
Early versions OOM'd on anycodebase over 2M loc because I was creating copies of ast in memeory while creating call graphs. I rewrote the call grpahs to minimize ram consumptions and turned entire stack around true streaming: ijson-based JSON parsing instead of load-and-decode, incremental binary writes with seek-back patching for counts, and mmap-based reads at query time instead of loading the index. That brought retrieval-time RAM from ~6x source file size down to 1.5x.
But lower RAM meant the query engine had to be smarter. I studied HNSW and borrowed its core insight partition the space so you only score the most promising regions instead of scanning everything then built a simpler IVF index via k-means centroids. Simultaneously, I added subsystem detection and path gating to handle multi-language monorepos, which took retrieval accuracy from ~70% to 95%+.
The part I'm most proud of: none of these decisions made sense in isolation. I had to understand why HNSW works, why streaming JSON matters, why call graph traversal and AST lookups matter alongside vector search, then make tradeoffs knowing exactly what I was losing. The final system is sub-300ms retrieval on large codebases at 1.5x RAM not because I copied an existing paper, but because I understood the problem deeply enough to build something simpler and faster for this specific use case.

## Performance Notes (analyze.rs / main.rs)

### OS I/O hints (FADV_SEQUENTIAL, readahead, FADV_DONTNEED) — DO NOT ADD

Attempted in v0.7.2, reverted in v0.7.3.

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
