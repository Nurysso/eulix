# Open issues — repro re-verification

I re-ran the repro scripts in `scripts/` and `scripts/known_issues/` and recorded
what each one shows today. The entries in [Known Issues](Overview.md) were last
verified at `ad744e9` (2026-11-03); this page is a fresh check at a later commit.
Each row links to the full entry; this page only records "does it still happen".

> **Verified at:** `497f8ed` (branch `better-mcp`, 2026-11-24), Linux, release parser
> **How:** the scripts named below, against this repo's `.eulix/` and a fixture each builds itself

## Still reproduces (open)

| ID | Issue | Script | What I saw |
| --- | --- | --- | --- |
| [PARSE-2](Parser-And-PRISM.md#parse-2-jsx-and-tsx-files-are-rejected) | `.jsx` / `.tsx` rejected | `known_issues/check_tsx.sh` | `PARSE ERROR: Unsupported language: Tsx` and `Jsx`. |
| [PRISM-2](Parser-And-PRISM.md#prism-2-v2-never-links-calls-to-methods-in-go-java-javascript-c-and-c) | v2 misses method edges in JS, C, C++ (Go/Java fixed) | `known_issues/method_edge_coverage.py` | Methods with a caller: `.js` 0/7, `.cpp` 0/4, `.ts` 2/14; Go 395/666 and Java 11/16 now work. |
| [PRISM-4](Parser-And-PRISM.md#prism-4-calls-resolve-across-languages) | calls resolve across languages | `known_issues/cross_language_edges.py` | 133 of 6723 call edges (2.0%) cross languages, none of which can be real; `py -> go` 123 is most of them. This re-does the count the entry says it did not repeat. |
| [EMB-1](Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings) | ONNX on ROCm produces wrong embeddings | `check_embed_rocm.py` | Cosine of ROCm vs CPU: unpadded `min 0.533`, padded-128 `min -0.556 mean -0.466` — matches the entry's table. The current `.eulix/embeddings.bin` has 27.8% negative pairwise cosines (`vec_stats.py`), i.e. it was built on ROCm and is broken (`eulix.toml` now sets `engine = "onnx"`). |

## No longer reproduces (fixed)

| Was | Script | What I saw |
| --- | --- | --- |
| #152 issue 1: a call into a sibling workspace crate loses its edge | `check_prism_v2_methods.sh` | `start -> util::run edge: present`. |
| #152 issue 2: same-line calls on different receivers list the caller twice | `check_prism_v2_methods.sh` | `functions_calling['get']` has no duplicates. |

## Not re-checked here

- **PRISM-8** (v2 slow on large Rust workspaces): `perf_rust_workspace.sh` runs, but I only ran a small 20×40 case (0.20 s); I did not reproduce the slowdown at scale.
- **PRISM-5, 6, 7** (receiver recording, JS base class, C++ double edges): need `known_issues/probe/` (a cargo run); I did not run it. `repro_prism.sh` runs and dumps the fixture's v2/v1 edges if you want to inspect them.
- **RET-\*** retrieval issues: `eval_mcp.py` / `retrival_eval.py` need a running MCP server and embeddings and are slow; not run.

## Scripts themselves

All scripts in `scripts/` and `scripts/known_issues/` run (the `kb_*.json` readers
were already migrated to the `kb_*.fb` FlatBuffers on this branch), and
`make scripts-all` passes. Two stale bits, both fixed:
- `known_issues/README.md` usage column still showed `kb_call_graph.json`; now `.fb`.
- `kb_id_name_check.py` flagged 4 valid IDs in `eulix-lsp/e2e_lsp.py` as inconsistent.
  The parser appends an `@<line>` suffix to disambiguate duplicate nested function
  names (two sets of `request`/`workspace_names`/… in one scope); the check now
  strips that suffix before matching the id against the name.
