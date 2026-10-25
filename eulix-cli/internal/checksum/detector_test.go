// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package checksum

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDetectsFirstFileOfNewLanguage(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"main.py", "first.go", "README.md", "ignored.rs"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("source"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".euignore"), []byte("ignored.rs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stored := &Checksum{Files: map[string]FileEntry{"main.py": {}}}
	d := newDetector(dir)
	d.exts = extsFromBaseline(stored)
	current, err := d.scan(stored)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.py", "first.go"} {
		if _, ok := current.Files[name]; !ok {
			t.Errorf("missing source file %s", name)
		}
	}
	for _, name := range []string{"README.md", "ignored.rs"} {
		if _, ok := current.Files[name]; ok {
			t.Errorf("unexpected file %s", name)
		}
	}
	added, _, _, ratio := compare(stored, current)
	if added != 1 || ratio == 0 {
		t.Errorf("added=%d ratio=%f, want 1 addition and nonzero change", added, ratio)
	}
}

func TestScanMatchesCurrentParserAllFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "util.ts"), []byte("export const n = 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDetector(dir)
	seed := &Checksum{Files: map[string]FileEntry{"util.ts": {}}}
	d.exts = extsFromBaseline(seed)
	baseline, err := d.scan(seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"App.jsx", "View.tsx", "page.php", "template.phtml", "header.inc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not tracked by parser all"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	current, err := d.scan(baseline)
	if err != nil {
		t.Fatal(err)
	}
	added, deleted, modified, ratio := compare(baseline, current)
	if added != 0 || deleted != 0 || modified != 0 || ratio != 0 {
		t.Fatalf("untracked files trigger repeated analysis: added=%d deleted=%d modified=%d ratio=%f", added, deleted, modified, ratio)
	}
}
