//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides context window building and query routing for Eulix.

package query

import (
	"slices"
	"strings"

	"eulix/internal/types"
)

const (
	topNCandidates     = 20
	maxGraphExpansions = 15 // cap on relationship edges processed
	maxNewChunksPerRel = 5  // cap chunks pulled per relationship, so one
	maxCallees         = 5
	// widely-referenced symbol can't flood the result
)

// findCallSites scans for exact symbol matches across indexed call sites and returns
// scored chunks representing call sites and function definitions.
func (cb *ContextBuilder) findCallSites(query string, intent QueryIntent) []ScoredChunk {
	symbols := extractPotentialSymbols(query)
	results := make([]ScoredChunk, 0, 32)
	seen := make(map[int]bool, 32)

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
				// This "call site" is actually the symbol's own definition,
				// not a caller -- deprioritize it for a callees query.
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

	slices.SortFunc(results, func(a, b ScoredChunk) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return 0
		}
	})
	return results
}

// buildContextWithGraph expands top candidate chunks using call graph relationships up to
// 15 total expansions, applies intent-based score multipliers, and truncates the sorted
// candidates to fit within the token budget.
func (cb *ContextBuilder) buildContextWithGraph(
	candidates []ScoredChunk, budget int, intent QueryIntent,
) []ScoredChunk {
	// pos + result replaces map[string]ScoredChunk: updates mutate 3 fields
	// in place via index instead of copying the whole (embedded-Chunk) struct,
	// and we skip the final "flatten map into slice" pass entirely.
	result := make([]ScoredChunk, 0, len(candidates)+maxGraphExpansions*maxNewChunksPerRel)
	pos := make(map[string]int, len(candidates)+maxGraphExpansions*maxNewChunksPerRel)

	for _, c := range candidates {
		pos[c.ID] = len(result)
		result = append(result, c)
	}

	topN := len(candidates)
	if topN > topNCandidates {
		topN = topNCandidates
	}

	relCount := 0
outerLoop:
	for i := 0; i < topN; i++ {
		cand := candidates[i]
		for _, sym := range cand.Symbols {
			rels, ok := cb.callGraph[sym]
			if !ok {
				continue
			}
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
				relCount++

				targets := cb.symbolIndex[rel.Target]
				if len(targets) > maxNewChunksPerRel {
					targets = targets[:maxNewChunksPerRel]
				}
				for _, idx := range targets {
					chunk := cb.chunks[idx]
					if j, ok := pos[chunk.ID]; ok {
						// Bump score/distance/provenance only -- never touch
						// MatchType/MatchDetails/IsExact of an existing entry.
						if ex := &result[j]; score > ex.Score {
							ex.Score = score
							ex.Distance = rel.Distance
							ex.FromID = cand.ID
						}
						continue
					}
					pos[chunk.ID] = len(result)
					result = append(result, ScoredChunk{
						Chunk:    chunk,
						Score:    score,
						Distance: rel.Distance,
						FromID:   cand.ID,
					})
				}
			}
		}
	}

	slices.SortFunc(result, func(a, b ScoredChunk) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return 0
		}
	})

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

	type fileStats struct {
		count int
		sum   float64
	}
	stats := make(map[string]*fileStats)
	for _, c := range candidates {
		s, ok := stats[c.File]
		if !ok {
			s = &fileStats{}
			stats[c.File] = s
		}
		s.count++
		s.sum += c.Score
	}

	hot := make(map[string]struct{}, len(stats))
	for file, s := range stats {
		if s.count >= 3 {
			hot[file] = struct{}{}
		}
	}

	candidatesCopy := make([]ScoredChunk, len(candidates))
	copy(candidatesCopy, candidates)
	for i := range candidatesCopy {
		if _, ok := hot[candidatesCopy[i].File]; ok {
			candidatesCopy[i].Score += 0.2
		}
	}

	slices.SortFunc(candidatesCopy, func(a, b ScoredChunk) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return 0
		}
	})
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

	if cb.kbData == nil {
		log("kbData is nil, cannot expand")
		return nil
	}

	exp := make([]ScoredChunk, 0, maxCallees)

	for i, call := range fn.Calls {
		if len(exp) >= maxCallees {
			break
		}

		log("Processing call %d: Callee=%s", i, call.Callee)

		if call.DefinedIn == nil || *call.DefinedIn == "" {
			log("Skipping call %s: DefinedIn missing", call.Callee)
			continue
		}
		definedIn := *call.DefinedIn

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
