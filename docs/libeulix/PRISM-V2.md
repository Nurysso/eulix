# PRISM v2

PRISM v2 is the scope-aware call-graph builder in `libeulix`: it resolves each call by searching from the nearest scope outward, and it declines to guess when a name is too common.

> **Crate / path:** `libeulix/src/analyze/call_graph/prism_v2.rs` (plus `prism_v2/php.rs`, `prism_v2/receivers.rs`, `prism_v2/receivers/rust_paths.rs`)  
> **Language:** Rust  
> **Status:** the intended default; receiver-aware for Python, TypeScript and Rust, with dedicated rules for PHP  
> **Last verified against:** `923a577`

## Overview

v2 keeps the same goal as [PRISM v1](PRISM-V1.md), which is to link every call to the definition it probably means. What changes is how it chooses. Instead of "the first function with that name anywhere", v2 asks "which function with that name is *closest* to where the call is made?" It looks in the same file first, then in what the file imports, then in the same directory, and only then across the whole project.

It also knows about classes. When code inside a method calls `self.validate()`, v2 starts at the method's own class, then walks up through its parent classes, and then down through its subclasses to find a method with that name. And when a name is shared by too many definitions to guess responsibly, v2 draws no edge at all. A missing edge is usually better for retrieval than a wrong one.

The source documents a target of roughly 1.4 seconds and 3.1 GB of peak memory for a ~64,000-file polyglot repository. That number comes from a source comment and was not re-measured for this page.

## Why It Exists

v1's global lookup is wrong whenever two unrelated symbols share a name, and in real repositories that is the normal case (`__init__`, `run`, `handle`, `Get`). v2 exists to fix that by using information the parsers already provide: the file a call is in, the file's imports, the directory structure and the class hierarchy. None of it requires a type checker, so v2 stays fast enough to run on repositories with hundreds of thousands of symbols.

## How It Works

`build_with` runs in five phases. Almost all of the heavy work is parallel, and the single-threaded merge steps exist to keep the result deterministic.

1. **Sort and index the files.** Files are sorted by directory and then by path. This places files from the same directory next to each other, which is what makes the "same directory" lookup a cheap range search. In parallel, each file contributes its nodes (functions, methods, classes), the base classes of its classes, and its resolved imports.
2. **Merge.** The per-chunk results are merged on one thread. If two files produce the same id, the earlier file in sorted order wins, so every run gives the same answer.
3. **Build the indices.** A map from each short name to all of its definitions (the inverted symbol map), a sorted list of class methods for fast lookup, a module index for resolving imports, and, if the project has PHP files, an include index.
4. **Resolve calls.** For each function and method, every call is resolved by the scope ladder described below. Edges are collapsed so that the graph has at most one edge per caller and target within a file, keeping the first call-site line. An edge is marked `conditional` only if *every* call to that target is conditional.
5. **Write the output.** Nodes and edges are materialised, and each node's `call_count_estimate` is the number of resolved `call` edges pointing at it. Inheritance edges are deliberately left out of that count, because for "hot spot" questions the number of callers is what matters.

After the graph is built, `populate_called_by_from_graph` fills each function's `called_by` list from the finished graph. It does not re-run resolution, so it can also run on a graph that was saved and reloaded. Unlike v1, v2 never rewrites `kb.structure`, so `calls[].callee` keeps the names the parser wrote.

### Main Components

| Item | Responsibility |
| --- | --- |
| `build_call_graph_v2(&structure)` | Entry point with default options. |
| `build_with(&structure, &CallGraphOptions)` | Entry point with your own options. |
| `populate_called_by_from_graph(&mut kb)` | Fills `called_by` from the finished graph. |
| `Resolver::lookup` | The scope ladder. |
| `Resolver::pick` | Chooses among candidates by kind, distance and a stable tiebreak. |
| `Resolver::resolve_call` | Interprets the shape of a call (`foo()`, `self.foo()`, `pkg.foo()`, `Cls.foo()`). |
| `receivers` module | The receiver-aware path for Python, TypeScript and Rust. |
| `php` module | PHP's own rules: namespaces, includes, traits, magic methods. |

### Data In, Data Out

The input is `&BTreeMap<String, FileData>`, the same as v1. The output is a `CallGraph`. Unlike v1, you do not run a pre-pass first.

## Key Algorithms

### PRISM v2 in Plain Words

Imagine you hear someone say "call Sam". You would not phone every Sam in the world. You would think of the Sam in the room first, then the Sam from your team, then the Sam from your building, and only after that consider any Sam in the company. If there were fifty Sams in the company and nothing else to go on, you would say "which Sam?" and not pick one.

v2 does exactly that for a function name. It checks the **same file**, then the files this file **imports**, then the **same directory**, and finally the **whole project**. The first place that has a match wins. In the whole-project step, if more than a handful of functions share the name, v2 gives up and draws no edge. For method calls it also looks through the class and its parents, because `self.save()` almost always means a method on the same class or a class it inherits from.

### The scope ladder

**The problem.** A short name like `parse` can exist in many files. Which one does a call mean?

**The approach.** The ladder has up to five rungs, and the first rung that yields a candidate decides the answer:

| Rung | Where it looks | Why it comes here |
| --- | --- | --- |
| 1. Same file | Definitions in the caller's own file | A name defined right next to the call is the most likely meaning. |
| 2. Explicit import | Files the caller imported the name from | The author said so directly. |
| 2b. PHP includes | Files pulled in with `include` / `require`, transitively | PHP's equivalent of an import. |
| 3. Same directory | Definitions in the caller's directory | In most languages a directory is a package. |
| 4. Whole project | Every definition with the name, but only if there are no more than `max_ambiguous` (default 8) | The last resort, and the only one that can abstain. |

When several candidates survive on one rung, `pick` prefers the better-fitting *kind* (a plain call prefers a function, then a class, and methods only when not strict; a method call prefers a method, then a function), then the candidate whose path shares more leading directories with the caller, and finally the lowest index, which makes the result repeatable.

**Why this way.** Nearer scopes are right more often than far ones, and checking them first costs almost nothing, because the candidate lists are sorted so each rung is a binary search and not a scan. The cap is the key design choice: if a name is that common and nothing nearer settled it, any pick would be a coin toss.

### Imports and `strict_imports`

If a name comes from an import that points *outside* the project (the standard library or a third-party package), then with `strict_imports` on, v2 stops there and draws no edge, unless the same name is also an internal import. Without that rule, `os.path.join()` could be linked to some unrelated project function called `join`. Turn it off if TypeScript path aliases such as `@app/...` hide your internal modules from the resolver.

### Class hierarchy

**The problem.** `self.validate()` should resolve to a method of the enclosing class or something it inherits from, not to any function named `validate`.

**The approach.** The lookup tries the class's own methods, then its ancestors with a breadth-first walk, then its descendants, so an override in a subclass is also found. The walk is capped at 64 levels, and a visited set guards against cycles.

**Why this way.** Inherited methods are the common case, so ancestors come first. Subclasses come second so that a call on a base class can still find an override. The cap protects against cyclic or adversarial hierarchies.

### Receiver-aware resolution

Python, TypeScript and Rust parsers keep the *receiver* of a call (the `x` in `x.foo()`) as a separate field. For those three languages, v2 uses that receiver together with the file's imports, including Rust `use` trees, relative Python imports and relative TypeScript modules, to decide where a call goes. Other languages use the older, name-based path. Methods in those languages are still indexed under a name derived from the node id, so I would not assume that method calls resolve as well there; I did not measure the current effect.

### Two details that avoid wrong edges

A call to something that is actually one of the caller's own **parameters** is skipped, so `def f(helper): helper()` does not link to a global function named `helper`. A **nested** function (one declared inside another function) can only be called by its parent, and it is invisible to every other file and function.

### PHP

PHP gets its own module because its rules are different. Names are case-insensitive and namespace-qualified, each call shape (`foo()`, `$x->foo()`, `X::foo()`, `new X`) can reach only one kind of target, and `include` / `require` add a scope. Method calls on a receiver whose class is unknown need the *only* method of that name in the project. PHP and non-PHP symbols never resolve to each other. A cap of 2,048 files limits how far one file's include chain can reach.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `max_ambiguous` | `CallGraphOptions` | `8` | A whole-project lookup gives no edge when more than this many definitions share the name. Raise for more coverage, lower for more precision. |
| `strict_imports` | `CallGraphOptions` | `true` | Refuses to fall back to project definitions for names imported from outside the project. Turn off if TypeScript path aliases do not resolve. |

## Usage Examples

```rust
use libeulix::analyze::call_graph::prism_v2::{self, CallGraphOptions};

let opts = CallGraphOptions { max_ambiguous: 8, strict_imports: true };
kb.call_graph = prism_v2::build_with(&kb.structure, &opts);
prism_v2::populate_called_by_from_graph(&mut kb);
```

Expected result: `kb.call_graph` holds nodes and edges, and every resolved callee's `called_by` is filled. `prism_v2::build_call_graph_v2(&kb.structure)` does the same with default options.

## Performance Notes

Resolution cost is dominated by sorting the files, building the name index, and, per call, a handful of binary searches over candidate lists. See [Overview](Overview.md) for the documented target profile. The formal complexity analysis is in the [research write-up](Research/PRISM-V2-Methodology.md).

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Dynamic calls get no edge (`getattr`, `$this->$m()`, `obj[name]()`). | The target is not visible in the syntax. | None. |
| A virtual call gets one override, not all of them. | The walk returns the first match. | None. |
| Multiple inheritance follows breadth-first order by index, not the language's own method-resolution order. | Base-class order is not kept. | None yet. |
| "Same directory" means exactly the same directory. | A sibling package is only reachable by the whole-project rung, under its cap. | Raise `max_ambiguous`. |
| Resolution across languages is not blocked, except for PHP. | Only PHP is kept apart. | [PRISM-4](../Known-Issues/Parser-And-PRISM.md#prism-4-calls-resolve-across-languages). |
| Calls to methods in Go, Java, JavaScript, C and C++ get no edge. | These languages still use the older path, which indexes methods under an id-derived name no call is spelled as. I confirmed this on 2026-11-03. | Use [PRISM v1](PRISM-V1.md) for method-heavy code in those languages. See [PRISM-2](../Known-Issues/Parser-And-PRISM.md#prism-2-v2-never-links-calls-to-methods-in-go-java-javascript-c-and-c). |
| Calls on a receiver of unknown type get no edge, even in Python, TypeScript and Rust. | PRISM has no type inference and no longer guesses by unique name. | [PRISM-3](../Known-Issues/Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2). |
| No call graph above 100,000 files via `analyze_and_build`. | Memory guard in the deprecated pipeline function. | Call `build_with` directly. |

Other known problems (cross-language edges, dropped receivers in Go and Java, JavaScript base classes, doubled C++ calls and the slow Rust-workspace case) are listed with reproductions in [Parser And PRISM Issues](../Known-Issues/Parser-And-PRISM.md).

## Related Docs

- [PRISM v1](PRISM-V1.md): the simpler, faster version.
- [Research write-up](Research/PRISM-V2-Methodology.md): the formal description, for readers who want the maths.
- [Public API](Public-API.md): the functions and options.
- [Knowledge Base Schema](Knowledge-Base-Schema.md): the node and edge types.
