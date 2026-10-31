package retrieval

import "testing"

func TestBuildPathGateUnknownFileIsSoft(t *testing.T) {
	files := map[string]struct{}{"eulix-cli/internal/config/config.go": {}}

	g := buildPathGate([]ExplicitAnchor{{File: "eulix.toml"}}, files)
	if g.active || !g.Pass("eulix-cli/internal/config/config.go") {
		t.Fatalf("file absent from chunks must not gate: %+v", g)
	}

	g = buildPathGate([]ExplicitAnchor{{File: "config.go"}}, files)
	if !g.active || g.Pass("eulix-cli/internal/query/router_core.go") {
		t.Fatalf("file present in chunks must still gate: %+v", g)
	}
}

// chunkFiles is built lowercased at load (anchors are lowercased before the
// lookup) and released by Close.
func TestChunkFilesBuiltAtLoadAndReleasedOnClose(t *testing.T) {
	cb := testEhnasBuilder(t, []Chunk{{ID: "a", File: "eulix-cli/internal/config/Config.go", Content: "load"}})
	if g := buildPathGate([]ExplicitAnchor{{File: "Config.go"}}, cb.chunkFiles); !g.active {
		t.Fatalf("mixed-case chunk path must still gate: chunkFiles=%v", cb.chunkFiles)
	}
	if err := cb.Close(); err != nil {
		t.Fatal(err)
	}
	if cb.chunkFiles != nil {
		t.Fatalf("Close kept chunkFiles: %v", cb.chunkFiles)
	}
}
