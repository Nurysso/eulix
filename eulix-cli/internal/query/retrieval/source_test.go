// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
)

// === bugs ===

// Bug 1: pure function, runs identically on all OSes.
func TestBuildSkeletonDoesNotLeakChildBodyAfterCommentHeavyGap(t *testing.T) {
	src := []string{"type A struct {"}
	for i := 0; i < 300; i++ {
		src = append(src, "// filler comment") // lines 2..301
	}
	src = append(
		src,
		"func (a A) child() {", // 302
		"\tsecretChildBody()",  // 303
		"}",                    // 304
		"}",                    // 305
	)
	groups := []childGroup{{
		startLine: 302,
		endLine:   304,
		children:  []childSpan{{chunkIdx: 1, startLine: 302, endLine: 304}},
	}}

	lines, pointers, tail := buildSkeleton(src, 1, 305, groups, "go")

	if joined := strings.Join(lines, "\n"); strings.Contains(joined, "secretChildBody") {
		t.Fatalf("child body leaked into parent skeleton:\n%s", joined)
	}
	if !tail {
		t.Error("tail should be true: source was left out")
	}
	if len(pointers) != 0 {
		t.Errorf("no pointer should be recorded when the scan stops before the child: %+v", pointers)
	}
}

// Bug 2: CRLF must not leave '\r' on lines (matters on Windows checkouts).
func TestFileCacheNormalizesCRLFAndBOM(t *testing.T) {
	dir := t.TempDir()
	body := "\xef\xbb\xbfpackage x\r\n\r\n// note\r\nfunc F() {}\r\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fc := newFileCache()
	lines := fc.get(filepath.Join(dir, "a.go"), nil)
	if len(lines) == 0 {
		t.Fatal("no lines read")
	}
	for i, l := range lines {
		if strings.Contains(l, "\r") {
			t.Errorf("line %d still has \\r: %q", i+1, l)
		}
	}
	if strings.HasPrefix(lines[0], "\xef\xbb\xbf") {
		t.Errorf("BOM left on first line: %q", lines[0])
	}
	for _, l := range stripSegment(lines, 1, len(lines), "go") {
		if strings.Contains(l, "\r") {
			t.Errorf("stripped output has \\r: %q", l)
		}
	}
}

func newRootedCache(t *testing.T, root string) *fileCache {
	t.Helper()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	fc := newFileCache()
	fc.root, fc.rootPath = r, root
	return fc
}

// Bug 3: only fails where the root path is a symlink (macOS /var -> /private/var).
func TestFileCacheAbsolutePathThroughSymlinkedRoot(t *testing.T) {
	root := t.TempDir() // macOS: /var/folders/... (unresolved)
	if err := os.WriteFile(filepath.Join(root, "a.py"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	fc := newRootedCache(t, root)

	if lines := fc.get(filepath.Join(resolved, "a.py"), nil); len(lines) == 0 {
		t.Fatalf("absolute path via resolved root failed (root=%s resolved=%s)", root, resolved)
	}
}

func TestFileCacheAbsolutePathThroughSymlinkedRootLinux(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "a.py"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(base, "link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// sourceRoot is what the user configured: the symlinked path.
	// The indexer resolved paths and stored them under realRoot.
	fc := newRootedCache(t, linkRoot)
	if lines := fc.get(filepath.Join(realRoot, "a.py"), nil); len(lines) == 0 {
		t.Fatalf("resolved absolute path rejected by unresolved root (root=%s real=%s)",
			linkRoot, realRoot)
	}
}

func TestFileCacheSymlinkEscapeStillRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	outside := filepath.Join(base, "secret.txt")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "leak.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fc := newRootedCache(t, root)
	if lines := fc.get(filepath.Join(root, "leak.txt"), nil); len(lines) != 0 {
		t.Fatalf("symlink escaping the root was read: %v", lines)
	}
}

// Regression guards for the os.Root containment fix, on every OS.
func TestFileCacheStaysInsideRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "secret.py")
	if err := os.WriteFile(outside, []byte("TOKEN = 'SYNTHETIC_SECRET'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.py"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fc := newRootedCache(t, root)

	if got := fc.get("../secret.py", nil); got != nil {
		t.Errorf("../ escape returned content: %v", got)
	}
	if got := fc.get(outside, nil); got != nil { // absolute, outside root
		t.Errorf("absolute outside path returned content: %v", got)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.py")); err == nil {
		if got := fc.get("link.py", nil); got != nil {
			t.Errorf("escaping symlink returned content: %v", got)
		}
	} // else: Windows without symlink privilege, skip that case
	if got := fc.get("ok.py", nil); len(got) == 0 {
		t.Error("file inside root should be readable")
	}
	if got := fc.get(filepath.Join(root, "ok.py"), nil); len(got) == 0 {
		t.Error("absolute path inside root should be readable")
	}
}

// Invariant guard for the allocator: spend never exceeds the budget.
func TestAllocateAndFitNeverExceedBudget(t *testing.T) {
	mk := func(idx, nLines int, w float64) *hItem {
		it := &hItem{idx: idx, weight: w}
		it.lines = make([]string, nLines)
		it.cum = make([]int, nLines+1)
		for i := range it.lines {
			it.lines[i] = strings.Repeat("x", 60)
			it.cum[i+1] = it.cum[i] + 61
		}
		it.need = it.cost(nLines)
		it.minUseful = max(minGrantTokens, int(float64(it.need)*minGrantFraction))
		it.minUseful = min(it.minUseful, it.need)
		return it
	}
	for _, budget := range []int{50, 300, 1000, 5000} {
		items := []*hItem{mk(0, 200, 1), mk(1, 40, 0.8), mk(2, 500, 0.5), mk(3, 10, 0.3)}
		allocateGrants(items, budget)
		if used := fitAndTopUp(items, budget); used > budget {
			t.Errorf("budget %d: used %d", budget, used)
		}
	}
}

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
	out, _, tail := buildSkeleton(src, 1, 60, nil, "go")
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
	groups := []childGroup{
		{startLine: 20, endLine: 50, children: []childSpan{{chunkIdx: 1, startLine: 20, endLine: 50}}},
		{startLine: 60, endLine: 80, children: []childSpan{{chunkIdx: 2, startLine: 60, endLine: 80}}},
	}

	out, pointers, _ := buildSkeleton(src, 1, 100, groups, "go")

	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "omitted") {
		t.Error("skeleton with kids should note omitted child ranges")
	}
	if len(pointers) != 2 {
		t.Errorf("want 2 pointers, got %d: %q", len(pointers), out)
	}
}

func TestBuildSkeleton_TailWhenLong(t *testing.T) {
	src := make([]string, 200)
	for i := range src {
		src[i] = fmt.Sprintf("line %d padding content here", i+1)
	}

	out, pointers, tail := buildSkeleton(src, 1, 200, nil, "go")

	if len(out) > skeletonHeadLines {
		t.Errorf("output exceeds skeletonHeadLines: %d", len(out))
	}
	if !tail {
		t.Error("tail should be true: 200 lines were cut to the head size")
	}
	if len(pointers) != 0 {
		t.Errorf("no children, so no pointers expected: %+v", pointers)
	}
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

// ========== Dangling Pointer Check Tests ==========

// newTestBuilder is the one place to adapt to your ContextBuilder. The tests only need
// buildItems and enforceCeiling. If DebugLogger.Log is not nil-receiver safe,
// enforceCeiling will panic on eviction, so set a real logger here.
func newTestBuilder() *ContextBuilder {
	return &ContextBuilder{debugLog: utils.NewDebugLogger()}
}

var skeletonFixtureSrc = []string{
	"package p",              // 1
	"type T struct{ n int }", // 2
	"func (t T) A() {",       // 3
	"\tt.n++",                // 4
	"}",                      // 5
	"func (t T) B() {",       // 6
	"\tt.n--",                // 7
	"}",                      // 8
	"var tail = 1",           // 9
}

var sepPointer = regexp.MustCompile(`lines (\d+)-(\d+) omitted \(separate chunk\)`)

func writeGoFile(t *testing.T, lines []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "p.go")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p // absolute path; cache has no root, so it goes through os.ReadFile
}

// runPipeline mirrors hydrateSourceCode after allocation: chunk 0 is the parent and gets its
// full skeleton; every child gets no grant, so each stays AST (what allocateGrants does
// when the pool can't cover them). ceilingFor picks the ceiling from what the parent
// used and what the children cost. Returns the output chunks the way hydrateSourceCode
// assembles them.
func runPipeline(t *testing.T, chunks []Chunk, ceilingFor func(usedCode, childAST int) int) []Chunk {
	t.Helper()
	cb := newTestBuilder()
	items := cb.buildItems(chunks, newFileCache(), 1000)

	parent := items[0]
	if parent.skip || !parent.skeleton {
		t.Fatalf("fixture broken: parent must hydrate as a skeleton (skip=%v reason=%q)", parent.skip, parent.reason)
	}
	parent.grant = parent.need

	usedCode := fitAndTopUp(items, parent.need)
	childAST := 0
	for _, it := range items[1:] {
		if !it.skip {
			t.Fatalf("fixture broken: child %d should have no grant", it.idx)
		}
		childAST += it.astTokens
	}

	evicted, _ := cb.enforceCeiling(items, ceilingFor(usedCode, childAST), usedCode)

	var out []Chunk
	for _, it := range items {
		c := it.chunk
		switch {
		case evicted[it.idx]:
			continue
		case it.skip:
		default:
			it.dropEvictedPointers(evicted)
			c.Content = it.render()
		}
		out = append(out, c)
	}
	return out
}

// danglingPointers returns every "(separate chunk)" claim in the parent whose lines are
// not fully covered by a chunk that is still in the output.
func danglingPointers(out []Chunk) []string {
	covered := map[int]bool{}
	for _, c := range out[1:] {
		for l := c.StartLine; l <= c.EndLine; l++ {
			covered[l] = true
		}
	}
	var bad []string
	for _, m := range sepPointer.FindAllStringSubmatch(out[0].Content, -1) {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		for l := lo; l <= hi; l++ {
			if !covered[l] {
				bad = append(bad, m[0])
				break
			}
		}
	}
	return bad
}

func skeletonFixture(t *testing.T) *hItem {
	t.Helper()
	src := skeletonFixtureSrc
	chunks := []Chunk{
		{File: "p.go", StartLine: 1, EndLine: 10, ChunkType: "file"},
		{File: "p.go", StartLine: 3, EndLine: 5, ChunkType: "function"}, // A
		{File: "p.go", StartLine: 7, EndLine: 9, ChunkType: "function"}, // B
		{File: "p.go", StartLine: 7, EndLine: 9, ChunkType: "function"}, // B, duplicate range
	}
	groups := childGroups(chunks, directChildren(chunks)[0], 1, 10)

	it := &hItem{lang: "go"}
	it.lines, it.pointers, it.tail = buildSkeleton(src, 1, 9, groups, "go")
	it.keep = len(it.lines)
	if len(it.pointers) != 2 {
		t.Fatalf("want 2 pointers, got %d: %q", len(it.pointers), it.lines)
	}
	return it
}

func pointerStartingAt(it *hItem, startLine int) string {
	for _, p := range it.pointers {
		if p.group.startLine == startLine {
			return it.lines[p.lineIdx]
		}
	}
	return ""
}

func TestDropEvictedPointers(t *testing.T) {
	tests := []struct {
		name         string
		evicted      map[int]bool
		wantA, wantB bool // pointer still claims a separate chunk
	}{
		{"nothing evicted", nil, true, true},
		{"A evicted", map[int]bool{1: true}, false, true},
		{"B evicted, duplicate survives", map[int]bool{2: true}, true, true},
		{"duplicate evicted, B survives", map[int]bool{3: true}, true, true},
		{"both copies of B evicted", map[int]bool{2: true, 3: true}, true, false},
		{"everything evicted", map[int]bool{1: true, 2: true, 3: true}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			it := skeletonFixture(t)
			it.dropEvictedPointers(tc.evicted)

			for _, want := range []struct {
				startLine int
				separate  bool
			}{{3, tc.wantA}, {7, tc.wantB}} {
				line := pointerStartingAt(it, want.startLine)
				if got := strings.Contains(line, "(separate chunk)"); got != want.separate {
					t.Errorf("pointer at line %d: separate=%v, want %v (%q)", want.startLine, got, want.separate, line)
				}
				if !strings.Contains(line, "omitted") {
					t.Errorf("pointer at line %d lost its omission marker: %q", want.startLine, line)
				}
			}
		})
	}
}

func TestFullyBackedMergedGroupNeedsEveryChild(t *testing.T) {
	chunks := []Chunk{
		{File: "p.go", StartLine: 1, EndLine: 9, ChunkType: "file"},
		{File: "p.go", StartLine: 3, EndLine: 5, ChunkType: "function"},
		{File: "p.go", StartLine: 6, EndLine: 8, ChunkType: "function"}, // touches the first, so they merge
	}
	groups := childGroups(chunks, directChildren(chunks)[0], 1, 9)
	if len(groups) != 1 || len(groups[0].children) != 2 {
		t.Fatalf("want one merged group with two children, got %+v", groups)
	}
	if !groups[0].fullyBacked(nil) {
		t.Error("untouched group reported as not backed")
	}
	if groups[0].fullyBacked(map[int]bool{2: true}) {
		t.Error("group 3-8 reported backed with child 6-8 evicted")
	}
}

// Control: nothing is evicted, so every pointer is backed. If this fails, the
// checker is wrong, not the code under test.
func TestSkeletonPointers_NoEvictionIsConsistent(t *testing.T) {
	f := writeGoFile(t, skeletonFixtureSrc)
	out := runPipeline(t, []Chunk{
		{File: f, StartLine: 1, EndLine: 9, ChunkType: "file"},
		{File: f, StartLine: 3, EndLine: 5, ChunkType: "function", Tokens: 50},
		{File: f, StartLine: 6, EndLine: 8, ChunkType: "function", Tokens: 50},
	}, func(used, child int) int { return used + child }) // everything fits

	if len(out) != 3 {
		t.Fatalf("want 3 chunks, got %d", len(out))
	}
	if !strings.Contains(out[0].Content, "(separate chunk)") {
		t.Fatalf("fixture broken: skeleton has no pointers:\n%s", out[0].Content)
	}
	if bad := danglingPointers(out); len(bad) != 0 {
		t.Fatalf("unexpected dangling pointers: %v", bad)
	}
}

// Bug: both children are evicted, but the parent still says their lines are separate chunks.
func TestSkeletonPointers_DanglingAfterChildEvicted(t *testing.T) {
	f := writeGoFile(t, skeletonFixtureSrc)
	out := runPipeline(t, []Chunk{
		{File: f, StartLine: 1, EndLine: 9, ChunkType: "file"},
		{File: f, StartLine: 3, EndLine: 5, ChunkType: "function", Tokens: 50},
		{File: f, StartLine: 6, EndLine: 8, ChunkType: "function", Tokens: 50},
	}, func(used, child int) int { return used }) // ceiling forces both children out

	if len(out) != 1 {
		t.Fatalf("fixture broken: want only the parent to survive, got %d chunks", len(out))
	}
	if bad := danglingPointers(out); len(bad) != 0 {
		t.Errorf("parent points at chunks that were evicted: %v\n--- parent ---\n%s", bad, out[0].Content)
	}
}

// Bug, merged variant: A (3-5) and B (6-8) are adjacent, so childRanges merges them into
// one pointer "lines 3-8". Evicting only B leaves lines 6-8 dangling under a pointer that
// still looks half-true.
func TestSkeletonPointers_DanglingOnMergedRangePartialEviction(t *testing.T) {
	f := writeGoFile(t, skeletonFixtureSrc)
	out := runPipeline(t, []Chunk{
		{File: f, StartLine: 1, EndLine: 9, ChunkType: "file"},
		{File: f, StartLine: 3, EndLine: 5, ChunkType: "function", Tokens: 50},
		{File: f, StartLine: 6, EndLine: 8, ChunkType: "function", Tokens: 50},
	}, func(used, child int) int { return used + 50 }) // room for one child; coldest (B) goes

	if len(out) != 2 {
		t.Fatalf("fixture broken: want parent + A to survive, got %d chunks", len(out))
	}
	if out[1].StartLine != 3 {
		t.Fatalf("fixture broken: expected A to survive, got %d-%d", out[1].StartLine, out[1].EndLine)
	}
	if bad := danglingPointers(out); len(bad) != 0 {
		t.Errorf("merged pointer claims evicted child B: %v\n--- parent ---\n%s", bad, out[0].Content)
	}
}
