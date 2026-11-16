package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetrievalEngineOverrides(t *testing.T) {
	dir := t.TempDir()
	toml := `
[retrievalConfig]
pre_mmr_score_floor_ratio = 0.05
max_context_chunks = 30

[retrievalConfig.ehnas]
pre_mmr_score_floor_ratio = 0.3
mmr_stop_floor = 0.1
inverted_index_min_chunks = 0
`
	if err := os.WriteFile(filepath.Join(dir, "eulix.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	rc := cfg.RetrievalConfig

	eh := rc.ForEngine("ehnas")
	if eh.PreMMRScoreFloorRatio != 0.3 || eh.MaxContextChunks != 30 {
		t.Errorf("ehnas: floor=%v maxChunks=%d, want 0.3 (override) / 30 (inherited)",
			eh.PreMMRScoreFloorRatio, eh.MaxContextChunks)
	}
	if eh.Ehnas.MMRStopFloor != 0.1 || eh.Ehnas.InvertedIndexMinChunks != 0 || eh.Ehnas.MCWalkers != 256 {
		t.Errorf("ehnas knobs: stop=%v invIdx=%d walkers=%d, want 0.1 / 0 (explicit zero kept) / 256 (default)",
			eh.Ehnas.MMRStopFloor, eh.Ehnas.InvertedIndexMinChunks, eh.Ehnas.MCWalkers)
	}

	if et := rc.ForEngine("etiya"); et.PreMMRScoreFloorRatio != 0.05 {
		t.Errorf("etiya floor = %v, want inherited 0.05", et.PreMMRScoreFloorRatio)
	}
	if rc.PreMMRScoreFloorRatio != 0.05 {
		t.Errorf("ForEngine mutated the receiver: shared floor = %v", rc.PreMMRScoreFloorRatio)
	}
}
