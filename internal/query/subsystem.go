//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides query classification functionality.

/*
This file is responsible for Identification of subsystems.
*/

package query

import (
	"math"
	"sort"
	"strings"
)

// SubsystemNode represents a directory node in the repository tree.
// Built once during index construction; read-only thereafter.
type SubsystemNode struct {
	Path        string // e.g. "nova/scheduler/filters"
	Children    []*SubsystemNode
	ChunkCount  int // chunks directly under this path prefix
	TotalChunks int // chunks in this subtree (own + children)
	Depth       int // 0 = repo root segment
	// PathTokens are the lowercase split segments of Path, cached for
	// query-time matching without repeated string splitting.
	PathTokens []string
}

// subsystemScore is an intermediate used only during query-time detection.
type subsystemScore struct {
	node  *SubsystemNode
	score float64
}

// buildSubsystemTree walks cb.chunks once and constructs a prefix tree of
// directory paths, annotating each node with chunk counts. Called from
// buildDerivedIndices after chunks are populated.
//
// Design: we use a flat map of path→node rather than a pointer tree during
// construction (avoids repeated child searches), then link parents once at
// the end. Nodes with fewer than subsysMinChunks total chunks are pruned;
// they're too sparse to be meaningful subsystem boundaries.
func (cb *ContextBuilder) buildSubsystemTree() {
	const subsysMinChunks = 30

	flat := make(map[string]*SubsystemNode, 256)

	get := func(path string) *SubsystemNode {
		if n, ok := flat[path]; ok {
			return n
		}
		segs := strings.Split(path, "/")
		n := &SubsystemNode{
			Path:       path,
			Depth:      len(segs) - 1,
			PathTokens: segs,
		}
		flat[path] = n
		return n
	}

	for _, c := range cb.chunks {
		if c.File == "" {
			continue
		}
		// Walk every prefix of the file's directory path.
		// e.g. "nova/scheduler/filters/foo.py" →
		//   "nova", "nova/scheduler", "nova/scheduler/filters"
		dir := dirOf(c.File)
		if dir == "" {
			continue
		}
		segs := strings.Split(dir, "/")
		for depth := 0; depth < len(segs); depth++ {
			prefix := strings.Join(segs[:depth+1], "/")
			n := get(prefix)
			n.ChunkCount++
		}
	}

	// Compute TotalChunks bottom-up: a node's total = own ChunkCount
	// (direct files in that exact dir) plus children's TotalChunks.
	// Since we have a flat map we can sort by depth descending and
	// accumulate upward.
	nodes := make([]*SubsystemNode, 0, len(flat))
	for _, n := range flat {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Depth > nodes[j].Depth // deepest first
	})

	for _, n := range nodes {
		n.TotalChunks = n.ChunkCount
		parent := parentPath(n.Path)
		if parent != "" {
			if p, ok := flat[parent]; ok {
				p.TotalChunks += n.TotalChunks
				p.Children = append(p.Children, n)
			}
		}
	}

	// Prune sparse nodes and keep only subsystem-boundary nodes:
	// nodes whose TotalChunks >= subsysMinChunks.
	cb.subsystemTree = make([]*SubsystemNode, 0, 64)
	for _, n := range nodes {
		if n.TotalChunks >= subsysMinChunks {
			cb.subsystemTree = append(cb.subsystemTree, n)
		}
	}

	cb.debugLog.Log("Subsystem tree: %d nodes (min chunks=%d)",
		len(cb.subsystemTree), subsysMinChunks)
}

// detectQuerySubsystems scores each subsystem node against the query and
// returns the top candidates sorted by score descending.
//
// Scoring per node:
//   - +4.0 per query token that exactly matches a path segment
//   - +1.5 per query token that is a substring of a path segment
//   - +0.8 per path segment that is a substring of a query token
//     (handles abbreviations like "sched" matching "scheduler")
//   - log-normalised chunk density bonus: rewards nodes that are
//     substantive subsystems rather than tiny leaf dirs
//
// We cap at returning the top subsysTopK nodes to keep the boost
// pass O(topK * candidates) rather than O(allNodes * candidates).
func detectQuerySubsystems(
	nodes []*SubsystemNode,
	queryTokens []string,
) []subsystemScore {
	const subsysTopK = 5

	if len(nodes) == 0 || len(queryTokens) == 0 {
		return nil
	}

	scores := make([]subsystemScore, 0, len(nodes))

	for _, n := range nodes {
		var sc float64
		for _, qt := range queryTokens {
			if len(qt) < 3 {
				continue // skip trivially short tokens
			}
			for _, seg := range n.PathTokens {
				switch {
				case seg == qt:
					sc += 4.0
				case strings.Contains(seg, qt):
					sc += 1.5
				case strings.Contains(qt, seg) && len(seg) >= 4:
					sc += 0.8
				}
			}
		}
		if sc == 0 {
			continue
		}
		// Density bonus: log2(totalChunks) scaled to ~0–3 range for
		// a corpus of 10–300K chunks. Prevents tiny dirs from winning
		// purely on name match.
		density := math.Log2(float64(n.TotalChunks)+1) / math.Log2(300_000)
		sc += density * 2.0

		scores = append(scores, subsystemScore{node: n, score: sc})
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	if len(scores) > subsysTopK {
		scores = scores[:subsysTopK]
	}
	return scores
}

// boostByDetectedSubsystems re-weights result scores based on subsystem
// detection. It replaces the existing boostBySubsystemPath call in
// multiStrategySearch — do not call both.
//
// Boost tiers (applied multiplicatively so they compose with BM25 scores):
//   - File path prefix exactly matches detected subsystem path: ×2.5
//   - File path contains detected subsystem path:              ×1.8
//   - File path shares ≥2 path tokens with detected subsystem: ×1.3
//   - No subsystem match AND chunk is in a noise path:         ×0.4
//
// The confidence of the top-ranked subsystem gates whether penalties are
// applied — if we're not confident about subsystem detection we only boost,
// never penalise, to avoid false negatives.
func boostByDetectedSubsystems(
	results []ScoredChunk,
	detected []subsystemScore,
	noisePaths []string,
) {
	if len(detected) == 0 {
		return
	}

	topConf := detected[0].score
	applyPenalty := topConf >= 6.0 // only penalise when detection is confident

	for i := range results {
		fileLow := strings.ToLower(results[i].File)
		bestBoost := 1.0
		matched := false

		for _, ds := range detected {
			pathLow := strings.ToLower(ds.node.Path)
			var boost float64
			switch {
			case strings.HasPrefix(fileLow, pathLow+"/") || fileLow == pathLow:
				boost = 2.5
			case strings.Contains(fileLow, "/"+pathLow+"/") ||
				strings.Contains(fileLow, pathLow):
				boost = 1.8
			default:
				// Count shared path tokens
				shared := 0
				for _, seg := range ds.node.PathTokens {
					if len(seg) >= 4 && strings.Contains(fileLow, seg) {
						shared++
					}
				}
				if shared >= 2 {
					boost = 1.3
				}
			}
			// Scale boost by relative confidence (top node = full boost,
			// subsequent nodes proportionally reduced)
			if boost > 1.0 {
				scaled := 1.0 + (boost-1.0)*(ds.score/topConf)
				if scaled > bestBoost {
					bestBoost = scaled
					matched = true
				}
			}
		}

		if !matched && applyPenalty {
			for _, np := range noisePaths {
				if strings.Contains(fileLow, np) {
					bestBoost = 0.4
					break
				}
			}
		}

		results[i].Score *= bestBoost
	}
}

// detectNoisePatterns identifies path prefixes that are likely to produce
// false positive matches for any domain query. Called once during
// buildDerivedIndices.
//
// A path is "noisy" when:
//   - Its chunk count is in the top 5% of all nodes (very high density →
//     generic infrastructure that matches everything), AND
//   - Its path tokens are all short or generic (len<5 or in a stoplist)
//
// These paths get a penalty multiplier applied by boostByDetectedSubsystems
// when the query subsystem is confidently detected elsewhere.
func (cb *ContextBuilder) detectNoisePatterns() {
	if len(cb.subsystemTree) == 0 {
		return
	}

	// Generic path segment tokens that indicate infrastructure/tooling
	// rather than domain-specific code.
	genericSegs := map[string]bool{
		"test": true, "tests": true, "common": true, "utils": true,
		"util": true, "tools": true, "scripts": true, "vendor": true,
		"third_party": true, "thirdparty": true, "lib": true, "libs": true,
		"fixtures": true, "mocks": true, "mock": true, "stubs": true,
		"helpers": true, "helper": true, "base": true, "core": true,
		"internal": true, "shared": true,
	}

	// Find the 95th percentile chunk count across nodes.
	counts := make([]int, len(cb.subsystemTree))
	for i, n := range cb.subsystemTree {
		counts[i] = n.TotalChunks
	}
	sort.Ints(counts)
	p95idx := int(float64(len(counts)) * 0.95)
	if p95idx >= len(counts) {
		p95idx = len(counts) - 1
	}
	p95 := counts[p95idx]

	cb.noisePaths = cb.noisePaths[:0]
	for _, n := range cb.subsystemTree {
		if n.TotalChunks < p95 {
			continue
		}
		allGeneric := true
		for _, seg := range n.PathTokens {
			if len(seg) >= 5 && !genericSegs[seg] {
				allGeneric = false
				break
			}
		}
		if allGeneric {
			cb.noisePaths = append(cb.noisePaths, strings.ToLower(n.Path))
			cb.debugLog.Log("Noise path detected: %s (chunks=%d)", n.Path, n.TotalChunks)
		}
	}
}

// proximityBonus scans content for co-occurrence of query terms within a
// sliding window. Returns an additive score bonus.
//
// Used by invertedKeywordSearchBM25 to reward chunks where query terms
// appear near each other (strong signal of topical relevance) vs spread
// across unrelated parts of a large file.
//
// Window is measured in characters rather than words to avoid splitting
// the content into tokens again — the BM25 pass already did that.
func proximityBonus(content string, terms []string, windowSize int) float64 {
	if len(terms) < 2 || content == "" {
		return 0
	}
	contentLow := strings.ToLower(content)
	// Find first occurrence position of each term.
	positions := make([]int, 0, len(terms))
	for _, t := range terms {
		idx := strings.Index(contentLow, t)
		if idx >= 0 {
			positions = append(positions, idx)
		}
	}
	if len(positions) < 2 {
		return 0
	}
	sort.Ints(positions)

	// Scan pairs of consecutive found positions: bonus inversely
	// proportional to their distance, capped at windowSize.
	bonus := 0.0
	for i := 1; i < len(positions); i++ {
		dist := positions[i] - positions[i-1]
		if dist <= windowSize {
			// Max bonus 5.0 at dist=0, decaying to 0 at dist=windowSize.
			bonus += 5.0 * (1.0 - float64(dist)/float64(windowSize))
		}
	}
	return bonus
}

// dirOf returns the directory portion of a file path (everything before
// the last slash). Returns "" for top-level files.
func dirOf(filePath string) string {
	idx := strings.LastIndex(filePath, "/")
	if idx < 0 {
		return ""
	}
	return filePath[:idx]
}

// parentPath returns the parent directory path.
// Returns "" if path has no slash (already a root segment).
func parentPath(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}
