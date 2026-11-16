# Retrieval Engines: ETIYA and EHNAS

Eulix has two retrieval engines that share most of their code and differ in how they find code that has no words in common with the question.

> **Crate / path:** `eulix-cli/internal/query/retrieval/engine.go`  
> **Language:** Go  
> **Status:** both engines beta; ETIYA is the default  
> **Last verified against:** `ad744e9`

## Overview

You choose an engine with `engine = "etiya"` or `engine = "ehnas"` under `[project]` in `eulix.toml`. Both read the same chunk store and run the same exact-match, partial-match, keyword and call-graph strategies. They differ in what else they use.

| | ETIYA | EHNAS |
| --- | --- | --- |
| Full name | Embedding-aware Topological Indexing for Your Architecture | Exact · Hierarchical · Node · Structure |
| Reaches unrelated words by | Vector similarity (embeddings) | Twin search, a Monte Carlo walk over the code graph, and novelty packing |
| Needs `embeddings.bin` and `vectors.bin` | Yes | No |
| Calls the embedding helper per question | Yes (except callers/callees) | No outbound calls at all |
| Final selector | Query-aware MMR | Structural MMR |
| Scores recomputable from source metadata | Not fully (vector term) | Yes in the core path |

## Why It Exists

Vector search is the usual way to bridge a question and code that share no words. It is also opaque: the reason a chunk ranks high is a geometric fact about a model's internals. EHNAS was built to answer the same need deterministically, so that "why was this retrieved?" always has a recomputable answer, and so that nothing needs to run a model at query time. ETIYA stays the default because it is what existing projects were built and tuned with.

## How It Works

### Choosing

`ResolveEngine` reads `project.engine`:

- empty or absent means ETIYA, so older configuration files behave as before;
- `etiya` and `ehnas` are accepted, in any letter case;
- anything else is an **error**. Silently running the wrong engine would return plausible results from the wrong pipeline, which is much harder to debug than a refusal to start.

`InitEngine` resolves this once, when the builder is created, and also applies the per-engine setting overrides from `[retrievalConfig.etiya]` or `[retrievalConfig.ehnas]` ([Config](../../Config.md)).

### One switch, two halves

The code is organized so that the choice appears in exactly two places.

**Load time** (`loadEngineArtifacts`). Both engines first load the chunks and the call graph. Then ETIYA loads the vectors (`loadEtiyaArtifacts`): a missing `embeddings.bin` degrades it to keyword-only retrieval and logs that loudly, because a silent degrade looks exactly like a quality regression. EHNAS builds its own structures from data already in memory (`loadEhnasArtifacts`): the twin index, eagerly, and the walk layers, lazily.

**Query time** (`runSearch`, `SelectorFor`). `runSearch` calls either the ETIYA multi-strategy search or `EhnasSearch`. The selector is structural MMR for EHNAS, query-aware MMR for ETIYA when vectors exist, and a plain greedy pick when ETIYA runs without vectors.

Code that wonders "do we have embeddings?" should instead ask which engine is running, via `Engine().UsesSemantic()`. Whether embeddings loaded is an outcome, not a mode.

### Main Components

| Item | Responsibility |
| --- | --- |
| `Engine`, `ResolveEngine`, `InitEngine` | Selection and set-up. |
| `loadEngineArtifacts` | Load-time half of the switch. |
| `runSearch`, `SelectorFor` | Query-time half. |
| `UsesSemantic` | Whether the engine uses vectors. |

### Data In, Data Out

In: `project.engine`. Out: a running builder that behaves as the chosen engine.

## Key Algorithms

The algorithms that differ are in [Search Pipeline](Search-Pipeline.md) (ETIYA's semantic strategy and query-aware MMR) and [Ehnas Augmentation](Ehnas-Augmentation.md) (twin search, walk, novelty packing, structural MMR).

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `project.engine` | `eulix.toml` | `etiya` | Pick the engine. Use `ehnas` for huge repositories, no network or embedder, or reproducible scoring. |
| `retrievalConfig.etiya.*`, `retrievalConfig.ehnas.*` | `eulix.toml` | inherit shared | Per-engine overrides. |

## Usage Examples

```toml
[project]
engine = "ehnas"
```

Expected result: `chat` and `mcp` start without launching the embedding helper, and the debug log shows `[EHNAS]` lines.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Switching engine after `analyze` does not remove the vectors. | `analyze` always builds embeddings. | None needed; EHNAS just ignores them. |
| ETIYA silently degrades if vectors are missing. | A load failure falls back to lexical-only. | Check the debug log for `[ETIYA] ... degrading`. |
| MCP's text-search exclusions apply only with ETIYA. | The server reads that setting only then. | Use `.euignore`. |

## Related Docs

- [Overview](Overview.md): the pipeline.
- [Ehnas Augmentation](Ehnas-Augmentation.md): the EHNAS layers.
- [Embeddings](../../Embeddings.md): the helper only ETIYA starts.
- [Index Loading](Index-Loading.md): what each engine loads.
