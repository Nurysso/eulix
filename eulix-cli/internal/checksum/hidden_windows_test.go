// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package checksum

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func winWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func winHide(t *testing.T, path string) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
}

func winScan(t *testing.T, root string) map[string]FileEntry {
	t.Helper()
	d := newDetector(root)
	d.exts = map[string]bool{".py": true}
	sum, err := d.scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	return sum.Files
}

// The parser's walker (ignore crate) skips NTFS-hidden entries on Windows, so
// the scan and ServableMatcher must too.
func TestScanSkipsHiddenAttribute(t *testing.T) {
	root := t.TempDir()
	winWrite(t, filepath.Join(root, "shown.py"))
	winWrite(t, filepath.Join(root, "hidden.py"))
	winWrite(t, filepath.Join(root, "hdir", "in.py"))
	winHide(t, filepath.Join(root, "hidden.py"))
	winHide(t, filepath.Join(root, "hdir"))

	files := winScan(t, root)
	if _, ok := files["shown.py"]; !ok || len(files) != 1 {
		t.Errorf("want only shown.py, got %v", files)
	}
}

func TestServableMatcherRefusesHiddenAttribute(t *testing.T) {
	root := t.TempDir()
	winWrite(t, filepath.Join(root, "shown.py"))
	winWrite(t, filepath.Join(root, "secret.py"))
	winWrite(t, filepath.Join(root, "hdir", "in.py"))
	winHide(t, filepath.Join(root, "secret.py"))
	winHide(t, filepath.Join(root, "hdir"))

	servable := ServableMatcher(root)
	for rel, want := range map[string]bool{"shown.py": true, "secret.py": false, "hdir/in.py": false} {
		if got := servable(rel); got != want {
			t.Errorf("servable(%q) = %v, want %v", rel, got, want)
		}
	}
}
