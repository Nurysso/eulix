package mmap_test

// Tests for the file access helpers the knowledge-base readers use.
//
// Strategy:
//   - SizeOverflows / ErrFileTooLargeForPath: pure unit tests, no I/O.
//   - WithFileBytes: real temp files on both sides of the 4 MiB mapping
//     threshold, so the mapped and the read path are both exercised.
//   - OpenForSequentialRead: always buffered, never mapped; tested at both
//     sizes, plus a rewrite of the file while it is being read.
//   - All tests are parallel-safe: each test creates its own temp directory.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"eulix/internal/query/retrieval/mmap"
)

// ========== Helpers ==========

// mustTempDir creates a test-scoped temporary directory and registers cleanup.
func mustTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mmap_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// writeTempFile writes raw bytes to a new file in dir and returns its path.
func writeTempFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
	return p
}

// largePadding returns a byte slice of exactly n bytes filled with spaces.
// Prepending / appending it to JSON keeps the payload valid (JSON ignores
// whitespace at the top level).
func largePadding(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return b
}

// makeLargeJSON creates valid JSON bytes whose total size is ≥ targetBytes by
// padding with leading whitespace. The payload is a JSON object {key: "val"}.
func makeLargeJSON(targetBytes int) []byte {
	inner := []byte(`{"key":"value"}`)
	if len(inner) >= targetBytes {
		return inner
	}
	pad := largePadding(targetBytes - len(inner))
	return append(pad, inner...)
}

// mmapThreshold mirrors the unexported constant so tests can stay DRY.
const mmapThreshold = 4 << 20 // 4 MiB

func TestSizeOverflows(t *testing.T) {
	t.Parallel()

	const maxInt64 = int64(math.MaxInt64)

	tests := []struct {
		name string
		size int64
		want bool
	}{
		{"zero", 0, false},
		{"one", 1, false},
		{"maxInt32", math.MaxInt32, false},
		// On 64-bit platforms maxInt64 == maxInt, so this is false.
		// On 32-bit platforms any value > MaxInt32 overflows.
		{"maxInt64_on_64bit", maxInt64, runtime.GOARCH == "386" || runtime.GOARCH == "arm"},
		// A very large but representable value on 64-bit.
		{"4GiB", 4 << 30, runtime.GOARCH == "386" || runtime.GOARCH == "arm"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mmap.SizeOverflows(tc.size)
			if got != tc.want {
				t.Errorf("SizeOverflows(%d) = %v; want %v", tc.size, got, tc.want)
			}
		})
	}
}

// On 64-bit hosts the function must return false for any realistic file size.
func TestSizeOverflows_64bit_commonSizes(t *testing.T) {
	t.Parallel()
	if runtime.GOARCH == "386" || runtime.GOARCH == "arm" {
		t.Skip("32-bit platform — overflow semantics differ")
	}
	for _, size := range []int64{0, 1, mmapThreshold, 1 << 30, 4 << 30, 16 << 30} {
		if mmap.SizeOverflows(size) {
			t.Errorf("SizeOverflows(%d) = true on 64-bit host; want false", size)
		}
	}
}

func TestErrFileTooLargeForPath(t *testing.T) {
	t.Parallel()

	path := "/data/kb.json"
	size := int64(5 << 30)
	err := mmap.ErrFileTooLargeForPath(path, size)

	if err == nil {
		t.Fatal("expected non-nil error")
	}

	// Must wrap the sentinel via errors.Is
	var sentinel interface{ Unwrap() error }
	_ = sentinel // just ensure the wrapping convention is tested via errors.Is
	// We can't import the unexported errFileTooLarge directly; test the message.

	msg := err.Error()
	if !strings.Contains(msg, path) {
		t.Errorf("error %q does not contain path %q", msg, path)
	}
	if !strings.Contains(msg, fmt.Sprint(size)) {
		t.Errorf("error %q does not contain size %d", msg, size)
	}
}

func TestErrFileTooLargeForPath_wrapsOverflowSentinel(t *testing.T) {
	t.Parallel()

	err := mmap.ErrFileTooLargeForPath("/tmp/x", 99)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	// The whole point: callers must be able to match the sentinel via errors.Is.
	if !errors.Is(err, mmap.ErrFileTooLarge) {
		t.Fatalf("errors.Is(err, ErrFileTooLarge) = false; want true (err = %v)", err)
	}

	// Message should carry the path and size so it's useful in logs.
	msg := err.Error()
	if !strings.Contains(msg, "/tmp/x") {
		t.Errorf("error message missing path: %q", msg)
	}
	if !strings.Contains(msg, "99") {
		t.Errorf("error message missing size: %q", msg)
	}

	// Two distinct calls produce distinct wraps (different paths/sizes),
	// even though both match the same sentinel.
	err2 := mmap.ErrFileTooLargeForPath("/tmp/y", 100)
	if errors.Is(err, err2) {
		t.Errorf("errors.Is(err, err2) = true; want false (distinct wraps)")
	}
	if !errors.Is(err2, mmap.ErrFileTooLarge) {
		t.Errorf("errors.Is(err2, ErrFileTooLarge) = false; want true")
	}
}

func TestOpenForSequentialRead_small_readAll(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	content := []byte(`{"hello":"world"}`)
	path := writeTempFile(t, dir, "small.json", content)

	r, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		t.Fatalf("OpenForSequentialRead: %v", err)
	}
	defer cleanup()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch:\n got: %s\nwant: %s", got, content)
	}
}

func TestOpenForSequentialRead_cleanup_called_twice(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)
	path := writeTempFile(t, dir, "x.json", []byte(`{}`))

	_, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		t.Fatalf("OpenForSequentialRead: %v", err)
	}
	// Calling cleanup twice must not panic (idempotent resource release).
	cleanup()
	// Second call — implementation uses _ = f.Close() which is safe to repeat
	// on a closed file (returns an error that is discarded).
	cleanup()
}

func TestOpenForSequentialRead_nonexistent(t *testing.T) {
	t.Parallel()
	_, cleanup, err := mmap.OpenForSequentialRead("/no/such/file.json")
	if err == nil {
		cleanup()
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestOpenForSequentialRead_large_readAll(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	content := makeLargeJSON(mmapThreshold + 8192)
	path := writeTempFile(t, dir, "large.json", content)

	r, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		t.Fatalf("OpenForSequentialRead (large): %v", err)
	}
	defer cleanup()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll (large): %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch on large file (lengths: got %d, want %d)", len(got), len(content))
	}
}

func TestOpenForSequentialRead_large_decode(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	content := makeLargeJSON(mmapThreshold + 4096)
	path := writeTempFile(t, dir, "large_decode.json", content)

	r, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		t.Fatalf("OpenForSequentialRead: %v", err)
	}
	defer cleanup()

	var doc map[string]string
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		t.Fatalf("json.Decode: %v", err)
	}
	if doc["key"] != "value" {
		t.Errorf("got key=%q; want %q", doc["key"], "value")
	}
}

// Cleanup must release the resource even when reading is interrupted mid-way.
func TestOpenForSequentialRead_large_cleanup_after_partial_read(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	content := makeLargeJSON(mmapThreshold + 65536)
	path := writeTempFile(t, dir, "partial.json", content)

	r, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		t.Fatalf("OpenForSequentialRead: %v", err)
	}

	// Read only a small prefix.
	buf := make([]byte, 128)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("partial read: %v", err)
	}

	// Cleanup must not panic or block.
	done := make(chan struct{})
	go func() {
		cleanup()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("cleanup blocked for more than 5 seconds")
	}
}

// A large kb.json must stream through a buffer, not a mapping: a mapping keeps
// every page it passes resident (212 MB peak for a 178 MB kb.json, vs 31 MB).
func TestOpenForSequentialRead_largeIsNotMapped(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)
	path := writeTempFile(t, dir, "large.json", makeLargeJSON(mmapThreshold+4096))

	r, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		t.Fatalf("OpenForSequentialRead: %v", err)
	}
	defer cleanup()

	if _, ok := r.(*bufio.Reader); !ok {
		t.Fatalf("got %T for a file over the mmap threshold; want *bufio.Reader", r)
	}
}

// The parser rewrites kb.json in place (truncate, then write). Through a
// mapping that crashed the reader with SIGBUS on Linux and macOS, and the
// rewrite itself failed on Windows. A buffered reader just reaches the new end
// of file early, and the JSON decode reports the error.
func TestOpenForSequentialRead_fileRewrittenWhileReading(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)
	path := writeTempFile(t, dir, "kb.json", makeLargeJSON(16<<20))

	r, cleanup, err := mmap.OpenForSequentialRead(path)
	if err != nil {
		t.Fatalf("OpenForSequentialRead: %v", err)
	}
	defer cleanup()

	if _, err := io.ReadFull(r, make([]byte, 4096)); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"structure":{`), 0o600); err != nil {
		t.Fatalf("rewriting the file while it is open for reading: %v", err)
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("reading after the rewrite: %v", err)
	}
}

// Concurrency: parallel DecodeJSONFile calls on the same file

// Concurrency: parallel OpenForSequentialRead calls on the same large file

func TestOpenForSequentialRead_concurrent_large(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	content := makeLargeJSON(mmapThreshold + 4096)
	path := writeTempFile(t, dir, "large_concurrent.json", content)

	const goroutines = 8
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			r, cleanup, err := mmap.OpenForSequentialRead(path)
			if err != nil {
				errs <- err
				return
			}
			defer cleanup()
			got, err := io.ReadAll(r)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, content) {
				errs <- fmt.Errorf("content mismatch (got %d bytes, want %d)", len(got), len(content))
				return
			}
			errs <- nil
		}()
	}

	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

// Edge cases: read-only / permission-denied files (Unix only)

func TestOpenForSequentialRead_permission_denied(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root — permission checks are bypassed")
	}
	if runtime.GOOS == "windows" {
		t.Skip("permission model differs on Windows")
	}

	dir := mustTempDir(t)
	path := writeTempFile(t, dir, "noperm.json", []byte(`{}`))

	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	_, cleanup, err := mmap.OpenForSequentialRead(path)
	if err == nil {
		cleanup()
		t.Error("expected permission error, got nil")
	}
}

// Benchmarks

func BenchmarkOpenForSequentialRead_large_readAll(b *testing.B) {
	dir, _ := os.MkdirTemp("", "bench_seq_*")
	defer func() { _ = os.RemoveAll(dir) }()

	data := makeLargeJSON(mmapThreshold + 65536)
	path := filepath.Join(dir, "seq.json")
	_ = os.WriteFile(path, data, 0o600)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		r, cleanup, err := mmap.OpenForSequentialRead(path)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, r); err != nil {
			cleanup()
			b.Fatal(err)
		}
		cleanup()
	}
}

// WithFileBytes must hand over the exact contents on both sides of the
// mapping threshold, and pass fn's error through.
func TestWithFileBytes(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)
	for _, size := range []int{1, mmapThreshold - 1, mmapThreshold, mmapThreshold + 4096} {
		want := bytes.Repeat([]byte("eulix\x00"), size/6+1)[:size]
		path := writeTempFile(t, dir, fmt.Sprintf("f%d.fb", size), want)
		var got []byte
		if err := mmap.WithFileBytes(path, func(b []byte) error {
			got = append([]byte(nil), b...) // fn must copy what it keeps
			return nil
		}); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("size %d: contents differ", size)
		}
	}
	sentinel := errors.New("stop")
	path := writeTempFile(t, dir, "e.fb", []byte("x"))
	if err := mmap.WithFileBytes(path, func([]byte) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want fn's error", err)
	}
	if err := mmap.WithFileBytes(filepath.Join(dir, "missing.fb"), func([]byte) error { return nil }); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: err = %v", err)
	}
}

// A file replaced by rename while it is mapped keeps its old contents for
// the reader: this is how eulix_parser replaces its outputs.
func TestWithFileBytes_renamedOverWhileMapped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow renaming over a mapped file")
	}
	t.Parallel()
	dir := mustTempDir(t)
	want := bytes.Repeat([]byte("old"), mmapThreshold/3+1)
	path := writeTempFile(t, dir, "kb.fb", want)
	err := mmap.WithFileBytes(path, func(b []byte) error {
		next := writeTempFile(t, dir, "kb.fb.tmp", []byte("new contents"))
		if err := os.Rename(next, path); err != nil {
			return err
		}
		if !bytes.Equal(b, want) {
			t.Error("mapped contents changed after the file was renamed over")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
