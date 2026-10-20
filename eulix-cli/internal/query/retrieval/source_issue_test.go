package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eulix/internal/config"
)

func TestIssueSourceEscape(t *testing.T) {
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
	cb := &ContextBuilder{sourceRoot: root, config: config.DefaultConfig()}
	for _, p := range []string{"../secret.py", "link.py"} {
		chunk := Chunk{ID: p, File: p, ChunkType: "function", StartLine: 1, EndLine: 1}
		got := cb.hydrateSourceCode([]Chunk{chunk}, 1000, 100)
		if len(got) > 0 && strings.Contains(got[0].Content, "SYNTHETIC_SECRET") {
			t.Errorf("%s returned content from outside the root", p)
		}
	}
}
