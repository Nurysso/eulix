# EHNAS Augmentation Layers

This page explains the three extra layers the EHNAS engine adds to the shared search: twin search, a Monte Carlo walk, and novelty packing, plus its structural selector.

> **Crate / path:** `eulix-cli/internal/query/retrieval/` (`ehnas_search.go`, `twins.go`, `walk_graph.go`, `mmr_ehnas.go`, `prf.go`)  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9` (the design is also described in `retrieval/README.md`, which this page follows)

## Overview

The EHNAS engine has a deterministic core: exact matching, a path gate, subsystem boosting and scope demotion, all described in [Search Pipeline](Search-Pipeline.md). At the scale of millions of lines, three problems remain that the core cannot solve, so EHNAS adds one layer for each. Each layer reports its contribution separately in the debug trace, so the deterministic part of any score can always be recovered.

| Problem at scale | Layer |
| --- | --- |
| A dependency chain several files long is invisible to a single-hop search. | **Monte Carlo walk** |
| The same logic ported to another language shares no words with the question. | **Twin search** |
| The context window fills with near-duplicate boilerplate. | **Novelty packing** |

## Why It Exists

A codebase is not a pile of independent documents. It is a graph of symbols and calls, it contains parallel implementations in several languages, and the context window is a fixed budget that must be spent well. The layers treat those three facts directly, without a model and without vectors.

## How It Works

### Twin search

Two chunks are *twins* if they have the same **structural signature**: a 64-bit hash of the chunk's type and its sorted, lower-cased set of symbols with underscores removed. A signature needs at least two symbols, and a signature shared by more than 24 chunks is discarded as too generic.

The index is built once, when the engine loads. At query time, every hit scoring at least 40 passes `score × 0.55 ÷ √(class size)` to each twin in a *different* language, and contributions from several sources add up. This is the only way a query can reach code that shares no word with it, and it costs a hash lookup per hit.

### Monte Carlo walk

Random walks over three graphs of the code: symbol co-occurrence, the call graph, and word co-occurrence.

1. **Seeds.** The best hits so far, with a score of at least 60 and at most 16 of them.
2. **Walkers.** 256 walkers start from the seeds.
3. **Each step.** A walker picks a layer and moves to a neighbour chosen at random in proportion to edge weights. A walk is at most 20 steps.
4. **Stopping.** At each step a walker stops with probability `0.30 + min(0.45, df/5000)`, where `df` is how many chunks contain the symbol. Common symbols (boilerplate) make walks stop sooner.
5. **Branching.** At a *hub* (a chunk with 96 or more neighbours), a walker spawns two more with probability 0.25.
6. **Scoring.** Chunks visited at least twice get `visits × 12`.

The walk's cost is about 256 × 20 steps per query regardless of corpus size, because it samples. The part that depends on corpus size is building the layers, done once or lazily. Because the seeds are EHNAS's own best hits, the walk explores only structure the deterministic search already trusts. Anchor pins are resolved *after* the walk, so they never seed it.

### Per-language normalization

Dense languages (C, with short symbols and high document frequency) would out-rank sparse ones simply for being dense. Each non-exact score is divided by its language's relative lexical density.

### Novelty packing

While the window is being filled, each candidate block is scored for *novelty*: one minus the share of its symbols that earlier picks already contain. A logistic curve turns that into a multiplier on the block's score, between 0.15 and 1.0. The curve starts soft and gets sharper as the window fills (a "temperature" that falls from 0.50 to 0.05), so early picks explore and late picks must be strictly new. Symbols found in more than 200 chunks (error handling, logging, common imports) carry no redundancy cost, so connector code can repeat. Exact and pinned chunks skip the gate.

### Structural MMR (selection)

EHNAS selects with a version of Maximal Marginal Relevance that does not use the question. Relevance is the fused score; redundancy against already-chosen chunks is a weighted sum of three channels:

| Channel | Weight | What it measures |
| --- | --- | --- |
| Symbol overlap | 0.45 | idf-weighted overlap of non-boilerplate symbols |
| Line proximity | 0.20 | distance between lines in the same file |
| Same twin | 0.55 | same structural twin class, in any language |

A per-file saturation floor stops one file from filling the window. The trace calls it `structural_mmr`.

### Main Components

| Item | Responsibility |
| --- | --- |
| `EhnasSearch` | The EHNAS search flow. |
| `buildTwinIndex`, `crossLangTwinSearch` | Twin signatures and lookup. |
| Walk layers in `walk_graph.go` | Symbol, call and lexical layers; the walk itself. |
| `prfExpand` | Pseudo-relevance feedback for the keyword strategy. |
| `noveltyPack` | The redundancy gate. |
| `structuralMMR` | The selector. |

### Data In, Data Out

In: the question and the loaded chunks. Out: scored chunks with extra match details such as `twin-bridge:`, `mc-visits:` and `novelty: w=`.

## Key Algorithms

### Why sampling is acceptable

**The problem.** Exploring every path in a million-chunk graph is impossible.

**The approach.** Sample many short random walks. Chunks that many walkers reach are probably well connected to the seeds.

**Why this way.** The results come with a built-in confidence (the visit count) and the cost does not grow with the repository. The cost is non-determinism in principle; the visit threshold of two keeps rare accidents out.

## Configuration

Settings are under `[retrievalConfig.ehnas]`.

| Key | Default | Meaning |
| --- | --- | --- |
| `mmr_stop_floor` | 0.0 | Structural MMR stops when the best marginal value is at or below this. |
| `novelty_gain` | 0.85 | Largest boost for a fully new block. |
| `mc_seed_min_score` | 60 | Smallest score for a hit to seed the walk. |
| `mc_walkers` | 256 | Walkers per query. |
| `mc_seed_cap` | 16 | Most seeds. |
| `common_symbol_df` | 200 | Symbols in more chunks than this carry no redundancy cost. |
| `inverted_index_min_chunks` | 5000 | Build the inverted index (for PRF and the lexical walk layer) only above this many chunks; `0` always builds it. |

Constants fixed in code: maximum walk length 20, base stop probability 0.30, hub branching 3, visit score scale 12, twin score floor 40, twin decay 0.55, twin class cap 24, temperature 0.50 falling to 0.05.

## Usage Examples

```toml
[project]
engine = "ehnas"

[retrievalConfig.ehnas]
mc_walkers = 512
```

Expected result: the debug log shows `[EHNAS]` load lines and `[mc-walk]` summaries with the walker count.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The walk is random. | Sampling. | Raise `mc_walkers` for stabler results. |
| Twin search needs at least two languages with similar structure. | Signatures are language-agnostic but only matter across languages. | None; it simply finds nothing in a single-language project. |
| Building the lexical layer costs time proportional to the sum of squared document frequencies. | It links chunks that share words. | It is built lazily and only above `inverted_index_min_chunks`. |
| Several functions in `ehnas_search.go` are commented-out earlier versions. | Work in progress. | Read the live code, not the comments. |

## Related Docs

- [Engines](Engines.md): choosing an engine.
- [Search Pipeline](Search-Pipeline.md): the shared core.
- [Selection And Hydration](Selection-And-Hydration.md): what happens after selection.
