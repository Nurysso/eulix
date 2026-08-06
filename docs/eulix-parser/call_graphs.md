# PRISM Call Graph — v1 and v2

Eulix Parser builds its call graph via one of two algorithms, chosen at
runtime by the `--prism <1|2>` flag. This file is the map; see the linked
docs for depth on each.

|                         | v1 (`--prism 1`)               | v2 (`--prism 2`)                                                      |
| ----------------------- | ------------------------------ | --------------------------------------------------------------------- |
| Function                | `build_call_graph()`           | `build_call_graph_v2()`                                               |
| Resolution tiers        | 2 (exact match → symbol index) | 5 (exact → self/class hierarchy → file scope → import → symbol index) |
| Uses inheritance edges? | Recorded, not used             | Used (Tier 2, BFS chain walk)                                         |
| Indexes built           | 1 (symbol_index)               | 7                                                                     |
| Relative accuracy       | Lower, esp. on OOP-heavy code  | Higher, especially method calls                                       |
| Relative speed/memory   | Faster, less RAM               | Slightly more work per file                                           |
| Detailed doc            | [PRISM_v1.md](./PRISM_v1.md)   | [PRISM_call_graph.md](./PRISM_call_graph.md)                          |

## Relationship between the two

v2 is not a rewrite — it's v1 with three tiers inserted in the middle.
Tier 1 (exact match) and the final fallback tier are structurally identical
in both versions; v2 just tries harder before giving up. If you understand
v1's two-tier resolver, v2 is "same idea, more context consulted first."

## Which one actually runs

Controlled by `analyze_and_build(kb, verbose, prism)` in `analyze.rs`:
`prism == 2` → v2, anything else → v1. There is currently no default value
on the `--prism` CLI arg — it must be passed explicitly.

## When to use which

- `--prism 2` (recommended default): better accuracy, worth the extra
  index-building cost for anything feeding the RAG pipeline.
- `--prism 1`: only if you need the memory/speed savings and can tolerate
  first-wins collisions on ambiguous short names.

## Read more about them

[Prism v1](PRISMv1.md) -> V1 of PRISM algorithm

[Prism v2](PRISMv2.md) -> V2 of PRISM algorithn
