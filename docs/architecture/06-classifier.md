# Architecture: Query Classifier

> **Purpose:** Details the three-level query classification system
> (`internal/query/classifier.go`) that decides which of the 15 query handlers
> to invoke. Understanding this is essential for adding new query types or
> debugging mis-routed queries.
>
> **Limitation:** This diagram represents the *current* cascade logic — a
> chain of early-return `if` statements. It does **not** represent an ideal
> design; the ordering dependency and early-exit behaviour are known limitations
> (documented below).

---

## Classification Decision Tree

```mermaid
flowchart TD
    IN["User query string"]

    subgraph L1["Level 1 — Regex pattern match (confidence: 0.95)"]
        direction TB
        D["debug?\n(why does · error · bug · crash · exception)"]
        CMP["comparison?\n(difference between · vs · compare)"]
        EX["example?\n(how to use · usage example · show me how)"]
        DF["data flow?\n(how data · trace data · passes through)"]
        SEC["security?\n(vulnerable · injection · XSS · auth)"]
        PERF["performance?\n(slow · bottleneck · latency · memory)"]
        REF["refactoring?\n(refactor · clean up · better way)"]
        DEP["dependency?\n(depends on · imports · external)"]
        TEST["testing?\n(test · unit test · mock · coverage)"]
        DOC["documentation?\n(what does · explain · describe)"]
        LOC["location?\n(where is · find the · locate)"]
        USAGE["usage?\n(who calls · what uses · which invokes)"]
        ARCH["architecture?\n(overall structure · system design)"]
        IMPL["implementation?\n(implement · add feature · create new)"]
    end

    subgraph L2["Level 2 — Symbol validation (confidence: 0.90–0.92)"]
        SYM_EX["extract symbols\n(regex: CamelCase · snake_case · UPPER)"]
        SYM_VAL["validate against kb_index.json\n(discard non-codebase words)"]
        MULTI2["≥2 valid symbols\n+ comparison keywords → Comparison"]
        SINGLE_FIND["1 symbol + where/find/locate → Location"]
        SINGLE_CALL["1 symbol + calls/uses → Usage"]
        SINGLE_EX["1 symbol + example/sample → Example"]
        MULTI_UNDERSTAND["≥1 symbols (no specific match) → Understanding"]
    end

    subgraph L3["Level 3 — Keyword fallback (confidence: 0.75–0.85)"]
        KW_DEBUG["debug · error · bug → Debug"]
        KW_PERF["slow · optimize · memory → Performance"]
        KW_REF["refactor · simplify → Refactoring"]
        KW_TEST["test · mock · coverage → Testing"]
        KW_IMPL["implement · add · create → Implementation"]
        KW_ARCH["architecture · structure → Architecture"]
        DEFAULT["default → Understanding  (0.75)"]
    end

    OUT["QueryType\n+ Symbols[]\n+ Keywords[]\n+ Confidence\n+ NeedsContext bool"]

    IN --> D
    D -->|"match"| OUT
    D -->|"no"| CMP --> EX --> DF --> SEC --> PERF --> REF --> DEP --> TEST --> DOC --> LOC --> USAGE --> ARCH --> IMPL

    IMPL -->|"no L1 match"| SYM_EX --> SYM_VAL

    SYM_VAL -->|"≥2 + compare words"| MULTI2 --> OUT
    SYM_VAL -->|"1 + location words"| SINGLE_FIND --> OUT
    SYM_VAL -->|"1 + call words"| SINGLE_CALL --> OUT
    SYM_VAL -->|"1 + example words"| SINGLE_EX --> OUT
    SYM_VAL -->|"≥1 symbols"| MULTI_UNDERSTAND --> OUT
    SYM_VAL -->|"no valid symbols"| KW_DEBUG

    KW_DEBUG --> KW_PERF --> KW_REF --> KW_TEST --> KW_IMPL --> KW_ARCH --> DEFAULT --> OUT
```

---

## Symbol Extraction Pipeline

```mermaid
flowchart LR
    Q["raw query"]
    REGEX["symbolPattern regex\nCamelCase · snake_case · UPPER_CASE"]
    FILTER["filter common words\n(the · how · what · is · are …)"]
    VALIDATE["validate against\nkb_index.json symbols"]
    ENTITY["classify entity type\nfunction | type | unknown"]

    Q --> REGEX --> FILTER --> VALIDATE --> ENTITY
```

### Symbol pattern used
```
\b[A-Z][a-z]+(?:[A-Z][a-z]+)*\b   ← CamelCase  (QueryTrafficController)
\b[a-z_][a-z0-9_]*\b              ← snake_case  (context_builder)
\b[A-Z_][A-Z0-9_]+\b              ← UPPER_CASE  (MAX_TOKENS)
```

---

## Query Type Reference

| Type | `NeedsContext` | LLM called | Primary data |
|------|---------------|-----------|-------------|
| Location | `false` | ❌ | `kb_index.FunctionsByName` / `TypesByName` |
| Usage | `false` | ❌ | `kb_call_graph.Functions` |
| Dependency | `false` | ❌ | Call graph (transitive, depth 2) |
| Understanding | `true` | ✅ | Semantic + call graph |
| Implementation | `true` | ✅ | Symbol locations + semantic |
| Architecture | `true` | ✅ | Call graph + semantic |
| Debug | `true` | ✅ | Semantic |
| Comparison | `true` | ✅ | Semantic (min 2 symbols) |
| Refactoring | `true` | ✅ | Semantic + call graph |
| Performance | `true` | ✅ | Semantic + call graph |
| DataFlow | `true` | ✅ | Call graph + type flow |
| Security | `true` | ✅ | Semantic |
| Documentation | `true` | ✅ | Semantic |
| Example | `true` | ✅ | Semantic |
| Testing | `true` | ✅ | Semantic |

---

## Known Classification Bugs & Limitations

| Issue | Example | Result | Root cause |
|-------|---------|--------|-----------|
| Ordering priority overrides intent | `"what does the optimize function do?"` | Routed to **Refactoring** | `refactoringPattern` matches "optimize" before `documentationPattern` matches "what does" |
| Generic words are extracted as symbols | `"how does the Router work?"` | "Router" extracted as symbol even if not in KB | `validateSymbols` falls back to all symbols when `validSymbols` is empty |
| Comparison requires exactly 2+ symbols at L2 | `"compare redis and sql caching strategy"` | If "redis"/"sql" aren't in KB, falls through to L3 | `validateSymbols` discards non-KB words |
| L1 has no score accumulation | `"add test for the performance handler"` | Routed to **Debug** (first regex hit) | Early return on first match; no scoring across all patterns |
| `"not working"` is a debug trigger | `"which functions are not working correctly with mocks?"` | Routed to **Debug** instead of **Testing** | `debugPattern` is checked before `testingPattern` |

### Recommended Fix

Replace the cascade of `if/return` statements with a scorer that evaluates all
patterns and picks the highest confidence:

```go
type PatternScore struct {
    Type       QueryType
    Confidence float64
    Reasoning  string
}

func (c *Classifier) level1PatternMatch(query, queryLower string) *Classification {
    var best PatternScore

    scores := []PatternScore{
        {QueryTypeDebug,    c.score(c.debugPattern, queryLower, 0.95), "debug pattern"},
        {QueryTypeTesting,  c.score(c.testingPattern, queryLower, 0.95), "testing pattern"},
        // ... all 14 patterns
    }

    for _, s := range scores {
        if s.Confidence > best.Confidence {
            best = s
        }
    }

    if best.Confidence >= 0.95 {
        return &Classification{Type: best.Type, Confidence: best.Confidence, ...}
    }
    return nil
}
```

---

## Adding a New Query Type

1. Add a constant to the `QueryType` iota block in `classifier.go`
2. Add its string representation to the `String()` array
3. Add a compiled `*regexp.Regexp` field to `Classifier`
4. Initialise the pattern in `QuerySheriff()`
5. Add a check in `level1PatternMatch` (or `level3KeywordAnalysis`)
6. Add a handler method `(r *Router) handleNewType(...)` in `router.go`
7. Add a `case QueryTypeNewType:` branch in `Router.Query()`
8. Update the routing table in this document
