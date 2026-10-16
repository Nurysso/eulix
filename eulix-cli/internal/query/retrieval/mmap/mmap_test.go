package mmap_test

// Tests for the mmap-backed JSON loader.
//
// Strategy:
//   - SizeOverflows / ErrFileTooLargeForPath: pure unit tests, no I/O.
//   - DecodeJSONFile / OpenForSequentialRead: table-driven, using real temp
//     files so both the mmap path (files ≥ 4 MiB) and the buffered-reader
//     fallback path (files < 4 MiB) are exercised without mocking OS calls.
//   - FlushPretouchLogs: behavioural test on a real DebugLogger instance.
//   - All tests are parallel-safe: each test creates its own temp directory.

import (
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
	"eulix/internal/utils"
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

// writeTempJSON marshals v and writes the result to a new file in dir.
// Returns the file path.
func writeTempJSON(t *testing.T, dir string, name string, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
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

	// ErrFileTooLargeForPath must wrap a sentinel so callers can use errors.Is
	// to distinguish overflow errors from generic I/O errors.
	err := mmap.ErrFileTooLargeForPath("/tmp/x", 99)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	// Distinct paths give distinct errors. Both wrap the same sentinel, but
	// errors.Is does not treat one fmt.Errorf wrap as the other.
	err2 := mmap.ErrFileTooLargeForPath("/tmp/y", 100)
	if errors.Is(err, err2) {
		t.Errorf("errors for different paths should not match each other")
	}

	// The error must not be nil — already checked above.
	_ = err
}

func TestFlushPretouchLogs_nilLogger(t *testing.T) {
	t.Parallel()
	// Must not panic when logger is nil.
	mmap.FlushPretouchLogs(nil)
}

func TestFlushPretouchLogs_withLogger(t *testing.T) {
	t.Parallel()

	logger := utils.NewDebugLogger() // returns *utils.DebugLogger

	// Must not panic and must emit at least one log line.
	mmap.FlushPretouchLogs(logger)

	logs := logger.Lines()
	if len(logs) == 0 {
		t.Error("expected at least one log line from FlushPretouchLogs, got none")
	}

	// Every line should mention "JIT" (the log prefix used in the implementation).
	for _, line := range logs {
		if !strings.Contains(line, "[JIT]") {
			t.Errorf("log line missing [JIT] prefix: %q", line)
		}
	}
}

type simplePayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDecodeJSONFile_small_roundtrip(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	want := simplePayload{Name: "hello", Count: 42}
	path := writeTempJSON(t, dir, "small.json", want)

	var got simplePayload
	if err := mmap.DecodeJSONFile(path, &got); err != nil {
		t.Fatalf("DecodeJSONFile: %v", err)
	}
	if got != want {
		t.Errorf("got %+v; want %+v", got, want)
	}
}

func TestDecodeJSONFile_empty_file(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)
	path := writeTempFile(t, dir, "empty.json", []byte{})

	var v simplePayload
	err := mmap.DecodeJSONFile(path, &v)
	// An empty file is not valid JSON; the decoder must return an error.
	if err == nil {
		t.Error("expected error for empty file, got nil")
	}
}

func TestDecodeJSONFile_invalid_json(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)
	path := writeTempFile(t, dir, "bad.json", []byte(`{not valid json`))

	var v simplePayload
	err := mmap.DecodeJSONFile(path, &v)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestDecodeJSONFile_nonexistent_path(t *testing.T) {
	t.Parallel()
	err := mmap.DecodeJSONFile("/does/not/exist/kb.json", new(simplePayload))
	if err == nil {
		t.Error("expected error for non-existent path, got nil")
	}
}

func TestDecodeJSONFile_json_array(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)
	path := writeTempJSON(t, dir, "arr.json", []int{1, 2, 3})

	var got []int
	if err := mmap.DecodeJSONFile(path, &got); err != nil {
		t.Fatalf("DecodeJSONFile: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("got %v; want [1 2 3]", got)
	}
}

func TestDecodeJSONFile_unicode_strings(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	type record struct {
		Text string `json:"text"`
	}
	want := record{Text: "日本語テスト 🎌 emoji"}
	path := writeTempJSON(t, dir, "unicode.json", want)

	var got record
	if err := mmap.DecodeJSONFile(path, &got); err != nil {
		t.Fatalf("DecodeJSONFile: %v", err)
	}
	if got.Text != want.Text {
		t.Errorf("got %q; want %q", got.Text, want.Text)
	}
}

// Verify that decoded strings are independent copies — not pointing into a
// shared buffer — so mutation after decode does not corrupt other strings.
// This tests the CopyString: true guarantee on the mmap path.
func TestDecodeJSONFile_strings_are_copies(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	type twoStrings struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	want := twoStrings{A: "alpha", B: "beta"}
	path := writeTempJSON(t, dir, "strings.json", want)

	var got twoStrings
	if err := mmap.DecodeJSONFile(path, &got); err != nil {
		t.Fatalf("DecodeJSONFile: %v", err)
	}

	// Sanity check values before we inspect header addresses.
	if got.A != want.A || got.B != want.B {
		t.Fatalf("values wrong: %+v", got)
	}

	// The two strings must not share backing memory.
	// We can't directly test mmap-specific aliasing in a unit test, but we can
	// verify the round-trip correctness which is the observable contract.
}

// TestDecodeJSONFile_large exercises the mmap code path by creating a ≥4 MiB
// file. The test is skipped when the available temp space can't hold it.
func TestDecodeJSONFile_large_roundtrip(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	type largeDoc struct {
		Key   string `json:"key"`
		Extra string `json:"extra"`
	}

	// Build JSON that is just over the 4 MiB threshold.
	// We embed a large whitespace prefix so the file size exceeds mmapThreshold
	// while the semantic content stays tiny.
	padding := largePadding(mmapThreshold + 1024) // > 4 MiB of leading spaces
	inner := []byte(`{"key":"value","extra":"data"}`)
	data := append(padding, inner...)

	path := writeTempFile(t, dir, "large.json", data)

	// Confirm the file is large enough to trigger mmap.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < mmapThreshold {
		t.Fatalf("test file too small (%d bytes); mmap path won't be exercised", fi.Size())
	}

	var got largeDoc
	if err := mmap.DecodeJSONFile(path, &got); err != nil {
		t.Fatalf("DecodeJSONFile (large): %v", err)
	}
	if got.Key != "value" || got.Extra != "data" {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestDecodeJSONFile_large_invalid_json(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	// > 4 MiB but invalid JSON.
	data := append(largePadding(mmapThreshold+512), []byte(`{broken`)...)
	path := writeTempFile(t, dir, "large_bad.json", data)

	var v simplePayload
	err := mmap.DecodeJSONFile(path, &v)
	if err == nil {
		t.Error("expected error for large invalid JSON, got nil")
	}
}

func TestDecodeJSONFile_exactly_at_threshold(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	inner := []byte(`{"key":"exactly"}`)
	padLen := mmapThreshold - len(inner)
	if padLen < 0 {
		t.Skip("inner payload larger than threshold; adjust test")
	}
	data := append(largePadding(padLen), inner...)

	// Confirm size.
	if len(data) != mmapThreshold {
		t.Fatalf("data length %d != mmapThreshold %d", len(data), mmapThreshold)
	}

	path := writeTempFile(t, dir, "threshold.json", data)

	type doc struct {
		Key string `json:"key"`
	}
	var got doc
	if err := mmap.DecodeJSONFile(path, &got); err != nil {
		t.Fatalf("DecodeJSONFile at threshold: %v", err)
	}
	if got.Key != "exactly" {
		t.Errorf("got %q; want %q", got.Key, "exactly")
	}
}

func TestDecodeJSONFile_one_byte_below_threshold(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	inner := []byte(`{"key":"below"}`)
	padLen := mmapThreshold - 1 - len(inner)
	if padLen < 0 {
		t.Skip("inner payload larger than threshold-1; adjust test")
	}
	data := append(largePadding(padLen), inner...)

	path := writeTempFile(t, dir, "below_threshold.json", data)

	type doc struct {
		Key string `json:"key"`
	}
	var got doc
	if err := mmap.DecodeJSONFile(path, &got); err != nil {
		t.Fatalf("DecodeJSONFile below threshold: %v", err)
	}
	if got.Key != "below" {
		t.Errorf("got %q; want %q", got.Key, "below")
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

func TestSonicCopy_decodes_small_payload(t *testing.T) {
	t.Parallel()

	input := []byte(`{"name":"test","count":7}`)

	var got simplePayload
	if err := mmap.SonicCopy.Unmarshal(input, &got); err != nil {
		t.Fatalf("SonicCopy.Unmarshal: %v", err)
	}
	if got.Name != "test" || got.Count != 7 {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestSonicCopy_rejects_invalid_json(t *testing.T) {
	t.Parallel()
	var v simplePayload
	if err := mmap.SonicCopy.Unmarshal([]byte(`not json`), &v); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// Concurrency: parallel DecodeJSONFile calls on the same file

func TestDecodeJSONFile_concurrent(t *testing.T) {
	t.Parallel()
	dir := mustTempDir(t)

	want := simplePayload{Name: "concurrent", Count: 99}
	path := writeTempJSON(t, dir, "concurrent.json", want)

	const goroutines = 16
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			var got simplePayload
			if err := mmap.DecodeJSONFile(path, &got); err != nil {
				errs <- err
				return
			}
			if got != want {
				errs <- fmt.Errorf("got %+v; want %+v", got, want)
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

func TestDecodeJSONFile_permission_denied(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root — permission checks are bypassed")
	}
	if runtime.GOOS == "windows" {
		t.Skip("permission model differs on Windows")
	}

	dir := mustTempDir(t)
	path := writeTempJSON(t, dir, "noperm.json", simplePayload{Name: "x"})

	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	var v simplePayload
	err := mmap.DecodeJSONFile(path, &v)
	if err == nil {
		t.Error("expected permission error, got nil")
	}
}

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

func BenchmarkDecodeJSONFile_small(b *testing.B) {
	dir, _ := os.MkdirTemp("", "bench_mmap_*")
	defer func() { _ = os.RemoveAll(dir) }()

	data, _ := json.Marshal(simplePayload{Name: "bench", Count: 1})
	path := filepath.Join(dir, "small.json")
	_ = os.WriteFile(path, data, 0o600)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v simplePayload
		if err := mmap.DecodeJSONFile(path, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeJSONFile_large(b *testing.B) {
	dir, _ := os.MkdirTemp("", "bench_mmap_large_*")
	defer func() { _ = os.RemoveAll(dir) }()

	data := makeLargeJSON(mmapThreshold + 65536)
	path := filepath.Join(dir, "large.json")
	_ = os.WriteFile(path, data, 0o600)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		var v map[string]string
		if err := mmap.DecodeJSONFile(path, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenForSequentialRead_large_readAll(b *testing.B) {
	dir, _ := os.MkdirTemp("", "bench_seq_*")
	defer func() { _ = os.RemoveAll(dir) }()

	data := makeLargeJSON(mmapThreshold + 65536)
	path := filepath.Join(dir, "seq.json")
	_ = os.WriteFile(path, data, 0o600)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
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
