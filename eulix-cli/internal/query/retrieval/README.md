# Architecture Document: Eulix Retrieval Pipeline v2

**Codename: EHNAS** — _Exact · Hierarchical · Node · Structure_

> EHNAS names the deterministic retrieval backbone: **exact** scoring with no semantic inference, **hierarchical** localization through the subsystem tree, **node**-level resolution of chunks and symbols, and **structure**-aware traversal of the dependency graph. Every result's provenance is a deterministic calculation over metadata already extracted from source, so "how did this get retrieved?" is always answerable by recomputation.
>
> At multi-million-LOC scale, three **augmentation layers** extend EHNAS without changing its deterministic core: Monte Carlo walk, twin search, and novelty packing.

Engine selection lives in `eulix.toml` (`engine = "ehnas"` or `engine = "etiya"`). **ETIYA** — _Embedding-aware Topological Indexing for Your Architecture_ — is the embedding-based engine and the default when the key is empty; EHNAS reads no vector artifacts.

---

## 1. Design Philosophy

Classic multi-strategy RAG treats retrieval as a _flat union of independent scorers_: exact match, keyword, vector, merge, sort, truncate. EHNAS keeps that proven skeleton but makes the deterministic path the backbone, and adds three augmentation layers for the failure modes of ultra-large-scale (multi-million LOC) codebases:

| Failure mode at scale                                                  | EHNAS core response                           | Augmentation layer   |
| ---------------------------------------------------------------------- | --------------------------------------------- | -------------------- |
| Long-range cross-file dependencies invisible to single-hop retrieval   | Structure graph + anchor seeds                | **Monte Carlo Walk** |
| The same code in several languages shares no token with the query      | Exact symbol matching stays language-agnostic | **Twin Search**      |
| Context window fills with redundant boilerplate; novelty dies silently | Scope demotion + subsystem boosting           | **Novelty Packing**  |

The insight: a codebase is not a bag of documents. It is a **graph** (symbols, calls, terms), a set of **cross-language equivalents** (the same structure ported between languages), and a **budget-constrained selection problem** (the context window). EHNAS retrieves deterministically through all three; the augmentation layers are how it handles them at scale.

The **Exact / Hierarchical / Node / Structure** split maps onto the pipeline as:

| Letter           | What it guarantees                                                                        |
| ---------------- | ----------------------------------------------------------------------------------------- |
| **Exact**        | Scoring is recomputable; no hidden semantic term in the core path                         |
| **Hierarchical** | Subsystem-tree detection and scope demotion decide relevance before scoring does          |
| **Node**         | Retrieval is chunk/symbol-node addressed, with anchor pins that survive every later stage |
| **Structure**    | The dependency graph (symbol co-occurrence, AST) is first-class, not an afterthought      |

---

## 2. High-Level Architecture

```
                        ┌─────────────────────────────────────────────┐
                        │              EHNAS Pipeline                  │
                        └─────────────────────────────────────────────┘

  query ──► [Stage 0] Symbol Hygiene                                    (core)
                │  extractPotentialSymbols → cleanSymbols (min len 3)
                │  → splitDottedSymbols (whole + parts)
                ▼
  ┌──────────────────────────────────────────────────────────────────┐
  │  STAGE 1 — EXPLICIT LOCALIZATION (deterministic, high precision) │  (core)
  │  extractExplicitAnchors → explicitAnchorSearch → path gate       │
  │  → resolveAnchorPins (5000+bonus; resolved after the MC walk)    │
  └──────────────────────────────────────────────────────────────────┘
                │
                ▼
  ┌──────────────────────────────────────────────────────────────────┐
  │  STAGE 2 — PARALLEL STRATEGY SWARM (run() wrapper)               │
  │                                                                  │
  │  EHNAS core:                                                     │
  │    kb_exact ──┐                                                  │
  │    grep ──────┤  pre-cleaned symbols, per-dimension caps         │
  │    exact ─────┤  name/symbol/file scoring, no stacking           │
  │    partial ───┤  token-level identifier decomposition            │
  │    keyword ───┤  BM25 + inverted index + PRF + symbol post-boost │
  │                                                                  │
  │  Augmentation:                                                   │
  │    twin ──────┤  structural-signature lookup, other languages    │
  │    monte_carlo┘  seeded random walks over symbol/call/lex layers │
  │                  → visit counts                                  │
  └──────────────────────────────────────────────────────────────────┘
                │
                ▼
  ┌──────────────────────────────────────────────────────────────────┐
  │  STAGE 3 — GRAPH-LEVEL SCORING                                   │
  │  3a. Monte Carlo walk (visit scoring, O(walkers·steps))          │  (aug)
  │  3b. Per-language score normalization                            │  (aug)
  │  3c. Subsystem detection & boosting (tree-based)                 │  (core)
  │  3d. Scope demotion (test/mock/cross-root penalties)             │  (core)
  └──────────────────────────────────────────────────────────────────┘
                │
                ▼
  ┌──────────────────────────────────────────────────────────────────┐
  │  STAGE 4 — NOVELTY PACKING                                       │  (aug)
  │  cost = -ln(novelty) per block, midpoint = ln2,                  │
  │  annealed temperature: 0.50→0.05                                 │
  │  common symbols (df>200) carry no redundancy cost                │
  │  redundant blocks are cut harder as the window fills             │
  └──────────────────────────────────────────────────────────────────┘
                │
                ▼
  ┌──────────────────────────────────────────────────────────────────┐
  │  STAGE 5 — SELECTION (structuralMMR)                             │
  │  query-free MMR: symbolOverlap + lineProximity + sameTwin        │
  └──────────────────────────────────────────────────────────────────┘
                │
                ▼
           top-K chunks → context window
```

---

## 3. Stage-by-Stage Specification

### Stage 0 — Symbol Hygiene _(EHNAS core: exact)_

**Purpose:** Kill noise before it enters any strategy.

- `cleanSymbols`: drops tokens < 3 chars, case-insensitive dedup.
- `splitDottedSymbols`: `foo.Bar` → [`foo.Bar`, `foo`, `Bar`] — whole dotted name scores highest, parts enable partial matches.
- **Why it matters:** single-char tokens (`s`, `f`, `m`) previously caused spurious +25 score bumps in keyword post-processing and polluted grep candidate sets.

### Stage 1 — Explicit Localization _(EHNAS core: exact + node)_

**Purpose:** Answer "user pointed at a specific place" queries deterministically.

- Regex extraction: `file.go:42`, bare filenames, path fragments (`a/b/c`), `funcName:line`.
- Resolution ladder: exact file+line (×1.5) → suffix file+line (×1.2) → filename → path fragment (×0.8) → symbol+line (×1.3).
- `resolveAnchorPins`: pins high-confidence entities at score `5000 + confidence + min(retrieval, 1000)` — these survive every later stage untouched. Pins are resolved after the Monte Carlo walk (Stage 3a), so they never seed it.
- Path gate: if anchors reference a specific path, all later strategies are gated to matching chunks.

### Stage 2 — Strategy Swarm

All strategies run through the `run()` wrapper, which handles:

- Truncation to 500 hits per strategy (pre-merge)
- Path gate application
- Multi-strategy boost on merge (`+2.5` kb_exact, `+3.0` grep, `+2.0` exact/keyword, `+1.5` partial, `+1.2` twin, `+1.0` monte_carlo)
- Trace recording (count, top score, avg score, top files)

#### 2.1 kb_exact _(core)_

Knowledge-base exact lookup. Highest boost (2.5) — user-curated knowledge wins.

#### 2.2 grep _(core)_

File-component, name, symbol, and content-def matching with **per-dimension caps** (file ≤150, name ≤180, symbol ≤160, content-def ≤200). Multiple tokens matching the same dimension don't stack. Content scan only when name/symbol/file gave nothing, with a hydration budget.

#### 2.3 exact _(core)_

Case-insensitive exact match on chunk name or symbol list. Score scaled by **specificity multiplier**: ≥20 chars ×3.0, ≥12 ×2.0, ≥7 ×1.5. Rarer symbols are more valuable.

#### 2.4 partial _(core)_

Token-level decomposition of identifiers (`splitIdentifierToTokens`). Requires ≥2 matched tokens or 1 token + score ≥15. Catches `NewHTTPServer` ← query `http server`.

#### 2.5 keyword _(core)_

BM25 over inverted index (fallback: linear keyword scoring), followed by a pseudo-relevance feedback (PRF) pass that re-queries with terms the top hits share. Post-processing:

- +25 if query symbol appears as call site (`.sym(` or `sym(`) in content
- −10 if chunk name starts with `_sym` (private/unrelated)

#### 2.6 twin — _Twin search_ _(augmentation)_

`crossLangTwinSearch` returns chunks that have the same **structural signature** as an existing hit but live in a different language.

- **Signature:** FNV-64a hash of the chunk type plus the sorted, lowercased, underscore-stripped symbol set (`structuralSignature`). Needs ≥2 symbols; signatures shared by more than 24 chunks are dropped as hubs.
- **Twin index:** built once at load time (`buildTwinIndex`) — per-chunk signature, signature → chunk indices, and per-language lexical density.
- **Scoring:** each source hit with score ≥ 40 contributes `score · 0.55 / √(class size)` to every twin in another language; contributions from several sources add up.
- **Why:** it is the only route by which a query reaches code that shares no token with it, at one hash lookup per source chunk, with no vector search.

#### 2.7 monte_carlo — _Monte Carlo walk_ _(augmentation)_

```
seeds (hits so far with score ≥ 60, top 16)
   │
   ▼ start 256 walkers
walk layers: symbol co-occurrence, call graph, lexical co-occurrence
   │
   ├── step: pick a layer by preference, move to a weighted random neighbour
   ├── stop: P = 0.30 + min(0.45, symDF/5000)  ← boilerplate boundaries
   └── branch: at hub chunks (degree ≥ 96, P = 0.25), push 2 extra walkers
   │
   ▼
visit count per chunk (≥ 2 visits) × 12.0 = score
```

- **Complexity:** O(256 × 20) = 5k steps per query, independent of corpus size.
- **Win:** surfaces 3–5 hop cross-file dependency chains (interface impl → caller → config → test fixture) that no single-hop strategy connects.
- **Seeded from EHNAS.** Walk seeds are the highest-scoring hits found so far (mostly anchor search and grep), so the walk only explores structure EHNAS has already validated.

### Stage 3 — Graph-Level Scoring

**3a. Monte Carlo walk** _(augmentation)_ — see 2.7 — runs after every Stage 2 strategy and before anchor pins are resolved.

**3b. Per-language normalization** _(augmentation)_ — `langNormalizeScores` divides each non-exact score by its language's relative lexical density, so a dense language (C: short symbols, high df) cannot out-rank a sparse one just for being dense.

**3c. Subsystem boosting** _(core: hierarchical)_ — query keywords → subsystem tree detection → score boost for chunks in detected subsystems. Noise paths filtered.

**3d. Scope demotion** _(core: structure)_ — penalties applied by structural position in the tree:

| Condition                              | Penalty             |
| -------------------------------------- | ------------------- |
| Test file + non-test query             | ×0.3                |
| Test file + concept/flow intent        | ×min(1.0, 0.3×2.33) |
| Cross-root (`charm-*`, `requirements`) | ×0.32               |

Exact/pinned chunks are immune.

### Stage 4 — Novelty Packing _(augmentation)_

```
per block:    cost = -ln(novelty)         novelty = 1 - redundant/total symbols
              w = 1 / (e^((cost-midpoint)/temp) + 1)          midpoint = ln2
              score *= (1 - 0.85) + 0.85 · w
temperature:  anneals 0.50 → 0.05 as the window fills
common:       symbols with df > 200 are excluded from the cost
```

- **Early picks:** temp=0.50, soft curve — exploration, diverse auxiliary signatures admitted.
- **Late picks:** temp=0.05, sharp cutoff — only strictly novel core logic passes.
- **Common connectors:** `err != nil`, standard imports, logging overhead — high-df symbols don't raise the cost, so connector chunks can repeat without penalty.
- **Pinned/exempt:** exact and pinned chunks bypass the gate entirely.

### Stage 5 — Selection _(structuralMMR)_

Query-free maximal marginal relevance. Relevance is the fused score (log-compressed, blended with rank); redundancy against already-selected chunks is a weighted sum of three channels:

| Channel         | Weight | Measures                                         |
| --------------- | ------ | ------------------------------------------------ |
| `symbolOverlap` | 0.45   | idf-weighted Jaccard over non-boilerplate symbols |
| `lineProximity` | 0.20   | line gap within the same file                    |
| `sameTwin`      | 0.55   | same structural twin class, any language         |

A per-file saturation floor stops one file from filling the window. The trace reports this selector as `structural_mmr`.

---

## 4. Data Flow Summary

```
query
 ├─► symbols ───────────────► grep / exact / partial / keyword post-boost   (core)
 ├─► anchors ───────────────► anchor search ─► MC seeds ─► pins           (core → aug → core)
 ├─► keywords ──────────────► BM25 + PRF / subsystem detection             (core)
 ├─► current hits ──────────► twin search                                  (aug)
 └─► merged map ─► MC visits ─► lang normalize ─► subsystem boost ─► demotion ─► sort
                                                  ─► [novelty pack] ─► topK ─► structuralMMR
```

**Score scale after all stages:** pins 5000+, exact 100–540, grep 60–200, MC visits 24–600, keyword 2–45, partial 15–50. Novelty packing rescales non-exact scores by 0.15–1.0×.

**Provenance guarantee (EHNAS):** for any chunk returned by the core path, the final score is a pure function of extracted source metadata, the query, and the constants in §6 — no learned weights, no inference. When augmentation layers are active, their contribution is reported separately in the trace, so the deterministic component of any score is always recoverable.

---

## 5. Complexity Budget

| Stage              | Time                                 | Space                | Layer |
| ------------------ | ------------------------------------ | -------------------- | ----- |
| Symbol hygiene     | O(\|query\|)                         | O(\|symbols\|)       | core  |
| Anchor search      | O(anchors × chunks)                  | O(hits)              | core  |
| grep/exact/partial | O(chunks × symbols)                  | O(candidates)        | core  |
| BM25               | O(terms × postings)                  | O(topK)              | core  |
| Twin index build   | O(chunks × symbols) once at load     | O(chunks)            | aug   |
| Twin search        | O(sources × class size)              | O(hits)              | aug   |
| MC walk            | O(256 × 20) = **O(1)** w.r.t. corpus | O(visit map)         | aug   |
| Novelty packing    | O(topK × syms/chunk)                 | O(selected syms)     | aug   |

The MC walk is the only stage whose cost is _constant_ in corpus size — the layer build is O(chunks × symbols) but is amortizable per-index-update, not per-query.

---

## 6. Configuration Surface

All new tunables live in the code as constants (no config struct changes needed):

| Constant            | Default     | Meaning                                |
| ------------------- | ----------- | -------------------------------------- |
| `mcWalkers`         | 256         | Walkers started per query              |
| `mcMaxSteps`        | 20          | Max walk length                        |
| `mcStopBase`        | 0.30        | Baseline stop probability              |
| `mcHubBranch`       | 3           | Branch factor at hubs                  |
| `mcVisitScoreScale` | 12.0        | Visit count → score multiplier         |
| `twinBridgeFloor`   | 40.0        | Min source score for twin search       |
| `twinBridgeDecay`   | 0.55        | Share of source score passed to a twin |
| `twinMaxDF`         | 24          | Max chunks per twin class              |
| `commonSymbolDF`    | 200         | df cutoff for common symbols           |
| `temp0` / `tempMin` | 0.50 / 0.05 | Annealing schedule                     |
| `noveltyGain`       | 0.85        | Max boost for fully novel blocks       |

### Debug log tags

| Tag                     | Source                                   |
| ----------------------- | ---------------------------------------- |
| `[EHNAS]`               | engine load report (`reportEhnasIndex`)  |
| `[ETIYA]`               | etiya engine artifact load               |
| `[crossLangTwinSearch]` | twin search                              |
| `[mc-walk]`             | Monte Carlo walk summary                 |
| `[MC-DIAG]`             | Monte Carlo walk diagnostics             |
| `[noveltyPack]`         | novelty packing                          |
| `[structuralMMR]`       | selection                                |

Match details carry `twin-bridge:`, `mc-visits:` and `novelty: w=` entries.

---

## 7. Why "EHNAS"

1. **Exact** — the core path contains no semantic inference; every score is recomputable from source metadata, which is what makes "how was this retrieved?" a deterministic question rather than a model-introspection problem.
2. **Hierarchical** — relevance is decided top-down: subsystem detection and scope demotion prune the tree before fine-grained scoring runs, matching how large codebases are actually organized.
3. **Node** — retrieval is addressed at chunk and symbol nodes, with anchor pins that outrank every later stage so an explicit user reference can never be demoted away.
4. **Structure** — the dependency graph is a first-class input. Symbol co-occurrence and AST structure feed both the core (scope, subsystem) and the Monte Carlo walk.

The three augmentation layers are not the name, but they are why EHNAS holds up at scale:

- **Monte Carlo walk** — random walks over the dependency graph, seeded by EHNAS's deterministic hits.
- **Twin search** — structural-signature matching, so cross-language ports of the same code are found without vectors.
- **Novelty packing** — a logistic, annealed redundancy gate on the context window under budget pressure.

Each targets a distinct scale bottleneck: graph size (walk), language fragmentation (twins), and budget pressure (packing). EHNAS core is the instrument; the augmentation layers extend its reach — and the trace always reports which part of a score was deterministic.
