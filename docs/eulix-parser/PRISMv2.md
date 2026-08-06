# PRISM — Polyglot Resolution via Inverted Symbol Map

> A first-match **approximation** call-graph algorithm built for **RAG retrieval**
> over multi-language codebases. Not for compilers. Not for static analysis
> tools that need soundness.

---

## TL;DR

PRISM is **deliberately lossy**. It builds a call graph in ~O(N + E) wall time
and ~O(N) RAM by ignoring everything a compiler would care about: types,
scopes, generics, traits, modules, headers, dynamic dispatch. That trade-off
buys a fast, single-pass graph over a 100k-file polyglot tree. The graph is
good enough for "show me what calls this function in the embedding index" and
"given this code chunk, which other chunks are related". It is **not** good
enough for "is this call safe to inline" or "what's the actual runtime type
here".

---

## What each parser feeds PRISM

Every per-language parser (`python_parser.rs`, `rust_parser.rs`, `cpp_parser.rs`,
`c_parser.rs`, `typescript_parser.rs`, `go_parser.rs`) collapses the call site
into a **bare short name** before pushing into `FunctionCall.callee`:

```rust
// Python:   obj.method()    → callee = "method"
// Rust:     pkg::Foo::bar() → callee = "bar"
// C++:      ns::Class::f()  → callee = "f"
// TypeScript: a.b.c()        → callee = "c"
// Go:        pkg.Bar()       → callee = "Bar"
```

All qualifiers — module path, namespace, type, attribute access — are stripped
at parse time. PRISM only ever sees `(short_name, line, args)`. The receiver
type, the import that brought the symbol in, and the lexical scope are gone.

The entity IDs that PRISM indexes against use a small grammar:

| Kind     | ID shape                                         |
| -------- | ------------------------------------------------ |
| function | `fn_<name>` or just `<name>`                     |
| method   | `method_<ClassName>_<methodName>`                |
| class    | `class_<Name>` / `struct_<Name>` / `enum_<Name>` |

PRISM uses the `"method_"` prefix and `::` segments as the only structure it
gets back. Everything else is a flat string.

---

## The algorithm — five phases

PRISM lives in `analyzer.rs::build_call_graph()`. Five phases, all parallelised
with Rayon over chunks of 2 000 files.

### Phase 1 — Node extraction (parallel)

```
for each file in chunk:
    for each func:     nodes += (id, FUNCTION)
    for each class:    nodes += (id, CLASS)
        for each m:    nodes += (id, METHOD)
dedup by id → unique_nodes, node_map: id → dense_index
```

A `CompactNode` is `(id, type ∈ {0,1,2}, file_idx, is_entry)`. Dedup is by
first occurrence. Result: `O(N)` nodes, `O(N)` ints in `node_map`.

### Phase 2 — Symbol index pre-computation

This is the **inverted symbol map** — the namesake of the algorithm. It is
built once, before any edge resolution:

```
symbol_index: HashMap<short_name, dense_index>

for (id, idx) in node_map:
    short = id.split("::").last()
              .and_then(|s| s.strip_prefix("method_"))
              .unwrap_or(id)
    symbol_index.entry(short).or_insert(idx)   # first wins
```

Two-step lookup turns what would be a 30-minute linear scan over every node
per call site into a **microsecond hash lookup**. The `or_insert` is the
"first wins" rule — it is the single most important line in the algorithm
and the single biggest source of inaccuracy (see §Accuracy).

### Phase 3 — Edge extraction (parallel)

For each call site, resolve the callee via a two-tier resolver:

```
resolve(callee):
    if callee in node_map:                      # tier 1 — exact match
        return that index

    short = callee.split("::").last()
              .and_then(|s| s.strip_prefix("method_"))
              .unwrap_or(callee)

    return symbol_index.get(short)               # tier 2 — first wins
```

Edges are stored as `CompactEdge = (from_idx, to_idx, kind, is_cond, line)`.
`kind` is `0` for call edges and `1` for inheritance edges (which Phase 3
records but **does not use** — see "Known limitations" below).

### Phase 4 — In-degree count

A single pass over edges to compute `call_count_estimate` per node — how many
times this node appears as a callee. This is the only numeric property PRISM
attaches to a node.

### Phase 5 — Conversion

Flat `Vec<CompactNode>` → `Vec<CallGraphNode>`,
flat `Vec<CompactEdge>` → `Vec<CallGraphEdge>` (with bounds checks on indices).

---

## The math

There is no textbook algorithm here — PRISM is closer to a **bag of hash
tables** than to a points-to analysis. The "math" worth naming is the
precision/recall framing, because that is what determines whether the graph
is useful for retrieval.

### Resolution as classification

Each call site is a **classification problem**: assign the bare short name
`c ∈ Σ*` to one of the candidate IDs `{n_1, …, n_k}` that share that short
name. PRISM implements the classifier:

$$
\hat{n} = \arg\min_i \text{ ord}(\text{first\_seen}(n_i))
$$

i.e. the **earliest-defined entity with that short name wins**. This is the
"first-wins" tiebreaker, exactly the `or_insert(idx)` line.

Precision of a call edge is then:

$$
P_{\text{edge}} = \Pr[\hat{n} = n^* \mid c]
$$

Recall of a call edge is **always 1** in PRISM — every call site that
resolves to _something_ produces an edge. The only way recall drops below 1
is if **both** tiers fail (`node_map` and `symbol_index` have no match),
which happens for external calls (stdlib, third-party) and dynamic names.
Those are silently dropped, not marked as "unknown".

### Why this is fast

Two hash lookups per call site, both O(1) amortised. Phase 3's inner loop is:

```
for call in func.calls:
    resolve(call.callee)            # O(1)
```

Total edge work is `O(E)` after the `O(N)` symbol index build. PRISM does
**no** BFS, no fixpoint iteration, no constraint solving. That's the whole
point.

### Why this is approximate

The classification rule ignores three pieces of information that a sound
analysis would use:

| Information              | Where it lives              | PRISM uses it?                             |
| ------------------------ | --------------------------- | ------------------------------------------ |
| Receiver type            | AST, before qualifier strip | No — stripped at parse                     |
| Lexical scope / module   | tree-sitter context         | No — not stored                            |
| Type inheritance / impls | class.bases (parsed but)    | Recorded as edges, **not used in resolve** |

The first-wins tiebreaker is the dominant source of error in any language
that has overloaded short names.

---

## Accuracy per language

Numbers below are **order-of-magnitude** estimates for a typical mid-size
codebase. They assume no unusual macros, no codegen, no aspect-oriented
weaving. They are **not** measured benchmarks — they are reasoning from the
algorithm's known failure modes.

| Language       | Edge precision | Edge recall | Notes                                                                                          |
| -------------- | -------------: | ----------: | ---------------------------------------------------------------------------------------------- |
| **Python**     |        ~35–50% |     ~85–90% | Heavy method overloading, monkey-patching, decorators. `init` collisions are rampant.          |
| **TypeScript** |        ~45–60% |     ~80–85% | Module system ambiguity: same name from two modules. Receivers often `any`.                    |
| **JavaScript** |        ~40–55% |     ~75–85% | Worse than TS — no type info at all, plus dynamic property access.                             |
| **Go**         |        ~55–70% |     ~90–95% | Receivers are typed, but `pkg.Foo` is collapsed to `Foo`. Cross-package collisions still bite. |
| **Rust**       |        ~40–55% |     ~80–85% | Trait dispatch (`T::method()`) resolves to whichever first impl is seen. Generics erased.      |
| **C**          |        ~50–65% |     ~85–90% | Headers and macros are opaque — PRISM sees only what the parsed source contains.               |
| **C++**        |        ~30–45% |     ~70–80% | Worst case: namespaces, ADL, operator overloading, templates, multiple inheritance.            |

**Where the numbers come from:**

- _Precision_ drops fast when the symbol table contains multiple definitions
  of the same short name. The "first wins" rule guesses one, silently wrong on
  the others. C++ namespaces, Rust trait methods, Python `__init__` — all
  collide.
- _Recall_ drops when the resolver returns `None` — calls to stdlib,
  third-party crates, dynamically-named methods (`getattr(obj, name)`), and
  macro-generated calls. PRISM does not invent a target; it just doesn't draw
  the edge.
- _Go_ has the best numbers in this set: short identifiers are more
  disciplined, package boundaries are stricter, and `pkg.Foo` is the
  dominant form. Even after collapsing to `Foo`, the language's naming
  conventions keep collisions low.
- _C++_ has the worst: every part of the language is designed to put multiple
  same-named entities in scope at once. PRISM has no chance of disambiguating
  them without a real frontend.

These figures describe **edge correctness**. PRISM's value for RAG is not in
being right about every edge — it's in giving the embedder a high-recall
"nearby" graph that surfaces related chunks. A wrong edge that points to a
plausible same-named function is far less harmful to retrieval than a missing
edge that hides a real call.

---

## Why this is **only** an approximation (and why that is fine)

### What a real call-graph algorithm does that PRISM does not

| Step                         |             Compiler-grade | PRISM |
| ---------------------------- | -------------------------: | ----: |
| Parse with type info         |                          ✓ |     ✗ |
| Build symbol table per scope |                          ✓ |     ✗ |
| Resolve overloads / generics |                          ✓ |     ✗ |
| Model virtual dispatch       |                          ✓ |     ✗ |
| Track dataflow / points-to   | ✓ (Steensgaard / Andersen) |     ✗ |
| Fixpoint iteration           |                          ✓ |     ✗ |
| Approximate by short-name    |                          ✗ |     ✓ |

A real call-graph build for a 100k-file polyglot corpus takes **hours** and
**gigabytes** of RAM. PRISM does it in **seconds** and **tens of MB**.

### Why RAG can tolerate the noise

A retrieval-augmented generator over a codebase needs three things from a
call graph:

1. **High recall on common calls** — most queries are about _how is X used_
   and _what does Y depend on_. PRISM delivers this because most call sites
   resolve cleanly through Phase 2's symbol index.
2. **Plausible neighbours, even when wrong** — a wrong edge that points to
   a same-named function in the same codebase is _almost_ as useful for
   retrieval as a right one. The embedding still co-locates the related
   chunks.
3. **A connected structure** — even with 50% precision, the graph stays
   connected enough for BFS-style "expand context around this function" to
   surface non-trivial neighbours.

What RAG does **not** need:

- **Soundness** — no one is going to refactor based on a guarantee that
  "this function cannot be called with `None`". The LLM is reading, not
  compiling.
- **Completeness at the edge** — dropping 10–20% of call edges (mostly to
  external libs) costs nothing because external code is not in the KB to
  embed anyway.
- **Type-correctness** — the embedding encodes textual + structural
  proximity, not type compatibility.

### Where the approximation visibly hurts

Two failure modes will surface in production:

1. **Ambiguous short names**: if your codebase has three classes with a
   `process()` method, every call to `process()` from anywhere in the codebase
   gets the same target. Retrieval will over-cluster these; downstream
   ranking needs to disambiguate using the actual chunk text.
2. **External / dynamic calls**: `os.path.join`, `obj.__getattr__("foo")`,
   Rust `format!` macro — these don't resolve and don't draw edges. The
   embedder sees orphan chunks; not catastrophic, but they lose "called by"
   context.

Both are accepted as design constraints. Fixing them would require either
(a) running each language's actual frontend (clang, rustc, tsc), or (b) doing
a real points-to analysis. Neither fits inside a "scan a polyglot tree in
seconds" pipeline.

---

## Known limitations (also in the code's own TODO list)

- `build_call_graph()` has a TODO to write `build_call_graph_precise()` and
  `populate_called_by_from_graph()`. Today there is only one
  implementation — the approximate one. The `use_precise = true` flag is
  permanently on and there is no precise alternative yet.
- Inheritance edges are recorded but not used in resolution. The data is
  there; the logic is not.
- No receiver-type tracking. `obj.method()` becomes `method`, period.
- No module / namespace tracking in the symbol index. Cross-namespace
  collisions silently collapse.
- `resolve_call_locations()` is a separate pre-pass that tries to upgrade
  raw call names to fully-qualified IDs by name lookup alone. When a short
  name has multiple definitions, only the first is recorded in
  `func_info` (`entry().or_insert_with(...)`). The same first-wins rule as
  the symbol index, applied one layer earlier.
- No handling of dynamic / reflective calls. `getattr`, `eval`,
  `Function("name")()` etc. silently resolve to nothing.
- For >100k files PRISM is **disabled entirely** (skipped with a warning).
  The threshold is conservative — the chunking + classification RAM cost
  scales roughly linearly and PRISM is the heaviest phase.

---

## When to upgrade

If you find yourself needing a _sound_ call graph — e.g. for a refactoring
tool, a security audit, or a deprecation impact analysis — the path is:

1. Drop in a real frontend per language. `tree-sitter` is already wired in
   for parsing, but resolution is what PRISM skips; you need a type-aware
   second pass.
2. Or, simpler: run the _compiler_ for statically-typed languages (`rustc`,
   `tsc --noEmit`, `g++ -fdump-tree-original`) and let it emit the call
   graph for you. The output format will not match PRISM's, so you'll need
   a separate ingestion path. The existing `eulix_embed` binary format does
   not change.
3. The `is_large` threshold (100 k files) is a single `const`. For very
   small corpora where you want full soundness, lower it to 0 and accept the
   RAM cost. For massive corpora where PRISM's approximation is too lossy,
   raise it and accept that some corpora simply don't get a call graph.

The current design assumes the RAG-flavoured trade-off — fast, approximate,
retrieval-grade — is the right default for a code-search / chat codebase
agent. That assumption is encoded in the algorithm name, in the comments,
and in the absence of a `build_call_graph_precise` function.

---

_End of PRISM.md_
