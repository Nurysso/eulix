//go:build windows

package mcp

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func setHidden(t *testing.T, path string) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
}

func TestLexicalSearchSkipsNTFSHiddenFileAndDir(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"credentials.txt":     "payment " + secretMarker + "\n", // no dot prefix
		"private_dir/pay.txt": "payment " + secretMarker + "\n",
		"billing/pay.py":      "def payment_total():\n    return 42\n",
	})
	setHidden(t, filepath.Join(dir, "credentials.txt"))
	setHidden(t, filepath.Join(dir, "private_dir"))

	got := runLexical(t, dir, "payment")
	if strings.Contains(got, secretMarker) {
		t.Fatalf("NTFS-hidden content returned:\n%s", got)
	}
	if !strings.Contains(got, "payment_total") {
		t.Fatalf("regular file missing:\n%s", got)
	}
}
