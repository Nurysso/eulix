//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides the context window builder and query routing for Eulix's
// RAG (Retrieval-Augmented Generation) system.

/*
Package query provides context window building and query routing for Eulix's RAG system.
Key Responsibilities:
  - Applies MMR or greedy file-locality strategies for diversity-aware chunk selection
  - Merges adjacent code spans and penalizes same-file redundancy
  - Assembles final context windows within strict token budgets and records chunk traces
*/
package query

import (
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
)

const (
	// defaultMMRLambda balances relevance vs. diversity in MMR re-ranking.
	// Higher values (towards 1.0) prioritize query relevance; lower values promote diversity.
	defaultMMRLambda = 0.65

	// headerOverhead is the estimated token/character cost added per context header section.
	headerOverhead = 20

	// distantLineThreshold is the maximum line distance between chunks before
	// treating them as non-contiguous context.
	distantLineThreshold = 150

	// simPenaltyFactor scales the penalty applied to redundant candidates during MMR selection.
	simPenaltyFactor = 1.20

	// anchorFileBonus is an additive score bonus (applied post-normalization)
	// for chunks originating from files explicitly referenced in the user's query.
	anchorFileBonus = 0.15

	// pinnedRedundancyDamp reduces the MMR similarity penalty for candidates
	// that overlap with pinned chunks. This prevents punishing context that is
	// directly related to explicitly requested information.
	pinnedRedundancyDamp = 0.55
)

// mmrSemanticWeight blends cosine(query,chunk) into relevance for EVERY candidate
// not only the ones that happened to come back from the semantic strategy.
// 0 disables the blend (pure fused-score relevance)
var mmrSemanticWeight = 0.20

var debugWatchSubstr = os.Getenv("EULIX_MMR_WATCH")

// mmrCand is one candidate's working state during MMR selection.
// rel is the normalized relevance; maxRed is the highest similarity seen
// against anything already picked, updated incrementally so we don't
// recompute the full similarity matrix every round.
type mmrCand struct {
	sc     ScoredChunk
	emb    []float32
	syms   []string // non-boilerplate symbols, for the no-embedding fallback
	rel    float64
	maxRed float64
	alive  bool
}

// gateFilterBoost applies the path gate to a candidate list. Builder's greedy path
// duplicates this loop; it can call this instead.
func gateFilterBoost(gate PathGate, in []ScoredChunk) []ScoredChunk {
	out := make([]ScoredChunk, 0, len(in))
	for _, c := range in {
		if gate.Pass(c.File) {
			c.Score *= gate.Boost(c.File)
			out = append(out, c)
		}
	}
	return out
}

func (cb *ContextBuilder) mmrSelect(
	candidates []ScoredChunk,
	budget int,
	qEmb []float32,
	anchorFiles map[string]bool,
	trace *DebugTrace,
	gate PathGate,
) []Chunk {
	// Pre-filter candidates through the gate before the MMR loop.
	if gate.active {
		candidates = gateFilterBoost(gate, candidates)
		if trace != nil {
			trace.Warnings = append(trace.Warnings,
				fmt.Sprintf("gate filtered to %d candidates", len(candidates)))
		}
	}
	if len(candidates) == 0 {
		cb.debugLog.Log("[MMR-DEBUG] candidates empty after gate: nothing to select")
		return nil
	}

	lambda := float64(cb.config.RetrievalConfig.MMRDiversityFactor)
	if lambda <= 0.0 || lambda > 1.0 {
		cb.debugLog.Log("[!] MMRDiversityFactor = %v out of range (0,1]; using default %.2f", lambda, defaultMMRLambda)
		lambda = defaultMMRLambda
	}
	maxChunks := cb.config.RetrievalConfig.MaxContextChunks
	if maxChunks <= 0 {
		maxChunks = 30
	}
	cb.debugLog.Log("MMR selection: lambda=%.2f, budget=%d, maxChunks=%d", lambda, budget, maxChunks)

	// Debug counters: how often embOf misses (ID mismatch between
	// vectorMap and chunk IDs from multiStrategySearch would show up
	// here as a spike in misses).
	var embHits, embMisses int64

	embOf := func(id string) []float32 {
		if idx, ok := cb.vectorMap[id]; ok && idx < len(cb.embeddings) {
			embHits++
			return cb.embeddings[idx]
		}
		embMisses++
		return nil
	}

	// split pinned from the pool BEFORE normalising anything
	// Pinned chunks carry score 5000+; leaving them in the pool would squash every
	// other candidate's relevance towards 0.
	var pinned []ScoredChunk
	pool := make([]ScoredChunk, 0, len(candidates))
	for _, c := range candidates {
		if c.Pinned {
			pinned = append(pinned, c)
		} else {
			pool = append(pool, c)
		}
	}

	sort.SliceStable(pinned, func(i, j int) bool {
		if pinned[i].Score != pinned[j].Score {
			return pinned[i].Score > pinned[j].Score
		}
		return pinned[i].ID < pinned[j].ID
	})
	if len(pinned) > pinnedAnchorTopN {
		cb.debugLog.Log("[MMR-DEBUG] %d pinned candidates; keeping top %d", len(pinned), pinnedAnchorTopN)
		pinned = pinned[:pinnedAnchorTopN]
	}

	cs := make([]mmrCand, len(pool))
	for i, c := range pool {
		cs[i] = mmrCand{sc: c, emb: embOf(c.ID), syms: cb.nonBoilerplate(c.Symbols), alive: true}
	}

	// Deterministic tie-breaking: equal scores are common, map-order shuffles are not welcome.
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].sc.Score != cs[j].sc.Score {
			return cs[i].sc.Score > cs[j].sc.Score
		}
		return cs[i].sc.ID < cs[j].sc.ID
	})
	fillRelevance(cs, qEmb, anchorFiles)

	for i := range cs {
		c := &cs[i]
		if mmrWatchMatch(c.sc.ID, c.sc.Name, c.sc.File) {
			cb.debugLog.Log("[MMR-DEBUG] watch candidate present pre-loop: id=%s file=%s lines=%d-%d rawScore=%.1f rel=%.4f",
				c.sc.ID, c.sc.File, c.sc.StartLine, c.sc.EndLine, c.sc.Score, c.rel)
		}
	}

	selected := make([]Chunk, 0, 24)
	chunkTraces := make([]ChunkTrace, 0, len(cs)+len(pinned))
	tokenSum := 0

	// noteSelected folds a newly selected chunk into every live candidate's maxRed.
	noteSelected := func(p *mmrCand, damp float64) {
		for i := range cs {
			c := &cs[i]
			if !c.alive {
				continue
			}
			if r := cb.mmrSim(c, p) * damp; r > c.maxRed {
				c.maxRed = r
			}
		}
	}

	// seed with pinned anchors
	for k, p := range pinned {
		cost := p.Tokens + headerOverhead
		ct := ChunkTrace{
			ID: p.ID, File: p.File, Lines: [2]int{p.StartLine, p.EndLine},
			Tokens: p.Tokens, Score: p.Score, MatchType: p.MatchType, MatchDetails: p.MatchDetails,
			Rank: len(selected) + 1, Included: true, ExcludeReason: "anchor-pinned",
		}
		// The top pin is always kept (it is what the user asked about); the rest must fit.
		if k > 0 && tokenSum+cost > budget {
			ct.Included, ct.ExcludeReason = false, "pinned but exceeds token budget"
			chunkTraces = append(chunkTraces, ct)
			cb.debugLog.Log("[MMR-DEBUG] pinned id=%s skipped: cost=%d over budget (%d/%d)", p.ID, cost, tokenSum, budget)
			continue
		}
		selected = append(selected, p.Chunk)
		tokenSum += cost
		chunkTraces = append(chunkTraces, ct)
		pc := mmrCand{sc: p, emb: embOf(p.ID), syms: cb.nonBoilerplate(p.Symbols)}
		noteSelected(&pc, pinnedRedundancyDamp)
		cb.debugLog.Log("[MMR-DEBUG] anchor-pinned id=%s file=%s lines=%d-%d score=%.1f",
			p.ID, p.File, p.StartLine, p.EndLine, p.Score)
	}

	// main MMR loop
	cb.debugLog.Log("[MMR-DEBUG] starting loop: %d candidates, maxChunks=%d, budget=%d tokens",
		len(cs), maxChunks, budget)
	stop := ""
	round := 0
	for len(selected) < maxChunks {
		round++
		if tokenSum >= budget {
			stop = "token budget exhausted"
			break
		}
		best, bestVal := -1, -math.MaxFloat64
		for i := range cs {
			c := &cs[i]
			if !c.alive {
				continue
			}
			v := lambda*c.rel - (1.0-lambda)*c.maxRed
			if mmrWatchMatch(c.sc.ID, c.sc.Name, c.sc.File) {
				cb.debugLog.Log("[MMR-DEBUG] round=%d watch id=%s file=%s rel=%.4f maxRed=%.4f mmr=%.4f (currentBest=%.4f)",
					round, c.sc.ID, c.sc.File, c.rel, c.maxRed, v, bestVal)
			}
			if v > bestVal {
				best, bestVal = i, v
			}
		}
		if best < 0 {
			stop = "candidates exhausted"
			break
		}
		pick := &cs[best]
		pick.alive = false

		ct := ChunkTrace{
			ID: pick.sc.ID, File: pick.sc.File, Lines: [2]int{pick.sc.StartLine, pick.sc.EndLine},
			Tokens: pick.sc.Tokens, Score: pick.sc.Score, MatchType: pick.sc.MatchType,
			MatchDetails: pick.sc.MatchDetails, Rank: len(selected) + 1,
		}
		var idx, cost int
		var reason string
		before := len(selected)
		selected, idx, cost, reason = addChunk(selected, pick.sc.Chunk, tokenSum, budget, maxChunks)
		cb.debugLog.Log("[MMR-DEBUG] round=%d winner id=%s file=%s rel=%.4f maxRed=%.4f mmr=%.4f cost=%d tokenSum(before)=%d/%d",
			round, pick.sc.ID, pick.sc.File, pick.rel, pick.maxRed, bestVal, cost, tokenSum, budget)
		if reason != "" {
			ct.Included, ct.ExcludeReason = false, reason
			chunkTraces = append(chunkTraces, ct)
			cb.debugLog.Log("[MMR-DEBUG] round=%d EXCLUDED id=%s file=%s — %s (cost=%d, tokenSum=%d, budget=%d)",
				round, pick.sc.ID, pick.sc.File, reason, cost, tokenSum, budget)
			continue
		}
		ct.Included = true
		if len(selected) == before { // nothing appended => it was merged into an existing chunk
			ct.Rank = idx + 1
			ct.ExcludeReason = fmt.Sprintf("merged into #%d", idx+1)
			cb.debugLog.Log("[MMR-DEBUG] round=%d merged id=%s into selection #%d: file=%s now lines %d-%d",
				round, pick.sc.ID, idx+1, selected[idx].File, selected[idx].StartLine, selected[idx].EndLine)
		}
		tokenSum += cost
		chunkTraces = append(chunkTraces, ct)
		noteSelected(pick, 1.0)
	}
	if stop == "" && len(selected) >= maxChunks {
		stop = fmt.Sprintf("hit maxChunks cap (%d)", maxChunks)
	}

	alive := 0
	for i := range cs {
		if cs[i].alive {
			alive++
		}
	}
	cb.debugLog.Log("[MMR-DEBUG] loop stopped: %s (selected=%d, tokens=%d/%d, %d candidates never evaluated)",
		stop, len(selected), tokenSum, budget, alive)

	if debugWatchSubstr != "" {
		foundInSelected, foundInTraces, stillWaiting := false, false, 0
		for _, s := range selected {
			if mmrWatchMatch(s.ID, s.Name, s.File) {
				foundInSelected = true
			}
		}
		for _, t := range chunkTraces {
			if mmrWatchMatch(t.ID, t.ID, t.File) {
				foundInTraces = true
				cb.debugLog.Log("[MMR-DEBUG] watch chunk trace: id=%s included=%t reason=%q rank=%d",
					t.ID, t.Included, t.ExcludeReason, t.Rank)
			}
		}
		for i := range cs {
			if cs[i].alive && mmrWatchMatch(cs[i].sc.ID, cs[i].sc.Name, cs[i].sc.File) {
				stillWaiting++
			}
		}
		cb.debugLog.Log("[MMR-DEBUG] SUMMARY watch=%q selected=%t everWonARound=%t stillInRemainingUnevaluated=%d",
			debugWatchSubstr, foundInSelected, foundInTraces, stillWaiting)
	}

	cb.debugLog.Log("mmrSelect: embOf hits=%d misses=%d (misses>0 with hasEmbeddings=true may indicate chunk ID / vectorMap ID mismatch)",
		embHits, embMisses)

	if trace != nil {
		trace.ChunkTraces = chunkTraces
	}
	return selected
}

// selectChunks is the no-embeddings fallback.
func (cb *ContextBuilder) selectChunks(scored []ScoredChunk, budget int) []Chunk {
	maxChunks := cb.config.RetrievalConfig.MaxContextChunks
	if maxChunks <= 0 {
		maxChunks = 30
	}
	// The old comparator mixed "by score" (different files) with "by line" (same file),
	// which is not a valid ordering (A<C<B<A cycles are possible). Score first; merging
	// handles locality afterwards.
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		if scored[i].File != scored[j].File {
			return scored[i].File < scored[j].File
		}
		return scored[i].StartLine < scored[j].StartLine
	})
	selected := make([]Chunk, 0, 24)
	tokenSum := 0
	for _, sc := range scored {
		var cost int
		var reason string
		selected, _, cost, reason = addChunk(selected, sc.Chunk, tokenSum, budget, maxChunks)
		if reason == "" {
			tokenSum += cost
		}
		// `continue`, not `break`: one oversized chunk must not end selection.
	}
	return selected
}

// kept only future refrence
//
//nolint:unused
func (cb *ContextBuilder) selectChunksLegacy(scored []ScoredChunk, budget int) []Chunk {
	selected := make([]Chunk, 0)
	tokenSum := 0
	hdr := 20
	maxChunks := cb.config.RetrievalConfig.MaxContextChunks
	if maxChunks <= 0 {
		maxChunks = 30
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].File != scored[j].File {
			if scored[i].Score != scored[j].Score {
				return scored[i].Score > scored[j].Score
			}
			return scored[i].File < scored[j].File
		}
		return scored[i].StartLine < scored[j].StartLine
	})
	for _, sc := range scored {
		if len(selected) >= maxChunks {
			break
		}
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

//nolint:unused
func mergeChunksLegacy(a, b Chunk) Chunk {
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

func mergeChunks(a, b Chunk) Chunk {
	out := a
	out.StartLine = min(a.StartLine, b.StartLine)
	out.EndLine = max(a.EndLine, b.EndLine)
	out.Importance = math.Max(a.Importance, b.Importance)
	out.Symbols = unionStrings(a.Symbols, b.Symbols)

	switch {
	case a.StartLine <= b.StartLine && b.EndLine <= a.EndLine:
		// a already contains b: nothing new.
	case b.StartLine <= a.StartLine && a.EndLine <= b.EndLine:
		out.Content, out.Tokens = b.Content, b.Tokens
	case a.EndLine < b.StartLine:
		out.Content = joinContent(a.Content, b.Content)
		out.Tokens = a.Tokens + b.Tokens
	case b.EndLine < a.StartLine:
		out.Content = joinContent(b.Content, a.Content)
		out.Tokens = a.Tokens + b.Tokens
	default: // partial overlap
		first, second := a, b
		if b.StartLine < a.StartLine {
			first, second = b, a
		}
		span := second.EndLine - second.StartLine + 1
		newLines := second.EndLine - first.EndLine
		out.Tokens = first.Tokens + second.Tokens*newLines/max(span, 1)
		out.Content = spliceOverlap(first, second)
	}
	return out
}

// addChunk merges c into any already-selected chunk it touches (not just the last
// one), otherwise appends. cost is the *marginal* token cost, so a merge only pays
// for the new lines and saves the header. reason != "" means it was not added.
func addChunk(selected []Chunk, c Chunk, tokenSum, budget, maxChunks int) (out []Chunk, idx, cost int, reason string) {
	if j := mergeTarget(selected, c); j >= 0 {
		merged := mergeChunks(selected[j], c)
		cost = merged.Tokens - selected[j].Tokens
		if tokenSum+cost > budget {
			return selected, -1, cost, "exceeds token budget"
		}
		selected[j] = merged
		return selected, j, cost, ""
	}
	cost = c.Tokens + headerOverhead
	if len(selected) >= maxChunks {
		return selected, -1, cost, "max chunks"
	}
	if tokenSum+cost > budget {
		return selected, -1, cost, "exceeds token budget"
	}
	return append(selected, c), len(selected), cost, ""
}

func mergeTarget(selected []Chunk, c Chunk) int {
	for j := range selected {
		if canMerge(selected[j], c) {
			return j
		}
	}
	return -1
}

// mmrSim is the redundancy measure between two candidates, in [0,1].
func (cb *ContextBuilder) mmrSim(a, b *mmrCand) float64 {
	if a.emb != nil && b.emb != nil {
		sim := math.Max(float64(dotProduct(a.emb, b.emb)), 0)
		if a.sc.File == b.sc.File && sim > 0.4 {
			d := a.sc.StartLine - b.sc.StartLine
			if d < 0 {
				d = -d
			}
			if d > distantLineThreshold {
				sim = math.Min(1.0, sim*simPenaltyFactor)
			}
		}
		return sim
	}
	// No embedding for at least one side: Jaccard over non-boilerplate symbols.
	if len(a.syms) == 0 || len(b.syms) == 0 {
		// Two symbol-less chunks are not "identical" (old code returned 1.0); at most
		// they are mildly redundant when they sit in the same file.
		if len(a.syms) == 0 && len(b.syms) == 0 && a.sc.File == b.sc.File {
			return 0.5
		}
		return 0.0
	}
	inter := 0
	for _, s := range b.syms {
		if slices.Contains(a.syms, s) {
			inter++
		}
	}
	union := len(a.syms) + len(b.syms) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
