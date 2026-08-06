//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query is responsible for query identification,
// running functions/tools based on query and build context window.

package query

import (
	"fmt"
	"time"

	"eulix/internal/config"
	"eulix/internal/embeddings"
	"eulix/internal/llm"
	"eulix/internal/types"
)

const (
	BinaryVersion = uint32(4)
	MagicBytes    = "EULX"
)

// ContextWindowCreator initializes ContextBuilder, loads index artifacts, and sets up search resources.
func ContextWindowCreator(eulixDir string, cfg *config.Config, llmClient *llm.Client, sourceRoot string) (*ContextBuilder, error) {
	cb := &ContextBuilder{
		eulixDir:      eulixDir,
		config:        cfg,
		llmClient:     llmClient,
		vectorMap:     make(map[string]int),
		hydrateIdx:    make(map[string]map[[2]int]func() string),
		sourceRoot:    sourceRoot,
		debugLog:      NewDebugLogger(eulixDir),
		subsystemTree: make([]*SubsystemNode, 0, 128),
		noisePaths:    make([]string, 0, 32),
	}
	cb.init()
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

	cb.loadAndIndexCallGraph()
	cb.hasKB = true
	cb.debugLog.Log("ContextBuilder initialized: %d chunks, %d subsystem nodes, %d noise paths",
		len(cb.chunks), len(cb.subsystemTree), len(cb.noisePaths))
	return cb, nil
}

// BuildContext constructs the ContextWindow for the target query.
func (cb *ContextBuilder) BuildContext(query string) (*types.ContextWindow, error) {
	maxLines := cb.config.Project.MaxLines
	ctx, _, err := cb.buildContextInternal(query, maxLines)
	if err != nil {
		return nil, err
	}

	if cb.config.Project.DebugConfig {
		if err := cb.writeContextToFile(ctx); err != nil {
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
				ID:       c.ID,
				File:     c.File,
				Lines:    [2]int{c.StartLine, c.EndLine},
				Tokens:   c.Tokens,
				Rank:     i + 1,
				Included: true,
			})
		}
	}
	cb.debugLog.Log("Selected %d chunks via %s", len(selected), trace.SelectionMethod)

	if cb.lazyContent {
		cb.hydrateContent(selected)
		cb.debugLog.Log("Hydrated KB content for %d chunks", len(selected))
	}

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

func (cb *ContextBuilder) candidateLimitForIntent(intent QueryIntent) int {
	scale := 1.0
	n := len(cb.chunks)
	switch {
	case n > 500_000:
		scale = 4.0
	case n > 100_000:
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

	limit := int(float64(base) * scale)
	if limit > 600 {
		limit = 600
	}
	return limit
}

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
