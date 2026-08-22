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
	"eulix/internal/utils"
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

	// Start auto-flush every 5 seconds
	cb.debugLog.StartAutoFlush(5 * time.Second)

	queryEmbedder, err := embeddings.VectorWeaver(cfg.Embeddings.Model)
	if err != nil {
		cb.debugLog.Log("Failed to initialize embedder: %v", err)
		cb.debugLog.Close()
		return nil, fmt.Errorf("failed to initialize embedder: %w", err)
	}
	cb.queryEmbedder = queryEmbedder

	if err := cb.loadChunks(); err != nil {
		cb.debugLog.Log("Failed to load chunks: %v", err)
		cb.debugLog.Close()
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
	cb.debugLog.Log("CONTEXT-BUILDER INITIALIZED: %d chunks, %d subsystem nodes, %d noise paths",
		len(cb.chunks), len(cb.subsystemTree), len(cb.noisePaths))
	return cb, nil
}

// BuildContext constructs the ContextWindow for the target query.
func (cb *ContextBuilder) BuildContext(query string) (*utils.ContextWindow, error) {
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

func (cb *ContextBuilder) buildContextInternal(query string, maxLinesDefault int) (*utils.ContextWindow, *DebugTrace, error) {
	start := time.Now()
	var trace *DebugTrace
	if cb.config.Project.DebugConfig {
		trace = &DebugTrace{Query: query}
	}
	explicitAnchor := extractExplicitAnchors(query)
	gate := buildPathGate(explicitAnchor)

	cb.debugLog.Log("\n=== NEW QUERY ===")
	cb.debugLog.Log("Query: %s", query)
	startRetrival := time.Now()
	intent := cb.classifyQueryIntent(query)
	trace.Intent = intent
	cb.debugLog.Log("Intent: %d (specificity: %.2f, confidence: %.2f)",
		intent.Type, intent.Specificity, intent.Confidence)
	cb.debugLog.Log("Embedder called Query is getting embedded")
	etime := time.Now()
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
	elapsed := time.Since(etime)
	cb.debugLog.Log("Embedder took %d ms to run", elapsed.Milliseconds())
	budget := cb.allocateBudget(query, intent)
	trace.Budget = budget
	cb.debugLog.Log("Budget: %d tokens for context (total: %d)",
		budget.ContextBudget, budget.MaxTokens)

	anchorFiles := make(map[string]bool)
	for _, ea := range explicitAnchor {
		if ea.File != "" {
			anchorFiles[ea.File] = true
		}
	}

	anchors := cb.exactSymbolSearch(query)
	filteredAnchors := make([]ScoredChunk, 0, len(anchors))
	for _, a := range anchors {
		// Only consider high-confidence exact matches that aren't boilerplate symbols
		if a.Score >= 90.0 && !cb.isBoilerplateSymbol(a.Name) {
			filteredAnchors = append(filteredAnchors, a)
		}
	}
	if len(filteredAnchors) > 2 {
		filteredAnchors = filteredAnchors[:2]
	}
	for _, a := range filteredAnchors {
		anchorFiles[a.File] = true
	}
	cb.debugLog.Log("Found %d exact anchors", len(filteredAnchors))

	var callSiteResults []ScoredChunk
	if intent.Type == IntentCallers || intent.Type == IntentCallees {
		callSiteResults = cb.findCallSites(query, intent)
		cb.debugLog.Log("Found %d call sites", len(callSiteResults))
	}

	candidateLimit := cb.candidateLimitForIntent(intent)
	candidates := cb.multiStrategySearch(query, candidateLimit, intent, budget.StrategyWeights, trace, qEmb)
	trace.TotalCandidates = len(candidates)
	cb.debugLog.Log("Multi-strategy search: %d candidates", len(candidates))

	// Filter weak candidates before graph expansion/MMR to prevent vendor
	// boilerplate matching single tokens from consuming budget.
	// Anchors and call-site results are exempt (already high-confidence).
	preFloorCount := len(candidates)
	candidates = ApplyPreMMRFloor(candidates, &cb.config.RetrievalConfig)
	if preFloorCount != len(candidates) {
		cb.debugLog.Log("Pre-MMR floor: %d → %d candidates (ratio=%.2f)",
			preFloorCount, len(candidates), cb.config.RetrievalConfig.PreMMRScoreFloorRatio)
		if trace != nil {
			trace.Warnings = append(trace.Warnings,
				fmt.Sprintf("pre-MMR floor filtered %d → %d candidates", preFloorCount, len(candidates)))
		}
	}

	candidates = mergeWithPriority(anchors, callSiteResults, candidates)
	cb.debugLog.Log("After merge: %d candidates", len(candidates))

	var expanded []ScoredChunk
	if cb.hasCallGraph {
		expanded = cb.buildContextWithGraph(candidates, budget.ContextBudget, intent)
		cb.debugLog.Log("Graph expansion: %d chunks", len(expanded))
	} else {
		cb.debugLog.Log("Building context without call graphs as they werent find")
		expanded = cb.buildContextWithoutGraph(candidates, budget.ContextBudget)
		cb.debugLog.Log("NO CALL GARAPHS: %d Chunks Expanded", len(expanded))
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
	retrivalDuration := time.Since(startRetrival)
	cb.debugLog.Log("=== QUERY COMPLETE ===")
	cb.debugLog.Log("Final context: %d chunks, %d tokens, %d sources",
		len(ctx.Chunks), ctx.TotalTokens, len(ctx.Sources))
	cb.debugLog.Log("Retrival Duration: %v\n", retrivalDuration)
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
			base = 80
		} else {
			base = 150
		}
	case intent.Type == IntentConcept || intent.Type == IntentFlow:
		if intent.Specificity > 0.8 {
			base = 150
		} else {
			base = 250
		}
	case intent.Specificity > 0.8:
		base = 150
	case intent.Specificity > 0.5:
		base = 200
	default:
		base = 300
	}

	limit := int(float64(base) * scale)
	if limit > 1000 {
		limit = 1000
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

func (cb *ContextBuilder) assembleContext(chunks []Chunk) *utils.ContextWindow {
	totalTokens := 0
	sources := make(map[string]bool, len(chunks))
	ctxChunks := make([]utils.ContextChunk, len(chunks))
	for i, c := range chunks {
		totalTokens += c.Tokens + 20
		sources[c.File] = true
		ctxChunks[i] = utils.ContextChunk{
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
	return &utils.ContextWindow{Chunks: ctxChunks, TotalTokens: totalTokens, Sources: srcList}
}
