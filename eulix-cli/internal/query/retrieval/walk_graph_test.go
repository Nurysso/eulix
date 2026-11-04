package retrieval

import (
	"os"
	"path/filepath"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
)

func TestCallLayerResolvesQualifiedAndNestedIDs(t *testing.T) {
	chunks := []Chunk{
		{ID: "func_outer.inner::a.py", File: "a.py", Name: "outer.inner", Symbols: []string{"outer.inner"}},
		{ID: "func_finish::b.py", File: "b.py", Name: "finish", Symbols: []string{"finish"}},
		{ID: "func_finish::other.py", File: "other.py", Name: "finish", Symbols: []string{"finish"}},
	}
	cb := testEhnasBuilder(t, chunks)
	cb.buildChunkIndex()
	cb.callGraph = map[string][]Relationship{chunks[0].ID: {{Type: "calls", Target: chunks[1].ID, Distance: 1}}}
	// An exact ID must win over a stale or ambiguous legacy caller mapping.
	cb.callSites = callSiteIndex{chunks[0].ID: {2}}
	layer := buildCallLayer(cb)
	if len(layer.edges[0]) != 1 || layer.edges[0][0].to != 1 || len(layer.edges[1]) != 1 || layer.edges[1][0].to != 0 || len(layer.edges[2]) != 0 {
		t.Fatalf("want only 0<->1, got %v", layer.edges)
	}
}

func TestCallLayerFallsBackToNamesAndIgnoresUnknownIDs(t *testing.T) {
	for _, missing := range []bool{false, true} {
		cb := testEhnasBuilder(t, []Chunk{
			{ID: "func_caller::a.go", Name: "caller", Symbols: []string{"caller"}},
			{ID: "func_callee::b.go", Name: "callee", Symbols: []string{"callee"}},
		})
		cb.buildChunkIndex()
		source := "caller"
		if missing {
			source = "func_missing::missing.go"
		}
		cb.callGraph = map[string][]Relationship{source: {{Type: "calls", Target: "callee", Distance: 1}}}
		layer := buildCallLayer(cb)
		if missing {
			if len(layer.edges[0])+len(layer.edges[1]) != 0 {
				t.Fatalf("unknown source created edges: %v", layer.edges)
			}
		} else if len(layer.edges[0]) != 1 || layer.edges[0][0].to != 1 {
			t.Fatalf("name fallback lost edge: %v", layer.edges)
		}
	}
}

func TestCallLayerRequestsCorpus(t *testing.T) {
	root := os.Getenv("EULIX_TEST_ARTIFACTS")
	if root == "" {
		t.Skip("set EULIX_TEST_ARTIFACTS to parsed public corpora")
	}
	cfg := config.DefaultConfig()
	cfg.Project.Engine = "ehnas"
	cb, err := ContextWindowCreator(filepath.Join(root, "requests"), cfg, nil, "", utils.NewDebugLogger(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cb.Close(); err != nil {
			t.Error(err)
		}
	})
	layer := buildCallLayer(cb)
	edges := 0
	for _, es := range layer.edges {
		edges += len(es)
	}
	t.Logf("requests: chunks=%d graphKeys=%d callLayerEdges=%d", len(cb.chunks), len(cb.callGraph), edges)
	if !cb.hasCallGraph || edges == 0 {
		t.Fatal("requests call layer has no edges")
	}
}
