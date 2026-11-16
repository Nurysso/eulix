# Call-Graph Benchmark Scoring

This page explains, step by step, how the call-graph accuracy harness in `bench/callgraph/` turns two lists of calls into precision, recall and the other numbers in its report. It covers what the reference tools write, how eulix's graph is loaded, how the two are matched, how each call is judged, and how every number in `results.json` is computed.

> **Path:** `bench/callgraph/cg_eval.py` (entry), `bench/callgraph/lib/score.py` (all of the scoring), `lib/aggregate.py` (repeated runs and per-language roll-ups), `lib/eulix.py`, `lib/refs.py`, `lib/corpus.py`, `lib/report.py`; reference tools in `bench/callgraph/refs/`; unit tests in `bench/callgraph/tests/test_score.py`
> **Language:** Python 3.11+ (standard library only); the reference tools are in Go, Java, Python and JavaScript
> **Last verified against:** `c294935` plus the uncommitted branch `prism-go-java-receivers` (2026-11-16)

For how to run the harness, see its [README](../../bench/callgraph/README.md). For the latest results, see [Call-Graph Accuracy](../libeulix/Call-Graph-Accuracy.md).

## Overview

The harness answers one question: when eulix says "function A calls function B", is that true? It cannot answer this by reading eulix's graph alone, so for each language it asks a tool that really resolves calls (the compiler or type checker) to list every call in a pinned project and where it lands. That list is the **reference**. Scoring compares eulix's edges against it.

```
 corpora.toml ──► snapshot of the project (pinned commit, optional subdir)
                      │
          ┌───────────┴────────────┐
          ▼                        ▼
  reference tool             eulix_parser --prism N
  (go/types, javac, …)       (kb.json, kb_call_graph.json)
          │                        │
  call sites (JSON lines)    nodes (spans) + call edges
          └───────────┬────────────┘
                      ▼
             score.py: match, judge
                      │
      ┌───────────────┼──────────────────┐
      ▼               ▼                  ▼
 per call site   caller → callee pairs   node and edge statistics
 (secondary)     (the headline numbers)
                      │
                      ▼
      results.json, report.md, <corpus>-v<N>-sites.jsonl
```

The work happens in seven steps, one section each below:

1. Fetch a snapshot of each corpus.
2. Run the reference tool and get its list of call sites.
3. Run `eulix_parser` and load its nodes and call edges.
4. Match the two: find each call's caller, translate each reference target into eulix node ids, and attach each eulix edge to the reference call it describes.
5. Judge every reference call site (TP, FN, wrong target, …).
6. Judge every caller → callee pair. This gives the headline numbers.
7. Compute the metrics.

## Step 1: Corpora And Snapshots

`corpora.toml` lists one real project per language, pinned to a full 40-character commit, plus a tiny hand-written "micro" fixture per language under `bench/callgraph/micro/`.

`lib/corpus.py` makes a snapshot under `~/.Eulix/bench/callgraph/corpora/<name>-<commit[:12]>/`: it fetches exactly that commit with `git fetch --depth 1`, checks it out, deletes `.git`, and removes the paths listed in `exclude` (for cJSON, its bundled test framework and fuzzing harness). A `.cg-ready` marker means the snapshot is complete and is reused next time. If the corpus has a `subdir`, both eulix and the reference analyse only that directory (for example `gson/src/main/java`).

**Why this way.** Both sides must see exactly the same files. A pinned commit makes a result reproducible, and the snapshot is shared by both PRISM versions, so v1 and v2 are scored on the same input.

## Step 2: The Reference Call Sites

### What a reference tool writes

Each tool in `refs/` reads the snapshot and writes one JSON line per call site:

| Field | Meaning |
| --- | --- |
| `file`, `line`, `col` | Where the call's callee name is (the `Foo` in `x.Foo(1)`) |
| `lines` | Other lines eulix may report for the same call, such as the line where a multi-line expression starts |
| `name` | The called name, as written |
| `kind` | `function`, `method` or `constructor` |
| `status` | `project`, `external` or `unknown` (see below) |
| `dispatch` | `static`, or `virtual` for a call through an interface or an overridable method |
| `targets` | For a `project` call: every declaration it may land on, as `{file, line, name}` |

`status` decides how the call is used:

| Status | Meaning | Example |
| --- | --- | --- |
| `project` | Resolves to one or more declarations inside the snapshot | `engine.Use(m)` where `Use` is a gin method |
| `external` | Resolves to the standard library, a dependency or a builtin | `fmt.Println`, `len`, `errors.New` |
| `unknown` | The tool could not resolve it, or it may or may not run project code | a call through a function value; `err.Error()` when project types implement `error` |

### How each tool decides

| Language | Tool | `project` targets | `unknown` |
| --- | --- | --- | --- |
| Go | `refs/go/main.go`, go/types through `golang.org/x/tools/go/packages` | The `*types.Func` a call names. A call on a project interface lists the interface method itself plus every project type that implements it. Conversions to a project type are skipped. | Function values; a project interface with no implementer; a method of an outside interface (`error`, `io.Writer`) that project types implement |
| Java | `refs/java/RefCalls.java`, javac attribution (`JavacTask`) with no classpath | The method javac attributes, plus every project override unless it is static, private, final or a constructor | Calls into missing dependencies (javac keeps attributing after errors) |
| Python | `refs/python/refcalls.py`, jedi 0.19.2 `goto` (then `infer`) | The definition jedi reaches under the root. Calling a class is a `constructor` aimed at the class and its `__init__`. | Nothing found, or a variable that holds a callable |
| Rust | `refs/rust/refcalls.py`, `rust-analyzer lsif` | The `fn` a name token resolves to, when the token is followed by `(` or `::<…>(` | No definition. A call through a trait bound resolves to the trait's declaration only. |
| TypeScript, JavaScript | `refs/ts/refcalls.mjs`, the TypeScript 5.9.3 checker (`allowJs` for JS) | The resolved signature's declaration, plus every project method of that name in subclasses and implementers | Callback parameters and other values with no function body |
| C, C++ | `refs/clang/refcalls.py`, libclang | The declaration clang resolved, plus every project override of a virtual method | Calls through function pointers; implicit constructor calls are skipped |

Listing every override or implementer is **class hierarchy analysis** (CHA). The harness treats any one of them as a correct answer, because PRISM picks one target per call.

### Caching

Reference output is cached under `~/.Eulix/bench/callgraph/refs/` as `<corpus>-<commit>-<lang>-<toolhash>.jsonl`. The tool hash is a SHA-256 of the tool's own source files, so editing a reference tool invalidates its cache automatically. Micro fixtures always re-run. `--refresh-ref` forces a re-run. The tool runs on a throwaway copy of the snapshot, because some tools write files (`go.sum`, `Cargo.lock`).

## Step 3: The Eulix Side

`lib/eulix.py` runs

```sh
eulix_parser --root <snapshot> --output <run>/kb.json --prism <1|2> --threads <N>
```

into `~/.Eulix/bench/callgraph/runs/<corpus>/v<N>/`, which is wiped first, and then loads two things.

**Nodes, from `kb.json`.** Every object in `structure` with an `id` and a `line_start` becomes a node with a span (`line_start..line_end`). Its kind comes from the id prefix:

| Id prefix | Node kind |
| --- | --- |
| `func_` | function |
| `method_`, `ctor_`, `ifacemethod_` | method |
| anything else (`class_`, `struct_`, `interface_`, …) | class |

If two declarations get the same id (overloads in one file), eulix has one node for them. The harness keeps one node and records the extra spans, so a call to either overload can still match that node.

**Edges, from `kb_call_graph.json`.** Each edge of type `call` becomes `(from id, to id, line)`. Exact duplicates of the triple are dropped and counted as `duplicate_edges`. Inheritance edges are ignored.

**Language filter.** Edges whose caller is in a file of another language than the corpus, or is not a loaded node, are dropped and counted as `other_language_edges`. JavaScript and TypeScript count as one group, and so do C and C++. Kept edges whose target is in another language group are counted as `cross_language_edges`; they stay in and are judged like any other edge.

## Step 4: Matching The Two Sides

The reference talks about files and lines; eulix talks about node ids. Matching translates between them in three steps.

### 4a. Who is the caller?

For each reference site, the caller is the **innermost** eulix function or method whose span contains the site's line in the same file (`SpanIndex.enclosing`). If no node contains it, the site is **out of scope**: top-level code, a class body, or a function eulix did not extract (for example a test function generated by a Rust macro, or a method of a Java anonymous class).

### 4b. Which eulix ids are acceptable targets?

For a `project` site, each reference target `{file, line, name}` is mapped to eulix ids (`map_target`), and the union is the site's **expected** set:

1. Take the function and method nodes whose span contains the target's line, innermost first.
2. Prefer the innermost one whose short name equals the target's name. The short name is the last segment after `::` or `.`, so C++ `XMLNode::Name` matches `Name`.
3. Otherwise accept the innermost node only if it **starts on the target's line** and the target has a name. This covers a declaration whose name differs from its node's, such as a constructor. An unnamed target, such as an arrow function returned by a factory, is never mapped to the function around it, which only shares its line.
4. For a `constructor` site, also accept the class node around the target, and any method of that class named `constructor`, `__init__`, `__new__`, `new` or the class's own name.

If a `project` site ends up with an empty expected set, it is **unmapped**: the reference knows where the call goes, but eulix has no node for that place.

### 4c. Which reference call does each eulix edge describe?

eulix records one line per edge. For each edge `(from, to, line)` the harness looks for a reference site in the same caller and picks one (`pick`):

1. **Same caller, same line** (the site's own line or any of its `lines`). Among those, prefer in order: a site that expects `to` and has no edge yet; a site that expects `to`; a site whose name matches `to`'s name, preferring one that has no edge yet.
2. **Nearby line.** If nothing matched, look at sites in the same caller with the **same name** as `to`, from 2 lines above the edge's line to 15 lines below it, nearest first, with the same preferences. This is for multi-line call chains, where eulix may report the line the expression starts on and the reference the line of the callee name. Each such match is counted as `fuzzy_line_matches`.
3. **Nothing.** The edge is **unverifiable**: there is no reference call by that name there. This happens with calls inside macro bodies, files the tool skipped, or a line the tool reports differently.

The matched site records the edge's target in its `hit` list. A site can collect several targets.

**Why match by line and name, and not only by caller and callee?** Because a wrong edge has to be pinned to some call to say *what kind* of wrong it is: the wrong project function, or a library call linked to a project function. Line and name are what both sides share.

## Step 5: Judging Each Call Site

Every reference site goes into exactly one bucket:

| Bucket | Reference says | eulix | Counts as |
| --- | --- | --- | --- |
| `TP` | project call, expected ids E | at least one hit is in E | TP (plus one FP for each extra hit outside E) |
| `wrong_target` | project call | has hits, none in E | one FN, plus one FP per hit |
| `FN` | project call | no hit | FN |
| `FP_external` | external call | has hits | one FP per hit |
| `TN` | external call | no hit | TN |
| `unknown` | could not resolve | – | not scored |
| `out_of_scope` | (no eulix caller) | – | not scored |
| `unmapped_callee` | project call, no eulix node | – | not scored |

If an eulix edge lands on a site that is not scored (`unknown`, `out_of_scope`, `unmapped_callee`), that edge is also recorded as unverifiable.

These per-site counts are reported under `per_call_site`, overall and by `kind` and `dispatch`. They are **not** the headline numbers. The next section explains why.

## Step 6: Caller → Callee Pairs (The Headline Numbers)

### Why pairs and not sites

A call graph states "A calls B". It does not say how many times. PRISM v2 keeps one edge per pair, at the line of the first call. If `Handler` calls `c.JSON` three times, v2 has one edge, and per-site scoring would count one TP and two FNs, even though the graph states the fact correctly. Pair scoring counts it as one correct fact. On gin with PRISM v2, per-site recall is 45.9% while pair recall is 66.3%: 686 of the 1,889 per-site misses are repeated calls to a callee that the graph already links from the same caller.

### Grouping the reference

- **Project groups**: every scored project site (bucket `TP`, `FN` or `wrong_target`) is grouped by `(caller, expected set)`. Three calls to `c.JSON` in one function form one group.
- **External groups**: every external site (bucket `TN` or `FP_external`) is grouped by `(caller, called name)`. Two `fmt.Println` calls in one function form one group.

### Judging each eulix pair

Edges are collapsed to distinct `(from, to)` pairs, each keeping the sites it matched in step 4c. Each pair is judged once:

1. **Correct.** If `to` is in the expected set of **any** project group of `from`, the pair satisfies every such group, whatever line it was recorded at. The line v2 keeps may belong to another call of the same name, such as another overload, or `errors.New` before `gin.New` in the same function. Such pairs are counted as `pairs_correct_for_another_call_of_the_caller`.
2. **Otherwise, judged by the call its line points at.** If one of its matched sites is a project call, the pair is a **wrong target** (one FP). If not, but one is an external call, it is a **wrongly linked external** call (one FP), and that external group is marked as not being a TN.
3. **Otherwise unverifiable.** It matched no scored site. It counts in strict precision only.

### Counting

| Count | Is |
| --- | --- |
| TP | project groups that some eulix pair satisfies |
| FN | project groups that no eulix pair satisfies |
| FP | eulix pairs judged wrong in rule 2 (each counts once, even if its line lands on two groups) |
| TN | external groups that no eulix pair was wrongly linked to |
| unverifiable | eulix pairs left in rule 3 |
| TP_edges | eulix pairs judged correct in rule 1 (each counts once) |
| unseen | project calls the front-end cannot see, grouped like the scored ones (see below) |

**Each side is judged in its own unit.** Recall is about the reference: TP and FN count reference groups. Precision is about eulix: TP_edges and FP count eulix pairs. The two correct counts differ only when one eulix pair satisfies two groups (two calls in one function whose acceptable targets overlap): that pair adds two to TP and one to TP_edges. On the 2026-11-16 run this changed precision by less than 0.05 points on every project.

**Unseen calls.** A `project` site in bucket `out_of_scope` or `unmapped_callee` is a call into the project that eulix cannot see: it lies outside every eulix function, or its callee has no node. These sites are grouped like scored ones, by `(caller, reference targets)`; a site with no caller uses `<outside>:<file>` as its caller. Each group counts once in `unseen`. External and unknown sites are never unseen.

### Breakdowns

`by_kind` and `by_dispatch` split the pair counts: a group's TP, FN or TN goes to the kind and dispatch of its first site; an FP goes to the kind and dispatch of the site its line was judged by. The kind is the reference tool's: Java's tool reports a call to a `static` method as `function`, so in Java `function` means static calls.

## Step 7: The Metrics

`metrics()` computes, for pairs and for sites alike:

| Metric | Formula | What it answers |
| --- | --- | --- |
| Precision | TP_edges / (TP_edges + FP) | Of the edges eulix drew that could be checked, how many are right? |
| Recall | TP / (TP + FN) | Of the project calls eulix can see, how many does it link? This measures the resolver. |
| Recall all | TP / (TP + FN + unseen) | Of all project calls the compiler sees, how many does eulix link? This also charges eulix for code its front-end does not extract, so it measures the whole system. |
| F1 | 2 · P · R / (P + R) | One number that balances the two |
| Accuracy | (TP + TN) / (TP + FP + FN + TN) | Right decisions, counting "left an external call alone" as right |
| Specificity | TN / (TN + FP) | How well eulix leaves external calls alone. Wrong targets are FPs too, so this is not a pure "external calls" rate. |
| Strict precision | TP_edges / (TP_edges + FP + unverifiable) | Precision if every edge the reference cannot confirm is assumed wrong; a lower bound |

A metric whose denominator is zero is `null` in JSON and `–` in the report. F1 is `0` when precision or recall is `0` and both are defined. Per call site there are no pairs: TP_edges equals TP, and `unseen` counts the unseen sites one by one.

**Why two recalls.** Without the unseen calls, a front-end that extracts less code would report a *higher* recall, because the calls it misses vanish from the denominator. Recall all closes that gap. The two differ by a few points on most projects, and a lot where extraction is weak: on 2026-11-16, pugixml 54.8% vs 3.7% (test cases written as macros), markdown-it 88.6% vs 56.4%, axios 82.8% vs 49.1%.

**Static and virtual calls.** `by_dispatch` splits every metric by dispatch, and `report.md` has a "Static and virtual calls" table. A virtual call accepts any override as correct, so static calls alone are the stricter test. On 2026-11-16, virtual calls were at most 4.3% of PRISM's correct pairs on 22 of 24 projects; on gson (22.9%) and jsoup (10.0%), the static-only figures were as high as or higher than the overall ones.

**Why TN means "external call left unlinked".** A call graph has no natural negatives: every pair of functions that is *not* linked is a negative, which would make accuracy close to 100% for any graph. The only negative that matters is a call that could wrongly be linked and was not, which is an external call whose name may collide with a project function.

**Why precision ignores unverifiable edges.** An edge the reference cannot speak about is neither shown right nor shown wrong. Precision leaves it out and strict precision counts it as wrong, so the true value lies between the two.

## Repeated Runs And Per-Language Numbers

### `--runs N`

With `--runs 3`, each graph is built and scored three times, in run directories `v<N>-r1` to `v<N>-r3`. Run 1 keeps the full detail: the examples, the sites file and the per-kind breakdowns. Every run contributes its summary, its parser time and a SHA-256 of its `kb_call_graph.json`. `lib/aggregate.py` (`combine_runs`) then records, under `runs` in the corpus entry:

| Field | Meaning |
| --- | --- |
| `n` | Number of runs |
| `identical_graphs` | Whether all runs produced a byte-identical call graph |
| `summary_mean` | The mean of every summary count and metric over the runs. These are the numbers the summary table shows. |
| `summary_spread` | Max − min of each of them |
| `parser_seconds`, `parser_seconds_mean` | Wall time of each parser run, and its mean |

PRISM is deterministic, so the graphs should be identical and every spread 0. In that case the mean equals any single run, and what the repeats add is the **check** that this holds, plus an averaged parse time. If a spread is not 0, the output changed between runs, which is a bug of its own. The harness prints a warning, and the report's "Repeated runs" table shows which graph changed. The reference tools are not repeated: they are the ground truth, and they are cached per corpus commit.

### Per language

With several real corpora per language, `by_language` rolls them up for each language and PRISM version. This gives two sets of numbers, because they answer different questions:

| Roll-up | How | Answers |
| --- | --- | --- |
| **Mean of repos** | The mean of the per-project precision, recall and F1. A project whose value is undefined (precision when it has no edge at all) is left out of that mean, and `mean_of_repos_n` says how many projects each mean covers. `min_max` gives the range across projects. | "On a typical project in this language, what should I expect?" Each project weighs the same. |
| **Pooled** | The counts of all projects summed, then the metrics computed once | "Over all the calls we measured in this language, how often is eulix right?" A large project weighs more. |

When the two disagree a lot, one project dominates the pooled number, and the per-project rows explain why. Micro fixtures and corpora that failed are never rolled up.

## A Worked Example

A Go function in a project that also declares a function `New`:

```go
func Handler(c *Context) {   // eulix node: method_..._Handler (lines 1-6)
    c.JSON(200, a)           // line 2: project, target Context.JSON
    c.JSON(404, b)           // line 3: project, target Context.JSON
    fmt.Println("hi")        // line 4: external
    errors.New("x")          // line 5: external; the project also has a New
}
```

Say eulix emits two edges: `Handler → Context.JSON` at line 2, and `Handler → New` at line 5.

**Step 4c** matches the first edge to the site at line 2 (same line, expects `Context.JSON`) and the second to the site at line 5 (same line, same name `New`).

**Per site (step 5):**

| Line | Bucket | Why |
| --- | --- | --- |
| 2 | TP | hit `Context.JSON`, which is expected |
| 3 | FN | no edge at line 3 |
| 4 | TN | external, no hit |
| 5 | FP_external | external, but linked to the project `New` |

TP 1, FP 1, FN 1, TN 1, so precision is 50% and recall 50%.

**Pairs (step 6):** one project group, `(Handler, {Context.JSON})`, and two external groups, `(Handler, Println)` and `(Handler, New)`.

- Pair `Handler → Context.JSON` satisfies the project group: **TP 1**.
- Pair `Handler → New` satisfies no project group. Its line points at an external call, so it is a wrongly linked external call: **FP 1**, and the group `(Handler, New)` is not a TN.
- No project group is left unsatisfied: **FN 0**. `(Handler, Println)` is a **TN**.

TP 1, FP 1, FN 0, TN 1, so precision is 50% and recall 100%. The second `c.JSON` call is not held against eulix, because the graph already states that `Handler` calls `Context.JSON`.

## Real Numbers, Side By Side

From the 2026-11-16 run (`~/.Eulix/bench/callgraph/reports/20261116-121133/`):

| Corpus, PRISM | Unit | TP | FP | FN | TN | Unverifiable | Precision | Recall |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| gin, v2 | pairs | 1,603 | 0 | 814 | 3,194 | 0 | 100.0% | 66.3% |
| gin, v2 | sites | 1,603 | 0 | 1,889 | 5,021 | 0 | 100.0% | 45.9% |
| flask, v2 | pairs | 165 | 3 | 58 | 487 | 6 | 98.2% | 74.0% |
| flask, v2 | sites | 165 | 3 | 80 | 595 | 6 | 98.2% | 67.3% |

For flask v2, strict precision is 165 / (165 + 3 + 6) = 94.8%.

gin's buckets for v2 also show the sites that are not scored: 196 `unknown` and 36 `out_of_scope`. These sites are not in either row, because neither side can be judged on them.

## Other Numbers In `results.json`

The top level of `results.json` has `meta` (parser, version, start time, PRISM versions, number of runs), `results` (one entry per corpus and PRISM version) and `by_language` (the per-language roll-up above). Each entry in `results` has `corpus`, `lang`, `prism`, the pinned `commit`/`tag`/`subdir`, the `parser` run (time and command), the `ref` run (tool, time, whether it was cached), and `score`, which holds:

| Key | Contents |
| --- | --- |
| `summary` | Pair metrics (the headline) |
| `by_kind`, `by_dispatch` | Pair metrics per call kind and per dispatch |
| `per_call_site` | The same three blocks, per call site |
| `buckets` | How many sites landed in each bucket of step 5 |
| `reference` | `call_sites`; `by_status` (project / external / unknown); `out_of_scope`; `unmapped_callee`; `files` |
| `eulix` | Statistics about the graph, below |
| `worst_files` | The 10 files with the lowest per-site F1, among files with at least 5 project calls |
| `runs` | Only with `--runs` above 1: see [Repeated Runs](#repeated-runs-and-per-language-numbers) |

The `eulix` block:

| Field | Meaning |
| --- | --- |
| `nodes`, `functions`, `methods`, `classes`, `files` | What eulix extracted |
| `call_edges_raw`, `call_edges_unique`, `duplicate_edges` | Call edges before and after removing exact duplicates |
| `other_language_edges` | Edges dropped because the caller is in another language group |
| `cross_language_edges` | Kept edges whose target is in another language group |
| `colliding_ids` | Nodes that stand for more than one declaration (same id) |
| `fuzzy_line_matches` | Edges matched through a nearby line (step 4c) |
| `pairs_correct_for_another_call_of_the_caller` | Correct pairs whose recorded line points at a different call |
| `extra_targets` | Per-site FPs from a site that has the right target and other targets too |
| `methods_with_caller` | Method nodes with at least one incoming call edge |
| `expected_callees`, `expected_callees_with_any_eulix_caller` | Distinct expected target nodes, and how many of them eulix links from somewhere |

`examples` holds up to `--examples` sample sites for each bucket `FN`, `wrong_target` and `FP_external`, plus unverifiable edges, each with the source line. `report.md` shows them.

## Checking A Number

When a number looks wrong, go to the sites file for that run. It has one line per reference site with its bucket, caller, expected ids and the ids eulix linked:

```sh
R=~/.Eulix/bench/callgraph/reports/20261116-121133

# every missed project call in gin with PRISM v2
jq -c 'select(.bucket=="FN")' $R/gin-v2-sites.jsonl | head

# wrong targets in flask, with what eulix linked instead
jq -c 'select(.bucket=="wrong_target") | {file, line, name, expected, eulix}' $R/flask-v2-sites.jsonl

# bucket counts for one corpus
jq -r .bucket $R/ripgrep-v2-sites.jsonl | sort | uniq -c
```

The graph itself is in `~/.Eulix/bench/callgraph/runs/<corpus>/v<N>/kb_call_graph.json`, and the snapshot that both sides read is in `~/.Eulix/bench/callgraph/corpora/`.

A line-free recount of the pairs, which groups the sites file by `(caller, expected)` and checks for any edge from that caller to one of the expected ids in `kb_call_graph.json`, must give the same TP and FN as `summary`. On the 2026-11-16 run it does, for all 16 runs.

## Limits Of The Scoring

- **Single target counts as right.** Under CHA, linking a virtual call to any one override is a TP, which is more lenient than "the function that actually runs".
- **TN is a lower bound.** No corpus has its dependencies installed, so calls into libraries the tool cannot see are `unknown`, not `external`.
- **The references are static analyses too.** Each has its own blind spots (Rust trait calls, C function pointers, Java anonymous classes). They are listed in the [README](../../bench/callgraph/README.md#limits-of-the-references).
- **Matching relies on lines.** eulix keeps one line per edge, so the harness has to attach the edge to a call by line and name. The nearby-line rule (2 above to 15 below) can in principle attach an edge to the wrong call of the same name. Rule 1 of step 6 makes this harmless for correct pairs; for wrong pairs it can only change which kind of FP is reported.
- **Unseen calls are grouped by file when they have no caller.** A call outside every eulix function has no caller to group by, so all such calls in one file to the same target count as one unseen pair. This is coarser than real callers would be, so recall all is, if anything, slightly too high.

## Tests

`make bench-callgraph-test` runs `tests/test_score.py`, which covers: the buckets; duplicates and extra targets; a multi-line call matched through a nearby line; a constructor call accepting the class; unmapped callees not being scored; two calls of one name on one line; a pair recorded at another call of the same name counting as correct; a wrong pair counting once even when its line lands on two groups; an unnamed target not being mapped to the function on its line; edges from other languages being dropped; qualified C++ names matching; recall all counting unseen calls, and grouping them like scored ones; precision counting each eulix pair once; and the static/virtual split. `tests/test_aggregate.py` covers the run means and spreads, the two per-language roll-ups, leaving undefined values out of the mean, and leaving micro fixtures and failed corpora out of the roll-up.

## Related Docs

- [Harness README](../../bench/callgraph/README.md): commands, corpora, reference tools and their limits.
- [Call-Graph Accuracy](../libeulix/Call-Graph-Accuracy.md): the results, and how the harness itself was audited.
- [PRISM v2](../libeulix/PRISM-V2.md): the algorithm being measured.
- [Version Benchmark](Benchmarking.md): the separate benchmark that compares two eulix versions.
