//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query is responsible for query identification,
// running functions/tools based on query and build context window.

/*
This file  orchestrates the full retrieval pipeline:
The Eulix's context window builder the core RAG engine
that transforms a natural language query into a ranked, token-budgeted slice of
code context optimized for LLM reasoning.

  1. Query classification (intent detection + specificity scoring)
  2. Budget allocation (token limits based on query intent and model capacity)
  3. Multi-strategy candidate search:
     - Exact symbol anchors (direct matches in function/class names)
     - Call-site discovery (caller/callee relationships)
     - Semantic search (embeddings-based similarity)
     - Keyword search (inverted index term matching)
     - Symbol overlap (shared identifiers between chunks)
  4. Graph expansion (if call graph is available)
  5. Selection (MMR diversity-aware or greedy fallback)
  6. Source code hydration (map chunks back to actual file content)
  7. Assembly (final ContextWindow for the LLM)

Memory and performance notes:
  - Embeddings are optional; queries degrade gracefully to keyword+symbol scoring
  - Call graphs are optional; context expansion works without them
  - Knowledge base (kb.json) is always loaded; it provides the chunk universe
  - For large corpora (> 50k chunks), chunk content is lazy-loaded on demand
  - Boilerplate symbols (ctx, err, i, j) are pre-filtered to reduce false matches

File loading happens in context_loader.go:
  - kb.json + kb_index.json → chunk definitions
  - embeddings.bin → vector embeddings
  - vectors.bin → ID→embedding index
  - kb_call_graph.json → call relationships

See:
  - context_loader.go: artifact loading and boilerplate detection setup
  - context_search.go: multi-strategy search implementation
  - context_mmr.go: Maximal Marginal Relevance selection
  - content_intent.go: query intent classification & token budget allocation
*/

package query

import (
	"fmt"
	"time"

	"eulix/internal/config"
	"eulix/internal/embeddings"
	"eulix/internal/llm"
	"eulix/internal/types"
)

// Constants
const (
	BinaryVersion = uint32(4)
	MagicBytes    = "EULX"
)

//  Constructor

func ContextWindowCreator(eulixDir string, cfg *config.Config, llmClient *llm.Client, sourceRoot string) (*ContextBuilder, error) {
	cb := &ContextBuilder{
		eulixDir:   eulixDir,
		config:     cfg,
		llmClient:  llmClient,
		vectorMap:  make(map[string]int),
		hydrateIdx: make(map[string]map[[2]int]func() string),
		sourceRoot: sourceRoot,
		debugLog:   NewDebugLogger(eulixDir),
	}
	cb.debugLog.Log("Initializing ContextBuilder with source root: %s", sourceRoot)

	queryEmbedder, err := embeddings.VectorWeaver(cfg.Embeddings.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize embedder: %w", err)
	}
	cb.queryEmbedder = queryEmbedder

	if err := cb.loadChunks(); err != nil {
		return nil, fmt.Errorf("failed to load chunks from KB: %w", err)
	}

	if err := cb.loadEmbeddings(); err != nil {
		cb.debugLog.Log("Embeddings not loaded: %v", err)
		cb.hasEmbeddings = false
	} else {
		cb.hasEmbeddings = true
		cb.debugLog.Log("Loaded %d embeddings", len(cb.embeddings))
	}

	if err := cb.loadVectorMap(); err != nil {
		cb.debugLog.Log("Vector map not loaded: %v", err)
		cb.vectorMap = make(map[string]int)
	} else {
		cb.debugLog.Log("Loaded %d vector mappings", len(cb.vectorMap))
	}

	// Single unified call graph load — runs after chunks are ready
	cb.loadAndIndexCallGraph()

	cb.hasKB = true
	cb.debugLog.Log("ContextBuilder initialized successfully")
	return cb, nil
}

func (cb *ContextBuilder) BuildContext(query string) (*types.ContextWindow, error) {
	maxLines := cb.config.Project.MaxLines
	ctx, _, err := cb.buildContextInternal(query, maxLines)
	if err != nil {
		return nil, err
	}

	if cb.config.Project.DebugConfig {
		if err := cb.writeContextToFile(ctx); err != nil {
			// don’t fail main flow for debug write issues
			fmt.Printf("failed to write debug context: %v\n", err)
		}
	}

	return ctx, nil
}

func (cb *ContextBuilder) buildContextInternal(query string, maxLinesDefault int) (*types.ContextWindow, *DebugTrace, error) {
	start := time.Now()
	trace := &DebugTrace{Query: query}
	explicitAnchor := extractExplicitAnchors(query)
	gate := buildPathGate(explicitAnchor)

	cb.debugLog.Log("\n=== NEW QUERY ===")
	cb.debugLog.Log("Query: %s", query)

	intent := cb.classifyQueryIntent(query)
	trace.Intent = intent
	cb.debugLog.Log("Intent: %d (specificity: %.2f, confidence: %.2f)",
		intent.Type, intent.Specificity, intent.Confidence)

	var qEmb []float32
	skipSemantic := intent.Type == IntentCallers ||
		intent.Type == IntentCallees ||
		intent.Specificity > 0.85
	if cb.hasEmbeddings && !skipSemantic {
		if emb, err := cb.queryEmbedder.EmbedQueryBinary(query); err == nil {
			qEmb = emb
		} else {
			trace.Warnings = append(trace.Warnings, "query embedding failed: "+err.Error())
		}
	}

	budget := cb.allocateBudget(query, intent)
	trace.Budget = budget
	cb.debugLog.Log("Budget: %d tokens for context (total: %d)",
		budget.ContextBudget, budget.MaxTokens)

	anchors := cb.exactSymbolSearch(query)
	if len(anchors) > 2 {
		anchors = anchors[:2]
	}
	anchorFiles := make(map[string]bool)
	for _, a := range anchors {
		anchorFiles[a.File] = true
	}
	cb.debugLog.Log("Found %d exact anchors", len(anchors))

	var callSiteResults []ScoredChunk
	if intent.Type == IntentCallers || intent.Type == IntentCallees {
		callSiteResults = cb.findCallSites(query, intent)
		cb.debugLog.Log("Found %d call sites", len(callSiteResults))
	}

	candidateLimit := cb.candidateLimitForIntent(intent)
	// Passes budget weights so multiStrategySearch uses the same allocation
	candidates := cb.multiStrategySearch(query, candidateLimit, intent, budget.StrategyWeights, trace, qEmb)
	trace.TotalCandidates = len(candidates)
	cb.debugLog.Log("Multi-strategy search: %d candidates", len(candidates))

	candidates = mergeWithPriority(anchors, callSiteResults, candidates)
	cb.debugLog.Log("After merge: %d candidates", len(candidates))

	var expanded []ScoredChunk
	if cb.hasCallGraph {
		expanded = cb.buildContextWithGraph(candidates, budget.ContextBudget, intent)
		cb.debugLog.Log("Graph expansion: %d chunks", len(expanded))
	} else {
		expanded = cb.buildContextWithoutGraph(candidates, budget.ContextBudget)
	}

	var selected []Chunk
	if cb.hasEmbeddings {
		trace.SelectionMethod = "mmr"
		selected = cb.mmrSelect(expanded, budget.ContextBudget, qEmb, anchorFiles, trace, gate)
	} else {
		trace.SelectionMethod = "greedy"
		if gate.active {
			filtered := make([]ScoredChunk, 0, len(expanded))
			for _, sc := range expanded {
				if gate.Pass(sc.File) {
					sc.Score *= gate.Boost(sc.File)
					filtered = append(filtered, sc)
				}
			}
			cb.debugLog.Log("Gate filtered greedy candidates: %d → %d",
				len(expanded), len(filtered))
			expanded = filtered
		}
		selected = cb.selectChunks(expanded, budget.ContextBudget)
		for i, c := range selected {
			trace.ChunkTraces = append(trace.ChunkTraces, ChunkTrace{
				ID: c.ID, File: c.File,
				Lines:  [2]int{c.StartLine, c.EndLine},
				Tokens: c.Tokens, Rank: i + 1, Included: true,
			})
		}
	}
	cb.debugLog.Log("Selected %d chunks via %s", len(selected), trace.SelectionMethod)

	if cb.lazyContent {
		cb.hydrateContent(selected)
		cb.debugLog.Log("Hydrated KB content for %d chunks", len(selected))
	}

	// Source hydration gets the full remaining budget — applyBudget's
	// bin-packing already handles fitting; no second discount needed.
	selected = cb.hydrateSourceCode(selected, budget.ContextBudget, maxLinesDefault)

	ctx := cb.assembleContext(selected)
	trace.TotalTokens = ctx.TotalTokens
	trace.Duration = time.Since(start)

	cb.debugLog.Log("=== QUERY COMPLETE ===")
	cb.debugLog.Log("Final context: %d chunks, %d tokens, %d sources",
		len(ctx.Chunks), ctx.TotalTokens, len(ctx.Sources))
	cb.debugLog.Log("Duration: %v\n", trace.Duration)

	cb.mu.Lock()
	cb.lastTrace = trace
	cb.mu.Unlock()
	return ctx, trace, nil
}

// candidateLimitForIntent avoids pulling 120 candidates for narrow queries
func (cb *ContextBuilder) candidateLimitForIntent(intent QueryIntent) int {
	scale := 1.0
	n := len(cb.chunks)
	switch {
	case n > 500_000:
		scale = 4.0
	case n > 100_00:
		scale = 2.5
	case n > 50_000:
		scale = 1.5
	}

	var base int
	switch {
	case intent.Type == IntentCallers || intent.Type == IntentCallees:
		if intent.Specificity > 0.9 {
			base = 5
		} else {
			base = 30
		}
	case intent.Type == IntentConcept || intent.Type == IntentFlow:
		if intent.Specificity > 0.8 {
			base = 15
		} else {
			base = 50
		}
	case intent.Specificity > 0.8:
		base = 20
	case intent.Specificity > 0.5:
		base = 50
	default:
		base = 80
	}
	// Hard cap: MMR over 600 candidates is still fast beyound that
	// just adds noise without improving recall.
	limit := int(float64(base) * scale)
	if limit > 600 {
		limit = 600
	}
	return limit
}

// mergeWithPriority blends pre-computed high-priority slices (anchors, callsites)
// into the main candidate list, ensuring they appear at the top.
func mergeWithPriority(anchors, callSites, candidates []ScoredChunk) []ScoredChunk {
	seen := make(map[string]bool, len(anchors)+len(callSites)+len(candidates))
	out := make([]ScoredChunk, 0, len(anchors)+len(callSites)+len(candidates))

	add := func(sc ScoredChunk) {
		if !seen[sc.ID] {
			seen[sc.ID] = true
			out = append(out, sc)
		}
	}
	for _, sc := range anchors {
		add(sc)
	}
	for _, sc := range callSites {
		add(sc)
	}
	for _, sc := range candidates {
		add(sc)
	}
	return out
}

func (cb *ContextBuilder) assembleContext(chunks []Chunk) *types.ContextWindow {
	totalTokens := 0
	sources := make(map[string]bool, len(chunks))
	ctxChunks := make([]types.ContextChunk, len(chunks))
	for i, c := range chunks {
		totalTokens += c.Tokens + 20
		sources[c.File] = true
		ctxChunks[i] = types.ContextChunk{
			File:       c.File,
			StartLine:  c.StartLine,
			EndLine:    c.EndLine,
			Content:    c.Content,
			Importance: c.Importance,
		}
	}
	srcList := make([]string, 0, len(sources))
	for s := range sources {
		srcList = append(srcList, s)
	}
	return &types.ContextWindow{Chunks: ctxChunks, TotalTokens: totalTokens, Sources: srcList}
}
