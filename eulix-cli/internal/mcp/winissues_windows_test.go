// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build winissues

package mcp

// Known Windows-only issues, see docs/windows-issues.md. Each test asserts the
// correct behavior, so it fails until the issue is fixed.
// Run with: go test -tags winissues ./internal/mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// WIN-1: while a repository is bound, the router keeps .eulix/debug/*.log
// open even with debugging off. Windows refuses to rename a folder that holds
// an open file, so the project (or its .eulix) cannot be moved or renamed
// while an editor runs 'eulix mcp', until the idle release closes the logs.
func TestWinIssue_BoundRepoFolderCannotBeMoved(t *testing.T) {
	repo := billing(t, nil)
	s := newTestServer(t)
	if _, err := s.SetRepoPath(repo); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{repo, filepath.Join(repo, ".eulix")} {
		if err := os.Rename(dir, dir+"-moved"); err != nil {
			t.Errorf("cannot move %s while bound: %v", filepath.Base(dir), err)
			continue
		}
		_ = os.Rename(dir+"-moved", dir)
	}
}

// WIN-2: EvalSymlinks does not resolve directory junctions (Go 1.23+), so the
// bound path stays the junction. The bind records os.Stat (the target) but
// each query compares it with os.Lstat (the junction), so every query is
// refused; lexical search also finds nothing, as WalkDir does not enter a
// root that is a junction.
func TestWinIssue_RepoBoundThroughJunctionIsUnusable(t *testing.T) {
	repo := billing(t, nil)
	link := filepath.Join(t.TempDir(), "proj")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, repo).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v %s", err, out)
	}
	s := newTestServer(t)
	if _, err := s.SetRepoPath(link); err != nil {
		t.Fatal(err)
	}

	got, err := s.ExecuteQuery(context.Background(), "", "how does payment_total work", nil, false)
	if err != nil || !strings.Contains(got, "return sum") {
		t.Errorf("query through a junction-bound repo failed: %v", err)
	}

	hits, err := lexicalSearch(context.Background(), s.GetActiveRepo(), "payment_total", nil, DefaultStrategyConfig())
	if err != nil || !strings.Contains(hits, "billing.py") {
		t.Errorf("lexical search under a junction root found nothing: %q, %v", hits, err)
	}
}
