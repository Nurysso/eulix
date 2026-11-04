# Analysis Passes

This page explains the `analyze` module of `libeulix`: the passes that turn parsed files into entry points, dependencies, patterns, indices, metrics and summaries.

> **Crate / path:** `libeulix/src/analyze/`  
> **Language:** Rust  
> **Status:** stable, but several passes are deliberately simple heuristics  
> **Last verified against:** `923a577`

## Overview

Parsing gives you a pile of per-file facts. The `analyze` module is where those facts become useful at project level. It contains the call-graph engines ([PRISM v1](PRISM-V1.md) and [PRISM v2](PRISM-V2.md), documented on their own pages) and six smaller passes that this page covers: entry points, external dependencies, patterns, indices, metrics and summary.

Each pass is a free function that reads a `KnowledgeBase` and returns a value. None of them depends on another pass's output, apart from the summary, which reads entry points and dependencies that earlier passes have already stored. So you can run only the ones you need.

## Why It Exists

A retrieval or navigation layer wants answers like "where does this program start?", "which third-party packages does it use?" and "which functions are riskiest to change?". Computing these once, during analysis, means that the CLI can answer them later with a lookup. The passes are intentionally cheap, because they run over every file of a repository every time it is analyzed.

## How It Works

The passes run after parsing and, for the call graph, after PRISM. Most of them walk `kb.structure` once and are parallelized or trivially fast.

### Main Components

| Pass | Function | Output | What it looks at |
| --- | --- | --- | --- |
| Entry points | `entry_points::find_entry_points(&kb)` | `Vec<EntryPoint>` | Function names and decorators |
| External dependencies | `dependencies::analyze_external_deps(&kb)` | `Vec<ExternalDependency>` | Import statements |
| Patterns | `patterns::detect_patterns(&kb)` | `PatternInfo` | Function names and file paths |
| Indices | `indices::generate_indices_view(&kb)` | `IndicesView` | Functions, classes, calls, tags |
| Metrics | `metrics::generate_metrics(&kb, k)` | `MetricsReport` | Complexity and importance of each function |
| Summary | `summary::generate_summary(&kb)` | `ProjectSummary` | Everything above, condensed |
| Pipeline | `pipeline::analyze_and_build(kb, verbose, prism)` | `KnowledgeBase` | Runs the passes above in order. Deprecated since 0.9.0, removed in 1.0.0. |

### Data In, Data Out

All passes take `&KnowledgeBase`. The indices and metrics passes return *views* that borrow strings from the knowledge base instead of copying them, which is why `IndicesView<'_>` and `MetricsReport<'_>` carry a lifetime. They are meant to be serialized right away, not stored.

## Key Algorithms

None of these passes is a sophisticated algorithm, and that is on purpose. They are heuristics tuned for speed. Knowing exactly what each one does tells you when to trust it.

### Entry points

**The problem.** To explain a program you first need to know where it starts and what it exposes.

**The approach.** A function named `main`, `run` or `start` is reported as a `main` entry point. A decorator whose text contains `route`, `get`, `post` or `api` is reported as an `api_endpoint`, with the quoted path pulled out by a regular expression and the HTTP verbs guessed from keywords (`GET` if none is found). A decorator containing `command` or `click` makes a `cli_command`.

**Why this way.** Matching on decorator text, and not on each framework's real syntax, makes it work for Flask, FastAPI and similar frameworks without any per-framework code. The price is false positives: a decorator that merely contains the letters `get` will match, and functions in languages without decorators (such as Go or Rust) are only found if they are named `main`, `run` or `start`. Parsers can also tag a function `entry-point`, and PRISM uses that tag to mark graph nodes, but this pass does not read it.

### External dependencies

**The problem.** You want to know which packages a project leans on, and how widely.

**The approach.** Every import is collected in parallel, and imports that point at project code are dropped. That covers types such as `internal`, `cgo`, `include`, `include_dynamic`, `reexport_all`, `reexport_named` and `rust_module`. What remains is grouped by module name, and each group records the set of files that import it. `import_count` is the number of *distinct files*, not the number of import statements. The `source` field is copied from the import's type, so it reads `stdlib` or `external`.

**Why this way.** Counting distinct files is the more useful number, because a package imported twice in one file is still used in one place.

### Patterns

**The problem.** A summary of a project should say how it is organized.

**The approach.** The naming convention is a majority vote: function names containing `_` count as snake case, names with an uppercase letter and no underscore count as camel case, and the larger group wins. The structure type is `Standard (src/ + tests/)` when there is a `src/` directory and a path containing `test`, `Library` when there is a `lib/` directory, and `Flat` otherwise. The architecture style is `layered` when paths suggest API, service and data layers, `mvc` when they suggest model, view and controller, and `microservices` when more than three paths contain `service`.

**Why this way.** Verifying real layering would need the full call graph and cross-language import resolution. Directory names are a fast, best-effort signal. Expect it to be wrong for projects that do not follow common naming.

### Metrics

**The problem.** People want to know which functions are the riskiest to touch.

**The approach.** All functions and methods are ranked by cyclomatic complexity, then by importance score as a tiebreaker, then by name, and the top `k` are kept.

**Why this way.** Complexity is the strongest cheap proxy for risk, and the tiebreakers make the order identical between runs.

### Indices and summary

The indices pass builds five lookup maps: functions by name (as `path:line`), the callers of each callee, functions by tag, types by name, and files by category. Nested functions are left out of the by-name index, because only their parent can call them. The file category comes from `summary::classify_file`, which checks path words in a fixed order (`test`, `auth`/`login`, `api`/`endpoint`/`route`, `util`/`helper`, `model`/`entity`, `ui`/`view`) and finally looks for function names containing `crypt` or `hash`, which marks a file as `Security`. The summary collects up to ten "key features" from the first sentences of function and class docstrings.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `k` | argument to `generate_metrics` | none | How many functions the metrics report keeps. |
| `prism` | argument to `analyze_and_build` | none | `2` runs PRISM v2; any other value runs PRISM v1. |
| `verbose` | argument to `analyze_and_build` | none | Prints progress to stdout when true. |

### What `analyze_and_build` does

Because many callers still use it, it helps to know the exact order. For repositories of 100,000 files or fewer, it resolves calls and builds the call graph with the PRISM version you chose, fills the reverse "called by" lists, and then runs patterns, entry points and dependencies. For larger repositories it skips the call graph and the reverse lists to protect memory, but still runs the other three. It does **not** run the indices, metrics or summary passes; callers invoke those themselves.

## Usage Examples

```rust
use libeulix::analyze::{indices, metrics, summary};

let top = metrics::generate_metrics(&kb, 20);
let view = indices::generate_indices_view(&kb);
let report = summary::generate_summary(&kb);

println!("{} complex functions listed", top.top_complex_functions.len());
println!("{} distinct function names", view.functions_by_name.len());
println!("{} entry points", report.entry_points.len());
```

Expected result: three lines of counts. The summary's `entry_points` are `file:line` strings built from `kb.entry_points`, so run the entry-point pass first.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Entry-point detection over-matches on decorators. | It does substring matching on decorator text. | Filter the results by `entry_type`, or check the decorator yourself. |
| Entry points are mostly a Python-style notion. | Other languages rarely use decorators. | Rely on the `entry-point` tag where a parser sets it. |
| Pattern detection is path-based. | Real layering needs the call graph. | Treat `architecture_style` as a hint. |
| Dependency `version` is always `None`. | Versions live in manifests such as `Cargo.toml`, which this pass does not read. | Read manifests separately. |
| Naming convention has two outcomes only. | The code returns `snake_case` only when snake case strictly wins. A tie, or a project with no functions, reports `camelCase`. There is no "mixed" answer. | Check the counts yourself if it matters. |

## Related Docs

- [PRISM v1](PRISM-V1.md) and [PRISM v2](PRISM-V2.md): the call-graph engines.
- [Knowledge Base Schema](Knowledge-Base-Schema.md): the types these passes read and write.
- [Public API](Public-API.md): how to call them.
