// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"os"
	"path/filepath"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
)

func TestChunkCapacity(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta *utils.Metadata
		want int
	}{
		{"no metadata", nil, minChunkPrealloc},
		{"zero totals", &utils.Metadata{}, minChunkPrealloc},
		{"negative totals from a corrupt file", &utils.Metadata{TotalFunctions: -5}, minChunkPrealloc},
		{"functions, classes and methods", &utils.Metadata{TotalFunctions: 50, TotalClasses: 7, TotalMethods: 20}, 77},
		{"capped", &utils.Metadata{TotalFunctions: 1 << 30}, maxChunkPrealloc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chunkCapacity(tc.meta); got != tc.want {
				t.Errorf("chunkCapacity = %d, want %d", got, tc.want)
			}
		})
	}
}

// Every builder used to reserve 320k chunks (~46 MB) however small the
// repository was. The slice is now sized from kb.json's metadata.
func TestStreamKBChunksSizesSliceFromMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, kb string
		want     int
	}{
		{"metadata totals", `{"metadata":{"total_functions":3,"total_classes":1,"total_methods":2},"structure":{}}`, 6},
		{"no metadata", `{"structure":{}}`, minChunkPrealloc},
		{"unreadable metadata", `{"metadata":"not an object","structure":{}}`, minChunkPrealloc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "kb.json"), []byte(tc.kb), 0o600); err != nil {
				t.Fatal(err)
			}
			cb := NewTestContextBuilder(config.DefaultConfig(), dir, utils.NewDebugLogger())
			if err := cb.streamKBChunks(); err != nil {
				t.Fatal(err)
			}
			if got := cap(cb.chunks); got != tc.want {
				t.Errorf("chunk slice capacity = %d, want %d", got, tc.want)
			}
		})
	}
}
