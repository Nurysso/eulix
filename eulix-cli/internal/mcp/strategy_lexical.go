// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"eulix/internal/checksum"
)

// ========== Lexical (grep-style) fallback ==========
//
// lexicalSearch is the last stage of the cascade. When semantic and anchored
// retrieval both fail to clear the quality bar, this walks the repository,
// scores files by weighted term frequency (anchors weigh 3x, other query
// terms weigh 1x), and renders the best line windows from the top files.
//
// The walk is bounded by maxLexScanFiles, maxLexFileBytes, and the caller's
// context. A timeout returns partial results; only cancellation aborts.

const (
	// maxLexFileBytes skips files larger than this; a code file bigger than
	// 1 MiB is almost never the useful match.
	maxLexFileBytes = 1 << 20

	// maxLexScanFiles caps how many files the walk inspects, so a huge
	// repository cannot make the fallback arbitrarily expensive.
	maxLexScanFiles = 20_000
)

// skipDirs are directory names that never contain useful source code. Hidden
// directories are skipped as well (see lexicalSearch).
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"__pycache__": true, "venv": true, "coverage": true,
}

// fileHit is one file that matched during the walk, with its raw score.
type fileHit struct {
	rel, abs string
	score    float64
}

// window is an inclusive line range rendered from a file.
type window struct{ start, end int }

// lexicalSearch ranks files by weighted term frequency and path matches, then
// renders the best line windows from the top files.
//
// Returns ("", nil) when there is nothing to search (empty root, no usable
// terms, no hits).
// This is the MCP-stage fallback because grepSymbolSearch is limited
// to in-memory indexed chunks and to queries that yield symbols; lexicalSearch
// hits the filesystem directly, covering stale/missing index entries and
// symbol-less queries.
func lexicalSearch(ctx context.Context, root, q string, anchors []Anchor, cfg StrategyConfig) (string, error) {
	if root == "" {
		return "", nil
	}

	// Weight terms: anchors weigh 3x, other query terms weigh 1x. Only terms
	// of length >= 3 are kept, so single/double-letter noise is ignored.
	weights := map[string]float64{}
	for _, a := range anchors {
		for _, k := range a.Keys {
			if len(k) >= 3 {
				weights[k] = 3
			}
		}
	}
	for _, t := range queryTerms(q) {
		if _, ok := weights[t]; !ok {
			weights[t] = 1
		}
	}
	if len(weights) == 0 {
		return "", nil
	}

	ignored := checksum.IgnoreMatcher(root)
	var hits []fileHit
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // unreadable entry: skip
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (skipDirs[name] || isHidden(name, d) || ignored(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		// Hidden files (.env, .npmrc, ...) often hold credentials, and files in
		// .euignore were left out on purpose: the fallback reads neither.
		if strings.HasPrefix(name, ".") || ignored(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 || info.Size() > maxLexFileBytes {
			return nil
		}
		if scanned++; scanned > maxLexScanFiles {
			return filepath.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			return nil
		}

		lower := strings.ToLower(string(data))
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		relLower := strings.ToLower(rel)

		var score float64
		for t, w := range weights {
			// Saturate the per-term count at 5 so one very repetitive file
			// cannot dominate the ranking.
			if c := strings.Count(lower, t); c > 0 {
				score += w * math.Min(float64(c), 5)
			}
			if strings.Contains(relLower, t) {
				score += 2 * w
			}
		}
		if score > 0 {
			hits = append(hits, fileHit{rel: rel, abs: path, score: score})
		}
		return nil
	})
	if errors.Is(err, context.Canceled) {
		return "", err // the caller went away; a deadline just means partial results
	}
	if len(hits) == 0 {
		return "", nil
	}

	sort.Slice(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		return hits[a].rel < hits[b].rel
	})
	if len(hits) > cfg.LexicalMaxFiles {
		hits = hits[:cfg.LexicalMaxFiles]
	}

	var b strings.Builder
outer:
	for _, h := range hits {
		data, err := os.ReadFile(h.abs)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, w := range bestWindows(lines, weights, 3, 3) {
			if b.Len() >= cfg.LexicalMaxBytes {
				break outer
			}
			fmt.Fprintf(&b, "### %s:%d-%d\n```\n", h.rel, w.start+1, w.end+1)
			for i := w.start; i <= w.end; i++ {
				b.WriteString(truncate(lines[i], 300))
				b.WriteByte('\n')
			}
			b.WriteString("```\n\n")
		}
	}
	return b.String(), nil
}

// isHidden covers both conventions: dot-prefixed names (Unix, and common
// on Windows for .env/.npmrc) and the NTFS hidden attribute.
func isHidden(name string, d fs.DirEntry) bool {
	return strings.HasPrefix(name, ".") || hiddenByAttr(d)
}

// bestWindows picks up to maxWin non-overlapping windows around the
// best-scoring lines. Each window covers ctxLines lines on either side of its
// centre. Windows are returned in ascending line order.
func bestWindows(lines []string, weights map[string]float64, maxWin, ctxLines int) []window {
	type scoredLine struct {
		idx   int
		score float64
	}
	var scored []scoredLine
	for i, ln := range lines {
		l := strings.ToLower(ln)
		var s float64
		for t, w := range weights {
			if strings.Contains(l, t) {
				s += w
			}
		}
		if s > 0 {
			scored = append(scored, scoredLine{i, s})
		}
	}
	sort.Slice(scored, func(a, b int) bool {
		if scored[a].score != scored[b].score {
			return scored[a].score > scored[b].score
		}
		return scored[a].idx < scored[b].idx
	})

	var wins []window
	for _, c := range scored {
		if len(wins) >= maxWin {
			break
		}
		w := window{start: max(0, c.idx-ctxLines), end: min(len(lines)-1, c.idx+ctxLines)}
		overlap := false
		for _, o := range wins {
			if w.start <= o.end && w.end >= o.start {
				overlap = true
				break
			}
		}
		if !overlap {
			wins = append(wins, w)
		}
	}
	sort.Slice(wins, func(a, b int) bool { return wins[a].start < wins[b].start })
	return wins
}

// isBinary reports whether data looks binary, by scanning the first 8 KiB for
// a NUL byte.
func isBinary(data []byte) bool {
	n := min(len(data), 8000)
	for _, c := range data[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

// truncate shortens s to at most n bytes, appending an ellipsis when it had
// to cut. The cut is byte-based, so it may split a UTF-8 sequence; for code
// windows this is acceptable.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
