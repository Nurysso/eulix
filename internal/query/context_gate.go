package query

import (
	"strings"
)

// PathGate holds the normalised path constraints extracted from a query.
// An empty gate (no constraints) is a no-op — Pass() always returns true.
type PathGate struct {
	// required: ALL of these fragments must appear in a chunk's file path.
	required []string
	// preferred: chunks matching these get a score multiplier but are not
	// rejected if absent (used for loose directory hints).
	preferred []string
	active    bool
}

// buildPathGate derives a PathGate from the explicit anchors found in the
// query. Only tier-1 and tier-2 anchors (file path / filename) produce hard
// constraints; tier-3 path fragments produce soft preferred hints.
func buildPathGate(anchors []ExplicitAnchor) PathGate {
	g := PathGate{}
	for _, a := range anchors {
		if a.File == "" {
			continue
		}
		fileLow := strings.ToLower(a.File)

		// Tier-1 / tier-2: has an extension → hard constraint
		if strings.Contains(fileLow, ".") {
			// Use the directory portion as the required fragment so that
			// "drivers/gpu/drm/amd/display/dc/dml/dcn32_fpu.c" constrains
			// to the "drivers/gpu/drm/amd/display/dc/dml/" subtree, not
			// just the single file (other files in the same dir are relevant).
			dir := fileLow[:strings.LastIndex(fileLow, "/")+1]
			if dir != "" {
				g.required = append(g.required, dir)
			} else {
				// bare filename like "dcn32_fpu.c" — use it directly
				g.required = append(g.required, fileLow)
			}
			g.active = true
		} else {
			// Tier-3: directory fragment → soft preferred
			g.preferred = append(g.preferred, fileLow)
		}
	}
	return g
}

// Pass returns true if the chunk's file path satisfies all hard constraints.
func (g *PathGate) Pass(filePath string) bool {
	if !g.active {
		return true
	}
	low := strings.ToLower(filePath)
	for _, req := range g.required {
		if !strings.Contains(low, req) {
			return false
		}
	}
	return true
}

// Boost returns a score multiplier for soft preferred matches.
// Returns 1.0 (no change) when the gate is inactive or no preferred
// fragments match.
func (g *PathGate) Boost(filePath string) float64 {
	if len(g.preferred) == 0 {
		return 1.0
	}
	low := strings.ToLower(filePath)
	matches := 0
	for _, p := range g.preferred {
		if strings.Contains(low, p) {
			matches++
		}
	}
	if matches == 0 {
		return 1.0
	}
	// 1.15 per matching preferred fragment, capped at 1.45
	boost := 1.0 + 0.15*float64(matches)
	if boost > 1.45 {
		boost = 1.45
	}
	return boost
}

// applyGate filters a scored slice in-place, rejecting chunks that fail
// the hard constraint and applying the soft boost to those that pass.
// Callers should use this after every retrieval strategy.
func (g *PathGate) applyGate(chunks []ScoredChunk) []ScoredChunk {
	if !g.active && len(g.preferred) == 0 {
		return chunks
	}
	out := chunks[:0] // reuse backing array
	for _, sc := range chunks {
		if !g.Pass(sc.File) {
			continue
		}
		sc.Score *= g.Boost(sc.File)
		out = append(out, sc)
	}
	return out
}
