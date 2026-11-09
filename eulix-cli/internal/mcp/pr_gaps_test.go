// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

// Regression tests for the gaps found while reviewing the symlink and stale
// index fixes. Numbers match the review. Run them with: go test -run TestPRGap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// #1: the walk learns a file's type when it lists the folder and reads it
// later, so a file swapped for a symlink in between must not be followed.
func TestPRGap1LexicalWalkDoesNotFollowSwappedSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.go")
	writeTree(t, filepath.Dir(outside), map[string]string{"secret.go": "// marker " + secretMarker + "\n"})
	for i := range 300 {
		writeTree(t, root, map[string]string{fmt.Sprintf("a_%03d.go", i): "package filler\n"})
	}
	target := filepath.Join(root, "z_target.go")
	writeTree(t, root, map[string]string{"z_target.go": "// marker SAFE\n"})
	if err := os.Symlink(outside, target+".probe"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_ = os.Remove(target + ".probe")

	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := target + ".tmp"
		for !stop.Load() {
			_ = os.Symlink(outside, tmp)
			_ = os.Rename(tmp, target)
			_ = os.WriteFile(tmp, []byte("// marker SAFE\n"), 0o600)
			_ = os.Rename(tmp, target)
		}
	}()
	defer func() { stop.Store(true); <-done }()

	for i, deadline := 1, time.Now().Add(2*time.Second); time.Now().Before(deadline); i++ {
		got, err := lexicalSearch(context.Background(), root, "marker", nil, DefaultStrategyConfig())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, secretMarker) {
			t.Fatalf("search %d returned a file from outside the repository:\n%s", i, got)
		}
	}
}

// #2: only the top files are shown, so holding every match in memory is a
// waste that can take the server down on a large repository.
func TestPRGap2LexicalSearchMemoryStaysBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("writes about 150 MiB")
	}
	root := t.TempDir()
	body := []byte(strings.Repeat("marker line of ordinary source code here\n", 12_000))
	const files = 300
	for i := range files {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d.go", i)), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	corpus := uint64(len(body) * files)

	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	baseline := m.HeapAlloc

	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		var s runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
			runtime.ReadMemStats(&s)
			if s.HeapAlloc > peak.Load() {
				peak.Store(s.HeapAlloc)
			}
		}
	}()
	_, err := lexicalSearch(context.Background(), root, "marker", nil, DefaultStrategyConfig())
	close(stop)
	<-sampled
	if err != nil {
		t.Fatal(err)
	}

	if grown := peak.Load() - min(baseline, peak.Load()); grown > corpus/3 {
		t.Fatalf("heap grew %d MiB while searching a %d MiB repository; matches should not stay in memory",
			grown>>20, corpus>>20)
	}
}

// #4: reading source for an answer must not follow a symlink that the index
// lists under a visible name to a hidden or ignored file.
func TestPRGap4QueryDoesNotFollowSymlinkToExcludedFile(t *testing.T) {
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

// #5: a call graph built before a file was ignored must not name that file or
// its functions.
func TestPRGap5CallGraphHidesIgnoredFiles(t *testing.T) {
	repo := leakyProject(t)
	writeTree(t, repo, map[string]string{".eulix/kb_call_graph.json": `{
		"nodes":[
			{"id":"func_payment_secret::secrets.py","node_type":"function","file":"secrets.py"},
			{"id":"func_payment_total::app/billing.py","node_type":"function","file":"app/billing.py"}],
		"edges":[
			{"from":"func_payment_total::app/billing.py","to":"func_payment_secret::secrets.py","edge_type":"call"}]}`})
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

// #8: once bound, the root is pinned. If the real folder later becomes a
// symlink to another repository, a search must not read that repository while
// the router and indexes still belong to the first one.
func TestPRGap8LexicalSearchDoesNotFollowRootReplacedAfterBind(t *testing.T) {
	repoA := billing(t, map[string]string{"alpha.py": "ALPHA_ONLY_MARKER\n"})
	repoB := billing(t, map[string]string{"beta.py": "BETA_ONLY_MARKER\n"})
	s := newTestServer(t)
	if _, err := s.SetRepoPath(repoA); err != nil {
		t.Fatal(err)
	}
	active := s.GetActiveRepo()
	if err := os.Rename(active, active+"-moved"); err != nil {
		t.Fatal(err)
	}
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

// #9: the active repository is now fully resolved, so fixtures must be too.
// macOS temp folders sit behind a symlink; pointing TMPDIR at one here shows
// the same failure on any Unix system.
func TestPRGap9FixturesWorkBehindSymlinkedTempDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use TMPDIR")
	}
	base := t.TempDir()
	realTmp := filepath.Join(base, "real")
	if err := os.Mkdir(realTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realTmp, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("TMPDIR", link)

	t.Run("fixture", func(t *testing.T) {
		repo := billing(t, nil)
		s := newTestServer(t)
		if _, err := s.SetRepoPath(repo); err != nil {
			t.Fatal(err)
		}
		if got := s.GetActiveRepo(); got != repo {
			t.Fatalf("active repository = %q, fixture path = %q; tests comparing them fail on macOS", got, repo)
		}
	})
}

// #10: the percent-encoded extended-length prefix can also arrive in the host
// position, where url.Parse rejects it.
func TestPRGap10URIPathEncodedExtendedLengthHost(t *testing.T) {
	const uri = "file://%3F/C%3A/repo"
	want := filepath.FromSlash("//?/C:/repo")
	if got := uriPath(uri, true); got != want {
		t.Fatalf("uriPath(%q, windows=true) = %q, want %q", uri, got, want)
	}
}
