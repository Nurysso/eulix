# Architecture: Query Pipeline (chat)

> **Purpose:** Describes the full runtime path from a user's natural-language
> question to the final LLM response. This is the "online" half of the system —
> everything that happens during `eulix chat`. This is the most complex subsystem
> in the codebase.
>
> **Limitation:** Token-budget arithmetic, score normalisation details, and the
> binary embedding file parsing are not expanded here. The diagram focuses on the
> component-level flow, not the mathematical scoring logic inside each step.

---

## End-to-End Query Flow

```mermaid
flowchart TD
    Q["💬 User question"]

    subgraph CACHE_READ["Cache layer (read)"]
        CR{"Cache hit?\n(query_hash + KB_checksum)"}
        CACHED["↩ Return cached answer"]
    end

    subgraph CLASSIFY["Query Classifier  ·  classifier.go"]
        L1["Level 1\nRegex pattern match\n15 patterns"]
        L2["Level 2\nSymbol validation +\nentity extraction"]
        L3["Level 3\nKeyword fallback\n→ defaults to Understanding"]
        L1 -->|"confidence ≥ 0.95"| TYPE
        L1 -->|"no match"| L2
        L2 -->|"symbols found"| TYPE
        L2 -->|"no match"| L3
        L3 --> TYPE
        TYPE["QueryType\n(1 of 15)"]
    end

    subgraph ROUTE["Router  ·  router.go"]
        direction LR
        LOC["Location\n→ KB index lookup"]
        USAGE["Usage\n→ call graph"]
        DEP["Dependency\n→ transitive graph"]
        CTX_NEEDED["Understanding · Architecture\nDebug · Implementation\nRefactoring · Performance\nDataFlow · Security\nDocumentation · Example\nTesting · Comparison"]
    end

    subgraph CTX["Context Builder  ·  context_builder.go"]
        direction TB
        MS["multiStrategySearch\n(topK = 100 candidates)"]
        KB_LK["① KB exact lookup\n  score 115–120"]
        SYM["② Exact symbol match\n  score 100"]
        PART["③ Partial identifier match\n  camelCase / snake_case split"]
        KW["④ Keyword search\n  TF-IDF-style"]
        VEC["⑤ Vector search\n  cosine similarity ≥ 0.5\n  (only if embeddings loaded)"]
        GRAPH["Call-graph expansion\n  top-20 neighbours × 0.9 decay"]
        BUDGET["Token-budget selection\n  (config.LLM.MaxTokens × 0.85)"]
        ASSEMBLE["Assemble ContextWindow\n  []ContextChunk"]

        MS --> KB_LK & SYM & PART & KW & VEC
        KB_LK & SYM & PART & KW & VEC -->|"merge + boost"| GRAPH
        GRAPH --> BUDGET --> ASSEMBLE
    end

    subgraph PROMPT["Prompt builder"]
        AH["Anti-hallucination\ninstructions"]
        META["Query type + symbols\n+ confidence"]
        HANDLER["Handler-specific\nAST-boundary framing"]
    end

    subgraph LLM_CALL["LLM  ·  llm/client.go"]
        OL["Ollama\nlocalhost:11434"]
        AN["Anthropic\napi.anthropic.com"]
    end

    subgraph CACHE_WRITE["Cache layer (write)"]
        CW["Store response\n(query_hash, KB_checksum, TTL)"]
    end

    Q --> CR
    CR -->|"hit"| CACHED
    CR -->|"miss"| L1
    TYPE --> ROUTE
    LOC & USAGE & DEP -->|"structured result\n(no LLM needed)"| CW
    CTX_NEEDED --> CTX
    ASSEMBLE --> AH & META & HANDLER
    AH & META & HANDLER --> OL & AN
    OL & AN --> CW
    CW --> ANS["✅ Answer shown in TUI"]
```

---

## Query Type Routing Table

| Query Type | LLM needed | Primary data source | Example question |
|-----------|-----------|-------------------|-----------------|
| Location | ❌ | `kb_index.json` → `FunctionsByName` | "where is `parseFile` defined?" |
| Usage | ❌ | `kb_call_graph.json` | "who calls `buildContext`?" |
| Dependency | ❌ | Call graph (transitive, 2 levels) | "what does `Router` depend on?" |
| Understanding | ✅ | Semantic + call graph context | "how does authentication work?" |
| Implementation | ✅ | Symbol locations + semantic context | "add a new cache backend" |
| Architecture | ✅ | Call graph + semantic context | "describe the overall structure" |
| Debug | ✅ | Semantic context | "why does glados fail on large repos?" |
| Comparison | ✅ | Semantic context (requires ≥2 symbols) | "difference between redis and sql cache" |
| Refactoring | ✅ | Semantic + call graph context | "how can I simplify the router?" |
| Performance | ✅ | Semantic + call graph context | "what's the slow part of the embed pipeline?" |
| DataFlow | ✅ | Call graph + type flow | "how does a query string become a vector?" |
| Security | ✅ | Semantic context | "are there input validation issues?" |
| Documentation | ✅ | Semantic context | "what does `ContextWindowCreator` do?" |
| Example | ✅ | Semantic context | "show me how to use `CacheController`" |
| Testing | ✅ | Semantic context | "how should I test the classifier?" |

---

## Retrieval Strategy Scoring

```mermaid
graph LR
    subgraph Strategies["Search strategies (merged into one candidate pool)"]
        S0["KB exact lookup\n⭐ score 115–120"]
        S1["Exact symbol match\n⭐ score 100"]
        S2["Partial identifier\n⭐ score 80–90"]
        S3["Keyword search\n⭐ score 40–70"]
        S4["Vector / semantic\n⭐ score 0.5–1.0\n(× 0.5 weight when combined)"]
    end

    MERGE["Merge by chunk ID\nkeep max score + boost +1.5–2.0\nif found by multiple strategies"]

    EXPAND["Call-graph expansion\ntop-20 → follow 'calls'/'called_by'\ndecay × 0.9 per hop"]

    SELECT["Token-budget selection\nsort by score → fill budget\n(MaxTokens × 0.85)"]

    S0 & S1 & S2 & S3 & S4 --> MERGE --> EXPAND --> SELECT
```

---

## Known Limitations

| Limitation | Impact | Status |
|-----------|--------|--------|
| KB exact lookup results are discarded by a `make(map)` reset on line ~700 of `context_builder.go` | Highest-quality signal (score 120) never influences context selection | 🐛 Bug — to be fixed |
| Vector search is skipped if `embeddings.bin` is missing or version-mismatched | Falls back to keyword-only retrieval silently | Acceptable fallback but not surfaced to user |
| `eulix_embed` binary path hardcoded as `../eulix_embed` relative to `.eulix/` | Breaks when binary is installed to `$PATH` | 🐛 Bug — to be fixed |
| Token count approximated as `len(bytes) / 4` | Can overflow context window on code-heavy chunks | Should use per-model tokeniser |
| Classification is single-winner (first regex match wins) | Overlapping queries may route incorrectly | Classifier accuracy degrades on ambiguous queries |
| No streaming LLM responses | TUI shows spinner until full response arrives | Suboptimal UX for long answers |
