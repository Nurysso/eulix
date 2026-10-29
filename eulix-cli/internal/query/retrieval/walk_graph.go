package retrieval

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// =============================================================================
// DIAGNOSTIC BUILD — Monte Carlo walk
//
// The walk logic below is BYTE-IDENTICAL to the original except for counter
// increments marked `//diag:`. Nothing that touches channel selection, stopping,
// neighbour choice, branching, or visit counting has been changed, so the
// measurements describe the behaviour you are actually getting.
//
// Set mcDiagEnabled = false to compile the counters out of the hot path.
// =============================================================================

const (
	layerSymbol = "symbol"
	layerCall   = "call"
	layerLex    = "lex"
)

const (
	prefSymbol         = 0.42
	prefCall           = 0.33
	prefLex            = 0.25
	symbolLayerMaxDF   = 128 // 512
	callLayerMaxFanout = 64
	lexLayerMaxDF      = 24
	lexLayerMaxNbrs    = 24
)

type walkEdge struct {
	to int
	w  float32
}

type walkLayer struct {
	name  string
	edges [][]walkEdge
}

// -----------------------------------------------------------------------------
// diag: accumulator
// -----------------------------------------------------------------------------

// mcDiagEnabled gates all instrumentation. The counters are observation-only:
// they never feed back into channel preference, stopping, or neighbour choice.
const mcDiagEnabled = true

type mcDiag struct {
	seedCount int
	seedFiles map[string]int
	seedFileN int
	seedDF    float64
	seedDeg   float64

	// per-channel
	chanNames   []string
	chanPref    []float64
	chanCum     []float64
	chanWeight  []float64
	chanPicked  []int
	chanDead    []int
	chanLive    []int
	chanNodes0  []int
	chanEdgeTot []int
	chanXFile   []int

	// walkers
	walkers       int
	visitTotal    int
	visitsOnSeeds int
	stopped       int
	stoppedAtSeed int
	stopProbSum   float64
	stopProbN     int
	stopProbMin   float64
	stopProbMax   float64
	stopSatCount  int // stopP was at/over the 0.75 cap
	walkPushes    int
	branches      int
	maxDepth      int
	depthHist     []int

	// movement
	fileSteps  int
	fileVisits map[string]int

	// reproducibility
	rngSeed  uint64
	rngFirst []float64
}

func newMcDiag(layers []*walkLayer) *mcDiag {
	d := &mcDiag{
		seedFiles:   map[string]int{},
		fileVisits:  map[string]int{},
		depthHist:   make([]int, mcMaxSteps+2),
		stopProbMin: 1e9,
	}
	for _, m := range layers {
		d.chanNames = append(d.chanNames, m.name)
		d.chanPref = append(d.chanPref, 0)
		d.chanCum = append(d.chanCum, 0)
		d.chanWeight = append(d.chanWeight, 0)
		d.chanPicked = append(d.chanPicked, 0)
		d.chanDead = append(d.chanDead, 0)
		d.chanLive = append(d.chanLive, 0)
		d.chanNodes0 = append(d.chanNodes0, 0)
		d.chanEdgeTot = append(d.chanEdgeTot, 0)
		d.chanXFile = append(d.chanXFile, 0)
	}
	return d
}

// chunkFileAt is defensive: the original code trusts chunk indices from the
// inverted/symbol indexes. If a stale index ever hands back an out-of-range
// index we want a log line, not a panic in the middle of a walk.
func (cb *ContextBuilder) chunkFileAt(i int) string {
	if i < 0 || i >= len(cb.chunks) {
		return "<oob>"
	}
	return cb.chunks[i].File
}

func (d *mcDiag) profileLayers(cb *ContextBuilder, layers []*walkLayer) {
	// diag: structural profile of each layer — edge totals, isolated nodes,
	// diag: and the cross-file fraction that H1 is about.
	for ci, m := range layers {
		for i := range m.edges {
			if len(m.edges[i]) == 0 {
				d.chanNodes0[ci]++
				continue
			}
			d.chanEdgeTot[ci] += len(m.edges[i])
			self := cb.chunkFileAt(i)
			for _, e := range m.edges[i] {
				if cb.chunkFileAt(e.to) != self {
					d.chanXFile[ci]++
				}
			}
		}
	}
}

func (d *mcDiag) profileSeeds(cb *ContextBuilder, seedIdxs []int, symDF []float64, deg []int) {
	d.seedCount = len(seedIdxs)
	for _, i := range seedIdxs {
		f := cb.chunkFileAt(i)
		if d.seedFiles[f] == 0 {
			d.seedFileN++
		}
		d.seedFiles[f]++
		if i >= 0 && i < len(symDF) {
			d.seedDF += symDF[i]
		}
		if i >= 0 && i < len(deg) {
			d.seedDeg += float64(deg[i])
		}
	}
	if d.seedCount > 0 {
		d.seedDF /= float64(d.seedCount)
		d.seedDeg /= float64(d.seedCount)
	}
}

func (d *mcDiag) noteStop(p float64) {
	d.stopProbSum += p
	d.stopProbN++
	if p < d.stopProbMin {
		d.stopProbMin = p
	}
	if p > d.stopProbMax {
		d.stopProbMax = p
	}
	// mcStopBase(0.30) + cap(0.45) = 0.75; anything at the cap means
	// symDF/5000 >= 0.45, i.e. symDF >= 2250, i.e. the term is saturated.
	if p >= 0.7499 {
		d.stopSatCount++
	}
}

func (d *mcDiag) report(cb *ContextBuilder, visits map[int]float64, scored int) {
	const P = "[MC-DIAG]"

	// ---- seeds
	files := make([]string, 0, len(d.seedFiles))
	for f, n := range d.seedFiles {
		files = append(files, fmt.Sprintf("%s:%d", f, n))
	}
	sort.Strings(files)
	cb.debugLog.Log("%s seeds=%d distinctSeedFiles=%d avgDF=%.0f avgDeg=%.1f", P, d.seedCount, d.seedFileN, d.seedDF, d.seedDeg)
	cb.debugLog.Log("%s seedFiles=%v", P, files)

	// ---- channels
	cb.debugLog.Log("%s channel        picked   dead  live  nodes0    edges   xFile%%   chanWeight  pref    cum",
		P)
	for i, n := range d.chanNames {
		xpc := 0.0
		if d.chanEdgeTot[i] > 0 {
			xpc = 100.0 * float64(d.chanXFile[i]) / float64(d.chanEdgeTot[i])
		}
		cb.debugLog.Log("%s   %-12s %6d %6d %5d %6d %8d %7.1f%% %9.0f %6.3f %6.3f",
			P, n, d.chanPicked[i], d.chanDead[i], d.chanLive[i], d.chanNodes0[i],
			d.chanEdgeTot[i], xpc, d.chanWeight[i], d.chanPref[i], d.chanCum[i])
	}

	// ---- walkers
	meanStop := 0.0
	if d.stopProbN > 0 {
		meanStop = d.stopProbSum / float64(d.stopProbN)
	}
	cb.debugLog.Log("%s walkers=%d walkPushes=%d branches=%d maxDepth=%d", P, d.walkers, d.walkPushes, d.branches, d.maxDepth)
	cb.debugLog.Log("%s stopped=%d (atSeed=%d, %.1f%%) meanStop=%.3f min=%.3f max=%.3f saturated=%d/%d",
		P, d.stopped, d.stoppedAtSeed, pct(d.stoppedAtSeed, d.walkers), meanStop, d.stopProbMin, d.stopProbMax, d.stopSatCount, d.stopProbN)
	cb.debugLog.Log("%s totalVisits=%d visitsOnSeeds=%d (%.1f%%) retainedHits=%d", P, d.visitTotal, d.visitsOnSeeds, pct(d.visitsOnSeeds, d.visitTotal), scored)

	// ---- concentration
	keys := make([]int, 0, len(visits))
	top := 0
	for k, f := range visits {
		keys = append(keys, k)
		top += int(f)
	}
	sort.Slice(keys, func(i, j int) bool { return visits[keys[i]] > visits[keys[j]] })
	n5 := len(keys)
	if n5 > 5 {
		n5 = 5
	}
	if n5 > 0 {
		var t5 int
		for i := 0; i < n5; i++ {
			t5 += int(visits[keys[i]])
		}
		cb.debugLog.Log("%s visits top5=%v share=%.1f%% distinctFilesVisited=%d", P, topVisitList(cb, visits, keys, 5), pct(t5, top), len(d.fileVisits))
	}

	// ---- depth
	parts := make([]string, 0, len(d.depthHist))
	for i, c := range d.depthHist {
		if c > 0 {
			parts = append(parts, fmt.Sprintf("%d:%d", i, c))
		}
	}
	cb.debugLog.Log("%s depthHist %s", P, strings.Join(parts, " "))

	// ---- cross-file movement
	cb.debugLog.Log("%s crossFileSteps=%d/%d (%.1f%%)", P, d.fileSteps, d.walkPushes, pct(d.fileSteps, d.walkPushes))

	// ---- visits by file
	ff := make([]string, 0, len(d.fileVisits))
	for f, n := range d.fileVisits {
		ff = append(ff, fmt.Sprintf("%s:%d", f, n))
	}
	sort.Strings(ff)
	if len(ff) > 12 {
		ff = ff[:12]
	}
	cb.debugLog.Log("%s visitsByFile %v", P, ff)

	// ---- reproducibility
	cb.debugLog.Log("%s rngSeed=%d firstDraws=%v", P, d.rngSeed, d.rngFirst)
}

func topVisitList(cb *ContextBuilder, visits map[int]float64, keys []int, n int) []string {
	if len(keys) > n {
		keys = keys[:n]
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s(%.0f)", cb.chunkFileAt(k), visits[k]))
	}
	return out
}

func pct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100.0 * float64(n) / float64(of)
}

// -----------------------------------------------------------------------------
// original code
// -----------------------------------------------------------------------------

func topEdges(acc map[int]float32, cap int) []walkEdge {
	if len(acc) == 0 || cap <= 0 {
		return nil
	}
	out := make([]walkEdge, 0, len(acc))
	for to, w := range acc {
		if to < 0 {
			continue
		}
		out = append(out, walkEdge{to: to, w: w})
	}
	if len(out) > cap {
		sort.Slice(out, func(i, j int) bool {
			if out[i].w != out[j].w {
				return out[i].w > out[j].w
			}
			return out[i].to < out[j].to
		})
		out = out[:cap]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].to < out[j].to })
	return out
}

func buildSymbolLayer(cb *ContextBuilder) *walkLayer {
	if cb.symLayer != nil {
		return cb.symLayer
	}
	m := &walkLayer{name: layerSymbol, edges: make([][]walkEdge, len(cb.chunks))}
	cb.symLayer = m

	acc := make(map[int]float32, mcMaxNeighbors)
	for i := range cb.chunks {
		clear(acc)
		for _, sym := range cb.chunks[i].Symbols {
			low := strings.ToLower(sym)
			postings := cb.symbolIndex[low]
			df := len(postings)
			if df == 0 || df > symbolLayerMaxDF {
				continue
			}
			w := float32(1.0 / math.Sqrt(float64(df)))
			for _, j := range postings {
				if j != i {
					acc[j] += w
				}
			}
		}
		m.edges[i] = topEdges(acc, mcMaxNeighbors)
	}
	return m
}

func buildCallLayer(cb *ContextBuilder) *walkLayer {
	if cb.callLayer != nil {
		return cb.callLayer
	}
	m := &walkLayer{name: layerCall, edges: make([][]walkEdge, len(cb.chunks))}
	cb.callLayer = m

	acc := make([]map[int]float32, len(cb.chunks))
	get := func(i int) map[int]float32 {
		if acc[i] == nil {
			acc[i] = make(map[int]float32, callLayerMaxFanout)
		}
		return acc[i]
	}
	link := func(a, b int, w float32) {
		if a == b || a < 0 || b < 0 || a >= len(cb.chunks) || b >= len(cb.chunks) {
			return
		}
		get(a)[b] += w
		get(b)[a] += w
	}
	inRange := func(i int) bool { return i >= 0 && i < len(cb.chunks) }

	for _, callers := range cb.callSites {
		if len(callers) < 2 || len(callers) > callLayerMaxFanout {
			continue
		}
		w := float32(1.0 / math.Sqrt(float64(len(callers))))
		for i := 0; i < len(callers); i++ {
			if !inRange(callers[i]) {
				continue
			}
			for j := i + 1; j < len(callers); j++ {
				if inRange(callers[j]) {
					link(callers[i], callers[j], w)
				}
			}
		}
	}

	for sym, rels := range cb.callGraph {
		if len(rels) == 0 {
			continue
		}
		callers := cb.callSites[sym]
		if len(callers) == 0 {
			callers = cb.symbolIndex[strings.ToLower(sym)]
		}
		if len(callers) == 0 || len(callers) > callLayerMaxFanout {
			continue
		}
		for _, r := range rels {
			if r.Target == "" {
				continue
			}
			callees := cb.symbolIndex[strings.ToLower(r.Target)]
			if len(callees) == 0 || len(callees) > callLayerMaxFanout {
				continue
			}
			d := r.Distance
			if d < 1 {
				d = 1
			}
			w := float32(math.Pow(0.7, float64(d-1)))
			for _, c := range callers {
				if !inRange(c) {
					continue
				}
				for _, v := range callees {
					if inRange(v) {
						link(c, v, w)
					}
				}
			}
		}
	}

	for i := range m.edges {
		m.edges[i] = topEdges(acc[i], mcMaxNeighbors)
	}
	return m
}

func buildLexLayer(cb *ContextBuilder) *walkLayer {
	if cb.lexLayer != nil {
		return cb.lexLayer
	}
	m := &walkLayer{name: layerLex, edges: make([][]walkEdge, len(cb.chunks))}
	cb.lexLayer = m

	idx := cb.invertedIdx
	if idx == nil || len(idx.Postings) == 0 {
		return m
	}
	acc := make([]map[int]float32, len(cb.chunks))
	get := func(i int) map[int]float32 {
		if acc[i] == nil {
			acc[i] = make(map[int]float32, lexLayerMaxNbrs)
		}
		return acc[i]
	}

	for _, postings := range idx.Postings {
		df := len(postings)
		if df < 2 || df > lexLayerMaxDF {
			continue
		}
		w := float32(1.0 / math.Sqrt(float64(df)))
		for i := 0; i < len(postings); i++ {
			a := postings[i].ChunkIdx
			if a < 0 || a >= len(cb.chunks) {
				continue
			}
			for j := i + 1; j < len(postings); j++ {
				b := postings[j].ChunkIdx
				if b < 0 || b >= len(cb.chunks) || a == b {
					continue
				}
				get(a)[b] += w
				get(b)[a] += w
			}
		}
	}

	for i := range m.edges {
		m.edges[i] = topEdges(acc[i], lexLayerMaxNbrs)
	}
	return m
}

func (cb *ContextBuilder) walkLayers() []*walkLayer {
	var out []*walkLayer
	if cb.hasCallGraph {
		if m := buildCallLayer(cb); m != nil && len(m.edges) > 0 {
			out = append(out, m)
		}
	}
	if m := buildLexLayer(cb); m != nil && len(m.edges) > 0 {
		out = append(out, m)
	}
	if m := buildSymbolLayer(cb); m != nil && len(m.edges) > 0 {
		out = append(out, m)
	}
	return out
}

func layerPreference(name string) float64 {
	switch name {
	case layerSymbol:
		return prefSymbol
	case layerCall:
		return prefCall
	default:
		return prefLex
	}
}

func (cb *ContextBuilder) MonteCarloWalk(seedIdxs []int, topK int) []ScoredChunk {
	return cb.monteCarloWalkWithQuery(seedIdxs, topK, "")
}

// monteCarloWalkWithQuery is monteCarloWalk plus an optional query
// string used ONLY for the RNG fingerprint log and the commented-out fix. Pass
// "" to reproduce the original behaviour exactly.
func (cb *ContextBuilder) monteCarloWalkWithQuery(seedIdxs []int, topK int, query string) []ScoredChunk {
	if len(seedIdxs) == 0 {
		return nil
	}
	layers := cb.walkLayers()
	if len(layers) == 0 {
		cb.debugLog.Log("[mc-walk] no walk layers available — walk skipped")
		return nil
	}

	pref := make([]float64, len(layers))
	var wsum float64
	for i, m := range layers {
		var chanWeight float64
		for _, es := range m.edges {
			for _, e := range es {
				chanWeight += float64(e.w)
			}
		}
		pref[i] = layerPreference(m.name) * math.Log1p(math.Sqrt(chanWeight))
		wsum += pref[i]
	}
	if wsum <= 0 {
		cb.debugLog.Log("[mc-walk] all layers have zero edge weight — walk skipped")
		return nil
	}
	cum := make([]float64, len(layers))
	var run float64
	for i := range layers {
		run += pref[i] / wsum
		cum[i] = run
	}
	cum[len(cum)-1] = 1.0

	// diag: original seed expression preserved verbatim. NOTE: it is a function of
	// diag: len(seedIdxs) and len(cb.chunks) ONLY — not the query. Two different
	// diag: queries with the same seed count on the same corpus get the identical
	// diag: xorshift64 stream. See the rngSeed/firstDraws line in the report.
	rawSeed := uint64(len(seedIdxs)*7919 + len(cb.chunks)*104729)

	// --- proposed fix (commented out; uncomment after confirming the collision) ---
	// rngSeed := rawSeed
	// if query != "" {
	//     h := uint64(1469598103934665603)
	//     for i := 0; i < len(query); i++ { h ^= uint64(query[i]); h *= 1099511628211 }
	//     rngSeed ^= h
	// }
	// ---------------------------------------------------------------------------

	rng := newMCRNG(rawSeed)

	// diag: fingerprint drawn from a CLONE so the live walk stream is untouched.
	var diag *mcDiag
	if mcDiagEnabled {
		diag = newMcDiag(layers)
		diag.rngSeed = rawSeed
		probe := newMCRNG(rawSeed)
		for i := 0; i < 4; i++ {
			diag.rngFirst = append(diag.rngFirst, probe.Float64())
		}
		diag.profileLayers(cb, layers)
		for i := range layers {
			diag.chanPref[i] = pref[i] / wsum
			diag.chanCum[i] = cum[i]
		}
		for i, m := range layers {
			var f float64
			for _, es := range m.edges {
				for _, e := range es {
					f += float64(e.w)
				}
			}
			diag.chanWeight[i] = f
		}
	}

	pickChannel := func(r float64) int {
		for i, c := range cum {
			if r <= c {
				return i
			}
		}
		return len(layers) - 1
	}
	pickNeighbour := func(es []walkEdge) int {
		var tot float32
		for _, e := range es {
			tot += e.w
		}
		if tot <= 0 {
			return es[rng.Intn(len(es))].to
		}
		r := float32(rng.Float64()) * tot
		for _, e := range es {
			r -= e.w
			if r <= 0 {
				return e.to
			}
		}
		return es[len(es)-1].to
	}

	symDF := make([]float64, len(cb.chunks))
	deg := make([]int, len(cb.chunks))
	seenSym := make(map[string]bool, 16)
	for i := range cb.chunks {
		df := 0
		for _, s := range cb.chunks[i].Symbols {
			low := strings.ToLower(s)
			if seenSym[low] {
				continue
			}
			seenSym[low] = true
			df += len(cb.symbolIndex[low])
		}
		clear(seenSym)
		symDF[i] = float64(df)
		for _, m := range layers {
			deg[i] += len(m.edges[i])
		}
	}

	if mcDiagEnabled {
		diag.profileSeeds(cb, seedIdxs, symDF, deg)
		diag.walkers = mcWalkers
	}

	seedSet := map[int]bool{}
	if mcDiagEnabled {
		for _, s := range seedIdxs {
			seedSet[s] = true
		}
	}

	visits := make(map[int]float64, topK*2)
	layerVisits := make([]int, len(layers))

	type walker struct{ idx, steps int }
	for p := 0; p < mcWalkers; p++ {
		seed := seedIdxs[rng.Intn(len(seedIdxs))]
		queue := []walker{{idx: seed, steps: 0}}
		for len(queue) > 0 {
			w := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			visits[w.idx]++

			if mcDiagEnabled {
				diag.visitTotal++
				diag.depthHist[w.steps]++
				if w.steps > diag.maxDepth {
					diag.maxDepth = w.steps
				}
				if seedSet[w.idx] {
					diag.visitsOnSeeds++
				}
				diag.fileVisits[cb.chunkFileAt(w.idx)]++
			}

			if w.steps >= mcMaxSteps {
				continue
			}
			ci := pickChannel(rng.Float64())
			m := layers[ci]
			es := m.edges[w.idx]
			if mcDiagEnabled {
				diag.chanPicked[ci]++
			}
			if len(es) == 0 {
				// diag: H2 — walker died because this node has no edges on the
				// diag: channel that was picked. There is no fallback channel.
				if mcDiagEnabled {
					diag.chanDead[ci]++
				}
				continue
			}
			layerVisits[ci]++
			if mcDiagEnabled {
				diag.chanLive[ci]++
			}

			stopP := mcStopBase + math.Min(0.45, symDF[w.idx]/5000.0)
			if mcDiagEnabled {
				diag.noteStop(stopP)
			}
			if rng.Float64() < stopP {
				if mcDiagEnabled {
					diag.stopped++
					if w.steps == 0 {
						diag.stoppedAtSeed++
					}
				}
				continue
			}
			next := pickNeighbour(es)
			nw := walker{idx: next, steps: w.steps + 1}
			if mcDiagEnabled {
				diag.walkPushes++
				if cb.chunkFileAt(next) != cb.chunkFileAt(w.idx) {
					diag.fileSteps++
				}
			}
			if deg[next] >= mcMaxNeighbors && rng.Float64() < 0.25 {
				if mcDiagEnabled {
					diag.branches++
				}
				for b := 0; b < mcHubBranch-1; b++ {
					queue = append(queue, nw)
					if mcDiagEnabled {
						diag.walkPushes++
					}
				}
			}
			queue = append(queue, nw)
		}
	}

	scored := make([]ScoredChunk, 0, len(visits))
	for idx, f := range visits {
		if f < 2 {
			continue
		}
		scored = append(scored, ScoredChunk{
			Chunk:        cb.chunks[idx],
			Score:        f * mcVisitScoreScale,
			MatchDetails: fmt.Sprintf("mc-visits: %.0f", f),
		})
	}
	slices.SortFunc(scored, byScoreDesc)
	if len(scored) > topK {
		scored = scored[:topK]
	}
	if debugLevel3 {
		shares := make([]string, len(layers))
		for i, m := range layers {
			shares[i] = fmt.Sprintf("%s=%d", m.name, layerVisits[i])
		}
		cb.debugLog.Log("[mc-walk] %d chunks visited, layer visits %v, top visits=%.0f",
			len(scored), shares, func() float64 {
				if len(scored) == 0 {
					return 0
				}
				return scored[0].Score / mcVisitScoreScale
			}())
	}
	if mcDiagEnabled && diag != nil {
		diag.report(cb, visits, len(scored))
	}
	return scored
}
