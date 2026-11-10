// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Retrieval builds its own copy of the call graph, so it needs the same filter
// as the router. Calls to external functions have no node and must survive.
func TestLoadCallGraphDropsIgnoredFiles(t *testing.T) {
	root := t.TempDir()
	graph := `{
		"nodes":[
			{"id":"func_secret::secrets.py","node_type":"function","file":"secrets.py"},
			{"id":"func_total::app/billing.py","node_type":"function","file":"app/billing.py"}],
		"edges":[
			{"from":"func_total::app/billing.py","to":"func_secret::secrets.py","edge_type":"call"},
			{"from":"func_total::app/billing.py","to":"print","edge_type":"call"}]}`
	for name, content := range map[string]string{".euignore": "secrets.py\n", "kb_call_graph.json": graph} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cb := &ContextBuilder{eulixDir: root, sourceRoot: root, debugLog: newDebugLog(t)}
	cb.loadAndIndexCallGraph()

	if n := len(cb.cgRef.Nodes); n != 1 || cb.cgRef.Nodes[0].File != "app/billing.py" {
		t.Errorf("nodes kept = %+v", cb.cgRef.Nodes)
	}
	if n := len(cb.cgRef.Edges); n != 1 || cb.cgRef.Edges[0].To != "print" {
		t.Errorf("edges kept = %+v", cb.cgRef.Edges)
	}
	for sym, rels := range cb.callGraph {
		for _, r := range rels {
			if strings.Contains(sym+r.Target, "secret") {
				t.Errorf("retrieval graph names an ignored file: %s -> %s", sym, r.Target)
			}
		}
	}
}
