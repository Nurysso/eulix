// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package query

import (
	"path/filepath"
	"strings"
	"testing"

	"eulix/internal/utils"
)

// agentRouter is a small Go project:
//
//	main → Serve → handle → shouldRetry
//	              handle → (test) TestHandle
//	Worker.run has no callers (started as a goroutine).
func agentRouter(t *testing.T) *Router {
	t.Helper()
	r := newRouter(t)
	dir := t.TempDir()
	r.config.Project.Path = dir
	writeFile(t, filepath.Join(dir, "main.go"), lines("package x", "func main() {", "\tServe()", "}"))
	writeFile(t, filepath.Join(dir, "srv.go"), lines(
		"package x",
		"// Serve runs the server.",
		"func Serve() {",
		"\thandle(1)",
		"\thandle(2)",
		"}",
		"func handle(n int) {",
		"\tif shouldRetry(n) {",
		"\t\treturn",
		"\t}",
		"}",
		"func shouldRetry(n int) bool {",
		"\ta := n",
		"\tb := a + 1",
		"\tc := b + 1",
		"\treturn c > 3",
		"}",
	))
	writeFile(t, filepath.Join(dir, "srv_test.go"), lines("package x", "func TestHandle() {", "\thandle(3)", "}"))
	writeFile(t, filepath.Join(dir, "worker.go"), lines("package x", "type Worker struct{}", "func (w *Worker) run() {", "\thandle(4)", "}"))

	node := func(id, file string, entry bool) M {
		return M{"ID": id, "NodeType": "function", "File": file, "IsEntryPoint": entry}
	}
	edge := func(from, to string, line int) M {
		return M{"From": from, "To": to, "EdgeType": "call", "CallSiteLine": line}
	}
	const (
		mainID   = "func_main::main.go"
		serveID  = "func_Serve::srv.go"
		handleID = "func_handle::srv.go"
		retryID  = "func_shouldRetry::srv.go"
		testID   = "func_TestHandle::srv_test.go"
		runID    = "method_Worker_run::worker.go"
	)
	r.setGraph(t, M{
		"Nodes": S{node(mainID, "main.go", true), node(serveID, "srv.go", false), node(handleID, "srv.go", false),
			node(retryID, "srv.go", false), node(testID, "srv_test.go", false), node(runID, "worker.go", false)},
		"Edges": S{edge(mainID, serveID, 3), edge(serveID, handleID, 4), edge(serveID, handleID, 5),
			edge(handleID, retryID, 8), edge(testID, handleID, 3), edge(runID, handleID, 4)},
	})
	r.kb = &utils.KnowledgeBaseSimplifiedRef{Metadata: &utils.Metadata{}, Structure: map[string]utils.FileData{
		"main.go": {Functions: []utils.KBFunction{{ID: mainID, Name: "main", LineStart: 2, LineEnd: 4}}},
		"srv.go": {Functions: []utils.KBFunction{
			{ID: serveID, Name: "Serve", LineStart: 3, LineEnd: 6, Signature: "func Serve()", Docstring: "Serve runs the server.\n\nMore detail."},
			{ID: handleID, Name: "handle", LineStart: 7, LineEnd: 11, Signature: "func handle(n int)"},
			{ID: retryID, Name: "shouldRetry", LineStart: 12, LineEnd: 17, Signature: "func shouldRetry(n int) bool"},
		}},
		"worker.go": {Classes: []utils.KBClass{{ID: "struct_Worker::worker.go", Name: "Worker", LineStart: 2, LineEnd: 2,
			Methods: []utils.KBFunction{{ID: runID, Name: "run", LineStart: 3, LineEnd: 5}}}}},
	}}
	return r
}

func TestCallsCallers(t *testing.T) {
	r := agentRouter(t)
	rep, err := r.Calls("handle", false, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.Location != "srv.go:7" || !rep.Complete() {
		t.Fatalf("bad report header: %+v", rep)
	}
	code, tests := rep.DirectCount()
	if code != 3 || tests != 1 {
		t.Fatalf("want 3 code sites (Serve x2, Worker.run) + 1 test, got %d + %d: %+v", code, tests, rep.Sites)
	}
	// Non-test first, ordered by file and line; the calling line is read.
	if s := rep.Sites[0]; s.File != "srv.go" || s.Line != 4 || s.Text != "handle(1)" || s.Name != "Serve" {
		t.Errorf("first site = %+v", s)
	}
	out := FormatCallReport(rep)
	for _, want := range []string{"callers of handle (srv.go:7): 3 call sites + 1 in tests, complete",
		"srv.go:5", "Worker.run", "tests: srv_test.go:3"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n > 6 {
		t.Errorf("compact format grew to %d lines:\n%s", n, out)
	}
}

func TestCallsCalleesAndDepth(t *testing.T) {
	r := agentRouter(t)
	rep, _ := r.Calls("Serve", true, 1, 0)
	if c, _ := rep.DirectCount(); c != 2 || rep.Sites[0].Name != "handle" {
		t.Fatalf("callees of Serve: %+v", rep.Sites)
	}
	deep, _ := r.Calls("shouldRetry", false, 2, 0)
	var sawVia bool
	for _, s := range deep.Sites {
		if s.Depth == 2 && s.Via == "handle" && s.Name == "Serve" {
			sawVia = true
		}
	}
	if !sawVia {
		t.Errorf("depth 2 should reach Serve via handle: %+v", deep.Sites)
	}
}

func TestCallsLimitMarksIncomplete(t *testing.T) {
	r := agentRouter(t)
	rep, _ := r.Calls("handle", false, 1, 2)
	if rep.Complete() || rep.Omitted != 2 {
		t.Fatalf("limit 2 of 4 should omit 2: omitted=%d", rep.Omitted)
	}
	if !strings.Contains(FormatCallReport(rep), "2 more not shown") {
		t.Error("incomplete report must say so")
	}
}

func TestCallsSymbolSpellings(t *testing.T) {
	r := agentRouter(t)
	for _, s := range []string{"Worker.run", "Worker::run", "(*Worker).run", "`Worker.run()`", "x.Worker.run"} {
		rep, err := r.Calls(s, true, 1, 0)
		if err != nil || !rep.Found || rep.Symbol != "Worker.run" {
			t.Errorf("Calls(%q) = %+v, %v", s, rep, err)
		}
	}
	rep, _ := r.Calls("hand", false, 1, 0)
	if rep.Found {
		t.Error("a prefix must not count as the symbol")
	}
	if !strings.Contains(FormatCallReport(rep), "not found") {
		t.Error("not-found report must say so")
	}
}

func TestCallsNoCallersExplains(t *testing.T) {
	r := agentRouter(t)
	rep, _ := r.Calls("Worker.run", false, 1, 0)
	if out := FormatCallReport(rep); !strings.Contains(out, "none found in the static call graph") || !strings.Contains(out, "goroutine") {
		t.Errorf("no-callers report should explain async boundaries:\n%s", out)
	}
}

func TestDefinitions(t *testing.T) {
	r := agentRouter(t)
	defs, _, err := r.Definitions("shouldRetry", 3, 0)
	if err != nil || len(defs) != 1 {
		t.Fatalf("defs=%v err=%v", defs, err)
	}
	d := defs[0]
	if !d.Truncated() || d.BodyEnd != 14 || !strings.Contains(d.Body, "func shouldRetry") {
		t.Errorf("body should be cut to 3 lines: %+v", d)
	}
	out := FormatDefinitions("shouldRetry", defs, nil)
	if !strings.Contains(out, "read srv.go:15-17") {
		t.Errorf("cut body must point at the rest:\n%s", out)
	}

	defs, _, _ = r.Definitions("Serve", 20, 0)
	if defs[0].Docstring != "Serve runs the server." {
		t.Errorf("docstring should be its first paragraph: %q", defs[0].Docstring)
	}

	defs, _, _ = r.Definitions("Worker", 50, 0)
	if len(defs) != 1 || defs[0].Body != "" || len(defs[0].Methods) != 1 {
		t.Errorf("a class is a header plus a method list, never its body: %+v", defs)
	}

	defs, _, _ = r.Definitions("Worker.run", 50, 0)
	if len(defs) != 1 || defs[0].Kind != "method" {
		t.Errorf("qualified method lookup: %+v", defs)
	}

	if defs, _, _ := r.Definitions("nope", 5, 0); len(defs) != 0 {
		t.Errorf("unknown symbol: %+v", defs)
	}
}

func TestTrace(t *testing.T) {
	r := agentRouter(t)

	tr, err := r.Trace("main", "shouldRetry", 0)
	if err != nil || !tr.Found || len(tr.Hops) != 3 {
		t.Fatalf("main→shouldRetry: %+v %v", tr, err)
	}
	if h := tr.Hops[2]; h.Caller != "handle" || h.Callee != "shouldRetry" || h.Line != 8 || h.Text != "if shouldRetry(n) {" {
		t.Errorf("last hop = %+v", h)
	}

	// No from: walk back to the entry point, skipping tests.
	tr, _ = r.Trace("", "shouldRetry", 0)
	if !tr.Found || tr.From != "main" || !tr.EntryPoint {
		t.Errorf("should reach the entry point main: %+v", tr)
	}
	out := FormatTrace(tr)
	if !strings.Contains(out, "main [entry point] → shouldRetry, 3 hops") {
		t.Errorf("trace format:\n%s", out)
	}

	// No static path: say why instead of returning nothing.
	tr, _ = r.Trace("Worker.run", "main", 0)
	if tr.Found || !strings.Contains(tr.Reason, "async boundary") {
		t.Errorf("no path should explain the async gap: %+v", tr)
	}
	tr, _ = r.Trace("", "ghost", 0)
	if len(tr.Missing) != 1 {
		t.Errorf("unknown target: %+v", tr)
	}
}

func TestClassifyForAgent(t *testing.T) {
	r := agentRouter(t)
	cases := []struct {
		q    string
		want AgentIntent
	}{
		{"What calls handle?", IntentCallersQ},
		{"who calls shouldRetry", IntentCallersQ},
		{"what does Serve call", IntentCalleesQ},
		{"where is shouldRetry defined", IntentDefinitionQ},
		{"trace the request from main to shouldRetry", IntentTraceQ},
		{"how does the server handle a retry", IntentExplainQ},
		{"what is the flow from Serve to shouldRetry", IntentTraceQ},
	}
	for _, tc := range cases {
		if got := r.ClassifyForAgent(tc.q); got.Intent != tc.want {
			t.Errorf("ClassifyForAgent(%q) = %s (raw %v), want %s", tc.q, got.Intent, got.Raw, tc.want)
		}
	}
	if got := r.ClassifyForAgent("What calls handle?"); got.Symbol != "handle" {
		t.Errorf("symbol = %q", got.Symbol)
	}
}

func TestIsTestPath(t *testing.T) {
	for p, want := range map[string]bool{
		"core/bifrost_test.go": true, "tests/test_query.py": true, "src/foo.spec.ts": true,
		"pkg/testdata/x.go": true, "core/internal/llmtests/x.go": true, "tests/e2e/api/a.mjs": true,
		"internal/testutil/h.go": true, "core/bifrost.go": false, "attestation/sign.go": false, "src/latest.py": false, "contest/x.go": false,
	} {
		if got := isTestPath(p); got != want {
			t.Errorf("isTestPath(%q) = %v", p, got)
		}
	}
}

func TestNormalizeSymbol(t *testing.T) {
	for in, want := range map[string]string{
		"SearchWorker::search": "SearchWorker.search", "(*Bifrost).handleRequest": "Bifrost.handleRequest",
		"`foo()`": "foo", "Foo#bar": "Foo.bar", " x ": "x",
	} {
		if got := normalizeSymbol(in); got != want {
			t.Errorf("normalizeSymbol(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnclosingSymbol(t *testing.T) {
	r := agentRouter(t)
	for line, want := range map[int]string{8: "handle", 13: "shouldRetry", 1: "", 99: ""} {
		if got := r.EnclosingSymbol("srv.go", line); got != want {
			t.Errorf("EnclosingSymbol(srv.go:%d) = %q, want %q", line, got, want)
		}
	}
	if got := r.EnclosingSymbol("worker.go", 4); got != "Worker.run" {
		t.Errorf("method: %q", got)
	}
}
