// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eulix/internal/config"
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

// Runs on every OS, so the bit logic is covered even from Linux CI.
func TestAttrsHidden(t *testing.T) {
	tests := []struct {
		attrs uint32
		want  bool
	}{
		{0x0, false},
		{0x2, true},   // HIDDEN
		{0x22, true},  // HIDDEN | ARCHIVE
		{0x26, true},  // HIDDEN | SYSTEM | ARCHIVE
		{0x20, false}, // ARCHIVE only
		{0x10, false}, // DIRECTORY only
		{0x4, false},  // SYSTEM only
	}
	for _, tc := range tests {
		if got := attrsHidden(tc.attrs); got != tc.want {
			t.Errorf("attrsHidden(%#x) = %v, want %v", tc.attrs, got, tc.want)
		}
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
		s := NewServer(nil, cfg, "", WithRetriever(emptyRetriever{}))
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
