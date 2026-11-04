package retrieval

import (
	"fmt"
	"testing"
)

func TestNoveltyPackWeightsTailBeforeRanking(t *testing.T) {
	for _, topK := range []int{1, 2, 3} {
		chunks := make([]Chunk, 3*topK+1)
		for i := range chunks {
			chunks[i] = Chunk{ID: fmt.Sprintf("c%02d", i), Symbols: []string{"raresym"}}
		}
		cb := testEhnasBuilder(t, chunks)
		in := make([]ScoredChunk, len(chunks))
		for i, c := range chunks {
			in[i] = ScoredChunk{Chunk: c, Score: 100 - float64(i)/10}
		}
		got := noveltyPack(in, topK, cb)
		if len(got) != len(chunks) || got[0].ID != "c00" {
			t.Fatalf("unweighted tail won for topK=%d: %v", topK, ids(got))
		}
	}
}

func TestNoveltyPackKeepsExactAndPinnedScores(t *testing.T) {
	cb := testEhnasBuilder(t, []Chunk{{ID: "pin"}, {ID: "exact"}, {ID: "normal"}, {ID: "tail"}})
	in := []ScoredChunk{
		{Chunk: Chunk{ID: "pin", Symbols: []string{"shared"}}, Score: 5000, Pinned: true, IsExact: true},
		{Chunk: Chunk{ID: "exact", Symbols: []string{"shared"}}, Score: 300, IsExact: true},
		{Chunk: Chunk{ID: "normal", Symbols: []string{"shared"}}, Score: 100},
		{Chunk: Chunk{ID: "tail", Symbols: []string{"shared"}}, Score: 99},
	}
	got := noveltyPack(in, 1, cb)
	if got[0].ID != "pin" || got[0].Score != 5000 || got[1].ID != "exact" || got[1].Score != 300 {
		t.Fatalf("changed pinned/exact scores: %v", got)
	}
}
