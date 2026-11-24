// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

// Tests for the agent cost changes (docs/Plan-Changes/Agent-Cost-Savings.md):
// a lean tool list, "complete" meaning answered, uses found by name, and
// several symbols per call.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"eulix/internal/kbfile/kbfiletest"
	"eulix/internal/utils"
)

// pricing is a project whose call graph has no edge into apply_discount: it
// is called through a module attribute and passed as a value, as in the
// "none found statically" replies of the agent benchmark.
//
//	app/cart.py: checkout_total calls pricing.apply_discount(...)  (unresolved)
//	             install passes apply_discount as a value
//	tests/test_pricing.py: test_apply calls it
//	app/cache.py / app/store.py: refreshCache (busy) and RefreshCache
//
// With twin set, app/legacy.py defines another apply_discount.
func pricing(t *testing.T, twin bool) string {
	t.Helper()
	files := map[string]string{
		"app/pricing.py": "def apply_discount(total):\n    return total * 0.9\n",
		"app/cart.py": "from app.pricing import apply_discount\n" +
			"def checkout_total(cart):\n" +
			"    # apply_discount(cart) is applied below\n" +
			"    label = \"self.apply_discount(x)\"\n" +
			"    return pricing.apply_discount(sum(cart))\n" +
			"def install(hooks):\n" +
			"    hooks.append(apply_discount)\n",
		"tests/test_pricing.py": "def test_apply():\n    assert apply_discount(10) == 9\n",
		"app/cache.py":          "def refreshCache():\n    pass\n",
		"app/store.py":          "def RefreshCache():\n    pass\ndef save():\n    RefreshCache()\n",
	}
	funcs := []fxFunc{
		{"app/pricing.py", "apply_discount", 1, 2},
		{"app/cart.py", "checkout_total", 2, 5},
		{"app/cart.py", "install", 6, 7},
		{"tests/test_pricing.py", "test_apply", 1, 2},
		{"app/cache.py", "refreshCache", 1, 2},
		{"app/store.py", "RefreshCache", 1, 2},
		{"app/store.py", "save", 3, 4},
	}
	if twin {
		files["app/legacy.py"] = "def apply_discount(total):\n    return total\n"
		funcs = append(funcs, fxFunc{"app/legacy.py", "apply_discount", 1, 2})
	}
	root := newProject(t, files, funcs)
	id := func(file, name string) string { return "func_" + name + "::" + file }
	var nodes []utils.CallGraphNode
	for _, f := range funcs {
		n := utils.CallGraphNode{ID: id(f.file, f.name), NodeType: "function", File: f.file}
		if f.name == "refreshCache" {
			n.CallCountEstimate = 50 // busier than RefreshCache: must not win on case alone
		}
		nodes = append(nodes, n)
	}
	cg := &utils.CallGraphRef{Nodes: nodes, Edges: []utils.CallGraphEdge{
		{From: id("app/store.py", "save"), To: id("app/store.py", "RefreshCache"), EdgeType: "call", CallSiteLine: 4},
	}}
	writeTree(t, root, map[string]string{".eulix/kb_call_graph.fb": string(kbfiletest.EncodeCallGraph(cg))})
	return root
}

func TestCallersFindsUsesByName(t *testing.T) {
	repo := pricing(t, false)
	cs := connect(t, newTestServer(t))
	text, isErr := call(t, cs, "callers", map[string]any{"symbol": "apply_discount", "repo_path": repo})
	if isErr {
		t.Fatal(text)
	}
	for _, want := range []string{
		"source: call graph (exact, static) + name scan · complete: yes",
		"none in the static call graph; found 1 call by name + 1 use as a value:",
		"app/cart.py:5  checkout_total",
		"return pricing.apply_discount(sum(cart))",
		"app/cart.py:7  install",
		"(tests, by name: tests/test_pricing.py:2)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	// The import, the comment, the string and the definition are not uses.
	for _, not := range []string{"app/cart.py:1 ", "app/cart.py:3 ", "app/cart.py:4 ", "  app/pricing.py:1 "} {
		if strings.Contains(text, not) {
			t.Errorf("%s is not a use:\n%s", not, text)
		}
	}
}

func TestCallersByNameWithATwinIsNotComplete(t *testing.T) {
	repo := pricing(t, true)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "callers", map[string]any{"symbol": "apply_discount", "repo_path": repo})
	if !strings.Contains(text, "complete: no — a use by name may belong to another definition") ||
		!strings.Contains(text, "also defined as apply_discount (app/") {
		t.Errorf("a shared name must not claim completeness:\n%s", text)
	}
}

// "RefreshCache" is the function spelled that way, not the busier
// refreshCache that matches it case-insensitively.
func TestSymbolResolutionPrefersExactCase(t *testing.T) {
	repo := pricing(t, false)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "callers", map[string]any{"symbol": "RefreshCache", "repo_path": repo})
	if !strings.Contains(text, "callers of RefreshCache (app/store.py:1)") || !strings.Contains(text, "app/store.py:4") {
		t.Errorf("want RefreshCache's callers:\n%s", text)
	}
}

func TestTraceListsWhereTheChainStarts(t *testing.T) {
	repo := pricing(t, false)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "trace", map[string]any{"to": "apply_discount", "repo_path": repo})
	for _, want := range []string{
		"name scan + ranked code · complete: yes",
		"apply_discount is used by name here (where the chain starts):",
		"app/cart.py:5  checkout_total calls it",
		"app/cart.py:7  install passes it as a value",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if path, _, _ := strings.Cut(text, "code for the path"); strings.Contains(path, "tests/test_pricing.py") {
		t.Errorf("a test is not where a production chain starts:\n%s", text)
	}
}

func TestTraceWithoutPathShowsTheTargetsChain(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "trace", map[string]any{"from": "retry_charge", "to": "checkout", "repo_path": repo})
	for _, want := range []string{
		"complete: no — no static path; the target's own chain and where it starts are listed",
		"checkout is reached by this chain instead, 1 hops",
		"1. main → checkout  at app/main.py:2",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

// A path resolved end to end answers the question, whatever code fitted.
func TestResolvedTraceIsComplete(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "trace", map[string]any{"from": "main", "to": "retry_charge", "max_tokens": 300, "repo_path": repo})
	if !strings.HasPrefix(text, "answer: trace · source: call graph (static) + ranked code · complete: yes") {
		t.Errorf("a resolved path is complete:\n%s", text)
	}
	if strings.Contains(text, "raise max_tokens") {
		t.Errorf("trace must not give budget advice:\n%s", text)
	}
}

func TestSeveralSymbolsInOneCall(t *testing.T) {
	repo := shop(t)
	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "callers", map[string]any{"symbol": "charge, checkout, charge", "repo_path": repo})
	if !strings.HasPrefix(text, "answer: callers for 2 symbols · complete: yes for all\n") ||
		!strings.Contains(text, "callers of charge") || !strings.Contains(text, "callers of checkout") {
		t.Errorf("two symbols, one reply:\n%s", text)
	}
	text, _ = call(t, cs, "definition", map[string]any{"symbol": "charge,nope_nothing"})
	if !strings.HasPrefix(text, "answer: definition for 2 symbols · complete: no for nope_nothing") ||
		!strings.Contains(text, "def charge(amount):") {
		t.Errorf("definition of two symbols:\n%s", text)
	}
	if text, isErr := call(t, cs, "callees", map[string]any{"symbol": "a,b,c,d,e,f,g,h,i"}); !isErr ||
		!strings.Contains(text, "at most 8 symbols") {
		t.Errorf("too many symbols: isError=%v %q", isErr, text)
	}
}

// Every listed tool and the instructions are re-read on every turn of every
// session. An agent session started on a repository lists the five typed
// tools only; the size guard keeps their descriptions from growing back.
func TestAgentToolListIsLean(t *testing.T) {
	repo := billing(t, nil)
	cs := connect(t, newTestServer(t, WithLegacyTools(false), WithInitialRepo(repo)))
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	size := 0
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		b, _ := json.Marshal(tool)
		size += len(b)
	}
	if got := strings.Join(names, ","); got != "callees,callers,definition,search,trace" {
		t.Errorf("tools = %s", got)
	}
	instr := cs.InitializeResult().Instructions
	if strings.Contains(instr, "set_repo_path") {
		t.Errorf("a pinned server does not advertise set_repo_path: %q", instr)
	}
	// 9 tools and the instructions took 8393 bytes before (2026-11-23).
	const maxBytes = 5000
	if size+len(instr) > maxBytes {
		t.Errorf("tool list %d + instructions %d bytes > %d", size, len(instr), maxBytes)
	}
	t.Logf("tool list %d bytes, instructions %d bytes", size, len(instr))

	// Unpinned, the agent needs set_repo_path to choose a repository.
	cs = connect(t, newTestServer(t, WithLegacyTools(false)))
	tools, err = cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTool(tools.Tools, "set_repo_path") || hasTool(tools.Tools, "query") {
		t.Errorf("unpinned agent server: %v", tools.Tools)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "set_repo_path") {
		t.Error("an unpinned server explains how to choose a repository")
	}
}

func hasTool(tools []*mcpsdk.Tool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

var rankedReply = "answer: search · source: ranked code, 3 chunks, ~900/1800 tokens · complete: n/a (ranked)\n" +
	"\n" +
	"[1] app/shop.py:6-10 (score 0.95)\n" +
	"```python\ndef charge(amount):\n    \"\"\"Charge the card.\"\"\"\n\n    return retry_charge(amount)\n```\n" +
	"\n" +
	"[2] app/shop.py:12-13 (score 0.80)\n" +
	"```python\ndef retry_charge(amount):\n    return amount\n```\n" +
	"\n" +
	"(already sent this session) app/main.py:1-2  def main():\n" +
	"\n" +
	"[3] app/big.py:1-150 (score 0.40)\n" +
	"Function: big_report\nSignature: def big_report()\n```python\ndef big_report():\n" +
	strings.Repeat("    line = 1  # big_report body\n", 9) + "```\n"

func TestLeanSearchKeepsTheBestMatchAndListsTheRest(t *testing.T) {
	got := leanSearch(rankedReply, []string{"retry_charge", "big_report"})
	for _, want := range []string{
		"source: ranked code, 3 chunks, ~900/1800 tokens; 1 listed without code · complete: n/a (ranked) · more: listed matches carry no code (bodies: true sends it)",
		"[1] app/shop.py:6-10 (score 0.95)\n```python\ndef charge(amount):",
		// Short enough to send whole.
		"[2] app/shop.py:12-13 (score 0.80)\n```python\ndef retry_charge(amount):\n    return amount\n```",
		"(already sent this session) app/main.py:1-2  def main():\n",
		"other matches, best first:\n  #3 app/big.py:1-150 (0.40)  def big_report()  · mentions big_report\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "big_report body") {
		t.Errorf("a listed match must not carry its body:\n%s", got)
	}
	// Only the chunks sent in full are remembered as sent.
	if sent := sentChunks(got); strings.Join(sent, ",") != "app/shop.py:6-10,app/shop.py:12-13" {
		t.Errorf("sentChunks = %v", sent)
	}
	// Replies without chunks, or with one, are left alone.
	for _, text := range []string{
		"answer: callers · source: call graph (exact, static) · complete: yes\ncallers of x: none\n",
		"answer: search · source: lexical fallback · complete: n/a (ranked)\napp/a.py:3  [run]  x = 1\n",
		strings.SplitAfter(rankedReply, "```\n")[0],
	} {
		if got := leanSearch(text, nil); got != text {
			t.Errorf("changed:\n%s\n=>\n%s", text, got)
		}
	}
}

func TestSearchBodiesSendsEveryMatch(t *testing.T) {
	repo := shop(t)
	q := "how does charge call retry_charge for the card"
	lean, _ := call(t, newConnected(t), "search", map[string]any{"question": q, "repo_path": repo})
	full, _ := call(t, newConnected(t), "search", map[string]any{"question": q, "repo_path": repo, "bodies": true})
	// Every chunk of this fixture is short: lean and full replies agree.
	if lean != full {
		t.Errorf("short chunks are sent whole\nlean:\n%s\nfull:\n%s", lean, full)
	}
	// A long lower-ranked chunk is listed unless bodies: true.
	q = "big_report lines and how charge works"
	lean, _ = call(t, newConnected(t), "search", map[string]any{"question": q, "repo_path": repo})
	full, _ = call(t, newConnected(t), "search", map[string]any{"question": q, "repo_path": repo, "bodies": true})
	if strings.Contains(full, "other matches, best first") || len(lean) > len(full) {
		t.Errorf("lean %d bytes, full %d bytes\nlean:\n%s\nfull:\n%s", len(lean), len(full), lean, full)
	}
}

// newConnected is a fresh server and session, so no chunk counts as sent.
func newConnected(t *testing.T) *mcpsdk.ClientSession {
	t.Helper()
	return connect(t, newTestServer(t))
}
