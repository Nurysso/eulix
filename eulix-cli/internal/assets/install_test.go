// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package assets

import (
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
