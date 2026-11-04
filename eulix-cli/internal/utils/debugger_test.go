// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDebugLogger_InMemory(t *testing.T) {
	// A regular file where the log directory should go makes MkdirAll fail.
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, d := range []*DebugLogger{NewDebugLogger(), NewDebugLogger(notDir)} {
		d.Log("first %d", 1)
		d.Log("second\n") // an existing newline must not add an empty line
		lines := d.Lines()
		if len(lines) != 2 || !strings.HasSuffix(lines[0], "] first 1") || !strings.HasSuffix(lines[1], "] second") {
			t.Fatalf("unexpected lines: %q", lines)
		}
	}
}

func TestDebugLogger_NilIsNoop(t *testing.T) {
	var d *DebugLogger
	d.Log("ignored")
	LogFileLoad(d, "kb.json")(nil)
	if lines := d.Lines(); lines != nil {
		t.Fatalf("nil logger returned lines: %q", lines)
	}
}

func TestDebugLogger_FileConcurrentThenClose(t *testing.T) {
	dir := t.TempDir()
	d := NewDebugLogger(dir)

	// 1600 lines of ~70 bytes is past Log's own 50KB mid-stream flush.
	const workers, perWorker = 8, 200
	pad := strings.Repeat("x", 32)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWorker {
				d.Log("worker %d line %d %s", w, i, pad)
			}
		}()
	}
	wg.Wait()
	d.Close()
	d.Log("after close") // must be a silent no-op

	data, err := os.ReadFile(filepath.Join(dir, "debug", "context_debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != workers*perWorker {
		t.Fatalf("got %d lines on disk, want %d", len(lines), workers*perWorker)
	}
	for _, l := range lines {
		if !strings.HasSuffix(l, pad) {
			t.Fatalf("torn or stray line on disk: %q", l)
		}
	}
}
