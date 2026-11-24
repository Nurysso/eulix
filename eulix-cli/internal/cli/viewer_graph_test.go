// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eulix/internal/utils"
)

func TestViewerCallGraphHonoursEuignore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".euignore"), []byte("server.py\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Written by eulix_parser; nodes in app/server.py and app/tool.go.
	fixture := filepath.Join("..", "kbfile", "testdata", "kb_call_graph.fb")

	data, err := viewerCallGraph(fixture, root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "server.py") {
		t.Error("viewer data names an ignored file")
	}
	if !strings.Contains(string(data), "func_helper::app/tool.go") {
		t.Error("viewer data lost a node from a kept file")
	}

	// A graph named with --graph is shown whole.
	all, err := viewerCallGraph(fixture, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(all), "server.py") {
		t.Error("--graph data was filtered")
	}
}

// The parser opens its outputs with File::create, which follows links.
func TestAnalyzeRefusesSymlinksInEulixDir(t *testing.T) {
	f := newAnalysisFixture(t)
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(utils.EulixDir, "placeholder"), "")
	if err := os.Symlink(victim, filepath.Join(f.root, utils.EulixDir, "kb.fb")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := f.run(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("analyze error = %v, want a refusal naming the symlink", err)
	}
	if f.calls != 0 {
		t.Error("the pipeline ran")
	}
}
