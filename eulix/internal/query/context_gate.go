package query

import (
	"strings"
)

// PathGate holds the normalised path constraints extracted from a query.
// An empty gate (no constraints) is a no-op — Pass() always returns true.
type PathGate struct {
	required  []string
	preferred []string
	active    bool
}

func buildPathGate(anchors []ExplicitAnchor) PathGate {
	g := PathGate{}
	for _, a := range anchors {
		if a.File == "" {
			continue
		}
		fileLow := strings.ToLower(a.File)

		if strings.Contains(fileLow, ".") {
			dir := fileLow[:strings.LastIndex(fileLow, "/")+1]
			if dir != "" {
				g.required = append(g.required, dir)
			} else {
				g.required = append(g.required, fileLow)
			}
			g.active = true
		} else {
			g.preferred = append(g.preferred, fileLow)
		}
	}
	return g
}

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
	boost := 1.0 + 0.15*float64(matches)
	if boost > 1.45 {
		boost = 1.45
	}
	return boost
}

func (g *PathGate) applyGate(chunks []ScoredChunk) []ScoredChunk {
	if !g.active && len(g.preferred) == 0 {
		return chunks
	}
	out := chunks[:0]
	for _, sc := range chunks {
		if !g.Pass(sc.File) {
			continue
		}
		sc.Score *= g.Boost(sc.File)
		out = append(out, sc)
	}
	return out
}
