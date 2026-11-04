// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package checksum

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eulix/internal/utils"
)

func writeFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// baseline writes the checksum the Rust parser would leave for root's
// current state, so Run has something to compare against.
func baseline(t *testing.T, root string, files ...string) *Checksum {
	t.Helper()
	seed := &Checksum{Files: map[string]FileEntry{}}
	for _, f := range files {
		seed.Files[f] = FileEntry{}
	}
	d := newDetector(root)
	d.exts = extsFromBaseline(seed)
	c, err := d.scan(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Save(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := &Checksum{
		ProjectPath: root,
		TotalFiles:  1,
		Files:       map[string]FileEntry{"a/b.go": {Hash: MetaHash(3, 9), Size: 3, ModTime: 9}},
	}
	d := newDetector(root)
	if err := d.Save(want); err != nil {
		t.Fatal(err)
	}
	d.checksumDir = filepath.Join(root, utils.EulixDir)
	got, err := d.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectPath != want.ProjectPath || got.TotalFiles != 1 || got.Files["a/b.go"] != want.Files["a/b.go"] {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestLoadErrors(t *testing.T) {
	root := t.TempDir()
	d := &Detector{projectPath: root, checksumDir: root}
	if _, err := d.Load(); err == nil {
		t.Fatal("missing checksum file: want error")
	}

	path := filepath.Join(root, utils.ChecksumFileName)
	if err := os.WriteFile(path, []byte("not zstd"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Load(); err == nil || !strings.Contains(err.Error(), "decompress") {
		t.Fatalf("corrupt zstd: got %v, want decompress error", err)
	}

	bad, err := compressZstd([]byte("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Load(); err == nil || !strings.Contains(err.Error(), "invalid json") {
		t.Fatalf("bad json: got %v, want invalid json error", err)
	}
}

func TestZstdRoundTrip(t *testing.T) {
	in := bytes.Repeat([]byte("eulix "), 1000)
	c, err := compressZstd(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decompressZstd(c)
	if err != nil || !bytes.Equal(in, out) {
		t.Fatalf("round trip failed: err=%v equal=%v", err, bytes.Equal(in, out))
	}
}

func TestRun(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root) // config.Load defaults Project.Path to the working directory
	keep := writeFile(t, root, "keep.go", "package a")
	gone := writeFile(t, root, "gone.go", "package a")
	writeFile(t, root, "edit.go", "package a")

	t.Run("no baseline is a first run", func(t *testing.T) {
		res, err := Run()
		if err != nil {
			t.Fatal(err)
		}
		if !res.FirstRun || res.ChangedRatio != 1 || res.Checksum != nil {
			t.Fatalf("got %+v, want FirstRun with ratio 1", res)
		}
	})

	baseline(t, root, "keep.go", "gone.go", "edit.go")

	t.Run("unchanged tree", func(t *testing.T) {
		res, err := Run()
		if err != nil {
			t.Fatal(err)
		}
		if res.FirstRun || res.ChangedRatio != 0 || res.Checksum.TotalFiles != 3 {
			t.Fatalf("got %+v, want no change over 3 files", res)
		}
	})

	t.Run("added, deleted and modified", func(t *testing.T) {
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		writeFile(t, root, "new.go", "package a")
		later := time.Now().Add(time.Hour)
		if err := os.Chtimes(filepath.Join(root, "edit.go"), later, later); err != nil {
			t.Fatal(err)
		}
		res, err := Run()
		if err != nil {
			t.Fatal(err)
		}
		if res.FilesAdded != 1 || res.FilesDeleted != 1 || res.FilesModified != 1 {
			t.Fatalf("got +%d -%d ~%d, want 1 each", res.FilesAdded, res.FilesDeleted, res.FilesModified)
		}
		if res.ChangedRatio != 1 {
			t.Fatalf("ratio = %v, want 3 changes / 3 files = 1", res.ChangedRatio)
		}
		if _, ok := res.Checksum.Files["keep.go"]; !ok {
			t.Fatalf("keep.go missing from current checksum (%s)", keep)
		}
	})

	t.Run("corrupt baseline falls back to a first run", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(root, utils.EulixDir, utils.ChecksumFileName), []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := Run()
		if err != nil || !res.FirstRun {
			t.Fatalf("got %+v, %v; want FirstRun", res, err)
		}
	})
}

func TestCompare(t *testing.T) {
	e := func(h string) FileEntry { return FileEntry{Hash: h} }
	cases := []struct {
		name            string
		stored, current map[string]FileEntry
		add, del, mod   int
		ratio           float64
	}{
		{"both empty", nil, nil, 0, 0, 0, 0},
		{"everything deleted", map[string]FileEntry{"a": e("1"), "b": e("2")}, nil, 0, 2, 0, 1},
		{"everything new", nil, map[string]FileEntry{"a": e("1")}, 1, 0, 0, 1},
		{"one of four modified", map[string]FileEntry{"a": e("1"), "b": e("2"), "c": e("3"), "d": e("4")},
			map[string]FileEntry{"a": e("x"), "b": e("2"), "c": e("3"), "d": e("4")}, 0, 0, 1, 0.25},
		{"ratio is capped at 1", map[string]FileEntry{"a": e("1")}, map[string]FileEntry{"b": e("1"), "c": e("1")}, 2, 1, 0, 1},
	}
	for _, c := range cases {
		add, del, mod, ratio := compare(&Checksum{Files: c.stored}, &Checksum{Files: c.current})
		if add != c.add || del != c.del || mod != c.mod || ratio != c.ratio {
			t.Errorf("%s: got +%d -%d ~%d %.2f, want +%d -%d ~%d %.2f", c.name, add, del, mod, ratio, c.add, c.del, c.mod, c.ratio)
		}
	}
}

func TestScanSkipsWhatTheRustWalkerSkips(t *testing.T) {
	root := t.TempDir()
	p := writeFile(t, root, "src/a.go", "package a")
	for _, rel := range []string{".hidden.go", ".cache/x.go", "node_modules/m.js", "target/t.rs", "src/notes.txt"} {
		writeFile(t, root, rel, "x")
	}
	if err := os.Symlink(p, filepath.Join(root, "link.go")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "srclink")); err != nil {
		t.Fatal(err)
	}

	// Scanning through a symlinked root must still give root-relative keys.
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	d := newDetector(alias)
	d.exts = extsFromBaseline(&Checksum{})
	c, err := d.scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 1 {
		t.Fatalf("files = %v, want only src/a.go", c.Files)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Files["src/a.go"]; got.Hash != MetaHash(info.Size(), info.ModTime().UnixNano()) {
		t.Fatalf("src/a.go entry %+v does not hash its size and mtime", got)
	}
}

func TestExtsFromBaseline(t *testing.T) {
	exts := extsFromBaseline(&Checksum{Files: map[string]FileEntry{
		"web/App.vue": {}, ".gitignore": {}, "Makefile": {}, "sub/.env": {},
	}})
	if !exts[".vue"] || !exts[".go"] {
		t.Errorf("want baseline .vue and built-in .go tracked")
	}
	for _, bad := range []string{".gitignore", ".env", ""} {
		if exts[bad] {
			t.Errorf("dotfile name %q treated as an extension", bad)
		}
	}
}

func TestUnreadableIgnoreFileIsSkipped(t *testing.T) {
	root := t.TempDir()
	// A directory named .euignore cannot be read as a file.
	if err := os.Mkdir(filepath.Join(root, utils.EuignorePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if IgnoreMatcher(root)(filepath.Join(root, "a.go")) {
		t.Fatal("unreadable .euignore must not ignore anything")
	}
}

// nestedPatterns caches behind a mutex, so one matcher may be shared; run with -race.
func TestIgnoreMatcherConcurrent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/.euignore", "secret.py\n")
	ignored := IgnoreMatcher(root)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !ignored(filepath.Join(root, "a", "b", "secret.py")) {
				t.Error("nested rule not applied")
			}
		}()
	}
	wg.Wait()
}
