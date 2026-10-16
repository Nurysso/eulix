// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLexicalSearchSkipsHiddenAndIgnoredFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".env":           "PAYMENT_TOKEN=SYNTHETIC_SECRET\n",
		".euignore":      "secret.py\n",
		"secret.py":      "payment_key = 'SYNTHETIC_SECRET'\n",
		"billing/pay.py": "def payment_total():\n    return 42\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := lexicalSearch(context.Background(), dir, "payment", nil, DefaultStrategyConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "SYNTHETIC_SECRET") {
		t.Fatalf("hidden or .euignore'd file content returned:\n%s", got)
	}
	if !strings.Contains(got, "payment_total") {
		t.Fatalf("regular source file missing from results:\n%s", got)
	}
}
