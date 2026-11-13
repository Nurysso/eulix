// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

/*
JSON loaders for the knowledge-base files, sized for 2-4GB kb.json
on machines with 16gb RAM OS(win11/ linux kernel / darwin)
Memory model:
	raw file bytes parsed struct (heap)
	os.ReadFile + Unmarshal 	in heap 	in heap		~2x file
	mmap + Unmarshal (DecodeJSONFile)	page cache 		in heap		->~ file
	buffered streaming Decode (OpenForSequentialRead)	1 MiB buffer 		built incrementally -> bounded
Platform notes (DecodeViaMmap):
	- Linux kernel(tested on 6.18 lts): MAP_SHARED + MADV_SEQUENTIAL + MADV_HUGEPAGE.
	- Windows 11: FILE_FLAG_SEQUENTIAL_SCAN at file-open time +
	  PrefetchVirtualMemory over the whole view after MapViewOfFile.
	- macOS: MAP_PRIVATE + MADV_SEQUENTIAL.
*/

package mmap

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"

	"eulix/internal/utils"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/option"
)

// PretouchResult records the outcome of a JIT pretouch operation.
type PretouchResult struct {
	Target string
	Err    error
}

const (
	// mmapThreshold defines the minimum file size (4 MiB) to use memory mapping (mmap).
	// Below 4 MiB, setup overhead (page-table allocation, first-access page faults)
	// exceeds the savings gained by bypassing standard read() copies on Linux and Windows 11.
	// Smaller files fall back to standard buffered I/O.
	mmapThreshold = 4 << 20

	// jsonBufSize specifies the I/O read buffer size (1 MiB) for the buffered
	// paths: decodeViaReader and OpenForSequentialRead.
	// This balances minimal syscall frequency on large files against low heap allocation pressure.
	jsonBufSize = 1 << 20
)

// errFileTooLarge is returned when size would overflow int on 32 bits platform
var ErrFileTooLarge = errors.New("file size overflows int on this platform")

// Stores results produced during package init()
var initPretouchResults []PretouchResult

// SizeOverflows returns true when size can't be represented as int.
func SizeOverflows(size int64) bool {
	const maxInt = int64(^uint(0) >> 1)
	return size > maxInt
}

// ErrFileTooLargeForPath formats the overflow error with path context.
func ErrFileTooLargeForPath(path string, size int64) error {
	return fmt.Errorf("%w: %s is %d bytes", ErrFileTooLarge, path, size)
}

// SonicCopy is a sonic config that always copies strings out of the input
// buffer. This is required on the mmap path: the mapped region is unmapped
// before the caller uses the decoded value, so any decoded string that
// references the raw bytes directly would become a dangling pointer.
// CopyString: true adds a small allocation cost but is safe on all paths.
var SonicCopy = sonic.Config{CopyString: true}.Froze()

func init() {
	targets := []struct {
		name string
		typ  reflect.Type
	}{
		{"knowledge Base", reflect.TypeOf(utils.KnowledgeBaseSimplifiedRef{})},
		{"Index", reflect.TypeOf(utils.IndexDataRef{})},
		{"CallGraphRef", reflect.TypeOf(utils.CallGraphRef{})},
	}

	for _, t := range targets {
		err := sonic.Pretouch(
			t.typ,
			option.WithCompileRecursiveDepth(8),
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[JIT] Failed to pretouch %s: %v\n", t.name, err)
		}

		// Collect execution status for deferred logging
		initPretouchResults = append(initPretouchResults, PretouchResult{
			Target: t.name,
			Err:    err,
		})
	}
}

// FlushPretouchLogs passes the recorded init results into any DebugLogger instance.
func FlushPretouchLogs(logger *utils.DebugLogger) {
	if logger == nil {
		return
	}
	logger.Log("[JIT] Initializing context: starting JIT pretouching for target Files...")
	for _, res := range initPretouchResults {
		if res.Err != nil {
			logger.Log("[JIT] Pretouch failed for %s: %v", res.Target, res.Err)
		} else {
			logger.Log("[JIT] Pretouch succeeded for %s", res.Target)
		}
	}
}

// DecodeJSONFile decodes path into v using the fastest available strategy:
//
//	Files ≥ mmapThreshold: mmap + sonic.Unmarshal
//	Files < mmapThreshold: buffered reader + sonic streaming decoder.
//
// Mmap failures fall back transparently to buffered reader. The
// fallback s intentional and silent at this layer, same os.Open
// will fail again in the fallback path, surfacing the real cause.
// Any error falls back, decode errors included, so a corrupt file is parsed
// twice. Intended: RET-12 in docs/Known-Issues/Retrieval-And-Query.md.
func DecodeJSONFile(path string, v any) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}

	size := fi.Size()

	if size >= mmapThreshold && !SizeOverflows(size) {
		if err := DecodeViaMmap(path, size, v); err == nil {
			return nil
		}
		// mmap failed (sandbox, exotic FS, OOM on mapping) fall through.
	}

	return decodeViaReader(path, v)
}

// decodeViaReader is the non-mmap fallback. sonicCopy is used because
// the bufio.Reader is heap-allocated and its buffer is reused across
// reads, strings that span two reads would otherwise reference the wrong buffer.
func decodeViaReader(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return SonicCopy.
		NewDecoder(bufio.NewReaderSize(f, jsonBufSize)).
		Decode(v)
}

// OpenForSequentialRead returns a buffered reader over path for a single
// front-to-back pass, and a cleanup func that closes the file. The caller must
// always call cleanup once it is done with the reader.
//
// No mmap here, whatever the size. The streaming caller (json.Decoder in
// loader.go) copies every byte into its own buffer anyway, so a mapping saves
// no copy, and every page it passes stays resident until unmap: a 178 MB
// kb.json peaked at 212 MB mapped and 31 MB read through this buffer, and the
// buffered read was ~5% faster. Reading also can't SIGBUS if the file is
// replaced or truncated underneath us; a short file just fails to decode.
func OpenForSequentialRead(path string) (r io.Reader, cleanup func(), err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return bufio.NewReaderSize(f, jsonBufSize), func() { _ = f.Close() }, nil
}
