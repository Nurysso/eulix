//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides repository subsystem detection, tree indexing, and path filtering.

package query

import (
	"eulix/internal/config"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SubsystemNode represents a directory node in the repository tree.
// Built once during index construction; read-only thereafter.
type SubsystemNode struct {
	Path        string
	IsNoise     bool
	Children    map[string]*SubsystemNode
	ChunkCount  int // Chunks directly under this path prefix
	TotalChunks int // Chunks in this subtree (own + children)
	Depth       int // 0 = repo root segment
	// PathTokens are the lowercase split segments of Path, cached for
	// query-time matching without repeated string splitting.
	PathTokens []string
}

// subsystemScore tracks intermediate subsystem detection scores at query time.
type subsystemScore struct {
	node  *SubsystemNode
	score float64
}

// ScoredSubsystem represents a subsystem node paired with its match score.
type ScoredSubsystem struct {
	Node  *SubsystemNode
	Score float64
}

// testDocSegments maps path segment names that mark test, spec, doc, or example code.
var testDocSegments = map[string]bool{
	"test": true, "tests": true, "testing": true,
	"unit": true, "integration": true, "e2e": true, "spec": true, "specs": true,
	"docs": true, "doc": true, "documentation": true,
	"examples": true, "example": true, "samples": true, "sample": true,
	"fixtures": true, "mocks": true, "mock": true, "stubs": true, "testdata": true,
}

// testDocAdmissionFloor sets the minimum score threshold required for test/doc chunks.
const testDocAdmissionFloor = 60.0

// subsysFinalK defines the top-K subsystem candidates kept for score boosting.
const subsysFinalK = 5

// logSubsystemsToFile writes the constructed subsystem tree nodes to a debug log file.
func logSubsystemsToFile(nodes []*SubsystemNode) error {
	eulixDir := ".eulix"
	logPath := filepath.Join(eulixDir, "debug", "susbstemDebug.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open subsystem.log: %w", err)
	}
	defer func() { _ = f.Close() }()

	_, err = fmt.Fprintln(f, "--- Subsystem Tree Index ---")
	if err != nil {
		return err
	}

	for _, n := range nodes {
		_, err := fmt.Fprintf(f, "Path: %-45s | TotalChunks: %-6d | ChunkCount: %-6d | Depth: %d\n",
			n.Path, n.TotalChunks, n.ChunkCount, n.Depth)
		if err != nil {
			return err
		}
	}
	return nil
}

// isTestDocPath reports whether any path segment matches a known test, doc, or example marker.
func isTestDocPath(fileLow string) bool {
	for _, seg := range strings.Split(fileLow, "/") {
		if testDocSegments[seg] {
			return true
		}
	}
	return false
}

// filterTestDocChunks filters out test and documentation chunks that fall below the admission threshold.
func filterTestDocChunks(results []ScoredChunk) []ScoredChunk {
	kept := results[:0]
	for _, r := range results {
		if isTestDocPath(strings.ToLower(r.File)) && r.Score < testDocAdmissionFloor {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// buildSubsystemTree constructs a prefix tree of directory paths and annotates nodes with chunk counts.
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

	// Map file directory prefixes to tree nodes and increment direct counts.
	for _, c := range cb.chunks {
		if c.File == "" {
			continue
		}
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

	// Sort nodes by depth descending to aggregate subtree totals bottom-up.
	nodes := make([]*SubsystemNode, 0, len(flat))
	for _, n := range flat {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Depth > nodes[j].Depth
	})

	for _, n := range nodes {
		n.TotalChunks = n.ChunkCount
		parent := parentPath(n.Path)
		if parent != "" {
			if p, ok := flat[parent]; ok {
				p.TotalChunks += n.TotalChunks

				if p.Children == nil {
					p.Children = make(map[string]*SubsystemNode)
				}
				p.Children[n.Path] = n
			}
		}
	}

	// Retain only boundary nodes meeting the minimum chunk threshold.
	cb.subsystemTree = make([]*SubsystemNode, 0, 64)
	for _, n := range nodes {
		if n.TotalChunks >= subsysMinChunks {
			cb.subsystemTree = append(cb.subsystemTree, n)
		}
	}

	if err := logSubsystemsToFile(cb.subsystemTree); err != nil {
		cb.debugLog.Log("Failed to write to subsystem.log: %v", err)
	}

	cb.debugLog.Log("Subsystem tree: %d nodes (min chunks=%d)",
		len(cb.subsystemTree), subsysMinChunks)
}

// detectQuerySubsystems scores subsystem nodes against query tokens and returns ranked candidates.
func detectQuerySubsystems(
	nodes []*SubsystemNode,
	queryTokens []string,
) []subsystemScore {
	const subsysCandidateWindow = 20

	if len(nodes) == 0 || len(queryTokens) == 0 {
		return nil
	}

	scores := make([]subsystemScore, 0, len(nodes))

	for _, n := range nodes {
		if isTestDocPath(strings.ToLower(n.Path)) {
			continue
		}
		var sc float64
		for _, qt := range queryTokens {
			if len(qt) < 3 {
				continue
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
		// Add log-normalized density bonus to favor substantive subsystems.
		density := math.Log2(float64(n.TotalChunks)+1) / math.Log2(300_000)
		sc += density * 2.0
		scores = append(scores, subsystemScore{node: n, score: sc})
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	if len(scores) > subsysCandidateWindow {
		scores = scores[:subsysCandidateWindow]
	}
	return scores
}

// filterNoiseSubsystems demotes candidate subsystems matching known noise path prefixes.
func filterNoiseSubsystems(detected []subsystemScore, noisePaths []string) []subsystemScore {
	for i := range detected {
		pathLow := strings.ToLower(detected[i].node.Path)
		for _, np := range noisePaths {
			if strings.HasPrefix(pathLow, np) {
				detected[i].score *= 0.15 // Demote score without complete exclusion.
				break
			}
		}
	}
	sort.Slice(detected, func(i, j int) bool { return detected[i].score > detected[j].score })
	return detected
}

// boostByDetectedSubsystems applies multiplicative score boosts based on subsystem matching confidence.
func boostByDetectedSubsystems(
	results []ScoredChunk,
	detected []subsystemScore,
	noisePaths []string,
	cfg *config.RetrievalConfig,
) {
	if len(detected) == 0 {
		return
	}

	topConf := detected[0].score
	applyPenalty := topConf >= 6.0 // Enforce penalties when top detection is confident.

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
			case strings.Contains(fileLow, "/"+pathLow+"/") || strings.Contains(fileLow, pathLow):
				boost = 1.8
			default:
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

			if boost > 1.0 {
				scaled := 1.0 + (boost-1.0)*(ds.score/topConf)
				if scaled > bestBoost {
					bestBoost = scaled
					matched = true
				}
			}
		}

		if !matched && applyPenalty {
			// Use configurable penalty instead of hardcoded multiplier
			bestBoost = float64(cfg.CrossRootPenalty)

			// Keep aggressive noise path check
			for _, np := range noisePaths {
				if strings.Contains(fileLow, np) {
					bestBoost = 0.01
					break
				}
			}
		}

		results[i].Score *= bestBoost
	}
}

// detectNoisePatterns identifies dense or highly generic directory prefixes to treat as noise paths.
func (cb *ContextBuilder) detectNoisePatterns() {
	if len(cb.subsystemTree) == 0 {
		return
	}

	genericSegs := map[string]bool{
		"test": true, "tests": true, "common": true, "utils": true,
		"util": true, "tools": true, "scripts": true, "vendor": true,
		"third_party": true, "thirdparty": true, "lib": true, "libs": true,
		"fixtures": true, "mocks": true, "mock": true, "stubs": true,
		"helpers": true, "helper": true, "base": true, "core": true,
		"internal": true, "shared": true,
	}

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

		// Flag as noise only when all segments match known generic tokens.
		allGeneric := true
		for _, seg := range n.PathTokens {
			if !genericSegs[seg] {
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

// proximityBonus calculates an additive score bonus for query term co-occurrence within a sliding character window.
func proximityBonus(content string, terms []string, windowSize int) float64 {
	if len(terms) < 2 || content == "" {
		return 0
	}
	contentLow := strings.ToLower(content)

	// Locate initial positions of query terms.
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

	// Calculate distance-decayed proximity bonuses for adjacent term hits.
	bonus := 0.0
	for i := 1; i < len(positions); i++ {
		dist := positions[i] - positions[i-1]
		if dist <= windowSize {
			bonus += 5.0 * (1.0 - float64(dist)/float64(windowSize))
		}
	}
	return bonus
}

// dirOf extracts the directory portion of a file path before the final slash.
func dirOf(filePath string) string {
	idx := strings.LastIndex(filePath, "/")
	if idx < 0 {
		return ""
	}
	return filePath[:idx]
}

// parentPath extracts the parent directory path segment.
func parentPath(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}
