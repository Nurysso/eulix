# PRISM v1

PRISM v1 is the fast, name-based call-graph builder in `libeulix`: every call resolves to the first function or method with the same name.

> **Crate / path:** `libeulix/src/analyze/call_graph/prism_v1.rs`  
> **Language:** Rust  
> **Status:** stable, maintained as the simple and fast option  
> **Last verified against:** `923a577`

## Overview

PRISM stands for *Polyglot Resolution via Inverted Symbol Map*. Version 1 is the simplest form of the idea. It builds a table from every function and method name to a definition, and then it looks up each call in that table. Because a table lookup takes the same tiny amount of time whether the project has ten functions or a million, v1 handles very large repositories quickly and with little memory.

The trade-off is precision. v1 does not know which file a call comes from, what that file imports, or which class it sits in. When two unrelated functions share a name, v1 links every caller to the same one. That is the problem [PRISM v2](PRISM-V2.md) was written to solve. Use v1 when you want broad coverage at the lowest cost and can accept some wrong edges on common names.

## Why It Exists

A call graph needs a decision for each call: which definition does it mean? The honest answer requires type information for every supported language, which is expensive and mostly out of reach. v1 skips that and makes the cheapest possible decision, "the first one with that name", because for retrieval and navigation a fast, mostly-right relationship is often more useful than a slow, perfect one.

An earlier version of this step scanned every node for every unresolved call, and on large repositories that took on the order of tens of minutes. Replacing the scan with a hash lookup is where the "inverted symbol map" in the name comes from: instead of searching the code for a name, you keep an index from the name to the code.

## How It Works

v1 is a three-step process, and the order of the steps matters.

**Step 1: resolve call locations.** `resolve_call_locations` first builds a map from each function and method *name* to the id of its first definition. Files are sorted by path, so "first" is well defined and repeatable. Then it rewrites every call in the project: the `callee` field changes from the plain name (`save`) to the definition's id (`method_Form_save::app/models.py`), and `defined_in` is filled with the file. This step **changes the knowledge base in place**.

**Step 2: build the graph.** `build_call_graph` collects a node for every function, method and class, and then walks every call. A call whose callee is now an exact node id becomes an edge. Each class's base classes are matched by class name to create `inheritance` edges. Finally, each node's `call_count_estimate` is set to the number of edges that point at it.

**Step 3: fill in callers.** `populate_called_by` groups all calls by their resolved callee and stores the callers on the target function, so each function knows who calls it.

If you call `build_call_graph` without running step 1 first, bare call names do not resolve, and you only get inheritance edges and calls that were already full ids.

### Main Components

| Function | Responsibility |
| --- | --- |
| `resolve_call_locations(&mut kb)` | Pre-pass that rewrites each call's `callee` to a definition id. Must run first. |
| `build_call_graph(&structure)` | Builds nodes and edges from the rewritten calls and the class bases. |
| `resolve_indirect_call(callee, ...)` | Tries an exact id, then a short-name lookup. See the note below. |
| `populate_called_by(&mut kb)` | Fills `called_by` from the rewritten calls. |

### Data In, Data Out

The input is a knowledge base whose files are already parsed. The output is a `CallGraph` with `call` and `inheritance` edges, plus the `called_by` lists inside `kb.structure`. The graph is deterministic: files are sorted by path before anything is chosen, so two runs on the same input give the same graph.

## Key Algorithms

### PRISM v1 in Plain Words

Think of a phone book with one line per function name. The line holds the first function in the project that has that name. When some code calls `save()`, v1 opens the phone book, finds `save`, and draws a line from the caller to that entry. It never asks where the call is made from, and it never says "I don't know".

That is why it is so fast, and also why it is wrong whenever two functions share a name. Both of them look the same to the phone book, and only the first one is ever listed.

### First definition wins

**The problem.** Many projects have several functions with the same name, such as `run`, `handle`, `Get` or `__init__`. A name-only lookup has to pick one.

**The approach.** Files are sorted by path, the work is split into chunks that run in parallel (1,000 files per chunk in the pre-pass, 2,000 in the graph build), and the per-chunk tables are merged in an order-preserving way. The earliest definition wins, so the answer does not depend on how the thread pool happened to split the work.

**Why this way.** It is simple, and it is repeatable. The cost is that the "right" definition is chosen only by file order, which is arbitrary from the point of view of the code.

### The short-name fallback (mostly inactive)

`resolve_indirect_call` has a second tier that looks the callee up by its short name. With the id format that every parser emits today (`kind_name::path`), that tier never finds anything the exact match did not. The index keys come from the part of the id after the last `::`, which is the file path, while the lookup uses the part before the first `::`. The two only meet for ids without a path suffix. In practice, v1 resolution is the pre-pass plus an exact id match.

## Configuration

PRISM v1 has no options.

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `prism` | `analyze_and_build` argument, or `--prism` / `prismVersion` in the parser | none | `1` selects v1. Any value other than `2` also selects v1 in `analyze_and_build`. |

## Usage Examples

```rust
use libeulix::analyze::call_graph::prism_v1;

prism_v1::resolve_call_locations(&mut kb);                   // rewrite callees to ids
kb.call_graph = prism_v1::build_call_graph(&kb.structure);   // nodes and edges
prism_v1::populate_called_by(&mut kb);                       // reverse edges
```

Expected result: `kb.call_graph` holds the nodes and edges, and each function's `called_by` is filled.

## Performance Notes

The work is a sort, one pass to build tables, and one pass over the calls, each using constant-time lookups. With F files, N nodes, C call sites and B base-class references, the cost is about O(F log F + N + C + B). There is no per-call scan and no iteration until a fixpoint. Nothing here was re-measured for this page.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| One target per name, project-wide. | The map has one entry per name. | Use [PRISM v2](PRISM-V2.md). |
| It never abstains. | There is no "too common" rule, so `get` always gets an edge. | Use v2 and its `max_ambiguous` option. |
| Calls into the standard library or vendor code can link to a project function with the same name. | There is no import awareness. | Use v2 with `strict_imports`. |
| Qualified callees such as `obj.m` or `$this->m` do not match. | The map is keyed by bare names. | None in v1. |
| Constructor calls get no edge. | Classes are not in the pre-pass map. | v2 links them to the class. |
| Shadowing is ignored. | A parameter with the same name as a global function still links to the global function. | v2 skips calls to parameters. |
| Inheritance is recorded but not used. | Walking the hierarchy is exactly the extra work v2 exists to do. | Use v2. |
| The pre-pass changes `kb.structure`. | It rewrites `callee` and `defined_in`. | Do not read `calls` expecting raw names after a v1 run. |
| `call_count_estimate` counts every edge into a node, including inheritance edges. | It is derived from all edges. | Count `call` edges yourself if you need only calls. |

For Python, TypeScript and Rust, v1 de-duplicates calls that share a callee name and line before using them, because those parsers record one call per receiver and a line like `a.get() + b.get()` would otherwise produce two identical edges.

## Related Docs

- [PRISM v2](PRISM-V2.md): the scope-aware version.
- [Public API](Public-API.md): the call order for v1.
- [Knowledge Base Schema](Knowledge-Base-Schema.md): the node and edge types.
