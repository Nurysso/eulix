// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mmap_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"eulix/internal/query/retrieval/mmap"
)

// These call the mapping functions directly. Through DecodeJSONFile and
// OpenForSequentialRead a broken mapping is invisible: both fall back to a
// plain read and still return the right data.

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

func TestMmapForSequentialRead_windows(t *testing.T) {
	path, data := writeLargeJSON(t)

	r, cleanup, err := mmap.MmapForSequentialRead(path, int64(len(data)))
	if err != nil {
		t.Fatalf("MmapForSequentialRead: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("read %d bytes that differ from the %d written", len(got), len(data))
	}
	// A second cleanup must not close a handle value that may be reused by now.
	cleanup()
	cleanup()
}

// Another eulix process may be reading the same file.
func TestMmapOpensAlongsideOtherReaders_windows(t *testing.T) {
	path, data := writeLargeJSON(t)

	other, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	_, cleanup, err := mmap.MmapForSequentialRead(path, int64(len(data)))
	if err != nil {
		t.Fatalf("mapping while another reader is open: %v", err)
	}
	defer cleanup()

	var got map[string]string
	if err := mmap.DecodeViaMmap(path, int64(len(data)), &got); err != nil {
		t.Fatalf("second mapping of the same file: %v", err)
	}
}
