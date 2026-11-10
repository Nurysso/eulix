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

func TestServableCallGraphJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".euignore"), []byte("secrets.py\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "graph.json")
	graph := `{
		"nodes":[
			{"id":"func_a::app/a.py","file":"app/a.py","node_type":"function"},
			{"id":"func_payment_secret::secrets.py","file":"secrets.py","node_type":"function"}],
		"edges":[
			{"from":"func_a::app/a.py","to":"func_payment_secret::secrets.py","edge_type":"call"},
			{"from":"func_a::app/a.py","to":"print","edge_type":"call","conditional":true}],
		"tags":{"func_a::app/a.py":["testing"],"func_payment_secret::secrets.py":["database"]},
		"file_categories":{"app/a.py":"Other","secrets.py":"Other"}}`
	if err := os.WriteFile(path, []byte(graph), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := servableCallGraphJSON(path, root)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "secrets.py") || strings.Contains(got, "payment_secret") {
		t.Errorf("viewer data names an ignored file: %s", got)
	}
	// The viewer reads fields the router's own types leave out.
	for _, want := range []string{`"app/a.py"`, `"print"`, `"conditional":true`, `"tags"`, `"file_categories"`, `"node_type"`} {
		if !strings.Contains(got, want) {
			t.Errorf("viewer data lost %s: %s", want, got)
		}
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
	if err := os.Symlink(victim, filepath.Join(f.root, utils.EulixDir, "kb.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := f.run(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("analyze error = %v, want a refusal naming the symlink", err)
	}
	if f.calls != 0 {
		t.Error("the pipeline ran")
	}
}
