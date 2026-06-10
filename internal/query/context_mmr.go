//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides the context window builder and query routing for Eulix's
// RAG (Retrieval-Augmented Generation) system.

/*
This file implements chunk ranking, diversity-aware selection,
and context assembly for the RAG pipeline.

This file provides two selection strategies:

1. MMR (Maximal Marginal Relevance) selection — preferred when embeddings are available.
   Uses a weighted combination of query relevance and diversity (inter-chunk similarity)
   to avoid redundant context. The mmrLambda parameter (0.7) weights relevance 70% and
   diversity 30%, tuned to favor topically-relevant chunks while penalizing
   near-duplicates. Anchor files (exact symbol matches) receive a 1.25× relevance boost.

2. Greedy selection — fallback when embeddings are unavailable.
   Sorts chunks by score, groups by file to maintain code locality, and merges
   adjacent chunks (within 5 lines) to reduce header overhead.

Similarity computation (simBetween) uses embeddings when available, but falls back
to boilerplate-filtered symbol overlap (Jaccard similarity). This hybrid approach
ensures meaningful diversity even in low-embedding regimes.

Key optimizations:
  - Boilerplate filtering: common tokens (ctx, err, i, j) are stripped before
    computing symbol overlap, reducing false positives on unrelated chunks.
  - Chunk merging: adjacent chunks in the same file are fused to reduce per-chunk
    overhead (20 tokens) and produce contiguous code spans.
  - Same-file locality penalty: chunks in the same file separated by > 150 lines
    have their inter-similarity penalized by 1.20× to encourage cross-file diversity.
  - Token budgeting: selection respects per-query token limits; chunks are ranked
    and added until the budget is exhausted.

Trace output (ChunkTrace) captures:
  - Inclusion decision and exclusion reason (budget exhausted, redundancy, etc.)
  - Match type and details (exact symbol, semantic, keyword, etc.)
  - Final rank and token cost
*/

package query

import (
	"fmt"
	"math"
	"sort"
)

const (
	mmrLambda            = 0.7
	headerOverhead       = 20
	distantLineThreshold = 150
	simPenaltyFactor     = 1.20
)

func (cb *ContextBuilder) mmrSelect(
	candidates []ScoredChunk,
	budget int,
	qEmb []float32,
	anchorFiles map[string]bool,
	trace *DebugTrace,
	gate PathGate,
) []Chunk {
	// Pre-filter candidates through the gate before the MMR loop.
	// This is a second line of defence: multiStrategySearch already
	// ran applyGate per strategy, but multiBoost accumulation across
	// strategies can resurrect off-path chunks that each individually
	// slipped through. Filtering here is cheap (one pass, pre-loop).
	if gate.active {
		filtered := make([]ScoredChunk, 0, len(candidates))
		for _, c := range candidates {
			if gate.Pass(c.File) {
				c.Score *= gate.Boost(c.File)
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
		if trace != nil {
			trace.Warnings = append(trace.Warnings,
				fmt.Sprintf("gate filtered to %d candidates", len(candidates)))
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	maxSc := 0.0
	for _, c := range candidates {
		if c.Score > maxSc {
			maxSc = c.Score
		}
	}
	if maxSc == 0 {
		maxSc = 1
	}

	embOf := func(id string) []float32 {
		if idx, ok := cb.vectorMap[id]; ok && idx < len(cb.embeddings) {
			return cb.embeddings[idx]
		}
		return nil
	}

	simToQuery := func(c ScoredChunk) float64 {
		base := 0.0
		if qEmb != nil {
			if e := embOf(c.ID); e != nil {
				base = cosineSimilarity(qEmb, e)
			}
		}
		if base == 0 {
			base = c.Score / maxSc
		}
		if anchorFiles[c.File] {
			base = math.Min(1.0, base*1.25)
		}
		return base
	}

	simBetween := func(a, b ScoredChunk) float64 {
		if ea, eb := embOf(a.ID), embOf(b.ID); ea != nil && eb != nil {
			sim := cosineSimilarity(ea, eb)

			if a.File == b.File && sim > 0.4 {
				dist := a.StartLine - b.StartLine
				if dist < 0 {
					dist = -dist
				}
				if dist > distantLineThreshold {
					sim = math.Min(1.0, sim*simPenaltyFactor)
				}
			}
			return sim
		}

		filtered := func(syms []string) []string {
			out := make([]string, 0, len(syms))
			for _, s := range syms {
				if !cb.isBoilerplateSymbol(s) {
					out = append(out, s)
				}
			}
			return out
		}

		aSyms := filtered(a.Symbols)
		bSyms := filtered(b.Symbols)

		if len(aSyms) == 0 && len(bSyms) == 0 {
			return 1.0
		}
		if len(aSyms) == 0 || len(bSyms) == 0 {
			return 0.0
		}

		setA := make(map[string]bool, len(aSyms))
		for _, s := range aSyms {
			setA[s] = true
		}
		inter := 0
		for _, s := range bSyms {
			if setA[s] {
				inter++
			}
		}
		union := len(aSyms) + len(bSyms) - inter
		if union == 0 {
			return 0
		}
		return float64(inter) / float64(union)
	}

	remaining := make([]ScoredChunk, len(candidates))
	copy(remaining, candidates)

	selected := make([]Chunk, 0, 24)
	selSC := make([]ScoredChunk, 0, 24)
	tokenSum := 0
	chunkTraces := make([]ChunkTrace, 0, len(candidates))

	for len(remaining) > 0 {
		bestIdx, bestMMR := -1, -math.MaxFloat64

		for i, c := range remaining {
			rel := simToQuery(c)

			maxRedund := 0.0
			for _, sel := range selSC {
				if r := simBetween(c, sel); r > maxRedund {
					maxRedund = r
				}
			}

			if mmr := mmrLambda*rel - (1-mmrLambda)*maxRedund; mmr > bestMMR {
				bestMMR, bestIdx = mmr, i
			}
		}

		if bestIdx < 0 {
			break
		}

		pick := remaining[bestIdx]
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)

		cost := pick.Tokens + headerOverhead
		ct := ChunkTrace{
			ID:           pick.ID,
			File:         pick.File,
			Lines:        [2]int{pick.StartLine, pick.EndLine},
			Tokens:       pick.Tokens,
			Score:        pick.Score,
			MatchType:    pick.MatchType,
			MatchDetails: pick.MatchDetails,
			Rank:         len(selected) + 1,
		}

		if tokenSum+cost > budget {
			ct.Included = false
			ct.ExcludeReason = "exceeds token budget"
			chunkTraces = append(chunkTraces, ct)
			continue
		}

		ct.Included = true
		chunkTraces = append(chunkTraces, ct)
		selected = append(selected, pick.Chunk)
		selSC = append(selSC, pick)
		tokenSum += cost

		if n := len(selected); n > 1 && canMerge(selected[n-2], selected[n-1]) {
			selected[n-2] = mergeChunks(selected[n-2], selected[n-1])
			selected = selected[:n-1]
			selSC = selSC[:n-1]
			tokenSum -= headerOverhead
		}
	}

	if trace != nil {
		trace.ChunkTraces = chunkTraces
	}
	return selected
}

//  Exact / partial symbol search

func (cb *ContextBuilder) selectChunks(scored []ScoredChunk, budget int) []Chunk {
	selected := make([]Chunk, 0)
	tokenSum := 0
	hdr := 20

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].File != scored[j].File {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].StartLine < scored[j].StartLine
	})
	for _, sc := range scored {
		if tokenSum+sc.Tokens+hdr > budget {
			break
		}
		if n := len(selected); n > 0 && canMerge(selected[n-1], sc.Chunk) {
			selected[n-1] = mergeChunks(selected[n-1], sc.Chunk)
			tokenSum += sc.Tokens
		} else {
			selected = append(selected, sc.Chunk)
			tokenSum += sc.Tokens + hdr
		}
	}
	return selected
}

func canMerge(a, b Chunk) bool {
	if a.File != b.File {
		return false
	}
	gap := 0
	if a.EndLine < b.StartLine {
		gap = b.StartLine - a.EndLine
	} else if b.EndLine < a.StartLine {
		gap = a.StartLine - b.EndLine
	}
	return gap <= 5
}

func mergeChunks(a, b Chunk) Chunk {
	start, end := a.StartLine, a.EndLine
	if b.StartLine < start {
		start = b.StartLine
	}
	if b.EndLine > end {
		end = b.EndLine
	}
	content := a.Content
	if b.StartLine > a.EndLine {
		content += "\n" + b.Content
	} else if a.StartLine > b.EndLine {
		content = b.Content + "\n" + content
	}
	symMap := make(map[string]bool)
	syms := make([]string, 0)
	for _, s := range append(a.Symbols, b.Symbols...) {
		if !symMap[s] {
			symMap[s] = true
			syms = append(syms, s)
		}
	}
	return Chunk{
		ID: a.ID, ChunkType: a.ChunkType, File: a.File,
		StartLine: start, EndLine: end,
		Content:    content,
		Tokens:     a.Tokens + b.Tokens,
		Symbols:    syms,
		Name:       a.Name,
		Importance: math.Max(a.Importance, b.Importance),
	}
}
