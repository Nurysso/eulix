// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeMCPServersKeepsOtherServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"x"}},"keep":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent
		if err := mergeMCPServers(path, "/bin/eulix", "/repo"); err != nil {
			t.Fatal(err)
		}
	}
	var doc map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if servers["other"] == nil || doc["keep"] == nil {
		t.Errorf("other entries were dropped: %s", data)
	}
	e := servers["eulix"].(map[string]any)
	if e["command"] != "/bin/eulix" || e["args"].([]any)[1] != "/repo" {
		t.Errorf("eulix entry: %v", e)
	}
}

func TestMergeMCPServersRefusesBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	_ = os.WriteFile(path, []byte(`{not json`), 0o644)
	if err := mergeMCPServers(path, "eulix", "/r"); err == nil {
		t.Fatal("broken JSON must be left alone")
	}
	if data, _ := os.ReadFile(path); string(data) != "{not json" {
		t.Errorf("file changed: %s", data)
	}
}

func TestUpsertHintBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	_ = os.WriteFile(path, []byte("# Project\nrules"), 0o644)
	if err := upsertHintBlock(path, "v1"); err != nil {
		t.Fatal(err)
	}
	if err := upsertHintBlock(path, "v2"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.HasPrefix(s, "# Project\nrules\n") || strings.Contains(s, "v1") || strings.Count(s, hintBegin) != 1 || !strings.Contains(s, "v2") {
		t.Errorf("block not replaced in place:\n%s", s)
	}
}
