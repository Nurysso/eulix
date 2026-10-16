// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package utils provides Shared type and func across project

package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type DebugLogger struct {
	file   *os.File
	writer *bufio.Writer
	buf    *bytes.Buffer // currently used for test
	mu     sync.Mutex
	closed bool
}

func IsDebugLevel3() bool {
	return os.Getenv("debug") == "3"
}

// NewDebugLogger creates a thread-safe debug logger writing to eulixDir/debug/context_debug.log.
// If file creation fails, returns a silent (no-op) logger rather than panicking.
func NewDebugLogger(eulixDir ...string) *DebugLogger {
	if len(eulixDir) == 0 || eulixDir[0] == "" {
		return &DebugLogger{buf: &bytes.Buffer{}}
	}

	logPath := filepath.Join(eulixDir[0], "debug", "context_debug.log")

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return &DebugLogger{buf: &bytes.Buffer{}} // silent fallback (in-memory)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return &DebugLogger{buf: &bytes.Buffer{}} // silent fallback (in-memory)
	}

	return &DebugLogger{
		file:   f,
		writer: bufio.NewWriterSize(f, 64*1024),
	}
}

// Write makes DebugLogger usable as an io.Writer.
func (d *DebugLogger) Write(p []byte) (int, error) {
	if d == nil {
		return len(p), nil
	}
	if d.writer != nil {
		return d.writer.Write(p)
	}
	if d.buf != nil {
		return d.buf.Write(p)
	}
	return len(p), nil
}

// Lines returns the log lines emitted so far.
// It flushes any buffered data first.
func (d *DebugLogger) Lines() []string {
	if d == nil {
		return nil
	}

	if d.buf != nil {
		s := d.buf.String()
		if s == "" {
			return nil
		}
		return strings.Split(strings.TrimRight(s, "\n"), "\n")
	}

	if d.writer != nil {
		_ = d.writer.Flush()
	}
	if d.file != nil {
		data, err := os.ReadFile(d.file.Name())
		if err != nil {
			return nil
		}
		s := strings.TrimRight(string(data), "\n")
		if s == "" {
			return nil
		}
		return strings.Split(s, "\n")
	}
	return nil
}

// Log writes a timestamped debug message to the log file.
// Thread-safe via mutex. Silent fallback if file is nil.
// Automatically appends newline if not present.
//
// Format: [HH:MM:SS] <formatted message>
func (d *DebugLogger) Log(format string, args ...interface{}) {
	if d == nil || d.closed {
		return
	}
	if d.writer == nil && d.buf == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	msg := fmt.Sprintf(
		"[%s.%03d.%03d.%03d] ",
		now.Format("15:04:05"),
		now.Nanosecond()/1_000_000,     // Milliseconds (0-999)
		(now.Nanosecond()/1_000)%1_000, // Microseconds remainder (0-999)
		now.Nanosecond()%1_000,         // Nanoseconds remainder (0-999)
	)

	msg += fmt.Sprintf(format, args...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}

	// In-memory mode (tests / silent fallback).
	if d.writer == nil {
		_, _ = d.buf.WriteString(msg)
		return
	}

	_, _ = d.writer.WriteString(msg)
	if d.writer.Buffered() > 50*1024 {
		_ = d.writer.Flush()
	}
}

// Flush forces all buffered logs to disk.
func (d *DebugLogger) Flush() {
	if d.file == nil || d.closed {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.writer != nil {
		_ = d.writer.Flush()
	}
}

// Close flushes and closes the logger.
func (d *DebugLogger) Close() {
	if d.file == nil || d.closed {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.writer != nil {
		_ = d.writer.Flush()
	}
	_ = d.file.Close()
	d.closed = true
}

// StartAutoFlush starts a goroutine that periodically flushes logs to disk.
func (d *DebugLogger) StartAutoFlush(interval time.Duration) {
	if d.file == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			d.Flush()
		}
	}()
}

// LogFileLoad logs the start of a file/resource load and returns a
// closure to call when the load finishes (with the resulting error, if any).
// Shared across retrieval, loaders, classifier, strip — anything holding
// a *DebugLogger can use it without depending on ContextBuilder.
func LogFileLoad(dbg *DebugLogger, name string) func(error) {
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	t0 := time.Now()

	dbg.Log("[LOAD] %-30s opening  (heap: %d MB)",
		name, memBefore.HeapInuse/1024/1024)

	return func(err error) {
		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)
		elapsed := time.Since(t0)
		heapDelta := int64(memAfter.HeapInuse) - int64(memBefore.HeapInuse)

		if err != nil {
			dbg.Log("[LOAD] %-30s FAILED   (%v) elapsed=%v",
				name, err, elapsed)
			return
		}
		dbg.Log(
			"[LOAD] %-30s done     elapsed=%-10v heap_delta=+%d MB total_heap=%d MB",
			name, elapsed,
			heapDelta/1024/1024,
			memAfter.HeapInuse/1024/1024,
		)
	}
}
