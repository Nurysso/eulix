// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import "testing"

func TestVectorSearchUsesVectorIDs(t *testing.T) {
	for _, ivf := range []bool{false, true} {
		t.Run(map[bool]string{false: "linear", true: "ivf"}[ivf], func(t *testing.T) {
			cb := &ContextBuilder{
				chunks:     []Chunk{{ID: "func_alpha::a.py"}, {ID: "func_beta::b.py"}, {ID: "missing"}},
				embeddings: [][]float32{{1, 0}, {0, 1}, {-1, 0}, {0, -1}},
				vectorMap:  map[string]int{"func_alpha::a.py": 0, "file:a.py": 1, "func_beta::b.py": 2, "file:b.py": 3},
			}
			if ivf {
				cb.ivfIndex = &IVFIndex{NClusters: 1, Centroids: [][]float32{{1, 0}}, Lists: [][]int32{{0, 1, 2, 3}}}
			}
			search := cb.vectorSearch
			if ivf {
				search = cb.vectorSearchIVF
			}
			if got := search([]float32{-1, 0}, 1, .9); len(got) != 1 || got[0].ID != "func_beta::b.py" {
				t.Errorf("beta vector returned %v, want beta", got)
			}
			if got := search([]float32{0, 1}, 1, .9); len(got) != 0 {
				t.Errorf("file-summary vector returned chunks: %v", got)
			}
		})
	}
}
