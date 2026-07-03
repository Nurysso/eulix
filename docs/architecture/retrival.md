# Retrieval Pipeline

## Overview

Eulix’s retrieval pipeline transforms a natural‑language question into a **compact, precise context window** that the LLM can use to answer accurately. The pipeline is engineered for **scale**, **speed**, and **relevance**, combining multiple retrieval strategies and a **Maximal Marginal Relevance (MMR)** selection layer.

The core component is the **`ContextBuilder`**, which orchestrates the entire retrieval flow. It lives in `internal/query/context_builder.go` and its auxiliary files.

---

## Data Sources

The `ContextBuilder` loads and indexes the following artefacts from the `.eulix/` directory:

| File                 | Contents                                        | Usage                                    |
| -------------------- | ----------------------------------------------- | ---------------------------------------- |
| `kb.json`            | Full knowledge base (functions, classes, files) | Lazy hydration of chunk content          |
| `kb_index.json`      | Symbol → file:line mapping                      | Fast exact lookup                        |
| `kb_call_graph.json` | Call graph nodes + edges                        | Call‑site expansion and relation mapping |
| `embeddings.bin`     | Float32 vectors (version 3/4)                   | Semantic similarity                      |
| `vectors.bin`        | Chunk ID → embedding index                      | Vector lookup by ID                      |

**Note:** The builder uses memory‑mapped I/O (`mmap`) and sequential prefetch (`MADV_SEQUENTIAL`) on Unix, and `FILE_FLAG_SEQUENTIAL_ONLY` on Windows, to reduce page faults and I/O overhead.

---

## Retrieval Pipeline Steps

The pipeline is executed inside `buildContextInternal()` and proceeds as follows:

### 1. Intent Classification (Local)

The query is analysed to determine its **intent** (exact symbol, callers/callees, conceptual, flow, debug). This affects later weights and expansion rules.
See `classifyQueryIntent()` in `context_intent.go`.

### 2. Budget Allocation

A token budget is calculated from the LLM’s `MaxTokens`, reserving space for system prompt, query, response, and safety margins.
The context budget is typically ~85% of remaining tokens.
Weights for each retrieval strategy (`kb_exact`, `keyword`, `semantic`, `graph`) are adjusted based on intent – e.g., `IntentCallers` favours `kb_exact` + `keyword` over `semantic`.

### 3. Multi‑Strategy Search (`multiStrategySearch`)

This is the core retrieval orchestrator. It runs several strategies in parallel (or sequentially) and merges results. Each strategy returns a list of `ScoredChunk` with a **score**.

#### Strategies (in order of priority)

| Strategy             | Method                                                                     | Score Boost               | When Used                                             |
| -------------------- | -------------------------------------------------------------------------- | ------------------------- | ----------------------------------------------------- |
| **Explicit Anchors** | Parses file:line, function names, path fragments from the query            | 120–200                   | Always                                                |
| **`kb_exact`**       | Direct lookup in `kb_index.json`                                           | 115–120                   | If KB available                                       |
| **`exact`**          | Case‑insensitive name match in `chunks`                                    | 90–100                    | Always                                                |
| **`partial`**        | Splits identifiers into tokens (e.g., `applyBudget` → `apply`, `budget`)   | 12–15 per token           | Always                                                |
| **`keyword`**        | Inverted index (BM25) or linear scan over metadata/content                 | Variable (term frequency) | Always                                                |
| **`semantic`**       | Cosine similarity against query embedding (IVF accelerated if >50k chunks) | Scaled by 20              | Unless intent is callers/callees or specificity >0.85 |
| **`callsite`**       | (Special) Scans source for `symbol(` patterns                              | 95                        | For callers/callees intents                           |

The results are merged with priority: anchors first, then exact matches, then others. Scores are combined and boosted.

**Path‑based Boosting:** After merging, chunks are boosted if their file path contains words from the query (exact or partial). This improves localisation.

**Test/Noise Penalty:** Chunks under `/tests/`, `test_`, `fake_`, or `_test.go` are penalised for conceptual/flow queries.

### 4. Call‑Graph Expansion (`buildContextWithGraph`)

If a call graph is available, the top‑N candidates are expanded by following **calls** (outbound) and **called_by** (inbound) edges.

- For `IntentCallers`, inbound edges are boosted (×1.2).
- For `IntentCallees`, outbound edges are boosted (×1.2).
- For other intents, expansion is limited (×0.6 for distance 1, ×0.9 for cross‑edges).

Only up to **15 expansions** are performed to keep the search bounded.

### 5. MMR Selection (`mmrSelect`)

**Maximal Marginal Relevance** balances **relevance** to the query and **diversity** among selected chunks.

- **Relevance** is measured by cosine similarity to the query embedding, or falls back to the candidate’s score.
- **Redundancy** is measured by symbol overlap or, if embeddings exist, cosine similarity between chunk vectors. Nearby lines in the same file are penalised less.
- The MMR formula:
  \[
  MMR = \lambda \cdot \text{relevance} - (1-\lambda) \cdot \max(\text{redundancy})
  \]
  where \(\lambda = 0.7\) (configurable).

Chunks are selected until the token budget is exhausted. If two chunks from the same file are within **5 lines**, they are merged into one larger chunk to reduce overhead.

### 6. Source Hydration (`hydrateSourceCode`)

Selected chunks are enriched with **actual source code** from the filesystem:

- The source root (`sourceRoot`) must be configured.
- A **budget** (65% of context budget) is allocated for source code.
- For each chunk, we read the source file from disk (mmap on Unix, standard read on Windows), strip comments and docstrings, and truncate to a sensible number of lines (max‑lines config, reduced for later chunks).
- Code is wrapped in a markdown code block with language tag.

### 7. Context Assembly

The final `types.ContextWindow` contains:

- A list of `ContextChunk`s (file, start/end lines, content, importance).
- Total tokens.
- List of unique source files.

---

## Performance Optimisations

- **Memory‑mapped I/O** for large JSON files (`kb.json`, `kb_index.json`, `call_graph.json`).
- **Lazy content hydration** – for large corpora (>50k chunks), content is stored empty and hydrated only for selected chunks.
- **IVF index** for embeddings >50k vectors – reduces search space from O(N) to O(k · Nprobe).
- **Parallel chunking** during load (Go’s `sync` primitives) – used in `streamKBChunks`.
- **Boilerplate detection** – symbols that appear in >30% of chunks (adaptive threshold) are excluded from symbol indices to reduce noise.

---

## Customisation

Key parameters (all in `context_builder.go`):

| Parameter              | Default | Description                                                 |
| ---------------------- | ------- | ----------------------------------------------------------- |
| `mmrLambda`            | 0.7     | Relevance‑vs‑diversity trade‑off                            |
| `distantLineThreshold` | 150     | Lines beyond which same‑file chunks are considered distinct |
| `ivfNClusters`         | 256     | Number of clusters for IVF                                  |
| `ivfNProbe`            | 32      | Number of clusters to probe during IVF search               |
| `lazyContentLimit`     | 50000   | Enable lazy hydration above this chunk count                |
| `bpMinChunks`          | 50      | Minimum chunks to enable boilerplate filtering              |

---

# Query Classification

## Overview

Eulix’s classifier (`Classifier` in `internal/query/classifier.go`) analyses the user’s question and maps it to one of **~20 query types**. This determines which handler is invoked and whether the LLM is needed.

The classifier operates in **three progressive levels**:

1. **Level 1 (Pattern Matching)** – fast regex‑based checks with high confidence.
2. **Level 2 (Symbol Analysis)** – extract symbols and infer intent from surrounding keywords.
3. **Level 3 (Keyword Analysis)** – fallback using keyword lists.

All classification logic resides in `classifier_core.go`, `classifier_patterns.go`, and `classifier_utils.go`.

---

## Query Types

| Type             | Description                       | Handler                | LLM?                 |
| ---------------- | --------------------------------- | ---------------------- | -------------------- |
| `Location`       | “Where is X?”                     | `handleLocation`       | ❌                   |
| `Usage`          | “Who calls X?” / “How is X used?” | `handleUsage`          | ❌                   |
| `Understanding`  | “How does X work?” / “Explain X”  | `handleUnderstanding`  | ✅                   |
| `Implementation` | “How is X implemented?”           | `handleImplementation` | ✅                   |
| `Architecture`   | “What is the architecture?”       | `handleArchitecture`   | ✅                   |
| `Debug`          | “Why is this failing?”            | `handleDebug`          | ✅                   |
| `Comparison`     | “Compare X and Y”                 | `handleComparison`     | ✅                   |
| `Dependency`     | “What does X depend on?”          | `handleDependency`     | ❌                   |
| `Refactoring`    | “How to refactor X?”              | `handleRefactoring`    | ✅                   |
| `Performance`    | “What is the hot path?”           | `handlePerformance`    | ✅                   |
| `DataFlow`       | “How does data flow?”             | `handleDataFlow`       | ✅                   |
| `Security`       | “Are there security issues?”      | `handleSecurity`       | ✅                   |
| `Documentation`  | “Generate docs for X”             | `handleDocumentation`  | ✅                   |
| `Example`        | “Show usage example”              | `handleExample`        | ✅                   |
| `Testing`        | “How to test X?”                  | `handleTesting`        | ✅                   |
| `CodeGeneration` | “Generate code”                   | `handleCodeGeneration` | ❌ (returns refusal) |
| `CallGraph`      | “Show call graph”                 | `handleCallGraph`      | ❌                   |
| `EntryPoints`    | “What are entry points?”          | `handleEntryPoints`    | ❌                   |
| `FileStructure`  | “What’s in this file?”            | `handleFileStructure`  | ❌                   |
| `Todos`          | “Show TODOs”                      | `handleTodosQuery`     | ❌                   |
| `Metrics`        | “Show complexity metrics”         | `handleMetrics`        | ❌                   |

---

## Classification Pipeline

### Level 1: Pattern Matching (Fast Path)

Regex patterns are compiled in `QuerySheriff` (see `classifier_patterns.go`). They are checked **in priority order** – more specific patterns first.

**Key patterns (excerpt):**

```go
usagePrefixPattern: regexp.MustCompile(`(?i)^(usage|use|uses\s+of|show\s+usage|find\s+usage|usage\s+of)\s+\S+`)
callGraphPattern:   regexp.MustCompile(`(?i)\b(call\s+graph|call\s+tree|who\s+calls|calls?\s+chain|callers?\s+of|callees?\s+of|call\s+hierarchy)\b`)
securityPattern:    regexp.MustCompile(`(?i)\b(security|vulnerable|sanitize|validation|injection|xss|csrf|authentication|authorization)\b`)
```

If a pattern matches **and** the confidence is ≥0.95, the classification is returned immediately.

### Level 2: Symbol Analysis

If Level 1 yields nothing, the classifier extracts symbols (using `extractSymbols`) and validates them against the symbol index loaded from `kb_index.json`.

- **Multiple symbols + comparison keywords** → `Comparison`
- **Single symbol + location keywords** → `Location`
- **Single symbol + usage keywords** → `Usage`
- **Multiple symbols (no keywords)** → `Understanding`

### Level 3: Keyword Analysis (Fallback)

The classifier looks for sets of keywords:

| Keyword Set                       | Type             |
| --------------------------------- | ---------------- |
| `debug, error, crash, bug`        | `Debug`          |
| `performance, slow, optimize`     | `Performance`    |
| `refactor, clean up, restructure` | `Refactoring`    |
| `test, unit test, mock`           | `Testing`        |
| `implement, add, create`          | `Implementation` |
| `architecture, structure, design` | `Architecture`   |

If nothing matches, the default is `Understanding` with confidence 0.75.

---

## Symbol Extraction

Symbols are extracted using a regex that matches identifiers (CamelCase, snake_case, etc.). Common English words are filtered out via `isCommonWord`.

### Symbol Validation

Symbols are cross‑checked against the in‑memory index built from `kb_index.json`. Only valid symbols are passed to Level 2.

---

## Confidence & Priority

Each classification carries:

- **Confidence** (0–1) – how certain the classifier is.
- **Priority** (1–5) – higher means more urgent (e.g., Debug = 1, Location = 5).

The router uses these to decide whether to build a context window and which handler to call.

---

## Router Integration

The `Router` (in `router_core.go`) dispatches based on `Classification.Type`:

- **Lightweight handlers** (Location, Usage, CallGraph, Metrics, etc.) run entirely from the KB index and call graph – **no LLM**.
- **Heavy handlers** (Understanding, Implementation, etc.) invoke the `ContextBuilder` to retrieve context, then build a chain‑of‑thought prompt and call the LLM.

---

## Adding a New Query Type

1. Add a constant to `QueryType` in `classifier_patterns.go`.
2. Extend the `String()` method.
3. Add a regex pattern in `QuerySheriff` (Level 1) or extend Level 2/3 logic.
4. Implement a handler method in `router_handlers.go`.
5. Add a case to `QueryEngine` in `router_core.go`.

---

## Performance

- Pattern matching is **microsecond** scale.
- Symbol extraction + validation is **O(n)** (n ≈ number of query tokens).
- The classifier is **stateless** – no network I/O.
- All regexes are compiled once at startup.

---

## Known Limitations

- **Misclassification** – generic keywords like “authentication” can trigger `Security` instead of `Understanding`.
  - **Workaround:** Add more specific patterns and reorder checks.
- **Limited to English** – all patterns are English‑centric.
- **No multi‑intent detection** – a single query can only have one type.

---

_Last updated: June 2026_
