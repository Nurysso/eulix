package retrieval

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
)

// Isospin lattice: structural twins without a vector space.
//
// In the strong interaction a proton and a neutron are one nucleon in two
// isospin projections: the force cannot tell them apart, only electromagnetic
// charge can. Code chunks are the same. A Rust `chunk_writer` and a C
// `chunk_writer` that declare the same symbol set are two projections of one
// structural state, and a query landing on one should resonate with the other
// even when the two share no string whatsoever.
//
// The old code removed the projection offset with a 768-dimensional matrix
// multiply, per query, against a corpus that never moved. That work now
// happens once, at index time, in two places:
//
//	density — each language's expected lexical score density. Dividing a raw
//	          score by it is the "remove the projection offset" step, done in
//	          score space instead of vector space.
//	twin    — a structural signature index, so cross-language resonance
//	          becomes a hash lookup rather than an approximate vector search.
//
// Glossary (physics term → CS meaning):
//
//	isospin       chunk equivalence class under surface-form variation
//	               (cross-language ports, symbol-renamed clones)
//	projection    one concrete realisation of an isospin class; concretely,
//	               a chunk in a specific language
//	twin          two chunks in the same isospin class, different projection
//	twin class    integer label of an isospin class
//	signature     language-independent structural hash of a chunk's declaration
//	lattice       the whole precomputed structure: per-language density,
//	               per-chunk signature, signature → chunk-index index
//	bridge        a resonance hop from one projection to a sibling projection,
//	               i.e. from a matched chunk to its cross-language twin
//	flux          accumulated bridge score into a twin, summed over all
//	               matched sources that reach it.

const (
	isoMinTwinSymbols = 2  // a 1-symbol signature is coincidence, not structure
	isoMaxTwinDF      = 24 // above this the signature is a hub, not a twin
	isoDensFloor      = 1e-6
	isoDensClamp      = 1.8 // a score may be damped, never erased
	isoBridgeFloor    = 40.0
	isoBridgeDecay    = 0.55
)

// isospinLattice is the precomputed, query-free structure that lets the
// selector and the bridge work in O(1) per chunk instead of scanning the
// corpus or calling a vector index.
//
//	sig        per-chunk structural signature; 0 means the chunk is not part
//	           of any isospin class (too few symbols to form a signature)
//	twin       signature → chunk indices that share it
//	twinDF     signature → number of chunks that share it, i.e. the class size
//	twinClass  per-chunk class label; -1 for chunks with no class
type isospinLattice struct {
	density   map[string]float64 // per-language expected score density
	relDens   map[string]float64 // density[lang] / mean(density); the divisor
	sig       []uint64           // per-chunk structural signature; 0 = not a twin
	twin      map[uint64][]int   // structural signature → chunk indices
	twinDF    map[uint64]int     // signature → how many projections share it
	twinClass []int
}

// isospinLatticeOrBuild returns the lattice, building it on first use. The
// result is cached on the builder; call resetIsospinIndex when chunks change.
func (cb *ContextBuilder) isospinLatticeOrBuild() *isospinLattice {
	if cb.isospin == nil {
		cb.isospin = buildIsospinLattice(cb)
	}
	return cb.isospin
}

// resetIsospinIndex drops every cached isospin and transport structure. Call
// this from the same place that rebuilds cb.chunks / cb.symbolIndex.
// nolint: unused
func (cb *ContextBuilder) resetIsospinIndex() {
	cb.isospin = nil
	cb.medSymbol = nil
	cb.medCall = nil
	cb.medLex = nil
}

// buildIsospinLattice does the work the old per-query alignment loop did, once.
//
// Pass 1 scores each chunk's expected exposure under a random-symbol query and
// averages that per language. The result is that language's natural scoring
// bias, the projection offset, expressed as a scalar instead of a vector.
//
// Pass 2 hashes every chunk's declaration into a language-independent
// structural fingerprint.
func buildIsospinLattice(cb *ContextBuilder) *isospinLattice {
	lat := &isospinLattice{
		density: make(map[string]float64),
		relDens: make(map[string]float64),
		sig:     make([]uint64, len(cb.chunks)),
		twin:    make(map[uint64][]int),
		twinDF:  make(map[uint64]int),
	}
	if len(cb.chunks) == 0 {
		return lat
	}

	// Pass 1: per-language lexical density.
	sum := make(map[string]float64, 8)
	cnt := make(map[string]int, 8)
	scratch := make(map[string]bool, 16)
	for i := range cb.chunks {
		lang := chunkLanguage(cb.chunks[i].File)
		d := 0.0
		for _, s := range cb.chunks[i].Symbols {
			low := strings.ToLower(s)
			if len(low) < 3 || scratch[low] {
				continue
			}
			scratch[low] = true
			// sqrt(df): rare symbols hit hard and are counted fully,
			// ubiquitous ones barely register, which is what actually
			// happens to them at query time.
			df := float64(len(cb.symbolIndex[low]))
			d += 1.0 / math.Sqrt(df)
		}
		clear(scratch)
		sum[lang] += d
		cnt[lang]++
	}

	var total float64
	var langs int
	for lang, s := range sum {
		c := float64(cnt[lang])
		if c == 0 {
			continue
		}
		lat.density[lang] = s / c
		total += lat.density[lang]
		langs++
	}
	if langs == 0 {
		return lat
	}
	mean := total / float64(langs)
	if mean < isoDensFloor {
		mean = isoDensFloor
	}
	for lang, d := range lat.density {
		rel := d / mean
		if rel < 1/isoDensClamp {
			rel = 1 / isoDensClamp
		} else if rel > isoDensClamp {
			rel = isoDensClamp
		}
		lat.relDens[lang] = rel
	}

	// Pass 2: structural signatures, cached per chunk so the bridge does not
	// re-hash every source chunk on every query.
	for i := range cb.chunks {
		sig, ok := structuralSignature(cb.chunks[i])
		if !ok {
			continue
		}
		lat.sig[i] = sig
		lat.twin[sig] = append(lat.twin[sig], i)
		lat.twinDF[sig] = len(lat.twin[sig])
	}
	// Drops a signature shared by hundreds of chunks carries no
	// structural information and would just be a slow way to return noise.
	for sig, df := range lat.twinDF {
		if df < 2 || df > isoMaxTwinDF {
			delete(lat.twin, sig)
			delete(lat.twinDF, sig)
		}
	}

	return lat
}

// structuralSignature hashes a chunk's declaration into a fingerprint that is
// stable across languages: the sorted, lowercased, underscore-stripped symbol
// set plus the chunk type. Two chunks declaring the same things in different
// languages collide here even when no string is shared.
//
// ChunkType is a language-neutral axis and is deliberately part of the key
// a method and a struct with identical symbol sets are not the same
// structural state. (The function is named for "structural", not "struct",
// for exactly this reason.)
func structuralSignature(c Chunk) (uint64, bool) {
	if len(c.Symbols) < isoMinTwinSymbols {
		return 0, false
	}
	norm := make([]string, 0, len(c.Symbols))
	seen := make(map[string]bool, len(c.Symbols))
	for _, s := range c.Symbols {
		n := strings.ToLower(strings.TrimLeft(s, "_"))
		if len(n) < 3 || seen[n] {
			continue
		}
		seen[n] = true
		norm = append(norm, n)
	}
	if len(norm) < isoMinTwinSymbols {
		return 0, false
	}
	sort.Strings(norm)

	h := fnv.New64a()
	_, _ = h.Write([]byte(c.ChunkType))
	for _, n := range norm {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(n))
	}
	return h.Sum64(), true
}

// isospinBridgeSeek crosses the doublet.
//
// For every chunk the lexical and graph strategies already love, emit the
// chunks that are the same structural state in a *different* language. This
// is the channel that used to be vectorSearch: the only route by which a
// query reaches code sharing no token with it. Cost is one hash lookup per
// source chunk, not a scan over the corpus.
//
// Flux accumulates a twin reachable from three strong sources resonates
// harder than one reachable from a single source. That is transport, not
// first-come-first-served.
func (cb *ContextBuilder) isospinBridgeSeek(sources map[string]ScoredChunk, topK int) []ScoredChunk {
	lat := cb.isospinLatticeOrBuild()
	if lat == nil || len(lat.twin) == 0 || len(sources) == 0 {
		return nil
	}
	if cb.chunkIdx == nil {
		return nil
	}

	flux := make(map[int]float64, topK*2)
	detail := make(map[int]string, topK*2)

	for _, src := range sources {
		if src.Pinned || src.Score < isoBridgeFloor {
			continue
		}
		si := cb.chunkIdx[src.ID]
		if si < 0 || si >= len(cb.chunks) || si >= len(lat.sig) {
			continue
		}
		sig := lat.sig[si]
		if sig == 0 {
			continue
		}
		peers := lat.twin[sig]
		if len(peers) < 2 {
			continue
		}
		hop := src.Score * isoBridgeDecay / math.Sqrt(float64(lat.twinDF[sig]))
		if hop < isoBridgeFloor*0.25 {
			continue
		}
		from := chunkLanguage(src.File)
		for _, j := range peers {
			if j == si {
				continue
			}
			// Same projection; grep/exact/partial already own that ground.
			if chunkLanguage(cb.chunks[j].File) == from {
				continue
			}
			flux[j] += hop
			if _, ok := detail[j]; !ok {
				detail[j] = fmt.Sprintf("isospin-bridge: %s ↔ %s (σ=%d, %s→%s)",
					nameOf(cb.chunks[si]), nameOf(cb.chunks[j]),
					lat.twinDF[sig], from, chunkLanguage(cb.chunks[j].File))
			}
		}
	}
	if len(flux) == 0 {
		return nil
	}

	out := make([]ScoredChunk, 0, len(flux))
	for j, f := range flux {
		out = append(out, ScoredChunk{
			Chunk:        cb.chunks[j],
			Score:        f,
			MatchDetails: detail[j],
		})
	}
	if len(out) > topK*2 {
		out = out[:topK*2]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if debugLevel3 {
		cb.debugLog.Log("[isospinBridge] %d structural signatures, %d cross-projection hits",
			len(lat.twin), len(out))
	}
	return out
}

// nameOf returns a chunk's display name: its Name if set, otherwise its
// ClassName, otherwise its ID. Used for bridge diagnostics only.
func nameOf(c Chunk) string {
	if c.Name != "" {
		return c.Name
	}
	if c.ClassName != "" {
		return c.ClassName
	}
	return c.ID
}

// isoNormalizeScores divides out each language's relative density, so a chunk
// in a lexically dense projection (C: short symbols, high df, dense
// declarations) cannot out-rank one in a sparse projection purely for being
// dense. Exact and pinned hits are exempt and those already mean what they say.
//
// Applied before subsystem boosting so an intentional subsystem boost
// survives normalization instead of being averaged away.
func isoNormalizeScores(cands []ScoredChunk, cb *ContextBuilder) []ScoredChunk {
	lat := cb.isospinLatticeOrBuild()
	if lat == nil || len(lat.relDens) == 0 {
		return cands
	}
	for i := range cands {
		if cands[i].IsExact || cands[i].Pinned {
			continue
		}
		d := lat.relDens[chunkLanguage(cands[i].File)]
		// Defensive: a language that appears in chunks always has a relDens
		// entry, so this miss should not happen in practice. Skip rather than
		// divide by zero, which would poison the score with NaN.
		if d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) {
			continue
		}
		cands[i].Score /= d
	}
	return cands
}
