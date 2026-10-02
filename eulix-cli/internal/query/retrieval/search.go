//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package retrieval provides context window Creation for Eulix's RAG system.

Key Responsibilities:
  - Orchestrates multi-strategy retrieval (KB exact, partial identifier, keyword, and vector search)
  - Deduplicates and merges candidate scores using strategy-specific boost multipliers
  - Adjusts strategy weights and search paths dynamically based on query intent and corpus scale
*/
package retrieval

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"eulix/internal/utils"
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

type searchToken struct {
	raw        string
	low        string
	classMatch string
	defMatch   string
	fnMatch    string
	funcMatch  string
	typeMatch  string
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

// cleanSymbols drops tokens shorter than 3 chars and dedupes case-insensitively.
// Mostly to kill garbage like "s", "q", "x" that extractPotentialSymbols lets through.
func cleanSymbols(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if len(s) < 3 {
			continue
		}
		low := strings.ToLower(s)
		if seen[low] {
			continue
		}
		seen[low] = true
		out = append(out, s)
	}
	return out
}

func splitDottedSymbols(symbols []string) []string {
	seen := make(map[string]bool, len(symbols)*2)
	out := make([]string, 0, len(symbols)*2)
	add := func(s string) {
		if len(s) < 3 {
			return
		}
		low := strings.ToLower(s)
		if seen[low] {
			return
		}
		seen[low] = true
		out = append(out, s)
	}
	// whole dotted names first as they score the highest
	for _, s := range symbols {
		add(s)
	}
	// then the individual parts
	for _, s := range symbols {
		if strings.Contains(s, ".") {
			for _, part := range strings.SplitN(s, ".", 2) {
				add(part)
			}
		}
	}
	return out
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
	rawSyms := extractPotentialSymbols(query)
	querySymbols := cleanSymbols(rawSyms)                    // short tokens dropped
	querySymbolsExpanded := splitDottedSymbols(querySymbols) // dotted names+part
	cb.debugLog.Log("[Step 1] Extracting explicit anchors...")
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
		cb.debugLog.Log("[Step 1.1] Processing explicit anchors: %d, gate active: %v (required: %v)",
			len(anchors), gate.active, gate.required)
		for _, a := range anchors {
			cb.debugLog.Log("  anchor: file=%q func=%q line=%v score=%.0f",
				a.File, a.FuncName, a.Line, a.Score)
		}
		anchorsHits := cb.explicitAnchorSearch(anchors)
		anchorsHits = gate.applyGate(anchorsHits)

		cb.debugLog.Log("[Step 1.2] Anchors search executed: %d hits returned", len(anchorsHits))

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
			cb.debugLog.Log("[Step 1.3] Adjusted topK to %d based on high-confidence anchor hits", topK)
		}
	}
	cb.debugLog.Log("[Timing] Anchors extraction & search took %v", time.Since(tAnchors))

	run := func(name string, fn func() []ScoredChunk, multiBoost float64) {
		t0 := time.Now()
		results := fn()
		const maxPerStrategy = 500
		if len(results) > maxPerStrategy {
			if debugLevel3 {
				cb.debugLog.Log("Strategy %q returned %d hits — truncating to %d before merge", name, len(results), maxPerStrategy)
			}
			results = results[:maxPerStrategy]
		}
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

	cb.debugLog.Log("[Step 2] Running search strategies")
	if cb.hasKB {
		cb.debugLog.Log("[Step 2.1] Executing kb_exact strategy")
		run("kb_exact", func() []ScoredChunk { return cb.kbExactLookup(query, intent) }, 2.5)
	}
	grepTopK := topK
	if grepTopK > 400 {
		grepTopK = 400
	}
	cb.debugLog.Log("[Step 2.2] Executing grep strategy (grepTopK=%d)", grepTopK)
	// Pass pre-cleaned symbols so grepSymbolSearch doesn't re-extract
	// and doesn't see single-char noise tokens or garbage dotted fragments.
	run("grep", func() []ScoredChunk {
		return cb.grepSymbolSearchWithSymbols(querySymbolsExpanded, hc, grepTopK)
	}, 3.0)

	cb.debugLog.Log("[Step 2.3] Executing exact strategy")
	run("exact", func() []ScoredChunk {
		return cb.exactSymbolSearchWithSymbols(querySymbolsExpanded)
	}, 2.0)

	cb.debugLog.Log("[Step 2.4] Executing partial strategy")
	run("partial", func() []ScoredChunk {
		return cb.partialIdentifierMatchWithSymbols(querySymbolsExpanded)
	}, 1.5)

	kwTopK := int(float64(topK) * (0.3 + 0.4*weights["keyword"]))
	cb.debugLog.Log("[Step 2.5] Executing keyword strategy (kwTopK=%d)", kwTopK)
	run("keyword", func() []ScoredChunk {
		var res []ScoredChunk
		if cb.invertedIdx != nil {
			res = cb.invertedKeywordSearchBM25(query, kwTopK, hc)
		} else {
			res = cb.keywordSearch(query, kwTopK, hc)
		}
		// Use pre-cleaned, min-length-3 symbols only for keyword post-processing.
		// Previously used raw syms which included single-char tokens like "s", "f", "m"
		// causing spurious +25 score bumps on nearly every chunk.
		for i := range res {
			content := hc.content(&res[i].Chunk)
			for _, sym := range querySymbols {
				if strings.EqualFold(res[i].Name, sym) {
					continue
				}
				if strings.Contains(content, "."+sym+"(") || strings.Contains(content, sym+"(") {
					res[i].Score += 25.0
					break
				}
			}
			for _, sym := range querySymbols {
				if strings.HasPrefix(strings.ToLower(res[i].Name), "_"+strings.ToLower(sym)) {
					res[i].Score -= 10.0
				}
			}
		}
		return res
	}, 2.0)

	skipSemantic := intent.Type == IntentCallers || intent.Type == IntentCallees
	if cb.hasEmbeddings && qEmb != nil && !skipSemantic {
		semTopK := int(float64(topK) * (0.2 + 0.5*weights["semantic"]))
		minimumSimimilarity := float64(0.15)
		if cb.config.RetrievalConfig.SemanticMinSimilarity > 0 {
			minimumSimimilarity = cb.config.RetrievalConfig.SemanticMinSimilarity
		}
		cb.debugLog.Log("[Step 2.6] Executing semantic strategy (semTopK=%d, minSim=%.2f)", semTopK, minimumSimimilarity)
		run("semantic", func() []ScoredChunk {
			var raw []ScoredChunk
			if cb.ivfIndex != nil {
				raw = cb.vectorSearchIVF(qEmb, semTopK, minimumSimimilarity)
			} else {
				raw = cb.vectorSearch(qEmb, semTopK, minimumSimimilarity)
			}
			for i := range raw {
				raw[i].Score *= 20.0
			}
			return raw
		}, 1.5)
	}

	cb.debugLog.Log("[Step 3] Resolving anchor pins")
	if pins := cb.resolveAnchorPins(query, all); len(pins) > 0 {
		cb.debugLog.Log("[Step 3.1] Resolving %d anchor pins", len(pins))
		cb.logAnchorPins(pins)
		for _, p := range pins {
			bonus := p.Confidence + math.Min(p.RetrievalScore, 1000.0)
			sc := ScoredChunk{
				Chunk:     p.Chunk,
				Score:     5000.0 + bonus,
				IsExact:   true,
				MatchType: "anchor_pin",
				MatchDetails: fmt.Sprintf("anchor-pinned: %s (%s, retrieval=%.0f)",
					p.Entity, p.Reason, p.RetrievalScore),
				Pinned: true,
			}
			if ex, ok := all[sc.ID]; ok {
				sc.MatchDetails = ex.MatchDetails + "; " + sc.MatchDetails
			}
			all[sc.ID] = sc
		}
	}

	cb.debugLog.Log("=== STRATEGY RESULTS SUMMARY ===")
	totalFound := 0
	for name, res := range strategyResults {
		totalFound += res.count
		cb.debugLog.Log("  %-12s: %3d hits, top=%.1f, avg=%.1f",
			name, res.count, res.topScore, res.avgScore)
	}
	cb.debugLog.Log("  Total unique chunks found: %d", len(all))
	cb.debugLog.Log("=== END STRATEGY SUMMARY ===")

	cb.debugLog.Log("[Step 4] Subsystem detection and boosting")
	tSubsys := time.Now()
	result := make([]ScoredChunk, 0, len(all))
	for _, sc := range all {
		result = append(result, sc)
	}
	var detected []subsystemScore
	if cb.config.RetrievalConfig.EnableSubsystemBoosting {
		queryTokens := extractQueryKeywords(strings.ToLower(query))
		detected = detectQuerySubsystems(cb, cb.subsystemTree, queryTokens)
		detected = filterNoiseSubsystems(cb, detected, cb.noisePaths)
		if len(detected) > subsysFinalK {
			detected = detected[:subsysFinalK]
		}
		if debugLevel3 {
			cb.debugLog.Log("[Step 4.1 Detail] All detected subsystems (%d total):", len(detected))
			for i, ds := range detected {
				cb.debugLog.Log("  #%d: path=%q score=%.2f chunks=%d",
					i+1, ds.node.Path, ds.score, ds.node.TotalChunks)
			}
		}
		if len(detected) > 0 {
			cb.debugLog.Log("[Step 4.1] Detected subsystem (%d): top=%q score=%.2f",
				len(detected), detected[0].node.Path, detected[0].score)
			if trace != nil {
				for _, ds := range detected {
					trace.Warnings = append(trace.Warnings,
						fmt.Sprintf("subsystem: %s (score=%.2f chunks=%d)",
							ds.node.Path, ds.score, ds.node.TotalChunks))
				}
			}
		}
		boostByDetectedSubsystems(result, detected, cb.noisePaths, &cb.config.RetrievalConfig)
	}
	result = filterTestDocChunks(result)
	cb.debugLog.Log("[Timing] Subsystem detection & boosting took %v", time.Since(tSubsys))

	cb.debugLog.Log("[Step 5] Applying scope demotions (test/mock/spec/cross-root)")
	tDemote := time.Now()
	qLow := strings.ToLower(query)
	isTestQuery := strings.Contains(qLow, "test") || strings.Contains(qLow, "mock") || strings.Contains(qLow, "spec")
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
	testPenalty := float64(0.3)
	if cb.config.RetrievalConfig.TestFilePenalty > 0 {
		testPenalty = float64(cb.config.RetrievalConfig.TestFilePenalty)
	}
	demotionCount := 0
	for i := range result {
		if result[i].IsExact {
			continue
		}
		f := result[i].File
		demoted := false
		switch {
		case isTestFile(f) && !isTestQuery:
			result[i].Score *= testPenalty
			demoted = true
		case isTestFile(f) && (intent.Type == IntentConcept || intent.Type == IntentFlow):
			conceptPenalty := math.Min(1.0, testPenalty*2.33)
			result[i].Score *= conceptPenalty
			demoted = true
		}
		if primaryRepo != "" && cb.config.RetrievalConfig.ApplyCrossRootIsolation {
			if fParts := strings.SplitN(f, "/", 2); len(fParts) > 0 && fParts[0] != primaryRepo {
				if strings.HasPrefix(fParts[0], "charm-") || fParts[0] == "requirements" {
					penalty := float64(cb.config.RetrievalConfig.CrossRootPenalty)
					if penalty <= 0 {
						penalty = 0.32
					}
					result[i].Score *= penalty
					demoted = true
				}
			}
		}
		if demoted {
			demotionCount++
		}
	}
	cb.debugLog.Log("[Timing] Scope demotion took %v (demoted %d chunks)", time.Since(tDemote), demotionCount)

	cb.debugLog.Log("[Step 6] Sorting and truncating final result set")
	tSort := time.Now()
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].IsExact != result[j].IsExact {
			return result[i].IsExact
		}
		return result[i].Score > result[j].Score
	})
	cb.debugLog.Log("[Timing] Sorting took %v", time.Since(tSort))
	if len(result) > topK {
		cb.debugLog.Log("[Step 6.1] Truncating results: %d → %d (topK=%d)", len(result), topK, topK)
		result = result[:topK]
	}

	cb.debugLog.Log("=== FINAL RESULTS ===")
	cb.debugLog.Log("  Total unique before truncation: %d", len(all))
	cb.debugLog.Log("  Final returned chunks: %d", len(result))
	if len(result) > 0 {
		topFiles := make([]string, 0, 10)
		for i, sc := range result {
			if i < 10 {
				topFiles = append(topFiles, fmt.Sprintf("%s (%.1f)", sc.File, sc.Score))
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

// grepSymbolSearchWithSymbols is the fixed version of grepSymbolSearch.
// It accepts pre-cleaned, pre-expanded symbols instead of re-extracting from
// the raw query, eliminating single-char token noise.
func (cb *ContextBuilder) grepSymbolSearchWithSymbols(symbols []string, hc *hydrationCache, topK int) []ScoredChunk {
	if len(symbols) == 0 {
		return nil
	}

	// Build compiled tokens. Minimum length 3 is already guaranteed by cleanSymbols.
	var compiledTokens []searchToken
	for _, s := range symbols {
		sLow := strings.ToLower(s)
		if sLow == "init" || sLow == "setup" || sLow == "self" || sLow == "test" || sLow == "main" {
			continue
		}
		compiledTokens = append(compiledTokens, searchToken{
			raw:        s,
			low:        sLow,
			classMatch: "class " + sLow,
			defMatch:   "def " + sLow,
			fnMatch:    "fn " + sLow,
			funcMatch:  "func " + sLow,
			typeMatch:  "type " + sLow,
		})
	}
	if len(compiledTokens) == 0 {
		return nil
	}
	if debugLevel3 {
		cb.debugLog.Log("[grepSymbolSearch] START: %d compiled tokens, scanning %d chunks", len(compiledTokens), len(cb.chunks))
	}

	scored := make([]ScoredChunk, 0, 32)
	seen := make(map[string]bool, 32)
	hydrationBudget := maxContentFallbackHydrations

	for i := range cb.chunks {
		chunk := &cb.chunks[i]
		fileLow := strings.ToLower(chunk.File)
		nameLow := strings.ToLower(chunk.Name)

		// file path component matching — split on "/" and "." so that
		// "sql" only matches a chunk whose path has "sql" as a whole component,
		// not as a substring of e.g. "mysql" or "postgresql".
		fileComponents := strings.FieldsFunc(fileLow, func(r rune) bool {
			return r == '/' || r == '.'
		})
		fileComponentSet := make(map[string]bool, len(fileComponents))
		for _, fc := range fileComponents {
			fileComponentSet[fc] = true
		}

		// Per-dimension score caps. We track the best score per (chunk, dimension)
		// pair so multiple tokens matching the same dimension don't stack.
		var fileScore, nameScore, symbolScore float64
		exactNameHit := false
		matchDetails := make([]string, 0, 2)

		for _, t := range compiledTokens {
			// File component match : token must be a full path component.
			if fileComponentSet[t.low] {
				if fileScore == 0 {
					fileScore = 150.0
					matchDetails = append(matchDetails, "file:"+t.raw)
				}
			}

			// Name match: exact beats sub-match. pick best, don't stack.
			if nameLow == t.low {
				if nameScore < 180.0 {
					nameScore = 180.0
					exactNameHit = true
					matchDetails = append(matchDetails, "name="+chunk.Name)
				}
			} else if strings.Contains(nameLow, t.low) && nameScore < 90.0 {
				nameScore = 90.0
				matchDetails = append(matchDetails, "name~"+t.raw)
			}

			// Symbol list match. pick best, don't stack.
			for _, sym := range chunk.Symbols {
				if strings.ToLower(sym) == t.low {
					if symbolScore < 160.0 {
						symbolScore = 160.0
						exactNameHit = true
						matchDetails = append(matchDetails, "sym="+sym)
					}
					break
				}
			}
		}

		score := fileScore + nameScore + symbolScore

		// Content scan only if name/symbol/file gave nothing.
		if score == 0 && hydrationBudget > 0 {
			wasEmpty := chunk.Content == ""
			contentLow := strings.ToLower(hc.content(chunk))
			if wasEmpty {
				hydrationBudget--
			}
			for _, t := range compiledTokens {
				if strings.Contains(contentLow, t.classMatch) ||
					strings.Contains(contentLow, t.defMatch) ||
					strings.Contains(contentLow, t.fnMatch) ||
					strings.Contains(contentLow, t.funcMatch) ||
					strings.Contains(contentLow, t.typeMatch) {
					score = 200.0
					matchDetails = append(matchDetails, "def:"+t.raw)
					break
				} else if strings.Contains(contentLow, t.low) {
					score = 60.0
					matchDetails = append(matchDetails, "content:"+t.raw)
					break
				}
			}
		}

		if score > 0 && !seen[chunk.ID] {
			seen[chunk.ID] = true
			scored = append(scored, ScoredChunk{
				Chunk:        *chunk,
				Score:        score,
				IsExact:      exactNameHit,
				MatchType:    "grep",
				MatchDetails: strings.Join(matchDetails, "; "),
			})
		}
	}

	if hydrationBudget <= 0 && debugLevel3 {
		cb.debugLog.Log("[grepSymbolSearch] Hit hydration budget (%d), some chunks skipped content scan", maxContentFallbackHydrations)
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if topK > 0 && len(scored) > topK {
		scored = scored[:topK]
	}
	if debugLevel3 {
		cb.debugLog.Log("[grepSymbolSearch] COMPLETE: Returning %d hits (topK=%d)", len(scored), topK)
	}
	return scored
}

// GrepSymbolSearch is kept for callers outside the package it(future update)
func (cb *ContextBuilder) GrepSymbolSearch(query string, hc *hydrationCache, topK int) []ScoredChunk {
	raw := extractPotentialSymbols(query)
	symbols := splitDottedSymbols(cleanSymbols(raw))
	return cb.grepSymbolSearchWithSymbols(symbols, hc, topK)
}

// exactSymbolSearchWithSymbols performs direct matching on chunk names and symbols.
// Returns chunks whose name or symbol list exactly matches (case-insensitive)
// a token extracted from the query.
func (cb *ContextBuilder) exactSymbolSearchWithSymbols(symbols []string) []ScoredChunk {
	if len(symbols) == 0 {
		return nil
	}

	// Pre-lowercase for O(1) lookup.
	symLowers := make([]string, len(symbols))
	for i, s := range symbols {
		symLowers[i] = strings.ToLower(s)
	}

	if debugLevel3 {
		cb.debugLog.Log("[exactSymbolSearch] START: symbols=%v, searching %d chunks", symbols, len(cb.chunks))
	}

	// specificity returns a score multiplier based on how long/specific the
	// matched symbol is. Longer symbols are rarer and therefore more valuable.
	specificity := func(sym string) float64 {
		l := len(sym)
		switch {
		case l >= 20:
			return 3.0
		case l >= 12:
			return 2.0
		case l >= 7:
			return 1.5
		default:
			return 1.0
		}
	}

	scored := make([]ScoredChunk, 0, 32)
	for _, chunk := range cb.chunks {
		nameLow := strings.ToLower(chunk.Name)
		best, detail := 0.0, ""

		for i, qsLow := range symLowers {
			s := specificity(symbols[i])
			nameScore := 100.0 * s
			if nameLow == qsLow && nameScore > best {
				best, detail = nameScore, "Exact name: "+chunk.Name
			}
		}

		for _, sym := range chunk.Symbols {
			sLow := strings.ToLower(sym)
			for i, qsLow := range symLowers {
				s := specificity(symbols[i])
				symScore := 90.0 * s
				if sLow == qsLow && symScore > best {
					best, detail = symScore, "Symbol: "+sym
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

	if debugLevel3 {
		cb.debugLog.Log("[exactSymbolSearch] COMPLETE: Found %d exact symbol matches", len(scored))
	}
	return scored
}

// ExactSymbolSearch is kept for callers outside the package it(future update).
func (cb *ContextBuilder) ExactSymbolSearch(query string) []ScoredChunk {
	raw := extractPotentialSymbols(query)
	symbols := splitDottedSymbols(cleanSymbols(raw))
	return cb.exactSymbolSearchWithSymbols(symbols)
}

// partialIdentifierMatchWithSymbols is the fixed version of partialIdentifierMatch.
// It accepts pre-cleaned symbols so no single-char tokens pollute candidate lookup
func (cb *ContextBuilder) partialIdentifierMatchWithSymbols(qTokens []string) []ScoredChunk {
	if debugLevel3 {
		cb.debugLog.Log("[partialIdentifierMatch] START: qTokens=%v", qTokens)
	}
	if len(qTokens) == 0 {
		return nil
	}

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
		if debugLevel3 {
			cb.debugLog.Log("[partialIdentifierMatch] Fallback to linear scan over all chunks")
		}
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
	if debugLevel3 {
		cb.debugLog.Log("[partialIdentifierMatch] Processing %d candidates", len(candidateIdxs))
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
	if debugLevel3 {
		cb.debugLog.Log("[partialIdentifierMatch] COMPLETE: Found %d partial matches", len(scored))
	}
	return scored
}

// PartialIdentifierMatch is kept for callers outside the package(future update).
//
//nolint:unused
func (cb *ContextBuilder) PartialIdentifierMatch(query string) []ScoredChunk {
	raw := extractPotentialSymbols(query)
	symbols := splitDottedSymbols(cleanSymbols(raw))
	return cb.partialIdentifierMatchWithSymbols(symbols)
}

// keywordSearch performs pre-filtered term frequency scoring against chunk candidates.
func (cb *ContextBuilder) keywordSearch(query string, topK int, hc *hydrationCache) []ScoredChunk {
	qLow := strings.ToLower(query)
	keywords := extractQueryKeywords(qLow)
	potSyms := extractPotentialSymbols(query)

	if debugLevel3 {
		cb.debugLog.Log("[keywordSearch] START: keywords=%v, potSyms=%v, topK=%d", keywords, potSyms, topK)
	}

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
		if debugLevel3 {
			cb.debugLog.Log("[keywordSearch] Symbol index miss; falling back to scanning chunk names")
		}
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
	if debugLevel3 {
		cb.debugLog.Log("[keywordSearch] COMPLETE: Returning %d scored chunks", len(scored))
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
		if debugLevel3 {
			cb.debugLog.Log("[invertedKeywordSearchBM25] Inverted index is nil; falling back to keywordSearch")
		}
		return cb.keywordSearch(query, topK, hc)
	}
	keywords := extractQueryKeywords(strings.ToLower(query))
	symbols := extractPotentialSymbols(query)
	terms := uniqueStrings(append(keywords, symbols...))

	if debugLevel3 {
		cb.debugLog.Log("[invertedKeywordSearchBM25] START: terms=%v, topK=%d", terms, topK)
	}

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
	if debugLevel3 {
		cb.debugLog.Log("[invertedKeywordSearchBM25] COMPLETE: Returning %d BM25 scored chunks", len(result))
	}
	return result
}

func buildCallSiteIndex(cg *utils.CallGraphRef, chunks []Chunk, log *utils.DebugLogger) callSiteIndex {
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
	if debugLevel3 {
		cb.debugLog.Log("[vectorSearch] START: embeddingsCount=%d, threshold=%.2f, topK=%d", n, threshold, topK)
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
	if debugLevel3 {
		cb.debugLog.Log("[vectorSearch] COMPLETE: Returning %d vector matches above threshold", len(scored))
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

	if debugLevel3 {
		cb.debugLog.Log("[explicitAnchorSearch] START: processing %d explicit anchors", len(anchors))
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
	if debugLevel3 {
		cb.debugLog.Log("[explicitAnchorSearch] COMPLETE: Found %d anchor hits", len(results))
	}
	return results
}
