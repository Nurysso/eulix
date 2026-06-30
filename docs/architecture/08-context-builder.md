# Eulix Context Window Builder — Architecture

## 1. Purpose

`internal/query` builds the LLM context window that eulix hands to the model. Given a natural-language query, it returns a token-budgeted slice of code, signatures, and documentation that is maximally likely to answer the question.

The package is designed for two regimes:

- **Small corpus** (≤ 50k chunks, < 1 GB on disk): eager-load everything, simple in-memory structures, predictable latency.
- **Large corpus** (10k–1M+ chunks, GB-scale minified JSON): stream-parse, write a content sidecar binary, rely on inverted index + IVF for sub-linear search.

Both regimes share the same public API (`BuildContext`, `BuildContextWithDebug`).

---

## 2. Non-goals

- **No generation.** This package only selects and formats context. The LLM client lives elsewhere.
- **No persistent cache of query results.** The cache layer (`internal/cache`) wraps the _retrieval_ path, not the LLM response.
- **No ingestion.** Building `kb.json` / `embeddings.bin` is a separate pipeline.
- **No re-ranking model.** MMR + lexical overlap is the final stage. A cross-encoder re-ranker is a future extension point (§15.1).

---

## 3. System Overview

```
                    +---------------------+
   query  ───►      |  classifyQueryIntent| ─── intent, weights
                    +----------+----------+
                               │
                               ▼
                    +----------+----------+
                    |  exactSymbolSearch  | ─── anchors (high precision)
                    +----------+----------+
                               │
                               ▼
                    +----------+----------+
                    |  findCallSites      | ─── (callers/callees only)
                    +----------+----------+
                               │
                               ▼
                    +----------+----------+
                    |  multiStrategySearch| ─── 5 strategies in parallel
                    |  • kb_exact         |
                    |  • exact            |
                    |  • partial          |
                    |  • keyword (inv)    |
                    |  • semantic (IVF)   |
                    +----------+----------+
                               │
                               ▼
                    +----------+----------+
                    |  buildContextWith   | ─── optional call-graph expansion
                    |  Graph              |
                    +----------+----------+
                               │
                               ▼
                    +----------+----------+
                    |  mmrSelect          | ─── diversity-aware top-k
                    +----------+----------+
                               │
                               ▼
                    +----------+----------+
                    |  hydrateContent     | ─── sidecar / kbData lookup
                    +----------+----------+
                               │
                               ▼
                    +----------+----------+
                    |  hydrateSourceCode  | ─── read from sourceRoot
                    +----------+----------+
                               │
                               ▼
                    +----------+----------+
                    |  assembleContext    | ─── types.ContextWindow
                    +----------+----------+
```

---

## 4. Data Flow

### 4.1 Cold Start

```
   ContextWindowCreator()
       │
       ├──► loadChunksFromKB()          // streams kb.json
       │       ├── reads kb_index.json       (sonic, small)
       │       ├── walks "structure" map     (json.Decoder, per-file)
       │       ├── decodes FileStructure     (sonic, per-file)
       │       ├── builds Chunk slice
       │       └── (if lazy) writes kb_content.bin sidecar
       │
       ├──► loadEmbeddings()            // binary, zero-copy
       ├──► loadVectorMap()             // binary, id → emb idx
       ├──► loadCallGraph()             // sonic
       ├──► loadKnowledgeBase()         // thin pass-through
       └──► BuildBoilerplateDetector()  // corpus-frequency analysis
```

The streaming pass uses `json.Decoder.Token()` to skip to the `structure` key, then `dec.Decode(&raw)` to pull each `FileStructure` as a `json.RawMessage`, then `sonic.ConfigDefault.Unmarshal` to decode that slice. The raw bytes are released before the next file is read, keeping GB-scale files tractable.

`BuildBoilerplateDetector` runs a single pass over the loaded chunk slice and populates `cb.boilerplate`. It must be called after `loadChunksFromKB` and before the first query is served.

### 4.2 Hot Path (One Query)

```
   BuildContext(query)
       │
       ├── classify(query) → intent
       ├── allocateBudget(query, intent) → BudgetAllocation
       ├── exactSymbolSearch(query) → anchors
       ├── (if callers/callees) findCallSites(query, intent) → callSiteResults
       ├── multiStrategySearch(query, limit, intent, trace)
       │     └── 5 strategies merged into a single ScoredChunk list
       │           (boilerplate symbols filtered during keyword/semantic scoring)
       ├── mergeWithPriority(anchors, callSiteResults, candidates)
       ├── (if call graph) buildContextWithGraph(...)
       ├── (if embeddings) mmrSelect(..., qEmb, anchorFiles, trace)
       │     else         selectChunks(..., greedy)
       ├── (if lazy) hydrateContent(selected)   // sidecar or kbData
       ├── hydrateSourceCode(selected, sourceBudget, maxLines)
       └── assembleContext(selected) → types.ContextWindow
```

Every step is synchronous in the current implementation. The only concurrent access is the `sync.RWMutex` guarding `InvertedIndex.Postings` during writes at startup and reads at query time.

---

## 5. Concurrency Model

The `ContextBuilder` is safe to share across goroutines for **reads only** after `ContextWindowCreator` returns. The mutexes protect:

| Field                  | Access                                   | Lock           |
| ---------------------- | ---------------------------------------- | -------------- |
| `invertedIdx.Postings` | Init + query                             | `sync.RWMutex` |
| `lastTrace`            | Write per query, read via `GetLastTrace` | `sync.Mutex`   |
| `debugLog.file`        | Write per log line                       | `sync.Mutex`   |

All other fields — including `boilerplate` — are written once at construction and read-only thereafter. `cb.boilerplate` is set by `BuildBoilerplateDetector` before query serving begins, so `isBoilerplateSymbol` requires no locking.

---

## 6. Data Structures and Indices

### 6.1 `Chunk`

The atomic unit of retrieval. A chunk corresponds to one function, class, or method from the KB.

```
Chunk {
  ID          string   // "file.go:10-42" — stable across rebuilds
  ChunkType   string   // "function" | "class" | "method"
  File        string   // relative path, e.g. "internal/query/context.go"
  StartLine   int      // 1-indexed
  EndLine     int      // 1-indexed, inclusive
  Content     string   // empty when lazyContent is true
  Tokens      int      // rough estimate: len(content) / 4
  Symbols     []string // [Name, ...resolved callee names]
  Name        string   // unqualified identifier, e.g. "mmrSelect"
  Importance  float64  // 0.0–1.0, used for tie-breaking
}
```

`Content` is the formatted representation returned to the LLM. In lazy mode it is empty until `hydrateContent` fills it from `kbData` or `kb_content.bin`.

### 6.2 `BoilerplateDetector`

Built once at startup from the full chunk slice. Its purpose is to identify symbols so common across the corpus that they provide no discriminating signal — and to suppress them from similarity comparisons and ranking.

```
BoilerplateDetector {
  boilerplate  map[string]bool  // normalized symbols that exceeded dfThreshold
  df           map[string]int   // raw document-frequency counts for debugging
  totalChunks  int
}
```

**Construction** (`NewBoilerplateDetector`):

1. For every chunk, iterate `chunk.Symbols`.
2. Normalize each symbol via `normalizeSymbol`: strip punctuation (`* & , ; ( ) [ ] { }`), lowercase, drop tokens of length ≤ 1 (loop variables, bare operators, single-letter type parameters).
3. Track distinct chunks per symbol using a per-chunk `seen` set to avoid double-counting the same symbol within one chunk.
4. After all chunks are processed, apply the threshold: a symbol is boilerplate if its document frequency ≥ `ceil(dfThreshold × totalChunks)`.
5. If `totalChunks < minChunks` (default 50), return an empty detector — the corpus is too small for reliable frequency statistics.

**Threshold guide:**

| `dfThreshold` | Character          | Effect                                                                         |
| ------------- | ------------------ | ------------------------------------------------------------------------------ |
| 0.20          | Aggressive         | Filters common helpers, short parameter names, widely-shared utility functions |
| 0.30          | Balanced (default) | Removes genuinely ubiquitous tokens while preserving domain-specific symbols   |
| 0.50          | Conservative       | Only truly universal tokens (e.g. `error`, `ctx`, `err`) are filtered          |

**Integration** — `BuildBoilerplateDetector` is called on the `ContextBuilder` after chunk loading:

```go
func (cb *ContextBuilder) BuildBoilerplateDetector(dfThreshold float64) {
    cb.boilerplate = NewBoilerplateDetector(cb.chunks, dfThreshold, 50)
    cb.debugLog.Log("Boilerplate detector built: %d symbols filtered (top: %v)",
        len(cb.boilerplate.boilerplate),
        cb.boilerplate.TopBoilerplate(5))
}
```

`isBoilerplateSymbol` is then used wherever symbols from `chunk.Symbols` are compared or scored — most notably inside `simBetween` (the chunk-to-chunk similarity function used by MMR) and within the inverted keyword index scoring path.

**Debugging** — `TopBoilerplate(n)` returns the n most frequent boilerplate symbols with their raw counts:

```
ctx (12400/14200)
err (11900/14200)
error (9800/14200)
...
```

### 6.3 `invertedIdx` — TF-IDF Keyword Index

Built once at startup when `len(chunks) > invIdxThreshold` (5,000). Maps lowercase terms to sorted posting lists:

```
InvertedIndex {
  Postings map[string][]Posting
  DocCount int          // total chunk count for IDF calculation
}

Posting { ChunkIdx, TF }  // TF is normalised term frequency within the document
```

Query flow:

1. Extract query keywords and potential symbol names.
2. For each term, collect IDF-weighted scores across matching posting lists.
3. Add +20.0 bonus for exact name match.
4. Filter boilerplate symbols before scoring (they are not indexed or are down-weighted).
5. Sort by score, return top-K.

The inverted index tokenises via `extractQueryKeywords`: lowercases, splits on non-alphanumeric, drops stop-words, and splits on `_` and camelCase boundaries. Callee names are also indexed with weight +2 so "where does X appear" queries land on call sites even when the body isn't materialised.

### 6.4 `symbolIndex` — Name → Chunk Indices

Built at startup alongside the chunk list. Used by `buildContextWithGraph` to map relationship targets back to chunk objects without an O(n) scan.

```
symbolIndex map[string][]int  // lowercase symbol → sorted chunk indices
```

### 6.5 `ivfIndex` — Approximate Nearest Neighbours

Built when `len(embeddings) > ivfBuildThreshold` (10,000). k-means clustering with `ivfNClusters = 256` cells. Query scans `ivfNProbe = 8` cells (3.1% of the space). k-means runs for `ivfKMeansIter = 20` iterations with a fixed seed (42) for reproducibility.

```
IVFIndex {
  Centroids [][]float32  // [256][dim] cluster centres
  Lists     [][]int32    // [256] → embedding indices in that cell
  NClusters int          // 256
  Dim       int          // embedding dimension
}
```

Without IVF, `vectorSearch` does a brute-force O(n) scan. With IVF it is O(n × 3%).

### 6.6 `callSites` — Identifier-at-`(` Scan

Built from `chunk.Content` (not the raw file) by scanning for `(` and collecting the preceding identifier. This is the primary signal for callers/callees intent. O(total_content_length), built once at startup.

```
callSiteIndex map[string][]int  // lowercase symbol → chunk indices that call it
```

### 6.7 `kb_content.bin` Sidecar

Written by the streaming loader when `len(chunks) > lazyContentLimit`. Binary format:

```
[4B "EULC"][4B version=1][4B count]
{ for each chunk:
    [4B idLen][idLen bytes][4B contentLen][contentLen bytes]
}
```

`readSidecar` does a linear scan. For corpora where many chunks are hydrated per query, add a secondary `kb_content_idx.bin` that stores `id → offset` as 8-byte LE integers and seek directly (§15.2).

---

## 7. Intent Classification

```
classifyQueryIntent(query) → QueryIntent

Intent           | When triggered                          | Budget emphasis
-----------------|-----------------------------------------|-------------------
IntentCallers    | "who calls", "call sites"               | kb_exact + keyword
IntentCallees    | "what does X call"                      | kb_exact + keyword
IntentDebug      | "error", "panic", …                     | kb_exact + keyword
IntentFlow       | "trace", "lifecycle", …                 | graph + semantic
IntentConcept    | "how does", "what is", "difference"     | semantic + keyword
IntentSymbolExact| ≥1 code symbol, short query             | kb_exact + exact
IntentSymbolFuzzy| partial symbol match                    | partial + keyword
```

`specificity` is `min(1.0, codeSymbolCount × 0.35)`. `confidence` is a 0–1 float based on keyword density. Both feed into `candidateLimitForIntent` and `allocateBudget`.

---

## 8. Budget Allocation

```
allocateBudget(query, intent) → BudgetAllocation

totalTokens   = config.LLM.MaxTokens
reserved      = 150 (system) + len(query)/4 (query) + 200 (safety) + 2000 (response)
contextBudget = (total - reserved) × 0.85
```

Per-strategy weights in `allocateBudget` are multiplied by intent-specific scalars in `multiStrategySearch` to compute `topK` for each strategy.

---

## 9. MMR Selection

Maximal Marginal Relevance balances relevance (similarity to query) against diversity (dissimilarity to already-selected chunks):

```
MMR = λ × simToQuery(c) − (1−λ) × maxRedundancy

λ = 0.7  (mmrLambda constant)
simToQuery:    cosine similarity to query embedding
               (fallback: score / maxScore when no embedding is available)
maxRedundancy: max cosine similarity to any already-selected chunk
```

Chunk similarity (`simBetween`) uses the chunk's `Symbols` slice. Boilerplate symbols — as identified by `BoilerplateDetector` — are skipped during this comparison so that ubiquitous tokens like `ctx` or `err` do not inflate apparent similarity between chunks that are actually about different things.

```
chunk is selected if: tokenSum + Tokens + headerOverhead ≤ budget
```

When two chunks are from the same file and > 150 lines apart (`distantLineThreshold`), the similarity penalty is multiplied by `simPenaltyFactor = 1.20` to reduce it — distant chunks in the same file are less redundant.

After selection, adjacent chunks within a 5-line gap (`canMerge`) are merged into a single span to reduce header overhead.

---

## 10. Source Hydration

`hydrateSourceCode` reads from `sourceRoot` (not from the KB). It is the last step in the pipeline and consumes 65% of `contextBudget`.

```
sourceBudget = contextBudget × 65 / 100

Progressive line budget per chunk:
  budgetFraction > 50% → full maxLines
  25–50%              → maxLines / 2
  < 25%               → maxLines / 4
  minimum             → 10 lines
```

Comments and docstrings are stripped before counting tokens. The language is inferred from the file extension.

---

## 11. Storage Formats

| File                 | Format           | Size                          | Load strategy                                   |
| -------------------- | ---------------- | ----------------------------- | ----------------------------------------------- |
| `kb_index.json`      | JSON             | Small (< 10 MB)               | `sonic` full decode                             |
| `kb.json`            | JSON             | Up to GB                      | Streaming via `json.Decoder` + `sonic` per-file |
| `kb_content.bin`     | Custom binary    | Proportional to total content | `readSidecar` linear scan                       |
| `embeddings.bin`     | Custom binary LE | `count × (4+dim×4) + header`  | Hand-rolled reader, no allocation for payload   |
| `vectors.bin`        | Custom binary LE | `count × (4+dim×4) + header`  | Hand-rolled reader                              |
| `kb_call_graph.json` | JSON             | Small                         | `sonic` full decode                             |

---

## 12. Error Handling

| Situation                                        | Behaviour                                                                                                       |
| ------------------------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| `kb_index.json` missing                          | Fatal — `loadChunksFromKB` returns error                                                                        |
| `kb.json` missing                                | Fatal — same                                                                                                    |
| `embeddings.bin` missing                         | Non-fatal — `hasEmbeddings = false`, falls back to greedy selection                                             |
| `vectors.bin` missing                            | Non-fatal — `hasEmbeddings = false`, `vectorMap = {}`                                                           |
| `kb_call_graph.json` missing                     | Non-fatal — `hasCallGraph = false`, graph expansion skipped                                                     |
| `BuildBoilerplateDetector` not called            | Non-fatal — `cb.boilerplate == nil`, `isBoilerplateSymbol` returns `false` for all symbols; no filtering occurs |
| Corpus below `minChunks` (50)                    | Non-fatal — `BoilerplateDetector` returns with empty maps; no symbols filtered                                  |
| Source file missing                              | Non-fatal — chunk kept with KB metadata only                                                                    |
| Query embedding fails                            | Non-fatal — `mmrSelect` falls back to score-based `simToQuery`, warning appended to trace                       |
| `sonic` decode fails on a single `FileStructure` | Fatal — the whole `loadChunksFromKB` returns error                                                              |
| Sidecar write fails                              | Non-fatal — falls back to inlining content                                                                      |

---

## 13. Memory Model

| Phase                            | What's allocated                                                                                                                                                                                                                                  |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `loadChunksFromKB` streaming     | `Chunk` slice (pre-sized via estimation), `symbolIndex`, `invertedIdx`, `callSites`, `FileStructure` map for `kbData`                                                                                                                             |
| `BuildBoilerplateDetector`       | `map[string]int` for `df` (one entry per unique normalized symbol), `map[string]bool` for `boilerplate` (subset). Both are retained for the lifetime of the `ContextBuilder` for query-time lookups. Typical size: O(unique symbols × ~80 bytes). |
| `loadEmbeddings`                 | `embeddings [][][]float32` — `numEmb × dim × 4 bytes`                                                                                                                                                                                             |
| Per-query                        | Candidates slice, expanded slice, selected chunk slice, trace structs — all bounded by `candidateLimit` (~80 items)                                                                                                                               |
| Sidecar path (lazy > 50k chunks) | `kbData` holds `map[string]FileStructure` — the structured form, ~3–5× smaller than raw JSON                                                                                                                                                      |
| Sidecar hydration                | `readSidecar` allocates per-chunk `contentBytes` then discards it immediately                                                                                                                                                                     |

Peak memory for a 100k-chunk corpus: roughly `100k × 200 bytes (chunk) + 100k × dim × 4 bytes (embeddings) + 100k × 50 bytes (FileStructure map) + unique_symbols × 80 bytes (boilerplate detector)` ≈ 1.2 GB + embeddings. Streaming keeps the raw JSON out of RAM entirely.

---

## 14. Configuration Knobs

| Constant                    | Value  | Effect                                                                 |
| --------------------------- | ------ | ---------------------------------------------------------------------- |
| `lazyContentLimit`          | 50,000 | Above this: content not inlined, sidecar written                       |
| `invIdxThreshold`           | 5,000  | Above this: inverted keyword index built                               |
| `ivfBuildThreshold`         | 10,000 | Above this: IVF index built instead of brute-force                     |
| `ivfNClusters`              | 256    | k-means cluster count                                                  |
| `ivfNProbe`                 | 8      | IVF cells scanned per query                                            |
| `mmrLambda`                 | 0.7    | Relevance vs. diversity trade-off                                      |
| `distantLineThreshold`      | 150    | Lines between chunks in same file before similarity penalty is reduced |
| `simPenaltyFactor`          | 1.20   | Multiplier applied to similarity when chunks are distant in same file  |
| `maxGraphExpansions`        | 15     | Max relationships traversed during call-graph expansion                |
| `dfThreshold` (boilerplate) | 0.30   | Fraction of chunks a symbol must appear in to be filtered              |
| `minChunks` (boilerplate)   | 50     | Corpus size below which boilerplate detection is disabled              |

---

## 15. Extensibility Points

### 15.1 Cross-Encoder Re-Ranker

`mmrSelect` takes `qEmb []float32` and computes `cosineSimilarity`. Replace with a cross-encoder call (~1ms per candidate) that scores `(query, chunk.Content)` directly. The `ScoredChunk` interface would not need to change; only the `simToQuery` closure inside `mmrSelect`.

### 15.2 Streaming Sidecar with Offset Index

Build `kb_content_idx.bin` alongside `kb_content.bin`:

```
[4B "EUID"][4B version][4B count]
{ for each chunk:
    [4B idLen][idLen bytes id]
    [8B LE offset into kb_content.bin]
    [4B LE length]
}
```

Then `readSidecar` does a binary search on the id slice and a single `Seek` instead of a linear scan.

### 15.3 Parallel Strategy Execution

Replace the sequential `run` calls in `multiStrategySearch` with a `sync.WaitGroup` and channel-based result collection. The `map[string]ScoredChunk` merge is safe under concurrent append because each strategy writes to distinct keys. Watch the `Score` merge logic — it currently takes the max; a parallel version needs to hold the lock during the `ex.Score` check.

### 15.4 Alternative Embedding Store

`embeddings.bin` + `vectors.bin` are Go-side formats. If you move to an external vector DB (Qdrant, Weaviate), implement a new `QueryEmbedder` that wraps the gRPC client and populate `cb.vectorMap` with the external IDs.

### 15.5 Tuning the Boilerplate Threshold

The `dfThreshold` can be tuned per-corpus using `TopBoilerplate(n)` to inspect what the current threshold is filtering. A reasonable workflow:

1. Build with `dfThreshold = 0.30` (default).
2. Call `cb.boilerplate.TopBoilerplate(20)` and inspect the output.
3. If domain-specific symbols like `scheduleEntity` or `runQueue` appear, raise the threshold.
4. If query-discriminating symbols like `ctx` are absent but you're seeing poor diversity in results, lower it.

---

## 16. Performance Characteristics

| Operation                                | Complexity                                     | Typical latency (50k chunks)     |
| ---------------------------------------- | ---------------------------------------------- | -------------------------------- |
| `classifyQueryIntent`                    | O(\|query\|)                                   | < 1 µs                           |
| `BuildBoilerplateDetector`               | O(chunks × avg_symbols)                        | 50–200 ms (one-time, at startup) |
| `exactSymbolSearch`                      | O(chunks × symbols)                            | 2–5 ms                           |
| `findCallSites`                          | O(total_call_sites × symbols)                  | 1–3 ms                           |
| `invertedKeywordSearch`                  | O(unique_terms × avg_posting_size)             | 1–2 ms                           |
| `vectorSearchIVF`                        | O(nClusters × nProbe × avgListSize) ≈ O(0.03n) | 5–15 ms                          |
| `buildContextWithGraph`                  | O(topN × graph_degree × expansions)            | 1–2 ms                           |
| `mmrSelect` (with boilerplate filtering) | O(candidates²)                                 | 2–10 ms                          |
| `hydrateSourceCode`                      | O(selected_chunks × file_reads)                | 10–50 ms (I/O bound)             |
| `assembleContext`                        | O(selected_chunks)                             | < 1 ms                           |

End-to-end p50 on a warm filesystem cache: **40–80 ms** for a typical query. Cold start (filesystem cache cold): dominated by `loadChunksFromKB` streaming + `loadEmbeddings`, **1–5 s** once. `BuildBoilerplateDetector` adds ~50–200 ms to cold start, depending on corpus size and symbol density.

---

## 17. Source File Layout

```
internal/query/
  context.go          // ContextBuilder, BuildContext, ContextWindowCreator
  loader.go           // loadChunksFromKB, loadEmbeddings, loadVectorMap, loadCallGraph
  intent.go           // classifyQueryIntent, allocateBudget
  search.go           // multiStrategySearch, exactSymbolSearch, findCallSites
  mmr.go              // mmrSelect, simBetween
  ivf.go              // IVFIndex, buildIVF, vectorSearchIVF
  inverted.go         // InvertedIndex, invertedKeywordSearch
  hydrate.go          // hydrateContent, hydrateSourceCode, readSidecar
  boilerplate.go      // BoilerplateDetector, NewBoilerplateDetector, normalizeSymbol
  graph.go            // buildContextWithGraph
  assemble.go         // assembleContext → types.ContextWindow
  debug.go            // DebugLogger, QueryTrace
```

---

## 18. Glossary

| Term                    | Definition                                                                                            |
| ----------------------- | ----------------------------------------------------------------------------------------------------- |
| Anchor                  | Exact-symbol match used as a mandatory top result                                                     |
| Boilerplate             | A symbol that appears in ≥ `dfThreshold` fraction of chunks and is suppressed from similarity scoring |
| Candidate               | A chunk that survived at least one strategy                                                           |
| Chunk                   | One function / class / method, the atomic retrieval unit                                              |
| Document frequency (DF) | Number of distinct chunks containing a given symbol                                                   |
| IVF                     | Inverted File Index — approximate nearest-neighbour via k-means clustering                            |
| KB                      | Knowledge Base — `kb.json`, the structured code summary                                               |
| Lazy content            | Content not inlined in chunks; materialised on demand                                                 |
| MMR                     | Maximal Marginal Relevance — relevance + diversity selection                                          |
| Sidecar                 | Secondary binary file (`kb_content.bin`) for lazy content                                             |
| Strategy                | One of five independent search methods (exact, partial, keyword, semantic, kb_exact)                  |
| Symbol normalization    | Lowercasing + punctuation stripping applied before boilerplate detection and similarity scoring       |
