//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package retrieval provides context window Creation for Eulix's RAG system.

 This file manages hydration of chunks and getting source code from repo
*/

package retrieval

import (
	"eulix/internal/query/strip"
	"eulix/internal/utils"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	prefetchMaxConcurrency = 8

	// Token estimation lives here so it can be calibrated in one place.
	charsPerToken = 4  // same len/4 estimator the rest of the pipeline uses
	fenceTokens   = 4  // "```lang\n" + "```"
	markerTokens  = 12 // one omission-marker line

	// Allocation.
	minGrantTokens   = 120  // a partial grant below this is not worth showing
	minGrantFraction = 0.35 // ...and neither is one covering less than this share of the chunk
	rankDecay        = 0.04 // weight = boost / (1 + rankDecay*rank); deliberately flat

	// Containers and oversized chunks render as a skeleton of this many cleaned lines.
	skeletonHeadLines = 40

	// true: hydrated source + leftover AST chunks are held to sourceBudget by evicting the
	// coldest unhydrated chunks. false: only the code budget is enforced (old behaviour).
	enforceContextCeiling = true
)

// anchorBoost returns a weight multiplier (>= 1) for chunks the retrieval stage pinned by anchor.
var anchorBoost = func(_ *Chunk) float64 { return 1 }

type fileEntry struct {
	once  sync.Once
	lines []string
}

// fileCache reads each file once. The map lock is only held to find the entry, so
// prefetch goroutines really do read different files in parallel.
type fileCache struct {
	mu    sync.Mutex
	files map[string]*fileEntry
}

func newFileCache() *fileCache {
	return &fileCache{files: make(map[string]*fileEntry)}
}

func (fc *fileCache) get(path string, debugLog *utils.DebugLogger) []string {
	fc.mu.Lock()
	e, ok := fc.files[path]
	if !ok {
		e = &fileEntry{}
		fc.files[path] = e
	}
	fc.mu.Unlock()

	e.once.Do(func() {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				debugLog.Log("Source file not found: %s", path)
			} else {
				debugLog.Log("Error reading %s: %v", path, err)
			}
			return
		}
		e.lines = strings.Split(string(data), "\n")
	})
	return e.lines
}

func (cb *ContextBuilder) prefetchFiles(chunks []Chunk, cache *fileCache) {
	seen := make(map[string]struct{}, len(chunks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, prefetchMaxConcurrency)
	for _, chunk := range chunks {
		sourceFile := filepath.Join(cb.sourceRoot, chunk.File)
		if _, ok := seen[sourceFile]; ok {
			continue
		}
		seen[sourceFile] = struct{}{}
		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer wg.Done()
			defer func() { <-sem }()
			cache.get(path, cb.debugLog)
		}(sourceFile)
	}
	wg.Wait()
}

// Hitem: one chunk plus everything needed to size and render its source
type hItem struct {
	idx       int
	chunk     Chunk
	lang      string
	weight    float64
	pinned    bool
	astTokens int // what the chunk costs today; freed if hydrated, kept if not

	skeleton bool     // container or oversized: header + elisions only
	lines    []string // cleaned candidate lines, in order
	cum      []int    // cum[k] = chars in the first k lines, newlines included
	tail     bool     // source continues past lines[]

	need      int // tokens to render every candidate line
	minUseful int // smallest grant worth spending
	grant     int
	keep      int // lines kept after fitting the grant
	used      int // tokens actually rendered

	skip   bool
	reason string
}

func estimateTokens(chars int) int {
	return (chars + charsPerToken - 1) / charsPerToken
}

func astTokensOf(c Chunk) int {
	if c.Tokens > 0 {
		return c.Tokens
	}
	return estimateTokens(len(c.Content))
}

// cost is the token cost of rendering the first k candidate lines, fence and marker included.
func (it *hItem) cost(k int) int {
	if k <= 0 {
		return 0
	}
	t := estimateTokens(it.cum[k]-1) + fenceTokens
	if k < len(it.lines) || it.tail {
		t += markerTokens
	}
	return t
}

// fit returns how many lines fit into grant tokens. Prefix sums make this O(log n)
// with no re-stripping or re-joining.
func (it *hItem) fit(grant int) int {
	n := len(it.lines)
	if it.cost(n) <= grant {
		return n
	}
	// cost(k) is monotonic for k < n; the predicate is known true at i = n-1.
	return sort.Search(n, func(i int) bool { return it.cost(i+1) > grant })
}

func (it *hItem) render() string {
	cm := commentPrefix(it.lang)
	var b strings.Builder
	b.Grow(it.cum[it.keep] + 64)
	b.WriteString("```")
	b.WriteString(it.lang)
	b.WriteByte('\n')
	for _, l := range it.lines[:it.keep] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	switch {
	case it.keep < len(it.lines):
		fmt.Fprintf(&b, "%s ... %d more lines omitted\n", cm, len(it.lines)-it.keep)
	case it.tail:
		fmt.Fprintf(&b, "%s ... rest of body omitted\n", cm)
	}
	b.WriteString("```")
	return b.String()
}

func span(c Chunk) int { return c.EndLine - c.StartLine }

// directChildren maps each chunk to the chunks whose innermost enclosing chunk it is.
func directChildren(chunks []Chunk) [][]int {
	kids := make([][]int, len(chunks))
	for j := range chunks {
		best := -1
		for i := range chunks {
			if i == j || !isSourceRangeContained(&chunks[i], &chunks[j]) {
				continue
			}
			if best < 0 || span(chunks[i]) < span(chunks[best]) {
				best = i
			}
		}
		if best >= 0 {
			kids[best] = append(kids[best], j)
		}
	}
	return kids
}

// childRanges returns the children's line ranges clamped to [lo, hi], sorted and merged.
func childRanges(chunks []Chunk, kidIdx []int, lo, hi int) [][2]int {
	if len(kidIdx) == 0 {
		return nil
	}
	rs := make([][2]int, 0, len(kidIdx))
	for _, k := range kidIdx {
		s, e := chunks[k].StartLine, chunks[k].EndLine
		if s < lo {
			s = lo
		}
		if e > hi {
			e = hi
		}
		if s <= e {
			rs = append(rs, [2]int{s, e})
		}
	}
	sort.Slice(rs, func(a, b int) bool { return rs[a][0] < rs[b][0] })
	merged := make([][2]int, 0, len(rs))
	for _, r := range rs {
		if n := len(merged); n > 0 && r[0] <= merged[n-1][1]+1 {
			if r[1] > merged[n-1][1] {
				merged[n-1][1] = r[1]
			}
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// stripSegment cleans raw lines [from, to] (1-based, inclusive).
func stripSegment(src []string, from, to int, lang string) []string {
	if from > to {
		return nil
	}
	return strip.StripCommentsAndDocs(src[from-1:to], lang)
}

// buildSkeleton renders the first skeletonHeadLines cleaned lines of [start, end], replacing
// each child range with a one-line pointer. tail reports whether source was left out.
func buildSkeleton(src []string, start, end int, kids [][2]int, lang string) (out []string, tail bool) {
	cm := commentPrefix(lang)
	out = make([]string, 0, skeletonHeadLines+4)
	cursor := start

	// Scan at most 4x the head size per gap so a 1,600-line class body costs almost nothing.
	take := func(from, to int) {
		if limit := from + skeletonHeadLines*4 - 1; to > limit {
			to = limit
		}
		if from <= to {
			out = append(out, stripSegment(src, from, to, lang)...)
			cursor = to + 1
		}
	}

	for _, kid := range kids {
		if len(out) >= skeletonHeadLines {
			break
		}
		if kid[0] > cursor {
			take(cursor, kid[0]-1)
			if cursor < kid[0] { // gap was cut short; the child lies beyond our window
				break
			}
		}
		out = append(out, fmt.Sprintf("%s ... lines %d-%d omitted (separate chunk)", cm, kid[0], kid[1]))
		cursor = kid[1] + 1
	}
	if len(out) < skeletonHeadLines && cursor <= end {
		take(cursor, end)
	}
	if len(out) > skeletonHeadLines {
		out = out[:skeletonHeadLines]
		tail = true
	}
	return out, tail || cursor <= end
}

func (cb *ContextBuilder) loadItem(it *hItem, cache *fileCache, chunks []Chunk, kidIdx []int, maxLines int) {
	c := it.chunk
	src := cache.get(filepath.Join(cb.sourceRoot, c.File), cb.debugLog)
	if src == nil {
		it.skip, it.reason = true, "source unavailable"
		return
	}
	end := c.EndLine
	if end > len(src) {
		end = len(src)
	}
	if c.StartLine < 1 || c.StartLine > len(src) || end < c.StartLine {
		it.skip, it.reason = true, fmt.Sprintf("invalid range (file has %d lines)", len(src))
		return
	}

	kids := childRanges(chunks, kidIdx, c.StartLine, end)
	if len(kids) > 0 || end-c.StartLine+1 > maxLines {
		it.skeleton = true
		it.lines, it.tail = buildSkeleton(src, c.StartLine, end, kids, it.lang)
	} else {
		it.lines = stripSegment(src, c.StartLine, end, it.lang)
	}
	if len(it.lines) == 0 {
		it.skip, it.reason = true, "empty after cleaning"
		return
	}

	it.cum = make([]int, len(it.lines)+1)
	for i, l := range it.lines {
		it.cum[i+1] = it.cum[i] + len(l) + 1
	}
	it.need = it.cost(len(it.lines))
	it.minUseful = int(float64(it.need) * minGrantFraction)
	if it.minUseful < minGrantTokens {
		it.minUseful = minGrantTokens
	}
	if it.minUseful > it.need {
		it.minUseful = it.need
	}
}

type rangeKey struct {
	file       string
	start, end int
}

func (cb *ContextBuilder) buildItems(chunks []Chunk, cache *fileCache, maxLines int) []*hItem {
	kids := directChildren(chunks)
	seen := make(map[rangeKey]struct{}, len(chunks))
	items := make([]*hItem, len(chunks))

	for i, c := range chunks {
		it := &hItem{
			idx:       i,
			chunk:     c,
			lang:      detectLanguage(c.File),
			astTokens: astTokensOf(c),
			weight:    1 / (1 + rankDecay*float64(i)),
		}
		if b := anchorBoost(&it.chunk); b > 0 {
			it.weight *= b
			it.pinned = b > 1
		}
		items[i] = it

		key := rangeKey{c.File, c.StartLine, c.EndLine}
		if _, dup := seen[key]; dup {
			it.skip, it.reason = true, "duplicate range"
			continue
		}
		seen[key] = struct{}{}
		cb.loadItem(it, cache, chunks, kids[i], maxLines)
	}
	return items
}

func removeItem(list []*hItem, victim *hItem) []*hItem {
	out := make([]*hItem, 0, len(list)-1)
	for _, it := range list {
		if it != victim {
			out = append(out, it)
		}
	}
	return out
}

// allocateGrants distributes pool tokens among hydratable items proportional to their weight.
//
// In each round, the algorithm evaluates remaining items and applies the first matching rule:
//  1. Refill: If an item's weighted share meets or exceeds its maximum need, it receives
//     its full need. The remaining surplus returns to the pool, and shares are recomputed.
//  2. Drop: If no item is fully covered, the lowest-weight item whose weighted share falls
//     below its minimum useful threshold is dropped (remaining AST), and shares are recomputed.
//  3. Settle: Otherwise, all remaining items receive floor(share) as a partial grant.
//
// Guaranteed to terminate because every iteration either settles or drops at least one item.
func allocateGrants(items []*hItem, pool int) {
	active := make([]*hItem, 0, len(items))
	for _, it := range items {
		if !it.skip {
			active = append(active, it)
		}
	}
	remaining := pool

	for len(active) > 0 && remaining > 0 {
		sumW := 0.0
		for _, it := range active {
			sumW += it.weight
		}
		share := func(it *hItem) float64 { return float64(remaining) * it.weight / sumW }

		// Decide against the current pool first, mutate afterwards, so one item's grant
		// cannot change another's share within the same round.
		var satisfied, rest []*hItem
		for _, it := range active {
			if share(it) >= float64(it.need) {
				satisfied = append(satisfied, it)
			} else {
				rest = append(rest, it)
			}
		}
		if len(satisfied) > 0 {
			for _, it := range satisfied {
				it.grant = it.need
				remaining -= it.need
			}
			active = rest
			continue
		}

		var victim *hItem
		for _, it := range active {
			if share(it) < float64(it.minUseful) && (victim == nil || it.weight < victim.weight) {
				victim = it
			}
		}
		if victim != nil {
			victim.skip, victim.reason = true, "fair share below minimum grant"
			active = removeItem(active, victim)
			continue
		}

		for _, it := range active {
			it.grant = int(share(it))
		}
		active = nil
	}

	for _, it := range active { // pool ran dry before these were reached
		it.skip, it.reason = true, "budget exhausted"
	}
}

// fitAndTopUp turns grants into whole lines, then spends whatever rounding and line
// granularity left over on truncated chunks, best weight first. Returns tokens used.
func fitAndTopUp(items []*hItem, codeBudget int) int {
	used := 0
	for _, it := range items {
		if it.skip {
			continue
		}
		it.keep = it.fit(it.grant)
		if it.keep == 0 {
			it.skip, it.reason = true, "grant smaller than one line"
			continue
		}
		it.used = it.cost(it.keep)
		used += it.used
	}

	order := make([]*hItem, 0, len(items))
	for _, it := range items {
		if !it.skip && it.keep < len(it.lines) {
			order = append(order, it)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return order[a].weight > order[b].weight })
	for _, it := range order {
		left := codeBudget - used
		if left <= 0 {
			break
		}
		k := it.fit(it.used + left)
		if k <= it.keep {
			continue
		}
		c := it.cost(k)
		used += c - it.used
		it.keep, it.used = k, c
	}
	return used
}

// enforceCeiling evicts the coldest unhydrated chunks until hydrated source plus the
// remaining AST chunks fit in ceiling. Pinned chunks are never evicted.
func (cb *ContextBuilder) enforceCeiling(items []*hItem, ceiling, usedCode int) (map[int]bool, int) {
	evicted := make(map[int]bool)
	total := usedCode
	var cold []*hItem
	for _, it := range items {
		if it.skip {
			total += it.astTokens
			if !it.pinned {
				cold = append(cold, it)
			}
		}
	}
	if !enforceContextCeiling || total <= ceiling {
		return evicted, total
	}
	sort.SliceStable(cold, func(a, b int) bool { return cold[a].weight < cold[b].weight })
	for _, it := range cold {
		if total <= ceiling {
			break
		}
		total -= it.astTokens
		evicted[it.idx] = true
		cb.debugLog.Log("Chunk %d (%s:%d-%d): evicted, -%d AST tokens (%s)",
			it.idx, it.chunk.File, it.chunk.StartLine, it.chunk.EndLine, it.astTokens, "over context ceiling")
	}
	if total > ceiling {
		cb.debugLog.Log("WARNING: context still %d tokens over ceiling after eviction (pinned or hydrated content)", total-ceiling)
	}
	return evicted, total
}

// hydrateSourceCode swaps AST content for real source where the budget allows.
// maxLinesDefault is the largest raw span rendered in full; bigger chunks become skeletons.
// The returned slice keeps input order and may be shorter if cold chunks were evicted.
func (cb *ContextBuilder) hydrateSourceCode(
	chunks []Chunk,
	sourceBudget int,
	maxLinesDefault int,
) []Chunk {
	if cb.sourceRoot == "" {
		cb.debugLog.Log("Source hydration skipped: no source root configured")
		return chunks
	}
	ratio := cb.config.RetrievalConfig.CodeToAstRatio
	codeBudget := int(float64(sourceBudget) * ratio)
	if enforceContextCeiling && codeBudget > sourceBudget {
		codeBudget = sourceBudget
	}
	maxLines := maxLinesDefault
	if maxLines < 10 {
		maxLines = 10
	}

	cb.debugLog.Log("=== SOURCE HYDRATION START ===")
	cb.debugLog.Log("Budget: %d tokens (code budget: %d, ratio %.2f) | full-hydration span: %d lines | Chunks: %d",
		sourceBudget, codeBudget, ratio, maxLines, len(chunks))
	if codeBudget <= 0 || len(chunks) == 0 {
		cb.debugLog.Log("Source hydration skipped: zero code budget or no chunks")
		return chunks
	}

	cache := newFileCache()
	cb.prefetchFiles(chunks, cache)

	items := cb.buildItems(chunks, cache, maxLines)

	demand, hydratable := 0, 0
	for _, it := range items {
		if !it.skip {
			demand += it.need
			hydratable++
		}
	}
	cb.debugLog.Log("Demand: %d tokens across %d hydratable chunks vs code budget %d", demand, hydratable, codeBudget)

	allocateGrants(items, codeBudget)
	usedCode := fitAndTopUp(items, codeBudget)
	evicted, total := cb.enforceCeiling(items, sourceBudget, usedCode)

	result := make([]Chunk, 0, len(chunks))
	hydrated := 0
	for _, it := range items {
		c := it.chunk
		id := fmt.Sprintf("Chunk %d (%s:%d-%d)", it.idx, c.File, c.StartLine, c.EndLine)
		switch {
		case evicted[it.idx]:
			continue
		case it.skip:
			cb.debugLog.Log("%s: kept as AST, %d tokens (%s)", id, it.astTokens, it.reason)
		default:
			mode := "full"
			switch {
			case it.skeleton:
				mode = "skeleton"
			case it.keep < len(it.lines):
				mode = "partial"
			}
			cb.debugLog.Log("%s: %s %d/%d lines, %d tokens (need %d, grant %d, weight %.2f, replaces %d AST)",
				id, mode, it.keep, len(it.lines), it.used, it.need, it.grant, it.weight, it.astTokens)
			c.Content = it.render()
			c.Tokens = it.used
			hydrated++
		}
		result = append(result, c)
	}

	cb.debugLog.Log("=== SOURCE HYDRATION COMPLETE ===")
	cb.debugLog.Log("Hydrated: %d/%d chunks | Code: %d/%d tokens | Evicted: %d | Context: %d/%d tokens",
		hydrated, len(chunks), usedCode, codeBudget, len(evicted), total, sourceBudget)
	return result
}

// isSourceRangeContained reports whether outer strictly contains inner in the same file.
func isSourceRangeContained(outer, inner *Chunk) bool {
	if outer == nil || inner == nil {
		return false
	}
	if outer.File != inner.File {
		return false
	}
	if outer.StartLine > inner.StartLine {
		return false
	}
	if outer.EndLine < inner.EndLine {
		return false
	}
	if outer.StartLine == inner.StartLine &&
		outer.EndLine == inner.EndLine {
		return false
	}
	return true
}

func commentPrefix(lang string) string {
	if lang == "python" {
		return "#"
	}
	return "//"
}

func detectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".js", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".java":
		return "java"
	default:
		return "unknown"
	}
}
