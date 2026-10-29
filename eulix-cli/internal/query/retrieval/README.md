# Architecture Document: Eulix Retrieval Pipeline v2

**Codename: EHNAS** — _Exact · Hierarchical · Node · Structure_

> EHNAS names the deterministic retrieval backbone: **exact** scoring with no semantic inference, **hierarchical** localization through the subsystem tree, **node**-level resolution of chunks and symbols, and **structure**-aware traversal of the dependency graph. Every result's provenance is a deterministic calculation over metadata already extracted from source, so "how did this get retrieved?" is always answerable by recomputation.
>
> At multi-million-LOC scale, three physics-inspired **augmentation layers** extend EHNAS without changing its deterministic core: Monte Carlo transport, isospin alignment, and Fermi-Dirac packing.

---

## 1. Design Philosophy

Classic multi-strategy RAG treats retrieval as a _flat union of independent scorers_: exact match, keyword, vector, merge, sort, truncate. EHNAS keeps that proven skeleton but makes the deterministic path the backbone, and adds three augmentation layers for the failure modes of ultra-large-scale (multi-million LOC) codebases:

| Failure mode at scale                                                  | EHNAS core response                           | Augmentation layer        |
| ---------------------------------------------------------------------- | --------------------------------------------- | ------------------------- |
| Long-range cross-file dependencies invisible to single-hop retrieval   | Structure graph + anchor seeds                | **Monte Carlo Transport** |
| Polyglot embeddings fragment into disconnected language clusters       | Exact symbol matching stays language-agnostic | **Isospin Alignment**     |
| Context window fills with redundant boilerplate; novelty dies silently | Scope demotion + subsystem boosting           | **Fermi-Dirac Packing**   |

The insight: a codebase is not a bag of documents. It is a **medium** (graph), a **quantum system** (embeddings with symmetries), and a **thermal ensemble** (the context window under budget pressure). EHNAS retrieves deterministically through all three; the augmentation layers are how it measures them at scale.

The **Exact / Hierarchical / Node / Structure** split maps onto the pipeline as:

| Letter           | What it guarantees                                                                        |
| ---------------- | ----------------------------------------------------------------------------------------- |
| **Exact**        | Scoring is recomputable; no hidden semantic term in the core path                         |
| **Hierarchical** | Subsystem-tree detection and scope demotion decide relevance before scoring does          |
| **Node**         | Retrieval is chunk/symbol-node addressed, with anchor pins that survive every later stage |
| **Structure**    | The dependency medium (symbol co-occurrence, AST) is first-class, not an afterthought     |

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
  │  → resolveAnchorPins (5000+bonus, pinned)                        │
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
  │    keyword ───┤  BM25 + inverted index + symbol post-boost       │
  │                                                                  │
  │  Augmentation:                                                   │
  │    semantic ──┤  Isospin-aligned query → Wigner-Eckart CG scoring│
  │               │     score = |q|·|e| · 20 · (0.5 + 0.5·cos²θ)    │
  │    monte_carlo┘  seeded random walks over symbol co-occurrence   │
  │                  graph → integrated particle flux                 │
  └──────────────────────────────────────────────────────────────────┘
                │
                ▼
  ┌──────────────────────────────────────────────────────────────────┐
  │  STAGE 3 — MEDIUM-LEVEL PHYSICS                                  │
  │  3a. Monte Carlo transport (flux scoring, O(particles·steps))    │  (aug)
  │  3b. Subsystem detection & boosting (tree-based)                 │  (core)
  │  3c. Scope demotion (test/mock/cross-root penalties)             │  (core)
  └──────────────────────────────────────────────────────────────────┘
                │
                ▼
  ┌──────────────────────────────────────────────────────────────────┐
  │  STAGE 4 — THERMAL QUANTIZATION (Fermi-Dirac packing)            │  (aug)
  │  ε = -ln(novelty) per block, μ = ln2, annealing kT: 0.50→0.05   │
  │  bosonic symbols (df>200) condense freely                        │
  │  fermionic redundancy → Pauli exclusion as window fills          │
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
- `resolveAnchorPins`: pins high-confidence entities at score `5000 + confidence + min(retrieval, 1000)` — these survive every later stage untouched.
- Path gate: if anchors reference a specific path, all later strategies are gated to matching chunks.

### Stage 2 — Strategy Swarm

All strategies run through the `run()` wrapper, which handles:

- Truncation to 500 hits per strategy (pre-merge)
- Path gate application
- Multi-strategy boost on merge (`+2.5` kb_exact, `+3.0` grep, `+2.0` exact/keyword, `+1.5` partial/semantic/mc)
- Trace recording (count, top score, avg score, top files)

#### 2.1 kb*exact *(core)\_

Knowledge-base exact lookup. Highest boost (2.5) — user-curated knowledge wins.

#### 2.2 grep _(core)_

File-component, name, symbol, and content-def matching with **per-dimension caps** (file ≤150, name ≤180, symbol ≤160, content-def ≤200). Multiple tokens matching the same dimension don't stack. Content scan only when name/symbol/file gave nothing, with a hydration budget.

#### 2.3 exact _(core)_

Case-insensitive exact match on chunk name or symbol list. Score scaled by **specificity multiplier**: ≥20 chars ×3.0, ≥12 ×2.0, ≥7 ×1.5. Rarer symbols are more valuable.

#### 2.4 partial _(core)_

Token-level decomposition of identifiers (`splitIdentifierToTokens`). Requires ≥2 matched tokens or 1 token + score ≥15. Catches `NewHTTPServer` ← query `http server`.

#### 2.5 keyword _(core)_

BM25 over inverted index (fallback: linear keyword scoring). Post-processing:

- +25 if query symbol appears as call site (`.sym(` or `sym(`) in content
- −10 if chunk name starts with `_sym` (private/unrelated)

#### 2.6 semantic — _Wigner-Eckart layer_ _(augmentation: isospin)_

```
score = |q_aligned| · |e| · 20 · (0.5 + 0.5 · cos²θ)
        └──────┬──────┘        └───────┬───────┘
        radial strength (semantics)   CG coupling (geometry)
```

- **Isospin alignment** pre-rotates the query: per-language centroids are computed from chunk embeddings (grouped by file extension → `cpp`, `rust`, `go`, `python`, `js`, `jvm`, `other`). The query is shifted by the mean-of-centroids, landing it in the shared language-agnostic subspace. Monolingual corpora → no-op (no doublet to align).
- **CG coefficient:** `cos²θ` between aligned query and chunk embedding. Structurally aligned chunks (right tree level, right scope) get full coupling; isotropically similar but structurally wrong chunks are damped.
- **Not part of EHNAS core.** This layer is only present when embeddings exist; EHNAS remains fully functional without it, and results scored purely by the core carry no semantic term to audit.

#### 2.7 monte*carlo — \_Transport layer* _(augmentation)_

```
seeds (anchor/grep hits, score ≥ 60, top 16)
   │
   ▼ emit 256 particles
symbol co-occurrence graph (chunk i ~ j iff share declared symbol)
   │
   ├── scatter: move to random neighbor, P = 1 - absorb
   ├── absorb: P = 0.30 + min(0.45, symDF/5000)  ← boilerplate boundaries
   └── fission: at hub chunks (fanout ≥ 24), spawn 2 extra daughters
   │
   ▼
integrated flux per chunk × 12.0 = score
```

- **Complexity:** O(256 × 20) = 5k operations per query, independent of corpus size.
- **Win:** surfaces 3–5 hop cross-file dependency chains (interface impl → caller → config → test fixture) that no single-hop strategy connects.
- **Seeded from EHNAS.** Walk seeds are anchor and grep hits from the deterministic core, so the transport layer only explores structure EHNAS has already validated.

### Stage 3 — Medium-Level Physics

**3a. MC transport** _(augmentation)_ — see 2.7 — runs after anchor pins so pins can seed it.

**3b. Subsystem boosting** _(core: hierarchical)_ — query keywords → subsystem tree detection → score boost for chunks in detected subsystems. Noise paths filtered.

**3c. Scope demotion** _(core: structure)_ — penalties applied by structural position in the tree:

| Condition                              | Penalty             |
| -------------------------------------- | ------------------- |
| Test file + non-test query             | ×0.3                |
| Test file + concept/flow intent        | ×min(1.0, 0.3×2.33) |
| Cross-root (`charm-*`, `requirements`) | ×0.32               |

Exact/pinned chunks are immune.

### Stage 4 — Fermi-Dirac Context Packing _(augmentation: thermal)_

```
per block:  ε = -ln(novelty)          novelty = 1 - redundant/total symbols
            n(ε) = 1 / (e^((ε-μ)/kT) + 1)          μ = ln2
            score *= (1 - 0.85) + 0.85 · n(ε)
temperature: kT anneals 0.50 → 0.05 as window fills
bosons:      symbols with df > 200 excluded from ε (condense freely)
```

- **Hot phase (early picks):** kT=0.50, occupancy curve soft — exploration, diverse auxiliary signatures admitted.
- **Degenerate phase (late picks):** kT=0.05, sharp cutoff — only strictly novel core logic passes the Pauli gate.
- **Bosonic condensate:** `err != nil`, standard imports, logging overhead — high-df symbols don't raise ε, so connector chunks can repeat without penalty.
- **Pinned/exempt:** exact and pinned chunks bypass the gate entirely.

---

## 4. Data Flow Summary

```
query
 ├─► symbols ───────────────► grep / exact / partial / keyword post-boost   (core)
 ├─► anchors ───────────────► anchor search ─► pins ─► MC seeds            (core → aug)
 ├─► keywords ──────────────► BM25 / subsystem detection                   (core)
 ├─► embedding ──[isospin]──► vector search ──[Wigner-Eckart]──► semantic   (aug)
 └─► merged map ─► MC flux ─► subsystem boost ─► demotion ─► sort          (aug → core)
                                                        ─► [Fermi-Dirac] ─► topK  (aug)
```

**Score scale after all stages:** pins 5000+, exact 100–540, grep 60–200, MC flux 24–600, semantic 3–20 (pre-CG) × CG factor, keyword 2–45, partial 15–50. Fermi-Dirac rescales non-exact scores by 0.15–1.0×.

**Provenance guarantee (EHNAS):** for any chunk returned by the core path, the final score is a pure function of extracted source metadata, the query, and the constants in §6 — no learned weights, no inference. When augmentation layers are active, their contribution is reported separately in the trace, so the deterministic component of any score is always recoverable.

---

## 5. Complexity Budget

| Stage              | Time                                 | Space              | Layer |
| ------------------ | ------------------------------------ | ------------------ | ----- |
| Symbol hygiene     | O(\|query\|)                         | O(\|symbols\|)     | core  |
| Anchor search      | O(anchors × chunks)                  | O(hits)            | core  |
| grep/exact/partial | O(chunks × symbols)                  | O(candidates)      | core  |
| BM25               | O(terms × postings)                  | O(topK)            | core  |
| Isospin align      | O(n × dim) once per query            | O(dim × languages) | aug   |
| Vector search      | O(n × dim) or IVF O(√n × dim)        | O(topK)            | aug   |
| MC transport       | O(256 × 20) = **O(1)** w.r.t. corpus | O(flux map)        | aug   |
| Fermi-Dirac        | O(topK × syms/chunk)                 | O(selected syms)   | aug   |

The MC transport is the only stage whose cost is _constant_ in corpus size — the adjacency build is O(chunks × symbols) but is amortizable per-index-update, not per-query.

---

## 6. Configuration Surface

All new tunables live in the code as constants (no config struct changes needed):

| Constant           | Default     | Meaning                             |
| ------------------ | ----------- | ----------------------------------- |
| `mcParticles`      | 256         | Neutrons emitted per query          |
| `mcMaxSteps`       | 20          | Mean-free-path cap                  |
| `mcAbsorbBase`     | 0.30        | Baseline absorption                 |
| `mcHubFission`     | 3           | Branch factor at hubs               |
| `mcFluxScoreScale` | 12.0        | Flux → score multiplier             |
| `bosonDFThreshold` | 200         | df cutoff for bosonic symbols       |
| `kT0` / `kTMin`    | 0.50 / 0.05 | Annealing schedule                  |
| `fermiGain`        | 0.85        | Max boost for fully occupied blocks |

---

## 7. Why "EHNAS"

1. **Exact** — the core path contains no semantic inference; every score is recomputable from source metadata, which is what makes "how was this retrieved?" a deterministic question rather than a model-introspection problem.
2. **Hierarchical** — relevance is decided top-down: subsystem detection and scope demotion prune the tree before fine-grained scoring runs, matching how large codebases are actually organized.
3. **Node** — retrieval is addressed at chunk and symbol nodes, with anchor pins that outrank every later stage so an explicit user reference can never be demoted away.
4. **Structure** — the dependency medium is a first-class input. Symbol co-occurrence and AST structure feed both the core (scope, subsystem) and the transport layer.

The three augmentation layers are not the name, but they are why EHNAS holds up at scale:

- **Transport** — Monte Carlo walking of the dependency medium, seeded by EHNAS's deterministic hits.
- **Isospin** — rotation into the language-agnostic nucleon doublet, so the vector path does not fragment across languages.
- **Thermal** — Fermi-Dirac quantization of the context ensemble under budget pressure.

Each targets a distinct scale bottleneck: graph size (transport), language fragmentation (isospin), and budget pressure (thermal). EHNAS core is the instrument; the augmentation layers are how it measures — by counting what actually arrives, and always reporting which part of the count was deterministic.

---

### Two things to confirm

1. **`isospin.go` and `mc_seed_diag.go` are named after layers, not the codename** — they don't rename under this scheme, but if you want the file layout to mirror the core/augmentation split (e.g. `ehnas/` vs `aug/`), that's a package move rather than a rename. Tell me the target layout and I'll map the files.
2. **`mmr_trinity.go` → `mmr_ehnas.go`?** MMR is a diversity rerank, which is closer to the thermal layer than the exact core. If you want file names to reflect the split, it probably belongs with the augmentation files, not with EHNAS.
