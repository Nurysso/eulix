# Query Package Documentation

> Comprehensive guide to the Eulix query package - the intelligent retrieval and routing system for code understanding.

---

## Table of Contents

1. [Overview](#overview)
2. [Design Philosophy](#design-philosophy)
3. [Architecture](#architecture)
4. [Key Components](#key-components)
5. [Query Classification](#query-classification)
6. [Context Building](#context-building)
7. [Multi-Strategy Retrieval](#multi-strategy-retrieval)
8. [Intent Classification](#intent-classification)
9. [Prompt Construction](#prompt-construction)
10. [Performance Optimization](#performance-optimization)
11. [Integration Points](#integration-points)
12. [Known Limitations](#known-limitations)

---

## Overview

The query package (`internal/query`) is the core intelligence layer of Eulix, responsible for transforming natural-language questions into precise, context-aware answers about code. It implements a sophisticated Retrieval-Augmented Generation (RAG) pipeline that combines static analysis, semantic search, and LLM reasoning.

### Core Responsibilities

- **Query Classification**: Categorize user questions into 15+ query types with confidence scoring
- **Context Retrieval**: Assemble relevant code context using multiple orthogonal search strategies
- **Intent Understanding**: Determine user intent to allocate retrieval budgets appropriately
- **Prompt Engineering**: Construct targeted prompts for different query types
- **Caching**: Cache responses to avoid redundant LLM calls
- **Routing**: Direct queries to appropriate handlers (some bypass LLM entirely)

### Why This Design Matters

The query package is the bridge between raw code analysis and human understanding. It must:

1. **Understand ambiguity**: Natural language questions are inherently ambiguous
2. **Handle scale**: Work efficiently with codebases from 1K to 1M+ LOC
3. **Be precise**: Return accurate answers without hallucination
4. **Be efficient**: Minimize LLM calls through smart caching and direct lookups
5. **Adapt**: Adjust retrieval strategies based on query intent

---

## Design Philosophy

### Core Principles

1. **Multi-Strategy Retrieval**: No single search strategy works for all queries. We combine 5 orthogonal approaches:
   - KB exact lookup (highest precision)
   - Exact symbol matching
   - Partial identifier matching
   - Keyword search
   - Semantic vector search

2. **Intent-Aware Budgeting**: Different query types need different context allocations. A "where is X defined?" query needs minimal context, while "how does authentication work?" needs broad context.

3. **Graceful Degradation**: The system works with partial data:
   - Missing embeddings? Fall back to keyword search
   - Missing call graph? Skip graph expansion
   - Large KB? Use lazy loading and approximate search

4. **Anti-Hallucination**: Prompts explicitly instruct LLMs to distinguish between:
   - CONFIRMED IN SOURCE
   - INFERRED FROM SIGNATURE
   - CANNOT DETERMINE

5. **Performance First**: Cache aggressively, use streaming for large files, and avoid unnecessary computations.

### Trade-offs Accepted

| Decision | Trade-off | Rationale |
|----------|-----------|-----------|
| Single-pass KB loading | Cannot randomly access KB | Memory efficiency for large files |
| Regex-based classification | May misclassify complex queries | Fast and works for 95% of cases |
| Fixed token budgets | May under/over-allocate | Simpler than dynamic optimization |
| No streaming LLM responses | Poor UX for long answers | Simpler implementation; can be added later |

---

## Architecture

### High-Level Flow

```
User Query
    ↓
Query Classifier (3-layer classification)
    ↓
Router (dispatch based on query type)
    ↓
├─→ Direct Answer (Location, Usage, Dependency)
└─→ Context Builder (for LLM queries)
    ↓
Intent Classifier (determine retrieval strategy)
    ↓
Multi-Strategy Search (5 orthogonal strategies)
    ↓
Graph Expansion (call graph traversal)
    ↓
Selection (MMR or greedy)
    ↓
Source Hydration (load actual code)
    ↓
Prompt Construction (type-specific prompts)
    ↓
LLM Call
    ↓
Cache Storage
    ↓
Answer Display
```

### Module Structure

```
internal/query/
├── router_core.go          # Top-level dispatcher
├── router_handlers.go      # Query type handlers
├── classifier_core.go     # Query classification logic
├── classifier_patterns.go  # Regex patterns for classification
├── classifier_utils.go     # Classification utilities
├── context_builder.go      # Main context orchestration
├── context_search.go      # Multi-strategy retrieval
├── context_loader.go      # KB and embedding loading
├── context_intent.go      # Intent classification & budgeting
├── context_types.go       # Shared type definitions
├── context_kb.go          # KB exact lookup
├── context_graph.go       # Call graph expansion
├── context_mmr.go         # Diversity-aware selection
├── context_source.go      # Source code hydration
├── context_utils.go       # Utility functions
├── context_vectorIVF.go   # Approximate vector search
├── prompts.go             # Prompt templates
├── subsystem.go           # Subsystem detection
└── boilerPlate.go         # Boilerplate symbol filtering
```

---

## Key Components

### 1. Router (`router_core.go`)

The Router is the top-level coordinator that:

- **Initializes components**: Creates classifier, context builder, and LLM client
- **Dispatches queries**: Routes to appropriate handler based on classification
- **Manages cache**: Integrates with cache layer for response caching
- **Handles non-LLM queries**: Direct answers for Location, Usage, Dependency queries

**Key Function**: `PromptOrAnswer(query string) (string, error)`

### 2. Classifier (`classifier_core.go`)

The Classifier implements a 3-layer classification system:

**Layer 1: Pattern Matching** (fastest, confidence ≥ 0.95)
- Regex patterns for each query type
- 15+ query types supported
- High-confidence matches bypass deeper analysis

**Layer 2: Symbol Validation** (medium confidence)
- Extracts symbols from query
- Validates against KB index
- Entity extraction for structured queries

**Layer 3: Keyword Analysis** (fallback)
- Keyword-based classification
- Defaults to "Understanding" type
- Handles ambiguous queries

**Query Types**:
- Location, Usage, Dependency (non-LLM)
- Understanding, Implementation, Architecture (LLM)
- Debug, Comparison, Refactoring (LLM)
- Performance, DataFlow, Security (LLM)
- Documentation, Example, Testing (LLM)
- CodeGeneration, CallGraph, EntryPoints, FileStructure, Todos, Metrics

### 3. Context Builder (`context_builder.go`)

The Context Builder orchestrates the full retrieval pipeline:

**Responsibilities**:
- Load KB artifacts (kb.json, kb_index.json, kb_call_graph.json)
- Load embeddings (embeddings.bin, vectors.bin)
- Build derived indices (symbol index, inverted index, call-site index)
- Detect subsystems and noise patterns
- Execute multi-strategy search
- Assemble final context window

**Key Function**: `BuildContext(query string) (*ContextWindow, error)`

### 4. Intent Classifier (`context_intent.go`)

The Intent Classifier determines retrieval strategy:

**Intent Types**:
- `IntentCallers`: "who calls X?" - focus on call-site discovery
- `IntentCallees`: "what does X call?" - focus on dependencies
- `IntentSymbolExact`: "show me function foo" - definition-only
- `IntentFlow`: "trace execution" - call graph + control flow
- `IntentConcept`: "how does X work?" - semantic similarity
- `IntentDebug`: "why does this crash?" - error paths
- `IntentUnknown`: fallback

**Budget Allocation**:
- Different intents allocate tokens differently across strategies
- Example: Callers gets 45% kb_exact, Concept gets 50% semantic
- Reserves space for system prompt, query, and LLM response

### 5. Multi-Strategy Search (`context_search.go`)

Implements 5 orthogonal retrieval strategies:

**Strategy 1: KB Exact Lookup** (score 115-120)
- Direct symbol table lookup in kb.json
- Highest precision for known symbols
- Multi-boost: 2.5×

**Strategy 2: Exact Symbol Match** (score 100)
- Direct chunk name and symbol matching
- High precision for explicit queries
- Multi-boost: 2.0×

**Strategy 3: Partial Identifier Match** (score 80-90)
- Token-level matching on camelCase/snake_case
- Handles typos and partial names
- Multi-boost: 1.5×

**Strategy 4: Keyword Search** (score 40-70)
- TF-IDF or linear scan with symbol boosting
- Good for concept queries
- Multi-boost: 2.0×

**Strategy 5: Semantic Search** (score 0.5-1.0)
- Vector embeddings similarity
- Optional (skipped if embeddings missing)
- Multi-boost: 1.5×

**Merging**:
- Results merged by chunk ID
- Multiple strategy hits get score boosts
- Final ranking: exact matches pinned, then by score

### 6. Context Loader (`context_loader.go`)

Handles efficient loading of KB artifacts:

**Memory Strategy**:
- Streams kb.json via mmap + json.NewDecoder
- Never materialises full struct in memory
- Peak memory: chunk slice + derived indices only
- Handles 4GB kb.json on 16GB RAM

**File Formats**:
- `kb.json`: Full codebase structure (streamed)
- `kb_index.json`: Lightweight symbol index
- `kb_call_graph.json`: Call relationships
- `embeddings.bin`: Vector embeddings (binary v3/v4)
- `vectors.bin`: Embedding→chunk ID map

**Boilerplate Filtering**:
- Applied during index construction
- Filters common symbols (ctx, err, i, j)
- Configurable threshold (default: 30% document frequency)

### 7. Prompt Builder (`prompts.go`)

Constructs type-specific prompts for LLM calls:

**Structure**:
- Shared header (anti-hallucination instructions)
- Type-specific body (reasoning steps)
- Shared footer (output format)

**Task-Specific Bodies**:
- Understanding: Explain what code does and why
- Implementation: Describe how implementation works
- Architecture: Describe architectural structure
- Debug: Investigate reported problems
- And 11+ more query types

**Anti-Hallucination**:
- Explicitly labels: CONFIRMED/INFERRED/CANNOT DETERMINE
- Requires citation of source lines
- Distinguishes signature-only from full source

---

## Query Classification

### Three-Layer Classification System

The classifier uses a cascading approach for efficiency and accuracy:

#### Layer 1: Pattern Matching (Fast Path)

```go
if result := c.level1PatternMatch(queryLower); result != nil && result.Confidence >= 0.95 {
    return result
}
```

- **Speed**: O(1) regex matching
- **Coverage**: ~70% of queries
- **Confidence**: ≥ 0.95 for matches
- **Examples**: 
  - "where is `parseFile` defined?" → Location
  - "who calls `buildContext`?" → Usage
  - "difference between X and Y" → Comparison

#### Layer 2: Symbol Validation (Medium Path)

```go
symbols := c.extractSymbols(query)
validSymbols := c.validateSymbols(symbols)
entities := c.extractEntities(validSymbols)

if len(validSymbols) > 0 {
    if result := c.level2SymbolAnalysis(queryLower, validSymbols, entities); result != nil {
        return result
    }
}
```

- **Speed**: O(n) symbol extraction + validation
- **Coverage**: ~20% of queries
- **Confidence**: 0.7-0.9
- **Examples**:
  - "show me the Router class" → Implementation
  - "how does ContextBuilder work?" → Understanding

#### Layer 3: Keyword Analysis (Fallback)

```go
return c.level3KeywordAnalysis(queryLower, validSymbols, entities)
```

- **Speed**: O(n) keyword matching
- **Coverage**: ~10% of queries
- **Confidence**: 0.5-0.7
- **Default**: Understanding type

### Query Type Routing

| Query Type | LLM Needed | Primary Data Source | Example |
|------------|------------|-------------------|---------|
| Location | ❌ | kb_index.json | "where is `parseFile` defined?" |
| Usage | ❌ | kb_call_graph.json | "who calls `buildContext`?" |
| Dependency | ❌ | Call graph (transitive) | "what does `Router` depend on?" |
| Understanding | ✅ | Semantic + call graph | "how does authentication work?" |
| Implementation | ✅ | Symbol locations + semantic | "add a new cache backend" |
| Architecture | ✅ | Call graph + semantic | "describe the overall structure" |
| Debug | ✅ | Semantic context | "why does glados fail?" |
| Comparison | ✅ | Semantic (≥2 symbols) | "difference between redis and sql" |
| Refactoring | ✅ | Semantic + call graph | "how can I simplify the router?" |
| Performance | ✅ | Semantic + call graph | "what's the slow part?" |
| DataFlow | ✅ | Call graph + type flow | "how does query become vector?" |
| Security | ✅ | Semantic context | "are there input validation issues?" |
| Documentation | ✅ | Semantic context | "what does `ContextWindowCreator` do?" |
| Example | ✅ | Semantic context | "show me how to use `CacheController`" |
| Testing | ✅ | Semantic context | "how should I test the classifier?" |

---

## Context Building

### The Retrieval Pipeline

The context builder implements a sophisticated 7-step pipeline:

#### Step 1: Query Classification
- Determine query type and confidence
- Extract symbols and entities
- Compute specificity score (0-1.0)

#### Step 2: Intent Classification
- Classify into 8 intent types
- Determine retrieval strategy
- Allocate token budget across strategies

#### Step 3: Budget Allocation
```
Total tokens = model_max_tokens
Reserve = system_prompt (150) + query (len/4) + buffer (200) + response (2000)
Available = Total - Reserve
Context budget = Available × 0.85
```

#### Step 4: Multi-Strategy Search
- Execute 5 orthogonal strategies in parallel
- Merge results with score boosting
- Apply inter-strategy boosts for multi-hit chunks

#### Step 5: Graph Expansion
- Expand via call graph (if available)
- Follow calls/called_by edges
- Apply decay factor (0.9 per hop)
- Limit to top-20 neighbors

#### Step 6: Selection
- Use MMR (Maximal Marginal Relevance) for diversity
- Fallback to greedy if MMR fails
- Respect token budget
- Pin exact matches to top

#### Step 7: Source Hydration
- Load actual source code for selected chunks
- Apply line range extraction
- Handle lazy loading for large corpora
- Assemble final ContextWindow

### Memory Management

**Streaming KB Loading**:
- Uses mmap + json.NewDecoder
- Never materialises full KnowledgeBase struct
- Processes FileData objects one at a time
- Peak memory: chunk slice + indices only

**Lazy Content Loading**:
- For corpora > 50k chunks
- Load source code on demand
- Cache frequently accessed files
- LRU eviction for memory management

**Embedding Loading**:
- Binary files loaded via os.ReadFile
- TODO: Switch to mmap for >2GB embedding files
- Vector map: chunk ID → embedding index
- Supports both float32 and SQ8 quantization

---

## Multi-Strategy Retrieval

### Strategy Execution Order

```go
strategies := []struct{
    name    string
    execute func() []ScoredChunk
    boost   float64
}{
    {"kb_exact", kbExactLookup, 2.5},
    {"exact", exactSymbolSearch, 2.0},
    {"partial", partialIdentifierMatch, 1.5},
    {"keyword", keywordSearch, 2.0},
    {"semantic", vectorSearch, 1.5},
}
```

### Strategy Details

#### KB Exact Lookup
- **Input**: Valid symbols from query
- **Process**: Direct lookup in kb.json symbol tables
- **Output**: Chunks with exact symbol definitions
- **Score**: 115-120 (highest precision)
- **Use Case**: Known symbol lookups

#### Exact Symbol Search
- **Input**: Query symbols and chunk names
- **Process**: String matching on chunk IDs and symbols
- **Output**: Chunks with matching names
- **Score**: 100
- **Use Case**: Explicit symbol references

#### Partial Identifier Match
- **Input**: Query tokens
- **Process**: Token-level matching on camelCase/snake_case
- **Output**: Chunks with partial name matches
- **Score**: 80-90
- **Use Case**: Typos, partial names, fuzzy matching

#### Keyword Search
- **Input**: Query keywords
- **Process**: 
  - Small corpora (<5k chunks): Linear scan with symbol boosting
  - Large corpora: TF-IDF with inverted index
- **Output**: Chunks with keyword matches
- **Score**: 40-70
- **Use Case**: Concept queries, no specific symbols

#### Semantic Search
- **Input**: Query embedding
- **Process**:
  - Small corpora (<10k embeddings): Brute-force cosine similarity
  - Large corpora: IVF approximate search (k-means clustering)
- **Output**: Chunks with semantic similarity
- **Score**: 0.5-1.0 (cosine similarity)
- **Use Case**: Concept understanding, analogies
- **Note**: Skipped if embeddings missing or for certain intents

### Merging and Scoring

```go
for _, strategy := range strategies {
    results := strategy.execute()
    for _, result := range results {
        if existing, found := merged[result.ID]; found {
            // Boost for multi-strategy hits
            existing.Score += result.Score * strategy.boost
            existing.MatchType += "+" + result.MatchType
        } else {
            merged[result.ID] = result
        }
    }
}
```

**Scoring Formula**:
```
final_score = base_score + (strategy_boost × multi_hit_bonus)
multi_hit_bonus = 1.5 to 2.0 depending on strategy count
```

### Graph Expansion

```go
func expandViaCallGraph(chunks []ScoredChunk, depth int) []ScoredChunk {
    for _, chunk := range chunks {
        // Follow calls edges
        for _, callee := range chunk.Calls {
            if neighbor := findChunk(callee); neighbor != nil {
                neighbor.Score *= 0.9 // Decay
                expanded = append(expanded, neighbor)
            }
        }
        // Follow called_by edges
        for _, caller := range chunk.CalledBy {
            if neighbor := findChunk(caller); neighbor != nil {
                neighbor.Score *= 0.9 // Decay
                expanded = append(expanded, neighbor)
            }
        }
    }
    return expanded
}
```

**Parameters**:
- Depth: 2 hops (configurable)
- Decay: 0.9 per hop
- Limit: Top-20 neighbors per chunk

---

## Intent Classification

### Intent Types and Characteristics

| Intent | Description | Budget Allocation | Skip Semantic? |
|--------|-------------|------------------|----------------|
| IntentCallers | "who calls X?" | 45% kb_exact, 15% graph | ✅ Yes |
| IntentCallees | "what does X call?" | 45% kb_exact, 15% graph | ✅ Yes |
| IntentSymbolExact | "show me function foo" | 55% kb_exact | ✅ Yes |
| IntentFlow | "trace execution" | 40% graph, 30% semantic | ❌ No |
| IntentConcept | "how does X work?" | 50% semantic, 20% keyword | ❌ No |
| IntentDebug | "why does this crash?" | 35% kb_exact, 35% keyword | ❌ No |
| IntentUnknown | Fallback | Balanced | ❌ No |

### Detection Algorithm

```go
func classifyQueryIntent(query string, symbols []string) QueryIntent {
    queryLower := strings.ToLower(query)
    
    // Priority 1: Call graph keywords
    if containsAny(queryLower, []string{"calls", "called by", "callers", "callees"}) {
        if containsAny(queryLower, []string{"calls", "called by"}) {
            return IntentCallers
        }
        return IntentCallees
    }
    
    // Priority 2: Flow keywords
    if containsAny(queryLower, []string{"trace", "flow", "sequence", "execution"}) {
        return IntentFlow
    }
    
    // Priority 3: Debug keywords
    if containsAny(queryLower, []string{"error", "crash", "fail", "bug", "issue"}) {
        return IntentDebug
    }
    
    // Priority 4: Symbol density
    specificity := calculateSpecificity(query, symbols)
    if specificity > 0.85 {
        return IntentSymbolExact
    }
    
    // Default
    return IntentConcept
}
```

### Budget Allocation Example

For an 8k token model with "how does authentication work?" query:

```
Total tokens: 8192
Query encoding: 12 tokens (50 chars / 4)
System prompt: 150 tokens
Response reserve: 2000 tokens
Safety buffer: 200 tokens
Available: 8192 - 12 - 150 - 2000 - 200 = 5830 tokens
Context budget: 5830 × 0.85 = 4955 tokens

Intent: Concept
Allocation:
  - kb_exact: 20% (991 tokens)
  - keyword: 30% (1486 tokens)
  - semantic: 50% (2478 tokens)
```

---

## Prompt Construction

### Prompt Structure

Every prompt follows a consistent structure:

```
[HEADER: Anti-hallucination instructions]
[BODY: Type-specific reasoning steps]
[FOOTER: Output format requirements]
```

### Header Template

```
You are a code analysis assistant. Answer the following question based ONLY on the provided context.

Rules:
1. Distinguish between:
   - CONFIRMED IN SOURCE: You can see the implementation
   - INFERRED FROM SIGNATURE: You only have the function signature
   - CANNOT DETERMINE: The information is not available

2. Cite your sources:
   - Use "file:lines" format when available
   - Explicitly state "signature only" when applicable
   - List gaps in information

3. Be precise:
   - Don't invent implementation details
   - Don't assume behavior not shown in source
   - Explicitly state what you cannot determine
```

### Body Examples

#### Understanding Query
```
Explain what the queried code does and why it exists.

Your reasoning must cover:
a) What each relevant symbol does, citing source lines when available or
   noting 'signature only' when not.
b) How the pieces interact — trace at least one concrete call path from
   entry point to leaf, naming intermediate functions.
c) Any non-obvious design choices visible in the source or signatures.
d) What remains unclear due to missing source (list each gap explicitly).

Preferred answer shape:
  • One-paragraph summary
  • Bullet breakdown per key symbol (name | file:lines | role)
  • Call-path trace
  • Open questions / gaps
```

#### Architecture Query
```
Describe the architectural structure relevant to the question.

Pre-computed call-graph excerpt:
{call_graph_excerpt}

Reasoning steps:
a) Identify the top-level entry points and the layers they delegate to.
b) Map each layer to its package/directory — use file paths from metadata.
c) Describe the dominant dependency direction (e.g., handler → service → repo).
d) Highlight any architectural violations visible in the call graph
   (e.g., cycles, cross-layer calls).
e) For each claim, cite the call edge or source file that supports it.
f) List structural facts you CANNOT determine (e.g., runtime wiring, DI config).

Output format:
  • Layer diagram (text art or bullet tree)
  • Key call paths (A → B → C, each with file:line when available)
  • Architectural observations
  • Unknowns
```

### Footer Template

```
Provide your answer in the following format:

[ANSWER]
Your detailed answer here
[/ANSWER]

[SOURCES]
List of sources used (file:lines)
[/SOURCES]

[GAPS]
Any information that could not be determined
[/GAPS]
```

---

## Performance Optimization

### Caching Strategy

**Cache Key**: `hash(query + kb_checksum)`

**Cache Entries**:
- Query classification result
- Context window (for identical queries)
- LLM response (final answer)

**TTL**: 24 hours (configurable)

**Cache Storage**: SQLite database in `.eulix/history.db`

### Parallelization

**Multi-Strategy Search**:
- All 5 strategies execute concurrently
- Results merged in a single pass
- No blocking between strategies

**Chunk Loading**:
- Parallel file reading for source hydration
- Batched embedding loading
- Concurrent graph expansion

### Memory Optimization

**Streaming KB Loading**:
- Never load full KB into memory
- Process FileData objects sequentially
- Free memory immediately after use

**Lazy Loading**:
- Load source code on demand
- LRU cache for frequently accessed files
- Configurable memory limits

**Approximate Search**:
- IVF clustering for large embedding sets
- Inverted index for large corpora
- Sampling for very large datasets

### Performance Characteristics

| Operation | Time Complexity | Space Complexity | Notes |
|-----------|----------------|------------------|-------|
| Query Classification | O(1) | O(1) | Regex matching |
| Symbol Validation | O(n) | O(n) | n = number of symbols |
| Multi-Strategy Search | O(k) | O(m) | k = strategies, m = candidates |
| Graph Expansion | O(d × e) | O(e) | d = depth, e = edges |
| MMR Selection | O(m²) | O(m) | m = candidates |
| Source Hydration | O(f) | O(f) | f = files to load |

---

## Integration Points

### With Parser

**Input from Parser**:
- `kb.json`: Full codebase structure
- `kb_index.json`: Symbol index
- `kb_call_graph.json`: Call relationships
- `kb_summary.json`: Project metadata

**Usage**:
- Classifier validates symbols against kb_index.json
- Context builder loads kb.json for chunk definitions
- Router uses call graph for dependency queries

### With Embedder

**Input from Embedder**:
- `embeddings.bin`: Vector embeddings
- `vectors.bin`: Embedding→chunk ID map

**Usage**:
- Context builder loads embeddings for semantic search
- Vector search uses cosine similarity for ranking
- Supports both float32 and SQ8 quantization

### With LLM

**Integration**:
- Constructs type-specific prompts
- Sends context window + query to LLM
- Parses structured responses
- Handles errors and retries

**Supported LLMs**:
- Ollama (local)
- Anthropic Claude (remote)
- Extensible to other providers

### With Cache

**Integration**:
- Caches query classifications
- Caches LLM responses
- Cache invalidation on KB changes
- Configurable TTL

**Cache Storage**:
- SQLite database in `.eulix/history.db`
- Schema: query_hash, kb_checksum, response, timestamp

---

## Known Limitations

### Current Limitations

| Limitation | Impact | Status |
|------------|--------|--------|
| KB exact lookup results discarded by make(map) reset | Highest-quality signal never influences context | 🐛 Bug - to be fixed |
| Vector search skipped if embeddings missing | Falls back to keyword-only silently | Acceptable fallback |
| Token count approximated as len(bytes)/4 | Can overflow context window | Should use per-model tokenizer |
| Single-winner classification | Overlapping queries may route incorrectly | Classifier accuracy degrades on ambiguity |
| No streaming LLM responses | Poor UX for long answers | Planned enhancement |
| JavaScript not supported | JS files fail to parse | Planned implementation |
| Fixed token budgets | May under/allocate for complex queries | Dynamic budgeting planned |

### Design Trade-offs

| Trade-off | Rationale | Future Consideration |
|-----------|-----------|---------------------|
| Regex-based classification | Fast and covers 95% of cases | ML-based classification for edge cases |
| Single-pass KB loading | Memory efficiency | Random access index for large KBs |
| Fixed strategy weights | Simpler implementation | Dynamic weight learning |
| No streaming responses | Simpler implementation | Streaming for long answers |
| ThreadPool capped at 4 | Chunking is allocator-bound | Dynamic sizing based on workload |

---

## Extension Points

### Adding New Query Types

1. **Add QueryType enum** in `contextTypes.go`
2. **Add pattern** in `classifier_patterns.go`
3. **Add handler** in `router_handlers.go`
4. **Add prompt template** in `prompts.go`
5. **Update routing logic** in `router_core.go`

### Adding New Retrieval Strategies

1. **Implement strategy function** in `context_search.go`
2. **Add to strategies list** in `multiStrategySearch`
3. **Define scoring logic** and boost factor
4. **Add MatchType** for result labeling

### Custom Intent Classification

1. **Add IntentType enum** in `context_intent.go`
2. **Add detection logic** in `classifyQueryIntent`
3. **Define budget allocation** in `allocateBudget`
4. **Update strategy weights** per intent

### Custom Prompt Templates

1. **Add function** in `taskBodies` map in `prompts.go`
2. **Define reasoning steps** specific to query type
3. **Specify output format** requirements
4. **Add anti-hallucination** instructions if needed

---

## Conclusion

The query package is the intelligence layer that makes Eulix more than just a code search tool. By combining:

- **Multi-strategy retrieval** for comprehensive coverage
- **Intent-aware budgeting** for efficient context allocation
- **Type-specific prompts** for accurate LLM reasoning
- **Graceful degradation** for robustness

It provides a sophisticated code understanding system that scales from small projects to massive codebases while maintaining accuracy and performance.

The design prioritizes:
- **Accuracy** through orthogonal search strategies
- **Efficiency** through caching and smart budgeting
- **Robustness** through graceful degradation
- **Extensibility** through modular architecture

This makes the query package suitable for a wide range of use cases from simple symbol lookup to complex architectural analysis.
