# Scripts

Audited 2026-11-04 on branch `pre-release-fixes` (`ad744e9`). All scripts were renamed to snake_case on 2026-11-04. The "Former name" column is the old name, for finding it in history.

## Running from any directory

Every sub-directory Makefile (`eulix-cli`, `eulix-embed`, `eulix-parser`) forwards unknown targets to the root `Makefile`, so these work from the root or any of them. Scripts run in the directory you typed `make` in, so relative paths in `ARGS` resolve there.

```
make scripts-help
make repro-prism                       # builds the parser, then runs known_issues/repro_prism.sh
make repro-all                         # every parser repro
make kb-diff ARGS="run1/kb.json run2/kb.json"
make script SCRIPT=known_issues/cross_language_edges.py ARGS=kb_call_graph.json
make repro-prism PARSER_BIN=/other/eulix_parser
```

## Defaults and output

Scripts need no arguments when run from the repo: input files default to `.eulix/kb.json`, `.eulix/kb_call_graph.json` and `.eulix/embeddings.bin`, the parser defaults to `eulix-parser/target/release/eulix_parser`, and `check_embed_*.py` use the repo's `eulix-embed/` (override with `EULIX_EMBED_DIR`). Anything a script writes goes to `script-test-dump/` at the repo root, which is in `.gitignore` and `.euignore`. `kb_diff.py` has no default because it needs two files. When you pass a path through `make`, use `ARGS=`, for example `make script SCRIPT=call_graph_id_patterns.py ARGS=other.json`.

## Bug-repro scripts (run these before a release)

These find and confirm bugs. Each one prints a result that shows whether the bug is present, so a fixed bug that comes back is caught by re-running it. Build the parser first: `cd eulix-parser && cargo build --release`.

| Bug | Former name | Name | What "bug present" looks like |
| --- | --- | --- | --- |
| PRISM-2, 3, 4, 5, 6 | `known-issues/repro-prism.sh` | `known_issues/repro_prism.sh` | Edges missing or wrong in the printed v1 / v2 tables (compare with `Known-Issues/Parser-And-PRISM.md`) |
| PRISM-2 | `known-issues/method-edge-coverage.py` | `known_issues/method_edge_coverage.py` | `0/N methods have a caller` for a non-PHP extension |
| PRISM-4 | `known-issues/cross-language-edges.py` | `known_issues/cross_language_edges.py` | Cross-language count above 0 on a project with no FFI calls |
| PRISM-5, 6, 7 | `known-issues/probe/` (Rust crate) | `known_issues/probe/` | Dumps what the parser records per call and class |
| PARSE-2 | `known-issues/check-tsx.sh` | `known_issues/check_tsx.sh` | `Unsupported language` for `.tsx` / `.jsx` |
| PRISM-8 | `known-issues/perf-rust-workspace.sh` (wraps `check-rust-workspace-perf.sh`, now `check_rust_workspace_perf.sh`) | `known_issues/perf_rust_workspace.sh` | 80x160 workspace takes tens of seconds (42 s measured; dev branch 0.20 s) and yields 0 edges |
| #152 review findings | `check-prism-v2-methods.sh` | `check_prism_v2_methods.sh` | `start -> util::run edge: MISSING` or `duplicates: YES` |
| Retrieval quality (EHNAS) | `eval_mcp.py` | `eval_mcp.py` (keep) | Expected file prefixes missing from MCP results; compare against `script-test-dump/eval_<engine>_baseline.json` |
| Embedding padding / ROCm drift | `padtest.py`, `rocmtest.py` | `check_embed_padding.py`, `check_embed_rocm.py` | Cosine between free and padded (or CPU and ROCm) vectors well below 1.0 |

## Diagnostic scripts (inspect output, do not assert a bug)

| Former name | Name | Use |
| --- | --- | --- |
| `kb_diff.py` | `kb_diff.py` (keep) | Compare two parser runs (`kb.json`, `kb_index.json`); exit 1 if different. Use it to confirm output stays deterministic. |
| `kbloader.py` | `kb_id_name_check.py` | Checks every function/method ID in `kb.json` against its `name` and file; prints mismatches and exits 1 if any. \|
| `ID_Pattern.py` | `call_graph_id_patterns.py` | Prints node ID prefix and pattern statistics from a call graph JSON. |
| `vecstats.py` | `vec_stats.py` | Pairwise cosine and int8 usage stats for a quantized vector file. |
| `kb_usage.py` + `astdump.go` | `kb_field_usage.py` + `ast_dump.go` | Reports which `kb.json` struct fields the Go CLI reads. |
| `aligo-walk.sh` | `alignment_go_walk.sh` | Pages through files with the external `aligo` tool. Not related to Eulix bugs. |

## Problems found in the scripts

1. **`retrival_eval.py` is a stale duplicate of `eval_mcp.py`.** Same code, its docstring still says `eval_mcp.py`, and the name is misspelled. Only difference: it lacks `eulix-embed/data_io/serialization.py` in one expected list. Delete it (not done; you asked only for renames).
2. ~~`check_prism_v2_methods.sh` wrote its test project into `scripts/`~~ Fixed: it writes to `script-test-dump/`.
3. ~~`check_rust_workspace_perf.sh` did the same~~ Fixed. `known_issues/perf_rust_workspace.sh` is now just a wrapper with smaller defaults and could be deleted.
4. **Duplicated PRISM scripts:** `check_prism_v2_methods.sh` and `known_issues/` overlap. Move it into `known_issues/` and list it in that README.
5. ~~`kb_usage.py` pointed at a moved `kb_struct.rs`~~ Fixed in the rename: `kb_field_usage.py` now uses `libeulix/src/struc/kb_struct.rs`, and its `--astdump` flag is now `--ast_dump`.
6. **`eval_mcp.py` needs `build/eulix_linux_amd`**, which is not built here, so I could not run it. The expected file paths it lists all still exist.
7. **`check_embed_padding.py` and `check_embed_rocm.py` hardcode `~/.Eulix/eulix_embed`** and call private methods (`_embed_batch`, `_target_output_name`). Renaming those methods would break the scripts silently. I did not run them (needs models, and ROCm for the second). Take the path from an env var.
8. **`kb_id_name_check.py` and `vec_stats.py` have no docstring or usage**, and `kb_id_name_check.py` only works from the repo root.
9. ~~Mixed dash and underscore names~~ Done.
10. `kb_field_usage.py` and `ast_dump.go` carry an "AI SLOP VIBE CODE" banner. Keep or drop is your call; if kept, say who verified the output.
11. Two docs reference old names: `docs/Known-Issues/Retrieval-And-Query.md` (`scripts/eval_mcp.py`) and `docs/Known-Issues/Parser-And-PRISM.md` (`scripts/check-rust-workspace-perf.sh`). Both are updated.

## Not verified

I read every script but did not execute any: no parser binary or models were built in this session. The measurements quoted above come from the script headers and the existing known-issues docs.
