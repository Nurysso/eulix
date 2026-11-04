// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

// End-to-end tests: a real MCP client talks JSON-RPC to the real server over
// in-memory transports, and the server binds real initialized projects and
// answers with the real query router. Projects use the Ehnas engine, which
// builds its index in memory, so no Python embedder is involved.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/utils"
)

// fxFunc is one function the fixture's knowledge base claims exists.
type fxFunc struct {
	file, name string
	start, end int
}

// newProject writes an initialized project: source files plus the .eulix
// artifacts the router loads, in the shapes eulix-parser writes them.
func newProject(t *testing.T, files map[string]string, funcs []fxFunc) string {
	t.Helper()
	root := t.TempDir()
	structure := map[string]utils.FileData{}
	byName := map[string][]string{}
	for _, f := range funcs {
		fd := structure[f.file]
		fd.Language = "python"
		fd.Functions = append(fd.Functions, utils.KBFunction{
			ID: "func_" + f.name + "::" + f.file, Name: f.name, Signature: "def " + f.name + "()",
			LineStart: f.start, LineEnd: f.end,
		})
		structure[f.file] = fd
		byName[f.name] = append(byName[f.name], f.file+":"+itoa(f.start))
	}
	kb, _ := json.Marshal(utils.KnowledgeBaseSimplifiedRef{
		Metadata:  &utils.Metadata{ProjectName: filepath.Base(root), Languages: []string{"python"}, TotalFiles: len(structure)},
		Structure: structure,
	})
	idx, _ := json.Marshal(utils.IndexDataRef{Indices: &utils.Indices{FunctionsByName: byName}})

	all := map[string]string{
		"eulix.toml":                "[project]\nengine = \"ehnas\"\n",
		".eulix/kb.json":            string(kb),
		".eulix/kb_index.json":      string(idx),
		".eulix/kb_call_graph.json": `{"nodes":[],"edges":[]}`,
	}
	for k, v := range files {
		all[k] = v
	}
	writeTree(t, root, all)
	return root
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// connect starts s on one end of an in-memory transport and returns a
// client session on the other, speaking the SDK's latest protocol version.
func connect(t *testing.T, s *Server, roots ...string) *mcpsdk.ClientSession {
	t.Helper()
	return connectWith(t, s, "", roots...)
}

// connectWith is connect pinned to a protocol version ("" for the latest).
// roots are advertised as the client's roots.
func connectWith(t *testing.T, s *Server, version string, roots ...string) *mcpsdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ct, st := mcpsdk.NewInMemoryTransports()
	ss, err := s.mcp.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "eulix-test-client", Version: "1"}, nil)
	for _, r := range roots {
		//nolint:staticcheck // roots are deprecated, but legacy clients still send them
		c.AddRoots(&mcpsdk.Root{URI: "file://" + filepath.ToSlash(r)})
	}
	cs, err := c.Connect(ctx, ct, &mcpsdk.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
		cancel()
		_ = s.Close()
	})
	return cs
}

// newTestServer returns a server with no repository bound, isolated from the
// developer's real ~/.Eulix.
func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return NewServer(nil, nil, "", opts...)
}

// call invokes a tool and returns its text and whether it reported an error.
func call(t *testing.T, cs *mcpsdk.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// billing is a small project with one real function the KB knows about.
func billing(t *testing.T, extra map[string]string, extraFuncs ...fxFunc) string {
	t.Helper()
	files := map[string]string{
		"app/billing.py": "def payment_total(items):\n    \"\"\"Sum the payments.\"\"\"\n    return sum(i.amount for i in items)\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return newProject(t, files, append([]fxFunc{{"app/billing.py", "payment_total", 1, 3}}, extraFuncs...))
}

func TestE2EToolsAndPromptsAreAdvertised(t *testing.T) {
	cs := connect(t, newTestServer(t))
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcpsdk.Tool{}
	for _, tool := range tools.Tools {
		got[tool.Name] = tool
	}
	for _, name := range []string{"set_repo_path", "query", "query_with_cot", "query_without_cot"} {
		if got[name] == nil {
			t.Fatalf("tool %q missing; have %v", name, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("unexpected extra tools: %v", got)
	}
	// Query tools only read; agents rely on the hint to skip confirmation prompts.
	for _, name := range []string{"query", "query_with_cot", "query_without_cot"} {
		if a := got[name].Annotations; a == nil || !a.ReadOnlyHint {
			t.Errorf("%s should be annotated read-only", name)
		}
	}
	schema, _ := json.Marshal(got["query"].InputSchema)
	if !strings.Contains(string(schema), `"required":["query"]`) {
		t.Errorf("query schema should require 'query': %s", schema)
	}

	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) != 2 {
		t.Fatalf("prompts = %v, %v", prompts, err)
	}
}

func TestE2ESetRepoPathRejectsBadPaths(t *testing.T) {
	s := newTestServer(t)
	cs := connect(t, s)
	file := filepath.Join(t.TempDir(), "f.txt")
	writeTree(t, filepath.Dir(file), map[string]string{"f.txt": "x"})

	for name, tc := range map[string]struct{ path, want string }{
		"empty":           {"   ", "cannot be empty"},
		"missing":         {filepath.Join(t.TempDir(), "nope"), "does not exist"},
		"a file":          {file, "not a directory"},
		"not initialized": {t.TempDir(), "eulix analyze"},
	} {
		if text, isErr := call(t, cs, "set_repo_path", map[string]any{"repo_path": tc.path}); !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%s: got isError=%v %q, want an error containing %q", name, isErr, text, tc.want)
		}
	}
	if s.GetActiveRepo() != "" {
		t.Fatalf("a rejected path was bound: %q", s.GetActiveRepo())
	}

	repo := billing(t, nil)
	if text, isErr := call(t, cs, "set_repo_path", map[string]any{"repo_path": repo}); isErr || !strings.Contains(text, repo) {
		t.Fatalf("valid repo: isError=%v %q", isErr, text)
	}
	if text, _ := call(t, cs, "set_repo_path", map[string]any{"repo_path": repo + "/"}); !strings.Contains(text, "already set") {
		t.Errorf("rebinding the same repo should be a no-op: %q", text)
	}
}

func TestE2EQueryArgumentErrors(t *testing.T) {
	cs := connect(t, newTestServer(t))
	if text, isErr := call(t, cs, "query", map[string]any{"query": "payment_total"}); !isErr || !strings.Contains(text, "set_repo_path") {
		t.Errorf("no repo bound: isError=%v %q", isErr, text)
	}
	if text, isErr := call(t, cs, "query", map[string]any{"query": " \t"}); !isErr || !strings.Contains(text, "non-empty") {
		t.Errorf("blank query: isError=%v %q", isErr, text)
	}
	if _, isErr := call(t, cs, "query", map[string]any{}); !isErr {
		t.Error("missing 'query' should be rejected by the schema")
	}
	if _, err := cs.GetPrompt(context.Background(), &mcpsdk.GetPromptParams{Name: "query_with_cot", Arguments: map[string]string{}}); err == nil {
		t.Error("prompt without 'query' should fail")
	}
}

func TestE2EQueryReturnsTheFunctionSource(t *testing.T) {
	repo := billing(t, nil)
	cs := connect(t, newTestServer(t))
	text, isErr := call(t, cs, "query", map[string]any{"query": "payment_total", "repo_path": repo})
	if isErr || !strings.Contains(text, "return sum(i.amount for i in items)") {
		t.Fatalf("want the hydrated body of payment_total, isError=%v:\n%s", isErr, text)
	}
}

// leakyProject holds secrets in every place the server must never read: hidden
// files and directories, root .euignore rules (plain, globstar), a nested
// .euignore, and built-in skipped directories. Its knowledge base also lists
// some of them, as a stale KB (rules added after 'eulix analyze') or a KB
// shipped inside a cloned repository would.
func leakyProject(t *testing.T) string {
	t.Helper()
	secret := func(name string) string {
		return "def " + name + "():\n    payment_key = '" + secretMarker + "'\n"
	}
	return billing(t, map[string]string{
		".env":                      "PAYMENT_KEY=" + secretMarker + "\n",
		".config/payment.py":        secret("payment_hidden_dir"),
		".euignore":                 "secrets.py\nconfig/**/secrets.py\n**/private/**\n",
		"secrets.py":                secret("payment_secret"),
		"config/secrets.py":         secret("payment_config"),
		"config/eu/prod/secrets.py": secret("payment_config_eu"),
		"a/private/x.py":            secret("payment_private"),
		"svc/payments/.euignore":    "creds.py\n",
		"svc/payments/creds.py":     secret("payment_creds"),
		"node_modules/pay/index.py": secret("payment_vendored"),
	},
		fxFunc{".env", "payment_env", 1, 1},
		fxFunc{"secrets.py", "payment_secret", 1, 2},
		fxFunc{"config/eu/prod/secrets.py", "payment_config_eu", 1, 2},
		fxFunc{"svc/payments/creds.py", "payment_creds", 1, 2},
	)
}

// The MCP server is reachable by any agent the user runs, so nothing a user
// hid or excluded may come back through any tool, prompt or retrieval stage.
func TestE2ENeverReturnsHiddenOrIgnoredFiles(t *testing.T) {
	repo := leakyProject(t)
	cs := connect(t, newTestServer(t))
	if _, isErr := call(t, cs, "set_repo_path", map[string]any{"repo_path": repo}); isErr {
		t.Fatal("bind failed")
	}

	queries := []map[string]any{
		{"query": "payment"},          // plain term: semantic then lexical
		{"query": "payment key"},      // words from the secret line itself
		{"query": "zzqx payment_key"}, // forces the lexical fallback
		{"query": "payment_env"},      // anchors naming the KB entries
		{"query": "payment_secret payment_creds"},
		{"query": "Where is payment_config_eu defined?"},
		{"query": "payment", "known_symbols": []string{".env", "secrets.py", "svc/payments/creds.py", "payment_private"}},
		{"query": "`config/eu/prod/secrets.py` payment"},
	}
	check := func(where, text string) {
		t.Helper()
		if strings.Contains(text, secretMarker) {
			t.Errorf("%s leaked a secret:\n%s", where, text)
		}
	}
	for _, q := range queries {
		for _, tool := range []string{"query", "query_with_cot", "query_without_cot"} {
			text, _ := call(t, cs, tool, q)
			check(tool+" "+q["query"].(string), text)
		}
		res, err := cs.GetPrompt(context.Background(), &mcpsdk.GetPromptParams{
			Name: "query_without_cot", Arguments: map[string]string{"query": q["query"].(string)},
		})
		if err == nil {
			for _, m := range res.Messages {
				check("prompt "+q["query"].(string), m.Content.(*mcpsdk.TextContent).Text)
			}
		}
	}
}

// A usage query reads each "file:line" the index lists. Those locations come
// from the repository's own .eulix files, so they must be read inside the
// repository (whatever the server's working directory) and never outside it.
func TestE2EUsageQueryReadsOnlyInsideTheRepo(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "id_rsa")
	writeTree(t, filepath.Dir(outside), map[string]string{"id_rsa": "-----BEGIN KEY " + secretMarker + "\n"})
	repo := billing(t, map[string]string{".env": "KEY=" + secretMarker + "\n"})
	rel, _ := filepath.Rel(repo, outside)
	idx, _ := json.Marshal(utils.IndexDataRef{Indices: &utils.Indices{FunctionsByName: map[string][]string{
		"payment_total": {"app/billing.py:1", outside + ":1", filepath.ToSlash(rel) + ":1", ".env:1"},
	}}})
	writeTree(t, repo, map[string]string{".eulix/kb_index.json": string(idx)})

	cs := connect(t, newTestServer(t))
	text, _ := call(t, cs, "query", map[string]any{"query": "usage of payment_total", "repo_path": repo})
	if strings.Contains(text, secretMarker) {
		t.Fatalf("usage query read a file outside the repository or a hidden one:\n%s", text)
	}
	if !strings.Contains(text, "def payment_total(items):") {
		t.Fatalf("usage query should read app/billing.py relative to the repo, not the working directory:\n%s", text)
	}
}

// Knowledge bases go stale: files shrink after 'eulix analyze'. A KB line
// range past the end of a file, inverted, or zero must not fail the query or
// take the server down (the SDK does not recover handler panics).
func TestE2EStaleLineRangesDoNotBreakQueries(t *testing.T) {
	repo := billing(t, map[string]string{"app/short.py": "def payment_refund():\n    pass\n"},
		fxFunc{"app/short.py", "payment_refund", 40, 90},
		fxFunc{"app/short.py", "payment_void", 9, 2},
		fxFunc{"app/short.py", "payment_zero", 0, 0},
		fxFunc{"app/gone.py", "payment_gone", 1, 5},
	)
	cs := connect(t, newTestServer(t))
	for _, q := range []string{"payment_refund", "payment_void payment_zero", "payment_gone", "usage of payment_refund", "payment"} {
		if text, isErr := call(t, cs, "query", map[string]any{"query": q, "repo_path": repo}); isErr {
			t.Errorf("%q failed on a stale KB: %s", q, text)
		}
	}
	if text, _ := call(t, cs, "query", map[string]any{"query": "payment_total"}); !strings.Contains(text, "return sum") {
		t.Fatalf("server stopped answering after stale-KB queries:\n%s", text)
	}
}

// ExecuteQuery promises that switching and querying happen under one lock, so
// concurrent calls naming different repositories never see each other's code.
func TestE2EConcurrentQueriesStayInTheirRepo(t *testing.T) {
	alpha := newProject(t, map[string]string{"a.py": "def alpha_total():\n    return 'ALPHA_BODY'\n"}, []fxFunc{{"a.py", "alpha_total", 1, 2}})
	beta := newProject(t, map[string]string{"b.py": "def beta_total():\n    return 'BETA_BODY'\n"}, []fxFunc{{"b.py", "beta_total", 1, 2}})
	cs := connect(t, newTestServer(t))

	errs := make(chan string, 64)
	done := make(chan struct{})
	for w := range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := range 6 {
				repo, q, own, other := alpha, "alpha_total", "ALPHA_BODY", "BETA_BODY"
				if (w+i)%2 == 1 {
					repo, q, own, other = beta, "beta_total", "BETA_BODY", "ALPHA_BODY"
				}
				res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
					Name: "query", Arguments: map[string]any{"query": q, "repo_path": repo},
				})
				if err != nil {
					errs <- err.Error()
					continue
				}
				text := res.Content[0].(*mcpsdk.TextContent).Text
				if res.IsError || !strings.Contains(text, own) || strings.Contains(text, other) {
					errs <- q + " got the wrong repository's answer:\n" + text
				}
			}
		}()
	}
	for range 8 {
		<-done
	}
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// Roots arrive after the legacy initialize handshake. Clients on protocol
// 2026-07-28 use stateless discovery and send no 'initialized', and roots are
// deprecated there (SEP-2577): such clients pass repo_path instead.
func TestE2ERootsBindTheFirstInitializedRoot(t *testing.T) {
	repo := billing(t, nil)
	s := newTestServer(t)
	connectWith(t, s, "2025-11-25", t.TempDir(), repo) // the first root is not initialized
	waitFor(t, "roots binding", func() bool { return s.GetActiveRepo() == repo })

	latest := newTestServer(t)
	cs := connect(t, latest, repo)
	time.Sleep(100 * time.Millisecond)
	if latest.GetActiveRepo() != "" {
		t.Fatal("unexpected roots binding without the legacy handshake")
	}
	if text, _ := call(t, cs, "query", map[string]any{"query": "payment_total", "repo_path": repo}); !strings.Contains(text, "return sum") {
		t.Fatalf("repo_path must work for clients without roots:\n%s", text)
	}
}

// Roots binding runs on its own goroutine. Whichever of it and set_repo_path
// takes the lock first, the explicit choice must end up active. (The old gap
// between the "nothing bound?" check and the bind was too narrow to hit from
// a test; the check and bind now share one lock.)
func TestRootsNeverOverrideAnExplicitChoice(t *testing.T) {
	chosen, root := billing(t, nil), billing(t, nil)
	for i := range 20 {
		s := newTestServer(t)
		done := make(chan struct{})
		go func() { s.bindFromRoots([]string{root}); close(done) }()
		if _, err := s.SetRepoPath(chosen); err != nil {
			t.Fatal(err)
		}
		<-done
		if got := s.GetActiveRepo(); got != chosen {
			t.Fatalf("iteration %d: roots replaced the explicitly chosen repository with %s", i, got)
		}
		_ = s.Close()
	}
}

func TestE2EIdleReleaseThenServeAgain(t *testing.T) {
	repo := billing(t, nil)
	s := newTestServer(t, WithIdleTimeout(50*time.Millisecond))
	cs := connect(t, s)
	if text, _ := call(t, cs, "query", map[string]any{"query": "payment_total", "repo_path": repo}); !strings.Contains(text, "return sum") {
		t.Fatalf("first query:\n%s", text)
	}
	waitFor(t, "idle release", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.router.HasContextBuilder()
	})
	if text, _ := call(t, cs, "query", map[string]any{"query": "payment_total"}); !strings.Contains(text, "return sum") {
		t.Fatalf("query after the idle release:\n%s", text)
	}
}

// MCP never reads or writes chat history, so binding a repository must not
// open (and lock) .eulix/history.db: that would make 'eulix chat' and
// 'eulix history' in the same project fail while an editor runs the server.
func TestE2EBindingDoesNotLockChatHistory(t *testing.T) {
	repo := billing(t, nil)
	t.Chdir(repo) // editors usually start the server in the workspace
	s := newTestServer(t)
	if _, err := s.SetRepoPath(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg, err := config.LoadFrom(repo)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	m, err := cache.CacheController(cfg)
	if err != nil {
		t.Fatalf("history.db unavailable while the MCP server runs (after %s): %v", time.Since(start), err)
	}
	if m != nil {
		_ = m.Close()
	}
}

// stdout carries the JSON-RPC stream, so nothing on the request path may print
// to it. This drives the paths known to have printed: an unreadable-length
// .euignore line, and a debug-mode context dump that cannot be written.
func TestE2ENothingIsWrittenToStdout(t *testing.T) {
	repo := billing(t, map[string]string{
		"eulix.toml":             "[project]\nengine = \"ehnas\"\nDebugConfig = true\n",
		".euignore":              strings.Repeat("x", 70_000) + "\n",
		".eulix/debug/retrieval": "a file where the debug dump directory should be",
	})
	cs := connect(t, newTestServer(t))

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	for _, q := range []string{"payment_total", "zzqx payment"} { // semantic, then lexical
		call(t, cs, "query", map[string]any{"query": q, "repo_path": repo})
	}
	os.Stdout = stdout
	_ = w.Close()
	if stray, _ := io.ReadAll(r); len(stray) > 0 {
		t.Fatalf("the request path wrote to stdout, corrupting the protocol stream: %q", stray)
	}
}

// waitFor polls cond, since some server work (roots binding, idle release)
// happens on its own goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
