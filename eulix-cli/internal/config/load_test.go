// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsBrokenConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadFrom(dir); err != nil {
		t.Fatalf("missing eulix.toml should fall back to defaults, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "eulix.toml"), []byte("[llm\nprovider = 'openai'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(dir); err == nil || !strings.Contains(err.Error(), "eulix.toml") {
		t.Fatalf("LoadFrom: broken eulix.toml should be an error naming the file, got %v", err)
	}
	t.Chdir(dir)
	if _, err := Load(); err == nil {
		t.Fatal("Load: broken eulix.toml should be an error")
	}
}
