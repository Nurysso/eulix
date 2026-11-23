// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"eulix/internal/kbfile"
	"eulix/internal/kbfile/kbfiletest"
	"eulix/internal/utils"
)

const victimBody = "IMPORTANT USER DATA\n"

func newVictim(t *testing.T) string {
	t.Helper()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte(victimBody), 0o600); err != nil {
		t.Fatal(err)
	}
	return victim
}

// moveAside renames the bound repository folder. Windows refuses while the
// server holds files open in it, so a bound folder cannot be swapped there.
func moveAside(t *testing.T, active string) {
	t.Helper()
	if err := os.Rename(active, active+"-moved"); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, fs.ErrPermission) {
			t.Skip("Windows cannot rename a folder with open files, so the bound folder cannot be replaced")
		}
		t.Fatal(err)
	}
}

func mustBeIntact(t *testing.T, victim string) {
	t.Helper()
	if data, err := os.ReadFile(victim); err != nil || string(data) != victimBody {
		t.Errorf("file outside the repository changed: %q, %v", data, err)
	}
}

func symlinkInto(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// A cloned repository can ship .eulix, so binding one must not write through or
// read through a link in it. This log used to be emptied with debugging off.
func TestBindRefusesSymlinksInEulixDir(t *testing.T) {
	for _, name := range []string{
		filepath.Join("debug", "context_debug.log"),
		filepath.Join("debug", "susbstemDebug.log"),
		"kb_entry_points.fb",
	} {
		t.Run(name, func(t *testing.T) {
			victim := newVictim(t)
			repo := billing(t, nil)
			symlinkInto(t, victim, filepath.Join(repo, ".eulix", name))
			t.Chdir(repo)

			s := newTestServer(t)
			_, err := s.SetRepoPath(repo)
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Errorf("bind error = %v, want a refusal naming the symlink", err)
			}
			mustBeIntact(t, victim)
		})
	}
}

// A pull can add a link after the bind check has already run.
func TestDebugLogLinkAddedAfterBindIsNotWrittenThrough(t *testing.T) {
	repo := billing(t, map[string]string{"eulix.toml": "[project]\nengine = \"ehnas\"\nDebugConfig = true\n"})
	s := newTestServer(t)
	if _, err := s.SetRepoPath(repo); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(repo, ".eulix", "debug", "mcp-query-debug.log")

	t.Run("control: the log is written", func(t *testing.T) {
		if _, err := s.ExecuteQuery(context.Background(), "", "payment_total", nil, false); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(log); !strings.Contains(string(data), "MCP QUERY DEBUG ENTRY") {
			t.Fatalf("debug log not written, so the check below proves nothing: %q", data)
		}
	})

	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	victim := newVictim(t)
	symlinkInto(t, victim, log)
	_, _ = s.ExecuteQuery(context.Background(), "", "payment_total", nil, false)
	mustBeIntact(t, victim)
}

// The index belongs to the folder that was bound. If another folder takes its
// place, answers must not mix that index with the other folder's files.
func TestQueryRefusesReplacedRepoFolder(t *testing.T) {
	const other = "OTHER_REPO_BODY"
	body := map[string]string{"app/billing.py": "def payment_total(items):\n    return '" + other + "'\n"}

	replace := func(t *testing.T, s *Server, with func(active string)) string {
		t.Helper()
		if _, err := s.SetRepoPath(billing(t, nil)); err != nil {
			t.Fatal(err)
		}
		active := s.GetActiveRepo()
		moveAside(t, active)
		with(active)
		return active
	}
	mustRefuse := func(t *testing.T, s *Server) {
		t.Helper()
		got, err := s.ExecuteQuery(context.Background(), "", "how does payment_total work", nil, false)
		if err == nil || !strings.Contains(err.Error(), "changed after it was bound") {
			t.Errorf("query error = %v, want a refusal", err)
		}
		if strings.Contains(got, other) {
			t.Errorf("answer came from the other repository:\n%s", got)
		}
	}

	t.Run("by a symlink", func(t *testing.T) {
		s := newTestServer(t)
		replace(t, s, func(active string) { symlinkInto(t, billing(t, body), active) })
		mustRefuse(t, s)
	})
	t.Run("removed", func(t *testing.T) {
		s := newTestServer(t)
		replace(t, s, func(string) {})
		mustRefuse(t, s)
	})
	t.Run("by a real folder, then bound again", func(t *testing.T) {
		s := newTestServer(t)
		active := replace(t, s, func(active string) {
			if err := os.Rename(billing(t, body), active); err != nil {
				t.Fatal(err)
			}
		})
		mustRefuse(t, s)

		msg, err := s.SetRepoPath(active)
		if err != nil || strings.Contains(msg, "already set") {
			t.Fatalf("rebind did not rebuild: %q, %v", msg, err)
		}
		if _, err := s.ExecuteQuery(context.Background(), "", "payment_total", nil, false); err != nil {
			t.Errorf("query after rebinding: %v", err)
		}
	})
}

// Entry points, metrics and dependencies come from their own .eulix files, which
// can predate the ignore rules like the index and call graph can.
func TestAnswersHideIgnoredFilesFromKBReports(t *testing.T) {
	repo := leakyProject(t)
	eulixDir := filepath.Join(repo, ".eulix")
	writeKB := func(name string, buf []byte) {
		t.Helper()
		if err := kbfiletest.WriteFile(filepath.Join(eulixDir, name), buf); err != nil {
			t.Fatal(err)
		}
	}
	writeKB(kbfile.EntryPointsFile, kbfiletest.EncodeEntryPoints([]utils.EntryPoint{
		{EntryType: "main", Handler: "visible_main", File: "app/billing.py", Line: 1},
		{EntryType: "main", Handler: "payment_secret", File: "secrets.py", Line: 1},
		{EntryType: "main", Handler: "hidden_main", File: ".config/payment.py", Line: 1},
	}))
	writeKB(kbfile.MetricsFile, kbfiletest.EncodeMetrics(&utils.MetricsReport{
		Metadata: &utils.Metadata{ProjectName: "p"},
		TopComplexFunctions: []utils.FunctionMetric{
			{Name: "visible_fn", File: "app/billing.py", Complexity: 3},
			{Name: "payment_secret", File: "secrets.py", Complexity: 9},
		},
	}))
	writeKB(kbfile.ExternalDepsFile, kbfiletest.EncodeExternalDeps([]utils.ExternalDependency{
		{Name: "sharedlib", Source: "external", UsedBy: []string{"app/billing.py", "secrets.py"}, ImportCount: 2},
		{Name: "secretlib", Source: "external", UsedBy: []string{"secrets.py"}, ImportCount: 1},
		{Name: "unlistedlib", Source: "external", UsedBy: []string{}, ImportCount: 0},
	}))

	for _, tc := range []struct {
		query     string
		want      []string
		forbidden []string
	}{
		{"list the entry points", []string{"visible_main"}, []string{"secrets.py", "payment_secret", "hidden_main", ".config"}},
		{"show project metrics", []string{"visible_fn"}, []string{"secrets.py", "payment_secret"}},
		{"complexity of payment_secret", nil, []string{"secrets.py"}},
		{"list all dependencies", []string{"Total External Dependencies: 2", "sharedlib", "unlistedlib"}, []string{"secretlib"}},
		{"dependency sharedlib", []string{"Used by (1 files)", "app/billing.py"}, []string{"secrets.py"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			s := newTestServer(t)
			got, err := s.ExecuteQuery(context.Background(), repo, tc.query, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q, so the leak check proves little:\n%s", w, got)
				}
			}
			for _, f := range tc.forbidden {
				if strings.Contains(got, f) {
					t.Errorf("named %q from an excluded file:\n%s", f, got)
				}
			}
		})
	}
}

// Previously TestPRGap8LexicalSearchDoesNotFollowRootReplacedAfterBind (review
// gap #8), which confirmed this bug: once bound, the root is pinned. If the
// real folder later becomes a symlink to another repository, a search must not
// read that repository while the router and indexes still belong to the first
// one.
func TestLexicalSearchDoesNotFollowRootReplacedAfterBind(t *testing.T) {
	repoA := billing(t, map[string]string{"alpha.py": "ALPHA_ONLY_MARKER\n"})
	repoB := billing(t, map[string]string{"beta.py": "BETA_ONLY_MARKER\n"})
	s := newTestServer(t)
	if _, err := s.SetRepoPath(repoA); err != nil {
		t.Fatal(err)
	}
	active := s.GetActiveRepo()
	moveAside(t, active)
	if err := os.Symlink(repoB, active); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := lexicalSearch(context.Background(), active, "BETA_ONLY_MARKER", nil, DefaultStrategyConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "beta.py") {
		t.Fatalf("search switched to another repository after the bind:\n%s", got)
	}
}

// Previously TestPRGap4QueryDoesNotFollowSymlinkToExcludedFile (review gap #4),
// which confirmed this bug: reading source for an answer must not follow a
// symlink that the index lists under a visible name to a hidden or ignored
// file.
func TestQueryDoesNotFollowSymlinkToExcludedFile(t *testing.T) {
	const query = "how does payment_total work"
	secret := "def payment_total(items):\n    payment_key = '" + secretMarker + "'\n    return 1\n"

	t.Run("control: a regular file is returned", func(t *testing.T) {
		cs := connect(t, newTestServer(t))
		got, _ := call(t, cs, "query", map[string]any{"query": query, "repo_path": billing(t, nil)})
		if !strings.Contains(got, "def payment_total(items):") {
			t.Fatalf("query did not render the source, so the leak checks below prove nothing:\n%s", got)
		}
	})

	for _, tc := range []struct{ name, excluded string }{
		{"hidden file", ".env"},
		{"ignored file", "secrets.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := billing(t, map[string]string{tc.excluded: secret, ".euignore": "secrets.py\n"})
			visible := filepath.Join(repo, "app", "billing.py")
			if err := os.Remove(visible); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join("..", tc.excluded), visible); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			cs := connect(t, newTestServer(t))
			got, _ := call(t, cs, "query", map[string]any{"query": query, "repo_path": repo})
			if strings.Contains(got, secretMarker) {
				t.Fatalf("query followed a symlink to %s:\n%s", tc.excluded, got)
			}
		})
	}
}

// Previously TestPRGap5CallGraphHidesIgnoredFiles (review gap #5), which
// confirmed this bug: a call graph built before a file was ignored must not
// name that file or its functions.
func TestCallGraphHidesIgnoredFiles(t *testing.T) {
	repo := leakyProject(t)
	writeTree(t, repo, map[string]string{".eulix/kb_call_graph.fb": string(kbfiletest.EncodeCallGraphJSON(`{
		"nodes":[
			{"id":"func_payment_secret::secrets.py","node_type":"function","file":"secrets.py"},
			{"id":"func_payment_total::app/billing.py","node_type":"function","file":"app/billing.py"}],
		"edges":[
			{"from":"func_payment_total::app/billing.py","to":"func_payment_secret::secrets.py","edge_type":"call"}]}`))})
	cs := connect(t, newTestServer(t))
	if _, isErr := call(t, cs, "set_repo_path", map[string]any{"repo_path": repo}); isErr {
		t.Fatal("bind failed")
	}

	got, _ := call(t, cs, "query", map[string]any{"query": "call graph of payment_total"})
	if !strings.Contains(got, "app/billing.py") {
		t.Fatalf("visible function missing from the call graph:\n%s", got)
	}
	if strings.Contains(got, "payment_secret") {
		t.Errorf("call graph named a function from an ignored file:\n%s", got)
	}

	got, _ = call(t, cs, "query", map[string]any{"query": "who calls payment_secret"})
	if strings.Contains(got, "secrets.py") {
		t.Errorf("call graph named an ignored file:\n%s", got)
	}
}
