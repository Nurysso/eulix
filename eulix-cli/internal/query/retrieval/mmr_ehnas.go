//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package retrieval provides context window creation for Eulix's RAG system.

This file defines structuralMMR, a query-free, MMR-family context selector.
It is not textbook MMR: relevance is a fused multi-strategy score rather
than a query cosine, and redundancy is a weighted sum over symbol,
spatial, and structural-twin channels rather than an embedding
cosine. See the Structural MMR block comment below for the full mapping.
*/
package retrieval

import (
	"fmt"
	"math"
	"sort"
)

// Structural MMR query-free maximal marginal relevance.
//
// Embeddings gave MMR two things: a relevance term (cosine to the query) and
// a redundancy term (cosine between candidates). EHNAS keeps relevance
// the fused multi-strategy score already is a relevance estimate, and it is
// better than a cosine because it is ground truth rather than coincidence.
//
// Redundancy is where the real work is. Asking "do these two chunks overlap"
// with one cosine throws away which kind of overlap it is.
//
// The kinds matter because they are not interchangeable:
//
//	symbolOverlap  shared declared surface, idf-weighted
//	lineProximity  same file, how far apart
//	sameTwin       same structural signature, any language
//
// sameTwin is the channel that pays for this whole file. The existing
// mmrSim fallback is Jaccard over non-boilerplate symbols, which scores a
// Rust parse_config and a Go ParseConfig at ZERO redundancy, because they
// share no literal identifier. MMR then reads five ports of the same parser
// as five independent, maximally diverse facts and spends the entire context
// budget on them. Symbol sets differ per language, so redundancy has to be
// measured on the structural signature or it does not apply at all.
//
// Terms:
//
//	twin         chunk equivalence under surface-form variation,
//	             e.g. EN/FR twins or cross-language ports of the same code
//	twinClass    integer label of a group of twins
//	twin index   precomputed chunk-index → twinClass map plus class metadata

const (
	// Redundancy channel weights. They sum above 1.0 on purpose a chunk that is
	// simultaneously adjacent, symbol-overlapping and a structural twin
	// really is fully redundant, and the clamp handles the overshoot.
	wSymbolOverlap = 0.45 // shared symbol surface
	wLineProximity = 0.20 // spatial relation within a file
	wSameTwin      = 0.55 // same structural signature

	// Line proximity response by line gap, same file. Adjacent blocks merge for
	// almost no marginal token cost, so they are cheap to carry together and
	// should not be treated as redundant. Distant blocks in the same file
	// cost real budget and share a file header, so they are.
	proximityAdjacent   = 0.15 // gap <= canMerge threshold: merges, nearly free
	proximityNear       = 0.50 // gap <= distantLineThreshold
	proximityFar        = 0.85 // same file, far apart
	proximityNone       = 0.0  // different file
	proximitySameAsFile = 0.65 // same file, overlapping or interleaved

	// File saturation. Every additional chunk from an already-represented
	// file raises the redundancy floor, so a long file cannot quietly eat
	// the whole window. noveltyPack gates redundancy at the candidate
	// stage; this is the selection-stage counterpart.
	fileSatPerChunk = 0.07
	fileSatCeiling  = 0.30

	// The no-embedding relevance blend from the old path, reused so the two
	// selectors stay comparable under the same MMRDiversityFactor.
	ehnasRankShare = 0.30
)

// structCand is one candidate's working state during selection. maxRed is
// updated incrementally as chunks are picked, so the similarity matrix is
// never materialised.
type structCand struct {
	sc        ScoredChunk
	syms      []string
	symW      map[string]float64 // symbol → idf weight
	rel       float64
	maxRed    float64
	alive     bool
	twinClass int
}

// structuralMMR is the query-free context selector for the EHNAS engine.
//
// Algorithm (MMR-family, query-free):
//
//	score(i) = lambda * relevance(i) - (1 - lambda) * redundancy(i, selected)
//
// relevance comes from fillQueryFreeRelevance (fused score, log + rank blend,
// no query involved). redundancy is the max over already-selected chunks of
// chunkRedundancy, floored by fileSaturation so a single file cannot
// dominate the window. Pinned anchors are seeded before the loop and the top
// pin is always kept regardless of budget.
//
// "Structural" in the name refers to the twin-class channel that treats
// cross-language structural twins as the same chunk in different
// languages. See the file-level comment above for the full glossary.
func (cb *ContextBuilder) structuralMMR(
	candidates []ScoredChunk,
	budget int,
	anchorFiles map[string]bool,
	trace *DebugTrace,
	gate PathGate,
) []Chunk {
	if gate.active {
		candidates = gateFilterBoost(gate, candidates)
		if trace != nil {
			trace.Warnings = append(trace.Warnings,
				fmt.Sprintf("gate filtered to %d candidates", len(candidates)))
		}
	}
	if len(candidates) == 0 {
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
	ti := cb.twinIndexOrBuild()
	cb.debugLog.Log("[structuralMMR] lambda=%.2f budget=%d maxChunks=%d candidates=%d twins=%d classes=%d",
		lambda, budget, maxChunks, len(candidates), len(ti.twin), len(ti.twin))

	//               pinned first, before anything is normalised
	// Pinned chunks score 5000+. Left in the pool they squash every other
	// candidate's relevance toward zero and MMR degenerates into "pick
	// anchors, then whatever survives".
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
		pinned = pinned[:pinnedAnchorTopN]
	}

	//               candidate state
	cs := make([]structCand, len(pool))
	for i, c := range pool {
		syms := cb.nonBoilerplate(c.Symbols)
		cs[i] = structCand{
			sc:        c,
			syms:      syms,
			symW:      cb.symWeights(syms),
			alive:     true,
			twinClass: -1,
		}
		if ci := cb.chunkIndexByID(c.ID); ci >= 0 && ci < len(ti.twinClass) {
			cs[i].twinClass = ti.twinClass[ci]
		}
	}

	// Deterministic order. Equal scores are common; map-order shuffles are
	// not welcome in a retrieval system.
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].sc.Score != cs[j].sc.Score {
			return cs[i].sc.Score > cs[j].sc.Score
		}
		return cs[i].sc.ID < cs[j].sc.ID
	})

	fillQueryFreeRelevance(cs, anchorFiles)

	//               selection
	selected := make([]Chunk, 0, maxChunks)
	chunkTraces := make([]ChunkTrace, 0, len(cs)+len(pinned))
	tokenSum := 0

	// fold a newly selected chunk into every live candidate's maxRed
	noteSelected := func(p *structCand, damp float64) {
		for i := range cs {
			c := &cs[i]
			if !c.alive {
				continue
			}
			if r := chunkRedundancy(c, p) * damp; r > c.maxRed {
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
		// The top pin is always kept; the rest must fit.
		if k > 0 && tokenSum+cost > budget {
			ct.Included, ct.ExcludeReason = false, "pinned but exceeds token budget"
			chunkTraces = append(chunkTraces, ct)
			continue
		}
		selected = append(selected, p.Chunk)
		tokenSum += cost
		chunkTraces = append(chunkTraces, ct)
		pc := structCand{
			sc:   p,
			symW: cb.symWeights(cb.nonBoilerplate(p.Symbols)),
		}
		// A pinned chunk must carry its twin class too, or it fails to
		// exclude its cross-language twin and the exclusion is one-sided.
		if pi := cb.chunkIndexByID(p.ID); pi >= 0 && pi < len(ti.twinClass) {
			pc.twinClass = ti.twinClass[pi]
		}
		noteSelected(&pc, pinnedRedundancyDamp)
	}

	// how many chunks each file has already contributed
	fileCount := make(map[string]int, maxChunks)

	// main MMR loop
	stop := ""
	for len(selected) < maxChunks {
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
			// File saturation acts as a redundancy floor, not an extra term:
			// it must be able to outrank a high-relevance chunk from a file
			// that already has five blocks in the window.
			red := c.maxRed
			if sat := fileSaturation(fileCount[c.sc.File]); sat > red {
				red = sat
			}
			v := lambda*c.rel - (1.0-lambda)*red
			if v > bestVal {
				best, bestVal = i, v
			}
		}
		if best < 0 {
			stop = "candidates exhausted"
			break
		}
		// Stop when nothing left beats the configured floor (mmr_stop_floor);
		// at 0, every remaining candidate costs more than the diversity it buys.
		if bestVal <= cb.config.RetrievalConfig.Ehnas.MMRStopFloor {
			stop = fmt.Sprintf("no candidate above mmr_stop_floor (best=%.4f)", bestVal)
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
		if reason != "" {
			ct.Included, ct.ExcludeReason = false, reason
			chunkTraces = append(chunkTraces, ct)
			continue
		}
		ct.Included = true
		if len(selected) == before { // merged into an existing block
			ct.Rank = idx + 1
			ct.ExcludeReason = fmt.Sprintf("merged into #%d", idx+1)
		} else {
			fileCount[pick.sc.File]++
		}
		tokenSum += cost
		chunkTraces = append(chunkTraces, ct)
		noteSelected(pick, 1.0)
	}
	if stop == "" && len(selected) >= maxChunks {
		stop = fmt.Sprintf("hit maxChunks cap (%d)", maxChunks)
	}

	//               diagnostics
	cb.redundancyStats(cs)
	if debugLevel3 {
		excluded := 0
		for _, t := range chunkTraces {
			if !t.Included {
				excluded++
			}
		}
		cb.debugLog.Log("[structuralMMR] selected=%d tokens=%d/%d traces=%d excluded=%d stop=%q",
			len(selected), tokenSum, budget, len(chunkTraces), excluded, stop)
	}
	if trace != nil {
		trace.ChunkTraces = chunkTraces
		trace.Warnings = append(trace.Warnings,
			fmt.Sprintf("structuralMMR: %d chunks, %d tokens, %s", len(selected), tokenSum, stop))
	}
	return selected
}

// ---------------------------------------------------------------------------
// Relevance
// ---------------------------------------------------------------------------

// fillQueryFreeRelevance normalizes fused scores into [0,1] without letting
// one outlier destroy the scale. This is the "query-free relevance" term of
// structuralMMR: there is no query to take a cosine against, so the fused
// multi-strategy score stands in for one.
//
// Min-max alone is wrong here and the failure is quiet. EHNAS's score
// distribution is heavy-tailed: anchor hits land around 250, twin-bridge
// hits inherit from them, keyword hits sit near 20. Min-max maps the top
// candidate to 1.0 and squashes everything else into 0.05–0.2, after which
// the (1-lambda)*maxRed term dominates for effectively every non-anchor and
// MMR quietly stops being relevance-weighted at all, it becomes a pure
// diversity picker wearing an MMR costume.
//
// So: log compression for magnitude, blended with rank for spread. The
// function deliberately does not consult the twin index, the twin-class
// channel belongs to redundancy, not relevance. cs is expected to already be
// sorted by score descending.
func fillQueryFreeRelevance(cs []structCand, anchorFiles map[string]bool) {
	if len(cs) == 0 {
		return
	}
	maxLog := 0.0
	for i := range cs {
		s := cs[i].sc.Score
		if s <= 0 {
			s = 0
		}
		if l := math.Log1p(s); l > maxLog {
			maxLog = l
		}
	}
	if maxLog <= 0 {
		maxLog = 1
	}
	n := float64(len(cs))
	for i := range cs {
		s := cs[i].sc.Score
		if s < 0 {
			s = 0
		}
		byMagnitude := math.Log1p(s) / maxLog
		byRank := 1.0 - float64(i)/n // cs is already sorted by score
		rel := (1.0-ehnasRankShare)*byMagnitude + ehnasRankShare*byRank
		if anchorFiles[cs[i].sc.File] {
			rel += anchorFileBonus
		}
		if rel > 1.0 {
			rel = 1.0
		}
		cs[i].rel = rel
	}
}

// ---------------------------------------------------------------------------
// Redundancy channels
// ---------------------------------------------------------------------------

// chunkRedundancy returns the combined redundancy of a and b in [0,1].
//
// Channels are independent rather than averaged. Two chunks that are the same
// structural state in different languages score high on sameTwin and zero
// on symbolOverlap, which is the entire point; averaging would dilute exactly the
// signal the sameTwin term exists to provide.
func chunkRedundancy(a, b *structCand) float64 {
	r := wSymbolOverlap*symbolOverlap(a, b) +
		wLineProximity*lineProximity(a, b) +
		wSameTwin*sameTwin(a, b)
	if r > 1.0 {
		return 1.0
	}
	return r
}

// symbolOverlap: weighted Jaccard over non-boilerplate symbols.
//
// Weighted rather than raw: a shared rare identifier means the two chunks
// are doing the same specific thing, a shared common one means little.
// unweighted Jaccard scores `error` and `parse_config` as equally redundant.
func symbolOverlap(a, b *structCand) float64 {
	if len(a.symW) == 0 || len(b.symW) == 0 {
		return 0.0
	}
	inter, union := 0.0, 0.0
	small, large := a.symW, b.symW
	if len(large) < len(small) {
		small, large = large, small
	}
	for s, wa := range small {
		wb, ok := large[s]
		if !ok {
			union += wa
			continue
		}
		if wa < wb {
			inter += wa
			union += wb
		} else {
			inter += wb
			union += wa
		}
	}
	// Symbols present only in the larger set still count toward the union.
	for s, wb := range large {
		if _, ok := small[s]; !ok {
			union += wb
		}
	}
	if union <= 0 {
		return 0.0
	}
	return inter / union
}

// lineProximity: spatial relation within a file.
//
// The response is inverted relative to intuition: adjacent blocks are cheap
// to carry together (addChunk merges them, so the marginal token cost is a
// few lines and no new file header) and should not be treated as redundant.
// Two distant blocks in the same file cost a full header each and share no
// lines, so they are the redundant pair.
func lineProximity(a, b *structCand) float64 {
	if a.sc.File != b.sc.File {
		return proximityNone
	}
	gap := 0
	if a.sc.EndLine < b.sc.StartLine {
		gap = b.sc.StartLine - a.sc.EndLine
	} else if b.sc.EndLine < a.sc.StartLine {
		gap = a.sc.StartLine - b.sc.EndLine
	}
	switch {
	case gap == 0:
		return proximitySameAsFile // overlapping or interleaved
	case canMerge(a.sc.Chunk, b.sc.Chunk):
		// Ask addChunk's own predicate rather than hardcoding its line
		// threshold here. If that constant moves and this does not, the
		// lineProximity term silently starts scoring free merges as redundant.
		return proximityAdjacent
	case gap <= distantLineThreshold:
		return proximityNear
	default:
		return proximityFar
	}
}

// sameTwin: same structural signature.
//
// Two chunks whose declared surface hashes to the same signature are the same
// code in different languages. This channel is the whole reason
// the selector exists: a language-agnostic measure of "these are the same
// thing", which a symbol-set measure structurally cannot express.
func sameTwin(a, b *structCand) float64 {
	if a.twinClass < 0 || a.twinClass != b.twinClass {
		return 0.0
	}
	return 1.0
}

// fileSaturation returns the redundancy floor contributed by a file that has
// already placed n chunks in the window. It is a floor rather than an added
// term so it can outrank a genuinely high-relevance chunk from a file that is
// already well represented; see the main loop in structuralMMR.
func fileSaturation(n int) float64 {
	if n <= 0 {
		return 0.0
	}
	s := fileSatPerChunk * float64(n)
	if s > fileSatCeiling {
		return fileSatCeiling
	}
	return s
}

// symWeights builds the idf-weighted symbol set for a candidate, computed
// once per candidate rather than per pair. Without this, chunkRedundancy
// would rebuild two maps on every call and the selection loop would be
// quadratic in a very expensive way.
func (cb *ContextBuilder) symWeights(syms []string) map[string]float64 {
	if len(syms) == 0 {
		return nil
	}
	out := make(map[string]float64, len(syms))
	for _, s := range syms {
		if _, ok := out[s]; ok {
			continue
		}
		df := float64(len(cb.symbolIndex[s]))
		// log1p(1/df): a symbol in every chunk carries no information, one
		// in a handful carries a great deal.
		out[s] = math.Log1p(1.0 / df)
	}
	return out
}

// redundancyStats logs how much of the selection was driven by which channel.
// If no candidate carries a twin class the sameTwin channel is inert and the
// selector has quietly degraded to a symbol Jaccard with a spatial term.
func (cb *ContextBuilder) redundancyStats(cs []structCand) {
	if !debugLevel3 || len(cs) == 0 {
		return
	}
	twined := 0
	for i := range cs {
		if cs[i].twinClass >= 0 {
			twined++
		}
	}
	cb.debugLog.Log("[structuralMMR] twin coverage: %d/%d candidates in a structural class "+
		"(if 0, the sameTwin channel is inert)", twined, len(cs))
}
