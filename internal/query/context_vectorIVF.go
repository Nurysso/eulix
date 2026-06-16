//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides the context window builder and query routing for Eulix's
// RAG (Retrieval-Augmented Generation) system.

/*
Package query implements Inverted File (IVF) vector indexing for efficient
semantic search over embeddings.

This file provides fast approximate nearest neighbor search through:
 1. Clustering: K-means partitioning of embedding space into centroids
 2. Inverted lists: Per-cluster assignment of embedding indices
 3. Probing: nProbe closest clusters searched at query time
 4. Scoring: Cosine similarity ranking of candidates

The IVF index trades recall for speed: instead of comparing a query
against all embeddings (O(n*d)), we:
  - Compute distances to k centroids (O(k*d))
  - Search nProbe closest clusters (O(nProbe * avg_cluster_size))
  - Re-rank candidates by exact similarity

Performance notes:
  - Index building: O(k * maxIter * n * d) via k-means
  - Query: O(k*d + nProbe*L) where L = avg embeddings per cluster
  - Memory: O(n*d + k*d) for embeddings + centroids
  - For large corpora (>100k embeddings), IVF provides 10-100x speedup
    at ~90% recall vs brute-force search

The index is built once during context setup (context_loader.go) and
reused across all queries. Fallback to brute-force search if index
is unavailable or disabled.

See:
  - context_loader.go: loading files for context window creations
  - context_search.go: integration with multi-strategy search pipeline
  - context_builder.go: vectorSearchIVF entry point and scoring

// This implements the standard k-means IVF variant. Other variants
// (e.g., residual encoding, product quantization) are not implemented.
*/
package query

import (
	"log"
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
		cd[i] = centDist{float32(cosineSimilarity(qEmb, c)), i}
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
			sim := float32(cosineSimilarity(qEmb, cb.embeddings[embIdx]))
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
//
// Algorithm:
//  1. Initialize k centroids by random selection from input embeddings
//  2. Iterate up to maxIter times:
//     a. Assign each embedding to nearest centroid (by cosine similarity)
//     b. Recompute centroids as mean of assigned embeddings
//     c. Stop if no assignments changed
//  3. Build inverted lists: for each centroid, collect its assigned indices
//
// Time complexity: O(k * maxIter * n * d)
// Space complexity: O(n*d + k*d)
func buildIVFIndex(embs [][]float32, k, maxIter int) *IVFIndex {
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
	perm := rng.Perm(n)
	centroids := make([][]float32, k)
	for i := range centroids {
		c := make([]float32, dim)
		copy(c, embs[perm[i]])
		centroids[i] = c
	}

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
						s := float32(cosineSimilarity(emb, c))
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

		log.Printf("[IVF] iter %d/%d: changed=%d elapsed=%v",
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
