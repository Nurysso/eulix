# Retrieval Search Pipeline

This page walks through the first half of retrieval: understanding the question, spending the token budget, and gathering and scoring candidate chunks.

> **Crate / path:** `eulix-cli/internal/query/retrieval/` (`intent.go`, `anchors.go`, `gate.go`, `search.go`, `kb.go`, `bm25.go`, `prf.go`, `boilerPlate.go`, `subsystem.go`, `vector_ivf.go`)  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

By the time a question reaches this stage, the router has decided that code needs to be retrieved. The pipeline here produces a ranked list of *candidate* chunks. Choosing the final set from them, and showing real source, comes afterwards in [Selection And Hydration](Selection-And-Hydration.md).

The page describes the default engine's search. EHNAS runs the same early steps and the same core strategies, then adds its own layers ([Ehnas Augmentation](Ehnas-Augmentation.md)); the differences are noted where they matter.

## Why It Exists

No single search method is reliable for code. Exact names are best found by matching names. Descriptions of behaviour are best found by words or meaning. References like `server.go:120` are best found by parsing them. Running several methods and merging their opinions is more robust than betting on one.

## How It Works

### Intent

`classifyQueryIntent` assigns one of seven intents with a specificity and a confidence. It uses word lists and the shape of the symbols in the question:

| Intent | Typical trigger |
| --- | --- |
| Callers | "who calls", "called by", "usage of" |
| Callees | "what does ... call", "dependencies of" |
| Debug | "error", "panic", "crash", "bug", "nil" |
| Flow | "trace", "flow", "lifecycle", "sequence" |
| Concept | "how", "why", "explain" |
| Symbol exact | at least one code-looking symbol |
| Symbol fuzzy | defined, but the classifier never returns it |

*Specificity* (0 to 1) is higher when the question contains code-shaped symbols (`snake_case` or capitalized names). It scales the number of candidates and sets confidence in exact matching.

### Budget

The token budget is `llm.max_tokens`, minus 130 for the system prompt, minus the question, minus 2,048 reserved for the reply, with a floor of 512. The intent sets how the search effort is weighted among four families:

| Intent | kb_exact | keyword | semantic | graph |
| --- | --- | --- | --- | --- |
| Callers, Callees | 0.45 | 0.30 | 0.10 | 0.15 |
| Symbol exact | 0.55 | 0.20 | 0.10 | 0.15 |
| Symbol fuzzy | 0.40 | 0.25 | 0.20 | 0.15 |
| Concept | 0.15 | 0.20 | 0.50 | 0.15 |
| Flow | 0.20 | 0.15 | 0.25 | 0.40 |
| Debug | 0.35 | 0.35 | 0.15 | 0.15 |
| other | 0.30 | 0.25 | 0.25 | 0.20 |

A "where is `foo`" question leans on exact lookup; "how does the system handle sessions" leans on meaning.

### Explicit anchors and the path gate

`extractExplicitAnchors` finds places the user pointed at: `file.go:42`, a bare file name, a path fragment (`a/b/c`), or `funcName:line`. `explicitAnchorSearch` resolves them in order of confidence: exact file and line (×1.5), a file-path suffix and line (×1.2), file name, path fragment (×0.8), symbol and line (×1.3).

The gate works on the file part of each anchor. An anchor that names a file that really exists in the index (a name with a dot, found in some chunk's path) becomes a **required** location: every later strategy keeps only chunks whose path contains that file's directory (or, for a bare file name, the name itself). An anchor that matches nothing in the index, such as `eulix.toml`, would otherwise remove every result, so it is demoted to a **preferred** hint instead: matching chunks get a boost of 15% per hint, up to 45%. With no usable anchors the gate does nothing.

When any anchor scores 250 or more, the candidate limit is raised so the explicit references have room.

### The search strategies

Every strategy returns scored chunks. A wrapper truncates each to 500 hits, applies the gate, and merges the results into one map. When the same chunk comes from several strategies, the merged score is the **maximum** of the scores **plus a bonus** for the strategy that found it, and the match details are concatenated, so seeing a chunk more than once raises its rank.

| Strategy | What it matches | Bonus |
| --- | --- | --- |
| `kb_exact` | Symbols named in the question against functions, classes and methods in the knowledge base. **Currently returns nothing**: see [RET-1](../../../Known-Issues/Retrieval-And-Query.md#ret-1-the-kb_exact-search-strategy-never-returns-anything). | +2.5 |
| `grep` | Line-level text matches on file components, names, symbols and definitions. Caps per kind so several tokens matching the same thing do not stack. | +3.0 |
| `exact` | A case-insensitive exact match of a chunk's name or symbol. Rarer, longer symbols score more (×3.0 at 20+ characters, ×2.0 at 12+, ×1.5 at 7+). | +2.0 |
| `partial` | Identifiers broken into words (`NewHTTPServer` matches `http server`). Needs two words, or one plus a high score. | +1.5 |
| `keyword` | BM25 over an inverted index, then pseudo-relevance feedback (below), then a +25 for symbols seen as call sites and −10 for names that start with an underscore plus the symbol. | +2.0 |
| `semantic` (ETIYA only) | Cosine similarity of the question vector to chunk vectors, using the IVF index when available. Skipped for callers and callees questions. | +1.5 |

**Symbol hygiene.** Before searching, symbols shorter than three characters are dropped, and dotted names are split (`foo.Bar` becomes `foo.Bar`, `foo` and `Bar`), so one-letter variables do not pollute the results.

**BM25.** A standard relevance formula. Words that appear in few chunks count for more, long chunks are normalized, and the inverted index makes lookup cost depend on the number of query words, not the number of chunks.

**Pseudo-relevance feedback (PRF).** After a first keyword pass, the words that the top hits share with each other are used to search again, which finds paraphrases without vectors. It is deterministic and weaker than embeddings, but it is the paraphrase channel when none exists.

**Boilerplate.** Symbols that occur in a large share of all chunks (the threshold adapts to the repository size) are treated as boilerplate and never pinned as exact anchors.

### Anchor pins

High-confidence entities from the question are *pinned* by giving them a score of 5,000 plus a confidence bonus, which no later stage can overtake. Entities that appear in the call graph earn an extra bonus so pins do not all tie.

### Subsystem boosting

Directories are arranged into a tree, built once at load time, keeping nodes with at least 30 chunks. The question's words are matched against directory names, and chunks in the best-matching subsystems are boosted. Paths that look like noise (vendored copies, generated code) are detected and excluded. `filterTestDocChunks` then drops test and documentation chunks unless the question calls for them or the chunk scores at least 60.

### Scope demotion

Scores are multiplied down for position, not content:

| Condition | Multiplier |
| --- | --- |
| Test file, question is not about tests | `test_file_penalty` (0.30) |
| Test file, question is about a concept or flow | up to 0.70 |
| File under a different top-level folder from the main subsystem (see below) | `cross_root_penalty` (0.32) |

Exact matches are exempt. The list is then sorted stably (exact hits first, then score) and cut to the candidate limit.

### Main Components

| Item | Responsibility |
| --- | --- |
| `classifyQueryIntent`, `allocateBudget` | Intent and budget. |
| `extractExplicitAnchors`, `explicitAnchorSearch`, `PathGate` | Explicit references. |
| `multiStrategySearch` | The ETIYA strategy swarm and merge. |
| `invertedKeywordSearchBM25`, `prfExpand` | Keyword and feedback. |
| `buildSubsystemTree`, `detectQuerySubsystems`, `boostByDetectedSubsystems` | Directory-aware boosting. |
| `vectorSearch`, `vectorSearchIVF` | Semantic strategy. |

### Data In, Data Out

In: the question and its intent. Out: a sorted list of `ScoredChunk`, each with a score, match type, match details and an exact flag.

## Key Algorithms

### Max plus bonus

**The problem.** Scores from different strategies are on different scales, so they cannot simply be added.

**The approach.** Keep the highest score for the chunk, and add a fixed bonus per extra strategy that found it. Pins and exact matches live in a different score range entirely (hundreds to thousands), so they are not affected by the bonuses.

**Why this way.** Agreement between independent methods is evidence, and a fixed bonus captures that without having to calibrate scales.

### IVF vector search

**The problem.** Comparing a question vector with a million chunk vectors one at a time is slow.

**The approach.** Chunk vectors are grouped into 256 clusters by k-means when the index loads. At query time, the 32 nearest cluster centres are probed and only the vectors in them are scored exactly. If the index is not built yet (it builds in the background) or the repository is small, a full scan is used.

**Why this way.** Probing 32 of 256 clusters examines about an eighth of the vectors with little loss of recall.

## Configuration

`top_k_candidates`, `semantic_min_similarity`, `max_exact_anchors`, `enable_subsystem_boosting`, `test_file_penalty`, `apply_cross_root_isolation` and `cross_root_penalty`, all in `[retrievalConfig]`; see [Config](../../Config.md).

## Usage Examples

Questions that exercise different paths:

```text
who calls applyBudget?                      → callers intent, call-site search
fix the panic in internal/mcp/server.go:120 → debug intent, explicit anchor and path gate
how do sessions expire?                     → concept intent, semantic and keyword weight
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Cross-root demotion applies only to top-level folders starting with `charm-` or named `requirements`. | Those names are written into the code, so `apply_cross_root_isolation` has no effect on other layouts. | [RET-2](../../../Known-Issues/Retrieval-And-Query.md#ret-2-cross-root-demotion-only-works-for-two-folder-names). |
| The `kb_exact` strategy is dead. | `kbData` is never set. | [RET-1](../../../Known-Issues/Retrieval-And-Query.md#ret-1-the-kb_exact-search-strategy-never-returns-anything). |
| Intent is decided by word lists. | No model. | Include a symbol or clear keyword. |
| Test files are recognized by a few path patterns (`/tests/`, `/test_`, `fake_`, `_test.go`). | Heuristic. | Name test directories conventionally. |
| `invertedKeywordSearchTFIDF` is dead code. | A TODO in the source notes the plan to make TF-IDF versus BM25 selectable. | None. |

## Related Docs

- [Overview](Overview.md): where this fits.
- [Ehnas Augmentation](Ehnas-Augmentation.md): the extra EHNAS layers.
- [Selection And Hydration](Selection-And-Hydration.md): the next stage.
- [Index Loading](Index-Loading.md): where chunks and indices come from.
