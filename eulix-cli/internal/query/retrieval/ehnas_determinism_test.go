package retrieval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
)

func determinismBuilder(t *testing.T, chunks []Chunk, engine string, inverted bool) *ContextBuilder {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Project.Engine = engine
	if inverted {
		cfg.RetrievalConfig.Ehnas.InvertedIndexMinChunks = 0
	}
	cb := NewTestContextBuilder(cfg, t.TempDir(), utils.NewDebugLogger(t.TempDir()))
	if err := cb.InitEngine(); err != nil {
		t.Fatal(err)
	}
	cb.chunks = slices.Clone(chunks)
	cb.buildDerivedIndices()
	cb.buildChunkIndex()
	if inverted && cb.invertedIdx == nil {
		cb.invertedIdx = cb.buildInvertedIndex()
	}
	return cb
}

func TestEhnasKeywordTiesUseChunkIDs(t *testing.T) {
	chunks := make([]Chunk, 30)
	for i := range chunks {
		chunks[i] = Chunk{ID: fmt.Sprintf("c%02d", 29-i), File: "a.go", Name: "worker", Content: "worker", Tokens: 10}
	}
	for _, reverse := range []bool{false, true} {
		input := slices.Clone(chunks)
		if reverse {
			slices.Reverse(input)
		}
		cb := determinismBuilder(t, input, "ehnas", false)
		for range 20 {
			got := ids(cb.keywordSearch("worker", 3, newHydrationCache(cb)))
			if !slices.Equal(got, []string{"c00", "c01", "c02"}) {
				t.Fatalf("keyword tie order: %v", got)
			}
		}
	}
}

func TestEhnasBM25ProximityCutUsesChunkIDs(t *testing.T) {
	chunks := make([]Chunk, 30)
	for i := range chunks {
		chunks[i] = Chunk{ID: fmt.Sprintf("c%02d", 29-i), File: "a.go", Content: "seed walk", Tokens: 10}
	}
	for _, reverse := range []bool{false, true} {
		input := slices.Clone(chunks)
		if reverse {
			slices.Reverse(input)
		}
		cb := determinismBuilder(t, input, "ehnas", true)
		got := ids(cb.invertedKeywordSearchBM25("seed walk", 3, nil))
		if !slices.Equal(got, []string{"c00", "c01", "c02"}) {
			t.Fatalf("BM25 cut depends on input order: %v", got)
		}
	}
}

func TestEtiyaKeepsPositionalBM25ProximityCut(t *testing.T) {
	chunks := make([]Chunk, 30)
	for i := range chunks {
		chunks[i] = Chunk{ID: fmt.Sprintf("c%02d", 29-i), File: "a.go", Content: "seed walk", Tokens: 10}
	}
	cb := determinismBuilder(t, chunks, "etiya", true)
	if got := ids(cb.invertedKeywordSearchBM25("seed walk", 3, nil)); !slices.Equal(got, []string{"c18", "c19", "c20"}) {
		t.Fatalf("ETIYA changed: %v", got)
	}
}

func TestEhnasStrategyCapsAreRepeatable(t *testing.T) {
	chunks := make([]Chunk, 600)
	for i := range chunks {
		chunks[i] = Chunk{ID: fmt.Sprintf("func_worker%03d::f.go", i), File: "f.go", Name: fmt.Sprintf("SearchWorker%03d", i), Symbols: []string{"search", "worker"}, Content: "search worker", Tokens: 10}
	}
	cb := determinismBuilder(t, chunks, "ehnas", true)
	weights := map[string]float64{"keyword": 1, "twin": .5}
	want := ids(cb.EhnasSearch("SearchWorker", 10, QueryIntent{}, weights, nil))
	if len(want) == 0 {
		t.Fatal("empty cap fixture")
	}
	for range 5 {
		got := ids(cb.EhnasSearch("SearchWorker", 10, QueryIntent{}, weights, nil))
		if !slices.Equal(got, want) {
			t.Fatalf("strategy cap changed: %v -> %v", want, got)
		}
	}
	slices.Reverse(chunks)
	other := determinismBuilder(t, chunks, "ehnas", true)
	if got := ids(other.EhnasSearch("SearchWorker", 10, QueryIntent{}, weights, nil)); !slices.Equal(got, want) {
		t.Fatalf("strategy cap depends on input: %v -> %v", want, got)
	}
}

func TestEhnasWalkIsIndependentOfChunkOrder(t *testing.T) {
	chunks := make([]Chunk, 20)
	for i := range chunks {
		chunks[i] = Chunk{ID: fmt.Sprintf("func_node%02d::f.go", i), File: "f.go", Name: fmt.Sprintf("Node%02d", i), Symbols: []string{fmt.Sprintf("isolated%02d", i)}, Tokens: 10}
	}
	chunks[0].Name = "SeedNode"
	chunks[0].Symbols = []string{"edge_alpha", "edge_beta", "edge_gamma"}
	chunks[1].Symbols = []string{"edge_alpha"}
	chunks[2].Symbols = []string{"edge_beta"}
	chunks[3].Symbols = []string{"edge_gamma"}
	run := func(input []Chunk) []string {
		cb := determinismBuilder(t, input, "ehnas", false)
		results := cb.EhnasSearch("SeedNode", 10, QueryIntent{}, map[string]float64{"keyword": 1, "twin": .5}, nil)
		out := make([]string, len(results))
		for i, sc := range results {
			out[i] = fmt.Sprintf("%s=%.12g", sc.ID, sc.Score)
		}
		return out
	}
	want := run(chunks)
	slices.Reverse(chunks)
	got := run(chunks)
	if len(want) < 2 || !slices.Equal(got, want) {
		t.Fatalf("walk changed after reversal: %v -> %v", want, got)
	}
}

func TestEhnasCorpusQueriesRepeatAndReverse(t *testing.T) {
	root := os.Getenv("EULIX_TEST_ARTIFACTS")
	if root == "" {
		t.Skip("set EULIX_TEST_ARTIFACTS to parsed public corpora")
	}
	for _, tc := range []struct{ repo, query string }{{"flask", "application context"}, {"cobra", "flag parsing"}} {
		t.Run(tc.repo, func(t *testing.T) {
			builders := make([]*ContextBuilder, 2)
			for i := range builders {
				cfg := config.DefaultConfig()
				cfg.Project.Engine = "ehnas"
				cfg.Project.DebugConfig = false
				cb, err := ContextWindowCreator(filepath.Join(root, tc.repo), cfg, nil, filepath.Join(os.Getenv("EULIX_TEST_CORPORA"), tc.repo), utils.NewDebugLogger(t.TempDir()))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := cb.Close(); err != nil {
						t.Error(err)
					}
				})
				if i == 1 {
					slices.Reverse(cb.chunks)
					cb.resetTwinIndex()
					cb.buildDerivedIndices()
					cb.buildChunkIndex()
					cb.callSites = buildCallSiteIndex(cb.cgRef, cb.chunks, cb.debugLog)
				}
				builders[i] = cb
			}
			var want string
			for i, cb := range builders {
				for rep := range 8 {
					ctx, err := cb.BuildContext(tc.query)
					if err != nil {
						t.Fatal(err)
					}
					data, err := json.Marshal(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if i == 0 && rep == 0 {
						want = string(data)
						t.Logf("query=%q chunks=%d tokens=%d", tc.query, len(ctx.Chunks), ctx.TotalTokens)
					} else if string(data) != want {
						t.Fatalf("context changed: builder=%d repeat=%d", i, rep)
					}
				}
			}
		})
	}
}

func TestEhnasOverloadIDTiesUseSourceLocation(t *testing.T) {
	chunks := []Chunk{
		{ID: "func_worker::a.py", File: "a.py", Name: "worker", Content: "worker", StartLine: 1, EndLine: 2},
		{ID: "func_worker::a.py", File: "a.py", Name: "worker", Content: "worker", StartLine: 10, EndLine: 11},
		{ID: "func_worker::a.py", File: "a.py", Name: "worker", Content: "worker", StartLine: 20, EndLine: 80},
	}
	for _, inverted := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			input := slices.Clone(chunks)
			if reverse {
				slices.Reverse(input)
			}
			cb := determinismBuilder(t, input, "ehnas", inverted)
			for range 20 {
				var got []ScoredChunk
				if inverted {
					got = cb.invertedKeywordSearchBM25("worker", 3, newHydrationCache(cb))
				} else {
					got = cb.keywordSearch("worker", 3, newHydrationCache(cb))
				}
				lines := make([]int, len(got))
				for i, sc := range got {
					lines[i] = sc.StartLine
				}
				if !slices.Equal(lines, []int{1, 10, 20}) {
					t.Fatalf("overload tie changed source location (BM25=%v): %v", inverted, lines)
				}
			}
		}
	}
}

func TestEhnasTwinBridgeAccumulationIsRepeatable(t *testing.T) {
	var chunks []Chunk
	sources := map[string]ScoredChunk{}
	for _, g := range []string{"a", "b", "c"} {
		for i, score := range []float64{80, 81, 112} {
			c := Chunk{ID: fmt.Sprintf("%s_src_%d", g, i), Name: fmt.Sprintf("Source%s%d", g, i), File: "src.py", ChunkType: "function", Symbols: []string{g + "alpha", g + "beta"}}
			chunks = append(chunks, c)
			sources[c.ID] = ScoredChunk{Chunk: c, Score: score}
		}
		chunks = append(chunks, Chunk{ID: g + "_target", File: "dst.go", ChunkType: "function", Symbols: []string{g + "alpha", g + "beta"}})
	}
	var want string
	for _, reverse := range []bool{false, true} {
		input := slices.Clone(chunks)
		if reverse {
			slices.Reverse(input)
		}
		cb := determinismBuilder(t, input, "ehnas", false)
		for rep := range 1000 {
			got, err := json.Marshal(cb.crossLangTwinSearch(sources, 1))
			if err != nil {
				t.Fatal(err)
			}
			if want == "" {
				want = string(got)
			} else if string(got) != want {
				t.Fatalf("bridge output changed: reverse=%v repeat=%d: %s -> %s", reverse, rep, want, got)
			}
		}
	}
}
