//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides the context window builder and query routing for Eulix's
RAG (Retrieval-Augmented Generation) system.

This file implements the multi-strategy retrieval pipeline that combines five
orthogonal search techniques to maximize recall and precision:

 1. KB Exact Lookup: Symbol-based search in the knowledge base (highest precision)
 2. Exact Symbol Search: Direct chunk name and symbol matching (high precision)
 3. Partial Identifier Match: Token-level matching on camelCase/snake_case names
 4. Keyword Search: Content-based TF-IDF or linear scan with symbol boosting
 5. Semantic Search: Vector embeddings (optional, skipped for certain intents)

Strategy execution:
  - Each strategy runs independently and returns scored chunks.
  - Results are merged into a deduplication map (by chunk ID).
  - When a chunk appears in multiple strategies, scores are combined with
    inter-strategy boost factors (multiBoost), and MatchType is concatenated.
  - Final ranking: Exact matches pinned to top, then by score descending.
  - Only top-K results returned for efficiency.

Intent-aware budget allocation:
  - IntentCallers/IntentCallees: Skip semantic search (focused, structural intent)
  - High specificity (>0.85): Skip semantic search (query is precise enough)
  - Otherwise: Allocate keyword/semantic budgets based on intent.Budget()

Keyword search modes:
  - Inverted index (invertedKeywordSearch): For large corpora (>5k chunks),
    uses TF-IDF with postings lists for O(term_count) lookup.
  - Linear scan (keywordSearch): For small corpora, scans all chunks
    with symbol name boosting and underscore penalization.

Call-site indexing:
  - buildCallSiteIndex scans chunk content for function calls (identifier + "(" pattern)
  - Used by callee/caller discovery strategies to find related code.

Vector search modes:
  - IVF approximate search (vectorSearchIVF): For >10k embeddings, clusters
    via k-means and probes nProbe closest clusters.
  - Brute-force search (vectorSearch): For smaller corpora, compares against all.

See:
  - context_kb.go: kbExactLookup implementation and KB symbol matching
  - context_intent.go: QueryIntent, budget allocation, intent types
  - context_vectorIVF.go: IVF index construction and approximate search
  - boilerplate.go: Symbol filtering in keyword/inverted index paths
  - context_mmr.go: Diversity-aware selection after multi-strategy merging
*/
package query

import (
	"math"
	"sort"
	"strings"
	"time"
)

// multiStrategySearch orchestrates five orthogonal retrieval strategies and
// merges their results into a single ranked list.
//
// Strategies executed (in order):
//  1. kb_exact (if hasKB): KB-based symbol lookup, multiBoost=2.5
//  2. exact: Direct chunk name and symbol matching, multiBoost=2.0
//  3. partial: Token-level identifier matching, multiBoost=1.5
//  4. keyword: TF-IDF or linear scan with symbol boosting, multiBoost=2.0
//  5. semantic: Vector embeddings (optional, multiBoost=1.5)
//
// Merging:
//   - Each chunk is identified by its unique ID.
//   - If a chunk appears in multiple strategies, scores are combined as:
//     new_score = max(existing_score, strategy_score) + multiBoost
//   - MatchType is concatenated: "exact+keyword"
//   - Final result is sorted by MatchType (exact first) then score descending.
//
// Keyword budget:
//   - kwTopK = topK * (0.4 + 0.3 * intent.Budget()["keyword"])
//   - Inverted index used if available, otherwise linear scan.
//   - Symbol boosting: +25.0 if query symbol appears as call in chunk.
//   - Underscore penalization: -10.0 for _symbol matches.
//
// Semantic skipping:
//   - Skipped if intent is IntentCallers or IntentCallees (structural).
//   - Skipped if specificity > 0.85 (query is precise enough).
//   - semTopK = topK * (0.3 + 0.3 * intent.Budget()["semantic"]) when enabled.
//
// Tracing:
//   - If trace is non-nil, appends StrategyTrace for each strategy
//     with timing, result count, top score, and average score.
//
// See:
//   - kbExactLookup: KB symbol-based retrieval
//   - exactSymbolSearch: Direct chunk name/symbol matching
//   - partialIdentifierMatch: Token-level matching
//   - keywordSearch / invertedKeywordSearch: Content-based retrieval
//   - vectorSearchIVF / vectorSearch: Semantic vector search
//   - context_intent.go: QueryIntent and budget allocation
func (cb *ContextBuilder) multiStrategySearch(
	query string, topK int, intent QueryIntent, trace *DebugTrace,
) []ScoredChunk {
	all := make(map[string]ScoredChunk, topK*2)

	run := func(name string, fn func() []ScoredChunk, multiBoost float64) {
		t0 := time.Now()
		results := fn()
		st := StrategyTrace{Name: name, Found: len(results), Duration: time.Since(t0)}
		sum := 0.0
		for _, m := range results {
			m.MatchType = name
			if ex, ok := all[m.ID]; ok {
				m.Score = math.Max(ex.Score, m.Score) + multiBoost
				m.MatchType = ex.MatchType + "+" + name
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

	kwTopK := int(float64(topK) * (0.4 + 0.3*intent.Budget()["keyword"]))
	syms := extractPotentialSymbols(query)
	run("keyword", func() []ScoredChunk {
		var res []ScoredChunk
		if cb.invertedIdx != nil {
			res = cb.invertedKeywordSearch(query, kwTopK)
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
			// Penalise _sym* matches
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

	if cb.hasEmbeddings && !skipSemantic {
		semTopK := int(float64(topK) * (0.3 + 0.3*intent.Budget()["semantic"]))
		run("semantic", func() []ScoredChunk {
			qEmb, err := cb.queryEmbedder.EmbedQueryBinary(query)
			if err != nil {
				return nil
			}
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
	sort.Slice(result, func(i, j int) bool {
		if result[i].MatchType == "exact" && result[j].MatchType != "exact" {
			return true
		}
		if result[i].MatchType != "exact" && result[j].MatchType == "exact" {
			return false
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
//
// Scoring:
//   - Exact name match: 100.0
//   - Exact symbol match: 90.0
//
// See:
//   - extractPotentialSymbols: Query tokenization
//   - multiStrategySearch: Orchestration point
//   - partialIdentifierMatch: Lower-precision token-level matching
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

// partialIdentifierMatch performs token-level matching on identifiers.
// Splits camelCase and snake_case names and symbols, then matches against
// query tokens. Returns chunks with 2+ token matches or 1 match with score ≥15.
//
// Scoring per match type:
//   - Exact token match in chunk name: +15.0
//   - Substring match in chunk name: +8.0
//   - Exact token match in chunk symbol: +12.0
//   - Substring match in chunk symbol: +6.0
//
// Filtering: Only returns chunks with matchCount >= 2 or (matchCount == 1 && score >= 15).
// Deduplication by chunk ID to avoid scoring same chunk multiple times.
//
// See:
//   - splitIdentifierToTokens: CamelCase/snake_case tokenization
//   - extractPotentialSymbols: Query tokenization
//   - multiStrategySearch: Orchestration point
func (cb *ContextBuilder) partialIdentifierMatch(query string) []ScoredChunk {
	qTokens := extractPotentialSymbols(query)
	scored := make([]ScoredChunk, 0)
	seen := make(map[string]bool)

	for _, chunk := range cb.chunks {
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
				matchedToks = append(matchedToks, qtLow)
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
					matchedToks = append(matchedToks, qtLow)
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

// keywordSearch performs linear content scanning with TF-based scoring.
// For each chunk, scores are accumulated from:
//   - Symbol name exact/substring match: ±20.0 / +10.0
//   - Chunk symbol exact/substring match: +15.0 / +7.0
//   - Chunk symbol keyword match: +10.0 / +5.0
//   - Content keyword match: +2.0
//   - File path keyword match: +1.0
//   - Chunk type bonus (function +1.0, class +0.8, method +0.6)
//   - Underscore penalization: -10.0 for _symbol matches
//
// Only returns chunks with score > 0, sorted by score descending, limited to topK.
// Hydrates content on-demand if lazy-loaded.
//
// See:
//   - extractQueryKeywords: Query tokenization
//   - extractPotentialSymbols: Potential identifiers extraction
//   - invertedKeywordSearch: TF-IDF variant using inverted index
//   - hydrateOne: Content materialization for lazy-loaded chunks
func (cb *ContextBuilder) keywordSearch(query string, topK int) []ScoredChunk {
	qLow := strings.ToLower(query)
	keywords := extractQueryKeywords(qLow)
	potSyms := extractPotentialSymbols(query)
	scored := make([]ScoredChunk, 0)

	for _, chunk := range cb.chunks {
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
			// Penalise _sym* matches
			if strings.HasPrefix(nameLow, "_"+qsLow) {
				score -= 10.0
			}
		}
		for _, sym := range chunk.Symbols {
			symLow := strings.ToLower(sym)
			for _, qs := range potSyms {
				qsLow := strings.ToLower(qs)
				if symLow == qsLow {
					score += 15.0
					details = append(details, "symbol="+sym)
				} else if strings.Contains(symLow, qsLow) {
					score += 7.0
				}
			}
			for _, kw := range keywords {
				if symLow == kw {
					score += 10.0
				} else if strings.Contains(symLow, kw) {
					score += 5.0
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
		switch chunk.ChunkType {
		case "function":
			score += 1.0
		case "class":
			score += 0.8
		case "method":
			score += 0.6
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
//
// Algorithm:
//  1. Extract keywords and symbols from query
//  2. Combine unique tokens for postings lookup
//  3. For each term, retrieve postings (chunkIdx, TF)
//  4. Score each chunk as: sum(TF * IDF) where IDF = log(docCount / docFreq) + 1
//  5. Apply symbol name boost: +20.0 if chunk name == query symbol
//  6. Return top-K results sorted by score descending
//
// Locking: Acquires RWMutex read lock on invertedIdx for thread-safe access.
//
// See:
//   - InvertedIndex: Postings lists and term frequency data structure
//   - keywordSearch: Linear scan alternative
//   - extractQueryKeywords / extractPotentialSymbols: Query tokenization
func (cb *ContextBuilder) invertedKeywordSearch(query string, topK int) []ScoredChunk {
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

// buildCallSiteIndex scans all chunks for function call sites (identifier + "(" pattern).
// Returns a map from function name to chunk indices where that function is called.
//
// Used for caller/callee discovery: if chunk[i] calls foo(), then callSiteIndex["foo"]
// includes i, enabling quick lookup of callers.
//
// Algorithm:
//  1. For each chunk, scan content for "(" characters
//  2. Backtrack from each "(" to collect the identifier (alphanumeric + underscore)
//  3. Extract lowercased function name
//  4. Append chunk index to callSiteIndex[name]
//
// See:
//   - isIdentRune: Character predicate for identifier matching
//   - context_graph.go: expandGraph uses this index for caller/callee expansion
//   - context_loader.go: Called during KB loading
func buildCallSiteIndex(chunks []Chunk) callSiteIndex {
	idx := make(callSiteIndex, 512)

	for i, chunk := range chunks {
		content := chunk.Content
		if content == "" {
			continue
		}

		for j := 0; j < len(content); {
			paren := strings.IndexByte(content[j:], '(')
			if paren < 0 {
				break
			}
			paren += j

			start := paren - 1
			for start >= 0 && isIdentRune(content[start]) {
				start--
			}
			start++

			if start < paren {
				sym := strings.ToLower(content[start:paren])
				idx[sym] = append(idx[sym], i)
			}
			j = paren + 1
		}
	}
	return idx
}

// vectorSearch performs brute-force semantic search over all embeddings.
// Compares query embedding against all chunk embeddings using cosine similarity.
// Returns chunks with similarity >= threshold, sorted by score descending, limited to topK.
//
// Complexity: O(n * d) where n = number of embeddings, d = embedding dimension.
// Use vectorSearchIVF for n > 10k to reduce to O(k*d + nProbe*L).
//
// Threshold typically 0.5 for semantic search (cosine similarity in [0, 1]).
//
// See:
//   - vectorSearchIVF: Approximate search using IVF clustering
//   - cosineSimilarity: Embedding distance metric
//   - context_vectorIVF.go: IVF index and approximate search
//   - context_loader.go: ivfBuildThreshold decision
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
