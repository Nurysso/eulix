package retrieval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Engine switch.
//
// Engine selects the retrieval strategy family. The two engines share the
// chunk store and every lexical strategy; they differ in how they reach code
// that shares no token with the query, and in what they need on disk.
//
//	orbit   — query embedding → vectorSearch/vectorSearchIVF → MMR selection.
//	          Needs embeddings.bin and vectors.bin. Queries the embedding
//	          service on every request.
//	Ehnas — isospin bridge → multi-channel particle transport →
//	          Fermi-Dirac packing. Reads no vector artifacts and makes no
//	          outbound call. Greedy selection.
//
// The switch has exactly two halves and each lives in one place:
//
//	load time   → loadEngineArtifacts
//	query time  → runSearch, useMMR, Engine().UsesSemantic()
//
// Anything that branches on "do we have embeddings" should branch on the
// engine instead. hasEmbeddings is a *load outcome* — orbit can be running
// with it false because a file was missing, and Ehnas is always false — so
// it is a bad proxy for "which pipeline are we running".

type Engine string

const (
	EngineOrbit Engine = "orbit"
	EngineEHNAS Engine = "ehnas"
)

// engineDefault is what an absent or empty config key resolves to. Orbit,
// because that is the behaviour every config file already has — a missing key
// must not silently change anyone's results.
const engineDefault = EngineOrbit

// ResolveEngine reads config.Project.Engine.
//
// Empty or absent → orbit (backwards compatible).
// A non-empty unknown value is an error. Silently running Ehnas when the
// operator meant orbit — or the reverse — returns plausible-looking results
// from the wrong pipeline, which is far more expensive to debug than a
// refusal to start.
func ResolveEngine(raw string) (Engine, error) {
	switch s := strings.ToLower(strings.TrimSpace(raw)); s {
	case "":
		return engineDefault, nil
	case string(EngineOrbit):
		return EngineOrbit, nil
	case string(EngineEHNAS):
		return EngineEHNAS, nil
	default:
		return "", fmt.Errorf("unknown config.Project.Engine %q (want %q or %q)",
			raw, EngineOrbit, EngineEHNAS)
	}
}

// SelectorFor picks the selection strategy for the active engine.
func (cb *ContextBuilder) SelectorFor() SelectorMode {
	switch {
	case cb.Engine() == EngineEHNAS:
		return SelectorStructuralMMR
	case cb.hasEmbeddings:
		return SelectorQueryMMR
	default:
		// Orbit running without embeddings: a load degraded.
		return SelectorGreedy
	}
}

// UsesSemantic reports whether the engine routes queries through embedding
// space. This is the predicate the loader, the search dispatch and the
// selector all ask, so "does this engine need a query vector" has one answer.
func (e Engine) UsesSemantic() bool { return e == EngineOrbit }

// Engine returns the engine this builder was constructed with. The zero value
// resolves to the default, so a builder created by a constructor that never
// called InitEngine keeps today's behaviour instead of panicking.
func (cb *ContextBuilder) Engine() Engine {
	if cb == nil || cb.engine == "" {
		return engineDefault
	}
	return cb.engine
}

// InitEngine resolves config.Project.Engine onto the builder. Call once from
// NewContextBuilder, before any loader runs — every load-time decision
// depends on it.
//
// Assumes Project.Engine is a string field. If yours is a custom type, cast
// here and nowhere else.
func (cb *ContextBuilder) InitEngine() error {
	if cb.config == nil {
		return fmt.Errorf("InitEngine: nil config.Project — cannot resolve engine")
	}
	eng, err := ResolveEngine(cb.config.Project.Engine)
	if err != nil {
		return err
	}
	cb.engine = eng                        // store on the builder
	cb.config.Project.Engine = string(eng) // <-- explicit conversion
	return nil
}

// Query-time half of the switch

// runSearch dispatches to the active engine's retrieval strategy. This is the
// only place the two pipelines meet on the query path.
//
// Note the asymmetry: EhnasSearch takes no query embedding at all, so the
// compiler — rather than a review comment — is what stops a dead embedding
// from being threaded through the embedding-free path.
func (cb *ContextBuilder) runSearch(
	query string,
	topK int,
	intent QueryIntent,
	weights map[string]float64,
	trace *DebugTrace,
	qEmb []float32,
) []ScoredChunk {
	if cb.Engine() == EngineEHNAS {
		return cb.EhnasSearch(query, topK, intent, weights, trace)
	}
	return cb.multiStrategySearch(query, topK, intent, weights, trace, qEmb)
}

// useMMR reports whether the active engine finishes with maximal marginal
// relevance.
//
// MMR needs a query vector for its relevance-weighted redundancy term, so
// Ehnas — which by construction has none — always selects greedily. Worth
// knowing that this is a real quality difference and not a free choice:
// noveltyPack gates redundancy before selection, but greedy can still
// re-adjacent blocks that fermi let through. A query-free MMR (redundancy
// measured only between already-selected chunks, e.g. token Jaccard) would
// close that gap without reintroducing a single embedding. That is the next
// thing to build on Ehnas, and this predicate is where it plugs in.
// nolint: unused
func (cb *ContextBuilder) useMMR() bool {
	return cb.hasEmbeddings || cb.Engine() == EngineEHNAS
}

// EnsureWeightKeys guarantees the map carries every StrategyWeights key any
// engine pipeline reads.
//
// The budget code sizes topK with a bare map index: a missing key silently
// reads 0 and collapses that strategy's budget to its floor. Without this, a
// config-only engine switch produces a working pipeline that retrieves almost
// nothing, with nothing in the logs to explain it.
func (cb *ContextBuilder) EnsureWeightKeys(weights map[string]float64) map[string]float64 {
	if weights == nil {
		weights = make(map[string]float64, 2)
	}
	for _, k := range []string{"semantic", "isospin"} {
		if _, ok := weights[k]; !ok {
			weights[k] = 0.5
		}
	}
	return weights
}

// Load-time half of the switch

// loadEngineArtifacts is the single entry point for populating a
// ContextBuilder from the eulix artifact directory.
//
// Replace any existing
//
//	loadChunks(); loadAndIndexCallGraph(); loadEmbeddings(); loadVectorMap()
//
// sequence with a single call to this.
func (cb *ContextBuilder) loadEngineArtifacts() error {
	eng := cb.Engine()
	t0 := time.Now()
	cb.debugLog.Log("[ENGINE] %s starting artifact load (semantic=%t, eulixDir=%s)",
		eng, eng.UsesSemantic(), cb.eulixDir)

	// Shared by both engines. The streaming loader forces lazyContent=false,
	// which matters more than it looks: PRF reads Chunk.Content directly, so
	// Ehnas requires chunks that carry their text inline.
	if err := cb.loadChunks(); err != nil {
		return fmt.Errorf("[%s] loadChunks: %w", eng, err)
	}
	cb.loadAndIndexCallGraph()

	if eng == EngineEHNAS {
		if err := cb.loadEhnasArtifacts(); err != nil {
			return fmt.Errorf("[%s] Ehnas artifacts: %w", eng, err)
		}
	} else if err := cb.loadOrbitArtifacts(); err != nil {
		return fmt.Errorf("[%s] orbit artifacts: %w", eng, err)
	}

	cb.debugLog.Log("[ENGINE] %s ready in %v: %d chunks, callGraph=%t, semantic=%t, invertedIdx=%t",
		eng, time.Since(t0), len(cb.chunks), cb.hasCallGraph,
		cb.hasEmbeddings, cb.invertedIdx != nil)
	return nil
}

// loadOrbitArtifacts loads the vector artifacts.
//
// A missing or unreadable file degrades to lexical-only rather than failing
// the builder — that is today's behaviour and worth preserving. It is logged
// loudly, though, because a silent degrade looks exactly like a retrieval
// quality regression with no visible cause.
func (cb *ContextBuilder) loadOrbitArtifacts() error {
	if err := cb.loadEmbeddings(); err != nil {
		cb.hasEmbeddings = false
		cb.debugLog.Log("[ORBIT] embeddings.bin unavailable (%v) — degrading to lexical-only retrieval", err)
	} else {
		cb.hasEmbeddings = len(cb.embeddings) > 0
	}
	if err := cb.loadVectorMap(); err != nil {
		cb.debugLog.Log("[ORBIT] vectors.bin unavailable (%v) — rebuilding chunk index in memory", err)
	}
	if cb.chunkIdx == nil {
		cb.buildChunkIndex()
	}
	if cb.hasEmbeddings {
		cb.debugLog.Log("[ORBIT] semantic enabled: %d embeddings, %d chunk ids",
			len(cb.embeddings), len(cb.chunkIdx))
	}
	return nil
}

// loadEhnasArtifacts builds the embedding-free structures. Nothing is read
// from disk: every one of them is derived from cb.chunks, cb.symbolIndex,
// cb.invertedIdx and cb.callGraph, all of which the shared loaders already
// produced.
func (cb *ContextBuilder) loadEhnasArtifacts() error {
	cb.buildChunkIndex()

	// Eager on purpose. O(N) with small constants, it pays for itself on the
	// first query, and it gives the operator an immediate read on how many
	// language projections and structural signatures the corpus actually has
	// — the two numbers that decide whether the isospin bridge does anything.
	//
	// The transport media stay lazy. The lexical medium is O(Σ df²) and
	// should not be paid for until a query actually needs a walk.
	cb.twins = buildTwinIndex(cb)

	cb.hasEmbeddings = false
	cb.reportEhnasIndex()
	return nil
}

// buildChunkIndex populates the chunk-ID → slice-index map.
//
// This is the same map vectors.bin carries, and the indices agree:
// vectorSearchIVF addresses cb.chunks[embIdx] with the embedding index, so
// "chunk ID → embedding slot" and "chunk ID → chunk slice index" are the same
// mapping. Orbit gets it from disk, Ehnas builds it in memory, and both end
// up in cb.chunkIdx. There is deliberately no second field — two maps for one
// concept is how you get a stale one.
//
// It also retires the O(N) chunkIndexByID linear scan that used to run once
// per candidate per query.
func (cb *ContextBuilder) buildChunkIndex() {
	cb.chunkIdx = make(map[string]int, len(cb.chunks))
	for i := range cb.chunks {
		cb.chunkIdx[cb.chunks[i].ID] = i
	}
	cb.debugLog.Log("[INDEX] chunk index: %d ids", len(cb.chunkIdx))
}

func (cb *ContextBuilder) reportEhnasIndex() {
	lat := cb.twins
	if lat != nil {
		projs := make([]string, 0, len(lat.density))
		for lang, d := range lat.density {
			projs = append(projs, fmt.Sprintf("%s:%.1f", lang, d))
		}
		sort.Strings(projs)
		cb.debugLog.Log("[EHNAS] isospin lattice: %d language projections [%s], %d structural signatures",
			len(lat.density), strings.Join(projs, " "), len(lat.twin))
	}

	// Proof of migration, not decoration. A size here confirms the artifact
	// is on disk and was deliberately not read; "absent" is what you expect
	// on a corpus that was never embedded.
	for _, f := range []string{"embeddings.bin", "vectors.bin"} {
		if st, err := os.Stat(filepath.Join(cb.eulixDir, f)); err == nil {
			cb.debugLog.Log("[EHNAS] %s present (%.1f MB) and NOT read by this engine",
				f, float64(st.Size())/(1<<20))
		} else {
			cb.debugLog.Log("[EHNAS] %s absent (expected — this engine never reads it)", f)
		}
	}

	// The one silent-failure mode in Ehnas. Both the pseudo-relevance
	// feedback pass and the lexical transport medium read invertedIdx, and
	// buildDerivedIndices only builds it above invIdxThreshold chunks. Below
	// that, Ehnas still runs and still returns results — it just loses the
	// paraphrase channel and the fuzzy medium without a word in the logs.
	if cb.invertedIdx == nil {
		cb.debugLog.Log("[EHNAS] WARNING: no inverted index (%d chunks ≤ %d threshold) — "+
			"PRF has no corpus to vote with and the lexical transport medium is empty. "+
			"Lower invIdxThreshold or accept lexical-only retrieval.",
			len(cb.chunks), invIdxThreshold)
	}
	if !cb.hasCallGraph {
		cb.debugLog.Log("[EHNAS] WARNING: no call graph — call medium unavailable, " +
			"structural queries will rely on the symbol and lexical channels")
	}
}
