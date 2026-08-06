//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides context window building and query routing for Eulix.

package query

import (
	"sort"
	"strings"

	"eulix/internal/types"
)

// findCallSites scans for exact symbol matches across indexed call sites and returns
// scored chunks representing call sites and function definitions.
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

		if intent.Type == IntentCallees {
			for _, rel := range cb.callGraph[symLower] {
				if rel.Type != "calls" {
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
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results
}

// buildContextWithGraph expands top candidate chunks using call graph relationships up to
// 15 total expansions, applies intent-based score multipliers, and truncates the sorted
// candidates to fit within the token budget.
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
								Chunk:    chunk,
								Score:    score,
								Distance: rel.Distance,
								FromID:   cand.ID,
							}
						}
					}
					relCount++
				}
			}
		}
	}

	result := make([]ScoredChunk, 0, len(expanded))
	for _, sc := range expanded {
		result = append(result, sc)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })

	used := 0
	for i, sc := range result {
		used += sc.Tokens
		if used > budget {
			return result[:i]
		}
	}
	return result
}

// buildContextWithoutGraph boosts candidate scores for files with high candidate density
// when call graph relationships are not available.
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
	return cb.applyBudget(candidatesCopy, budget)
}

// expandFromKBFunction resolves functions called by fn using knowledge base data
// and returns them as scored chunks up to a maximum limit of 5 callees.
func (cb *ContextBuilder) expandFromKBFunction(fn types.KBFunction, filePath string, baseScore float64) []ScoredChunk {
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

		if call.DefinedIn == nil {
			log("Skipping call %s: DefinedIn is nil", call.Callee)
			continue
		}

		definedIn := *call.DefinedIn
		if definedIn == "" {
			log("Skipping call %s: DefinedIn is empty", call.Callee)
			continue
		}

		if cb.kbData == nil {
			log("kbData is nil, cannot expand")
			continue
		}

		fs, ok := cb.kbData.Structure[definedIn]
		if !ok {
			log("File not found: %s", definedIn)
			continue
		}

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
		}
	}

	return exp
}
