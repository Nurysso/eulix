package retrieval

import (
	"fmt"
	"slices"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
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

// TestBM25TieBreakIsDeterministic BM25 sorted only by score and truncated twice, so tied chunks at
// either cut were kept or dropped in map order. IDs run opposite to chunk
// index here, so the two tie-breaks (index for candidates, ID for the
// result) pick different chunks and both are checked.
func TestBM25TieBreakIsDeterministic(t *testing.T) {
	chunks := make([]Chunk, 30)
	for i := range chunks {
		chunks[i] = Chunk{ID: fmt.Sprintf("c%02d", 29-i), File: "a.go", Content: "seed walk"}
	}
	cb := testEhnasBuilder(t, chunks)
	want := ids(cb.invertedKeywordSearchBM25("seed walk", 3, nil))
	for range 20 { // map iteration order varies per call
		if got := ids(cb.invertedKeywordSearchBM25("seed walk", 3, nil)); !slices.Equal(got, want) {
			t.Fatalf("BM25 order changed: %v vs %v", want, got)
		}
	}
	// Proximity (topK*4 = 12 candidates) goes to the lowest indices, c29..c18;
	// the result keeps the lowest IDs among them.
	if !slices.Equal(want, []string{"c18", "c19", "c20"}) {
		t.Fatalf("want [c18 c19 c20], got %v", want)
	}
}

func TestByScoreDescBreaksTiesOnID(t *testing.T) {
	cs := []ScoredChunk{
		{Chunk: Chunk{ID: "c"}, Score: 1},
		{Chunk: Chunk{ID: "a"}, Score: 1},
		{Chunk: Chunk{ID: "z"}, Score: 2},
		{Chunk: Chunk{ID: "b"}, Score: 1},
	}
	slices.SortFunc(cs, byScoreDesc)
	if got := ids(cs); !slices.Equal(got, []string{"z", "a", "b", "c"}) {
		t.Fatalf("got %v", got)
	}
}

func TestNoveltyPackTieBreakIsOrderIndependent(t *testing.T) {
	cb := &ContextBuilder{config: config.DefaultConfig()}
	mk := func(order ...string) []ScoredChunk {
		out := make([]ScoredChunk, len(order))
		for i, id := range order {
			out[i] = ScoredChunk{Chunk: Chunk{ID: id}, Score: 5}
		}
		return out
	}
	a := ids(noveltyPack(mk("c", "a", "b"), 3, cb))
	b := ids(noveltyPack(mk("b", "c", "a"), 3, cb))
	if !slices.Equal(a, []string{"a", "b", "c"}) || !slices.Equal(a, b) {
		t.Fatalf("tied chunks depend on input order: %v vs %v", a, b)
	}
}

// testEhnasBuilder loads chunks into an EHNAS builder with derived indices
// (chunkFiles, symbol and inverted index) built the way loader.go does.
func testEhnasBuilder(t *testing.T, chunks []Chunk) *ContextBuilder {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Project.Engine = "ehnas"
	cfg.RetrievalConfig.Ehnas.InvertedIndexMinChunks = 0
	cb := NewTestContextBuilder(cfg, t.TempDir(), utils.NewDebugLogger(t.TempDir()))
	if err := cb.InitEngine(); err != nil {
		t.Fatal(err)
	}
	cb.chunks = chunks
	cb.buildDerivedIndices()
	return cb
}

func ids(cs []ScoredChunk) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}
