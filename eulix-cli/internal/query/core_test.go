package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"eulix/internal/config"
	"eulix/internal/query/classifier"
	"eulix/internal/query/retrieval"
	"eulix/internal/utils"
)

// Fixture helpers
type M = map[string]any
type S = []any

// fill populates *dst from a spec of nested M (structs/maps), S (slices) and scalars,
// keyed by Go field name. A bare string for a struct with a Name field means {Name: s}.
func fill(t testing.TB, dst any, spec any) {
	t.Helper()
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		t.Fatalf("fill: dst must be a non-nil pointer, got %T", dst)
	}
	fillValue(t, rv.Elem(), spec)
}

func fillValue(t testing.TB, v reflect.Value, spec any) {
	t.Helper()
	if spec == nil {
		return
	}
	if !v.CanSet() {
		t.Fatalf("fill: cannot set value of type %s", v.Type())
	}
	switch v.Kind() {
	case reflect.Pointer:
		nv := reflect.New(v.Type().Elem())
		fillValue(t, nv.Elem(), spec)
		v.Set(nv)
	case reflect.Interface:
		v.Set(reflect.ValueOf(spec))
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			s, ok := spec.(string)
			if !ok {
				t.Fatalf("fill: time.Time needs an RFC3339 string, got %T", spec)
			}
			tm, err := time.Parse(time.RFC3339, s)
			if err != nil {
				t.Fatalf("fill: %v", err)
			}
			v.Set(reflect.ValueOf(tm))
			return
		}
		if s, ok := spec.(string); ok && v.FieldByName("Name").IsValid() {
			spec = M{"Name": s}
		}
		m, ok := spec.(M)
		if !ok {
			t.Fatalf("fill: %s needs a map spec, got %T", v.Type(), spec)
		}
		for k, sv := range m {
			f := v.FieldByName(k)
			if !f.IsValid() {
				t.Fatalf("fill: %s has no field %q", v.Type(), k)
			}
			fillValue(t, f, sv)
		}
	case reflect.Slice:
		items, ok := spec.([]any)
		if !ok {
			t.Fatalf("fill: %s needs a slice spec, got %T", v.Type(), spec)
		}
		s := reflect.MakeSlice(v.Type(), len(items), len(items))
		for i, it := range items {
			fillValue(t, s.Index(i), it)
		}
		v.Set(s)
	case reflect.Map:
		m, ok := spec.(M)
		if !ok {
			t.Fatalf("fill: %s needs a map spec, got %T", v.Type(), spec)
		}
		mv := reflect.MakeMapWithSize(v.Type(), len(m))
		for k, sv := range m {
			elem := reflect.New(v.Type().Elem()).Elem()
			fillValue(t, elem, sv)
			mv.SetMapIndex(reflect.ValueOf(k).Convert(v.Type().Key()), elem)
		}
		v.Set(mv)
	default:
		sv := reflect.ValueOf(spec)
		if v.Kind() == reflect.String && sv.Kind() != reflect.String {
			t.Fatalf("fill: refusing to convert %s to string", sv.Kind())
		}
		if !sv.Type().ConvertibleTo(v.Type()) {
			t.Fatalf("fill: cannot convert %s to %s", sv.Type(), v.Type())
		}
		v.Set(sv.Convert(v.Type()))
	}
}

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t testing.TB, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(b))
}

func chdir(t testing.TB, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func sp(s string) *string { return &s }

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func mustNotPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", name, r)
		}
	}()
	fn()
}

func assertStable(t *testing.T, name string, runs int, fn func() string) {
	t.Helper()
	first := fn()
	for i := 1; i < runs; i++ {
		if got := fn(); got != first {
			t.Fatalf("%s is not deterministic.\nrun 0:\n%s\nrun %d:\n%s", name, first, i, got)
		}
	}
}

func node(id, file, typ string, callCount int) M {
	return M{"ID": id, "File": file, "NodeType": typ, "CallCountEstimate": callCount}
}

func edge(from, to string) M { return M{"From": from, "To": to, "EdgeType": "call"} }

func newRouter(t testing.TB) *Router {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	fill(t, cfg, M{"Project": M{"Path": dir}, "RetrievalConfig": M{"CodeToAstRatio": 0.5}})
	cl, err := classifier.QuerySheriff("")
	if err != nil {
		t.Fatalf("QuerySheriff: %v", err)
	}
	return &Router{
		eulixDir:   dir,
		config:     cfg,
		classifier: cl,
		debug:      utils.NewDebugLogger(dir),
		kbIndex:    &utils.Indices{},
		callGraph:  buildRouterCallGraph(nil),
		cgIdx:      &callGraphIndex{cache: map[string]string{}},
		cgBuild:    BuildCallGraphIndex(nil),
	}
}

func (r *Router) projectPath() string { return r.config.Project.Path }

func (r *Router) setGraph(t testing.TB, spec M) *utils.CallGraphRef {
	t.Helper()
	ref := &utils.CallGraphRef{}
	fill(t, ref, spec)
	r.callGraph = buildRouterCallGraph(ref)
	r.cgBuild = BuildCallGraphIndex(ref)
	r.cgIdx = &callGraphIndex{cache: map[string]string{}}
	return ref
}

func (r *Router) setIndex(t testing.TB, spec M) {
	t.Helper()
	idx := &utils.Indices{}
	fill(t, idx, spec)
	r.kbIndex = idx
}

func (r *Router) setKB(t testing.TB, files M) {
	t.Helper()
	kb := &utils.KnowledgeBaseSimplifiedRef{}
	fill(t, kb, M{
		"Metadata":  M{"ProjectName": "proj", "TotalFiles": len(files), "TotalLoc": 100},
		"Structure": files,
	})
	r.kb = kb
}

func setDeps(t testing.TB, deps []utils.ExternalDependency) {
	t.Helper()
	prev := externalDeps
	externalDeps = deps
	t.Cleanup(func() { externalDeps = prev })
}

func sampleDeps() []utils.ExternalDependency {
	return []utils.ExternalDependency{
		{Name: "requests", Version: sp("2.31"), Source: "pip", ImportCount: 12, UsedBy: []string{"src/a.py", "src/b.py"}},
		{Name: "serde", Source: "cargo", ImportCount: 3, UsedBy: []string{"src/lib.rs"}},
		{Name: "github.com/gin-gonic/gin", Version: sp("1.9.0"), Source: "go", ImportCount: 7, UsedBy: []string{"main.go", "api/handler.go"}},
	}
}

func cls(qt classifier.QueryType, symbols ...string) *classifier.Classification {
	return &classifier.Classification{Type: qt, Symbols: symbols}
}

func fileSpec() M {
	return M{
		"Language": "go", "Loc": 40,
		"Functions":     S{M{"Name": "Foo", "LineStart": 1, "LineEnd": 10, "Complexity": 3, "ImportanceScore": 0.5}},
		"Classes":       S{M{"Name": "Bar", "LineStart": 12, "LineEnd": 30, "Methods": S{"m1", "m2"}}},
		"Todos":         S{M{"Line": 3, "Text": "fix", "Priority": "high"}},
		"SecurityNotes": S{M{"NoteType": "secret", "Line": 9, "Description": "hardcoded"}},
	}
}

func TestBareID(t *testing.T) {
	cases := map[string]string{
		"func_foo":             "foo",
		"method_Router_handle": "Router.handle",
		"method_A_b_c":         "A.b_c",
		"method_nounderscore":  "nounderscore",
		"class_Foo":            "Foo",
		"struct_S":             "S",
		"enum_E":               "E",
		"interface_I":          "I",
		"type_T":               "T",
		"foo":                  "foo",
		"function_foo":         "function_foo",
		"func_foo::a.go:12":    "foo",
		"func_":                "",
		"::x":                  "",
		"":                     "",
	}
	for in, want := range cases {
		if got := bareID(in); got != want {
			t.Errorf("bareID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCallGraphShortName(t *testing.T) {
	cases := map[string]string{
		"func_x":               "x",
		"method_Router_handle": "handle",
		"method_A_b_c":         "b_c",
		"method_nounderscore":  "nounderscore",
		"class_Foo::a.py":      "Foo",
		"x::y":                 "x",
		"plain":                "plain",
		"":                     "",
	}
	for in, want := range cases {
		if got := callGraphShortName(in); got != want {
			t.Errorf("callGraphShortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsLikelySymbol_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "package qualified function", in: "pkg.Func", want: true},
		{name: "dotted identifier", in: "a.b.c", want: true},
		{name: "numeric string", in: "123", want: false},
		{name: "number prefix lowercase", in: "123foo", want: false},
		{name: "number prefix mixed case", in: "1Foo", want: false},
		{name: "valid symbol with trailing number", in: "Foo1", want: true},
		{name: "lowercase with trailing number", in: "foo1", want: false},
		{name: "kebab-case identifier", in: "foo-bar", want: false},
		{name: "kebab-case capitalized", in: "Foo-Bar", want: false},
		{name: "blank identifier", in: "_", want: true},
		{name: "leading underscore with mixed case", in: "_fooBar", want: true},
		{name: "snake_case", in: "foo_bar", want: true},
		{name: "acronym with trailing lowercase", in: "HTTPServer", want: true},
		{name: "all caps acronym", in: "HTTP", want: false},
		{name: "single capital letter", in: "F", want: false},
		{name: "empty string", in: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLikelySymbol(tt.in)
			if got != tt.want {
				t.Errorf("isLikelySymbol(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtractEntityName(t *testing.T) {
	cases := []struct{ q, want string }{
		{"where is the function HandleRequest", "HandleRequest"},
		{"how does foo work", "foo"},
		{"find foo_bar please", "foo_bar"},
		{"where is Foo", "Foo"},
		{"what does HTTP do", "HTTP"},
		{"where is the", ""},
		{"", ""},
		{"   ", ""},
		{"WHERE IS Foo", "Foo"},
	}
	for _, tc := range cases {
		if got := extractEntityName(tc.q); got != tc.want {
			t.Errorf("extractEntityName(%q) = %q, want %q", tc.q, got, tc.want)
		}
	}
}

func TestFirstSymbolOrExtracted(t *testing.T) {
	cases := []struct {
		name    string
		symbols []string
		query   string
		want    string
	}{
		{"symbol wins", []string{"Foo", "Bar"}, "anything", "Foo"},
		{"metrics word skipped, falls back to query", []string{"metrics"}, "metrics for Foo", "Foo"},
		{"metrics check is case-insensitive", []string{"Summary"}, "summary of Foo", "Foo"},
		{"no symbols uses query", nil, "where is Foo", "Foo"},
		{"only metrics words yields empty", nil, "metrics", ""},
		{"only stopwords yields empty", nil, "where is the", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firstSymbolOrExtracted(cls(classifier.QueryTypeMetrics, tc.symbols...), tc.query)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractFilePath(t *testing.T) {
	cases := []struct{ q, want string }{
		{"what is in file main.go", "main.go"},
		{"show internal/query/core.go", "internal/query/core.go"},
		{"look at x/y", "x/y"},
		{"no file here", ""},
		{"a.b", ""},
		{".go", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := extractFilePath(tc.q); got != tc.want {
			t.Errorf("extractFilePath(%q) = %q, want %q", tc.q, got, tc.want)
		}
	}
}

func TestStripCommandPrefix(t *testing.T) {
	prefixes := []string{"usage of", "usage", "use", "uses of", "show usage", "find usage"}
	cases := []struct{ q, want string }{
		{"usage of Foo", "Foo"},
		{"Usage Foo", "Foo"},
		{"USAGE OF Foo", "Foo"},
		{"use Foo", "Foo"},
		{"uses of Foo", "Foo"},
		{"show usage Foo", "Foo"},
		{"find usage Foo", "Foo"},
		{"what uses Foo", "what uses Foo"},
		{"usage", "usage"},
		{"usageFoo", "usageFoo"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripCommandPrefix(tc.q, prefixes...); got != tc.want {
			t.Errorf("stripCommandPrefix(%q) = %q, want %q", tc.q, got, tc.want)
		}
	}
	if got := stripCommandPrefix("usage Foo"); got != "usage Foo" {
		t.Errorf("no prefixes should be a no-op, got %q", got)
	}
}

func TestParseLocation(t *testing.T) {
	cases := []struct {
		in      string
		file    string
		line    int
		wantErr bool
	}{
		{"a.go:10", "a.go", 10, false},
		{"pkg/sub/a.go:1", "pkg/sub/a.go", 1, false},
		{`C:\src\a.go:42`, `C:\src\a.go`, 42, false},
		{"a.go:12:5", "a.go:12", 5, false},
		{"a.go:0", "a.go", 0, false},
		{":5", "", 5, false},
		{"a.go", "", 0, true},
		{"a.go:", "", 0, true},
		{"a.go:x", "", 0, true},
		{"a.go: 5", "", 0, true},
		{"", "", 0, true},
	}
	for _, tc := range cases {
		file, line, err := parseLocation(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseLocation(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && (file != tc.file || line != tc.line) {
			t.Errorf("parseLocation(%q) = (%q, %d), want (%q, %d)", tc.in, file, line, tc.file, tc.line)
		}
	}
}

func TestDedupe(t *testing.T) {
	in := []string{"b", "a", "b", "c", "a"}
	orig := append([]string(nil), in...)
	if got := dedupe(in); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Errorf("dedupe = %v", got)
	}
	if !reflect.DeepEqual(in, orig) {
		t.Errorf("dedupe mutated its input: %v", in)
	}
	if got := dedupe(nil); got == nil || len(got) != 0 {
		t.Errorf("dedupe(nil) = %#v, want empty non-nil", got)
	}
	if got := dedupe([]string{"", ""}); !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("dedupe of empties = %v", got)
	}
}

func TestDedupStrings(t *testing.T) {
	if got := dedupStrings([]string{"b", "a", "b", "c", "a"}); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Errorf("dedupStrings = %v", got)
	}
	if got := dedupStrings(nil); len(got) != 0 {
		t.Errorf("dedupStrings(nil) = %v", got)
	}
	if got := dedupStrings([]string{"x"}); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("dedupStrings single = %v", got)
	}
}

func TestFuzzyScore(t *testing.T) {
	if got := fuzzyScore("foo", "foo"); got != 1000 {
		t.Errorf("exact = %d, want 1000", got)
	}
	if got := fuzzyScore("foo", "foobar"); got != 500 {
		t.Errorf("prefix-contains = %d, want 500", got)
	}
	if got := fuzzyScore("foo", "xxfooxx"); got != 500 {
		t.Errorf("infix-contains = %d, want 500", got)
	}
	partial := fuzzyScore("foo", "fxx")
	if partial <= 0 || partial >= 500 {
		t.Errorf("partial = %d, want in (0, 500)", partial)
	}
	if got := fuzzyScore("qqqq", "abc"); got > 0 {
		t.Errorf("unrelated = %d, want <= 0", got)
	}
	if fuzzyScore("foo", "foo") <= fuzzyScore("foo", "foobar") || fuzzyScore("foo", "foobar") <= partial {
		t.Error("expected ordering exact > contains > partial")
	}
	mustNotPanic(t, "fuzzyScore unicode", func() { _ = fuzzyScore("héllo", "hello wörld") })
}

func TestFuzzySearch(t *testing.T) {
	t.Run("empty index", func(t *testing.T) {
		r := newRouter(t)
		if got := r.fuzzySearch("anything"); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("ranks exact above contains, labels kind", func(t *testing.T) {
		r := newRouter(t)
		r.setIndex(t, M{
			"FunctionsByName": M{"handle": S{"a.go:1"}, "handler": S{"a.go:2"}},
			"TypesByName":     M{"HandleCfg": S{"b.go:3"}},
		})
		got := r.fuzzySearch("handle")
		if len(got) != 3 || got[0] != "handle (function)" {
			t.Fatalf("got %v", got)
		}
		joined := strings.Join(got, "|")
		if !strings.Contains(joined, "HandleCfg (type)") || !strings.Contains(joined, "handler (function)") {
			t.Errorf("missing expected entries: %v", got)
		}
	})
	t.Run("case-insensitive", func(t *testing.T) {
		r := newRouter(t)
		r.setIndex(t, M{"FunctionsByName": M{"HandleRequest": S{"a.go:1"}}})
		if got := r.fuzzySearch("HANDLEREQUEST"); len(got) != 1 || got[0] != "HandleRequest (function)" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("capped at five", func(t *testing.T) {
		r := newRouter(t)
		fns := M{}
		for i := 0; i < 9; i++ {
			fns[fmt.Sprintf("foo%d", i)] = S{"a.go:1"}
		}
		r.setIndex(t, M{"FunctionsByName": fns})
		if got := r.fuzzySearch("foo"); len(got) != 5 {
			t.Errorf("len = %d, want 5 (%v)", len(got), got)
		}
	})
}

func TestDetectLang(t *testing.T) {
	cases := map[string]language{
		"a.go": langGo, "a.rs": langRust, "a.py": langPython,
		"a.ts": langTS, "a.tsx": langTS, "a.js": langTS, "a.jsx": langTS,
		"a.c": langC, "a.h": langC, "a.cpp": langC, "a.cc": langC, "a.cxx": langC, "a.hpp": langC,
		"a.java": langGo, "Makefile": langGo, "dir.v2/file": langGo, "": langGo,
	}
	for path, want := range cases {
		if got := detectLang(path); got != want {
			t.Errorf("detectLang(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestParseSig(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		lang  language
		want  string
	}{
		{"go method", []string{"func (r *Router) Foo(a int) error {", "\treturn nil", "}"}, langGo,
			"  func (r *Router) Foo(a int) error\n"},
		{"go multi-line params", []string{"func Foo(", "\ta int,", "\tb string,", ") error {", "body"}, langGo,
			"  func Foo(\n  \ta int,\n  \tb string,\n  ) error\n"},
		{"go struct", []string{"type Foo struct {", "\tX int", "}"}, langGo, "  type Foo struct\n"},
		{"go one-liner", []string{"func f() { return }"}, langGo, "  func f()\n"},
		{"go no terminator, trailing blanks trimmed", []string{"func f()", "", ""}, langGo, "  func f()\n"},
		{"rust fn", []string{"pub fn foo(a: i32) -> Result<(), Error> {"}, langRust,
			"  pub fn foo(a: i32) -> Result<(), Error>\n"},
		{"rust trait decl", []string{"fn foo(&self);"}, langRust, "  fn foo(&self);\n"},
		{"rust multi-line", []string{"pub fn foo(", "    a: i32,", "    b: i32,", ") -> i32 {"}, langRust,
			"  pub fn foo(\n      a: i32,\n      b: i32,\n  ) -> i32\n"},
		{"python simple", []string{"def foo(a, b):", "    pass"}, langPython, "  def foo(a, b)\n"},
		{"python multi-line", []string{"def foo(", "    a,", "    b,", "):", "    pass"}, langPython,
			"  def foo(\n      a,\n      b,\n  )\n"},
		{"ts arrow", []string{"export const f = (a: number) => {", "  return a", "}"}, langTS,
			"  export const f = (a: number) =>\n"},
		{"ts async", []string{"async function f(x: string): Promise<void> {"}, langTS,
			"  async function f(x: string): Promise<void>\n"},
		{"c function", []string{"static int foo(int a) {"}, langC, "  static int foo(int a)\n"},
		{"unterminated returns what it has", []string{"func f(", "a int,"}, langGo, "  func f(\n  a int,\n"},
		{"index past end", nil, langGo, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := 0
			if tc.name == "index past end" {
				idx = 5
			}
			got, err := parseSig(append([]string(nil), tc.lines...), idx, tc.lang)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestParseSig_ScansAtMostFortyLines(t *testing.T) {
	ls := []string{"func f("}
	for i := 0; i < 100; i++ {
		ls = append(ls, "\ta int,")
	}
	got, err := parseSig(ls, 0, langGo)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "\n"); n != 40 {
		t.Errorf("collected %d lines, want 40", n)
	}
}

func TestParseSig_StartsAtIndex(t *testing.T) {
	ls := []string{"package x", "", "func A() {", "}", "func B(x int) {", "}"}
	got, _ := parseSig(ls, 4, langGo)
	if got != "  func B(x int)\n" {
		t.Errorf("got %q", got)
	}
}

func TestExtractSignature(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "svc.go")
	writeFile(t, goFile, lines("package x", "", "func Foo(a int, b string) error {", "\treturn nil", "}"))
	pyFile := filepath.Join(dir, "svc.py")
	writeFile(t, pyFile, lines("import os", "", "def foo(a, b):", "    return a"))

	t.Run("go", func(t *testing.T) {
		got, err := extractSignature(goFile, 3)
		if err != nil || got != "  func Foo(a int, b string) error\n" {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("python picks language from extension", func(t *testing.T) {
		got, err := extractSignature(pyFile, 3)
		if err != nil || got != "  def foo(a, b)\n" {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, err := extractSignature(filepath.Join(dir, "nope.go"), 1); err == nil || !strings.Contains(err.Error(), "open") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("line out of range", func(t *testing.T) {
		for _, n := range []int{0, -1, 999} {
			_, err := extractSignature(goFile, n)
			if err == nil || !strings.Contains(err.Error(), "out of range") {
				t.Errorf("line %d: err = %v", n, err)
			}
		}
	})
	t.Run("last line is accepted", func(t *testing.T) {
		if _, err := extractSignature(goFile, 6); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

func TestFormatFunctionMetrics(t *testing.T) {
	fn := utils.KBFunction{Name: "f", LineStart: 10, LineEnd: 19, Complexity: 4, ImportanceScore: 0.5}
	want := "Metrics for f (a/b.go, lines 10\u201319)\n" +
		"  Cyclomatic complexity : 4\n" +
		"  LOC                   : 10\n" +
		"  Importance            : 0.50\n"
	if got := formatFunctionMetrics(fn, "a/b.go"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	one := utils.KBFunction{Name: "g", LineStart: 7, LineEnd: 7}
	if got := formatFunctionMetrics(one, "x.go"); !strings.Contains(got, "LOC                   : 1\n") {
		t.Errorf("single-line function should have LOC 1:\n%s", got)
	}
}

func TestFormatFileData(t *testing.T) {
	t.Run("all sections", func(t *testing.T) {
		fd := &utils.FileData{}
		fill(t, fd, fileSpec())
		want := "File: pkg/a.go  [go, 40 LOC]\n" +
			"\nFunctions (1):\n  \u2022 Foo  (lines 1\u201310, complexity 3, importance 0.50)\n" +
			"\nClasses (1):\n  \u2022 Bar  (lines 12\u201330, 2 methods)\n" +
			"\nTODOs (1):\n  [high] line 3: fix\n" +
			"\nSecurity notes (1):\n  [secret] line 9: hardcoded\n"
		if got := formatFileData("pkg/a.go", fd); got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})
	t.Run("header only", func(t *testing.T) {
		fd := &utils.FileData{}
		fill(t, fd, M{"Language": "rust", "Loc": 5})
		if got := formatFileData("x.rs", fd); got != "File: x.rs  [rust, 5 LOC]\n" {
			t.Errorf("got %q", got)
		}
	})
}

func TestBuildCallGraphIndex(t *testing.T) {
	t.Run("nil ref gives empty non-nil maps", func(t *testing.T) {
		idx := BuildCallGraphIndex(nil)
		if idx == nil || idx.Nodes == nil || idx.Calls == nil || idx.CalledBy == nil {
			t.Fatalf("got %+v", idx)
		}
		if len(idx.Nodes)+len(idx.Calls)+len(idx.CalledBy) != 0 {
			t.Error("expected all maps empty")
		}
	})
	t.Run("empty ref", func(t *testing.T) {
		idx := BuildCallGraphIndex(&utils.CallGraphRef{})
		if len(idx.Nodes) != 0 {
			t.Errorf("nodes = %v", idx.Nodes)
		}
	})
	t.Run("indexes nodes by ID and points into the ref", func(t *testing.T) {
		ref := &utils.CallGraphRef{}
		fill(t, ref, M{"Nodes": S{node("func_a", "a.go", "function", 1), node("func_b", "b.go", "function", 2)}})
		idx := BuildCallGraphIndex(ref)
		if len(idx.Nodes) != 2 || idx.Nodes["func_a"] != &ref.Nodes[0] || idx.Nodes["func_b"] != &ref.Nodes[1] {
			t.Errorf("unexpected node index: %v", idx.Nodes)
		}
	})
	t.Run("only call edges, deduped, order kept", func(t *testing.T) {
		ref := &utils.CallGraphRef{}
		fill(t, ref, M{
			"Nodes": S{node("func_a", "a.go", "function", 0)},
			"Edges": S{
				edge("func_a", "func_b"), edge("func_a", "func_c"), edge("func_a", "func_b"),
				M{"From": "func_a", "To": "func_z", "EdgeType": "import"},
				edge("func_x", "func_b"), edge("func_x", "func_b"),
			},
		})
		idx := BuildCallGraphIndex(ref)
		if !reflect.DeepEqual(idx.Calls["func_a"], []string{"func_b", "func_c"}) {
			t.Errorf("Calls[a] = %v", idx.Calls["func_a"])
		}
		if !reflect.DeepEqual(idx.CalledBy["func_b"], []string{"func_a", "func_x"}) {
			t.Errorf("CalledBy[b] = %v", idx.CalledBy["func_b"])
		}
		if _, ok := idx.CalledBy["func_z"]; ok {
			t.Error("non-call edge leaked into CalledBy")
		}
	})
	t.Run("edges to unknown nodes are kept", func(t *testing.T) {
		ref := &utils.CallGraphRef{}
		fill(t, ref, M{"Edges": S{edge("ghost1", "ghost2")}})
		idx := BuildCallGraphIndex(ref)
		if !reflect.DeepEqual(idx.Calls["ghost1"], []string{"ghost2"}) {
			t.Errorf("Calls = %v", idx.Calls)
		}
	})
}

func TestBuildRouterCallGraph(t *testing.T) {
	t.Run("nil ref", func(t *testing.T) {
		g := buildRouterCallGraph(nil)
		if g == nil || g.Functions == nil || len(g.Functions) != 0 {
			t.Errorf("got %+v", g)
		}
	})
	t.Run("keys are bare IDs, location from File", func(t *testing.T) {
		ref := &utils.CallGraphRef{}
		fill(t, ref, M{
			"Nodes": S{node("func_a", "a.go", "function", 0), node("method_R_m", "r.go", "method", 0)},
			"Edges": S{edge("func_a", "method_R_m"), M{"From": "func_a", "To": "func_q", "EdgeType": "import"}},
		})
		g := buildRouterCallGraph(ref)
		if g.Functions["a"].Location != "a.go" || g.Functions["R.m"].Location != "r.go" {
			t.Errorf("locations wrong: %+v", g.Functions)
		}
		if !reflect.DeepEqual(g.Functions["a"].Calls, []string{"R.m"}) {
			t.Errorf("a.Calls = %v", g.Functions["a"].Calls)
		}
		if !reflect.DeepEqual(g.Functions["R.m"].CalledBy, []string{"a"}) {
			t.Errorf("R.m.CalledBy = %v", g.Functions["R.m"].CalledBy)
		}
		if _, ok := g.Functions["q"]; ok {
			t.Error("import edge created an entry")
		}
	})
	t.Run("edge to unknown node creates entry with empty location", func(t *testing.T) {
		ref := &utils.CallGraphRef{}
		fill(t, ref, M{"Edges": S{edge("func_x", "func_y")}})
		g := buildRouterCallGraph(ref)
		if fn, ok := g.Functions["y"]; !ok || fn.Location != "" || !reflect.DeepEqual(fn.CalledBy, []string{"x"}) {
			t.Errorf("got %+v", g.Functions)
		}
	})
}

func TestResolveCallGraphEntity(t *testing.T) {
	build := func(t *testing.T, nodes ...M) *Router {
		r := newRouter(t)
		r.setGraph(t, M{"Nodes": func() S {
			s := S{}
			for _, n := range nodes {
				s = append(s, n)
			}
			return s
		}()})
		return r
	}

	t.Run("nil index", func(t *testing.T) {
		r := newRouter(t)
		r.cgBuild = nil
		if _, _, ok, _ := r.resolveCallGraphEntity("x"); ok {
			t.Error("expected not found")
		}
	})
	t.Run("exact key", func(t *testing.T) {
		r := build(t, node("func_foo", "a.go", "function", 1))
		key, n, ok, amb := r.resolveCallGraphEntity("func_foo")
		if !ok || key != "func_foo" || n == nil || amb != nil {
			t.Errorf("got (%q, %v, %v, %v)", key, n, ok, amb)
		}
	})
	t.Run("bare name resolves via kind prefix without ambiguity", func(t *testing.T) {
		r := build(t, node("func_foo", "a.go", "function", 1))
		key, _, ok, amb := r.resolveCallGraphEntity("foo")
		if !ok || key != "func_foo" || amb != nil {
			t.Errorf("got (%q, %v, %v)", key, ok, amb)
		}
	})
	t.Run("kind-prefix order is func before struct", func(t *testing.T) {
		r := build(t, node("struct_foo", "s.go", "struct", 99), node("func_foo", "f.go", "function", 1))
		if key, _, _, _ := r.resolveCallGraphEntity("foo"); key != "func_foo" {
			t.Errorf("key = %q", key)
		}
	})
	t.Run("case-insensitive short name", func(t *testing.T) {
		r := build(t, node("method_Router_handle", "r.go", "method", 5))
		key, _, ok, amb := r.resolveCallGraphEntity("HANDLE")
		if !ok || key != "method_Router_handle" || len(amb) != 1 {
			t.Errorf("got (%q, %v, %v)", key, ok, amb)
		}
	})
	t.Run("Type.method form", func(t *testing.T) {
		r := build(t, node("method_Router_handle", "r.go", "method", 5), node("method_Other_handle", "o.go", "method", 9))
		key, _, ok, amb := r.resolveCallGraphEntity("Router.handle")
		if !ok || key != "method_Router_handle" || len(amb) != 1 {
			t.Errorf("got (%q, %v, %v)", key, ok, amb)
		}
	})
	t.Run("functions beat higher-traffic types on short-name ties", func(t *testing.T) {
		r := build(t,
			node("class_handle::a.py", "a.py", "class", 100),
			node("func_handle::b.py", "b.py", "function", 1))
		key, _, ok, amb := r.resolveCallGraphEntity("handle")
		if !ok || key != "func_handle::b.py" || len(amb) != 2 {
			t.Errorf("got (%q, %v, %v)", key, ok, amb)
		}
	})
	t.Run("call count breaks ties between same kinds", func(t *testing.T) {
		r := build(t, node("method_A_go", "a.go", "method", 3), node("method_B_go", "b.go", "method", 8))
		if key, _, _, _ := r.resolveCallGraphEntity("go"); key != "method_B_go" {
			t.Errorf("key = %q", key)
		}
	})
	t.Run("prefix-only match still resolves", func(t *testing.T) {
		r := build(t, node("func_handler_x", "a.go", "function", 1))
		key, _, ok, amb := r.resolveCallGraphEntity("hand")
		if !ok || key != "func_handler_x" || len(amb) != 1 {
			t.Errorf("got (%q, %v, %v)", key, ok, amb)
		}
	})
	t.Run("exact short name outranks prefix-only matches", func(t *testing.T) {
		r := build(t,
			node("func_run::a.go", "a.go", "function", 1),
			node("func_runner::b.go", "b.go", "function", 500))
		key, _, _, amb := r.resolveCallGraphEntity("run")
		if key != "func_run::a.go" || len(amb) != 2 {
			t.Errorf("got (%q, %v)", key, amb)
		}
	})
	t.Run("not found", func(t *testing.T) {
		r := build(t, node("func_foo", "a.go", "function", 1))
		key, n, ok, amb := r.resolveCallGraphEntity("zzz")
		if ok || key != "" || n != nil || amb != nil {
			t.Errorf("got (%q, %v, %v, %v)", key, n, ok, amb)
		}
	})
}

func callGraphFixture(t *testing.T) *Router {
	r := newRouter(t)
	main := node("func_main", "main.go", "function", 0)
	main["IsEntryPoint"] = true
	r.setGraph(t, M{
		"Nodes": S{main, node("func_a", "a.go", "function", 1), node("func_b", "b.go", "function", 1), node("func_c", "c.go", "function", 1)},
		"Edges": S{edge("func_main", "func_a"), edge("func_main", "func_b"), edge("func_a", "func_c"), edge("func_main", "func_a")},
	})
	r.setIndex(t, M{"FunctionsByName": M{"alpha": S{"a.go:1"}}})
	return r
}

func TestHandleCallGraph(t *testing.T) {
	t.Run("exact output for a middle node", func(t *testing.T) {
		r := callGraphFixture(t)
		got, err := r.handleCallGraph("call graph for a", cls(classifier.QueryTypeCallGraph, "a"))
		if err != nil {
			t.Fatal(err)
		}
		want := lines(
			"Call Graph: a",
			"  File    : a.go",
			"  Type    : function",
			"  Metrics : Fan-in 1 | Fan-out 1",
			"",
			"\u250c\u2500\u2500 Called by (Inbound):",
			"\u2502   \u2190 main",
			"\u2514\u2500\u2500 Calls (Outbound):",
			"    \u2192 c",
		)
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("entry point shows role, grandchildren, and dedupes edges", func(t *testing.T) {
		r := callGraphFixture(t)
		got, _ := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "main"))
		for _, s := range []string{
			"  Role    : entry point\n",
			"Fan-in 0 | Fan-out 2",
			"(none \u2014 likely an entry point or exported API)",
			"    \u2192 a\n        \u2192 c\n    \u2192 b\n",
		} {
			if !strings.Contains(got, s) {
				t.Errorf("missing %q in:\n%s", s, got)
			}
		}
	})
	t.Run("leaf shows callers and grandparents", func(t *testing.T) {
		r := callGraphFixture(t)
		got, _ := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "c"))
		for _, s := range []string{"Fan-in 1 | Fan-out 0", "\u2190 a\n", "\u2190 main\n", "(none \u2014 leaf function)"} {
			if !strings.Contains(got, s) {
				t.Errorf("missing %q in:\n%s", s, got)
			}
		}
		if strings.Contains(got, "Role    :") {
			t.Error("non-entry node should not print a Role line")
		}
	})
	t.Run("high fan-out warning starts above seven", func(t *testing.T) {
		for _, n := range []int{7, 8} {
			r := newRouter(t)
			nodes, edges := S{node("func_hub", "h.go", "function", 0)}, S{}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("func_t%d", i)
				nodes = append(nodes, node(id, "t.go", "function", 0))
				edges = append(edges, edge("func_hub", id))
			}
			r.setGraph(t, M{"Nodes": nodes, "Edges": edges})
			got, _ := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "hub"))
			if has := strings.Contains(got, "High fan-out"); has != (n > 7) {
				t.Errorf("fan-out %d: warning present = %v", n, has)
			}
		}
	})
	t.Run("cycles terminate", func(t *testing.T) {
		r := newRouter(t)
		r.setGraph(t, M{
			"Nodes": S{node("func_x", "x.go", "function", 0), node("func_y", "y.go", "function", 0)},
			"Edges": S{edge("func_x", "func_y"), edge("func_y", "func_x")},
		})
		mustNotPanic(t, "cycle", func() {
			got, err := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "x"))
			if err != nil || !strings.Contains(got, "Call Graph: x") {
				t.Errorf("got (%q, %v)", got, err)
			}
		})
	})
	t.Run("no entity", func(t *testing.T) {
		r := callGraphFixture(t)
		got, err := r.handleCallGraph("the", cls(classifier.QueryTypeCallGraph))
		if err != nil || got != "Could not identify a symbol for call graph analysis." {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("missing call graph is an error", func(t *testing.T) {
		r := callGraphFixture(t)
		r.cgBuild = nil
		if _, err := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "a")); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("unknown symbol with no fuzzy candidates", func(t *testing.T) {
		r := callGraphFixture(t)
		got, _ := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "qqqq"))
		if got != "'qqqq' not found in call graph." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("unknown symbol with fuzzy candidates", func(t *testing.T) {
		r := callGraphFixture(t)
		got, _ := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "alph"))
		if !strings.HasPrefix(got, "'alph' not found. Did you mean: alpha (function)") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("results are cached per resolved key", func(t *testing.T) {
		r := callGraphFixture(t)
		first, _ := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "a"))
		r.cgIdx.mu.RLock()
		cached := r.cgIdx.cache["func_a"]
		r.cgIdx.mu.RUnlock()
		if cached != first {
			t.Errorf("cache[func_a] = %q, want %q", cached, first)
		}
		r.cgIdx.mu.Lock()
		r.cgIdx.cache["func_a"] = "CACHED"
		r.cgIdx.mu.Unlock()
		if got, _ := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, "a")); got != "CACHED" {
			t.Errorf("cache not consulted: %q", got)
		}
	})
	t.Run("concurrent use is race-free and consistent", func(t *testing.T) {
		r := callGraphFixture(t)
		want := map[string]string{}
		for _, s := range []string{"main", "a", "b", "c"} {
			want[s], _ = r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, s))
		}
		r.cgIdx = &callGraphIndex{cache: map[string]string{}}
		var wg sync.WaitGroup
		errs := make(chan string, 64)
		for g := 0; g < 16; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					for s, w := range want {
						if got, _ := r.handleCallGraph("", cls(classifier.QueryTypeCallGraph, s)); got != w {
							select {
							case errs <- fmt.Sprintf("%s differs", s):
							default:
							}
							return
						}
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
	})
}

func TestHandleLocation(t *testing.T) {
	r := newRouter(t)
	r.setIndex(t, M{
		"FunctionsByName":  M{"Foo": S{"a.go:1", "a.go:1"}, "FooBar": S{"b.go:2"}},
		"TypesByName":      M{"Foo": S{"a.go:9"}, "Widget": S{"w.go:1"}},
		"FunctionsCalling": M{"Foo": S{"main", "main", "init"}},
	})

	t.Run("function, type and callers, all deduped, in order", func(t *testing.T) {
		got, err := r.handleLocation("where is Foo", cls(classifier.QueryTypeLocation, "Foo"))
		want := lines(
			"Function 'Foo' defined at:", "  a.go:1",
			"Type 'Foo' defined at:", "  a.go:9",
			"'Foo' is called by:", "  main", "  init",
		)
		if err != nil || got != want {
			t.Errorf("got (%q, %v)\nwant %q", got, err, want)
		}
	})
	t.Run("type only", func(t *testing.T) {
		got, _ := r.handleLocation("", cls(classifier.QueryTypeLocation, "Widget"))
		if got != lines("Type 'Widget' defined at:", "  w.go:1") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("entity extracted from query when no symbols", func(t *testing.T) {
		got, _ := r.handleLocation("where is Widget", cls(classifier.QueryTypeLocation))
		if !strings.Contains(got, "Type 'Widget'") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("metrics-command symbol is ignored", func(t *testing.T) {
		got, _ := r.handleLocation("where is Widget", cls(classifier.QueryTypeLocation, "summary"))
		if !strings.Contains(got, "Type 'Widget'") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("falls back to fuzzy matches", func(t *testing.T) {
		got, _ := r.handleLocation("", cls(classifier.QueryTypeLocation, "Widge"))
		if !strings.HasPrefix(got, "No exact match for 'Widge'. Closest symbols:\n") || !strings.Contains(got, "  Widget (type)\n") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("nothing found", func(t *testing.T) {
		got, _ := r.handleLocation("", cls(classifier.QueryTypeLocation, "qqqq"))
		if got != "'qqqq' was not found in the knowledge base." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("no entity", func(t *testing.T) {
		got, _ := r.handleLocation("where is the", cls(classifier.QueryTypeLocation))
		if got != "Could not identify a function or class name in the query." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("nil index is an error, not a panic", func(t *testing.T) {
		r2 := newRouter(t)
		r2.kbIndex = nil
		mustNotPanic(t, "handleLocation(nil index)", func() {
			if _, err := r2.handleLocation("", cls(classifier.QueryTypeLocation, "Foo")); err == nil || !strings.Contains(err.Error(), "kb index unavailable") {
				t.Errorf("err = %v", err)
			}
		})
	})
}

func TestHandleUsage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "svc.go")
	writeFile(t, src, lines("package x", "", "func Foo(a int, b string) error {", "\treturn nil", "}"))
	newUsageRouter := func(t *testing.T, locs ...string) *Router {
		r := newRouter(t)
		items := S{}
		for _, l := range locs {
			items = append(items, l)
		}
		r.setIndex(t, M{"FunctionsByName": M{"Foo": items}})
		return r
	}

	t.Run("prints location and signature", func(t *testing.T) {
		r := newUsageRouter(t, src+":3")
		got, err := r.handleUsage("usage of Foo", cls(classifier.QueryTypeUsage, "Foo"))
		want := "Usage of 'Foo'\n\n  " + src + ":3\n  func Foo(a int, b string) error\n\n"
		if err != nil || got != want {
			t.Errorf("got (%q, %v)\nwant %q", got, err, want)
		}
	})
	t.Run("duplicate locations print once", func(t *testing.T) {
		r := newUsageRouter(t, src+":3", src+":3")
		got, _ := r.handleUsage("usage of Foo", cls(classifier.QueryTypeUsage, "Foo"))
		if n := strings.Count(got, "func Foo("); n != 1 {
			t.Errorf("signature printed %d times", n)
		}
	})
	t.Run("multiple locations", func(t *testing.T) {
		other := filepath.Join(dir, "other.go")
		writeFile(t, other, lines("package x", "func Foo() {", "}"))
		r := newUsageRouter(t, src+":3", other+":2")
		got, _ := r.handleUsage("", cls(classifier.QueryTypeUsage, "Foo"))
		if !strings.Contains(got, "func Foo(a int, b string) error") || !strings.Contains(got, "func Foo()\n") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("command prefix is stripped before entity extraction", func(t *testing.T) {
		r := newUsageRouter(t, src+":3")
		for _, q := range []string{"usage of Foo", "show usage Foo", "find usage Foo", "uses of Foo", "use Foo", "USAGE Foo"} {
			got, _ := r.handleUsage(q, cls(classifier.QueryTypeUsage))
			if !strings.HasPrefix(got, "Usage of 'Foo'") {
				t.Errorf("%q -> %q", q, got)
			}
		}
	})
	t.Run("unreadable file is reported inline", func(t *testing.T) {
		r := newUsageRouter(t, filepath.Join(dir, "gone.go")+":3")
		got, _ := r.handleUsage("", cls(classifier.QueryTypeUsage, "Foo"))
		if !strings.Contains(got, "could not read signature") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("unknown symbol", func(t *testing.T) {
		r := newUsageRouter(t, src+":3")
		got, _ := r.handleUsage("", cls(classifier.QueryTypeUsage, "Bar"))
		if got != "'Bar' not found in knowledge base." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("no entity", func(t *testing.T) {
		r := newUsageRouter(t, src+":3")
		got, _ := r.handleUsage("the", cls(classifier.QueryTypeUsage))
		if got != "Could not identify a function or class name in the query." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("malformed location does not panic", func(t *testing.T) {
		r := newUsageRouter(t, "no-colon", src+":x", src+":3")
		mustNotPanic(t, "handleUsage", func() {
			got, err := r.handleUsage("", cls(classifier.QueryTypeUsage, "Foo"))
			if err != nil || !strings.Contains(got, "func Foo(") {
				t.Errorf("good location should still be printed: (%q, %v)", got, err)
			}
		})
	})
}

func TestExtractDepQueryTerm(t *testing.T) {
	cases := []struct{ q, want string }{
		{"who imports requests", "requests"},
		{"dependency of foo", "foo"},
		{"what does main.go import", "main.go"},
		{"how many dependencies", "many"},
		{"depends on serde::Serialize", "serde::serialize"},
		{"WHO USES REQUESTS", "requests"},
		{"who imports go", "go"},
		{"who imports x", ""},
		{"list dependencies", ""},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := extractDepQueryTerm(tc.q); got != tc.want {
			t.Errorf("extractDepQueryTerm(%q) = %q, want %q", tc.q, got, tc.want)
		}
	}
}

func TestClassifyDepIntent(t *testing.T) {
	cases := []struct {
		query, entity string
		want          depIntent
	}{
		{"how many dependencies", "many", depIntentCount},
		{"total deps", "total", depIntentCount},
		{"number of imports", "imports", depIntentCount},
		{"list dependencies please", "please", depIntentAll},
		{"anything", "all", depIntentAll},
		{"anything", "list", depIntentAll},
		{"anything", "project", depIntentAll},
		{"who imports requests", "requests", depIntentWhoUses},
		{"what uses requests", "requests", depIntentWhoUses},
		{"requests is used by", "requests", depIntentWhoUses},
		{"what does main.go import", "main.go", depIntentFile},
		{"show me foo.go", "foo.go", depIntentFile},
		{"requests", "requests", depIntentLookup},
		{"", "", depIntentLookup},
		{"how many imports in main.go", "main.go", depIntentCount},
		{"who uses this project", "project", depIntentAll},
	}
	for _, tc := range cases {
		if got := classifyDepIntent(tc.query, tc.entity); got != tc.want {
			t.Errorf("classifyDepIntent(%q, %q) = %v, want %v", tc.query, tc.entity, got, tc.want)
		}
	}
}

func TestLooksLikeFilePath(t *testing.T) {
	for _, s := range []string{"a.go", "x/y.py", "lib.rs", "a.tsx", "a.hpp", "Foo.java", "a.rb", "a.php"} {
		if !looksLikeFilePath(s) {
			t.Errorf("looksLikeFilePath(%q) = false", s)
		}
	}
	for _, s := range []string{"", "go", "requests", "a.txt", "a.go.bak", "github.com/x/y"} {
		if looksLikeFilePath(s) {
			t.Errorf("looksLikeFilePath(%q) = true", s)
		}
	}
}

func TestBuildDepIndexAndMatches(t *testing.T) {
	deps := sampleDeps()
	idx := buildDepIndex(deps)
	if len(idx.entries) != 3 {
		t.Fatalf("entries = %d", len(idx.entries))
	}
	gin := idx.entries[2]
	if gin.nameLow != "github.com/gin-gonic/gin" || len(gin.segments) != 3 {
		t.Errorf("gin entry = %+v", gin)
	}
	if !reflect.DeepEqual(gin.tokens, []string{"github", "com", "gin", "gonic", "gin"}) {
		t.Errorf("gin tokens = %v", gin.tokens)
	}
	if !reflect.DeepEqual(idx.fileKeys, []string{"api/handler.go", "main.go", "src/a.py", "src/b.py", "src/lib.rs"}) {
		t.Errorf("fileKeys = %v", idx.fileKeys)
	}
	if idx.entries[0].dep != &deps[0] {
		t.Error("entries should point into the caller's slice")
	}

	t.Run("depEntry.matches", func(t *testing.T) {
		serde := buildDepIndex([]utils.ExternalDependency{{Name: "Serde::Serialize"}}).entries[0]
		if serde.rootLow != "serde" {
			t.Errorf("rootLow = %q", serde.rootLow)
		}
		for _, term := range []string{"serde", "SERIAL", "serde::ser", "ize"} {
			if !serde.matches(term) {
				t.Errorf("matches(%q) = false", term)
			}
		}
		if serde.matches("tokio") {
			t.Error("matches(tokio) = true")
		}
	})
	t.Run("filesMatching", func(t *testing.T) {
		if got := idx.filesMatching("main.go"); len(got) != 1 || got[0].Name != "github.com/gin-gonic/gin" {
			t.Errorf("exact = %v", got)
		}
		if got := idx.filesMatching("src/"); len(got) != 3 {
			t.Errorf("substring hits = %d, want 3", len(got))
		}
		if got := idx.filesMatching(".go"); len(got) != 1 {
			t.Errorf("a dependency used by two matching files is returned once, got %d", len(got))
		}
		if got := idx.filesMatching("nope"); len(got) != 0 {
			t.Errorf("no match = %v", got)
		}
	})
	t.Run("empty deps", func(t *testing.T) {
		if e := buildDepIndex(nil); len(e.entries) != 0 || len(e.fileKeys) != 0 {
			t.Errorf("got %+v", e)
		}
	})
}

func TestMatchDeps(t *testing.T) {
	deps := sampleDeps()
	if got := matchDeps(deps, "req"); len(got) != 1 || got[0].Name != "requests" {
		t.Errorf("got %v", got)
	}
	if got := matchDeps(deps, "gin"); len(got) != 1 {
		t.Errorf("got %v", got)
	}
	if got := matchDeps(deps, "zzz"); len(got) != 0 {
		t.Errorf("got %v", got)
	}
	if got := matchDeps(nil, "x"); len(got) != 0 {
		t.Errorf("got %v", got)
	}
	if got := matchDeps(deps, "REQ"); len(got) != 0 {
		t.Errorf("matchDeps expects a lowercase term, got %v", got)
	}
}

func TestDepFormatters(t *testing.T) {
	deps := sampleDeps()

	t.Run("formatMatchedDeps", func(t *testing.T) {
		got := formatMatchedDeps("requests", deps[:1])
		want := "Found 1 dependencies matching 'requests':\n\n" +
			"\u2022 requests (v2.31)\n  Source: pip\n  Import Count: 12\n  Used by (2 files):\n    - src/a.py\n    - src/b.py"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
		if strings.HasSuffix(got, "\n") {
			t.Error("output should be trimmed")
		}
	})
	t.Run("formatMatchedDeps unpinned and unused", func(t *testing.T) {
		got := formatMatchedDeps("x", []utils.ExternalDependency{{Name: "solo", Source: "go"}})
		if !strings.Contains(got, "solo") || strings.Contains(got, "Used by") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("formatAllExternalDeps", func(t *testing.T) {
		got := formatAllExternalDeps(deps)
		if !strings.HasPrefix(got, "Total External Dependencies: 3\n\n") {
			t.Errorf("header wrong: %q", got)
		}
		if !strings.Contains(got, "\u2022 requests (v2.31) [pip] - Imported 12 times across 2 files") {
			t.Errorf("row missing: %q", got)
		}
		if formatAllExternalDeps(nil) != "Total External Dependencies: 0" {
			t.Errorf("empty = %q", formatAllExternalDeps(nil))
		}
	})
	t.Run("formatDepCount", func(t *testing.T) {
		if got := formatDepCount(deps); got != "Total external dependencies tracked: 3" {
			t.Errorf("got %q", got)
		}
		if got := formatDepCount(nil); got != "Total external dependencies tracked: 0" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("formatFileImports", func(t *testing.T) {
		idx := buildDepIndex(deps)
		got := formatFileImports("src/", idx)
		if !strings.HasPrefix(got, "Imports in 'src/' (3):\n") {
			t.Errorf("header: %q", got)
		}
		iReq, iSerde := strings.Index(got, "requests"), strings.Index(got, "serde")
		if iReq < 0 || iSerde < 0 || iReq > iSerde {
			t.Errorf("rows should be sorted by name: %q", got)
		}
		if !strings.Contains(got, "(unpinned)") || !strings.Contains(got, "[cargo]") {
			t.Errorf("unpinned/source missing: %q", got)
		}
		if got := formatFileImports("nothere.go", idx); got != "No recorded imports found for 'nothere.go'." {
			t.Errorf("not found = %q", got)
		}
	})
}

func TestLoadExternalDeps(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		setDeps(t, nil)
		ref := &utils.ExternalDependencyRef{}
		fill(t, ref, M{"ExternalDependencies": S{M{"Name": "pkg", "Source": "pip", "ImportCount": 2, "UsedBy": S{"a.py"}}}})
		path := filepath.Join(t.TempDir(), "deps.json")
		writeJSON(t, path, ref)
		if err := loadExternalDeps(path); err != nil {
			t.Fatal(err)
		}
		if got := getExternalDeps(); len(got) != 1 || got[0].Name != "pkg" || got[0].ImportCount != 2 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		setDeps(t, nil)
		err := loadExternalDeps(filepath.Join(t.TempDir(), "nope.json"))
		if err == nil || !strings.Contains(err.Error(), "failed to read deps file") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("invalid JSON leaves previous deps intact", func(t *testing.T) {
		setDeps(t, sampleDeps())
		path := filepath.Join(t.TempDir(), "bad.json")
		writeFile(t, path, "{not json")
		err := loadExternalDeps(path)
		if err == nil || !strings.Contains(err.Error(), "failed to unmarshal JSON") {
			t.Errorf("err = %v", err)
		}
		if len(getExternalDeps()) != 3 {
			t.Error("failed load must not clobber existing deps")
		}
	})
}

func TestHandleDependency(t *testing.T) {
	t.Run("who imports X returns the matching dependency", func(t *testing.T) {
		setDeps(t, sampleDeps())
		got, err := newRouter(t).handleDependency("who imports requests", nil)
		if err != nil || !strings.HasPrefix(got, "Found 1 dependencies matching 'requests':") {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("lookup results are sorted by name", func(t *testing.T) {
		setDeps(t, []utils.ExternalDependency{{Name: "zeta-lib"}, {Name: "alpha-lib"}, {Name: "mid-lib"}})
		got, _ := newRouter(t).handleDependency("who uses lib", nil)
		a, m, z := strings.Index(got, "alpha-lib"), strings.Index(got, "mid-lib"), strings.Index(got, "zeta-lib")
		if a < 0 || a >= m || m >= z {
			t.Errorf("not sorted: %q", got)
		}
	})
	t.Run("lookup is case-insensitive", func(t *testing.T) {
		setDeps(t, sampleDeps())
		got, _ := newRouter(t).handleDependency("Who Imports REQUESTS", nil)
		if !strings.Contains(got, "requests (v2.31)") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("no match", func(t *testing.T) {
		setDeps(t, sampleDeps())
		got, _ := newRouter(t).handleDependency("who imports zzz", nil)
		if got != "No dependency named 'zzz' found.\nTip: use 'list dependencies' to see all." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("count", func(t *testing.T) {
		setDeps(t, sampleDeps())
		got, _ := newRouter(t).handleDependency("how many dependencies", nil)
		if got != "Total external dependencies tracked: 3" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("all via broad entity", func(t *testing.T) {
		setDeps(t, sampleDeps())
		got, _ := newRouter(t).handleDependency("dependencies of this project", nil)
		if !strings.HasPrefix(got, "Total External Dependencies: 3") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("file intent", func(t *testing.T) {
		setDeps(t, sampleDeps())
		got, _ := newRouter(t).handleDependency("what does main.go import", nil)
		if !strings.HasPrefix(got, "Imports in 'main.go' (1):") || !strings.Contains(got, "github.com/gin-gonic/gin") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("file intent, substring path", func(t *testing.T) {
		setDeps(t, sampleDeps())
		got, _ := newRouter(t).handleDependency("imports in handler.go", nil)
		if !strings.Contains(got, "github.com/gin-gonic/gin") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("file intent, unknown file", func(t *testing.T) {
		setDeps(t, sampleDeps())
		got, _ := newRouter(t).handleDependency("what does nothere.go import", nil)
		if got != "No recorded imports found for 'nothere.go'." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("no identifiable entity", func(t *testing.T) {
		setDeps(t, sampleDeps())
		for _, q := range []string{"", "   ", "what is the"} {
			got, err := newRouter(t).handleDependency(q, nil)
			if err != nil || got != "Could not identify an entity for dependency analysis." {
				t.Errorf("%q -> (%q, %v)", q, got, err)
			}
		}
	})
	t.Run("lazy-loads external_deps.json from the working directory", func(t *testing.T) {
		setDeps(t, nil)
		ref := &utils.ExternalDependencyRef{}
		fill(t, ref, M{"ExternalDependencies": S{M{"Name": "lazydep", "Source": "pip", "ImportCount": 1}}})
		dir := t.TempDir()
		writeJSON(t, filepath.Join(dir, "external_deps.json"), ref)
		chdir(t, dir)
		got, err := newRouter(t).handleDependency("who imports lazydep", nil)
		if err != nil || !strings.Contains(got, "lazydep") {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("lazy-load failure is wrapped", func(t *testing.T) {
		setDeps(t, nil)
		chdir(t, t.TempDir())
		_, err := newRouter(t).handleDependency("who imports requests", nil)
		if err == nil || !strings.Contains(err.Error(), "could not load external deps") {
			t.Errorf("err = %v", err)
		}
	})
}

func writeMetrics(t *testing.T, r *Router, nFuncs int) {
	t.Helper()
	fns := S{}
	for i := 1; i <= nFuncs; i++ {
		fns = append(fns, M{"Name": fmt.Sprintf("fn%02d", i), "File": fmt.Sprintf("f%02d.go", i),
			"LineStart": i * 10, "LineEnd": i*10 + 5, "Complexity": 100 - i, "ImportanceScore": 0.25})
	}
	rep := &utils.MetricsReport{}
	fill(t, rep, M{
		"Metadata": M{"ProjectName": "proj", "TotalFiles": 2, "TotalLoc": 100, "TotalFunctions": nFuncs,
			"Languages": S{"go", "rust"}, "ParsedAt": "2026-01-02T03:04:05Z"},
		"TopComplexFunctions": fns,
	})
	writeJSON(t, filepath.Join(r.projectPath(), ".eulix", "kb_metrics.json"), rep)
}

func TestHandleMetrics(t *testing.T) {
	rowRE := regexp.MustCompile(`(?m)^ +\d+\. fn`)

	t.Run("function metrics", func(t *testing.T) {
		r := newRouter(t)
		writeMetrics(t, r, 3)
		got, err := r.handleMetrics("complexity of fn02", cls(classifier.QueryTypeMetrics, "fn02"))
		want := "Metrics for fn02 (f02.go, lines 20\u201325)\n  Cyclomatic complexity : 98\n  LOC                   : 6\n  Importance            : 0.25\n"
		if err != nil || got != want {
			t.Errorf("got (%q, %v)\nwant %q", got, err, want)
		}
	})
	t.Run("unknown function", func(t *testing.T) {
		r := newRouter(t)
		writeMetrics(t, r, 3)
		got, _ := r.handleMetrics("complexity of nosuch", cls(classifier.QueryTypeMetrics, "nosuch"))
		if got != "'nosuch' not found in metrics index." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("project summary lists at most ten rows", func(t *testing.T) {
		r := newRouter(t)
		writeMetrics(t, r, 12)
		got, err := r.handleMetrics("project metrics", cls(classifier.QueryTypeMetrics))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{
			"Project metrics: proj\n", "  Files       : 2\n", "  Total LOC   : 100\n",
			"  Functions   : 12\n", "  Languages   : go, rust\n", "  Parsed at   : ", "Top 10 most complex functions:\n",
		} {
			if !strings.Contains(got, s) {
				t.Errorf("missing %q in:\n%s", s, got)
			}
		}
		if n := len(rowRE.FindAllString(got, -1)); n != 10 {
			t.Errorf("rows = %d, want 10", n)
		}
		if !strings.Contains(got, " 1. fn01") || !strings.Contains(got, "10. fn10") || strings.Contains(got, "fn11") {
			t.Errorf("row numbering/limit wrong:\n%s", got)
		}
	})
	t.Run("summary triggers", func(t *testing.T) {
		for _, q := range []string{"project stats", "overall numbers", "give me a summary", "all functions", "total count"} {
			r := newRouter(t)
			writeMetrics(t, r, 2)
			got, _ := r.handleMetrics(q, cls(classifier.QueryTypeMetrics, "fn01"))
			if !strings.HasPrefix(got, "Project metrics:") {
				t.Errorf("%q should give the project summary, got %q", q, got)
			}
		}
	})
	t.Run("no symbols means project summary", func(t *testing.T) {
		r := newRouter(t)
		writeMetrics(t, r, 2)
		got, _ := r.handleMetrics("complexity", cls(classifier.QueryTypeMetrics))
		if !strings.HasPrefix(got, "Project metrics:") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("summary with no functions", func(t *testing.T) {
		r := newRouter(t)
		writeMetrics(t, r, 0)
		got, err := r.handleMetrics("project", cls(classifier.QueryTypeMetrics))
		if err != nil || !strings.HasSuffix(got, "Top 10 most complex functions:\n") {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		r := newRouter(t)
		_, err := r.handleMetrics("x", cls(classifier.QueryTypeMetrics, "fn01"))
		if err == nil || !strings.Contains(err.Error(), "failed to read metrics file") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("corrupt file", func(t *testing.T) {
		r := newRouter(t)
		writeFile(t, filepath.Join(r.projectPath(), ".eulix", "kb_metrics.json"), "{oops")
		_, err := r.handleMetrics("x", cls(classifier.QueryTypeMetrics, "fn01"))
		if err == nil || !strings.Contains(err.Error(), "failed to parse metrics JSON") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestHandleEntryPoints(t *testing.T) {
	writeEPs := func(t *testing.T, r *Router, eps S) {
		ref := &utils.EntryPointsRef{}
		fill(t, ref, M{"EntryPoints": eps})
		writeJSON(t, filepath.Join(r.projectPath(), ".eulix", "kb_entry_points.json"), ref)
	}
	httpEP := M{"EntryType": "http", "Path": "/users", "Methods": S{"GET", "POST"}, "Handler": "handleUsers", "File": "api.go", "Line": 10}
	mainEP := M{"EntryType": "main", "Handler": "main", "File": "main.go", "Line": 5}

	t.Run("route entry point", func(t *testing.T) {
		r := newRouter(t)
		writeEPs(t, r, S{httpEP})
		got, err := r.handleEntryPoints("", nil)
		want := "Entry points \n\u2500\u2500 HTTP \u2500\u2500\n  [GET, POST] /users \u2192 handleUsers  (api.go:10)\n\n"
		if err != nil || got != want {
			t.Errorf("got (%q, %v)\nwant %q", got, err, want)
		}
	})
	t.Run("entry point without a path", func(t *testing.T) {
		r := newRouter(t)
		writeEPs(t, r, S{mainEP})
		got, _ := r.handleEntryPoints("", nil)
		if !strings.Contains(got, "\u2500\u2500 MAIN \u2500\u2500\n  main  (main.go:5)\n") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("groups by type", func(t *testing.T) {
		r := newRouter(t)
		writeEPs(t, r, S{httpEP, mainEP, M{"EntryType": "http", "Path": "/x", "Methods": S{"GET"}, "Handler": "hx", "File": "x.go", "Line": 1}})
		got, _ := r.handleEntryPoints("", nil)
		if strings.Count(got, "HTTP") != 1 || strings.Count(got, "MAIN") != 1 {
			t.Errorf("groups not merged:\n%s", got)
		}
		if !strings.Contains(got, "handleUsers") || !strings.Contains(got, "hx") {
			t.Errorf("entries missing:\n%s", got)
		}
	})
	t.Run("appends architecture style when known", func(t *testing.T) {
		r := newRouter(t)
		writeEPs(t, r, S{mainEP})
		r.Patterns = &utils.PatternInfo{}
		fill(t, r.Patterns, M{"ArchitectureStyle": "layered"})
		got, _ := r.handleEntryPoints("", nil)
		if !strings.HasSuffix(got, "Architecture style : layered\n") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("patterns present but style nil", func(t *testing.T) {
		r := newRouter(t)
		writeEPs(t, r, S{mainEP})
		r.Patterns = &utils.PatternInfo{}
		if got, _ := r.handleEntryPoints("", nil); strings.Contains(got, "Architecture style") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("no entry points", func(t *testing.T) {
		r := newRouter(t)
		writeEPs(t, r, S{})
		if got, err := r.handleEntryPoints("", nil); err != nil || got != "Entry points \n" {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("missing and corrupt files", func(t *testing.T) {
		r := newRouter(t)
		if _, err := r.handleEntryPoints("", nil); err == nil || !strings.Contains(err.Error(), "failed to read entry points") {
			t.Errorf("err = %v", err)
		}
		writeFile(t, filepath.Join(r.projectPath(), ".eulix", "kb_entry_points.json"), "{")
		if _, err := r.handleEntryPoints("", nil); err == nil || !strings.Contains(err.Error(), "failed to parse kb_entry_points.json") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestHandleFileStructure(t *testing.T) {
	t.Run("kb not loaded", func(t *testing.T) {
		got, err := newRouter(t).handleFileStructure("what is in file main.go")
		if err != nil || !strings.Contains(got, "not loaded") {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("project listing", func(t *testing.T) {
		r := newRouter(t)
		r.setKB(t, M{"pkg/a.go": fileSpec()})
		got, _ := r.handleFileStructure("show me the structure")
		want := "Project: proj  (1 files, 100 LOC)\n\n  pkg/a.go  [go, 40 LOC, 1 fns, 1 classes]\n"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})
	t.Run("single file by substring", func(t *testing.T) {
		r := newRouter(t)
		r.setKB(t, M{"pkg/a.go": fileSpec(), "pkg/other.rs": M{"Language": "rust", "Loc": 1}})
		got, _ := r.handleFileStructure("what is in file a.go")
		if !strings.HasPrefix(got, "File: pkg/a.go  [go, 40 LOC]\n") || !strings.Contains(got, "Functions (1):") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("file not found", func(t *testing.T) {
		r := newRouter(t)
		r.setKB(t, M{"pkg/a.go": fileSpec()})
		got, _ := r.handleFileStructure("show zzz.go")
		if got != "File matching 'zzz.go' not found in knowledge base." {
			t.Errorf("got %q", got)
		}
	})
}

func TestHandleTodosQuery(t *testing.T) {
	t.Run("kb not loaded", func(t *testing.T) {
		got, _ := newRouter(t).handleTodosQuery("", nil)
		if !strings.Contains(got, "not loaded") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty kb", func(t *testing.T) {
		r := newRouter(t)
		r.setKB(t, M{"a.go": M{"Language": "go", "Loc": 1}})
		got, _ := r.handleTodosQuery("", nil)
		if got != "No TODOs or security notes found in the knowledge base." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("single file exact output", func(t *testing.T) {
		r := newRouter(t)
		r.setKB(t, M{"pkg/a.go": fileSpec()})
		got, _ := r.handleTodosQuery("", nil)
		want := "\u26a0 Security notes (1):\n  [secret] pkg/a.go:9 \u2014 hardcoded\n\n" +
			"\U0001F534 High priority TODOs (1):\n  pkg/a.go:3 \u2014 fix\n\n"
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})
	t.Run("priority bucketing: unknown priorities fall into low", func(t *testing.T) {
		r := newRouter(t)
		td := func(p string) M { return M{"Line": 1, "Text": p + "-todo", "Priority": p} }
		r.setKB(t, M{"a.go": M{"Language": "go", "Loc": 1, "Todos": S{td("high"), td("medium"), td("low"), td(""), td("urgent")}}})
		got, _ := r.handleTodosQuery("", nil)
		for _, s := range []string{"High priority TODOs (1):", "Medium priority TODOs (1):", "Low priority TODOs (3):"} {
			if !strings.Contains(got, s) {
				t.Errorf("missing %q in:\n%s", s, got)
			}
		}
		h, m, l := strings.Index(got, "High"), strings.Index(got, "Medium"), strings.Index(got, "Low")
		if h >= m || m >= l {
			t.Errorf("sections out of order:\n%s", got)
		}
	})
	t.Run("aggregates across files", func(t *testing.T) {
		r := newRouter(t)
		one := func(text string) M {
			return M{"Language": "go", "Loc": 1, "Todos": S{M{"Line": 1, "Text": text, "Priority": "high"}}}
		}
		r.setKB(t, M{"a.go": one("first"), "b.go": one("second")})
		got, _ := r.handleTodosQuery("", nil)
		if !strings.Contains(got, "High priority TODOs (2):") || !strings.Contains(got, "first") || !strings.Contains(got, "second") {
			t.Errorf("got %q", got)
		}
	})
}

func TestHandleCodeGeneration(t *testing.T) {
	got, err := newRouter(t).handleCodeGeneration()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"cannot safely generate implementation code", "What I CAN help with instead", "architecture or data-flow"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q", s)
		}
	}
}

func TestHasSourceCode(t *testing.T) {
	mk := func(chunks ...string) *utils.ContextWindow {
		items := S{}
		for _, c := range chunks {
			items = append(items, M{"Content": c})
		}
		ctx := &utils.ContextWindow{}
		fill(t, ctx, M{"Chunks": items})
		return ctx
	}
	cases := []struct {
		name string
		ctx  *utils.ContextWindow
		want bool
	}{
		{"no chunks", mk(), false},
		{"plain text", mk("hello", "world"), false},
		{"fenced block", mk("a", "```go\nx := 1\n```"), true},
		{"inline backticks only", mk("use `x` and ``y``"), false},
		{"fence in later chunk", mk("a", "b", "c```"), true},
	}
	for _, tc := range cases {
		if got := hasSourceCode(tc.ctx); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRouterPlumbing(t *testing.T) {
	t.Run("SetCurrentChecksum", func(t *testing.T) {
		r := newRouter(t)
		r.SetCurrentChecksum("abc123")
		if r.currentChecksum != "abc123" {
			t.Errorf("checksum = %q", r.currentChecksum)
		}
	})
	t.Run("Close with no context builder", func(t *testing.T) {
		r := newRouter(t)
		if err := r.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	t.Run("ensureContextBuilder is a no-op when already built", func(t *testing.T) {
		r := newRouter(t)
		r.contextBuilder = &retrieval.ContextBuilder{}
		r.config = nil // must not be touched
		if err := r.ensureContextBuilder(); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("ensureContextBuilder reports a missing source root", func(t *testing.T) {
		r := newRouter(t)
		missing := filepath.Join(r.projectPath(), "does", "not", "exist")
		fill(t, r.config, M{"Project": M{"Path": missing}})
		err := r.ensureContextBuilder()
		if err == nil || !strings.Contains(err.Error(), "source root does not exist: "+missing) {
			t.Errorf("err = %v", err)
		}
		if r.contextBuilder != nil {
			t.Error("contextBuilder should stay nil on failure")
		}
	})
}

var llmQueries = []string{
	"how does foo work", "implement foo", "architecture of foo", "debug this thing",
	"compare foo and bar", "refactor foo", "performance of foo", "data flow in foo",
	"security audit needed", "example of foo", "test foo",
}

func missingRootRouter(t *testing.T) *Router {
	r := newRouter(t)
	fill(t, r.config, M{"Project": M{"Path": filepath.Join(r.projectPath(), "missing")}})
	return r
}

func TestQueryEngine_Routing(t *testing.T) {
	t.Run("code generation is refused without touching the context builder", func(t *testing.T) {
		got, err := missingRootRouter(t).QueryEngine("write code for me")
		if err != nil || !strings.Contains(got, "cannot safely generate implementation code") {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("todos", func(t *testing.T) {
		r := newRouter(t)
		r.setKB(t, M{"a.go": fileSpec()})
		got, err := r.QueryEngine("todo list")
		if err != nil || !strings.Contains(got, "High priority TODOs") {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("file structure without kb", func(t *testing.T) {
		got, err := newRouter(t).QueryEngine("what is in file main.go")
		if err != nil || !strings.Contains(got, "not loaded") {
			t.Errorf("got (%q, %v)", got, err)
		}
	})
	t.Run("handler errors are returned, not swallowed", func(t *testing.T) {
		r := newRouter(t)
		if got, err := r.QueryEngine("entry points"); err == nil || got != "" || !strings.Contains(err.Error(), "failed to read entry points") {
			t.Errorf("entry points: (%q, %v)", got, err)
		}
		if got, err := r.QueryEngine("complexity of foo"); err == nil || got != "" || !strings.Contains(err.Error(), "failed to read metrics file") {
			t.Errorf("metrics: (%q, %v)", got, err)
		}
	})
	t.Run("non-LLM handlers answer without error", func(t *testing.T) {
		setDeps(t, sampleDeps())
		r := callGraphFixture(t)
		r.setIndex(t, M{"FunctionsByName": M{"foo": S{"a.go:1"}}})
		for _, q := range []string{"find the foo", "what uses foo", "call graph for foo", "dependency of foo"} {
			got, err := r.QueryEngine(q)
			if err != nil || strings.TrimSpace(got) == "" {
				t.Errorf("%q -> (%q, %v)", q, got, err)
			}
		}
	})
	t.Run("LLM-backed types fail with a clear error when the source root is missing", func(t *testing.T) {
		r := missingRootRouter(t)
		for _, q := range llmQueries {
			got, err := r.QueryEngine(q)
			if err == nil || got != "" || !strings.Contains(err.Error(), "source root does not exist") {
				t.Errorf("%q -> (%q, %v)", q, got, err)
			}
		}
	})
	t.Run("works with a nil cache", func(t *testing.T) {
		r := newRouter(t)
		r.cache = nil
		mustNotPanic(t, "QueryEngine nil cache", func() { _, _ = r.QueryEngine("todo list") })
	})
	t.Run("empty and junk queries do not panic", func(t *testing.T) {
		r := missingRootRouter(t)
		for _, q := range []string{"", "   ", "???", "\x00", "xyzzy nothing here"} {
			mustNotPanic(t, fmt.Sprintf("QueryEngine(%q)", q), func() { _, _ = r.QueryEngine(q) })
		}
	})
}

func TestPromptOrAnswer_Routing(t *testing.T) {
	t.Run("direct-answer types", func(t *testing.T) {
		r := newRouter(t)
		r.setKB(t, M{"a.go": fileSpec()})
		if got, err := r.PromptOrAnswer("todo list"); err != nil || !strings.Contains(got, "High priority TODOs") {
			t.Errorf("todos: (%q, %v)", got, err)
		}
		if got, err := r.PromptOrAnswer("write code for me"); err != nil || !strings.Contains(got, "cannot safely generate") {
			t.Errorf("codegen: (%q, %v)", got, err)
		}
		if _, err := r.PromptOrAnswer("entry points"); err == nil {
			t.Error("entry points: expected error without kb_entry_points.json")
		}
	})
	t.Run("prompt-building types fail cleanly when the source root is missing", func(t *testing.T) {
		r := missingRootRouter(t)
		for _, q := range llmQueries {
			got, err := r.PromptOrAnswer(q)
			if err == nil || got != "" || !strings.Contains(err.Error(), "source root does not exist") {
				t.Errorf("%q -> (%q, %v)", q, got, err)
			}
		}
	})
}

func TestCotHeader(t *testing.T) {
	class := &classifier.Classification{
		Type: classifier.QueryTypeDebug, Symbols: []string{"A", "B"}, Keywords: []string{"k1"}, Confidence: 0.875,
	}
	t.Run("with source", func(t *testing.T) {
		got := cotHeader("why crash?", class, true, 0.5)
		for _, s := range []string{
			"REAL SOURCE CODE (\u224850%)",
			"Relevant symbols : [A B]\n",
			"Key terms        : [k1]\n",
			"Query type       : Debug  (confidence 87.5%)\n",
			"Question you need to answer is  why crash?\n\n",
			"=== CHAIN-OF-THOUGHT INSTRUCTIONS ===",
			"<reasoning>", "<answer>",
			"Step 1", "Step 2", "Step 3", "Step 4",
		} {
			if !strings.Contains(got, s) {
				t.Errorf("missing %q in:\n%s", s, got)
			}
		}
		if strings.Contains(got, "AST metadata ONLY") {
			t.Error("source branch should not claim metadata-only")
		}
	})
	t.Run("without source", func(t *testing.T) {
		got := cotHeader("q", class, false, 0.5)
		if !strings.Contains(got, "AST metadata ONLY") || strings.Contains(got, "REAL SOURCE CODE") {
			t.Errorf("got:\n%s", got)
		}
	})
	t.Run("optional lines are omitted when empty", func(t *testing.T) {
		got := cotHeader("q", &classifier.Classification{Type: classifier.QueryTypeDebug}, true, 0.5)
		if strings.Contains(got, "Relevant symbols") || strings.Contains(got, "Key terms") {
			t.Errorf("got:\n%s", got)
		}
	})
	t.Run("query containing format verbs is not interpreted", func(t *testing.T) {
		got := cotHeader("100% of %s %d %v", &classifier.Classification{}, false, 0)
		if !strings.Contains(got, "100% of %s %d %v") || strings.Contains(got, "%!") {
			t.Errorf("got:\n%s", got)
		}
	})
	t.Run("unknown type prints Unknown", func(t *testing.T) {
		if got := cotHeader("q", &classifier.Classification{}, false, 0); !strings.Contains(got, "Query type       : Unknown") {
			t.Errorf("got:\n%s", got)
		}
	})
}

func TestCotFooter(t *testing.T) {
	got := cotFooter()
	for _, s := range []string{"HONESTY CONTRACT", "Cite file + line range", "Not visible in context", "I infer from signature"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q", s)
		}
	}
}

func TestBuildPromptString(t *testing.T) {
	class := cls(classifier.QueryTypeDebug, "Foo")
	got := BuildPromptString("the query", class, true, "<<TASK BODY>>", 0.5)
	iHeader := strings.Index(got, "Question you need to answer is  the query")
	iTask := strings.Index(got, " TASK \n")
	iBody := strings.Index(got, "<<TASK BODY>>")
	iFooter := strings.LastIndex(got, "HONESTY CONTRACT")
	if iHeader < 0 || iHeader > iTask || iTask > iBody || iBody > iFooter {
		t.Errorf("sections out of order (%d %d %d %d):\n%s", iHeader, iTask, iBody, iFooter, got)
	}
	if !strings.HasSuffix(got, cotFooter()) {
		t.Error("prompt should end with the footer")
	}
	if got := BuildPromptString("q", class, false, "", 0.5); !strings.Contains(got, " TASK \n") {
		t.Error("empty task body should still yield a well-formed prompt")
	}
}

func promptRouter(t *testing.T) *Router {
	r := newRouter(t)
	r.setIndex(t, M{
		"FunctionsByName": M{"Foo": S{"a.go:1", "a2.go:2"}},
		"TypesByName":     M{"Foo": S{"t.go:3"}, "Widget": S{"w.go:4"}},
	})
	r.setGraph(t, M{
		"Nodes": S{node("func_Foo", "a.go", "function", 0), node("func_Bar", "b.go", "function", 0), node("func_Baz", "c.go", "function", 0)},
		"Edges": S{edge("func_Foo", "func_Bar"), edge("func_Baz", "func_Foo")},
	})
	return r
}

func TestTaskBodies_AllWellFormed(t *testing.T) {
	r := promptRouter(t)
	if len(taskBodies) == 0 {
		t.Fatal("taskBodies is empty")
	}
	for qt, fn := range taskBodies {
		t.Run(qt.String(), func(t *testing.T) {
			for _, class := range []*classifier.Classification{
				cls(qt), cls(qt, "Foo"), cls(qt, "Foo", "Bar", "Missing"),
			} {
				body := fn(r, "does 100% of %s work?", class)
				if strings.TrimSpace(body) == "" {
					t.Fatal("empty body")
				}
				if strings.Contains(body, "%!") {
					t.Errorf("fmt error marker in body (verb/arg mismatch):\n%s", body)
				}
				if qt != classifier.QueryTypeComparison || len(class.Symbols) >= 2 {
					if !strings.Contains(body, "HONESTY CONTRACT:") {
						t.Errorf("citation contract missing for %d symbols", len(class.Symbols))
					}
				}
			}
		})
	}
}

func TestGetTaskBody(t *testing.T) {
	r := promptRouter(t)

	t.Run("always prefixed with language note and confidence legend", func(t *testing.T) {
		for qt := range taskBodies {
			got := getTaskBody(r, "q", cls(qt, "Foo", "Bar"))
			if !strings.HasPrefix(got, languageAgnosticNote+"\n"+confidenceLegend+"\n") {
				t.Errorf("%v: prefix missing", qt)
			}
		}
	})
	t.Run("types without a body fall back to understanding", func(t *testing.T) {
		want := getTaskBody(r, "q", cls(classifier.QueryTypeUnderstanding))
		for _, qt := range []classifier.QueryType{
			classifier.QueryTypeLocation, classifier.QueryTypeUsage, classifier.QueryTypeMetrics,
			classifier.QueryTypeTodos, classifier.QueryTypeCodeGeneration, classifier.QueryType(0), classifier.QueryType(99),
		} {
			if got := getTaskBody(r, "q", cls(qt)); got != want {
				t.Errorf("%v did not fall back to the understanding body", qt)
			}
		}
	})
	t.Run("comparison with fewer than two symbols asks for more", func(t *testing.T) {
		for _, syms := range [][]string{nil, {"Foo"}} {
			got := getTaskBody(r, "compare", cls(classifier.QueryTypeComparison, syms...))
			if !strings.Contains(got, "Not enough symbols") || strings.Contains(got, "Compare the following symbols") {
				t.Errorf("symbols=%v:\n%s", syms, got)
			}
		}
	})
	t.Run("comparison lists the symbols", func(t *testing.T) {
		got := getTaskBody(r, "compare", cls(classifier.QueryTypeComparison, "Foo", "Bar"))
		if !strings.Contains(got, "Compare the following symbols: [Foo Bar]") {
			t.Errorf("got:\n%s", got)
		}
	})
	t.Run("implementation lists known locations for functions and types", func(t *testing.T) {
		got := getTaskBody(r, "q", cls(classifier.QueryTypeImplementation, "Foo", "Widget", "Missing"))
		if !strings.Contains(got, "Known file locations: [a.go:1 a2.go:2 t.go:3 w.go:4]") {
			t.Errorf("got:\n%s", got)
		}
	})
	t.Run("implementation with no known symbol", func(t *testing.T) {
		got := getTaskBody(r, "q", cls(classifier.QueryTypeImplementation, "Missing"))
		if !strings.Contains(got, "Known file locations: []") {
			t.Errorf("got:\n%s", got)
		}
	})
	t.Run("architecture embeds call graph excerpt", func(t *testing.T) {
		got := getTaskBody(r, "q", cls(classifier.QueryTypeArchitecture, "Foo"))
		for _, s := range []string{"\nFoo  (@ a.go)\n", "  calls   : [Bar]\n", "  calledBy: [Baz]\n"} {
			if !strings.Contains(got, s) {
				t.Errorf("missing %q in:\n%s", s, got)
			}
		}
	})
	t.Run("architecture omits empty edge lists and unknown symbols", func(t *testing.T) {
		got := getTaskBody(r, "q", cls(classifier.QueryTypeArchitecture, "Bar", "Ghost"))
		if strings.Contains(got, "calls   :") || strings.Contains(got, "Ghost") {
			t.Errorf("got:\n%s", got)
		}
		if !strings.Contains(got, "calledBy: [Foo]") {
			t.Errorf("Bar is called by Foo:\n%s", got)
		}
	})
	t.Run("data flow prompt is built from the same body table", func(t *testing.T) {
		got := getTaskBody(r, "q", cls(classifier.QueryTypeDataFlow, "Foo"))
		if !strings.Contains(got, "\nFoo \u2192 [Bar]") {
			t.Errorf("got:\n%s", got)
		}
	})
	t.Run("testing body embeds the query verbatim", func(t *testing.T) {
		got := getTaskBody(r, "100% of %s and %d", cls(classifier.QueryTypeTesting))
		if !strings.Contains(got, "Generate a testing strategy for: 100% of %s and %d") {
			t.Errorf("got:\n%s", got)
		}
	})
	t.Run("safety-critical bodies keep their guard rails", func(t *testing.T) {
		checks := map[classifier.QueryType][]string{
			classifier.QueryTypeSecurity:    {"Do not claim a vulnerability exists unless the evidence supports it"},
			classifier.QueryTypeDebug:       {"Do not suggest fixes for logic you cannot see"},
			classifier.QueryTypeRefactoring: {"Do not recommend changes to code you cannot see"},
			classifier.QueryTypeExample:     {"Do not invent parameter values"},
		}
		for qt, wants := range checks {
			got := getTaskBody(r, "q", cls(qt, "Foo"))
			for _, w := range wants {
				if !strings.Contains(got, w) {
					t.Errorf("%v missing %q", qt, w)
				}
			}
		}
	})
}

func FuzzHelpers(f *testing.F) {
	for _, s := range []string{
		"", " ", "func_foo", "method_A_b", "::", "usage of Foo", "where is Foo?", "main.go:12",
		"def f(x: int) -> str:", "func f() interface{} {", "who imports requests", "\x00", "\xff\xfe",
		"日本語 のクエリ", strings.Repeat("(", 100), strings.Repeat("a ", 200),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = bareID(s)
		_ = callGraphShortName(s)
		_ = extractEntityName(s)
		_ = extractFilePath(s)
		_ = isLikelySymbol(s)
		_ = extractDepQueryTerm(s)
		_ = looksLikeFilePath(s)
		_ = stripCommandPrefix(s, "usage of", "usage", "use", "uses of", "show usage", "find usage")
		_ = fuzzyScore(strings.ToLower(s), "handler")
		_ = dedupe(strings.Fields(s))

		if file, _, err := parseLocation(s); err == nil && !strings.HasPrefix(s, file+":") {
			t.Fatalf("parseLocation(%q) returned file %q that is not a prefix", s, file)
		}
		ls := strings.Split(s, "\n")
		for _, lang := range []language{langGo, langRust, langPython, langTS, langC} {
			sig, err := parseSig(append([]string(nil), ls...), 0, lang)
			if err != nil {
				t.Fatalf("parseSig error: %v", err)
			}
			if strings.Count(sig, "\n") > 40 {
				t.Fatalf("parseSig produced more than 40 lines for lang %v", lang)
			}
		}
		if got := extractEntityName(s); got != "" && !strings.Contains(s, got) {
			t.Fatalf("extractEntityName(%q) = %q, not a substring", s, got)
		}
	})
}
