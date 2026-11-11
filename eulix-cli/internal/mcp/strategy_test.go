// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// scripted is a Retriever returning canned text per stage and recording calls.
type scripted struct {
	multi, open, anchored string
	errs                  map[Stage]error
	onOpen                func() // runs inside OpenEnded, e.g. to cancel the request
	calls                 []Stage
	anchors               []string // what Anchored was seeded with
}

func (r *scripted) MultiHop(_ context.Context, _ string, seeds, expansion []string, _ bool) (string, error) {
	r.calls = append(r.calls, StageMultiHop)
	return r.multi, r.errs[StageMultiHop]
}

func (r *scripted) OpenEnded(context.Context, string, bool) (string, error) {
	r.calls = append(r.calls, StageOpenEnded)
	if r.onOpen != nil {
		r.onOpen()
	}
	return r.open, r.errs[StageOpenEnded]
}

func (r *scripted) Anchored(_ context.Context, _ string, anchors []string, _ bool) (string, error) {
	r.calls = append(r.calls, StageAnchoredFromOpenEnded)
	r.anchors = anchors
	return r.anchored, r.errs[StageAnchoredFromOpenEnded]
}

// routerStyle renders a result the way the router does: context chunks with
// file:line headers, then the echoed query.
func routerStyle(q string, chunks ...string) string {
	var b strings.Builder
	b.WriteString("Context: some source chunks\n\n── SOURCE ──\n\n")
	for i, c := range chunks {
		b.WriteString("[" + itoa(i+1) + "] app/billing.py:" + itoa(10*i+1) + "-" + itoa(10*i+9) + " (score 0.90)\n```python\n" + c + "\n```\n\n")
	}
	b.WriteString("QUERY\n" + q + "\n")
	return b.String()
}

func cascade(t *testing.T, r Retriever, root, q string, known ...string) (string, RetrievalTrace, error) {
	t.Helper()
	s := NewServer(nil, nil, "", testLogger(t), WithRetriever(r))
	s.activeRepo = root
	return s.retrieveLocked(context.Background(), q, known, false)
}

const goodBody = "def payment_total(items):\n    \"\"\"Sum every payment in the batch.\"\"\"\n    return sum(i.amount for i in items)"

// vague has no anchors, and every one of its terms appears in goodBody.
const vague = "how does every payment sum"

func TestCascadeVagueQuestionSkipsMultiHop(t *testing.T) {
	r := &scripted{open: routerStyle(vague, goodBody, goodBody, goodBody)}
	got, trace, err := cascade(t, r, "", vague)
	if err != nil || trace.Chosen != StageOpenEnded || len(trace.Attempts) != 1 || !slices.Equal(r.calls, []Stage{StageOpenEnded}) {
		t.Fatalf("calls=%v trace=%s err=%v", r.calls, trace, err)
	}
	if strings.HasPrefix(got, lexicalPreface) {
		t.Error("a semantic answer must not carry the lexical preface")
	}
}

func TestCascadeAnchorsOrKnownSymbolsStartWithMultiHop(t *testing.T) {
	for name, tc := range map[string]struct {
		q     string
		known []string
	}{
		"anchor in the query": {"payment_total", nil},
		"known_symbols only":  {vague, []string{"payment_total"}},
	} {
		r := &scripted{multi: routerStyle(tc.q, goodBody, goodBody, goodBody)}
		_, trace, err := cascade(t, r, "", tc.q, tc.known...)
		if err != nil || trace.Chosen != StageMultiHop || len(trace.Attempts) != 1 || !slices.Equal(r.calls, []Stage{StageMultiHop}) {
			t.Errorf("%s: calls=%v trace=%s err=%v", name, r.calls, trace, err)
		}
	}
}

// Regression: a one-symbol query was scored with every occurrence of the
// symbol stripped (as an "echo"), so precise semantic hits scored ~0.1 and
// lost to grep output.
func TestCascadeSingleSymbolQueryKeepsItsSemanticAnswer(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"app/billing.py": goodBody + "\n"})
	q := "payment_total"
	r := &scripted{multi: routerStyle(q, goodBody)}
	_, trace, err := cascade(t, r, root, q)
	if err != nil || trace.Chosen != StageMultiHop {
		t.Fatalf("a precise single-chunk hit should win at multi-hop: %s (err %v)", trace, err)
	}
}

func TestCascadeFallsBackToLexicalWithPreface(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"billing/pay.py": "def zebra_quokka(herd):\n    \"\"\"Count every zebra and quokka in the herd.\"\"\"\n" +
		"    zebras = [a for a in herd if a.kind == 'zebra']\n    quokkas = [a for a in herd if a.kind == 'quokka']\n    return len(zebras), len(quokkas)\n"})
	r := &scripted{open: "nothing useful"}
	got, trace, err := cascade(t, r, root, "zebra quokka")
	if err != nil || trace.Chosen != StageLexical || !strings.HasPrefix(got, lexicalPreface) || !strings.Contains(got, "billing/pay.py") {
		t.Fatalf("trace=%s err=%v:\n%s", trace, err, got)
	}
}

func TestCascadeReturnsBestWeakResultWhenNothingPasses(t *testing.T) {
	q := "how are refunds reconciled"
	weak := routerStyle(q, "def unrelated():\n    pass  # refunds are mentioned here only")
	r := &scripted{open: weak}
	got, trace, err := cascade(t, r, t.TempDir(), q)
	if err != nil || trace.Chosen != StageOpenEnded || got != weak {
		t.Fatalf("want the weak open-ended result as best effort: trace=%s err=%v", trace, err)
	}
}

func TestCascadeNoResultsAnywhere(t *testing.T) {
	got, trace, err := cascade(t, &scripted{}, t.TempDir(), "zebra quokka")
	if err != nil || got != noContextMessage || trace.Chosen != "" {
		t.Fatalf("got %q trace=%s err=%v", got, trace, err)
	}
}

func TestCascadeErrors(t *testing.T) {
	boom := errors.New("index unavailable")
	r := &scripted{errs: map[Stage]error{StageOpenEnded: boom}}
	if _, _, err := cascade(t, r, t.TempDir(), "zebra quokka"); !errors.Is(err, boom) {
		t.Fatalf("every stage failed: want the backend error, got %v", err)
	}

	root := t.TempDir()
	writeTree(t, root, map[string]string{"pay.py": "def zebra_quokka(): return 'zebra quokka'\n"})
	if got, trace, err := cascade(t, r, root, "zebra quokka"); err != nil || trace.Chosen != StageLexical || got == "" {
		t.Fatalf("a failing backend must not hide a lexical answer: trace=%s err=%v", trace, err)
	}
}

func TestCascadeHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &scripted{open: "weak", onOpen: cancel}
	s := NewServer(nil, nil, "", testLogger(t), WithRetriever(r))
	s.activeRepo = t.TempDir()
	if _, _, err := s.retrieveLocked(ctx, "zebra quokka", nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if slices.Contains(r.calls, StageAnchoredFromOpenEnded) {
		t.Error("stages kept running after the request was cancelled")
	}
}

// The anchored stage is seeded with symbols from the open-ended result whose
// name parts overlap the query: this is how a vague question becomes concrete.
func TestCascadeSeedsAnchoredStageFromOpenEndedResult(t *testing.T) {
	q := "how is the tax computed"
	r := &scripted{open: "see compute_tax_rate and format_output in billing", anchored: ""}
	_, _, _ = cascade(t, r, t.TempDir(), q)
	if !slices.Contains(r.anchors, "compute_tax_rate") || slices.Contains(r.anchors, "format_output") {
		t.Fatalf("anchored stage seeds = %v", r.anchors)
	}
}

// Memory only learns from answers that cleared the bar, so noise cannot
// steer later multi-hop expansions.
func TestCascadeMemoryLearnsOnlyFromTrustedResults(t *testing.T) {
	q := vague
	weakText := routerStyle(q, "tiny tax_helper")
	r := &scripted{open: weakText}
	s := NewServer(nil, nil, "", testLogger(t), WithRetriever(r))
	s.activeRepo = t.TempDir()
	if _, _, err := s.retrieveLocked(context.Background(), q, nil, false); err != nil {
		t.Fatal(err)
	}
	if n := s.memoryLocked().size(); n != 0 {
		t.Fatalf("memory learned %d symbols from a result below the quality bar", n)
	}

	r.open = routerStyle(q, goodBody+"\n    tax_rate = load_tax_rate()", goodBody, goodBody)
	if _, _, err := s.retrieveLocked(context.Background(), q, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := s.memoryLocked().expand(ExtractAnchors("payment_total"), 8); !slices.Contains(got, "load_tax_rate") {
		t.Fatalf("memory should link payment_total to its neighbours, got %v", got)
	}
}

func TestAssess(t *testing.T) {
	q := "payment_total"
	anchors := ExtractAnchors(q)
	if s := assess(q, anchors, "short").Score; s != 0 {
		t.Errorf("tiny result scored %v", s)
	}
	if s := assess(q, anchors, "No results found for payment_total. "+strings.Repeat("padding ", 20)).Score; s != 0 {
		t.Errorf("short refusal scored %v", s)
	}
	if s := assess(q, anchors, "Context: 0 source chunks\n\nQUERY\n"+q+"\n"+strings.Repeat("Relevant symbols : [payment_total]\n", 10)).Score; s != 0 {
		t.Errorf("an answer that only echoes the query scored %v", s)
	}
	echo := "how are payments summed across batches"
	if s := assess(echo, nil, strings.Repeat(echo+"\n", 8), echo).Score; s != 0 {
		t.Errorf("a result made of the echoed question scored %v", s)
	}
	for _, res := range []string{routerStyle(q, goodBody), strings.Repeat("payment_total ", 2000)} {
		if s := assess(q, anchors, res).Score; s < DefaultStrategyConfig().MinQuality || s > 1 {
			t.Errorf("score %v outside [MinQuality, 1] for a real hit", s)
		}
	}
}

func TestExtractAnchors(t *testing.T) {
	cases := []struct {
		q    string
		want []string
	}{
		{"How does SQLCompiler.get_from_clause() work?", []string{"SQLCompiler.get_from_clause", "SQLCompiler"}},
		{"fix internal/mcp/server.go and utils.go", []string{"internal/mcp/server.go", "utils.go"}},
		{"what is `main` for", []string{"main"}},                                          // backticks mark code even for plain words
		{"call foo( now", []string{"foo"}},                                                // call syntax
		{"e.g. this, i.e. that", nil},                                                     // abbreviations are not qualified names
		{"how do payments work", nil},                                                     // plain English has no anchors
		{"parse_config parseConfig ParseConfig", []string{"parse_config", "parseConfig"}}, // case-insensitive dedupe
		{"Router::Close and a->b", []string{"Router::Close"}},                             // "Router" alone is plain-word shaped
	}
	for _, tc := range cases {
		if got := anchorTexts(ExtractAnchors(tc.q)); !slices.Equal(got, tc.want) {
			t.Errorf("ExtractAnchors(%q) = %q, want %q", tc.q, got, tc.want)
		}
	}
	var many []string
	for i := range 30 {
		many = append(many, "sym_"+itoa(i))
	}
	if got := ExtractAnchors(strings.Join(many, " ")); len(got) != maxAnchors {
		t.Errorf("anchors not capped: %d", len(got))
	}
	if got := anchorsFromHints(append(many, "", "  ")); len(got) != maxHints {
		t.Errorf("hints not capped: %d", len(got))
	}
}

func TestTokenising(t *testing.T) {
	for in, want := range map[string][]string{
		"HTTPServer.Start":   {"http", "server", "start"},
		"resolve_expression": {"resolve", "expression"},
		"SQLCompiler":        {"sql", "compiler"},
	} {
		if got := splitIdent(in); !slices.Equal(got, want) {
			t.Errorf("splitIdent(%q) = %q, want %q", in, got, want)
		}
	}
	if got := queryTerms("How does the payment payment work in the API?"); !slices.Equal(got, []string{"payment", "api"}) {
		t.Errorf("queryTerms = %q", got)
	}
}

func TestProjectMemory(t *testing.T) {
	m := newProjectMemory(10)
	m.Observe("payment_total calls load_tax_rate\n\nunrelated_symbol stands alone")
	if got := m.expand(ExtractAnchors("payment_total"), 8); !slices.Equal(got, []string{"load_tax_rate"}) {
		t.Errorf("expand = %v (only same-block neighbours, never the anchor itself)", got)
	}
	for i := range 50 {
		m.Observe("block_symbol_" + itoa(i))
	}
	if m.size() > 10 {
		t.Errorf("memory grew past its cap: %d", m.size())
	}
	if got := m.derive("how is the tax computed", "compute_tax_rate and format_output", 8); !slices.Equal(got, []string{"compute_tax_rate"}) {
		t.Errorf("derive = %v", got)
	}
	if got := m.derive("anything", "  ", 8); got != nil {
		t.Errorf("derive on an empty result = %v", got)
	}
}

func TestURIPath(t *testing.T) {
	cases := []struct {
		uri     string
		windows bool
		want    string
	}{
		{"file:///home/u/repo", false, "/home/u/repo"},
		{"file://localhost/home/u/repo", false, "/home/u/repo"},
		{"file:///home/u/my%20repo", false, "/home/u/my repo"},
		{" /plain/path ", false, "/plain/path"},
		{"file:///C:/Users/u/repo", true, "C:/Users/u/repo"},
		{"file:///c%3A/Users/u/repo", true, "c:/Users/u/repo"}, // VS Code's encoding
		{"file://server/share/repo", true, "//server/share/repo"},
		{"file://localhost/C:/Users/u/repo", true, "C:/Users/u/repo"},
		{"file:///%3F/C:/very/long/repo", true, "//?/C:/very/long/repo"},
		{"file:///%3F/UNC/server/share/repo", true, "//?/UNC/server/share/repo"},
		{"file:///C:/x", false, "/C:/x"},
	}
	for _, tc := range cases {
		// Only file:// URIs become OS-native paths; anything else is returned verbatim.
		want := filepath.FromSlash(tc.want)
		if !strings.HasPrefix(strings.TrimSpace(tc.uri), "file://") {
			want = tc.want
		}
		if got := uriPath(tc.uri, tc.windows); got != want {
			t.Errorf("uriPath(%q, windows=%v) = %q, want %q", tc.uri, tc.windows, got, tc.want)
		}
	}
	if got := splitList(" a, ,b ,,"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("splitList = %q", got)
	}
}

func TestRetrievalTraceString(t *testing.T) {
	tr := RetrievalTrace{
		Anchors: []string{"payment_total"},
		Attempts: []StageAttempt{
			{Stage: StageMultiHop, Err: errors.New("boom")},
			{Stage: StageOpenEnded, Note: "empty"},
			{Stage: StageLexical, Score: 0.912},
		},
		Chosen: StageLexical,
	}
	want := "anchors=[payment_total] path=[multi-hop(error: boom) -> open-ended(empty) -> lexical(0.91)] chosen=lexical"
	if got := tr.String(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
