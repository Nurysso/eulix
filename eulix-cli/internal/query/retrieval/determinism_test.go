package retrieval

import (
	"testing"

	"eulix/internal/config"
)

func TestApplyPreMMRFloorIgnoresPinnedMax(t *testing.T) {
	cfg := &config.RetrievalConfig{PreMMRScoreFloorRatio: 0.05}
	in := []ScoredChunk{
		{Chunk: Chunk{ID: "pin"}, Score: 5456, Pinned: true},
		{Chunk: Chunk{ID: "top"}, Score: 346},
		{Chunk: Chunk{ID: "mid"}, Score: 94},
		{Chunk: Chunk{ID: "low"}, Score: 10}, // < 346*0.05
	}
	got := ApplyPreMMRFloor(in, cfg)
	if len(got) != 3 || got[0].ID != "pin" || got[2].ID != "mid" {
		t.Fatalf("want [pin top mid], got %v", ids(got))
	}

	onlyPins := []ScoredChunk{{Chunk: Chunk{ID: "a"}, Score: 5000, Pinned: true}}
	if got := ApplyPreMMRFloor(onlyPins, cfg); len(got) != 1 {
		t.Fatalf("pinned-only input dropped: %v", ids(got))
	}
}

func TestMCSeedIndicesTieBreakIsDeterministic(t *testing.T) {
	cb := &ContextBuilder{config: config.DefaultConfig()}
	cb.chunks = make([]Chunk, 20)
	cb.chunkIdx = make(map[string]int, 20)
	all := make(map[string]ScoredChunk, 20)
	for i := range cb.chunks {
		id := string(rune('a' + i))
		cb.chunks[i] = Chunk{ID: id, File: id + ".go"}
		cb.chunkIdx[id] = i
		all[id] = ScoredChunk{Chunk: cb.chunks[i], Score: 94} // all tied, 20 > 16 cap
	}
	want := mcSeedIndices(cb, all)
	for range 20 { // map iteration order varies per call
		got := mcSeedIndices(cb, all)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("seed order changed: %v vs %v", want, got)
			}
		}
	}
	if len(want) != 16 || want[0] != 0 || want[15] != 15 {
		t.Fatalf("want lowest 16 indices, got %v", want)
	}
}

func ids(cs []ScoredChunk) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}
