// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func mustSymlink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestCheckEulixDir(t *testing.T) {
	newDir := func(t *testing.T) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), ".eulix")
		if err := os.MkdirAll(filepath.Join(dir, "debug"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "kb.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	if err := CheckEulixDir(newDir(t)); err != nil {
		t.Errorf("regular files refused: %v", err)
	}
	if err := CheckEulixDir(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("a missing folder is not this check's error: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "secret.json")
	t.Run("file link", func(t *testing.T) {
		dir := newDir(t)
		mustSymlink(t, outside, filepath.Join(dir, "kb_metrics.json"))
		if CheckEulixDir(dir) == nil {
			t.Error("link accepted")
		}
	})
	t.Run("nested link", func(t *testing.T) {
		dir := newDir(t)
		mustSymlink(t, outside, filepath.Join(dir, "debug", "context_debug.log"))
		if CheckEulixDir(dir) == nil {
			t.Error("link accepted")
		}
	})
	t.Run("link inside the folder", func(t *testing.T) {
		dir := newDir(t)
		mustSymlink(t, "kb.json", filepath.Join(dir, "kb_index.json"))
		if CheckEulixDir(dir) == nil {
			t.Error("link accepted")
		}
	})
	t.Run("the folder itself", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), ".eulix")
		mustSymlink(t, newDir(t), link)
		if CheckEulixDir(link) == nil {
			t.Error("link accepted")
		}
	})
}

func TestOpenEulixFile(t *testing.T) {
	const keep = "IMPORTANT USER DATA\n"
	const flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	rel := filepath.Join("debug", "x.log")

	newVictim := func(t *testing.T) string {
		t.Helper()
		victim := filepath.Join(t.TempDir(), "victim.txt")
		if err := os.WriteFile(victim, []byte(keep), 0o600); err != nil {
			t.Fatal(err)
		}
		return victim
	}
	mustBeIntact := func(t *testing.T, victim string) {
		t.Helper()
		if data, err := os.ReadFile(victim); err != nil || string(data) != keep {
			t.Errorf("file outside the repository changed: %q, %v", data, err)
		}
	}

	t.Run("control: creates the folders and the file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".eulix")
		f, err := OpenEulixFile(dir, rel, flag)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Error(err)
		}
	})
	t.Run("control: relative path", func(t *testing.T) {
		t.Chdir(t.TempDir())
		f, err := OpenEulixFile(EulixDir, rel, flag)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	})
	t.Run("empty path", func(t *testing.T) {
		if _, err := OpenEulixFile("", rel, flag); err == nil {
			t.Error("wrote relative to the working directory")
		}
	})
	t.Run("file link", func(t *testing.T) {
		victim := newVictim(t)
		dir := filepath.Join(t.TempDir(), ".eulix")
		mustSymlink(t, victim, filepath.Join(dir, rel))
		if f, err := OpenEulixFile(dir, rel, flag); err == nil {
			_ = f.Close()
			t.Error("opened through the link")
		}
		mustBeIntact(t, victim)
	})
	t.Run("dangling link does not create its target", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "new.txt")
		dir := filepath.Join(t.TempDir(), ".eulix")
		mustSymlink(t, target, filepath.Join(dir, rel))
		if f, err := OpenEulixFile(dir, rel, flag); err == nil {
			_ = f.Close()
			t.Error("opened through the link")
		}
		if _, err := os.Lstat(target); err == nil {
			t.Error("created a file outside the repository")
		}
	})
	t.Run("folder link", func(t *testing.T) {
		outside := t.TempDir()
		dir := filepath.Join(t.TempDir(), ".eulix")
		mustSymlink(t, outside, filepath.Join(dir, "debug"))
		if f, err := OpenEulixFile(dir, rel, flag); err == nil {
			_ = f.Close()
			t.Error("opened through the link")
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("wrote outside the repository: %v", entries)
		}
	})
	t.Run("the folder itself", func(t *testing.T) {
		outside := t.TempDir()
		dir := filepath.Join(t.TempDir(), ".eulix")
		mustSymlink(t, outside, dir)
		if f, err := OpenEulixFile(dir, rel, flag); err == nil {
			_ = f.Close()
			t.Error("opened through the link")
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("wrote outside the repository: %v", entries)
		}
	})
}
