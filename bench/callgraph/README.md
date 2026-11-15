# bench/callgraph

Scores the call graph `eulix_parser` builds (PRISM v1 and v2) against call sites resolved by each language's own tooling, on one pinned corpus per language. The latest written-up results are in [Call-Graph Accuracy](../../docs/libeulix/Call-Graph-Accuracy.md).

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

### Call sites

Every reference call site first gets a bucket, by the eulix edge whose line matches it (the per-call-site numbers):

| Bucket | Reference says | eulix | Counts as |
| --- | --- | --- | --- |
| TP | calls project function X | links it to X | TP |
| FN | calls project function X | no edge | FN |
| wrong_target | calls project function X | links it only to Y | FP + FN |
| FP_external | calls stdlib / dependency / builtin | links it to a project function | FP |
| TN | calls stdlib / dependency / builtin | no edge | TN |
| unknown | could not resolve (function value, missing dependency), or a call through a library interface or method that project types implement or override | – | not scored |
| out_of_scope | call is not inside any eulix function (top-level code) | – | not scored |
| unmapped_callee | target has no eulix node (including unnamed functions) | – | not scored |

### Caller → callee pairs (the headline numbers)

PRISM v2 keeps one edge per caller → callee pair, at the line of the first call, so the headline numbers count pairs. Project calls are grouped by (caller, acceptable targets), external calls by (caller, name). An eulix pair is:

- **correct** (TP for every group it satisfies) when its target is acceptable for *any* of the caller's project calls, whatever line it was recorded at. The single line v2 keeps may belong to another call of the same name: another overload, or `errors.New` before `gin.New` in the same function;
- otherwise judged once, by the call its line points at: a project call there makes it a **wrong target**, an external call a **wrongly linked external** call (that external group is then not a TN), and anything else (an unknown call, a call the reference does not have) leaves it **unverifiable**.

A project group no eulix pair satisfies is an FN; an external group no eulix pair was wrongly linked to is a TN.

- **precision** TP / (TP + FP); **recall** TP / (TP + FN); **F1**.
- **accuracy** (TP + TN) / all scored. TN means *external calls left unlinked*, which is the only meaningful negative for a call graph; counting every unlinked function pair instead would push accuracy to ~100%.
- **specificity** TN / (TN + FP).
- **strict precision** also counts unverifiable pairs as FPs.

### Matching

An eulix node is matched by containment: the reference target's line must fall in a node's `line_start..line_end`, and the innermost node with the same name wins. A target whose name differs from its node's (a constructor, a C++ `A::f`) may still take the node that starts on its line; an unnamed target (an arrow returned by a factory) never does, since the enclosing function only shares its line. A constructor call also accepts the class node and its `constructor` / `__init__` / same-name method. eulix reports one line per call, and a multi-line chain may be reported at the line where the expression starts, so an edge with no site on its exact line may match a same-name site up to 15 lines below it in the same caller (shown as "matched via a nearby line").

The scoring was cross-checked against an independent scorer that ignores lines entirely; they agree on every TP and FN. See [How The Benchmark Was Checked](../../docs/libeulix/Call-Graph-Accuracy.md#how-the-benchmark-was-checked).

## Limits of the references

- They are static. A virtual call through a project type lists every override in the project (class hierarchy analysis), so an eulix edge to one implementor is a TP, and recall needs only one of them. A call through a *library* interface or method that project types implement or override (`err.Error()`, `list.get()`) may or may not run project code, so it is `unknown`.
- Go loads `./...` plus `testdata/` and `_`/`.`-prefixed package directories, which `./...` skips.
- Python follows jedi's `goto`, but prefers `infer` when `goto` lands in one of jedi's bundled third-party stubs (jedi ships stubs for flask itself).
- TypeScript follows an overload signature to the implementation.
- Rust trait calls resolve to the trait's declaration, not to the impls, so dynamic dispatch through a bound often lands on "unmapped_callee".
- No corpus has its dependencies installed: calls into libraries the tool cannot see are `unknown`, not `external`.
- C/C++ calls through function pointers, and to functions that are only implicitly declared, are `unknown`; macro-expanded calls count at the macro's call site.
