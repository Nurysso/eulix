// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mmap_test

import (
	"os"
	"path/filepath"
	"testing"

	"eulix/internal/query/retrieval/mmap"
)

// These call DecodeViaMmap directly. Through DecodeJSONFile a broken mapping
// is invisible: it falls back to a plain read and still returns the right data.

func writeLargeJSON(t *testing.T) (string, []byte) {
	t.Helper()
	data := makeLargeJSON(mmapThreshold + 1)
	path := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestDecodeViaMmap_windows(t *testing.T) {
	path, data := writeLargeJSON(t)

	var got map[string]string
	if err := mmap.DecodeViaMmap(path, int64(len(data)), &got); err != nil {
		t.Fatalf("DecodeViaMmap: %v", err)
	}
	if got["key"] != "value" {
		t.Fatalf("decoded %v, want key=value", got)
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

	var got map[string]string
	if err := mmap.DecodeViaMmap(path, int64(len(data)), &got); err != nil {
		t.Fatalf("mapping while two other readers are open: %v", err)
	}
}
