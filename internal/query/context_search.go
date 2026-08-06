//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides context window building and query routing for Eulix's RAG system.

Key Responsibilities:
  - Orchestrates multi-strategy retrieval (KB exact, partial identifier, keyword, and vector search)
  - Deduplicates and merges candidate scores using strategy-specific boost multipliers
  - Adjusts strategy weights and search paths dynamically based on query intent and corpus scale
*/
package query

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"eulix/internal/types"
)

// explicitAnchorPatterns covers the common ways a user pins a location:
//
//	full path + line:   drivers/gpu/drm/amd/display/dc/dml/dcn32_fpu.c:847
//	filename + line:    dcn32_fpu.c:847
//	filename + func:    dcn32_fpu.c calculate_wm_and_dlg
//	func + line:        calculate_wm_and_dlg:847  (rarer but valid)
//	bare path fragment: drivers/gpu/drm/amd/display  (directory prefix)
var (
	rePathLine = regexp.MustCompile(`([\w./-]+\.\w+):(\d+)`)
	reFuncLine = regexp.MustCompile(`(\w{4,}):(\d+)`)
	reFilename = regexp.MustCompile(`([\w-]+\.(?:c|h|go|rs|py|ts|cpp|hpp)):(\b)`)
	rePathFrag = regexp.MustCompile(`((?:\w+/){2,}\w+)`)
)

// ExplicitAnchor describes a user-specified location extracted from the query.
type ExplicitAnchor struct {
	File     string
	FuncName string
	Line     int
	Score    float64
}

type scoredIdx struct {
	idx   int
	score float64
}

// multiStrategySearch executes up to five retrieval strategies (kb_exact, exact, partial, keyword, semantic),
// merging duplicate chunk scores with multi-strategy boosts and sorting by match type and score.
// If trace is non-nil, it records performance and score metrics for each executed strategy.
func (cb *ContextBuilder) multiStrategySearch(
	query string,
	topK int,
	intent QueryIntent,
	weights map[string]float64,
	trace *DebugTrace,
	qEmb []float32,
) []ScoredChunk {
	all := make(map[string]ScoredChunk, topK*2)
	anchors := extractExplicitAnchors(query)
	gate := buildPathGate(anchors)

	if len(anchors) > 0 {
		cb.debugLog.Log("Explicit anchors: %d, gate active: %v (required: %v)",
			len(anchors), gate.active, gate.required)
		for _, a := range anchors {
			cb.debugLog.Log("  anchor: file=%q func=%q line=%q score=%.0f",
				a.File, a.FuncName, a.Line, a.Score)
		}
		anchorsHits := cb.explicitAnchorSearch(anchors)
		anchorsHits = gate.applyGate(anchorsHits)
		cb.debugLog.Log("Anchors search: %d hits", len(anchorsHits))
		for _, sc := range anchorsHits {
			all[sc.ID] = sc
		}
		highConfidence := 0
		for _, sc := range anchorsHits {
			if sc.Score >= 250.0 {
				highConfidence++
			}
		}
		if highConfidence > 0 && topK < highConfidence+20 {
			topK = highConfidence + 20
		}
	}

	run := func(name string, fn func() []ScoredChunk, multiBoost float64) {
		t0 := time.Now()
		results := fn()
		results = gate.applyGate(results)
		st := StrategyTrace{Name: name, Found: len(results), Duration: time.Since(t0)}
		sum := 0.0
		for _, m := range results {
			m.MatchType = name
			if ex, ok := all[m.ID]; ok {
				m.Score = math.Max(ex.Score, m.Score) + multiBoost
				m.MatchType = ex.MatchType + "+" + name
				m.IsExact = ex.IsExact || name == "exact" || name == "kb_exact"
			} else {
				m.IsExact = name == "exact" || name == "kb_exact"
			}
			all[m.ID] = m
			if m.Score > st.TopScore {
				st.TopScore = m.Score
			}
			sum += m.Score
		}
		if len(results) > 0 {
			st.AvgScore = sum / float64(len(results))
		}
		if trace != nil {
			trace.Strategies = append(trace.Strategies, st)
		}
	}

	if cb.hasKB {
		run("kb_exact", func() []ScoredChunk { return cb.kbExactLookup(query, intent) }, 2.5)
	}
	run("exact", func() []ScoredChunk { return cb.exactSymbolSearch(query) }, 2.0)
	run("partial", func() []ScoredChunk { return cb.partialIdentifierMatch(query) }, 1.5)

	// Use weights from allocateBudget
	kwTopK := int(float64(topK) * (0.3 + 0.4*weights["keyword"]))
	syms := extractPotentialSymbols(query)
	run("keyword", func() []ScoredChunk {
		var res []ScoredChunk
		if cb.invertedIdx != nil {
			res = cb.invertedKeywordSearchBM25(query, kwTopK)
		} else {
			res = cb.keywordSearch(query, kwTopK)
		}
		for i := range res {
			content := res[i].Content
			if content == "" {
				content = cb.hydrateOne(res[i].Chunk)
			}
			for _, sym := range syms {
				if strings.EqualFold(res[i].Name, sym) {
					continue
				}
				if strings.Contains(content, "."+sym+"(") || strings.Contains(content, sym+"(") {
					res[i].Score += 25.0
					break
				}
			}
			for _, sym := range syms {
				if strings.HasPrefix(strings.ToLower(res[i].Name), "_"+strings.ToLower(sym)) {
					res[i].Score -= 10.0
				}
			}
		}
		return res
	}, 2.0)

	skipSemantic := intent.Type == IntentCallers ||
		intent.Type == IntentCallees ||
		intent.Specificity > 0.85
	if cb.hasEmbeddings && !skipSemantic && qEmb != nil {
		semTopK := int(float64(topK) * (0.2 + 0.5*weights["semantic"]))
		run("semantic", func() []ScoredChunk {
			var raw []ScoredChunk
			if cb.ivfIndex != nil {
				raw = cb.vectorSearchIVF(qEmb, semTopK, 0.5)
			} else {
				raw = cb.vectorSearch(qEmb, semTopK, 0.5)
			}
			for i := range raw {
				raw[i].Score *= 20.0
			}
			return raw
		}, 1.5)
	}

	result := make([]ScoredChunk, 0, len(all))
	for _, sc := range all {
		result = append(result, sc)
	}
	// Subsystem-aware boosting replaces old boostBySubSystemPath call.
	// we drive query tokens the same way B<25 does so the subsystem
	// detector sees the same vocabulary the keyword search used.
	queryTokens := extractQueryKeywords(strings.ToLower(query))
	detected := detectQuerySubsystems(cb.subsystemTree, queryTokens)
	detected = filterNoiseSubsystems(detected, cb.noisePaths)
	if len(detected) > subsysFinalK {
		detected = detected[:subsysFinalK]
	}
	if len(detected) > 0 {
		cb.debugLog.Log("Detected subsystem (%d): top=%q score=%.2f",
			len(detected), detected[0].node.Path, detected[0].score)
		if trace != nil {
			for _, ds := range detected {
				trace.Warnings = append(trace.Warnings,
					fmt.Sprintf("subsystem: %s (score=%.2f chunks=%d)",
						ds.node.Path, ds.score, ds.node.TotalChunks))
			}
		}
	}
	boostByDetectedSubsystems(result, detected, cb.noisePaths)
	result = filterTestDocChunks(result)
	for i := range result {
		f := result[i].File
		if strings.Contains(f, "/tests/") ||
			strings.Contains(f, "/test_") ||
			strings.Contains(f, "fake_") ||
			strings.Contains(f, "_test.go") {
			if intent.Type == IntentConcept || intent.Type == IntentFlow {
				result[i].Score *= 0.4
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].IsExact != result[j].IsExact {
			return result[i].IsExact
		}
		return result[i].Score > result[j].Score
	})
	if len(result) > topK {
		result = result[:topK]
	}
	return result
}

// exactSymbolSearch performs direct matching on chunk names and symbols.
// Returns chunks whose name or symbol list exactly matches (case-insensitive)
// a token extracted from the query.
func (cb *ContextBuilder) exactSymbolSearch(query string) []ScoredChunk {
	potSyms := extractPotentialSymbols(query)
	scored := make([]ScoredChunk, 0)
	for _, chunk := range cb.chunks {
		nameLow := strings.ToLower(chunk.Name)
		for _, qs := range potSyms {
			if nameLow == strings.ToLower(qs) {
				scored = append(scored, ScoredChunk{
					Chunk: chunk, Score: 100.0,
					MatchDetails: "Exact name: " + chunk.Name,
				})
				break
			}
		}
		for _, sym := range chunk.Symbols {
			symLow := strings.ToLower(sym)
			for _, qs := range potSyms {
				if symLow == strings.ToLower(qs) {
					scored = append(scored, ScoredChunk{
						Chunk: chunk, Score: 90.0,
						MatchDetails: "Symbol: " + sym,
					})
					break
				}
			}
		}
	}
	return scored
}

// partialIdentifierMatch performs sub-token matching across camelCase and snake_case identifiers.
//
// Key Behaviors:
//   - Tokenizes chunk names and symbols into constituent sub-words.
//   - Scores exact and substring token matches with deduplication by chunk ID.
//   - Filters out weak matches requiring multi-token agreement or a high-confidence single match.
func (cb *ContextBuilder) partialIdentifierMatch(query string) []ScoredChunk {
	qTokens := extractPotentialSymbols(query)

	// Pre-filter using symbol index
	candidateIdxs := make(map[int]bool, 100)
	for _, qt := range qTokens {
		qtLow := strings.ToLower(qt)
		if idxs, ok := cb.symbolIndex[qtLow]; ok {
			for _, idx := range idxs {
				candidateIdxs[idx] = true
			}
		}
	}

	// Fallback: scan names only (no content hydration)
	if len(candidateIdxs) == 0 {
		for i, chunk := range cb.chunks {
			nameLow := strings.ToLower(chunk.Name)
			for _, qt := range qTokens {
				if strings.Contains(nameLow, strings.ToLower(qt)) {
					candidateIdxs[i] = true
					break
				}
			}
		}
	}

	scored := make([]ScoredChunk, 0, len(candidateIdxs))
	seen := make(map[string]bool)

	for idx := range candidateIdxs {
		chunk := cb.chunks[idx]
		cTokens := splitIdentifierToTokens(chunk.Name)
		cNameLow := strings.ToLower(chunk.Name)
		matchCount, totalScore := 0, 0.0
		matchedToks := []string{}

		for _, qt := range qTokens {
			qtLow := strings.ToLower(qt)
			for _, ct := range cTokens {
				if ct == qtLow {
					matchCount++
					totalScore += 15.0
					matchedToks = append(matchedToks, ct)
					break
				}
			}
			if strings.Contains(cNameLow, qtLow) && matchCount == 0 {
				totalScore += 8.0
			}
		}
		for _, sym := range chunk.Symbols {
			symToks := splitIdentifierToTokens(sym)
			symLow := strings.ToLower(sym)
			for _, qt := range qTokens {
				qtLow := strings.ToLower(qt)
				for _, st := range symToks {
					if st == qtLow {
						matchCount++
						totalScore += 12.0
						matchedToks = append(matchedToks, st)
						break
					}
				}
				if strings.Contains(symLow, qtLow) && matchCount == 0 {
					totalScore += 6.0
				}
			}
		}
		if (matchCount >= 2 || (matchCount == 1 && totalScore >= 15)) && !seen[chunk.ID] {
			seen[chunk.ID] = true
			scored = append(scored, ScoredChunk{
				Chunk: chunk, Score: totalScore,
				MatchDetails: "Partial: " + strings.Join(uniqueStrings(matchedToks), ", "),
			})
		}
	}
	return scored
}

// keywordSearch performs pre-filtered term frequency scoring against chunk candidates.
//
// Key Behaviors:
//   - Pre-filters candidate chunks using cb.symbolIndex to avoid O(n) full scans.
//   - Scores candidate matches across symbol names, content, file paths, and chunk types.
//   - Hydrates content on demand for top-scoring lazy-loaded chunks.
func (cb *ContextBuilder) keywordSearch(query string, topK int) []ScoredChunk {
	qLow := strings.ToLower(query)
	keywords := extractQueryKeywords(qLow)
	potSyms := extractPotentialSymbols(query)

	// Pre-filter using symbol index
	candidateIdxs := make(map[int]bool, 200)
	for _, sym := range potSyms {
		if idxs, ok := cb.symbolIndex[sym]; ok {
			for _, idx := range idxs {
				candidateIdxs[idx] = true
			}
		}
	}
	// Also check keyword matches in symbol index
	for _, kw := range keywords {
		if idxs, ok := cb.symbolIndex[kw]; ok {
			for _, idx := range idxs {
				candidateIdxs[idx] = true
			}
		}
	}

	// Fallback: if no symbol matches, scan everything (rare for non-gibberish queries)
	if len(candidateIdxs) == 0 {
		// Quick scan of names only (no content hydration yet)
		for i, chunk := range cb.chunks {
			nameLow := strings.ToLower(chunk.Name)
			for _, qs := range potSyms {
				if strings.Contains(nameLow, strings.ToLower(qs)) {
					candidateIdxs[i] = true
					break
				}
			}
			if !candidateIdxs[i] {
				for _, kw := range keywords {
					if strings.Contains(nameLow, kw) {
						candidateIdxs[i] = true
						break
					}
				}
			}
		}
	}

	// Score only candidates
	scored := make([]ScoredChunk, 0, len(candidateIdxs))
	for idx := range candidateIdxs {
		chunk := cb.chunks[idx]
		score := 0.0
		content := chunk.Content
		if content == "" {
			content = cb.hydrateOne(chunk)
		}
		contentLow := strings.ToLower(content)
		nameLow := strings.ToLower(chunk.Name)
		details := []string{}

		for _, qs := range potSyms {
			qsLow := strings.ToLower(qs)
			if nameLow == qsLow {
				score += 20.0
				details = append(details, "name="+chunk.Name)
			} else if strings.Contains(nameLow, qsLow) {
				score += 10.0
				details = append(details, "name~"+qs)
			}
		}
		for _, sym := range chunk.Symbols {
			symLow := strings.ToLower(sym)
			for _, qs := range potSyms {
				qsLow := strings.ToLower(qs)
				if symLow == qsLow {
					score += 15.0
					details = append(details, "symbol="+sym)
				}
			}
		}
		for _, kw := range keywords {
			if strings.Contains(contentLow, kw) {
				score += 2.0
			}
		}
		fileLow := strings.ToLower(chunk.File)
		for _, kw := range keywords {
			if strings.Contains(fileLow, kw) {
				score += 1.0
			}
		}
		if score > 0 {
			scored = append(scored, ScoredChunk{
				Chunk:        chunk,
				Score:        score,
				MatchDetails: strings.Join(details, ", "),
			})
		}
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

// invertedKeywordSearch performs TF-IDF retrieval using an inverted index.
// Falls back to linear keywordSearch if index is unavailable.
// TODO: Allow users to switch between tf-idf and bm24.
// Currently a DEADCODE
//
//nolint:unused
func (cb *ContextBuilder) invertedKeywordSearchTFIDF(query string, topK int) []ScoredChunk {
	if cb.invertedIdx == nil {
		return cb.keywordSearch(query, topK)
	}

	keywords := extractQueryKeywords(strings.ToLower(query))
	symbols := extractPotentialSymbols(query)
	terms := uniqueStrings(append(keywords, symbols...))

	cb.invertedIdx.mu.RLock()
	defer cb.invertedIdx.mu.RUnlock()

	scores := make(map[int]float64, topK*4)
	matched := make(map[int][]string, topK*4)
	n := float64(cb.invertedIdx.DocCount)

	for _, term := range terms {
		posts := cb.invertedIdx.Postings[strings.ToLower(term)]
		if len(posts) == 0 {
			continue
		}
		idf := math.Log(n/float64(len(posts))) + 1
		for _, p := range posts {
			scores[p.ChunkIdx] += float64(p.TF) * idf
			matched[p.ChunkIdx] = append(matched[p.ChunkIdx], term)
		}
	}

	for _, sym := range symbols {
		symLow := strings.ToLower(sym)
		for idx := range scores {
			if strings.ToLower(cb.chunks[idx].Name) == symLow {
				scores[idx] += 20.0
			}
		}
	}

	result := make([]ScoredChunk, 0, len(scores))
	for idx, score := range scores {
		result = append(result, ScoredChunk{
			Chunk:        cb.chunks[idx],
			Score:        score,
			MatchDetails: strings.Join(uniqueStrings(matched[idx]), ", "),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	if len(result) > topK {
		result = result[:topK]
	}
	return result
}

func (cb *ContextBuilder) invertedKeywordSearchBM25(query string, topK int) []ScoredChunk {
	if cb.invertedIdx == nil {
		return cb.keywordSearch(query, topK)
	}
	keywords := extractQueryKeywords(strings.ToLower(query))
	symbols := extractPotentialSymbols(query)
	terms := uniqueStrings(append(keywords, symbols...))

	cb.invertedIdx.mu.RLock()
	defer cb.invertedIdx.mu.RUnlock()

	n := cb.invertedIdx.DocCount
	avgLen := cb.invertedIdx.AvgChunkTokens
	if avgLen == 0 {
		avgLen = 100 // safe fallback if field not yet populated
	}

	scores := make(map[int]float64, topK*4)
	matched := make(map[int][]string, topK*4)

	for _, term := range terms {
		posts := cb.invertedIdx.Postings[strings.ToLower(term)]
		if len(posts) == 0 {
			continue
		}
		df := len(posts)
		for _, p := range posts {
			chunkLen := cb.chunks[p.ChunkIdx].Tokens
			scores[p.ChunkIdx] += bm25Score(p.TF, df, n, chunkLen, avgLen)
			matched[p.ChunkIdx] = append(matched[p.ChunkIdx], term)
		}
	}

	// Proximity bonus: reward chunks where query terms appear near each
	// other in content. Only applied to the top candidates by BM25 score
	// to keep this O(topK) rather than O(all matched chunks).
	const proximityWindow = 300 // characters
	proximityLimit := topK * 4  // todo: make this multiplier customizable via config

	candidates := make([]scoredIdx, 0, len(scores))
	for idx, s := range scores {
		candidates = append(candidates, scoredIdx{idx, s})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	if proximityLimit > len(candidates) {
		proximityLimit = len(candidates)
	}

	if len(terms) >= 2 {
		for _, c := range candidates[:proximityLimit] {
			idx := c.idx
			matchedTerms := matched[idx]
			if len(matchedTerms) < 2 {
				continue
			}
			content := cb.chunks[idx].Content
			if content == "" {
				content = cb.hydrateOne(cb.chunks[idx])
			}
			scores[idx] += proximityBonus(content, matchedTerms, proximityWindow)
		}
	}

	// Exact name match bonus (unchanged from original).
	for _, sym := range symbols {
		symLow := strings.ToLower(sym)
		for idx := range scores {
			if strings.ToLower(cb.chunks[idx].Name) == symLow {
				scores[idx] += 20.0
			}
		}
	}

	result := make([]ScoredChunk, 0, len(scores))
	for idx, score := range scores {
		result = append(result, ScoredChunk{
			Chunk:        cb.chunks[idx],
			Score:        score,
			MatchDetails: strings.Join(uniqueStrings(matched[idx]), ", "),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	if len(result) > topK {
		result = result[:topK]
	}
	return result
}

// buildCallSiteIndex scans all chunks for function call sites (identifier + "(" pattern).
// Returns a map from function name to chunk indices where that function is called.
//
// Used for caller/callee discovery: if chunk[i] calls foo(), then callSiteIndex["foo"]
// includes i, enabling quick lookup of callers.
func buildCallSiteIndex(cg *types.CallGraphRef, chunks []Chunk) callSiteIndex {
	// primary lookup: symbol stored in chunk.Symbols
	symToChunks := make(map[string][]int, len(chunks))
	for i, c := range chunks {
		for _, sym := range c.Symbols {
			symToChunks[sym] = append(symToChunks[sym], i)
		}
	}

	// fallback lookup: derive the call-graph-style ID from chunk fields
	nameToChunks := make(map[string][]int, len(chunks))
	for i, c := range chunks {
		key := normalizeToCallGraphID(c)
		nameToChunks[key] = append(nameToChunks[key], i) // was nameToChunks[i]
	}

	callSites := make(callSiteIndex)
	var missed int
	for _, edge := range cg.Edges {
		if edge.EdgeType != "call" {
			continue
		}
		callerChunks, ok := symToChunks[edge.From]
		if !ok {
			callerChunks = nameToChunks[edge.From]
		}
		if len(callerChunks) == 0 {
			missed++
			continue
		}
		callSites[edge.To] = append(callSites[edge.To], callerChunks...)
	}
	return callSites
}

// normalizeToCallGraphID derives the call-graph node ID format from a chunk.
// Mirrors what eulix-parser writes: "method_ClassName_funcName" or "func_funcName"
func normalizeToCallGraphID(c Chunk) string {
	name := strings.ToLower(strings.ReplaceAll(c.Name, " ", "_"))
	if c.ClassName != "" {
		class := strings.ToLower(strings.ReplaceAll(c.ClassName, " ", "_"))
		return "method_" + class + "_" + name
	}
	return "func_" + name
}

// vectorSearch performs brute-force semantic search over all embeddings.
// Compares query embedding against all chunk embeddings using cosine similarity.
// Returns chunks with similarity >= threshold, sorted by score descending, limited to topK.
// Threshold typically 0.5 for semantic search (cosine similarity in [0, 1]).
func (cb *ContextBuilder) vectorSearch(qEmb []float32, topK int, threshold float64) []ScoredChunk {
	scored := make([]ScoredChunk, 0)
	for i, chunkEmb := range cb.embeddings {
		if i >= len(cb.chunks) {
			break
		}
		if sim := cosineSimilarity(qEmb, chunkEmb); sim >= threshold {
			scored = append(scored, ScoredChunk{Chunk: cb.chunks[i], Score: sim})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

// extractExplicitAnchors parses the query for any explicit file/line/func
// references and returns them ranked by specificity.
func extractExplicitAnchors(query string) []ExplicitAnchor {
	anchors := make([]ExplicitAnchor, 0, 2)
	seen := make(map[string]bool)

	add := func(a ExplicitAnchor) {
		key := fmt.Sprintf("%s:%s:%d", a.File, a.FuncName, a.Line)
		if !seen[key] {
			seen[key] = true
			anchors = append(anchors, a)
		}
	}

	// file path + line number (highest specificity)
	for _, m := range rePathLine.FindAllStringSubmatch(query, -1) {
		line := 0
		_, _ = fmt.Sscanf(m[2], "%d", &line)
		add(ExplicitAnchor{File: m[1], Line: line, Score: 200.0})
	}

	// bare filename (with extension) anywhere in query
	for _, m := range reFilename.FindAllStringSubmatch(query, -1) {
		add(ExplicitAnchor{File: m[1], Score: 150.0})
	}

	// path fragment (two or more slash-separated components)
	for _, m := range rePathFrag.FindAllStringSubmatch(query, -1) {

		covered := false
		for _, a := range anchors {
			if strings.Contains(a.File, m[1]) {
				covered = true
				break
			}
		}
		if !covered {
			add(ExplicitAnchor{File: m[1], Score: 120.0})
		}
	}

	// funcName:lineNum without a file (rarer, e.g. from a stack trace)
	for _, m := range reFuncLine.FindAllStringSubmatch(query, -1) {
		alreadyCovered := false
		for _, a := range anchors {
			if a.File != "" && strings.HasSuffix(a.File, m[1]) {
				alreadyCovered = true
				break
			}
		}
		if !alreadyCovered {
			line := 0
			_, _ = fmt.Sscanf(m[2], "%d", &line)
			add(ExplicitAnchor{FuncName: m[1], Line: line, Score: 130.0})
		}
	}

	return anchors
}

// explicitAnchorSearch resolves ExplicitAnchors against the chunk index.
// Resolution order for each anchor:
//  1. Exact file path match + line contained in chunk range  (score: anchor.Score * 1.5)
//  2. File path suffix match + line contained               (score: anchor.Score * 1.2)
//  3. File path suffix match, no line                       (score: anchor.Score)
//  4. Path fragment anywhere in file path                   (score: anchor.Score * 0.8)
//  5. FuncName-only anchor resolved via symbolIndex         (score: anchor.Score)
func (cb *ContextBuilder) explicitAnchorSearch(anchors []ExplicitAnchor) []ScoredChunk {
	if len(anchors) == 0 {
		return nil
	}

	results := make([]ScoredChunk, 0, len(anchors)*3)
	seen := make(map[string]bool)

	add := func(sc ScoredChunk) {
		if !seen[sc.ID] {
			seen[sc.ID] = true
			results = append(results, sc)
		} else {
			// Keep the higher score if we hit the same chunk via multiple anchors
			for i, r := range results {
				if r.ID == sc.ID && sc.Score > r.Score {
					results[i].Score = sc.Score
					results[i].MatchDetails = sc.MatchDetails
					break
				}
			}
		}
	}

	for _, anchor := range anchors {
		if anchor.File != "" {
			fileLow := strings.ToLower(anchor.File)

			for i := range cb.chunks {
				c := &cb.chunks[i]
				cFileLow := strings.ToLower(c.File)

				var score float64
				var detail string

				switch {
				case cFileLow == fileLow && anchor.Line > 0 &&
					c.StartLine <= anchor.Line && anchor.Line <= c.EndLine:
					// Exact file + line inside chunk — highest confidence
					score = anchor.Score * 1.5
					detail = fmt.Sprintf("exact file+line %s:%d", anchor.File, anchor.Line)

				case strings.HasSuffix(cFileLow, fileLow) && anchor.Line > 0 &&
					c.StartLine <= anchor.Line && anchor.Line <= c.EndLine:
					// Suffix file match + line inside chunk
					score = anchor.Score * 1.2
					detail = fmt.Sprintf("suffix file+line %s:%d", anchor.File, anchor.Line)

				case strings.HasSuffix(cFileLow, fileLow) && anchor.Line == 0:
					// Filename match, no line — include all chunks from this file
					score = anchor.Score
					detail = fmt.Sprintf("filename match %s", anchor.File)

				case strings.Contains(cFileLow, fileLow) && anchor.Line == 0:
					// Fragment anywhere in path
					score = anchor.Score * 0.8
					detail = fmt.Sprintf("path fragment %s", anchor.File)

				default:
					continue
				}

				add(ScoredChunk{
					Chunk:        *c,
					Score:        score,
					MatchType:    "anchor",
					MatchDetails: detail,
				})
			}
		}

		// FuncName-only anchor (e.g. from funcName:line without a file)
		if anchor.FuncName != "" && anchor.File == "" {
			fnLow := strings.ToLower(anchor.FuncName)
			if idxs, ok := cb.symbolIndex[fnLow]; ok {
				for _, idx := range idxs {
					c := cb.chunks[idx]
					score := anchor.Score
					detail := fmt.Sprintf("symbol anchor %s", anchor.FuncName)
					if anchor.Line > 0 && c.StartLine <= anchor.Line && anchor.Line <= c.EndLine {
						score *= 1.3
						detail = fmt.Sprintf("symbol+line anchor %s:%d", anchor.FuncName, anchor.Line)
					}
					add(ScoredChunk{
						Chunk: c, Score: score,
						MatchType: "anchor", MatchDetails: detail,
					})
				}
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}
