package retrieval

import (
	"fmt"
	"sort"
)

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// mcSeedIndices with diagnostics.
//
// Two things the original hides:
//   - how many candidates existed BEFORE the Score>=60 filter (a near-empty
//     candidate list means the walk is starved, which is a different problem
//     from the walk being broken)
//   - how many distinct files the 16 retained seeds cover (1 file = the walk
//     starts entirely inside one file, and with a query-independent RNG it
//     tends to stay there)
func mcSeedIndices(cb *ContextBuilder, all map[string]ScoredChunk) []int {
	type seed struct {
		idx   int
		score float64
	}
	var seeds []seed
	candidates := 0
	unmapped := 0
	belowThreshold := 0
	maxScore := 0.0
	files := map[string]int{}

	for _, sc := range all {
		if sc.Score > maxScore {
			maxScore = sc.Score
		}
		if sc.Score < 60.0 {
			belowThreshold++
			continue
		}
		i := cb.chunkIndexByID(sc.ID)
		if i < 0 {
			unmapped++
			continue
		}
		candidates++
		seeds = append(seeds, seed{i, sc.Score})
		files[cb.chunkFileAt(i)]++
	}
	sort.Slice(seeds, func(a, b int) bool { return seeds[a].score > seeds[b].score })

	capped := len(seeds) > 16
	if capped {
		seeds = seeds[:16]
		// recount distinct files after the cap
		files = map[string]int{}
		for _, s := range seeds {
			files[cb.chunkFileAt(s.idx)]++
		}
	}

	if mcDiagEnabled {
		// Usage in your log statement:
		cb.debugLog.Log("[MC-DIAG] seedCandidates: all=%d >=60=%d mapped=%d unmapped=%d below60=%d maxScore=%.0f cap16Hit=%v",
			len(all), candidates+boolToInt(capped), candidates, unmapped, belowThreshold, maxScore, capped)
		if len(seeds) > 0 {
			cb.debugLog.Log("[MC-DIAG] seedFiles: %v", files)
			parts := make([]string, 0, 5)
			for i, s := range seeds {
				if i >= 5 {
					break
				}
				parts = append(parts, fmt.Sprintf("%s(%.0f)", cb.chunkFileAt(s.idx), s.score))
			}
			cb.debugLog.Log("[MC-DIAG] topSeeds: %v", parts)
		}
	}

	out := make([]int, 0, len(seeds))
	for _, s := range seeds {
		out = append(out, s.idx)
	}
	return out
}
