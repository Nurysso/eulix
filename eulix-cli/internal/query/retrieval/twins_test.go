package retrieval

import (
	"fmt"
	"slices"
	"testing"
)

func TestStructuralMMRPenalizesCrossLanguageTwins(t *testing.T) {
	chunks := []Chunk{
		{ID: "port_py", File: "one.py", ChunkType: "function", Name: "PyPort", Symbols: []string{"__parse_config", "__write_value"}, Tokens: 10},
		{ID: "port_go", File: "two.go", ChunkType: "function", Name: "GoPort", Symbols: []string{"parse_config", "write_value"}, Tokens: 10},
		{ID: "unique", File: "three.rs", ChunkType: "function", Name: "Unique", Symbols: []string{"distinct_alpha", "distinct_beta"}, Tokens: 10},
	}
	cb := testEhnasBuilder(t, slices.Clone(chunks))
	cb.buildChunkIndex()
	cb.config.RetrievalConfig.MMRDiversityFactor = .2
	cb.config.RetrievalConfig.MaxContextChunks = 3
	in := []ScoredChunk{{Chunk: chunks[0], Score: 100}, {Chunk: chunks[1], Score: 99}, {Chunk: chunks[2], Score: 98}}
	selected := cb.structuralMMR(in, 1000, nil, nil, PathGate{})
	var got []string
	ports := 0
	for _, c := range selected {
		got = append(got, c.ID)
		if c.ID == "port_py" || c.ID == "port_go" {
			ports++
		}
	}
	t.Logf("twin groups=%d twinClass=%v selected=%v", len(cb.twins.twin), cb.twins.twinClass, got)
	if ports != 1 {
		t.Fatalf("expected one of the redundant structural twins, selected %d: %v", ports, got)
	}
}

func TestStructuralSignatureBridgesSnakeAndCamelCase(t *testing.T) {
	chunks := []Chunk{
		{ID: "snake", File: "a.py", ChunkType: "function", Name: "parse_config", Symbols: []string{"parse_config", "read_value"}},
		{ID: "camel", File: "b.go", ChunkType: "function", Name: "ParseConfig", Symbols: []string{"ParseConfig", "ReadValue"}},
	}
	a, okA := structuralSignature(chunks[0])
	b, okB := structuralSignature(chunks[1])
	cb := testEhnasBuilder(t, slices.Clone(chunks))
	cb.buildChunkIndex()
	hits := cb.crossLangTwinSearch(map[string]ScoredChunk{"snake": {Chunk: chunks[0], Score: 100}}, 10)
	t.Logf("snake=%x camel=%x valid=%v/%v bridge=%v", a, b, okA, okB, ids(hits))
	if len(hits) != 1 || hits[0].ID != "camel" {
		t.Fatal("underscore-stripped cross-language signature cannot bridge the snake/camel port")
	}
}

func TestTwinClassesAreStableAndExcludeIsolatedAndHubChunks(t *testing.T) {
	chunks := []Chunk{
		{ID: "a", ChunkType: "function", Symbols: []string{"alpha", "beta"}},
		{ID: "b", ChunkType: "function", Symbols: []string{"alpha", "beta"}},
		{ID: "c", ChunkType: "function", Symbols: []string{"gamma", "delta"}},
		{ID: "d", ChunkType: "function", Symbols: []string{"gamma", "delta"}},
		{ID: "isolated", ChunkType: "function", Symbols: []string{"single"}},
	}
	for i := 0; i < 25; i++ {
		chunks = append(chunks, Chunk{ID: fmt.Sprintf("hub%02d", i), ChunkType: "function", Symbols: []string{"hub_left", "hub_right"}})
	}
	classes := func(input []Chunk) map[string]int {
		cb := testEhnasBuilder(t, slices.Clone(input))
		ti := buildTwinIndex(cb)
		if len(ti.twinClass) != len(input) {
			t.Fatalf("missing twin classes: got %d want %d", len(ti.twinClass), len(input))
		}
		out := make(map[string]int, len(input))
		for i, c := range cb.chunks {
			out[c.ID] = ti.twinClass[i]
		}
		if out["a"] < 0 || out["a"] != out["b"] || out["c"] < 0 || out["c"] != out["d"] || out["a"] == out["c"] {
			t.Fatalf("bad group classes: %v", out)
		}
		if out["isolated"] != -1 {
			t.Fatal("isolated chunk acquired a class")
		}
		for i := 0; i < 25; i++ {
			if out[fmt.Sprintf("hub%02d", i)] != -1 {
				t.Fatal("hub acquired a class")
			}
		}
		return out
	}
	want := classes(slices.Clone(chunks))
	slices.Reverse(chunks)
	got := classes(chunks)
	for id, class := range want {
		if got[id] != class {
			t.Fatalf("class changed for %s: %d -> %d", id, class, got[id])
		}
	}
}

func TestStructuralMMRUnknownPinDoesNotAcquireFirstTwinClass(t *testing.T) {
	chunks := []Chunk{
		{ID: "a", File: "a.py", ChunkType: "function", Symbols: []string{"alpha", "beta"}, Tokens: 10},
		{ID: "b", File: "b.go", ChunkType: "function", Symbols: []string{"alpha", "beta"}, Tokens: 10},
	}
	cb := testEhnasBuilder(t, slices.Clone(chunks))
	cb.buildChunkIndex()
	cb.config.RetrievalConfig.MMRDiversityFactor = .1
	cb.config.RetrievalConfig.MaxContextChunks = 2
	pin := ScoredChunk{Chunk: Chunk{ID: "external-pin", File: "other.rs", Tokens: 10}, Score: 5000, Pinned: true, IsExact: true}
	got := cb.structuralMMR([]ScoredChunk{pin, {Chunk: chunks[0], Score: 100}}, 1000, nil, nil, PathGate{})
	if len(got) != 2 || got[0].ID != "external-pin" || got[1].ID != "a" {
		t.Fatalf("unknown pin incorrectly penalized an unrelated twin: %v", got)
	}
}

func TestStructuralMMRDistinguishesOverloadTwinClasses(t *testing.T) {
	chunks := []Chunk{
		{ID: "func_worker::a.py", Name: "worker", File: "a.py", StartLine: 1, EndLine: 2, ChunkType: "function", Symbols: []string{"delta", "epsilon"}, Tokens: 10},
		{ID: "func_worker::a.py", Name: "worker", File: "a.py", StartLine: 10, EndLine: 11, ChunkType: "function", Symbols: []string{"alpha", "beta"}, Tokens: 10},
		{ID: "pin", Name: "pin", File: "b.go", ChunkType: "function", Symbols: []string{"alpha", "beta"}, Tokens: 10},
	}
	cb := testEhnasBuilder(t, slices.Clone(chunks))
	cb.buildChunkIndex()
	cb.config.RetrievalConfig.MMRDiversityFactor = .1
	cb.config.RetrievalConfig.MaxContextChunks = 2
	pin := ScoredChunk{Chunk: chunks[2], Score: 5000, Pinned: true, IsExact: true}
	got := cb.structuralMMR([]ScoredChunk{pin, {Chunk: chunks[0], Score: 100}}, 1000, nil, nil, PathGate{})
	if len(got) != 2 {
		t.Fatalf("unrelated earlier overload was excluded: got=%v classes=%v", got, cb.twins.twinClass)
	}
}
