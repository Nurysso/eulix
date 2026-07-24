//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides query classification functionality.

/*
This file is responsible for Identification of subsystems.
*/

package query

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// SubsystemNode represents a directory node in the repository tree.
// Built once during index construction; read-only thereafter.
type SubsystemNode struct {
	Path        string
	IsNoise     bool
	Children    map[string]*SubsystemNode
	ChunkCount  int // chunks directly under this path prefix
	TotalChunks int // chunks in this subtree (own + children)
	Depth       int // 0 = repo root segment
	// PathTokens are the lowercase split segments of Path, cached for
	// query-time matching without repeated string splitting.
	PathTokens []string
	// Children    []*SubsystemNode
}

// subsystemScore is an intermediate used only during query-time detection.
type subsystemScore struct {
	node  *SubsystemNode
	score float64
}
type ScoredSubsystem struct {
	Node  *SubsystemNode
	Score float64
}

// testDocSegments are path segments that mark test, spec, doc, or example
// code regardless of corpus density — detectNoisePatterns only catches
// paths in the top 5% by chunk count, which misses smaller test dirs in
// corpora dominated by one giant subsystem. This list is checked directly
// against every chunk at admission time, independent of subsystem
// detection confidence.
var testDocSegments = map[string]bool{
	"test": true, "tests": true, "testing": true,
	"unit": true, "integration": true, "e2e": true, "spec": true, "specs": true,
	"docs": true, "doc": true, "documentation": true,
	"examples": true, "example": true, "samples": true, "sample": true,
	"fixtures": true, "mocks": true, "mock": true, "stubs": true, "testdata": true,
}

func logSubsystemsToFile(nodes []*SubsystemNode) error {
	eulixDir := ".eulix"
	logPath := filepath.Join(eulixDir, "debug", "susbstemDebug.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open subsystem.log: %w", err)
	}
	defer f.Close()

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

func SelectBestSubsystems(root *SubsystemNode, query string, topK int) []*SubsystemNode {
	if root == nil {
		return nil
	}

	queryTokens := tokenizeQuery(query)
	var candidates []ScoredSubsystem

	// Traverse the subsystem tree and calculate relevance scores
	var traverse func(node *SubsystemNode)
	traverse = func(node *SubsystemNode) {
		if node == nil {
			return
		}

		// Only evaluate non-root or non-empty nodes
		if node.Path != "" && node.ChunkCount > 0 {
			score := calculateRelevanceScore(node, queryTokens)
			if score > 0 {
				candidates = append(candidates, ScoredSubsystem{
					Node:  node,
					Score: score,
				})
			}
		}

		for _, child := range node.Children {
			traverse(child)
		}
	}

	traverse(root)

	// Sort candidates descending by relevance score
	sortCandidates(candidates)

	// Return top K nodes
	var result []*SubsystemNode
	for i := 0; i < len(candidates) && i < topK; i++ {
		result = append(result, candidates[i].Node)
	}

	return result
}

// calculateRelevanceScore combines query term matching, noise penalties, and chunk density.
func calculateRelevanceScore(node *SubsystemNode, queryTokens []string) float64 {
	pathLower := strings.ToLower(node.Path)
	pathSegments := strings.Split(pathLower, "/")

	var matchScore float64
	matchedTokens := 0

	for _, token := range queryTokens {
		if token == "" {
			continue
		}

		// Exact match against directory segment gets the highest boost
		for _, segment := range pathSegments {
			if segment == token {
				matchScore += 3.0
				matchedTokens++
				break
			} else if strings.Contains(segment, token) {
				matchScore += 1.5
				matchedTokens++
				break
			}
		}
	}

	// If no query tokens match the path, rely strictly on chunk density with a penalty
	if matchedTokens == 0 {
		matchScore = 0.1
	}

	// Apply Noise Penalty (Demote test, docs, example directories)
	noiseMultiplier := 1.0
	if node.IsNoise {
		noiseMultiplier = 0.2 // Soft demotion instead of hard exclusion
	}

	// Log-scale Chunk Density Weight (prevents massive directories from completely dominating)
	densityWeight := math.Log1p(float64(node.ChunkCount))

	// Final composite score
	finalScore := matchScore * noiseMultiplier * densityWeight
	return finalScore
}

// Tokenize query into lowercase alphanumeric words
func tokenizeQuery(query string) []string {
	f := func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsNumber(c)
	}
	rawTokens := strings.FieldsFunc(strings.ToLower(query), f)

	var tokens []string
	for _, t := range rawTokens {
		if len(t) > 2 { // Filter out ultra-short stop words
			tokens = append(tokens, t)
		}
	}
	return tokens
}

// Simple in-place sort for scored subsystems
func sortCandidates(candidates []ScoredSubsystem) {
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].Score > candidates[i].Score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
}

// isTestDocPath reports whether any segment of the (lowercased) file path
// matches a known test/doc/example marker.
func isTestDocPath(fileLow string) bool {
	for _, seg := range strings.Split(fileLow, "/") {
		if testDocSegments[seg] {
			return true
		}
	}
	return false
}

// testDocAdmissionFloor is the minimum score a test/doc/example chunk must
// clear to survive into the final result set. Set well above the typical
// single-strategy match score (BM25/partial/exact hits in the 10-100 range
// before boosts) so these paths only get through on strong, multi-signal
// matches — not on a single keyword collision — while still allowing a
// deliberate "show me the test for X" query to succeed via exact/kb_exact
// matches, which score in the 90-250+ range untouched by this gate.
const testDocAdmissionFloor = 60.0

// filterTestDocChunks drops test/doc/example chunks that don't clear the
// admission floor. Applied unconditionally after all boosting — unlike
// boostByDetectedSubsystems's penalty, which only fires when subsystem
// detection is confident, this runs on every query regardless of whether
// a subsystem was detected at all.
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

				// Initialize map if nil
				if p.Children == nil {
					p.Children = make(map[string]*SubsystemNode)
				}

				// Map assignment instead of append
				p.Children[n.Path] = n
			}
		}
	}

	// Prune sparse nodes and keep only subsystem-boundary nodes:
	// nodes whose TotalChunks >= subsysMinChunks.
	// Prune sparse nodes and keep only subsystem-boundary nodes:
	// nodes whose TotalChunks >= subsysMinChunks.
	cb.subsystemTree = make([]*SubsystemNode, 0, 64)
	for _, n := range nodes {
		if n.TotalChunks >= subsysMinChunks {
			cb.subsystemTree = append(cb.subsystemTree, n)
		}
	}

	// Log all retained subsystems to subsystem.log
	if err := logSubsystemsToFile(cb.subsystemTree); err != nil {
		cb.debugLog.Log("Failed to write to subsystem.log: %v", err)
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
	// Return a wider candidate window than we ultimately keep. Noise
	// demotion runs on this full window (via filterNoiseSubsystems) and
	// re-sorts before final truncation — if we truncated to the final
	// top-K here, a legitimate subsystem with fewer total chunks (and
	// therefore a lower raw density-boosted score) than several noisy
	// variants of the same name (tests/unit/X, X itself, parent dirs)
	// would already be cut before demotion ever ran, making the demotion
	// a no-op. See subsysFinalK for the actual cap applied by the caller.
	const subsysCandidateWindow = 20

	if len(nodes) == 0 || len(queryTokens) == 0 {
		return nil
	}

	scores := make([]subsystemScore, 0, len(nodes))

	for _, n := range nodes {
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

// subsysFinalK is the number of subsystem candidates actually used for
// boosting, applied by the caller after filterNoiseSubsystems has
// re-ranked the wider candidate window.
const subsysFinalK = 5

// filterNoiseSubsystems demotes (not drops) subsystem detections whose
// path falls under a known noise prefix. detectQuerySubsystems scores
// purely on token/chunk-count match, so a heavily-tested package like
// nova/nova/tests/unit/scheduler can outscore the actual production
// nova/nova/scheduler subsystem simply because it has more chunks —
// which then drives boostByDetectedSubsystems toward test code instead
// of the subsystem the person actually asked about.
//
// cb.noisePaths is stored lowercased (see detectNoisePatterns); node.Path
// is stored in its original case, so we lowercase here rather than assume
// callers keep the two in sync.
func filterNoiseSubsystems(detected []subsystemScore, noisePaths []string) []subsystemScore {
	for i := range detected {
		pathLow := strings.ToLower(detected[i].node.Path)
		for _, np := range noisePaths {
			if strings.HasPrefix(pathLow, np) {
				detected[i].score *= 0.15 // heavy demotion, not exclusion —
				break                     // tests can still surface for debug/caller intents
			}
		}
	}
	sort.Slice(detected, func(i, j int) bool { return detected[i].score > detected[j].score })
	return detected
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
		// A node is noise only if EVERY segment is an explicit generic
		// term. Previously, segments under 5 characters skipped the
		// genericSegs check entirely and defaulted to "generic" —
		// which silently classified short top-level project names
		// (nova, heat) as noise just because they're short and dense.
		// That's backwards: project roots are exactly the paths that
		// SHOULD win subsystem detection, not lose to their own name.
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
