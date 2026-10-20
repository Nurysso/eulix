// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eulix/internal/config"
)

func TestHydrateSourceCodeStaysInsideSourceRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "secret.py")
	if err := os.WriteFile(outside, []byte("key = 'SYNTHETIC_SECRET'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.py"), []byte("def ok():\n    return 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cb := &ContextBuilder{sourceRoot: root, config: config.DefaultConfig(), debugLog: newDebugLog(t)}
	for _, p := range []string{"../secret.py", "link.py"} {
		chunk := Chunk{ID: p, File: p, ChunkType: "function", StartLine: 1, EndLine: 1}
		got := cb.hydrateSourceCode([]Chunk{chunk}, 1000, 100)
		if len(got) > 0 && strings.Contains(got[0].Content, "SYNTHETIC_SECRET") {
			t.Errorf("%s returned content from outside the source root", p)
		}
	}

	chunk := Chunk{ID: "func_ok::inside.py", File: "inside.py", ChunkType: "function", StartLine: 1, EndLine: 2}
	got := cb.hydrateSourceCode([]Chunk{chunk}, 1000, 100)
	if len(got) != 1 || !strings.Contains(got[0].Content, "return 1") {
		t.Errorf("file inside the root was not hydrated: %+v", got)
	}
}
