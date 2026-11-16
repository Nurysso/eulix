# retrieval

The `retrieval` package chooses which pieces of code to show the language model for a given question.

> **Crate / path:** `eulix-cli/internal/query/retrieval/`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

A model can only read a few thousand tokens at a time, while a repository can have millions of lines. Retrieval is the step in between: given a question, find the handful of functions, classes and files that matter, fit them into the token budget, and show real source where it can. The quality of every Eulix answer depends on this step more than on any other.

The package is built around one object, `ContextBuilder`. It holds the loaded knowledge base as a list of **chunks** (one per function, method or class), the indices over them, and the call graph. `BuildContext(question)` runs the whole pipeline and returns a `ContextWindow`.

This folder documents the package in six more pages:

| Page | Covers |
| --- | --- |
| [Engines](Engines.md) | The two interchangeable engines, ETIYA and EHNAS, and how one is chosen. |
| [Search Pipeline](Search-Pipeline.md) | Intent, budget, anchors, the search strategies, boosting and demotion. |
| [Ehnas Augmentation](Ehnas-Augmentation.md) | Twin search, the Monte Carlo walk, novelty packing and the structural selector. |
| [Selection And Hydration](Selection-And-Hydration.md) | Call-graph expansion, picking the final chunks, and swapping signatures for source. |
| [Index Loading](Index-Loading.md) | How the knowledge base and vectors are loaded into memory. |
| [Mmap](Mmap.md) | The memory-mapped file helpers. |

## Why It Exists

Three failures make naive approaches poor on code. Keyword search misses code that does not share words with the question. Pure vector search misses exact names and returns plausible but wrong neighbours. And both tend to fill the window with near-duplicate chunks (boilerplate, logging, error handling) that crowd out the important code. This package combines several strategies, keeps explicit user references safe from being outranked, and then removes redundancy deliberately.

## How It Works

`buildContextInternal` is the whole story in order. The numbers refer to the stages explained in later pages.

1. **Anchors and gate.** Pull explicit references out of the question (`file.go:42`, `a/b/c`, `name:line`) and, if they name a path, build a path gate that restricts later strategies to matching files.
2. **Intent and budget.** Classify the question's retrieval intent (callers, callees, debug, flow, concept, exact symbol), then split the token budget: total tokens, minus 130 for the system prompt, minus the question, minus 2,048 reserved for the reply.
3. **Query vector.** Only for the ETIYA engine, and not for callers/callees questions, embed the question.
4. **Exact anchors.** Find high-confidence exact symbol matches (score 90 or more, and not boilerplate); keep at most `max_exact_anchors` (2).
5. **Call sites.** For callers/callees questions, add the matching call-site chunks.
6. **Multi-strategy search.** Run the active engine's search and gather up to a scaled candidate limit.
7. **Pre-MMR floor.** Drop candidates scoring under 5% of the best.
8. **Merge with priority.** Anchors, then call sites, then everything else.
9. **Graph expansion.** Add neighbours from the call graph.
10. **Selection.** Choose the final chunks under the token budget with the engine's selector (query-aware MMR for ETIYA, structural MMR for EHNAS, or a greedy pick if ETIYA lost its vectors).
11. **Hydration.** Replace signatures with real source where the code budget allows.
12. **Assembly.** Build the `ContextWindow`.

A `DebugTrace` records what each step did. When debugging is on, the context is also written to a file.

### Candidate limits

The base is `top_k_candidates` (150), scaled by intent and by repository size. Callers and callees questions with high specificity use about 55% of the base. Concept and flow questions with low specificity use 165%. Other low-specificity questions use 135% to 200%. Repositories with more than 50,000 chunks scale the limit by 1.5, over 100,000 by 2.5, and over 500,000 by 4, capped at the larger of 1,000 and four times the base.

### Main Components

| Area | Files |
| --- | --- |
| Builder and engine switch | `builder.go`, `engine.go`, `types.go` |
| Intent and budget | `intent.go` |
| Search | `search.go`, `bm25.go`, `prf.go`, `anchors.go`, `gate.go`, `kb.go`, `boilerPlate.go`, `subsystem.go` |
| EHNAS | `ehnas_search.go`, `twins.go`, `walk_graph.go`, `mmr_ehnas.go` |
| ETIYA | `vector_ivf.go`, `mmr_etiya.go` |
| Expansion and output | `graph.go`, `source.go` |
| Loading | `loader.go`, `mmap/` |

### Data In, Data Out

In: a question string. Out: a `ContextWindow` with the chosen chunks, total tokens and source files. Behind it: the files in `.eulix/` ([Overview](../../Overview.md)).

## Key Algorithms

The central idea is **fusion with protection**. Many cheap scorers each propose chunks. Their scores are merged (taking the maximum and adding a bonus for being found by more than one scorer). Then position-based adjustments, not content-based ones, push weak candidates down: test files, other modules, and unrelated directories are demoted. Explicit exact matches are exempt from demotion. Finally, a diversity-aware selector picks the set, so the window is not filled with near-copies. [Search Pipeline](Search-Pipeline.md) and [Selection And Hydration](Selection-And-Hydration.md) explain each step.

## Configuration

All settings are in `[retrievalConfig]`; see [Config](../../Config.md). The two that change the most are `project.engine` and `top_k_candidates`.

## Usage Examples

The package is used through the router; to see what it did for a question, enable `DebugConfig` and read `.eulix/debug/context_debug.log` and the written context file.

## Performance Notes

Loading dominates cost for large repositories, and the cost is paid once per process. The README's design aims at sub-300 ms retrieval for large codebases at about 1.5 times the source size in memory; those figures come from the author's notes, and I did not re-measure them.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The retrieval README and this folder describe the code at different levels. | The README is a design document for EHNAS. | Read both. |
| Question intent here is separate from the router's classifier type. | Two layers with different purposes. | See [Classifier](../Classifier.md). |
| Loads the whole index in memory. | Needed for fast search. | Use a smaller project or `.euignore`. |

## Related Docs

- [Engines](Engines.md), [Search Pipeline](Search-Pipeline.md), [Selection And Hydration](Selection-And-Hydration.md).
- [query Overview](../Overview.md): the router that calls this.
- [MCP Overview](../../mcp/Overview.md): another caller.
