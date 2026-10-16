// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
)

// ========== helpers ==========

func newDebugLog(t *testing.T) *utils.DebugLogger {
	t.Helper()
	// Use an in-memory logger; tests don't want to touch the filesystem.
	return utils.NewDebugLogger()
}

func makeChunk(file string, start, end int, opts ...func(*Chunk)) Chunk {
	c := Chunk{File: file, StartLine: start, EndLine: end}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func withType(ct string) func(*Chunk) {
	return func(c *Chunk) { c.ChunkType = ct }
}

func withTokens(n int) func(*Chunk) {
	return func(c *Chunk) { c.Tokens = n }
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// ========== estimateTokens ==========

func TestEstimateTokens_Basic(t *testing.T) {
	cases := []struct {
		chars int
		want  int
	}{
		{0, 0},
		{1, 1},
		{4, 1},
		{5, 2},
		{8, 2},
		{100, 25},
		{101, 26},
	}
	for _, tc := range cases {
		if got := estimateTokens(tc.chars); got != tc.want {
			t.Errorf("estimateTokens(%d) = %d, want %d", tc.chars, got, tc.want)
		}
	}
}

func TestEstimateTokens_LargeInput(t *testing.T) {
	const chars = 1_000_000
	got := estimateTokens(chars)
	if got != 250_000 {
		t.Errorf("estimateTokens(%d) = %d, want 250000", chars, got)
	}
}

// ========== astTokensOf ==========

func TestAstTokensOf_UsesFieldWhenSet(t *testing.T) {
	c := Chunk{Tokens: 42, Content: strings.Repeat("x", 80)}
	if got := astTokensOf(c); got != 42 {
		t.Errorf("want 42, got %d", got)
	}
}

func TestAstTokensOf_FallsBackToContent(t *testing.T) {
	c := Chunk{Tokens: 0, Content: strings.Repeat("x", 8)}
	if got := astTokensOf(c); got != 2 {
		t.Errorf("want 2, got %d", got)
	}
}

func TestAstTokensOf_EmptyContent(t *testing.T) {
	c := Chunk{Tokens: 0, Content: ""}
	if got := astTokensOf(c); got != 0 {
		t.Errorf("want 0, got %d", got)
	}
}

// ========== detectLanguage ==========

func TestDetectLanguage(t *testing.T) {
	cases := []struct{ path, want string }{
		{"main.py", "python"},
		{"app.go", "go"},
		{"index.js", "javascript"},
		{"page.jsx", "javascript"},
		{"module.ts", "typescript"},
		{"component.tsx", "typescript"},
		{"lib.rs", "rust"},
		{"util.c", "c"},
		{"header.h", "c"},
		{"engine.cpp", "cpp"},
		{"engine.cc", "cpp"},
		{"engine.cxx", "cpp"},
		{"engine.hpp", "cpp"},
		{"Service.java", "java"},
		{"plugin.php", "php"},
		{"template.phtml", "php"},
		{"include.inc", "php"},
		{"data.json", "unknown"},
		{"README.md", "unknown"},
		{"noext", "unknown"},
	}
	for _, tc := range cases {
		if got := detectLanguage(tc.path); got != tc.want {
			t.Errorf("detectLanguage(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestDetectLanguage_CaseInsensitiveExt(t *testing.T) {
	if got := detectLanguage("MAIN.PY"); got != "python" {
		t.Errorf("want python, got %q", got)
	}
}

// ========== commentPrefix ==========

func TestCommentPrefix(t *testing.T) {
	if got := commentPrefix("python"); got != "#" {
		t.Errorf("python: want #, got %q", got)
	}
	for _, lang := range []string{"go", "javascript", "typescript", "rust", "java", "unknown"} {
		if got := commentPrefix(lang); got != "//" {
			t.Errorf("%s: want //, got %q", lang, got)
		}
	}
}

// ========== isSourceRangeContained ==========

func TestIsSourceRangeContained(t *testing.T) {
	outer := &Chunk{File: "a.go", StartLine: 1, EndLine: 100}
	inner := &Chunk{File: "a.go", StartLine: 10, EndLine: 50}

	if !isSourceRangeContained(outer, inner) {
		t.Error("inner should be contained in outer")
	}
	if isSourceRangeContained(inner, outer) {
		t.Error("outer should not be contained in inner")
	}
}

func TestIsSourceRangeContained_SameRange(t *testing.T) {
	c := &Chunk{File: "a.go", StartLine: 1, EndLine: 10}
	if isSourceRangeContained(c, c) {
		t.Error("identical ranges should not be considered contained")
	}
}

func TestIsSourceRangeContained_DifferentFiles(t *testing.T) {
	a := &Chunk{File: "a.go", StartLine: 1, EndLine: 100}
	b := &Chunk{File: "b.go", StartLine: 1, EndLine: 50}
	if isSourceRangeContained(a, b) {
		t.Error("different files should not be considered contained")
	}
}

func TestIsSourceRangeContained_NilInputs(t *testing.T) {
	c := &Chunk{File: "a.go", StartLine: 1, EndLine: 10}
	if isSourceRangeContained(nil, c) {
		t.Error("nil outer should return false")
	}
	if isSourceRangeContained(c, nil) {
		t.Error("nil inner should return false")
	}
}

func TestIsSourceRangeContained_EdgeBoundaries(t *testing.T) {
	outer := &Chunk{File: "a.go", StartLine: 5, EndLine: 20}
	inner := &Chunk{File: "a.go", StartLine: 5, EndLine: 19}
	if !isSourceRangeContained(outer, inner) {
		t.Error("inner sharing start but shorter should be contained")
	}
}

// ========== span ==========

func TestSpan(t *testing.T) {
	c := Chunk{StartLine: 10, EndLine: 30}
	if got := span(c); got != 20 {
		t.Errorf("span = %d, want 20", got)
	}
}

// ========== shouldSkeleton ==========

func TestShouldSkeleton_WithKids(t *testing.T) {
	c := makeChunk("a.go", 1, 200, withType("class"))
	if !shouldSkeleton(c, 2, 200, 50) {
		t.Error("chunk with kids should always skeleton")
	}
}

func TestShouldSkeleton_ShortSpan(t *testing.T) {
	c := makeChunk("a.go", 1, 30, withType("class"))
	if shouldSkeleton(c, 0, 30, 50) {
		t.Error("short span with no kids should not skeleton")
	}
}

func TestShouldSkeleton_FunctionNeverSkeletons(t *testing.T) {
	for _, ct := range []string{"function", "Function", "FUNCTION", "method", "Method"} {
		c := makeChunk("a.go", 1, 300, withType(ct))
		if shouldSkeleton(c, 0, 300, 50) {
			t.Errorf("chunk type %q should not skeleton even when large", ct)
		}
	}
}

func TestShouldSkeleton_LargeNonFunction(t *testing.T) {
	c := makeChunk("a.go", 1, 200, withType("class"))
	if !shouldSkeleton(c, 0, 200, 50) {
		t.Error("large non-function should skeleton")
	}
}

// ========== hItem cost / fit ==========

func makeHItem(lines []string) *hItem {
	cum := make([]int, len(lines)+1)
	for i, l := range lines {
		cum[i+1] = cum[i] + len(l) + 1
	}
	return &hItem{lines: lines, cum: cum, lang: "go"}
}

func TestHItemCost_Zero(t *testing.T) {
	it := makeHItem([]string{"line one", "line two"})
	if got := it.cost(0); got != 0 {
		t.Errorf("cost(0) = %d, want 0", got)
	}
}

func TestHItemCost_Full(t *testing.T) {
	lines := []string{"hello", "world"}
	it := makeHItem(lines)
	c := it.cost(len(lines))
	if c <= 0 {
		t.Errorf("cost for full content should be positive, got %d", c)
	}
}

func TestHItemCost_IncludesMarker_WhenTruncated(t *testing.T) {
	lines := []string{"a", "b", "c"}
	it := makeHItem(lines)
	partial := it.cost(2)
	it2 := makeHItem(lines)
	it2.tail = false
	full := it2.cost(3)
	if partial <= full-markerTokens {
		t.Errorf("partial cost should include marker overhead")
	}
}

func TestHItemFit_FullGrant(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d content here", i)
	}
	it := makeHItem(lines)
	it.need = it.cost(len(lines))
	if got := it.fit(it.need + 1000); got != len(lines) {
		t.Errorf("fit with large grant should return all %d lines, got %d", len(lines), got)
	}
}

func TestHItemFit_TinyGrant(t *testing.T) {
	lines := []string{"short line one", "short line two", "short line three"}
	it := makeHItem(lines)
	if got := it.fit(1); got != 0 {
		t.Errorf("fit(1) should return 0, got %d", got)
	}
}

// ========== hItem render ==========

func TestHItemRender_FullContent(t *testing.T) {
	lines := []string{"func foo() {", "    return 42", "}"}
	it := makeHItem(lines)
	it.keep = len(lines)
	out := it.render()
	if !strings.HasPrefix(out, "```go\n") {
		t.Error("render should start with fenced code block")
	}
	if !strings.HasSuffix(out, "```") {
		t.Error("render should end with closing fence")
	}
	for _, l := range lines {
		if !strings.Contains(out, l) {
			t.Errorf("render missing line: %q", l)
		}
	}
}

func TestHItemRender_Truncated(t *testing.T) {
	lines := []string{"line1", "line2", "line3", "line4", "line5"}
	it := makeHItem(lines)
	it.keep = 2
	out := it.render()
	if !strings.Contains(out, "omitted") {
		t.Error("truncated render should mention omitted lines")
	}
	if strings.Contains(out, "line3") {
		t.Error("truncated render must not include lines past keep")
	}
}

func TestHItemRender_TailMarker(t *testing.T) {
	lines := []string{"line1", "line2"}
	it := makeHItem(lines)
	it.keep = len(lines)
	it.tail = true
	out := it.render()
	if !strings.Contains(out, "rest of body omitted") {
		t.Errorf("tail render should include body-omitted marker, got:\n%s", out)
	}
}

func TestHItemRender_PythonLang(t *testing.T) {
	lines := []string{"def foo():", "    pass", "extra"}
	it := makeHItem(lines)
	it.lang = "python"
	it.keep = 2
	out := it.render()
	if !strings.Contains(out, "# ...") {
		t.Errorf("python render should use # comment prefix for omission marker, got:\n%s", out)
	}
}

// ========== directChildren ==========

func TestDirectChildren_NoNesting(t *testing.T) {
	chunks := []Chunk{
		makeChunk("a.go", 1, 10),
		makeChunk("a.go", 20, 30),
	}
	kids := directChildren(chunks)
	for i, k := range kids {
		if len(k) != 0 {
			t.Errorf("chunk %d should have no children, got %v", i, k)
		}
	}
}

func TestDirectChildren_SingleNesting(t *testing.T) {
	chunks := []Chunk{
		makeChunk("a.go", 1, 100),
		makeChunk("a.go", 10, 50),
	}
	kids := directChildren(chunks)
	if len(kids[0]) != 1 || kids[0][0] != 1 {
		t.Errorf("chunk 0 should have chunk 1 as child, got %v", kids[0])
	}
	if len(kids[1]) != 0 {
		t.Errorf("chunk 1 should have no children, got %v", kids[1])
	}
}

func TestDirectChildren_DifferentFiles(t *testing.T) {
	chunks := []Chunk{
		makeChunk("a.go", 1, 100),
		makeChunk("b.go", 5, 50),
	}
	kids := directChildren(chunks)
	for i, k := range kids {
		if len(k) != 0 {
			t.Errorf("chunk %d: cross-file containment should not occur, got %v", i, k)
		}
	}
}

func TestDirectChildren_PicksClosestParent(t *testing.T) {
	chunks := []Chunk{
		makeChunk("a.go", 1, 100), // grandparent
		makeChunk("a.go", 10, 80), // parent
		makeChunk("a.go", 20, 50), // child
	}
	kids := directChildren(chunks)
	if len(kids[1]) != 1 || kids[1][0] != 2 {
		t.Errorf("parent (idx 1) should own child (idx 2), got %v", kids[1])
	}
	for _, k := range kids[0] {
		if k == 2 {
			t.Error("grandparent should not directly own grandchild")
		}
	}
}

// ========== childRanges ==========

func TestChildRanges_Empty(t *testing.T) {
	chunks := []Chunk{makeChunk("a.go", 1, 100)}
	rs := childRanges(chunks, nil, 1, 100)
	if len(rs) != 0 {
		t.Errorf("no kids → empty ranges, got %v", rs)
	}
}

func TestChildRanges_MergesOverlapping(t *testing.T) {
	chunks := []Chunk{
		makeChunk("a.go", 1, 100),
		makeChunk("a.go", 10, 30),
		makeChunk("a.go", 25, 50),
	}
	rs := childRanges(chunks, []int{1, 2}, 1, 100)
	if len(rs) != 1 {
		t.Errorf("overlapping ranges should merge into 1, got %v", rs)
	}
	if rs[0][0] != 10 || rs[0][1] != 50 {
		t.Errorf("merged range wrong: %v", rs[0])
	}
}

func TestChildRanges_ClampsToBounds(t *testing.T) {
	chunks := []Chunk{
		makeChunk("a.go", 1, 100),
		makeChunk("a.go", 0, 200),
	}
	rs := childRanges(chunks, []int{1}, 10, 90)
	if len(rs) == 0 {
		t.Fatal("expected at least one range")
	}
	if rs[0][0] < 10 || rs[0][1] > 90 {
		t.Errorf("range should be clamped to [10,90], got %v", rs[0])
	}
}

// ========== fileCache ==========

func TestFileCache_MissingFile(t *testing.T) {
	fc := newFileCache()
	dl := newDebugLog(t)
	lines := fc.get("/nonexistent/path/file.go", dl)
	if lines != nil {
		t.Errorf("missing file should return nil, got %v", lines)
	}
}

func TestFileCache_ReadsExistingFile(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "sample.go", "line1\nline2\nline3")
	fc := newFileCache()
	dl := newDebugLog(t)
	lines := fc.get(filepath.Join(dir, "sample.go"), dl)
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "line1" {
		t.Errorf("first line should be 'line1', got %q", lines[0])
	}
}

func TestFileCache_CachesResult(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "cached.go", "a\nb\nc")
	fc := newFileCache()
	dl := newDebugLog(t)
	first := fc.get(path, dl)
	second := fc.get(path, dl)
	if &first[0] != &second[0] {
		t.Error("second call should return the same slice (cached)")
	}
}

func TestFileCache_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "concurrent.go", strings.Repeat("line\n", 50))
	fc := newFileCache()
	dl := newDebugLog(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fc.get(path, dl)
		}()
	}
	wg.Wait()
}

// ========== allocateGrants ==========

func TestAllocateGrants_SufficientBudget(t *testing.T) {
	items := []*hItem{
		{need: 100, minUseful: 50, weight: 1, lines: []string{"x"}},
		{need: 200, minUseful: 80, weight: 1, lines: []string{"x"}},
	}
	allocateGrants(items, 1000)
	for i, it := range items {
		if it.skip {
			t.Errorf("item %d should not be skipped with ample budget", i)
		}
		if it.grant < it.need {
			t.Errorf("item %d grant %d < need %d", i, it.grant, it.need)
		}
	}
}

func TestAllocateGrants_ZeroBudget(t *testing.T) {
	items := []*hItem{
		{need: 100, minUseful: 50, weight: 1, lines: []string{"x"}},
	}
	allocateGrants(items, 0)
	if !items[0].skip {
		t.Error("item should be skipped when budget is 0")
	}
}

func TestAllocateGrants_SkipsPreSkipped(t *testing.T) {
	items := []*hItem{
		{need: 50, minUseful: 20, weight: 1, skip: true, lines: []string{"x"}},
		{need: 50, minUseful: 20, weight: 1, lines: []string{"x"}},
	}
	allocateGrants(items, 200)
	if items[0].grant != 0 {
		t.Error("pre-skipped item should not receive a grant")
	}
}

func TestAllocateGrants_DropsLowWeightWhenBelowMinimum(t *testing.T) {
	items := []*hItem{
		{need: 1000, minUseful: 900, weight: 0.01, lines: []string{"x"}},
		{need: 100, minUseful: 50, weight: 1.0, lines: []string{"x"}},
	}
	allocateGrants(items, 110)
	if !items[0].skip {
		t.Error("low-weight item whose share is below minUseful should be dropped")
	}
	if items[1].skip {
		t.Error("high-weight item should survive")
	}
}

func TestAllocateGrants_WeightedDistribution(t *testing.T) {
	items := []*hItem{
		{need: 10000, minUseful: 1, weight: 3, lines: []string{"x"}},
		{need: 10000, minUseful: 1, weight: 1, lines: []string{"x"}},
	}
	allocateGrants(items, 400)
	if items[0].grant <= items[1].grant {
		t.Errorf("heavier item should get more tokens: %d vs %d", items[0].grant, items[1].grant)
	}
}

// ========== fitAndTopUp ==========

func TestFitAndTopUp_BasicFit(t *testing.T) {
	lines := []string{"line one long enough", "line two long enough"}
	it := makeHItem(lines)
	it.need = it.cost(len(lines))
	it.grant = it.need
	it.weight = 1

	used := fitAndTopUp([]*hItem{it}, it.need+1000)
	if it.skip {
		t.Errorf("item should not be skipped; reason: %s", it.reason)
	}
	if it.keep != len(lines) {
		t.Errorf("keep = %d, want %d", it.keep, len(lines))
	}
	if used <= 0 {
		t.Errorf("used should be positive, got %d", used)
	}
}

func TestFitAndTopUp_SkipsWhenGrantTooSmall(t *testing.T) {
	lines := []string{"a very long line that costs many tokens"}
	it := makeHItem(lines)
	it.grant = 0
	it.weight = 1
	fitAndTopUp([]*hItem{it}, 10000)
	if !it.skip {
		t.Error("item with grant=0 should be skipped")
	}
}

func TestFitAndTopUp_TopUpUsesSlack(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = fmt.Sprintf("content line %d with padding", i)
	}
	it := makeHItem(lines)
	it.need = it.cost(len(lines))
	it.grant = it.cost(5)
	it.weight = 1

	budget := it.need + 500
	fitAndTopUp([]*hItem{it}, budget)
	if it.keep <= 5 {
		t.Errorf("top-up should expand keep beyond initial grant fit; got keep=%d", it.keep)
	}
}

// ========== buildSkeleton ==========

func TestBuildSkeleton_NoKids(t *testing.T) {
	src := make([]string, 60)
	for i := range src {
		src[i] = fmt.Sprintf("line %d", i+1)
	}
	out, tail := buildSkeleton(src, 1, 60, nil, "go")
	if len(out) == 0 {
		t.Error("skeleton output should not be empty")
	}
	_ = tail
}

func TestBuildSkeleton_WithKids(t *testing.T) {
	src := make([]string, 100)
	for i := range src {
		src[i] = fmt.Sprintf("// line %d", i+1)
	}
	kids := [][2]int{{20, 50}, {60, 80}}
	out, _ := buildSkeleton(src, 1, 100, kids, "go")
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "omitted") {
		t.Error("skeleton with kids should note omitted child ranges")
	}
}

func TestBuildSkeleton_TailWhenLong(t *testing.T) {
	src := make([]string, 200)
	for i := range src {
		src[i] = fmt.Sprintf("line %d padding content here", i+1)
	}
	out, tail := buildSkeleton(src, 1, 200, nil, "go")
	if len(out) > skeletonHeadLines {
		t.Errorf("output exceeds skeletonHeadLines: %d", len(out))
	}
	_ = tail
}

// ========== stripSegment ==========

func TestStripSegment_ReturnsNilForInvertedRange(t *testing.T) {
	src := []string{"a", "b", "c"}
	out := stripSegment(src, 5, 3, "go")
	if out != nil {
		t.Errorf("inverted range should return nil, got %v", out)
	}
}

func TestStripSegment_ValidRange(t *testing.T) {
	src := []string{"line1", "line2", "line3", "line4"}
	out := stripSegment(src, 2, 3, "go")
	if len(out) == 0 {
		t.Error("valid segment should not be empty")
	}
}

// ========== removeItem ==========

func TestRemoveItem_RemovesTarget(t *testing.T) {
	a := &hItem{idx: 0}
	b := &hItem{idx: 1}
	c := &hItem{idx: 2}
	list := []*hItem{a, b, c}
	result := removeItem(list, b)
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}
	for _, it := range result {
		if it == b {
			t.Error("removed item should not appear in result")
		}
	}
}

func TestRemoveItem_AbsentTarget(t *testing.T) {
	a := &hItem{idx: 0}
	b := &hItem{idx: 1}
	ghost := &hItem{idx: 99}
	result := removeItem([]*hItem{a, b}, ghost)
	if len(result) != 2 {
		t.Errorf("removing absent item should leave list unchanged, got len=%d", len(result))
	}
}

func TestRemoveItem_SingleElement(t *testing.T) {
	a := &hItem{idx: 0}
	result := removeItem([]*hItem{a}, a)
	if len(result) != 0 {
		t.Errorf("removing only element should yield empty list, got %v", result)
	}
}

func TestShouldSkeleton(t *testing.T) {
	fn := Chunk{ChunkType: "function"}
	method := Chunk{ChunkType: "method"}
	class := Chunk{ChunkType: "class"}
	file := Chunk{ChunkType: "file"}

	cases := []struct {
		name      string
		c         Chunk
		nKids     int
		spanLines int
		maxLines  int
		want      bool
	}{
		{name: "leaf function under cap", c: fn, spanLines: 80, maxLines: 100, want: false},
		{name: "leaf function over cap", c: fn, spanLines: 291, maxLines: 100, want: false},
		{name: "leaf method over cap", c: method, spanLines: 200, maxLines: 100, want: false},
		{name: "function with nested children", c: fn, nKids: 1, spanLines: 80, maxLines: 100, want: true},
		{name: "class over cap", c: class, spanLines: 200, maxLines: 100, want: true},
		{name: "file over cap", c: file, spanLines: 400, maxLines: 100, want: true},
		{name: "class under cap no kids", c: class, spanLines: 40, maxLines: 100, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldSkeleton(tc.c, tc.nKids, tc.spanLines, tc.maxLines)
			if got != tc.want {
				t.Fatalf("shouldSkeleton(%s, kids=%d, span=%d) = %v, want %v",
					tc.c.ChunkType, tc.nKids, tc.spanLines, got, tc.want)
			}
		})
	}
}

func TestHydrateSourceCodeKeepsOversizedFunctionBody(t *testing.T) {
	dir := t.TempDir()
	const n = 180
	var b strings.Builder
	b.WriteString("fn build_with() {\n")
	for i := 1; i <= n; i++ {
		b.WriteString("    let x = ")
		b.WriteString(strings.Repeat("a", 8))
		b.WriteString(";\n")
	}
	b.WriteString("    unique_tail_marker();\n")
	b.WriteString("}\n")
	src := b.String()
	path := filepath.Join(dir, "prism_v2.rs")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	end := strings.Count(src, "\n")
	if !strings.HasSuffix(src, "\n") {
		end++
	}

	cb := &ContextBuilder{
		sourceRoot: dir,
		config: &config.Config{
			RetrievalConfig: config.RetrievalConfig{CodeToAstRatio: 1},
		},
		debugLog: &utils.DebugLogger{},
	}
	chunks := []Chunk{{
		ID:        "func_build_with",
		ChunkType: "function",
		File:      "prism_v2.rs",
		Name:      "build_with",
		StartLine: 1,
		EndLine:   end,
		Content:   "fn build_with() { /* ast stub */ }",
		Tokens:    20,
	}}

	out := cb.hydrateSourceCode(chunks, 8000, 100)
	if len(out) != 1 {
		t.Fatalf("got %d chunks, want 1", len(out))
	}
	if strings.Contains(out[0].Content, "rest of body omitted") {
		t.Fatalf("oversized leaf function was skeletonized:\n%s", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "unique_tail_marker") {
		t.Fatalf("function body was truncated before the tail:\n%s", out[0].Content)
	}
}

func TestHydrateSourceCodeSkeletonsOversizedClass(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("class Container {\n")
	for i := 0; i < 150; i++ {
		b.WriteString("    int field;\n")
	}
	b.WriteString("    void buried_method() {}\n")
	b.WriteString("}\n")
	src := b.String()
	if err := os.WriteFile(filepath.Join(dir, "box.java"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	end := strings.Count(src, "\n")

	cb := &ContextBuilder{
		sourceRoot: dir,
		config: &config.Config{
			RetrievalConfig: config.RetrievalConfig{CodeToAstRatio: 1},
		},
		debugLog: &utils.DebugLogger{},
	}
	out := cb.hydrateSourceCode([]Chunk{{
		ID:        "class_Container",
		ChunkType: "class",
		File:      "box.java",
		Name:      "Container",
		StartLine: 1,
		EndLine:   end,
		Content:   "class Container {}",
		Tokens:    20,
	}}, 8000, 100)
	if len(out) != 1 {
		t.Fatalf("got %d chunks, want 1", len(out))
	}
	if !strings.Contains(out[0].Content, "rest of body omitted") {
		t.Fatalf("expected class over Max_Lines to stay a skeleton, got:\n%s", out[0].Content)
	}
	if strings.Contains(out[0].Content, "buried_method") {
		t.Fatalf("skeleton leaked the class tail:\n%s", out[0].Content)
	}
}
