// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
)

const secretMarker = "SYNTHETIC_SECRET"

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func runLexical(t *testing.T, root, q string) string {
	t.Helper()
	got, err := lexicalSearch(context.Background(), root, q, nil, DefaultStrategyConfig())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLexicalSearchSkipsHiddenAndIgnoredFiles(t *testing.T) {
	leak := "payment_key = '" + secretMarker + "'\n"

	tests := []struct {
		name    string
		files   map[string]string
		wantNot []string // content or path fragments that must NOT appear
	}{
		{
			name: "hidden file at root",
			files: map[string]string{
				".env": "PAYMENT_TOKEN=" + secretMarker + "\n",
			},
			wantNot: []string{secretMarker, ".env"},
		},
		{
			name: "hidden file in subdirectory",
			files: map[string]string{
				"billing/.npmrc": "payment_token=" + secretMarker + "\n",
			},
			wantNot: []string{secretMarker, ".npmrc"},
		},
		{
			name: "hidden file whose name matches the query",
			files: map[string]string{
				".payment_notes": "payment " + secretMarker + "\n",
			},
			wantNot: []string{secretMarker, ".payment_notes"},
		},
		{
			name: "hidden directory",
			files: map[string]string{
				".secrets/creds.py": leak,
				".git/config":       "payment = " + secretMarker + "\n",
			},
			wantNot: []string{secretMarker, ".secrets", ".git"},
		},
		{
			name: "euignore exact filename",
			files: map[string]string{
				".euignore": "secret.py\n",
				"secret.py": leak,
			},
			wantNot: []string{secretMarker, "secret.py"},
		},
		{
			name: "euignore filename matches in nested dir",
			files: map[string]string{
				".euignore":     "secret.py\n",
				"pkg/secret.py": leak,
			},
			wantNot: []string{secretMarker, "secret.py"},
		},
		{
			name: "euignore glob",
			files: map[string]string{
				".euignore":       "*.pem\n",
				"keys/server.pem": "payment " + secretMarker + "\n",
			},
			wantNot: []string{secretMarker, "server.pem"},
		},
		{
			name: "euignore directory",
			files: map[string]string{
				".euignore":      "private/\n",
				"private/pay.py": leak,
			},
			wantNot: []string{secretMarker, "private/"},
		},
		{
			name: "built-in skipDirs",
			files: map[string]string{
				"node_modules/pay/index.js": "// payment " + secretMarker + "\n",
				"vendor/pay/pay.go":         "// payment " + secretMarker + "\n",
			},
			wantNot: []string{secretMarker, "node_modules", "vendor"},
		},
		{
			name: "no euignore present",
			files: map[string]string{
				".env": "payment=" + secretMarker + "\n",
			},
			wantNot: []string{secretMarker},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"billing/pay.py": "def payment_total():\n    return 42\n",
			}
			for k, v := range tc.files {
				files[k] = v
			}
			writeTree(t, dir, files)

			got := runLexical(t, dir, "payment")

			for _, s := range tc.wantNot {
				if strings.Contains(got, s) {
					t.Errorf("result leaked %q:\n%s", s, got)
				}
			}
			if !strings.Contains(got, "payment_total") {
				t.Errorf("regular source file missing from results:\n%s", got)
			}
		})
	}
}

// The root-guard (path != root) must keep a hidden or dot-named root from
// excluding the whole search. On Windows, t.TempDir() already sits under the
// hidden AppData directory, which exercises the same rule.
func TestLexicalSearchHiddenRootIsStillSearched(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".project")
	writeTree(t, root, map[string]string{
		"billing/pay.py": "def payment_total():\n    return 42\n",
		".env":           "PAYMENT=" + secretMarker + "\n",
	})

	got := runLexical(t, root, "payment")
	if !strings.Contains(got, "payment_total") {
		t.Fatalf("regular file under dot-named root missing:\n%s", got)
	}
	if strings.Contains(got, secretMarker) {
		t.Fatalf("hidden file leaked:\n%s", got)
	}
}

// A visible symlink pointing at a hidden file must not be a way around the
// hidden check. The walk only reads regular files, so symlinks are skipped.
func TestLexicalSearchDoesNotFollowSymlinkToHiddenFile(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		".env":           "PAYMENT=" + secretMarker + "\n",
		"billing/pay.py": "def payment_total():\n    return 42\n",
	})
	if err := os.Symlink(filepath.Join(dir, ".env"), filepath.Join(dir, "config.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err) // Windows without privilege
	}

	got := runLexical(t, dir, "payment")
	if strings.Contains(got, secretMarker) {
		t.Fatalf("secret reached via symlink:\n%s", got)
	}
	if !strings.Contains(got, "payment_total") {
		t.Fatalf("regular file missing:\n%s", got)
	}
}

func TestLexExcluded(t *testing.T) {
	pats := []string{"docs/", "*.md", "scripts/eval_mcp.py", "eval_results_*.json"}
	for rel, want := range map[string]bool{
		"docs":                          true,
		"docs/a/b.go":                   true,
		"arch.bak/pr-commits.md":        true,
		"scripts/eval_mcp.py":           true,
		"eval_results_run2.json":        true,
		"eulix-cli/eval_results_x.json": true,
		"scripts/kb_diff.py":            false,
		"documents/a.go":                false,
	} {
		if got := lexExcluded(rel, pats); got != want {
			t.Errorf("lexExcluded(%q) = %v, want %v", rel, got, want)
		}
	}
}

type emptyRetriever struct{}

func (emptyRetriever) OpenEnded(context.Context, string, bool) (string, error) { return "", nil }
func (emptyRetriever) Anchored(context.Context, string, []string, bool) (string, error) {
	return "", nil
}
func (emptyRetriever) MultiHop(context.Context, string, []string, []string, bool) (string, error) {
	return "", nil
}

// lexical_exclude lives under [retrievalConfig.etiya]: the grep fallback
// honours it under etiya and ignores it under ehnas.
func TestLexicalExcludeOnlyAppliesToEtiya(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"scripts/eval_mcp.py": "zebra quokka zebra quokka\n",
		"cache/manager.go":    "// zebra quokka\n",
	})
	for engine, wantEval := range map[string]bool{"etiya": false, "ehnas": true} {
		cfg := config.DefaultConfig()
		cfg.Project.Engine = engine
		cfg.RetrievalConfig.Etiya.LexicalExclude = []string{"scripts/eval_mcp.py"}
		s := NewServer(nil, cfg, "", testLogger(t), WithRetriever(emptyRetriever{}))
		s.activeRepo = root
		got, _, err := s.retrieveLocked(context.Background(), "zebra quokka", nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "eval_mcp.py") != wantEval || !strings.Contains(got, "manager.go") {
			t.Errorf("%s: eval_mcp.py returned=%v (want %v), output:\n%s", engine, !wantEval, wantEval, got)
		}
	}
}

// ignoreCorpus is a tree of tricky .euignore cases. Every file is a Python
// function mentioning "secret", so one lexical query can see all of them.
func ignoreCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		// root rules: CRLF line endings, a >64 KiB line, then real patterns
		".euignore": strings.Repeat("x", 70_000) + "\r\n# comment\r\nconfig/**/secrets.py\r\n**/private/**\r\n" +
			"keys/**\r\n/anchored/\r\nlogs/\r\nneg/*.py\r\n!neg/keep.py\r\n",
		"svc/payments/.euignore": "creds.py\n",                                 // nested rules apply below their directory
		"crlf/.euignore":         "win.py\r\n",                                 // nested, CRLF
		"long/.euignore":         strings.Repeat("y", 70_000) + "\nafter.py\n", // nested, long line first
	}
	for _, f := range []string{
		"app/main.py",
		"config/secrets.py", "config/prod/secrets.py", "config/eu/prod/secrets.py", "config/topsecrets.py",
		"a/private/x.py", "private/y.py", "deep/a/b/private/z.py",
		"keys/k1.py", "keys/sub/k2.py",
		"svc/payments/creds.py", "svc/payments/ok.py", "svc/other/creds.py",
		"long/after.py", "crlf/win.py", "anchored/root.py", "sub/anchored/root.py",
		"logs/l.py", "data/logs.py", "neg/keep.py", "neg/drop.py",
	} {
		files[f] = "def secret_" + strings.NewReplacer("/", "_", ".", "_").Replace(f) + "():\n    return 1\n"
	}
	writeTree(t, root, files)
	return root
}

// lexicalFiles returns every file the lexical fallback shows for q.
func lexicalFiles(t *testing.T, root, q string) []string {
	t.Helper()
	cfg := DefaultStrategyConfig()
	cfg.LexicalMaxFiles, cfg.LexicalMaxBytes = 1000, 1<<30
	got, err := lexicalSearch(context.Background(), root, q, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(got, "\n") {
		if rest, ok := strings.CutPrefix(line, "### "); ok {
			seen[rest[:strings.LastIndex(rest, ":")]] = true
		}
	}
	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// The files eulix-parser indexes for ignoreCorpus (verified against it), less
// neg/keep.py: "!" negation is not supported here, so it stays hidden, which
// errs on the safe side.
var corpusVisible = []string{
	"app/main.py", "config/topsecrets.py", "data/logs.py",
	"sub/anchored/root.py", "svc/other/creds.py", "svc/payments/ok.py",
}

func TestLexicalSearchHonoursEuignoreLikeTheParser(t *testing.T) {
	if got := lexicalFiles(t, ignoreCorpus(t), "secret"); !slices.Equal(got, corpusVisible) {
		t.Fatalf("lexical search shows\n  %q\nwant\n  %q", got, corpusVisible)
	}
}

// The real oracle: whatever eulix-parser leaves out of the knowledge base, the
// lexical fallback must not show. Runs when the parser binary is available.
func TestLexicalSearchNeverShowsWhatTheParserExcludes(t *testing.T) {
	parser := os.Getenv("EULIX_PARSER")
	if parser == "" {
		parser = filepath.Join("..", "..", "..", "eulix-parser", "target", "release", "eulix_parser")
	}
	if _, err := os.Stat(parser); err != nil {
		t.Skipf("eulix-parser not built (set EULIX_PARSER): %v", err)
	}
	root := ignoreCorpus(t)
	out := filepath.Join(t.TempDir(), "kb.json")
	if msg, err := exec.Command(parser, "-r", root, "-o", out, "-p", "2", "-n").CombinedOutput(); err != nil {
		t.Fatalf("parser: %v\n%s", err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var kb utils.KnowledgeBaseSimplifiedRef
	if err := json.Unmarshal(data, &kb); err != nil {
		t.Fatal(err)
	}
	for _, f := range lexicalFiles(t, root, "secret") {
		if _, ok := kb.Structure[f]; !ok {
			t.Errorf("lexical search shows %s, which the parser excluded", f)
		}
	}
	for f := range kb.Structure {
		if !slices.Contains(corpusVisible, f) && f != "neg/keep.py" {
			t.Errorf("parser now indexes %s: update corpusVisible", f)
		}
	}
}

func TestLexicalSearchSkipsBinaryAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"billing/pay.py": "def payment_total():\n    return 42\n",
		"blob.bin":       "payment\x00" + secretMarker,
		"huge.txt":       "payment " + secretMarker + "\n" + strings.Repeat("x", maxLexFileBytes),
	})
	if got := runLexical(t, root, "payment"); strings.Contains(got, secretMarker) || !strings.Contains(got, "payment_total") {
		t.Fatalf("binary or oversized file returned:\n%s", got)
	}
}

func TestLexicalSearchCapsItsOutput(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	for i := range 40 {
		files["pkg/f"+itoa(i)+".py"] = strings.Repeat("payment line with some width to it\n", 30)
	}
	writeTree(t, root, files)
	cfg := DefaultStrategyConfig()
	got, err := lexicalSearch(context.Background(), root, "payment", nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "### "); n == 0 || len(lexicalFiles(t, root, "payment")) != 40 {
		t.Fatalf("expected matches, got %d windows", n)
	}
	files2 := map[string]bool{}
	for _, line := range strings.Split(got, "\n") {
		if rest, ok := strings.CutPrefix(line, "### "); ok {
			files2[rest[:strings.LastIndex(rest, ":")]] = true
		}
	}
	// One window (7 lines of at most 300 bytes) may overshoot the byte cap.
	if len(files2) > cfg.LexicalMaxFiles || len(got) > cfg.LexicalMaxBytes+8*310 {
		t.Fatalf("output not capped: %d files, %d bytes", len(files2), len(got))
	}
}

// Hits are trimmed during the walk, which must not lose a strong match found
// early or late.
func TestLexicalSearchTrimKeepsTheBestFiles(t *testing.T) {
	root := t.TempDir()
	strong := strings.Repeat("payment\n", 5)
	files := map[string]string{"a_first.py": strong, "z_last.py": strong}
	for i := range 40 {
		files["m/f"+itoa(i)+".py"] = "payment\n"
	}
	writeTree(t, root, files)
	cfg := DefaultStrategyConfig()
	cfg.LexicalMaxFiles = 2
	got, err := lexicalSearch(context.Background(), root, "payment", nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "a_first.py") || !strings.Contains(got, "z_last.py") || strings.Contains(got, "m/f") {
		t.Fatalf("wrong files kept:\n%s", got)
	}
}

func TestLexicalSearchStopsWhenCancelled(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"pay.py": "payment\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lexicalSearch(ctx, root, "payment", nil, DefaultStrategyConfig()); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
