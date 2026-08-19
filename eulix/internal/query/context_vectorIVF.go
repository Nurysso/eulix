//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides context window building and query routing for Eulix's RAG system.
Key Responsibilities:
  - K-means clustering and inverted list construction over embedding spaces
  - Fast approximate nearest neighbor (ANN) retrieval using centroid probing
  - Cosine similarity candidate re-ranking with brute-force fallback
*/
package query

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const ivfNProbe = 32

// vectorSearchIVF performs approximate nearest neighbor search using IVF clustering.
// It finds topK chunks with similarity >= threshold by:
// 1. Computing distances to all centroids
// 2. Probing nProbe closest clusters
// 3. Re-ranking candidates by exact cosine similarity
// Falls back to brute-force search if IVF index is unavailable.
func (cb *ContextBuilder) vectorSearchIVF(qEmb []float32, topK int, threshold float64) []ScoredChunk {
	// IVF may still be building in background — fall back to linear scan
	cb.mu.Lock()
	idx := cb.ivfIndex
	cb.mu.Unlock()
	if idx == nil {
		return cb.vectorSearch(qEmb, topK, threshold)
	}

	type centDist struct {
		d float32
		i int
	}

	// Rank centroids by similarity to query (cast to float32 to stay consistent
	// with how centroids were built)
	cd := make([]centDist, idx.NClusters)
	for i, c := range idx.Centroids {
		cd[i] = centDist{float32(dotProduct(qEmb, c)), i}
	}
	// Sort descending by similarity (highest first)
	sort.Slice(cd, func(i, j int) bool { return cd[i].d > cd[j].d })

	nProbe := ivfNProbe
	if nProbe > idx.NClusters {
		nProbe = idx.NClusters
	}

	threshF := float32(threshold)
	scored := make([]ScoredChunk, 0, topK*2)
	seen := make(map[int32]bool, topK*4)

	for p := 0; p < nProbe; p++ {
		for _, embIdx := range idx.Lists[cd[p].i] {
			if seen[embIdx] {
				continue
			}
			seen[embIdx] = true
			if int(embIdx) >= len(cb.chunks) || int(embIdx) >= len(cb.embeddings) {
				continue
			}
			sim := float32(dotProduct(qEmb, cb.embeddings[embIdx]))
			if sim >= threshF {
				scored = append(scored, ScoredChunk{
					Chunk: cb.chunks[embIdx],
					Score: float64(sim),
				})
			}
		}
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored
}

// buildIVFIndex constructs a k-means IVF index via Lloyd's algorithm.
// Returns nil if embs is empty or k <= 0.
// The index clusters embeddings into k groups and creates inverted lists
// mapping each centroid to its assigned embedding indices.
func (cb *ContextBuilder) buildIVFIndex(embs [][]float32, k, maxIter int) *IVFIndex {
	n := len(embs)
	if n == 0 || k <= 0 {
		return nil
	}
	if k > n {
		k = n
	}
	dim := len(embs[0])
	nWorkers := runtime.NumCPU()

	rng := rand.New(rand.NewSource(42))
	centroids, _ := kmeansPlusPlusInit(embs, k, rng)

	assignments := make([]int32, n)
	sums := make([][]float64, k)
	for i := range sums {
		sums[i] = make([]float64, dim)
	}
	counts := make([]int, k)

	chunkSize := (n + nWorkers - 1) / nWorkers

	for iter := 0; iter < maxIter; iter++ {
		iterStart := time.Now()

		// Parallel assignment: each worker owns a slice of vectors
		var totalChanged int64
		var wg sync.WaitGroup
		for w := 0; w < nWorkers; w++ {
			lo := w * chunkSize
			hi := lo + chunkSize
			if hi > n {
				hi = n
			}
			wg.Add(1)
			go func(lo, hi int) {
				defer wg.Done()
				var localChanged int64
				for ei := lo; ei < hi; ei++ {
					emb := embs[ei]
					best := int32(0)
					// reuse cosineSimilarity but track as float32 to avoid
					// repeated float64 conversions in the hot path
					bestSim := float32(-1e9)
					for ci, c := range centroids {
						// cosineSimilarity returns float64; cast once per call
						s := dotProduct(emb, c)
						if s > bestSim {
							bestSim = s
							best = int32(ci)
						}
					}
					if assignments[ei] != best {
						localChanged++
						assignments[ei] = best
					}
				}
				atomic.AddInt64(&totalChanged, localChanged)
			}(lo, hi)
		}
		wg.Wait()

		cb.debugLog.Log("[IVF] iter %d/%d: changed=%d elapsed=%v",
			iter+1, maxIter, totalChanged, time.Since(iterStart))

		if totalChanged == 0 {
			break
		}

		// Serial centroid update — cheap relative to assignment
		for i := range sums {
			counts[i] = 0
			for j := range sums[i] {
				sums[i][j] = 0
			}
		}
		for ei, emb := range embs {
			ci := assignments[ei]
			counts[ci]++
			for j, v := range emb {
				sums[ci][j] += float64(v)
			}
		}
		for ci := range centroids {
			if counts[ci] == 0 {
				continue
			}
			inv := 1.0 / float64(counts[ci])
			for j := range centroids[ci] {
				centroids[ci][j] = float32(sums[ci][j] * inv)
			}
			Normalize(centroids[ci])
		}
	}

	lists := make([][]int32, k)
	for i := range lists {
		lists[i] = make([]int32, 0, n/k+16)
	}
	for ei, ci := range assignments {
		lists[ci] = append(lists[ci], int32(ei))
	}
	return &IVFIndex{
		Centroids: centroids,
		Lists:     lists,
		NClusters: k,
		Dim:       dim,
	}
}

// kmeansPlusPlusInit selects k initial centroids from embs using k-means++
// seeding in cosine-similarity space.
//
// embs MUST be L2-normalized. For unit vectors, ||a-b||^2 = 2*(1 - a·b),
// so (1 - dot) is proportional to squared Euclidean distance -- exactly
// the weight k-means++ requires. Un-normalized input silently breaks the
// algorithm's guarantees rather than erroring.
func kmeansPlusPlusInit(embs [][]float32, k int, rng *rand.Rand) ([][]float32, error) {
	n := len(embs)
	if n == 0 {
		return nil, fmt.Errorf("kmeansPlusPlusInit: empty input")
	}
	if k <= 0 || k > n {
		return nil, fmt.Errorf("kmeansPlusPlusInit: invalid k=%d for n=%d", k, n)
	}
	dim := len(embs[0])

	centroids := make([][]float32, 0, k)
	chosen := make([]bool, n)

	first := rng.Intn(n)
	c0 := make([]float32, dim)
	copy(c0, embs[first])
	centroids = append(centroids, c0)
	chosen[first] = true

	minWeight := make([]float64, n)
	for i := range minWeight {
		minWeight[i] = math.MaxFloat64
	}

	const parallelThreshold = 20_000 // tune to taste
	nWorkers := 1
	if n >= parallelThreshold {
		nWorkers = runtime.NumCPU()
	}
	chunkSize := (n + nWorkers - 1) / nWorkers

	for len(centroids) < k {
		newCentroid := centroids[len(centroids)-1]
		partialSums := make([]float64, nWorkers)

		var wg sync.WaitGroup
		for w := 0; w < nWorkers; w++ {
			lo, hi := w*chunkSize, min((w+1)*chunkSize, n)
			if lo >= hi {
				continue
			}
			wg.Add(1)
			go func(w, lo, hi int) {
				defer wg.Done()
				sum := 0.0
				for i := lo; i < hi; i++ {
					if chosen[i] {
						minWeight[i] = 0
						continue
					}
					wgt := 1.0 - float64(dotProduct(embs[i], newCentroid))
					if wgt < 0 {
						wgt = 0
					}
					if wgt < minWeight[i] {
						minWeight[i] = wgt
					}
					sum += minWeight[i]
				}
				partialSums[w] = sum
			}(w, lo, hi)
		}
		wg.Wait()

		total := 0.0
		for _, s := range partialSums {
			total += s
		}

		var picked int
		if total == 0 {
			remaining := make([]int, 0, n-len(centroids))
			for i := 0; i < n; i++ {
				if !chosen[i] {
					remaining = append(remaining, i)
				}
			}
			picked = remaining[rng.Intn(len(remaining))]
		} else {
			target := rng.Float64() * total
			acc := 0.0
			picked = -1
			for i := 0; i < n; i++ {
				if chosen[i] {
					continue
				}
				acc += minWeight[i]
				if acc >= target {
					picked = i
					break
				}
			}
			if picked == -1 {
				for i := n - 1; i >= 0; i-- {
					if !chosen[i] {
						picked = i
						break
					}
				}
			}
		}

		c := make([]float32, dim)
		copy(c, embs[picked])
		centroids = append(centroids, c)
		chosen[picked] = true
	}

	return centroids, nil
}
