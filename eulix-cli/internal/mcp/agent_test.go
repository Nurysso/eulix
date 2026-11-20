// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"eulix/internal/query"
	"eulix/internal/utils"
)

// shop is a project with a call graph:
//
//	main → checkout → charge → retry_charge
//	test_checkout → checkout          (test)
//	worker_loop (no callers)          (started on a thread)
func shop(t *testing.T) string {
	t.Helper()
	root := newProject(t, map[string]string{
		"app/main.py": "def main():\n    checkout([])\n",
		"app/shop.py": "def checkout(cart):\n    total = 0\n    charge(total)\n    return total\n\n" +
			"def charge(amount):\n    \"\"\"Charge the card.\"\"\"\n    if amount < 0:\n        raise ValueError\n" +
			"    return retry_charge(amount)\n\n" +
			"def retry_charge(amount):\n    return amount\n",
		"app/worker.py":       "def worker_loop():\n    charge(1)\n",
		"tests/test_shop.py":  "def test_checkout():\n    checkout([1])\n",
		"app/big.py":          bigFunc(),
		"docs/charge_note.py": "# charge is the card path\n",
	}, []fxFunc{
		{"app/main.py", "main", 1, 2},
		{"app/shop.py", "checkout", 1, 4},
		{"app/shop.py", "charge", 6, 10},
		{"app/shop.py", "retry_charge", 12, 13},
		{"app/worker.py", "worker_loop", 1, 2},
		{"tests/test_shop.py", "test_checkout", 1, 2},
		{"app/big.py", "big_report", 1, 150},
	})
	id := func(file, name string) string { return "func_" + name + "::" + file }
	type node struct {
		ID       string `json:"id"`
		NodeType string `json:"node_type"`
		File     string `json:"file"`
		Entry    bool   `json:"is_entry_point"`
	}
	type edge struct {
		From string `json:"from"`
		To   string `json:"to"`
		Type string `json:"edge_type"`
		Line int    `json:"call_site_line"`
	}
	nodes := []node{
		{id("app/main.py", "main"), "function", "app/main.py", true},
		{id("app/shop.py", "checkout"), "function", "app/shop.py", false},
		{id("app/shop.py", "charge"), "function", "app/shop.py", false},
		{id("app/shop.py", "retry_charge"), "function", "app/shop.py", false},
		{id("app/worker.py", "worker_loop"), "function", "app/worker.py", false},
		{id("tests/test_shop.py", "test_checkout"), "function", "tests/test_shop.py", false},
	}
	edges := []edge{
		{id("app/main.py", "main"), id("app/shop.py", "checkout"), "call", 2},
		{id("app/shop.py", "checkout"), id("app/shop.py", "charge"), "call", 3},
		{id("app/shop.py", "charge"), id("app/shop.py", "retry_charge"), "call", 10},
		{id("app/worker.py", "worker_loop"), id("app/shop.py", "charge"), "call", 2},
		{id("tests/test_shop.py", "test_checkout"), id("app/shop.py", "checkout"), "call", 2},
	}
	cg, _ := json.Marshal(map[string]any{"nodes": nodes, "edges": edges})
	writeTree(t, root, map[string]string{".eulix/kb_call_graph.json": string(cg)})
	return root
}

func bigFunc() string {
	var b strings.Builder
	b.WriteString("def big_report():\n")
	for i := range 149 {
		fmt.Fprintf(&b, "    line_%d = %d  # big_report body\n", i, i)
	}
	return b.String()
}

func TestAgentCallersTool(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	text, isErr := call(t, cs, "callers", map[string]any{"symbol": "charge", "repo_path": repo})
	if isErr {
		t.Fatal(text)
	}
	for _, want := range []string{
		"answer: callers · source: call graph (exact, static) · complete: yes",
		"callers of charge (app/shop.py:6): 2 call sites",
		"app/shop.py:3", "checkout", "charge(total)",
		"app/worker.py:2", "worker_loop",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if len(text) > 600 {
		t.Errorf("callers reply should be compact, got %d bytes:\n%s", len(text), text)
	}

	text, _ = call(t, cs, "callers", map[string]any{"symbol": "checkout"})
	if !strings.Contains(text, "+ 1 in tests") || !strings.Contains(text, "tests: tests/test_shop.py:2") {
		t.Errorf("test call sites should be counted apart:\n%s", text)
	}

	text, _ = call(t, cs, "callees", map[string]any{"symbol": "charge"})
	if !strings.Contains(text, "calls made by charge") || !strings.Contains(text, "retry_charge") {
		t.Errorf("callees:\n%s", text)
	}

	text, _ = call(t, cs, "callers", map[string]any{"symbol": "nope_nothing"})
	if !strings.Contains(text, "complete: no — symbol not found") {
		t.Errorf("unknown symbol must not claim completeness:\n%s", text)
	}
}

// "What calls X?" through the legacy free-text tool must answer with call
// sites, not X's definition (the measured bug behind the doc's problem 1).
func TestAgentQueryRoutesCallersToCallGraph(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	for _, q := range []string{"What calls charge?", "who calls charge", "Where is charge used", "charge is called by?"} {
		text, _ := call(t, cs, "query", map[string]any{"query": q, "repo_path": repo})
		if !strings.Contains(text, "answer: callers") || !strings.Contains(text, "app/worker.py:2") {
			t.Errorf("%q should return call sites:\n%s", q, text)
		}
	}
	text, _ := call(t, cs, "query", map[string]any{"query": "where is retry_charge defined"})
	if !strings.Contains(text, "answer: definition") || !strings.Contains(text, "app/shop.py:12-13") {
		t.Errorf("definition question:\n%s", text)
	}
}

func TestAgentDefinitionTool(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "definition", map[string]any{"symbol": "big_report", "repo_path": repo})
	if !strings.Contains(text, "complete: no — body cut") || !strings.Contains(text, "read app/big.py:41-150") {
		t.Errorf("long body should be cut with a pointer to the rest:\n%s", text)
	}
	if n := strings.Count(text, "big_report body"); n != 39 {
		t.Errorf("want 40 body lines (def + 39), got %d body lines", n)
	}
	zero := 0
	text, _ = call(t, cs, "definition", map[string]any{"symbol": "charge", "body_lines": zero})
	if strings.Contains(text, "raise ValueError") || !strings.Contains(text, "def charge()") {
		t.Errorf("body_lines=0 means signature only:\n%s", text)
	}
}

func TestAgentTraceTool(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))

	text, _ := call(t, cs, "trace", map[string]any{"from": "main", "to": "retry_charge", "repo_path": repo})
	for _, want := range []string{
		"answer: trace", "call path main [entry point] → retry_charge, 3 hops",
		"1. main → checkout  at app/main.py:2",
		"3. charge → retry_charge  at app/shop.py:10  return retry_charge(amount)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if i, j := strings.Index(text, "call path"), strings.Index(text, "code for the path"); j >= 0 && j < i {
		t.Errorf("the path must come before the code:\n%s", text)
	}

	// Only 'to': walk back to the entry point, never through a test.
	text, _ = call(t, cs, "trace", map[string]any{"to": "retry_charge"})
	if !strings.Contains(text, "call path main [entry point]") || strings.Contains(text, "test_checkout →") {
		t.Errorf("trace from entry:\n%s", text)
	}

	// Endpoints taken from the question.
	text, _ = call(t, cs, "trace", map[string]any{"question": "how does main reach retry_charge?"})
	if !strings.Contains(text, "call path main") {
		t.Errorf("trace from question:\n%s", text)
	}

	// No static path: explain where the chain breaks.
	text, _ = call(t, cs, "trace", map[string]any{"from": "worker_loop", "to": "main"})
	if !strings.Contains(text, "complete: no") || !strings.Contains(text, "async boundary") {
		t.Errorf("no path should be explained:\n%s", text)
	}
}

func TestAgentSearchBudgetAndHeader(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "search", map[string]any{"question": "how is the card charged", "repo_path": repo})
	if !strings.HasPrefix(text, "answer: ") || !strings.Contains(text, " · complete: ") {
		t.Errorf("every reply starts with the completeness header:\n%s", text)
	}
	if strings.Contains(text, "\nQUERY\n") {
		t.Errorf("agent replies do not echo the query:\n%s", text)
	}
}

func TestAgentSearchCutsLongChunks(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "search", map[string]any{"question": "big_report", "max_tokens": 4000, "repo_path": repo})
	if strings.Count(text, "big_report body") > defaultMaxChunkLines {
		t.Errorf("a chunk longer than max_chunk_lines must be cut:\n%s", text)
	}
	if strings.Contains(text, "big_report body") && !strings.Contains(text, "more lines cut (read app/big.py:") {
		t.Errorf("a cut chunk must name the lines to read:\n%s", text)
	}
}

func TestRenderContextReferencesRepeats(t *testing.T) {
	win := &utils.ContextWindow{TotalTokens: 100, Chunks: []utils.ContextChunk{
		{File: "a.py", StartLine: 1, EndLine: 3, Content: "```python\ndef a():\n    pass\n```", Importance: 0.9},
		{File: "b.py", StartLine: 1, EndLine: 3, Content: "```python\ndef b():\n    pass\n```", Importance: 0.8},
	}}
	ac := &query.AgentContext{Window: win, Budget: 1000}

	got := renderContext(ac, renderOpts{answer: "search", maxLines: 60, sent: map[string]bool{"a.py:1-3": true}})
	if !strings.Contains(got, "(already sent this session) a.py:1-3  def a():") || !strings.Contains(got, "[2] b.py:1-3") {
		t.Errorf("repeat should be a reference next to new chunks:\n%s", got)
	}
	if strings.Count(got, "    pass") != 1 {
		t.Errorf("only the new chunk carries a body:\n%s", got)
	}
	if refs := sentChunks(got); len(refs) != 1 || refs[0] != "b.py:1-3" {
		t.Errorf("only b is sent in full: %v", refs)
	}

	// All repeats: the agent asked again because it lost them; resend.
	got = renderContext(ac, renderOpts{answer: "search", maxLines: 60,
		sent: map[string]bool{"a.py:1-3": true, "b.py:1-3": true}})
	if strings.Count(got, "    pass") != 2 {
		t.Errorf("an all-repeat reply is sent in full:\n%s", got)
	}

	ac.Budget, ac.Window.TotalTokens = 100, 95
	if got := renderContext(ac, renderOpts{answer: "search", maxLines: 60}); !strings.Contains(got, "complete: no — budget hit") {
		t.Errorf("a full budget must be reported:\n%s", got)
	}
}

func TestBudgetFor(t *testing.T) {
	s := &Server{}
	if got := s.budgetFor(query.IntentDefinitionQ, 0); got != budgetDefinition {
		t.Errorf("definition = %d", got)
	}
	if got := s.budgetFor(query.IntentSearchQ, 0); got != defaultReplyTokens {
		t.Errorf("search = %d", got)
	}
	if got := s.budgetFor(query.IntentTraceQ, 0); got != budgetTrace {
		t.Errorf("trace = %d", got)
	}
	if got := s.budgetFor(query.IntentSearchQ, 50); got != budgetFloor {
		t.Errorf("max_tokens floor = %d", got)
	}
	if got := s.budgetFor(query.IntentSearchQ, 1<<20); got != budgetCeiling {
		t.Errorf("max_tokens ceiling = %d", got)
	}
}

func TestLexicalOneLinePerHit(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"pkg/a.py": "x = 1\n" + strings.Repeat("payment_total = compute()\n", 20),
	})
	cfg := DefaultStrategyConfig()
	cfg.Enclosing = func(file string, line int) string { return "compute_" + filepath.Base(file) }
	got, err := lexicalSearch(t.Context(), root, "payment_total", nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "payment_total = compute()"); n != lexLinesPerFile {
		t.Errorf("want %d hit lines per file, got %d:\n%s", lexLinesPerFile, n, got)
	}
	if !strings.Contains(got, "  2  [compute_a.py]  payment_total") {
		t.Errorf("each hit names its line and enclosing function:\n%s", got)
	}
}

// A callers question answered with prose scores 0, so the cascade keeps going.
func TestCallersGateRejectsWrongAnswerType(t *testing.T) {
	if looksLikeCallReport("Function 'charge' defined at app/shop.py:6") {
		t.Error("a definition is not a call report")
	}
	if !looksLikeCallReport("callers of charge (app/shop.py:6): 2 call sites") {
		t.Error("a call report is a call report")
	}
}

// The retrieval benchmark compares releases on query_without_cot: it must
// keep the pre-budget output (router prompt, no header, no routing).
func TestLegacyQueryWithoutCoTUnchanged(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "query_without_cot", map[string]any{"query": "how is the card charged", "repo_path": repo})
	if strings.HasPrefix(text, "answer: ") || !strings.Contains(text, "\nQUERY\n") {
		t.Errorf("legacy tool output changed:\n%s", text)
	}
}

func TestCapChunkSqueezesBlankRuns(t *testing.T) {
	c := utils.ContextChunk{File: "a.py", StartLine: 1, EndLine: 40,
		Content: "```python\nclass A:\n\n\n\n\n    x = 1\n\n\n    def f(self): ...\n```"}
	got, cut := capChunk(c, 60)
	if cut || strings.Contains(got, "\n\n\n") {
		t.Errorf("blank runs should collapse to one line:\n%q", got)
	}
}
