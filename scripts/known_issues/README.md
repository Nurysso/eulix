# Known-issue repro scripts

Scripts used to confirm the issues in `docs/Known-Issues/` (verified 2026-11-03 at `ad744e9`).
Build the parser first: `cd eulix-parser && cargo build --release`.

| Script | Reproduces | Usage |
| --- | --- | --- |
| `repro_prism.sh` | PRISM-2, 3, 4, 5, 6 (edges for a multi-language fixture, PRISM v2 then v1) | `./repro_prism.sh <parser> [workdir]` |
| `cross_language_edges.py` | PRISM-4 (counts cross-language call edges) | `./cross_language_edges.py kb_call_graph.json` |
| `method_edge_coverage.py` | PRISM-2 (methods with a caller, per file extension) | `./method_edge_coverage.py kb_call_graph.json` |
| `probe/` | PRISM-5, 6, 7 (what the parser records per call and class) | `cd probe && cargo run -q -- file...` |
| `check_tsx.sh` | PARSE-2 (`.tsx` / `.jsx` rejected) | `./check_tsx.sh [workdir]` |
| `perf_rust_workspace.sh` | PRISM-8 (v2 slowdown on Rust workspaces) | `./perf_rust_workspace.sh <parser> [crates] [modules]` |
