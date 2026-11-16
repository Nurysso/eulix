// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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

// RET-9: the router and every context builder it creates call StartAutoFlush
// on the same logger. That must run one goroutine, and Close must stop it.
func TestDebugLogger_AutoFlushStartsOnceAndStopsOnClose(t *testing.T) {
	dir := t.TempDir()
	d := NewDebugLogger(dir)
	before := runtime.NumGoroutine()

	for range 5 {
		d.StartAutoFlush(time.Millisecond)
	}
	if extra := runtime.NumGoroutine() - before; extra > 1 {
		t.Fatalf("%d goroutines started by 5 StartAutoFlush calls, want 1", extra)
	}

	// The one goroutine does flush: the line reaches disk with no Flush call.
	d.Log("auto flushed")
	logPath := filepath.Join(dir, "debug", "context_debug.log")
	waitFor(t, "the auto-flush to write the line", func() bool {
		data, _ := os.ReadFile(logPath)
		return strings.Contains(string(data), "auto flushed")
	})

	d.Close()
	waitFor(t, "Close to stop the auto-flush goroutine", func() bool {
		return runtime.NumGoroutine() <= before
	})

	d.StartAutoFlush(time.Millisecond) // after Close: must not start one
	if extra := runtime.NumGoroutine() - before; extra > 0 {
		t.Fatalf("StartAutoFlush after Close started %d goroutines", extra)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// Run with -race: Log reads closed, which Close writes.
func TestDebugLogger_LogRacesClose(t *testing.T) {
	d := NewDebugLogger(t.TempDir())
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				d.Log("line %d", i)
			}
		}()
	}
	d.Close()
	wg.Wait()
}
