# bench/callgraph

Scores the call graph `eulix_parser` builds (PRISM v1 and v2) against call sites resolved by each language's own tooling, on one pinned corpus per language.

```sh
make bench-callgraph-micro                      # tiny fixtures, ~1 min, also checks the reference tools work
make bench-callgraph                            # real corpora, PRISM v1 and v2
make bench-callgraph ARGS='--lang go,java --prism 2 --examples 40'
make bench-callgraph-test                       # scoring unit tests
python3 bench/callgraph/cg_eval.py --help
```

Output: a table on stdout, and in `~/.Eulix/bench/callgraph/reports/<timestamp>/`:

| File | What it holds |
| --- | --- |
| `report.md` | Summary, then per corpus and PRISM version: metrics by call kind and dispatch, counts, worst files, example FN / wrong-target / FP / unverifiable calls with the source line |
| `results.json` | Everything in the report, machine-readable |
| `<corpus>-v<N>-sites.jsonl` | Every reference call site with its bucket, caller, expected eulix ids and the ids eulix linked. Grep this when a number looks wrong |

Snapshots, cached reference output (keyed by corpus commit and a hash of the tool's source) and parser runs also live under `~/.Eulix/bench/callgraph` (override with `--home` or `EULIX_CG_HOME`). `--refresh-ref` re-runs a cached reference.

## Corpora and reference tools

| Lang | Corpus (`corpora.toml`) | Reference | Needs |
| --- | --- | --- | --- |
| Go | gin v1.12.0 | `refs/go`: go/types through `golang.org/x/tools/go/packages`; interface calls list every project implementor | Go, network on first run (module download) |
| Java | gson 2.15.0, `gson/src/main/java` | `refs/java/RefCalls.java`: javac attribution (`JavacTask`); virtual calls list project overriders | JDK 17+ |
| Python | flask 3.1.3, `src/` | `refs/python`: jedi 0.19.2 `goto`/`infer` | uv (fetches Python 3.12 and jedi) |
| Rust | ripgrep 15.2.0 | `refs/rust`: `rust-analyzer lsif` | rust-analyzer, `rust-src` |
| TypeScript | class-validator 0.15.1, `src/` | `refs/ts`: TypeScript 5.9.3 checker; overrides and implementors listed | Node, npm (`npm ci` on first run) |
| JavaScript | axios 1.21.1, `lib/` | same, with `allowJs` | same |
| C | cJSON 1.7.19 | `refs/clang`: libclang | system clang + python-clang |
| C++ | tinyxml2 11.0.0 | same, `-x c++`; virtual calls list overriders | same |

PHP has no reference tool yet: there is no PHP toolchain on the dev machines, and a name-based PHP parser would not be better than PRISM itself.

Every tool writes one JSON line per call site: `file`, `line` (of the callee name), `lines` (other lines eulix may report, such as the start of the call expression), `name`, `kind` (`function` / `method` / `constructor`), `status`, `dispatch` (`static` / `virtual`), and `targets` (`[{file, line, name}]`).

## How scoring works

The unit is the **call site**, not the edge. Every site gets one bucket:

| Bucket | Reference says | eulix | Counts as |
| --- | --- | --- | --- |
| TP | calls project function X | links it to X | TP |
| FN | calls project function X | no edge | FN |
| wrong_target | calls project function X | links it only to Y | FP + FN |
| FP_external | calls stdlib / dependency / builtin | links it to a project function | FP |
| TN | calls stdlib / dependency / builtin | no edge | TN |
| unknown | could not resolve (function value, missing dependency) | – | not scored |
| out_of_scope | call is not inside any eulix function (top-level code) | – | not scored |
| unmapped_callee | target has no eulix node | – | not scored |

A correct edge plus extra edges for the same call (ambiguous name lookup) is one TP plus one FP per extra target.

- **precision** TP / (TP + FP); **recall** TP / (TP + FN); **F1**.
- **accuracy** (TP + TN) / all scored sites. TN means *external calls left unlinked*, which is the only meaningful negative for a call graph; counting every unlinked function pair instead would push accuracy to ~100%.
- **specificity** TN / (TN + FP).
- **strict precision** also counts as FP the eulix edges the reference cannot confirm ("unverifiable": no reference call with that name at that line in that caller, or a call the reference left `unknown`).
- **edge level**: the same precision and recall over unique caller → callee pairs.

An eulix node is matched by containment: the reference target's line must fall in a node's `line_start..line_end`, and the innermost node with the same name wins. A constructor call also accepts the class node and its `constructor` / `__init__` / same-name method. eulix reports one line per call, and a multi-line chain may be reported at the line where the expression starts, so an edge with no site on its exact line may match a same-name site up to 15 lines below it in the same caller (shown as "matched via a nearby line").

## Limits of the references

- They are static. A virtual call lists every override in the project (class hierarchy analysis), so an eulix edge to one implementor is a TP, and a site's recall needs only one of them.
- Rust trait calls resolve to the trait's declaration, not to the impls, so dynamic dispatch through a bound often lands on "unmapped_callee".
- Java and TS/JS are compiled without dependencies: calls into missing libraries are `unknown`, not `external`.
- C/C++ calls through function pointers are `unknown`; macro-expanded calls count at the macro's call site.
