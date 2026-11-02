package retrieval

import (
	"math"
	"slices"
	"testing"

	"eulix/internal/config"
)

const rdir = "eulix-cli/internal/query/retrieval/"

func TestLongChunkFactor(t *testing.T) {
	seeds := indexKeys([]string{"How are the seeds for the Monte Carlo walk selected"})
	span := func(file, name string, lines int) Chunk {
		return Chunk{File: file, Name: name, StartLine: 1, EndLine: lines}
	}
	for _, c := range []struct {
		name  string
		chunk Chunk
		query []string
		want  float64
	}{
		{"short chunk untouched", span(rdir+"walk_diag.go", "mcSeedIndices", 70), seeds, 1},
		{"at threshold untouched", span(rdir+"x.go", "f", longChunkLines), seeds, 1},
		{"unnamed 523 lines keeps ~48%", span(rdir+"ehnas_search.go", "EhnasSearch", 523), seeds, 0.479},
		{"unnamed 257 lines keeps ~68%", span(rdir+"search.go", "multiStrategySearch", 257), seeds, 0.683},
		// Query 19: the answer is a 396-line class the question names.
		{
			"name match exempt", span("eulix-embed/embedders/onnx_embed.py", "EmbeddingGeneratorOnnx", 396),
			indexKeys([]string{"How does the ONNX embedder generate embeddings"}), 1,
		},
		// Query 17: build_with (386 lines) is named only by its directory.
		{
			"directory match exempt", span("libeulix/src/analyze/call_graph/prism_v2.rs", "build_with", 386),
			indexKeys([]string{"How is the call graph built"}), 1,
		},
	} {
		if got := longChunkFactor(c.chunk, c.query); math.Abs(got-c.want) > 0.001 {
			t.Errorf("%s: got %.3f, want %.3f", c.name, got, c.want)
		}
	}
}

// EhnasSearch applies the penalty: two chunks match the query identically,
// and without it the long one wins the tie on its lower ID.
func TestEhnasSearchRanksLongUnnamedChunkLower(t *testing.T) {
	doc := "Documentation: picks the seeds the Monte Carlo walk starts from."
	cb := testEhnasBuilder(t, []Chunk{
		{ID: "a", File: rdir + "ehnas_search.go", Name: "EhnasSearch", StartLine: 25, EndLine: 547, Content: doc},
		{ID: "b", File: rdir + "walk_diag.go", Name: "mcSeedIndices", StartLine: 24, EndLine: 94, Content: doc},
	})
	q := "How are the seeds for the Monte Carlo walk selected"
	if res := cb.EhnasSearch(q, 10, cb.classifyQueryIntent(q), nil, &DebugTrace{}); len(res) != 2 || res[0].ID != "b" {
		t.Fatalf("want the 70-line chunk first, got %v", ids(res))
	}
}

// TestLongOrchestratorsDropBelowAnswerAndFloor, benchmark query 5. Scores are what EhnasSearch produced on this
// repository before the length penalty. EhnasSearch and multiStrategySearch
// outranked mcSeedIndices, the answer, and survived the pre-MMR floor, so
// both were hydrated (~3.4k tokens for EhnasSearch alone). With the penalty
// both fall below the answer and below the floor.
func TestLongOrchestratorsDropBelowAnswerAndFloor(t *testing.T) {
	q := "How are the seeds for the Monte Carlo walk selected"
	qKeys := slices.DeleteFunc(indexKeys([]string{q}), func(k string) bool { return prfStopwords[k] })
	sc := func(id, file string, start, end int, score float64) ScoredChunk {
		return ScoredChunk{Chunk: Chunk{ID: id, Name: id, File: rdir + file, StartLine: start, EndLine: end}, Score: score}
	}
	cands := []ScoredChunk{
		sc("monteCarloWalkWithQuery", "walk_graph.go", 510, 759, 38.095), // long, but named by the query
		sc("EhnasSearch", "ehnas_search.go", 25, 547, 18.474),
		sc("multiStrategySearch", "search.go", 115, 497, 12.659),
		sc("mcSeedIndices", "walk_diag.go", 24, 94, 12.068),
	}
	for i := range cands {
		cands[i].Score *= longChunkFactor(cands[i].Chunk, qKeys)
	}
	kept := ids(ApplyPreMMRFloor(cands, &config.RetrievalConfig{PreMMRScoreFloorRatio: 0.3}))
	if want := []string{"monteCarloWalkWithQuery", "mcSeedIndices"}; !slices.Equal(kept, want) {
		t.Fatalf("after penalty and floor: got %v, want %v", kept, want)
	}
}
