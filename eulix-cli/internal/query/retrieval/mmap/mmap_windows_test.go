// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mmap_test

import (
	"os"
	"path/filepath"
	"testing"

	"eulix/internal/query/retrieval/mmap"
)

// These call mapFile (exported to tests as MapFile) directly. Through WithFileBytes a broken mapping is
// invisible: it falls back to a plain read and still returns the right data.

func writeLargeJSON(t *testing.T) (string, []byte) {
	t.Helper()
	data := makeLargeJSON(mmapThreshold + 1)
	path := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestMapFile_windows(t *testing.T) {
	path, data := writeLargeJSON(t)

	got, release, err := mmap.MapFile(path)
	if err != nil {
		t.Fatalf("MapFile: %v", err)
	}
	defer release()
	if string(got) != string(data) {
		t.Fatal("mapped contents differ from the file")
	}
}

// Another eulix process may be reading the same file, plainly or streaming.
func TestMmapOpensAlongsideOtherReaders_windows(t *testing.T) {
	path, data := writeLargeJSON(t)

	other, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	_, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		t.Fatalf("streaming reader while another reader is open: %v", err)
	}
	defer cleanup()

	got, release, err := mmap.MapFile(path)
	if err != nil {
		t.Fatalf("mapping while two other readers are open: %v", err)
	}
	defer release()
	if len(got) != len(data) {
		t.Fatalf("mapped %d bytes, want %d", len(got), len(data))
	}
}
