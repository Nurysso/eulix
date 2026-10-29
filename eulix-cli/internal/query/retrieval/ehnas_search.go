package retrieval

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// EhnasSearch executes up to six retrieval strategies (kb_exact, exact,
// partial, keyword, semantic, monte_carlo), merging duplicate chunk scores with
// multi-strategy boosts and sorting by match type and score.
//
// Physics-inspired extensions over the classic pipeline:
//   - Monte Carlo particle transport: seeded random walks over the symbol
//     co-occurrence graph score chunks by integrated flux (O(N·k) not O(V²)).
//   - Wigner-Eckart/Clebsch-Gordan coupling: semantic scores factorize into
//     radial strength (embedding norms) and angular geometry (cosine coupling).
//   - Isospin alignment: per-language centroid rotation moves the query into a
//     language-agnostic nucleon-doublet space before vector search.
//   - Fermi-Dirac packing: the context budget is the chemical potential μ; a
//     temperature schedule gates redundant blocks while bosonic connector
//     chunks (high-df symbols) condense without penalty.
//
// If trace is non-nil, it records performance and score metrics per strategy.
func (cb *ContextBuilder) EhnasSearch(
	query string,
	topK int,
	intent QueryIntent,
	weights map[string]float64,
	trace *DebugTrace,
) []ScoredChunk {
	tTotal := time.Now()
	cb.debugLog.Log("=== EhnasSearch START: topK=%d, intent=%v ===", topK, intent.Type)

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
		// st := StrategyTrace{Name: name, Found: len(results), Duration: time.Since(t0)}
		st := StrategyTrace{Name: name, Found: len(results), Duration: time.Since(t0)}
		// diag: rawTop is the strategy's own top score BEFORE the merge boost and
		// diag: before max() against whatever was already in `all`. The existing
		// diag: st.TopScore is post-merge, so an overlapping chunk from an earlier
		// diag: strategy (notably the 5000+ anchor_pin) silently becomes this
		// diag: strategy's reported top.
		var rawTop float64
		for _, m := range results {
			if m.Score > rawTop {
				rawTop = m.Score
			}
		}
		cb.debugLog.Log("[Timing] Strategy %q took %v (Found: %d hits)", name, st.Duration, st.Found)

		if len(results) > 0 {
			topFiles := make([]string, 0, 5)
			scores := make([]float64, 0, len(results))
			var preTop float64
			for i, m := range results {
				if m.Score > preTop {
					preTop = m.Score
				}
				if i < 5 {
					topFiles = append(topFiles, fmt.Sprintf("%s (%.1f)", m.File, m.Score))
				}
				m.MatchType = name
				if ex, ok := all[m.ID]; ok {
					// Pinned chunks already carry a 5000+ floor; folding MC's
					// (or anyone's) score into them via math.Max+boost only
					// makes the strategy look like it produced a 5k hit. Skip.
					if ex.Pinned {
						continue
					}
					m.Score = math.Max(ex.Score, m.Score) + multiBoost
					m.MatchDetails = ex.MatchDetails + "; " + m.MatchDetails
					m.IsExact = ex.IsExact || name == "exact" || name == "kb_exact"
				} else {
					m.IsExact = name == "exact" || name == "kb_exact"
				}
				all[m.ID] = m
				scores = append(scores, m.Score) // post-merge, consistent with top
				if m.Score > st.TopScore {
					st.TopScore = m.Score
				}
			}
			if len(scores) > 0 {
				st.AvgScore = sum(scores) / float64(len(scores))
			} else {
				st.AvgScore = 0
			}

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

			cb.debugLog.Log("[Strategy %q] Found %d hits, rawTop=%.1f, mergedTop=%.1f, avg=%.1f, samples: %v",
				name, len(results), rawTop, st.TopScore, st.AvgScore, topFiles)
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

		// Pseudo-relevance feedback. The top hits vote on what else belongs to
		// the answer; the corpus promotes its own vocabulary into a second
		// pass. This is the embedding-free paraphrase channel.
		if fbTerms := prfExpand(cb, res, kwTopK); len(fbTerms) > 0 {
			if fb := cb.invertedKeywordSearchBM25(strings.Join(fbTerms, " "), kwTopK, hc); len(fb) > 0 {
				cb.debugLog.Log("[Step 2.5] PRF expanded query with %d terms: %v",
					len(fbTerms), fbTerms)
				res = prfMerge(res, fb)
			}
		}

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

	// skipSemantic := intent.Type == IntentCallers || intent.Type == IntentCallees
	// if cb.hasEmbeddings && qEmb != nil && !skipSemantic {
	// 	semTopK := int(float64(topK) * (0.2 + 0.5*weights["semantic"]))
	// 	minimumSimimilarity := float64(0.15)
	// 	if cb.config.RetrievalConfig.SemanticMinSimilarity > 0 {
	// 		minimumSimimilarity = cb.config.RetrievalConfig.SemanticMinSimilarity
	// 	}
	// 	// Isospin alignment: rotate the query into the language-agnostic
	// 	// representation space before emission, the same way isospin rotation
	// 	// treats proton/neutron as one nucleon doublet. Removes the per-language
	// 	// centroid offset so Rust/C/Go/Python embeddings share one doublet.
	// 	qAligned, isoShift := isospinAlignQuery(qEmb, cb)
	// 	cb.debugLog.Log("[Step 2.6] Executing semantic strategy (semTopK=%d, minSim=%.2f, isospinShift=%.4f)",
	// 		semTopK, minimumSimimilarity, isoShift)
	// 	run("semantic", func() []ScoredChunk {
	// 		var raw []ScoredChunk
	// 		if cb.ivfIndex != nil {
	// 			raw = cb.vectorSearchIVF(qAligned, semTopK, minimumSimimilarity)
	// 		} else {
	// 			raw = cb.vectorSearch(qAligned, semTopK, minimumSimimilarity)
	// 		}
	// 		// Wigner-Eckart factorization: score = radial strength × angular
	// 		// coupling. Radial part = |q|·|e| (semantic magnitude), geometric
	// 		// part = cos²θ acts as the Clebsch-Gordan coupling coefficient
	// 		// between query intent and chunk structure.
	// 		qNorm := vecNorm32(qAligned)
	// 		for i := range raw {
	// 			embIdx := chunkIndexByID(&cb.chunks, raw[i].ID)
	// 			var cg float64
	// 			if embIdx >= 0 && embIdx < len(cb.embeddings) && qNorm > 0 {
	// 				e := cb.embeddings[embIdx]
	// 				eNorm := vecNorm32(e)
	// 				if eNorm > 0 {
	// 					cos := float64(dotProduct(qAligned, e)) / (qNorm * eNorm)
	// 					if cos < 0 {
	// 						cos = 0
	// 					}
	// 					cg = cos * cos // CG-like coupling coefficient ∈ [0,1]
	// 				}
	// 			}
	// 			raw[i].Score *= 20.0 * (0.5 + 0.5*cg)
	// 			if debugLevel3 && i < 3 {
	// 				cb.debugLog.Log("[semantic Wigner-Eckart] id=%s cg=%.3f final=%.1f",
	// 					raw[i].ID, cg, raw[i].Score)
	// 			}
	// 		}
	// 		return raw
	// 	}, 1.5)
	// }
	// ---------------------------------------------------------------------
	// [Step 2.6] Isospin bridge — cross-projection resonance.
	//
	// This is the channel that used to be vectorSearch. For every chunk the
	// lexical and graph strategies already love, it emits the chunks that are
	// the same structural state in a *different* language: same declared
	// symbol set, no shared string required. It is the only route by which a
	// query reaches code it shares no token with, and it costs one hash
	// lookup per source chunk instead of a scan over the corpus.
	//
	// Deliberately not gated on intent.Callers/Callees the way the old
	// semantic block was — a cross-language twin of a caller set is still
	// useful, and transport handles the precision cases.
	// ---------------------------------------------------------------------
	if lat := cb.isospinLatticeOrBuild(); len(lat.twin) > 0 {
		bridgeTopK := int(float64(topK) * (0.3 + 0.4*weights["isospin"]))
		if bridgeTopK < 8 {
			bridgeTopK = 8
		}
		cb.debugLog.Log("[Step 2.6] Executing isospin bridge strategy (bridgeTopK=%d, %d signatures, %d languages)",
			bridgeTopK, len(lat.twin), len(lat.density))
		run("isospin", func() []ScoredChunk {
			return cb.isospinBridgeSeek(all, bridgeTopK)
		}, 1.2)
	}
	// ---------------------------------------------------------------------
	// [Step 3.5] Monte Carlo particle transport.
	// The symbol co-occurrence graph is the transport medium; anchor/grep hits
	// are neutron sources. Particles scatter along edges, absorb at boilerplate
	// boundaries, and fission at hub chunks. Chunks score by integrated flux.
	// ---------------------------------------------------------------------
	tMC := time.Now()
	{
		seeds := mcSeedIndices(cb, all)
		if len(seeds) > 0 {
			cb.debugLog.Log("[Step 3.5] Monte Carlo transport: %d seeds emitted into medium", len(seeds))
			run("monte_carlo", func() []ScoredChunk {
				return cb.monteCarloTransportWithQuery(seeds, topK, query)
			}, 1.0)
		} else {
			cb.debugLog.Log("[Step 3.5] Monte Carlo transport skipped: no seeds (no anchors/grep hits)")
		}
		cb.debugLog.Log("[Timing] Monte Carlo transport took %v", time.Since(tMC))
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
	// [Step 3.9] Isospin score normalization. Divide out each language's
	// relative lexical density so a chunk in a dense projection (C: short
	// symbols, high df) cannot out-rank one in a sparse projection purely for
	// being dense. This is the "remove the projection offset" step the old
	// code did in 768 dimensions, done in score space.
	preBoost := len(result)
	cb.debugLog.Log("[Step 3.9] Isospin score normalization over %d candidates, %d languages",
		preBoost, len(cb.isospinLatticeOrBuild().relDens))
	result = isoNormalizeScores(result, cb)

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

	// ---------------------------------------------------------------------
	// [Step 6.2] Fermi-Dirac context packing.
	// The context budget is the chemical potential μ. Candidate blocks carry an
	// energy ε = -ln(novelty): redundant fermionic blocks (overlapping symbols
	// with already-selected blocks) have high ε and are Pauli-excluded as the
	// window fills; bosonic connector blocks (high-df utility symbols) condense
	// freely. Temperature anneals from hot exploration to degenerate packing.
	// ---------------------------------------------------------------------
	tFermi := time.Now()
	result = fermiDiracPack(result, topK, cb)
	cb.debugLog.Log("[Timing] Fermi-Dirac packing took %v", time.Since(tFermi))

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
	{
		getCount := func(name string) int {
			if r, ok := strategyResults[name]; ok {
				return r.count
			}
			return 0
		}
		cb.debugLog.Log(
			"[floor] strategy tops: anchors=%d grep=%d exact=%d partial=%d keyword=%d mc=%d",
			getCount("anchors"),
			getCount("grep"),
			getCount("exact"),
			getCount("partial"),
			getCount("keyword"),
			getCount("monte_carlo"), // run() registered this as "monte_carlo", not "mc"
		)
	}

	cb.debugLog.Log("=== EhnasSearch COMPLETE: Total Time %v, Returning %d chunks ===", time.Since(tTotal), len(result))
	return result
}

// ---------------------------------------------------------------------------
// Monte Carlo particle transport
// ---------------------------------------------------------------------------

// mcRNG is a deterministic xorshift64* PRNG so transport is reproducible
// across runs without pulling in math/rand.
type mcRNG struct{ s uint64 }

func newMCRNG(seed uint64) *mcRNG {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15
	}
	return &mcRNG{s: seed}
}

func (r *mcRNG) next() uint64 {
	r.s ^= r.s << 13
	r.s ^= r.s >> 7
	r.s ^= r.s << 17
	return r.s
}

func (r *mcRNG) Float64() float64 { return float64(r.next()>>11) * (1.0 / (1 << 53)) }

func (r *mcRNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

const (
	mcParticles      = 256 // emitted "neutrons" per search
	mcMaxSteps       = 20  // max mean-free-path length before forced absorption
	mcAbsorbBase     = 0.30
	mcHubFission     = 3  // branch factor at critical hub chunks
	mcMaxNeighbors   = 96 // 48 // cap adjacency fanout per chunk
	mcFluxScoreScale = 12.0
)

// chunkLanguage derives a coarse language group from the file extension,
// the isospin quantum number of the chunk in embedding space.
func chunkLanguage(file string) string {
	dot := strings.LastIndex(file, ".")
	if dot < 0 || dot == len(file)-1 {
		return "none"
	}
	switch strings.ToLower(file[dot+1:]) {
	case "c", "h", "cc", "cpp", "cxx", "hpp", "hh":
		return "cpp"
	case "rs":
		return "rust"
	case "go":
		return "go"
	case "py":
		return "python"
	case "ts", "tsx", "js", "jsx":
		return "js"
	case "java", "kt":
		return "jvm"
	default:
		return "other"
	}
}

// chunkIndexByID resolves a chunk ID to its slice index via the prebuilt map.
// This was a linear scan called from inside a per-result scoring loop, which
// made the old semantic path O(chunks × results) per query.
func (cb *ContextBuilder) chunkIndexByID(id string) int {
	if cb.chunkIdx == nil {
		return -1
	}
	return cb.chunkIdx[id]
}

// buildSymbolAdjacency constructs the transport medium: chunk i is adjacent to
// chunk j iff they share a declared symbol (call edge, trait impl, import hub).
// func buildSymbolAdjacency(cb *ContextBuilder) [][]int {
// 	adj := make([][]int, len(cb.chunks))
// 	seen := make([]map[int]bool, len(cb.chunks))
// 	for i := range cb.chunks {
// 		seen[i] = make(map[int]bool, mcMaxNeighbors)
// 	}
// 	for i := range cb.chunks {
// 		for _, sym := range cb.chunks[i].Symbols {
// 			idxs, ok := cb.symbolIndex[strings.ToLower(sym)]
// 			if !ok {
// 				continue
// 			}
// 			for _, j := range idxs {
// 				if j == i || seen[i][j] {
// 					continue
// 				}
// 				seen[i][j] = true
// 				if len(adj[i]) < mcMaxNeighbors {
// 					adj[i] = append(adj[i], j)
// 				}
// 			}
// 		}
// 	}
// 	return adj
// }

// mcSeedIndices picks emission sites: high-scoring chunks already found by
// anchors/grep/exact act as neutron sources at the medium boundary.
// func mcSeedIndices(cb *ContextBuilder, all map[string]ScoredChunk) []int {
// 	type seed struct {
// 		idx   int
// 		score float64
// 	}
// 	var seeds []seed
// 	for _, sc := range all {
// 		if sc.Score < 60.0 {
// 			continue
// 		}
// 		if i := cb.chunkIndexByID(sc.ID); i >= 0 {
// 			seeds = append(seeds, seed{i, sc.Score})
// 		}
// 	}
// 	sort.Slice(seeds, func(a, b int) bool { return seeds[a].score > seeds[b].score })
// 	if len(seeds) > 16 {
// 		seeds = seeds[:16]
// 	}
// 	out := make([]int, 0, len(seeds))
// 	for _, s := range seeds {
// 		out = append(out, s.idx)
// 	}
// 	return out
// }

// monteCarloContextWalk emits mcParticles random walkers from the seed chunks.
// At each step a walker scatters to a random neighbor with probability
// (1 - absorb), absorbs (dies) at boilerplate boundaries with probability
// proportional to how common the chunk's symbols are, and fissions (spawns
// mcHubFission daughters) when it lands on a hub chunk. Chunks accumulate
// flux; flux × mcFluxScoreScale is the relevance score. Hidden long-range
// cross-file dependencies surface because walkers bridge multiple hops that
// no single-hop strategy connects.
// func (cb *ContextBuilder) monteCarloContextWalk(seedIdxs []int, topK int) []ScoredChunk {
// 	if len(seedIdxs) == 0 {
// 		return nil
// 	}
// 	adj := buildSymbolAdjacency(cb)
// 	rng := newMCRNG(uint64(len(seedIdxs)*7919 + len(cb.chunks)*104729))
// 	flux := make(map[int]float64, topK*2)

// 	// symbol document frequency, reused as absorption cross-section:
// 	// chunks built from ubiquitous symbols are boilerplate boundaries.
// 	symDF := func(idx int) float64 {
// 		df := 0
// 		for _, sym := range cb.chunks[idx].Symbols {
// 			df += len(cb.symbolIndex[strings.ToLower(sym)])
// 		}
// 		return float64(df)
// 	}

// 	type walker struct{ idx, steps int }
// 	for p := 0; p < mcParticles; p++ {
// 		seed := seedIdxs[rng.Intn(len(seedIdxs))]
// 		queue := []walker{{idx: seed, steps: 0}}
// 		for len(queue) > 0 {
// 			w := queue[len(queue)-1]
// 			queue = queue[:len(queue)-1]
// 			flux[w.idx]++
// 			if w.steps >= mcMaxSteps {
// 				continue
// 			}
// 			neighbors := adj[w.idx]
// 			if len(neighbors) == 0 {
// 				continue
// 			}
// 			// Absorption: cross-section rises with symbol commonality.
// 			absorb := mcAbsorbBase + math.Min(0.45, symDF(w.idx)/5000.0)
// 			if rng.Float64() < absorb {
// 				continue // absorbed at boundary
// 			}
// 			next := neighbors[rng.Intn(len(neighbors))]
// 			nw := walker{idx: next, steps: w.steps + 1}
// 			// Fission: hub chunks (high fanout) are critical interfaces.
// 			if len(adj[next]) >= mcMaxNeighbors/2 && rng.Float64() < 0.25 {
// 				for b := 0; b < mcHubFission-1; b++ {
// 					queue = append(queue, nw)
// 				}
// 			}
// 			queue = append(queue, nw)
// 		}
// 	}

// 	scored := make([]ScoredChunk, 0, len(flux))
// 	for idx, f := range flux {
// 		if f < 2 { // single-visit noise floor
// 			continue
// 		}
// 		scored = append(scored, ScoredChunk{
// 			Chunk:        cb.chunks[idx],
// 			Score:        f * mcFluxScoreScale,
// 			MatchDetails: fmt.Sprintf("mc-flux: %.0f particles", f),
// 		})
// 	}
// 	slices.SortFunc(scored, byScoreDesc)
// 	if len(scored) > topK {
// 		scored = scored[:topK]
// 	}
// 	if debugLevel3 {
// 		cb.debugLog.Log("[monteCarloContextWalk] COMPLETE: %d chunks received flux, top flux=%.0f",
// 			len(scored), func() float64 {
// 				if len(scored) == 0 {
// 					return 0
// 				}
// 				return scored[0].Score / mcFluxScoreScale
// 			}())
// 	}
// 	return scored
// }

// ---------------------------------------------------------------------------
// Fermi-Dirac context packing
// ---------------------------------------------------------------------------

// fermiDiracPack gates the sorted candidate list through the Fermi-Dirac
// occupancy function. Each block's energy ε = -ln(novelty), where novelty is
// the fraction of its symbols not already represented in the selected window.
// Bosonic symbols (document frequency above bosonDFThreshold) behave like
// condensate connectors — repeated error checks/imports — and never raise ε.
// Temperature anneals linearly from kT0 to kTMin over the packing progress,
// so early picks explore broadly and late picks form a degenerate core.
func fermiDiracPack(cands []ScoredChunk, topK int, cb *ContextBuilder) []ScoredChunk {
	if len(cands) == 0 {
		return cands
	}
	const (
		bosonDFThreshold = 200  // df above which a symbol is a bosonic connector
		kT0              = 0.50 // hot: exploration
		kTMin            = 0.05 // degenerate: sharp Pauli cutoff
		fermiGain        = 0.85 // max score amplification for novel blocks
	)
	mu := math.Ln2 // chemical potential: half-filled baseline

	// Pre-compute df for boson classification.
	df := func(sym string) int { return len(cb.symbolIndex[strings.ToLower(sym)]) }

	selectedSymCounts := make(map[string]int, topK*8)
	limit := len(cands)
	if limit > topK*3 {
		limit = topK * 3
	}
	packed := 0
	for i := 0; i < len(cands) && packed < limit; i++ {
		sc := cands[i]
		if sc.Pinned || sc.IsExact {
			packed++
			continue // pinned/exact blocks bypass the exclusion principle
		}
		progress := float64(packed) / float64(topK)
		if progress > 1 {
			progress = 1
		}
		kT := kT0 + (kTMin-kT0)*progress // annealing schedule

		syms := sc.Symbols
		if len(syms) == 0 {
			packed++
			continue
		}
		redundant, total := 0, 0
		for _, sym := range syms {
			low := strings.ToLower(sym)
			if df(low) > bosonDFThreshold {
				continue // bosonic: condenses, no exclusion cost
			}
			total++
			if selectedSymCounts[low] > 0 {
				redundant++
			}
		}
		var occupancy float64
		if total == 0 {
			occupancy = 1.0 // pure bosonic chunk
		} else {
			novelty := 1.0 - float64(redundant)/float64(total)
			if novelty < 1e-6 {
				novelty = 1e-6
			}
			epsilon := -math.Log(novelty) // redundancy energy
			occupancy = 1.0 / (math.Exp((epsilon-mu)/kT) + 1.0)
		}
		// Rescale: occupancy 1 → +fermiGain boost, occupancy 0 → deep cut,
		// but never zero a demotion-surviving exact-ish candidate entirely.
		// around line 810 in the original file:
		if math.IsNaN(occupancy) || math.IsInf(occupancy, 0) {
			// novelty was 0 or negative; this block has no discriminative signal.
			// Keep the score rather than propagating NaN.
			occupancy = 1.0
		}
		cands[i].Score *= (1.0 - fermiGain) + fermiGain*occupancy
		cands[i].MatchDetails = fmt.Sprintf("%s; fermi: n=%.2f", cands[i].MatchDetails, occupancy)

		for _, sym := range syms {
			selectedSymCounts[strings.ToLower(sym)]++
		}
		packed++
	}

	// Re-sort after occupancy weighting (stable keeps exact/pinned on top).
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].IsExact != cands[j].IsExact {
			return cands[i].IsExact
		}
		return cands[i].Score > cands[j].Score
	})
	if debugLevel3 {
		cb.debugLog.Log("[fermiDiracPack] Annealed kT %.2f→%.2f, μ=%.2f over %d slots",
			kT0, kTMin, mu, packed)
	}
	return cands
}
