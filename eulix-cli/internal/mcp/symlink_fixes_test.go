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
		"kb_entry_points.json",
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
	writeTree(t, repo, map[string]string{
		".eulix/kb_entry_points.json": `{"entry_points":[
			{"entry_type":"main","handler":"visible_main","file":"app/billing.py","line":1},
			{"entry_type":"main","handler":"payment_secret","file":"secrets.py","line":1},
			{"entry_type":"main","handler":"hidden_main","file":".config/payment.py","line":1}]}`,
		".eulix/kb_metrics.json": `{"metadata":{"project_name":"p"},"top_complex_functions":[
			{"name":"visible_fn","file":"app/billing.py","complexity":3},
			{"name":"payment_secret","file":"secrets.py","complexity":9}]}`,
		".eulix/kb_external_deps.json": `{"external_dependencies":[
			{"name":"sharedlib","source":"external","used_by":["app/billing.py","secrets.py"],"import_count":2},
			{"name":"secretlib","source":"external","used_by":["secrets.py"],"import_count":1},
			{"name":"unlistedlib","source":"external","used_by":[],"import_count":0}]}`,
	})

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
