// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

/*
Package query: the agent-facing API.

The handlers in router_handlers.go render answers for a person reading a
terminal. An agent in a tool loop needs something else: the exact answer to a
structural question (who calls X, what does X call, where is X defined, how
does A reach B), as few tokens as possible, and a statement of whether the
answer is complete so it does not re-check with grep.

Everything here returns plain structs; the MCP server renders them.
*/
package query

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"eulix/internal/checksum"
	"eulix/internal/query/classifier"
	"eulix/internal/query/retrieval"
	"eulix/internal/utils"
)

// ========== Symbol definitions (from kb.fb) ==========

// symDef is one function, method or type definition from the knowledge base.
type symDef struct {
	ID        string
	Name      string // bare name: "handleRequest"
	Qualified string // "Bifrost.handleRequest" for a method, else Name
	Kind      string // function | method | class
	File      string
	Start     int
	End       int
	Signature string
	Docstring string
	Methods   []utils.KBFunction // classes only
}

// Location renders "file:line".
func (d *symDef) Location() string {
	if d.Start > 0 {
		return fmt.Sprintf("%s:%d", d.File, d.Start)
	}
	return d.File
}

// symTable indexes the KB's definitions by ID and by lower-cased name.
type symTable struct {
	byID   map[string]*symDef
	byName map[string][]*symDef // lower(Name) and lower(Qualified)
	byFile map[string][]*symDef // functions and methods per file
}

// ensureSymTable builds the definition table from kb.fb once.
func (r *Router) ensureSymTable() (*symTable, error) {
	r.mu.RLock()
	t := r.syms
	r.mu.RUnlock()
	if t != nil {
		return t, nil
	}
	if err := r.ensureKB(); err != nil {
		return nil, fmt.Errorf("load knowledge base: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.syms != nil {
		return r.syms, nil
	}
	t = &symTable{byID: map[string]*symDef{}, byName: map[string][]*symDef{}, byFile: map[string][]*symDef{}}
	add := func(d *symDef) {
		if d.ID != "" {
			t.byID[d.ID] = d
		}
		if d.Kind != "class" && d.Start > 0 {
			t.byFile[d.File] = append(t.byFile[d.File], d)
		}
		keys := []string{strings.ToLower(d.Name)}
		if d.Qualified != d.Name {
			keys = append(keys, strings.ToLower(d.Qualified))
		}
		for _, k := range keys {
			t.byName[k] = append(t.byName[k], d)
		}
	}
	files := make([]string, 0, len(r.kb.Structure))
	for p := range r.kb.Structure {
		files = append(files, p)
	}
	sort.Strings(files) // deterministic order of same-name definitions
	for _, file := range files {
		fd := r.kb.Structure[file]
		for _, fn := range fd.Functions {
			add(&symDef{ID: fn.ID, Name: fn.Name, Qualified: fn.Name, Kind: "function", File: file,
				Start: fn.LineStart, End: fn.LineEnd, Signature: fn.Signature, Docstring: fn.Docstring})
		}
		for _, cl := range fd.Classes {
			add(&symDef{ID: cl.ID, Name: cl.Name, Qualified: cl.Name, Kind: "class", File: file,
				Start: cl.LineStart, End: cl.LineEnd, Docstring: cl.Docstring, Methods: cl.Methods})
			for _, m := range cl.Methods {
				add(&symDef{ID: m.ID, Name: m.Name, Qualified: cl.Name + "." + m.Name, Kind: "method", File: file,
					Start: m.LineStart, End: m.LineEnd, Signature: m.Signature, Docstring: m.Docstring})
			}
		}
	}
	r.syms = t
	return t, nil
}

// EnclosingSymbol names the innermost function or method whose lines contain
// file:line, or "" when none does (or the KB is unavailable).
func (r *Router) EnclosingSymbol(file string, line int) string {
	t, err := r.ensureSymTable()
	if err != nil {
		return ""
	}
	var best *symDef
	for _, d := range t.byFile[file] {
		if line < d.Start || line > d.End {
			continue
		}
		if best == nil || d.End-d.Start < best.End-best.Start {
			best = d
		}
	}
	if best == nil {
		return ""
	}
	return best.Qualified
}

// normalizeSymbol turns the ways an agent writes a symbol into the
// "Type.method" form the index uses: "SearchWorker::search", "Foo#bar",
// "foo()", "`Foo.bar`".
func normalizeSymbol(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`'\"")
	s = strings.TrimSuffix(s, "()")
	s = strings.ReplaceAll(s, "::", ".")
	s = strings.ReplaceAll(s, "#", ".")
	s = strings.ReplaceAll(s, "->", ".")
	// A receiver written Go-style: "(*Bifrost).handleRequest".
	s = strings.NewReplacer("(*", "", "(", "", ")", "", "*", "").Replace(s)
	return strings.Trim(s, ". ")
}

// lookupDefs returns every definition matching name: exact qualified match
// first, then a bare-name match, then a "Type.method" suffix match.
func (t *symTable) lookupDefs(name string) []*symDef {
	norm := normalizeSymbol(name)
	low := strings.ToLower(norm)
	if low == "" {
		return nil
	}
	if ds := t.byName[low]; len(ds) > 0 {
		return preferExactCase(ds, norm)
	}
	// "pkg.Type.method" or "module.func": try progressively shorter suffixes.
	parts := strings.Split(norm, ".")
	for i := 1; i < len(parts); i++ {
		suffix := strings.Join(parts[i:], ".")
		if ds := t.byName[strings.ToLower(suffix)]; len(ds) > 0 {
			return preferExactCase(ds, suffix)
		}
	}
	return nil
}

// preferExactCase keeps the definitions spelled exactly like name when there
// are any: "Bifrost" is the type, not the method "liveUsage.bifrost".
func preferExactCase(ds []*symDef, name string) []*symDef {
	var exact []*symDef
	for _, d := range ds {
		if d.Name == name || d.Qualified == name {
			exact = append(exact, d)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return ds
}

// ========== Call reports (callers / callees) ==========

// CallSite is one edge of a call report.
type CallSite struct {
	// Name is the other end of the edge: the caller in a callers report,
	// the callee in a callees report.
	Name string
	ID   string
	// File and Line locate the call expression (always in the caller's file).
	File string
	Line int
	// Text is the source line of the call, trimmed. Empty when unknown.
	Text string
	// Depth is 1 for direct edges, 2 for an edge one hop further, and so on.
	Depth int
	// Via is the symbol this edge attaches to when Depth > 1.
	Via  string
	Test bool
	// Edge is "" for a resolved call, else how the edge was inferred:
	// "async", "dispatch" or "inferred" (see CallGraphIdx.Inferred).
	Edge string
}

// CallReport answers "who calls X" or "what does X call" from the call graph.
type CallReport struct {
	Symbol    string // resolved, bare: "Bifrost.shouldTryFallbacks"
	SymbolID  string
	Location  string // definition "file:line"
	Callees   bool   // false = callers report
	Depth     int
	Sites     []CallSite
	Omitted   int      // sites left out by the limit
	Others    []string // other symbols the name also matched
	Found     bool
	Suggested []string // closest names when not found

	// Name scan (direct callers of a specific name, see scanName): the uses
	// the call graph has no edge for.
	Scanned     bool
	ByName      []NameUse // calls whose receiver the graph could not resolve
	AsValue     []NameUse // the name passed as a value: a handler, callback or goroutine target
	NameOmitted int       // name uses left out by the limit
	// Twins are other definitions with the same name: a name use may be theirs.
	Twins []string
}

// Complete reports whether the report answers "who calls X" in full.
//
// Without a name scan, an empty report is never complete: a static graph
// misses calls it could not resolve (through a field, an interface, a
// closure), so "no callers" is a claim to verify, not a fact. With a scan,
// every use of the name in the indexed code is listed, and the report is
// complete unless a use could belong to another definition of the same name.
func (c *CallReport) Complete() bool {
	if !c.Found || c.Omitted > 0 || c.NameOmitted > 0 {
		return false
	}
	if c.Scanned {
		if len(c.ByName)+len(c.AsValue) == 0 {
			return true
		}
		// Name uses are direct; deeper levels were not walked for them.
		return len(c.Twins) == 0 && c.Depth <= 1
	}
	return len(c.Sites) > 0
}

// DirectCount counts depth-1 sites, split into non-test and test.
func (c *CallReport) DirectCount() (code, tests int) {
	for _, s := range c.Sites {
		if s.Depth != 1 {
			continue
		}
		if s.Test {
			tests++
		} else {
			code++
		}
	}
	return code, tests
}

// isTestPath reports whether a file is test code by common conventions.
func isTestPath(p string) bool {
	p = strings.ToLower(p)
	base := path.Base(p)
	switch {
	case strings.Contains(base, "_test."), strings.HasPrefix(base, "test_"),
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."),
		strings.HasSuffix(strings.TrimSuffix(base, path.Ext(base)), "_tests"):
		return true
	}
	// Directory names: tests/, testdata/, testutil/, __tests__/, e2e/, and
	// suites named like llmtests/ or integrationtests/.
	dirs := strings.Split(path.Dir(p), "/")
	for _, d := range dirs {
		switch {
		case d == "e2e", d == "__tests__", d == "spec", d == "fixtures", d == "testing",
			strings.HasPrefix(d, "test"), strings.HasSuffix(d, "tests"), strings.HasSuffix(d, "_test"):
			return true
		}
	}
	return false
}

// lineReader reads single source lines through the project root, caching each
// file for the duration of one request. It never follows symlinks and never
// serves hidden or .euignore'd files.
type lineReader struct {
	root     *os.Root
	servable func(string) bool
	files    map[string][]string
}

func (r *Router) newLineReader() (*lineReader, error) {
	if r.config == nil || r.config.Project.Path == "" {
		return nil, errors.New("project path not configured")
	}
	root, err := os.OpenRoot(r.config.Project.Path)
	if err != nil {
		return nil, fmt.Errorf("open project root: %w", err)
	}
	return &lineReader{
		root:     root,
		servable: checksum.ServableMatcher(r.config.Project.Path),
		files:    map[string][]string{},
	}, nil
}

func (lr *lineReader) Close() {
	if lr != nil && lr.root != nil {
		_ = lr.root.Close()
	}
}

// lines returns the file's lines (nil if unreadable or not servable).
func (lr *lineReader) lines(file string) []string {
	if lr == nil {
		return nil
	}
	if ls, ok := lr.files[file]; ok {
		return ls
	}
	var ls []string
	if lr.servable(file) {
		if data, err := checksum.ReadRegularNoSymlinks(lr.root, file); err == nil {
			ls = strings.Split(string(data), "\n")
		}
	}
	lr.files[file] = ls
	return ls
}

// line returns the trimmed text of 1-based line n, or "".
func (lr *lineReader) line(file string, n int) string {
	ls := lr.lines(file)
	if n < 1 || n > len(ls) {
		return ""
	}
	return strings.TrimSpace(ls[n-1])
}

// span returns lines [start, end] (1-based, inclusive), clamped to the file.
func (lr *lineReader) span(file string, start, end int) []string {
	ls := lr.lines(file)
	if len(ls) == 0 || start < 1 {
		return nil
	}
	if end > len(ls) {
		end = len(ls)
	}
	if start > end {
		return nil
	}
	return ls[start-1 : end]
}

const maxCallSiteText = 160

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// resolveForAgent maps a symbol to a call-graph node, normalizing the
// spellings agents use. It returns the key, node, the other keys the name
// matched, and whether anything was found.
func (r *Router) resolveForAgent(symbol string) (string, *utils.CallGraphNode, []string, bool) {
	if r.cgBuild == nil {
		return "", nil, nil, false
	}
	tries := []string{strings.TrimSpace(symbol), normalizeSymbol(symbol)}
	// "pkg.Type.method": also try the last two parts and the last part.
	if parts := strings.Split(normalizeSymbol(symbol), "."); len(parts) > 2 {
		tries = append(tries, strings.Join(parts[len(parts)-2:], "."))
	}
	for _, name := range tries {
		if name == "" {
			continue
		}
		key, node, ok, amb := r.resolveCallGraphEntity(name)
		if !ok {
			continue
		}
		// A prefix-only match ("build" → "buildIndex") is not what was asked for.
		if !strings.EqualFold(bareID(key), name) && !strings.EqualFold(callGraphShortName(key), name) &&
			!strings.EqualFold(bareID(key), normalizeSymbol(name)) {
			continue
		}
		// Prefer a node spelled exactly as asked ("UpdateProvider" is not the
		// busier handler updateProvider), and pick among several the same way
		// every time (the scan above runs in map order).
		if k := r.bestSpelled(append([]string{key}, amb...), name); k != "" {
			key, node = k, r.cgBuild.Nodes[k]
		}
		var others []string
		for _, k := range amb {
			if k != key && (strings.EqualFold(bareID(k), name) || strings.EqualFold(callGraphShortName(k), name)) {
				others = append(others, k)
			}
		}
		sort.Strings(others)
		return key, node, others, true
	}
	return "", nil, nil, false
}

// bestSpelled picks, among call-graph keys, the best one spelled exactly as
// name, or "" when none is: production code before tests, a concrete
// function or method before an interface declaration or another node, then
// the most called, then by key, so the answer does not depend on map order.
func (r *Router) bestSpelled(keys []string, name string) string {
	best := ""
	var bestNode *utils.CallGraphNode
	better := func(k string, n *utils.CallGraphNode) bool {
		if best == "" {
			return true
		}
		if t1, t2 := isTestPath(r.fileOfID(k)), isTestPath(r.fileOfID(best)); t1 != t2 {
			return !t1
		}
		concrete := func(k string, n *utils.CallGraphNode) bool {
			return (n.NodeType == "function" || n.NodeType == "method") && !strings.HasPrefix(k, "ifacemethod_")
		}
		f1, f2 := concrete(k, n), concrete(best, bestNode)
		if f1 != f2 {
			return f1
		}
		if n.CallCountEstimate != bestNode.CallCountEstimate {
			return n.CallCountEstimate > bestNode.CallCountEstimate
		}
		if c1, c2 := len(r.cgBuild.CalledBy[k]), len(r.cgBuild.CalledBy[best]); c1 != c2 {
			return c1 > c2
		}
		return k < best
	}
	for _, k := range keys {
		n := r.cgBuild.Nodes[k]
		if n == nil || !spelledAs(k, name) {
			continue
		}
		if better(k, n) {
			best, bestNode = k, n
		}
	}
	return best
}

// spelledAs reports whether a call-graph key names exactly name (same case),
// as its bare ID ("Bifrost.run") or its short name ("run").
func spelledAs(key, name string) bool {
	return bareID(key) == name || callGraphShortName(key) == name || bareID(key) == normalizeSymbol(name)
}

// defLocation returns "file:line" for a call-graph node, falling back to the
// node's file when the KB has no line for it.
func (r *Router) defLocation(id string, node *utils.CallGraphNode) string {
	if t, err := r.ensureSymTable(); err == nil {
		if d := t.byID[id]; d != nil {
			return d.Location()
		}
	}
	if node != nil {
		return node.File
	}
	return ""
}

// fileOfID returns the file a node ID lives in.
func (r *Router) fileOfID(id string) string {
	if n := r.cgBuild.Nodes[id]; n != nil && n.File != "" {
		return n.File
	}
	if i := strings.LastIndex(id, "::"); i >= 0 {
		return id[i+2:]
	}
	return ""
}

// Calls builds a callers report (callees=false) or a callees report for
// symbol, walking depth hops (1 = direct edges only) and listing at most
// limit sites. Call-site lines come from the parser's edges; the text of each
// line is read from the working tree.
func (r *Router) Calls(symbol string, callees bool, depth, limit int) (*CallReport, error) {
	if depth < 1 {
		depth = 1
	}
	if depth > 4 {
		depth = 4
	}
	if limit <= 0 {
		limit = 40
	}
	rep := &CallReport{Callees: callees, Depth: depth}
	if r.cgBuild == nil || len(r.cgBuild.Nodes) == 0 {
		return nil, errors.New("call graph not available for this repository; run 'eulix analyze'")
	}

	key, node, others, ok := r.resolveForAgent(symbol)
	if !ok {
		rep.Symbol = normalizeSymbol(symbol)
		rep.Suggested = r.fuzzySearch(rep.Symbol)
		return rep, nil
	}
	rep.Found = true
	rep.Symbol = bareID(key)
	rep.SymbolID = key
	rep.Location = r.defLocation(key, node)
	for _, o := range others {
		rep.Others = append(rep.Others, fmt.Sprintf("%s (%s)", bareID(o), r.fileOfID(o)))
	}

	lr, err := r.newLineReader()
	if err != nil {
		return nil, err
	}
	defer lr.Close()

	seen := map[string]bool{key: true}
	frontier := []string{key}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, at := range frontier {
			var edges []string
			if callees {
				edges = r.cgBuild.Calls[at]
			} else {
				edges = r.cgBuild.CalledBy[at]
			}
			for _, other := range edges {
				from, to := other, at // callers: other → at
				if callees {
					from, to = at, other
				}
				file := r.fileOfID(from)
				lines := dedupInts(r.cgBuild.Sites[retrieval.SiteKey(from, to)])
				if len(lines) == 0 {
					lines = []int{0}
				}
				for _, ln := range lines {
					if len(rep.Sites) >= limit {
						rep.Omitted++
						continue
					}
					s := CallSite{
						Name: displayName(other), ID: other, File: file, Line: ln,
						Depth: d, Test: isTestPath(file),
						Edge: r.cgBuild.Inferred[retrieval.SiteKey(from, to)],
					}
					if ln > 0 {
						s.Text = clipText(lr.line(file, ln), maxCallSiteText)
					}
					if d > 1 {
						s.Via = bareID(at)
					}
					rep.Sites = append(rep.Sites, s)
				}
				if !seen[other] {
					seen[other] = true
					next = append(next, other)
				}
			}
		}
		frontier = next
	}

	// Non-test code first, then by depth, file and line: a stable order the
	// agent can rely on across calls.
	sort.SliceStable(rep.Sites, func(i, j int) bool {
		a, b := rep.Sites[i], rep.Sites[j]
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		if a.Test != b.Test {
			return !a.Test
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	if code, tests := rep.DirectCount(); !callees && (depth == 1 || code+tests == 0) {
		r.scanCallers(rep, key, lr, limit)
	}
	return rep, nil
}

// maxTwinsListed caps the other same-name definitions named in a report.
const maxTwinsListed = 3

// scanCallers adds to a callers report the uses of the symbol's name that the
// call graph has no edge for (see scanName). Common names are not scanned:
// their matches would mostly be other types' methods.
func (r *Router) scanCallers(rep *CallReport, key string, lr *lineReader, limit int) {
	name := lastSegment(rep.Symbol)
	if !nameIsSpecific(name) {
		return
	}
	t, err := r.ensureSymTable()
	if err != nil {
		return
	}
	rep.Scanned = true
	rep.Twins = r.twinsOf(t, rep.Symbol, key, name)

	// Sites the graph already lists, by line, or by enclosing function when
	// the edge has no line; and sites it resolved to another definition of
	// the same name (`s.Save()` on a different type is not a use of this one).
	known := r.sitesOfTwins(key, name)
	for _, s := range rep.Sites {
		if s.Depth != 1 {
			continue
		}
		if s.Line > 0 {
			known[fmt.Sprintf("%s:%d", s.File, s.Line)] = true
		} else {
			known[s.File+"|"+lastSegment(bareID(s.ID))] = true
		}
	}
	limit = min(limit, maxNameUses)
	uses := r.scanName(lr, name, strings.Contains(rep.Symbol, "."))
	if len(uses) >= maxNameScanUses {
		rep.NameOmitted++ // the scan stopped early; more may exist
	}
	for _, u := range uses {
		if known[fmt.Sprintf("%s:%d", u.File, u.Line)] || known[u.File+"|"+lastSegment(u.Enclosing)] {
			continue
		}
		if len(rep.ByName)+len(rep.AsValue) >= limit {
			rep.NameOmitted++
			continue
		}
		if u.Call {
			rep.ByName = append(rep.ByName, u)
		} else {
			rep.AsValue = append(rep.AsValue, u)
		}
	}
}

// sitesOfTwins returns, as "file:line", the call sites the graph resolved to
// another node with the same short name as key.
func (r *Router) sitesOfTwins(key, name string) map[string]bool {
	out := map[string]bool{}
	for k := range r.cgBuild.Nodes {
		if k == key || callGraphShortName(k) != name {
			continue
		}
		for _, caller := range r.cgBuild.CalledBy[k] {
			file := r.fileOfID(caller)
			for _, ln := range r.cgBuild.Sites[retrieval.SiteKey(caller, k)] {
				out[fmt.Sprintf("%s:%d", file, ln)] = true
			}
		}
	}
	return out
}

// twinsOf lists the other definitions sharing a symbol's bare name, as
// "Qualified (file:line)". A same-named method on the same type is the
// symbol itself; a same-named function in another file is a twin.
func (r *Router) twinsOf(t *symTable, symbol, key, name string) []string {
	file := r.fileOfID(key)
	var out []string
	for _, d := range t.definitionsNamed(name) {
		if d.Qualified == symbol && (d.Kind == "method" || d.File == file) {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s)", d.Qualified, d.Location()))
	}
	if len(out) > maxTwinsListed {
		out = append(out[:maxTwinsListed], fmt.Sprintf("+%d more", len(out)-maxTwinsListed))
	}
	return out
}

// displayName is bareID plus a marker for interface methods, whose callee
// is resolved only at run time (any implementation may run).
func displayName(id string) string {
	if strings.HasPrefix(id, "ifacemethod_") {
		return bareID(id) + " (interface)"
	}
	return bareID(id)
}

func dedupInts(xs []int) []int {
	if len(xs) < 2 {
		return xs
	}
	out := append([]int(nil), xs...)
	sort.Ints(out)
	j := 1
	for i := 1; i < len(out); i++ {
		if out[i] != out[j-1] {
			out[j] = out[i]
			j++
		}
	}
	return out[:j]
}

// ========== Definitions ==========

// Definition is one definition of a symbol, ready for an agent: where it is,
// its signature and docstring, and a short body. A class is returned as its
// header and a list of its methods, never as its whole body.
type Definition struct {
	Name      string
	Kind      string
	Location  string
	File      string
	Start     int
	End       int
	Signature string
	Docstring string
	// Body is the source of [Start, BodyEnd]; BodyEnd < End means it was cut.
	Body    string
	BodyEnd int
	// Methods lists "name :line" for a class, at most maxListedMethods.
	Methods     []string
	MoreMethods int // methods left out of Methods
}

// maxListedMethods caps the method list of one class definition.
const maxListedMethods = 40

// Truncated reports whether Body stops before the end of the definition.
func (d *Definition) Truncated() bool { return d.Body != "" && d.BodyEnd < d.End }

// Definitions returns up to limit definitions of symbol. bodyLines caps the
// lines of body returned per definition (0 = signature only).
func (r *Router) Definitions(symbol string, bodyLines, limit int) ([]Definition, []string, error) {
	t, err := r.ensureSymTable()
	if err != nil {
		return nil, nil, err
	}
	if limit <= 0 {
		limit = 3
	}
	defs := t.lookupDefs(symbol)
	if len(defs) == 0 {
		return nil, r.fuzzySearch(normalizeSymbol(symbol)), nil
	}
	lr, err := r.newLineReader()
	if err != nil {
		return nil, nil, err
	}
	defer lr.Close()

	// Prefer the definition that has the most callers when names collide.
	ranked := append([]*symDef(nil), defs...)
	if r.cgBuild != nil {
		sort.SliceStable(ranked, func(i, j int) bool {
			return len(r.cgBuild.CalledBy[ranked[i].ID]) > len(r.cgBuild.CalledBy[ranked[j].ID])
		})
	}

	var out []Definition
	for _, d := range ranked {
		if len(out) >= limit {
			break
		}
		if !lr.servable(d.File) {
			continue
		}
		def := Definition{
			Name: d.Qualified, Kind: d.Kind, Location: d.Location(), File: d.File,
			Start: d.Start, End: d.End, Signature: d.Signature, Docstring: firstParagraph(d.Docstring, 320),
		}
		if d.Kind == "class" {
			def.Signature = lr.line(d.File, d.Start)
			for i, m := range d.Methods {
				if i == maxListedMethods {
					def.MoreMethods = len(d.Methods) - i
					break
				}
				def.Methods = append(def.Methods, fmt.Sprintf("%s :%d", m.Name, m.LineStart))
			}
		} else if bodyLines > 0 && d.Start > 0 {
			end := d.End
			if end <= 0 || end < d.Start {
				end = d.Start
			}
			cut := end
			if cut-d.Start+1 > bodyLines {
				cut = d.Start + bodyLines - 1
			}
			if src := lr.span(d.File, d.Start, cut); len(src) > 0 {
				def.Body = strings.Join(src, "\n")
				def.BodyEnd = d.Start + len(src) - 1
			}
		}
		if def.Signature == "" {
			def.Signature = lr.line(d.File, d.Start)
		}
		out = append(out, def)
	}
	return out, nil, nil
}

// firstParagraph trims a docstring to its first paragraph, capped at n bytes.
func firstParagraph(doc string, n int) string {
	doc = strings.TrimSpace(doc)
	if i := strings.Index(doc, "\n\n"); i >= 0 {
		doc = doc[:i]
	}
	return clipText(strings.Join(strings.Fields(doc), " "), n)
}

// ========== Traces (call paths) ==========

// Hop is one edge of a call path: Caller calls Callee at File:Line.
type Hop struct {
	Caller, Callee     string
	CallerID, CalleeID string
	File               string
	Line               int
	Text               string
	// Edge is "" for a resolved call, else how the hop was inferred.
	Edge string
}

// TraceResult is an ordered call path from From to To.
type TraceResult struct {
	From, To   string // resolved bare names ("" when not given / not found)
	FromID     string
	ToID       string
	Hops       []Hop
	Found      bool   // a path was found
	EntryPoint bool   // the path starts at a known entry point
	Reason     string // why no path, or why it stops where it does
	Missing    []string
	Locations  map[string]string // bare name -> definition "file:line"

	// Upstream is the target's own chain when from → to has no static path:
	// from the nearest entry point, or a function nothing calls, to To.
	Upstream []Hop
	// Start is the function a chain begins at when nothing calls it
	// statically (and it is no entry point). StartUses are where its name is
	// used instead: a handler registration, a goroutine start, a call through
	// a field. StartScanned says the scan ran (specific names only).
	Start        string
	StartUses    []NameUse
	StartTwins   []string
	StartScanned bool
}

// maxStartUses caps the name uses listed for the start of a chain.
const maxStartUses = 8

// Trace finds the shortest call path from → to in the call graph. With from
// empty, it walks callers back from to until it reaches an entry point (or a
// function nothing calls). maxHops bounds the search.
func (r *Router) Trace(from, to string, maxHops int) (*TraceResult, error) {
	if r.cgBuild == nil || len(r.cgBuild.Nodes) == 0 {
		return nil, errors.New("call graph not available for this repository; run 'eulix analyze'")
	}
	if maxHops <= 0 || maxHops > 12 {
		maxHops = 8
	}
	res := &TraceResult{Locations: map[string]string{}}

	toKey, toNode, _, ok := r.resolveForAgent(to)
	if !ok {
		res.Missing = append(res.Missing, to)
		res.Reason = fmt.Sprintf("'%s' is not in the call graph", to)
		return res, nil
	}
	res.To, res.ToID = bareID(toKey), toKey
	res.Locations[res.To] = r.defLocation(toKey, toNode)

	lr, err := r.newLineReader()
	if err != nil {
		return nil, err
	}
	defer lr.Close()

	var path []string // node IDs, from start to toKey
	if strings.TrimSpace(from) != "" {
		fromKey, fromNode, _, ok := r.resolveForAgent(from)
		if !ok {
			res.Missing = append(res.Missing, from)
			res.Reason = fmt.Sprintf("'%s' is not in the call graph", from)
			return res, nil
		}
		res.From, res.FromID = bareID(fromKey), fromKey
		res.Locations[res.From] = r.defLocation(fromKey, fromNode)
		path = r.bfsPath(fromKey, toKey, maxHops)
		if path == nil {
			res.Reason = fmt.Sprintf("no static call path from %s to %s within %d hops. "+
				"The chain may cross an async boundary (goroutine, channel, callback, event, queue) "+
				"that a syntax-based call graph cannot see", res.From, res.To, maxHops)
			// Say where the target's own chain begins, so the agent knows
			// which function's start to look for.
			up := r.pathFromEntry(toKey, maxHops)
			res.Upstream = r.hopsAlong(lr, up, res.Locations)
			if n := r.cgBuild.Nodes[up[0]]; n == nil || !n.IsEntryPoint {
				r.scanStart(res, lr, up[0])
			}
			return res, nil
		}
	} else {
		path = r.pathFromEntry(toKey, maxHops)
		if len(path) > 0 {
			res.FromID = path[0]
			res.From = bareID(path[0])
		}
	}

	res.Hops = r.hopsAlong(lr, path, res.Locations)
	res.Found = len(res.Hops) > 0
	if len(path) > 0 {
		if n := r.cgBuild.Nodes[path[0]]; n != nil && n.IsEntryPoint {
			res.EntryPoint = true
		}
	}
	if !res.Found {
		res.Reason = fmt.Sprintf("%s has no callers in the call graph: it is an entry point, exported API, "+
			"or reached only through an async boundary (goroutine, channel, callback) the graph cannot see", res.To)
		r.scanStart(res, lr, toKey)
	} else if strings.TrimSpace(from) == "" && !res.EntryPoint {
		res.Reason = fmt.Sprintf("the chain stops at %s, which has no static callers in this repository: it is "+
			"called by a framework or runtime (a handler, a dunder or interface method), or started as a "+
			"goroutine, thread, callback or event handler; look for where it is registered", res.From)
		r.scanStart(res, lr, path[0])
	}
	return res, nil
}

// hopsAlong turns a path of node IDs into hops with call-site lines, and
// records each caller's definition in locs.
func (r *Router) hopsAlong(lr *lineReader, path []string, locs map[string]string) []Hop {
	var hops []Hop
	for i := 0; i+1 < len(path); i++ {
		a, b := path[i], path[i+1]
		h := Hop{Caller: bareID(a), Callee: bareID(b), CallerID: a, CalleeID: b, File: r.fileOfID(a),
			Edge: r.cgBuild.Inferred[retrieval.SiteKey(a, b)]}
		if lines := dedupInts(r.cgBuild.Sites[retrieval.SiteKey(a, b)]); len(lines) > 0 {
			h.Line = lines[0]
			h.Text = clipText(lr.line(h.File, h.Line), maxCallSiteText)
		}
		hops = append(hops, h)
		locs[h.Caller] = r.defLocation(a, r.cgBuild.Nodes[a])
	}
	return hops
}

// scanStart records where the name of a chain's first function is used,
// when nothing calls it statically: that is where the chain really starts (a
// route registration, "go worker()", a call through a field). Test code is
// left out: a test is not how production code reaches it.
func (r *Router) scanStart(res *TraceResult, lr *lineReader, id string) {
	res.Start = bareID(id)
	name := lastSegment(res.Start)
	if !nameIsSpecific(name) {
		return
	}
	t, err := r.ensureSymTable()
	if err != nil {
		return
	}
	res.StartScanned = true
	res.StartTwins = r.twinsOf(t, res.Start, id, name)
	for _, u := range r.scanName(lr, name, strings.Contains(res.Start, ".")) {
		if u.Test || len(res.StartUses) >= maxStartUses {
			continue
		}
		res.StartUses = append(res.StartUses, u)
	}
}

// bfsPath returns the shortest path of node IDs from → to over call edges.
func (r *Router) bfsPath(from, to string, maxHops int) []string {
	if from == to {
		return []string{from}
	}
	prev := map[string]string{from: ""}
	frontier := []string{from}
	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var next []string
		for _, n := range frontier {
			for _, c := range r.cgBuild.Calls[n] {
				if _, seen := prev[c]; seen {
					continue
				}
				prev[c] = n
				if c == to {
					return unwind(prev, to)
				}
				next = append(next, c)
			}
		}
		frontier = next
	}
	return nil
}

// pathFromEntry walks callers back from target and returns the shortest path
// from an entry point to target. Without an entry point in range, it returns
// the path from the farthest root caller (a function nothing calls) it found.
// Test code is skipped: a test is not how production code reaches target.
func (r *Router) pathFromEntry(target string, maxHops int) []string {
	next := map[string]string{target: ""} // node -> the node it calls on the path
	frontier := []string{target}
	root, rootDepth := "", -1
	for hop := 1; hop <= maxHops && len(frontier) > 0; hop++ {
		var nf []string
		for _, n := range frontier {
			for _, c := range r.cgBuild.CalledBy[n] {
				if _, seen := next[c]; seen || isTestPath(r.fileOfID(c)) {
					continue
				}
				next[c] = n
				if node := r.cgBuild.Nodes[c]; node != nil && node.IsEntryPoint {
					return forward(next, c)
				}
				if !r.hasNonTestCaller(c) && hop > rootDepth {
					root, rootDepth = c, hop
				}
				nf = append(nf, c)
			}
		}
		frontier = nf
	}
	if root == "" {
		// No root within range: start from the farthest node reached.
		if len(frontier) > 0 {
			sort.Strings(frontier)
			return forward(next, frontier[0])
		}
		return []string{target}
	}
	return forward(next, root)
}

func (r *Router) hasNonTestCaller(id string) bool {
	for _, c := range r.cgBuild.CalledBy[id] {
		if !isTestPath(r.fileOfID(c)) {
			return true
		}
	}
	return false
}

func unwind(prev map[string]string, end string) []string {
	var out []string
	for n := end; n != ""; n = prev[n] {
		out = append(out, n)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func forward(next map[string]string, start string) []string {
	var out []string
	for n := start; n != ""; n = next[n] {
		out = append(out, n)
		if len(out) > 64 {
			break
		}
	}
	return out
}

// ========== Query intent for agents ==========

// AgentIntent is what kind of answer a free-text question asks for.
type AgentIntent string

const (
	IntentCallersQ    AgentIntent = "callers"
	IntentCalleesQ    AgentIntent = "callees"
	IntentDefinitionQ AgentIntent = "definition"
	IntentTraceQ      AgentIntent = "trace"
	IntentExplainQ    AgentIntent = "explain"
	IntentSearchQ     AgentIntent = "search"
)

// AgentClassification is the classifier's verdict mapped to an answer type,
// with the symbol it is about.
type AgentClassification struct {
	Intent AgentIntent
	Symbol string
	Raw    classifier.QueryType
}

// ClassifyForAgent maps a free-text question to the answer type an agent
// expects, so the MCP server can serve callers questions from the call graph
// and size the reply by question type.
func (r *Router) ClassifyForAgent(q string) AgentClassification {
	cl := r.classifier.Classify(q)
	out := AgentClassification{Raw: cl.Type, Intent: IntentExplainQ}
	low := strings.ToLower(q)
	sym := ""
	if len(cl.Symbols) > 0 {
		sym = cl.Symbols[0]
	}
	switch cl.Type {
	case classifier.QueryTypeCallGraph, classifier.QueryTypeUsage:
		if calleesPhrase.MatchString(low) {
			out.Intent = IntentCalleesQ
		} else {
			out.Intent = IntentCallersQ
		}
		if sym == "" {
			sym = extractEntityName(stripCommandPrefix(q, "usage of", "usage", "use", "uses of", "show usage", "find usage"))
		}
	case classifier.QueryTypeLocation:
		out.Intent = IntentDefinitionQ
		if sym == "" {
			sym = extractEntityName(q)
		}
	case classifier.QueryTypeDataFlow:
		out.Intent = IntentTraceQ
	case classifier.QueryTypeUnderstanding, classifier.QueryTypeArchitecture, classifier.QueryTypeImplementation:
		out.Intent = IntentExplainQ
	default:
		out.Intent = IntentSearchQ
	}
	// A flow question names its own shape; it wins over explain/search only,
	// never over an exact callers / callees / definition question.
	if (out.Intent == IntentExplainQ || out.Intent == IntentSearchQ) && tracePhrase.MatchString(low) {
		out.Intent = IntentTraceQ
	}
	out.Symbol = sym
	return out
}

var (
	calleesPhrase = regexp.MustCompile(`\b(what|which)\s+(\w+\s+){0,2}does\s+\S+\s+call\b|\bcallees?\b|\bcalls\s+made\s+by\b|\boutbound\b`)
	tracePhrase   = regexp.MustCompile(`\b(trace|flow|path|reach(es)?|get(s)?\s+to|end\s+up\s+in|from\s+\S+\s+to\s+\S+|step\s+by\s+step|lifecycle|journey)\b`)
)

// ========== Ranked context with an agent-sized budget ==========

// AgentContext is ranked code context built for a tool caller.
type AgentContext struct {
	Window     *utils.ContextWindow
	Budget     int // context tokens allowed
	Candidates int // chunks considered before selection
	Warnings   []string
}

// BudgetHit reports whether selection filled (almost) the whole budget, so
// more relevant chunks were probably left out.
func (a *AgentContext) BudgetHit() bool {
	return a.Window != nil && a.Budget > 0 && a.Window.TotalTokens >= a.Budget*85/100
}

// AgentContextFor runs ranked retrieval for q with an explicit context
// budget (no answer reserve: the caller is an agent, not an LLM answer).
// maxLines caps the span hydrated in full per chunk.
func (r *Router) AgentContextFor(q string, budget, maxLines int) (*AgentContext, error) {
	if err := r.ensureContextBuilder(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	cb := r.contextBuilder
	r.mu.RUnlock()
	if cb == nil {
		return nil, errors.New("context builder unavailable")
	}
	win, trace, err := cb.BuildContextBudget(q, budget, maxLines)
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}
	out := &AgentContext{Window: win, Budget: budget}
	if trace != nil {
		out.Candidates = trace.TotalCandidates
		out.Warnings = trace.Warnings
	}
	return out, nil
}

// CoTFor returns the Chain-of-Thought task block for query over win, the same
// one PromptOrAnswerWithCoT appends, for callers that render context themselves.
func (r *Router) CoTFor(query string, win *utils.ContextWindow) string {
	cl := r.classifier.Classify(query)
	return BuildPromptString(query, cl, win != nil && hasSourceCode(win), getTaskBody(r, query, cl),
		r.config.RetrievalConfig.CodeToAstRatio)
}

// IsDirectAnswer reports whether the classifier answers q from the index
// alone (location, call graph, dependencies, entry points, file layout,
// TODOs, metrics) rather than from ranked code.
func (r *Router) IsDirectAnswer(q string) bool {
	switch r.classifier.Classify(q).Type {
	case classifier.QueryTypeLocation, classifier.QueryTypeUsage, classifier.QueryTypeDependency,
		classifier.QueryTypeCallGraph, classifier.QueryTypeEntryPoints, classifier.QueryTypeFileStructure,
		classifier.QueryTypeTodos, classifier.QueryTypeMetrics, classifier.QueryTypeCodeGeneration:
		return true
	}
	return false
}

// HasSymbol reports whether name resolves to a call-graph node.
func (r *Router) HasSymbol(name string) bool {
	_, _, _, ok := r.resolveForAgent(name)
	return ok
}
