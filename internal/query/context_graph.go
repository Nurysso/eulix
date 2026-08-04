//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides the context window builder and query routing for Eulix's
// RAG (Retrieval-Augmented Generation) system.

/*
This files implements context expansion via call-graph relationships
and call-site discovery for graph-aware RAG.

Provides three context expansion strategies:

1. Call-site discovery (findCallSites) — literal string scanning for ".symbol(" patterns.
   O(n × |symbols|) but the single most reliable signal for "who calls X" queries.
   Runs first before fuzzy matching. Returns chunks containing direct call sites with
   high confidence scores (95.0 for call sites, 70.0 for definitions).

2. Graph-based expansion (buildContextWithGraph) — uses the loaded call graph to
   expand initial candidates with related functions. Walks the top-20 candidates,
   follows their call relationships (calls + called_by), and re-scores connected
   chunks based on query intent:
     - IntentCallers: boost "called_by" relationships (1.2×)
     - IntentCallees: boost "calls" relationships (1.2×)
     - Other: apply conservative penalties (0.9× for direct calls, 0.6× for distant)
   Limits expansion to 15 relationships to avoid context bloat.

3. Non-graph fallback (buildContextWithoutGraph) — when call graph is unavailable.
   Identifies "hot files" (files with ≥3 chunks in candidates), computes average
   chunk scores per file, and boosts chunks from hot files by 0.2 to encourage
   code locality.

KB function expansion (expandFromKBFunction) — used during graph expansion to
materialize called functions. Looks up the callee in kb.json, extracts its full
definition (signature, params, docstring, complexity), and wraps it as a ScoredChunk.
Applies decay (0.75×) and same-file bonus (1.03×).

Integration points:
  - findCallSites: runs before multi-strategy search (highest priority)
  - buildContextWithGraph: used if call graph is loaded (cb.hasCallGraph)
  - buildContextWithoutGraph: fallback for corpora without call graphs
  - expandFromKBFunction: called during graph expansion to materialize kb.json entries

Boilerplate filtering note:
  - Call-site patterns are matched literally (symbol name in a call expression)
  - Symbol lookups via cb.symbolIndex already filter boilerplate (see context_loader.go)
  - Graph relationship lookups (cb.callGraph[sym]) also respect boilerplate filtering
    if the symbol index is pre-filtered
*/

package query

import (
	"sort"
	"strings"

	"eulix/internal/types"
)

// findCallSites does a literal string scan for ".symbol(" patterns across all
// chunks. This is O(n × |symbols|) but is the single most reliable signal for
// "who calls X" queries — this should run first, before any fuzzy matching.
func (cb *ContextBuilder) findCallSites(query string, intent QueryIntent) []ScoredChunk {
	symbols := extractPotentialSymbols(query)
	results := make([]ScoredChunk, 0, 32)
	seen := make(map[int]bool)

	for _, sym := range symbols {
		symLower := strings.ToLower(sym)

		indices, ok := cb.callSites[symLower]
		if !ok {
			continue
		}

		for _, idx := range indices {
			if seen[idx] {
				continue
			}
			seen[idx] = true

			chunk := cb.chunks[idx]

			score := 95.0
			matchDetail := "calls " + sym

			if intent.Type == IntentCallees && strings.EqualFold(chunk.Name, sym) {
				score = 70.0
				matchDetail = "definition of " + sym
			}
			if strings.HasPrefix(strings.ToLower(chunk.Name), "_"+symLower) {
				score -= 20.0
			}

			results = append(results, ScoredChunk{
				Chunk:        chunk,
				Score:        score,
				MatchType:    "callsite",
				MatchDetails: matchDetail,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results
}

func (cb *ContextBuilder) buildContextWithGraph(
	candidates []ScoredChunk, budget int, intent QueryIntent,
) []ScoredChunk {
	expanded := make(map[string]ScoredChunk, len(candidates))
	for _, c := range candidates {
		expanded[c.ID] = c
	}
	topN := 20
	if len(candidates) < topN {
		topN = len(candidates)
	}
	const maxGraphExpansions = 15
	relCount := 0

outerLoop:
	for i := 0; i < topN; i++ {
		cand := candidates[i]
		for _, sym := range cand.Symbols {
			if rels, ok := cb.callGraph[sym]; ok {
				for _, rel := range rels {
					if relCount >= maxGraphExpansions {
						break outerLoop
					}
					score := cand.Score

					switch {
					case intent.Type == IntentCallers && rel.Type == "called_by":
						score *= 1.2
					case intent.Type == IntentCallees && rel.Type == "calls":
						score *= 1.2
					case rel.Type == "calls" || rel.Type == "called_by":
						score *= 0.9
					case rel.Distance <= 2:
						score *= 0.6
					default:
						continue
					}

					for _, idx := range cb.symbolIndex[rel.Target] {
						chunk := cb.chunks[idx]
						if ex, ok := expanded[chunk.ID]; !ok || score > ex.Score {
							expanded[chunk.ID] = ScoredChunk{
								Chunk: chunk, Score: score,
								Distance: rel.Distance, FromID: cand.ID,
							}
						}
					}
					relCount++ // count per relationship processed, not per symbol
				}
			}
		}
	}

	result := make([]ScoredChunk, 0, len(expanded))
	for _, sc := range expanded {
		result = append(result, sc)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	// Enforce token budget walk higest scored chunk first, stop when full
	used := 0
	for i, sc := range result {
		used += sc.Chunk.Tokens
		if used > budget {
			return result[:i]
		}
	}
	return result
}

func (cb *ContextBuilder) buildContextWithoutGraph(candidates []ScoredChunk, budget int) []ScoredChunk {
	if len(candidates) < 20 {
		return cb.applyBudget(candidates, budget)
	}

	fileGroups := make(map[string][]ScoredChunk)
	for _, c := range candidates {
		fileGroups[c.File] = append(fileGroups[c.File], c)
	}
	type hotFile struct {
		file     string
		avgScore float64
	}
	hot := make([]hotFile, 0)
	for file, chunks := range fileGroups {
		if len(chunks) >= 3 {
			sum := 0.0
			for _, c := range chunks {
				sum += c.Score
			}
			hot = append(hot, hotFile{file, sum / float64(len(chunks))})
		}
	}
	sort.Slice(hot, func(i, j int) bool { return hot[i].avgScore > hot[j].avgScore })
	hotMap := make(map[string]float64, len(hot))
	for _, h := range hot {
		hotMap[h.file] = h.avgScore
	}
	candidatesCopy := make([]ScoredChunk, len(candidates))
	copy(candidatesCopy, candidates)
	for i := range candidatesCopy {
		if _, ok := hotMap[candidatesCopy[i].File]; ok {
			candidatesCopy[i].Score += 0.2
		}
	}
	sort.Slice(candidatesCopy, func(i, j int) bool { return candidatesCopy[i].Score > candidatesCopy[j].Score })
	return candidatesCopy
}

func (cb *ContextBuilder) expandFromKBFunction(fn types.KBFunction, filePath string, baseScore float64) []ScoredChunk {
	// Safe logging helper
	log := func(format string, args ...interface{}) {
		if cb.debugLog != nil {
			cb.debugLog.Log(format, args...)
		}
	}

	exp := make([]ScoredChunk, 0)
	const maxCallees = 5

	for i, call := range fn.Calls {
		if len(exp) >= maxCallees {
			break
		}

		log("Processing call %d: Callee=%s", i, call.Callee)

		// Safe nil check for DefinedIn
		if call.DefinedIn == nil {
			log("Skipping call %s: DefinedIn is nil", call.Callee)
			continue
		}

		definedIn := *call.DefinedIn
		if definedIn == "" {
			log("Skipping call %s: DefinedIn is empty", call.Callee)
			continue
		}

		// Check if kbData exists
		if cb.kbData == nil {
			log("kbData is nil, cannot expand")
			continue
		}

		fs, ok := cb.kbData.Structure[definedIn]
		if !ok {
			log("File not found: %s", definedIn)
			continue
		}

		// Find the function
		found := false
		for _, calledFn := range fs.Functions {
			if calledFn.Name == call.Callee {
				score := baseScore * 0.75
				if definedIn == filePath {
					score *= 1.03
				}
				exp = append(exp, ScoredChunk{
					Chunk:        cb.buildChunkFromKBFunction(calledFn, definedIn),
					Score:        score,
					Distance:     1,
					MatchType:    "kb_called",
					MatchDetails: "Called by " + fn.Name,
				})
				found = true
				break
			}
		}

		if !found {
			log("Callee %s not found in file %s", call.Callee, definedIn)
			// Continue to next call - no crash
		}
	}

	return exp
}
