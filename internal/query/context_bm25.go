package query

import (
	"math"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.74
)

// bm25Score computes the BM25 contribution of a single term in a single chunk.
//
//	tf        — term frequency in this chunk
//	df        — document (chunk) frequency of this term in the corpus
//	n         — total number of chunks
//	chunkLen  — token count of this chunk
//	avgLen    — average token count across all chunks
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
