//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package retrieval provides context window Creation for Eulix's RAG system.

/*
This file is responsible for bm25Score
*/

package retrieval

import (
	"math"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.74
)

// bm25Score computes the BM25 contribution of a single term in a single chunk.
func bm25Score(tf float32, df, n, chunkLen int, avgLen float64) float64 {
	if df == 0 || n == 0 {
		return 0
	}
	ftf := float64(tf)
	idf := math.Log((float64(n)-float64(df)+0.5)/(float64(df)+0.5) + 1)
	tfNorm := ftf * (bm25K1 + 1) /
		(ftf + bm25K1*(1-bm25B+bm25B*float64(chunkLen)/avgLen))
	return idf * tfNorm
}
