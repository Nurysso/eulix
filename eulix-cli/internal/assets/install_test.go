// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallBinaryReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "parser")
	for _, content := range []string{"first", "second"} {
		if err := installBinary(dest, []byte(content)); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dest); string(got) != content {
			t.Fatalf("dest = %q, want %q", got, content)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestInstallBinaryReplacesRunningExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows locks running executables")
	}
	source, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "eulix_parser.exe")
	if err := os.WriteFile(dest, source, 0o755); err != nil {
		t.Fatal(err)
	}
	running := exec.Command(dest, "-n", "30", "127.0.0.1")
	if err := running.Start(); err != nil {
		t.Skip(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = running.Process.Kill()
			_ = running.Wait()
		}
	}
	defer stop()

	if err := os.WriteFile(dest, []byte("new"), 0o755); err == nil {
		t.Fatal("expected Windows to refuse a plain overwrite; test proves nothing")
	}
	if err := installBinary(dest, []byte("new")); err != nil {
		t.Fatalf("installBinary over a running exe: %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "new" {
		t.Fatalf("dest = %q, want new", got)
	}

	// The next install clears the moved-aside copy once it has stopped.
	stop()
	if err := installBinary(dest, []byte("newer")); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("old parser not cleaned up: %v", entries)
	}
}

func TestInstallBinaryCleanupIgnoresGlobCharsInDir(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "[ab]", "bin")
	other := filepath.Join(root, "a", "bin")
	for _, d := range []string{real, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	leftover := filepath.Join(real, "eulix_parser.old-1")
	outside := filepath.Join(other, "eulix_parser.old-1")
	for _, f := range []string{leftover, outside} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := installBinary(filepath.Join(real, "eulix_parser"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("file outside the install dir was deleted: %v", err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("leftover in the install dir was not removed: %v", err)
	}
}

func TestCommonTopLevelDirHandlesBackslashEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{`eulix-embed\main.py`, `eulix-embed\sub\a.py`} {
		if _, err := zw.Create(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if got := commonTopLevelDir(zr.File); got != "eulix-embed" {
		t.Fatalf("got %q, want eulix-embed", got)
	}
}
