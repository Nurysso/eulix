# Retrieval Selection And Hydration

This page covers the second half of retrieval: widening the candidates with the call graph, choosing the final chunks, and turning signatures into real source code.

> **Crate / path:** `eulix-cli/internal/query/retrieval/` (`graph.go`, `mmr_etiya.go`, `mmr_ehnas.go`, `source.go`)  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

After the [search pipeline](Search-Pipeline.md), there is a ranked list of candidates, usually far more than will fit. Three jobs remain. **Expansion** adds code that is connected to the best candidates through calls. **Selection** picks the final set within the token budget while avoiding redundancy. **Hydration** replaces the compact signature entries with real source code, as far as the budget allows.

## Why It Exists

The best match is often not enough on its own: to understand a function you often need what it calls and what calls it. But adding neighbours blindly would flood the window. And the top-ranked chunks tend to be near-copies of one another. Selection trades relevance against redundancy, and hydration spends the remaining budget where it helps most.

## How It Works

### Call-graph expansion

`buildContextWithGraph` takes the 20 best candidates and, for each symbol they contain, looks up the call graph (edges of type `call`, stored in both directions as `calls` and `called_by`). Each relationship is followed with these rules:

- the neighbour's score is derived from the candidate's: ×1.2 when it is the direction the question asked about (callers for a "who calls" question), ×0.9 for other calls, ×0.6 for other close relationships;
- at most 15 relationships are followed in total (`max_graph_expansions`), and at most 5 new chunks are taken per relationship, so one widely referenced function cannot flood the result;
- with `max_graph_expansion_depth` of 2 or more, neighbours' neighbours are added with a further ×0.6 discount;
- if the neighbour is already a candidate, only its score and provenance are raised; its match details are never overwritten.

If the depth is 0 or the call graph did not load, expansion is skipped.

### Selection: query-aware MMR (ETIYA)

Maximal Marginal Relevance (MMR) picks items one at a time, each time choosing the candidate with the best balance of relevance and *novelty against what is already chosen*. `mmrSelect` works like this:

- **Relevance** is the normalized fused score, blended with the cosine similarity of the chunk to the question (a weight of 0.20) for *every* candidate, not only those found by the semantic strategy. Chunks from files the question named get a +0.15 bonus.
- **Redundancy** between two chunks is their embedding similarity. For two chunks in the same file that are highly similar but far apart (more than 150 lines), the similarity is raised by 20%, because they probably repeat each other. Without embeddings it falls back to the overlap of non-boilerplate symbols.
- **The balance** is the diversity factor, `mmr_diversity_factor`.
- Chunks overlapping a pinned chunk are penalized less (×0.55), since related context to an explicit request is wanted.
- The path gate is applied first, so out-of-scope files never enter the loop.
- Chunks that fit are added to the list, merging adjacent chunks of the same file; selection stops at the token budget or `max_context_chunks`.

### Selection: structural MMR (EHNAS)

Explained in [Ehnas Augmentation](Ehnas-Augmentation.md): the same idea without a question vector.

### Selection: greedy fallback

When ETIYA has no vectors, chunks are taken in score order, merging neighbours, until the budget is spent.

### Hydration

The selected chunks so far carry compact text (signatures, docstrings, call information). `hydrateSourceCode` swaps that for real source:

1. **Budget.** The *code budget* is the context budget times `code_to_ast_ratio` (0.85 by default). The rest is left for compact entries.
2. **Read the files.** Source is read through a rooted file handle, so a path in the index cannot reach outside the project, with up to 8 files prefetched in parallel.
3. **Build an item per chunk.** Each item works out what it would cost: cleaned source (comments stripped by [Strip](../Strip.md)), or a **skeleton** for a container such as a class or file. A skeleton shows its first 40 lines with the bodies of child chunks replaced by one-line pointers like `// lines 40-88 (separate chunk)`. Nested children always skeletonize their parent so bodies are not shown twice, while leaf functions and methods hydrate in full when the budget allows.
4. **Allocate** (`allocateGrants`). Tokens are shared among the items in proportion to their weight, where the weight decays gently with rank (`boost ÷ (1 + 0.04 × rank)`). In rounds: an item whose share covers its full need gets exactly that and returns the surplus; if no item is covered, the lowest-weight item whose share falls below a minimum useful size (120 tokens, or 35% of its need) is dropped back to its compact form; otherwise everyone settles for their share. Every round either settles or drops at least one item, so it always ends.
5. **Top up and enforce a ceiling.** Leftover budget is spent topping items up. Then the whole context, source plus the remaining compact entries, is held to the budget by evicting the coldest unhydrated chunks.
6. **Assemble.** Hydrated items become source chunks, others stay as compact entries, and pointers to evicted children are rewritten. Input order is preserved.

### Main Components

| Item | Responsibility |
| --- | --- |
| `buildContextWithGraph`, `buildContextWithoutGraph` | Expansion. |
| `mmrSelect`, `mmrSim` | ETIYA selection. |
| `structuralMMR` | EHNAS selection. |
| `selectChunks` | Greedy fallback. |
| `hydrateSourceCode` and helpers | Source hydration. |

### Data In, Data Out

In: ranked candidates and the token budget. Out: a list of chunks, some with real source (no `[METADATA]` prefix), some compact, which become the `ContextWindow`.

## Key Algorithms

### Fair sharing under a budget

**The problem.** There are more hydratable chunks than tokens. Giving the first chunk everything would starve the rest; splitting equally would waste tokens on chunks that need little.

**The approach.** The grant algorithm gives everyone a share by weight, finishes any chunk whose share already covers its need (returning the surplus to the pool), and drops chunks that cannot get a useful amount.

**Why this way.** Small functions get complete source, large ones get what fits, and no chunk is shown as a useless sliver. The weight decays slowly with rank on purpose, so lower-ranked chunks are not starved.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `code_to_ast_ratio` | `[retrievalConfig]` | 0.85 | Share of the budget for real code. Lower it to see more structure. |
| `max_graph_expansion_depth` | `[retrievalConfig]` | 1 | Hops in the call graph; 0 disables expansion. |
| `max_graph_expansions` | `[retrievalConfig]` | 15 | Relationships followed. |
| `mmr_diversity_factor` | `[retrievalConfig]` | 0.85 | Relevance versus variety. |
| `max_context_chunks` | `[retrievalConfig]` | 30 | Most chunks in the context. |
| `Max_Lines` | `[project]` | 100 | The longest span of a file or class chunk shown in full before it becomes a skeleton. |

## Usage Examples

Lower `code_to_ast_ratio` to 0.5 for an architectural question where signatures of many functions matter more than a few full bodies.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Hydration needs the source files on disk. | It reads them at query time. | Keep the repository at `project.path`. |
| A file changed since analysis can hydrate lines that no longer match the index. | Line ranges come from the analysis. | Re-run `eulix analyze`. |
| Expansion is capped, so deep dependency chains need depth 2 or EHNAS's walk. | By design. | Raise the depth or use `ehnas`. |
| Skeletons show only the first 40 lines of a container. | A fixed cap. | None. |

## Related Docs

- [Search Pipeline](Search-Pipeline.md): the candidates.
- [Ehnas Augmentation](Ehnas-Augmentation.md): structural MMR.
- [Strip](../Strip.md): comment removal.
- [Prompts](../Prompts.md): what happens to the finished context.
