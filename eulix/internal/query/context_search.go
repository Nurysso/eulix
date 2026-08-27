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
	"eulix/internal/utils"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

const fileExtPattern = `c|h|cc|cpp|cxx|hpp|hh|go|rs|py|ts|tsx|js|jsx|sh|bash|yaml|yml|json|md|toml|proto|java|kt|rb|php|cs|dts|dtsi`

var (
	rePathLine = regexp.MustCompile(`([\w./-]+\.(?:` + fileExtPattern + `)):(\d+)`)
	reFuncLine = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]{3,}):(\d{1,6})\b`)
	reFilename = regexp.MustCompile(`\b([\w-]+\.(?:` + fileExtPattern + `))\b`)
	rePathFrag = regexp.MustCompile(`((?:[\w-]+/){2,}[\w-]+)`)
)

var funcLineNoiseWords = map[string]bool{
	"error": true, "line": true, "port": true, "step": true,
	"code": true, "test": true, "case": true, "count": true,
	"index": true, "level": true, "value": true, "state": true,
}

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
	tTotal := time.Now()
	cb.debugLog.Log("=== multiStrategySearch START: topK=%d, intent=%v ===", topK, intent.Type)

	all := make(map[string]ScoredChunk, topK*2)
	anchors := extractExplicitAnchors(query)
	gate := buildPathGate(anchors)
	hc := newHydrationCache(cb)

	// Track detailed results per strategy
	strategyResults := make(map[string]struct {
		count    int
		topScore float64
		avgScore float64
		topFiles []string
	})

	tAnchors := time.Now()
	if len(anchors) > 0 {
		cb.debugLog.Log("Explicit anchors: %d, gate active: %v (required: %v)",
			len(anchors), gate.active, gate.required)
		for _, a := range anchors {
			cb.debugLog.Log("  anchor: file=%q func=%q line=%v score=%.0f",
				a.File, a.FuncName, a.Line, a.Score)
		}
		anchorsHits := cb.explicitAnchorSearch(anchors)
		anchorsHits = gate.applyGate(anchorsHits)
		cb.debugLog.Log("Anchors search: %d hits", len(anchorsHits))

		// Log anchor details
		if len(anchorsHits) > 0 {
			topFiles := make([]string, 0, 5)
			for i, sc := range anchorsHits {
				if i < 5 {
					topFiles = append(topFiles, fmt.Sprintf("%s (%.1f)", sc.File, sc.Score))
				}
				all[sc.ID] = sc
			}
			strategyResults["anchors"] = struct {
				count    int
				topScore float64
				avgScore float64
				topFiles []string
			}{
				count:    len(anchorsHits),
				topScore: anchorsHits[0].Score,
				avgScore: avgScore(anchorsHits),
				topFiles: topFiles,
			}
			cb.debugLog.Log("[Anchor Results] Found %d hits, top=%.1f, avg=%.1f, samples: %v",
				len(anchorsHits), anchorsHits[0].Score, avgScore(anchorsHits), topFiles)
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
	cb.debugLog.Log("[Timing] Anchors extraction & search took %v", time.Since(tAnchors))

	run := func(name string, fn func() []ScoredChunk, multiBoost float64) {
		t0 := time.Now()
		results := fn()
		results = gate.applyGate(results)
		st := StrategyTrace{Name: name, Found: len(results), Duration: time.Since(t0)}

		cb.debugLog.Log("[Timing] Strategy %q took %v (Found: %d hits)", name, st.Duration, st.Found)

		// Log what this strategy found
		if len(results) > 0 {
			topFiles := make([]string, 0, 5)
			scores := make([]float64, 0, len(results))
			for i, m := range results {
				scores = append(scores, m.Score)
				if i < 5 {
					topFiles = append(topFiles, fmt.Sprintf("%s (%.1f)", m.File, m.Score))
				}
				m.MatchType = name
				isExactStrategy := name == "exact" || name == "kb_exact" || name == "grep"
				if ex, ok := all[m.ID]; ok {
					m.Score = math.Max(ex.Score, m.Score) + multiBoost
					m.MatchDetails = ex.MatchDetails + "; " + m.MatchDetails
					m.IsExact = ex.IsExact || name == "exact" || name == "kb_exact"
				} else {
					m.IsExact = isExactStrategy
				}
				all[m.ID] = m
				if m.Score > st.TopScore {
					st.TopScore = m.Score
				}
			}
			if len(results) > 0 {
				st.AvgScore = sum(scores) / float64(len(results))
			}

			// Store strategy results for summary
			strategyResults[name] = struct {
				count    int
				topScore float64
				avgScore float64
				topFiles []string
			}{
				count:    len(results),
				topScore: st.TopScore,
				avgScore: st.AvgScore,
				topFiles: topFiles,
			}

			cb.debugLog.Log("[Strategy %q] Found %d hits, top=%.1f, avg=%.1f, samples: %v",
				name, len(results), st.TopScore, st.AvgScore, topFiles)
		} else {
			cb.debugLog.Log("[Strategy %q] Found 0 hits", name)
			strategyResults[name] = struct {
				count    int
				topScore float64
				avgScore float64
				topFiles []string
			}{count: 0, topScore: 0, avgScore: 0, topFiles: []string{}}
		}

		if trace != nil {
			trace.Strategies = append(trace.Strategies, st)
		}
	}

	if cb.hasKB {
		run("kb_exact", func() []ScoredChunk { return cb.kbExactLookup(query, intent) }, 2.5)
	}
	run("grep", func() []ScoredChunk { return cb.grepSymbolSearch(query, hc) }, 3.0)
	run("exact", func() []ScoredChunk { return cb.exactSymbolSearch(query) }, 2.0)
	run("partial", func() []ScoredChunk { return cb.partialIdentifierMatch(query) }, 1.5)

	// Use weights from allocateBudget
	kwTopK := int(float64(topK) * (0.3 + 0.4*weights["keyword"]))
	syms := extractPotentialSymbols(query)
	run("keyword", func() []ScoredChunk {
		var res []ScoredChunk
		if cb.invertedIdx != nil {
			res = cb.invertedKeywordSearchBM25(query, kwTopK, hc)
		} else {
			res = cb.keywordSearch(query, kwTopK, hc)
		}
		for i := range res {
			content := hc.content(&res[i].Chunk)
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
				raw = cb.vectorSearchIVF(qEmb, semTopK, 0.15)
			} else {
				raw = cb.vectorSearch(qEmb, semTopK, 0.15)
			}
			for i := range raw {
				raw[i].Score *= 20.0
			}
			return raw
		}, 1.5)
	}

	// Log summary of all strategies
	cb.debugLog.Log("=== STRATEGY RESULTS SUMMARY ===")
	totalFound := 0
	for name, res := range strategyResults {
		totalFound += res.count
		cb.debugLog.Log("  %-12s: %3d hits, top=%.1f, avg=%.1f",
			name, res.count, res.topScore, res.avgScore)
	}
	cb.debugLog.Log("  Total unique chunks found: %d", len(all))
	cb.debugLog.Log("=== END STRATEGY SUMMARY ===")

	tSubsys := time.Now()
	result := make([]ScoredChunk, 0, len(all))
	for _, sc := range all {
		result = append(result, sc)
	}

	// Subsystem-aware boosting replaces old boostBySubSystemPath call.
	queryTokens := extractQueryKeywords(strings.ToLower(query))
	detected := detectQuerySubsystems(cb.subsystemTree, queryTokens)
	detected = filterNoiseSubsystems(detected, cb.noisePaths)
	if len(detected) > subsysFinalK {
		detected = detected[:subsysFinalK]
	}
	if len(detected) > 0 {
		cb.debugLog.Log("Detected subsystem (%d): top=%q score=%.2f",
			len(detected), detected[0].node.Path, detected[0].score) // Use detected[0].score, not node.score
		if trace != nil {
			for _, ds := range detected {
				trace.Warnings = append(trace.Warnings,
					fmt.Sprintf("subsystem: %s (score=%.2f chunks=%d)",
						ds.node.Path, ds.score, ds.node.TotalChunks)) // ds.score, not ds.node.score
			}
		}
	}
	boostByDetectedSubsystems(result, detected, cb.noisePaths, &cb.config.RetrievalConfig)
	result = filterTestDocChunks(result)
	cb.debugLog.Log("[Timing] Subsystem detection & boosting took %v", time.Since(tSubsys))

	tDemote := time.Now()
	qLow := strings.ToLower(query)
	isTestQuery := strings.Contains(qLow, "test") || strings.Contains(qLow, "mock") || strings.Contains(qLow, "spec")

	// Primary repo scope demotion
	var primaryRepo string
	if len(detected) > 0 && detected[0].score >= 10.0 {
		parts := strings.Split(detected[0].node.Path, "/")
		if len(parts) > 0 {
			primaryRepo = parts[0]
		}
	}
	isTestFile := func(f string) bool {
		return strings.Contains(f, "/tests/") ||
			strings.Contains(f, "/test_") ||
			strings.Contains(f, "fake_") ||
			strings.Contains(f, "_test.go")
	}

	demotionCount := 0
	for i := range result {
		if result[i].IsExact {
			continue // verified exact/grep hits are protected from scope demotion
		}
		f := result[i].File
		demoted := false

		switch {
		case isTestFile(f) && !isTestQuery:
			result[i].Score *= 0.3
			demoted = true
		case isTestFile(f) && (intent.Type == IntentConcept || intent.Type == IntentFlow):
			// still demote lightly even on a testing query, if intent is conceptual
			result[i].Score *= 0.7
			demoted = true
		}

		if primaryRepo != "" {
			if fParts := strings.SplitN(f, "/", 2); len(fParts) > 0 && fParts[0] != primaryRepo {
				if strings.HasPrefix(fParts[0], "charm-") || fParts[0] == "requirements" {
					result[i].Score *= 0.3
					demoted = true
				}
			}
		}
		if demoted {
			demotionCount++
		}
	}
	cb.debugLog.Log("[Timing] Scope demotion took %v (demoted %d chunks)", time.Since(tDemote), demotionCount)

	tSort := time.Now()
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].IsExact != result[j].IsExact {
			return result[i].IsExact
		}
		return result[i].Score > result[j].Score
	})
	cb.debugLog.Log("[Timing] Sorting took %v", time.Since(tSort))

	if len(result) > topK {
		cb.debugLog.Log("Truncating results: %d → %d (topK=%d)", len(result), topK, topK)
		result = result[:topK]
	}

	// Log final results breakdown
	cb.debugLog.Log("=== FINAL RESULTS ===")
	cb.debugLog.Log("  Total unique before truncation: %d", len(all))
	cb.debugLog.Log("  Final returned chunks: %d", len(result))
	if len(result) > 0 {
		topFiles := make([]string, 0, 10)
		// topScores := make([]float64, 0, 10)
		for i, sc := range result {
			if i < 10 {
				topFiles = append(topFiles, fmt.Sprintf("%s (%.1f)", sc.File, sc.Score))
				// topScores = append(topScores, sc.Score)
			}
		}
		cb.debugLog.Log("  Top files: %v", topFiles)
		cb.debugLog.Log("  Score range: %.1f - %.1f", result[0].Score, result[len(result)-1].Score)
	}

	cb.debugLog.Log("=== multiStrategySearch COMPLETE: Total Time %v, Returning %d chunks ===", time.Since(tTotal), len(result))
	return result
}

// Helper functions
func avgScore(chunks []ScoredChunk) float64 {
	if len(chunks) == 0 {
		return 0
	}
	sum := 0.0
	for _, c := range chunks {
		sum += c.Score
	}
	return sum / float64(len(chunks))
}

func sum(scores []float64) float64 {
	total := 0.0
	for _, s := range scores {
		total += s
	}
	return total
}

// grepSymbolSearch performs targeted exact-token matching against code chunks.
//
// It extracts structural identifiers (e.g., classes, functions) from the query
// and matches them against file paths, chunk names, symbol tables, and raw code content.
// Direct language definition matches (e.g., "func foo", "class Bar") receive the
// highest relevance score. Content fallbacks are rate-limited by hydrationBudget.
func (cb *ContextBuilder) grepSymbolSearch(query string, hc *hydrationCache) []ScoredChunk {
	symbols := extractPotentialSymbols(query)
	if len(symbols) == 0 {
		return nil
	}

	var targetTokens []string
	for _, s := range symbols {
		sLow := strings.ToLower(s)
		if sLow == "init" || sLow == "setup" || sLow == "self" || sLow == "test" || sLow == "main" {
			continue
		}
		if len(s) >= 3 {
			targetTokens = append(targetTokens, s)
		}
	}
	if len(targetTokens) == 0 {
		return nil
	}

	scored := make([]ScoredChunk, 0, 32)
	seen := make(map[string]bool, 32)
	hydrationBudget := maxContentFallbackHydrations

	for i := range cb.chunks {
		chunk := &cb.chunks[i]
		fileLow := strings.ToLower(chunk.File)
		nameLow := strings.ToLower(chunk.Name)

		matched := false
		var matchDetail string
		score := 0.0

		for _, tok := range targetTokens {
			tokLow := strings.ToLower(tok)

			if strings.Contains(fileLow, tokLow) {
				score += 150.0
				matched = true
				matchDetail = "Grep file path match: " + tok
			}
			if nameLow == tokLow {
				score += 180.0
				matched = true
				matchDetail = "Grep exact name: " + chunk.Name
			} else if strings.Contains(nameLow, tokLow) {
				score += 90.0
				matched = true
				matchDetail = "Grep name sub-match: " + tok
			}
			for _, sym := range chunk.Symbols {
				if strings.ToLower(sym) == tokLow {
					score += 160.0
					matched = true
					matchDetail = "Grep exact symbol: " + sym
					break
				}
			}
		}

		if !matched && hydrationBudget > 0 {
			wasEmpty := chunk.Content == ""
			contentLow := strings.ToLower(hc.content(chunk))
			if wasEmpty {
				hydrationBudget--
			}

			for _, tok := range targetTokens {
				tokLow := strings.ToLower(tok)
				if strings.Contains(contentLow, "class "+tokLow) ||
					strings.Contains(contentLow, "def "+tokLow) ||
					strings.Contains(contentLow, "fn "+tokLow) ||
					strings.Contains(contentLow, "func "+tokLow) ||
					strings.Contains(contentLow, "type "+tokLow) {
					score += 200.0
					matched = true
					matchDetail = "Grep code definition: " + tok
					break
				} else if strings.Contains(contentLow, tokLow) {
					score += 60.0
					matched = true
					matchDetail = "Grep literal content hit: " + tok
					break
				}
			}
		}

		if matched && !seen[chunk.ID] {
			seen[chunk.ID] = true
			scored = append(scored, ScoredChunk{
				Chunk:        *chunk,
				Score:        score,
				IsExact:      true,
				MatchType:    "grep",
				MatchDetails: matchDetail,
			})
		}
	}

	if hydrationBudget <= 0 {
		cb.debugLog.Log("grepSymbolSearch: hit hydration budget (%d), some chunks skipped content scan", maxContentFallbackHydrations)
	}

	return scored
}

// exactSymbolSearch performs direct matching on chunk names and symbols.
// Returns chunks whose name or symbol list exactly matches (case-insensitive)
// a token extracted from the query.
func (cb *ContextBuilder) exactSymbolSearch(query string) []ScoredChunk {
	potentialSymbols := extractPotentialSymbols(query)
	if len(potentialSymbols) == 0 {
		return nil
	}
	potentialSymbolsLower := make([]string, len(potentialSymbols))
	for i, s := range potentialSymbols {
		potentialSymbolsLower[i] = strings.ToLower(s)
	}

	scored := make([]ScoredChunk, 0, 32)
	for _, chunk := range cb.chunks {
		nameLow := strings.ToLower(chunk.Name)
		best, detail := 0.0, ""

		for i, qsLow := range potentialSymbolsLower {
			if nameLow == qsLow && 100.0 > best {
				best, detail = 100.0, "Exact name: "+chunk.Name
			}
			_ = i
		}
		for _, sym := range chunk.Symbols {
			symLow := strings.ToLower(sym)
			for _, qsLow := range potentialSymbolsLower {
				if symLow == qsLow && 90.0 > best {
					best, detail = 90.0, "Symbol: "+sym
				}
			}
		}

		if best > 0 {
			scored = append(scored, ScoredChunk{
				Chunk: chunk, Score: best,
				IsExact: true, MatchDetails: detail,
			})
		}
	}
	return scored
}

// partialIdentifierMatch performs sub-token matching across camelCase and snake_case identifiers.
func (cb *ContextBuilder) partialIdentifierMatch(query string) []ScoredChunk {
	qTokens := extractPotentialSymbols(query)

	candidateIdxs := make(map[int]bool, 100)
	for _, qt := range qTokens {
		qtLow := strings.ToLower(qt)
		if idxs, ok := cb.symbolIndex[qtLow]; ok {
			for _, idx := range idxs {
				candidateIdxs[idx] = true
			}
		}
	}

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
			exactToken := false
			for _, ct := range cTokens {
				if ct == qtLow {
					matchCount++
					totalScore += 15.0
					matchedToks = append(matchedToks, ct)
					exactToken = true
					break
				}
			}
			if !exactToken && strings.Contains(cNameLow, qtLow) {
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
func (cb *ContextBuilder) keywordSearch(query string, topK int, hc *hydrationCache) []ScoredChunk {
	qLow := strings.ToLower(query)
	keywords := extractQueryKeywords(qLow)
	potSyms := extractPotentialSymbols(query)

	candidateIdxs := make(map[int]bool, 200)
	for _, sym := range potSyms {
		if idxs, ok := cb.symbolIndex[strings.ToLower(sym)]; ok {
			for _, idx := range idxs {
				candidateIdxs[idx] = true
			}
		}
	}
	for _, kw := range keywords {
		if idxs, ok := cb.symbolIndex[kw]; ok {
			for _, idx := range idxs {
				candidateIdxs[idx] = true
			}
		}
	}

	if len(candidateIdxs) == 0 {
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

	scored := make([]ScoredChunk, 0, len(candidateIdxs))
	for idx := range candidateIdxs {
		chunk := cb.chunks[idx]
		score := 0.0
		content := chunk.Content
		if content == "" {
			content = hc.content(&chunk)
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

// invertedKeywordSearchTFIDF performs TF-IDF retrieval using an inverted index.
// TODO: Allow users to switch between tf-idf and bm24.
// Currently a DEADCODE
//
//nolint:unused
func (cb *ContextBuilder) invertedKeywordSearchTFIDF(query string, topK int, hc *hydrationCache) []ScoredChunk {
	if cb.invertedIdx == nil {
		return cb.keywordSearch(query, topK, hc)
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

func (cb *ContextBuilder) invertedKeywordSearchBM25(query string, topK int, hc *hydrationCache) []ScoredChunk {
	if cb.invertedIdx == nil {
		return cb.keywordSearch(query, topK, hc)
	}
	keywords := extractQueryKeywords(strings.ToLower(query))
	symbols := extractPotentialSymbols(query)
	terms := uniqueStrings(append(keywords, symbols...))

	cb.invertedIdx.mu.RLock()
	defer cb.invertedIdx.mu.RUnlock()

	n := cb.invertedIdx.DocCount
	avgLen := cb.invertedIdx.AvgChunkTokens
	if avgLen == 0 {
		avgLen = 100
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
			effectiveLen := chunkLen
			if effectiveLen < 40 {
				effectiveLen = 40
			}
			scores[p.ChunkIdx] += bm25Score(p.TF, df, n, effectiveLen, avgLen)
			matched[p.ChunkIdx] = append(matched[p.ChunkIdx], term)
		}
	}

	const proximityWindow = 300
	proximityLimit := topK * 4

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

func buildCallSiteIndex(cg *utils.CallGraphRef, chunks []Chunk, log *DebugLogger) callSiteIndex {
	symToChunks := make(map[string][]int, len(chunks))
	for i, c := range chunks {
		for _, sym := range c.Symbols {
			symToChunks[sym] = append(symToChunks[sym], i)
		}
	}

	nameToChunks := make(map[string][]int, len(chunks))
	for i, c := range chunks {
		key := normalizeToCallGraphID(c)
		nameToChunks[key] = append(nameToChunks[key], i)
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
	if missed > 0 && log != nil {
		log.Log("buildCallSiteIndex: %d edges had no resolvable caller chunk", missed)
	}
	return callSites
}

// normalizeToCallGraphID derives the call-graph node ID format from a chunk.
// Mirrors what eulix-parser writes: "method_ClassName_funcName" or "func_funcName"
func normalizeToCallGraphID(c Chunk) string {
	name := strings.ToLower(strings.ReplaceAll(c.Name, " ", "_"))
	// c.ID is now "func_name::path" or "method_class_name::path"
	// Strip the ::path qualifier to get the raw call-graph node key
	id := c.ID
	if i := strings.Index(id, "::"); i != -1 {
		id = id[:i]
	}
	_ = name // id already encodes method_/func_ prefix
	return id
}

func (cb *ContextBuilder) vectorSearch(qEmb []float32, topK int, threshold float64) []ScoredChunk {
	threshF := float32(threshold)
	n := len(cb.embeddings)
	if len(cb.chunks) < n {
		n = len(cb.chunks)
	}
	scored := make([]ScoredChunk, 0, topK*2)
	for i := 0; i < n; i++ {
		if sim := dotProduct(qEmb, cb.embeddings[i]); sim >= threshF {
			scored = append(scored, ScoredChunk{Chunk: cb.chunks[i], Score: float64(sim)})
		}
	}
	slices.SortFunc(scored, byScoreDesc)
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
		if funcLineNoiseWords[strings.ToLower(m[1])] {
			continue
		}
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
		sc.IsExact = true
		if !seen[sc.ID] {
			seen[sc.ID] = true
			results = append(results, sc)
		} else {
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
					score = anchor.Score * 1.5
					detail = fmt.Sprintf("exact file+line %s:%d", anchor.File, anchor.Line)

				case strings.HasSuffix(cFileLow, fileLow) && anchor.Line > 0 &&
					c.StartLine <= anchor.Line && anchor.Line <= c.EndLine:
					score = anchor.Score * 1.2
					detail = fmt.Sprintf("suffix file+line %s:%d", anchor.File, anchor.Line)

				case strings.HasSuffix(cFileLow, fileLow) && anchor.Line == 0:
					score = anchor.Score
					detail = fmt.Sprintf("filename match %s", anchor.File)

				case strings.Contains(cFileLow, fileLow) && anchor.Line == 0:
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
