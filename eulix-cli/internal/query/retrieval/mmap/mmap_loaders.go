//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package mmap hold os specific mmap helper code.

/*
Mmap backed json loader, optimised for 2-4GB kb.json
on machines with 16gb RAM OS(win11/ linux kernel / darwin)
Memory model:
	raw file bytes parsed struct (heap)
	os.ReadFile + Unmarshal 	in heap 	in heap		~2x file
	mmap + Unmarshal	page cache 		in heap		->~ file
	mmap + streaming Decode 	page cache 		built incrementally -> bounded
Platform notes:
	- Linux kernel(tested on 6.18 lts): MAP_POPULATE + MADV_SEQUENTIAL + MADV_HUGEPAGE.
	- Windows 11: FILE_FLAG_SEQUENTIAL_ONLY at file-open time +
	- PrefetchVirtualMemory after MapViewOfFile.
	- macOS: MADV_SEQUENTIAL via the UBC; MAP_PRIVATE preferred to keep the working set exclusive.
*/

package mmap

import (
	"bufio"
	"errors"
	"eulix/internal/utils"
	"fmt"
	"io"
	"os"
	"reflect"

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

	// jsonBufSize specifies the I/O read buffer size (1 MiB) for the non-mmap fallback path.
	// This balances minimal syscall frequency on large files against low heap allocation pressure.
	jsonBufSize = 1 << 20
)

// errFileTooLarge is returned when size would overflow int on 32 bits platform
var errFileTooLarge = errors.New("file size overflows int on this platform")

// Stores results produced during package init()
var initPretouchResults []PretouchResult

// sizeFitsInt returns true when size can't be represented as int.
func SizeOverflows(size int64) bool {
	const maxInt = int64(^uint(0) >> 1)
	return size > maxInt
}

// errFileTooLargeForPath formats the overflow error with path context.
func ErrFileTooLargeForPath(path string, size int64) error {
	return fmt.Errorf("%w: %s is %d bytes", errFileTooLarge, path, size)
}

// sonicCopy is a sonic config that always copies strings out of the input
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

// openForSequentialRead returns an io.Reader over path, using mmap with
// sequential-read hints for large files and a buffered file reader otherwise.
//
// The returned cleanup func must always be called once reader is no longer
// in use (this is true even for the buffered-reader fallback,
// releasing the file handle on cleanup so the caller has a single shutdown path
// regardless of which backend was used
func OpenForSequentialRead(path string) (r io.Reader, cleanup func(), err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	size := fi.Size()

	if size >= mmapThreshold && !SizeOverflows(size) {
		if r, cleanup, err := MmapForSequentialRead(path, size); err == nil {
			return r, cleanup, nil
		}
		// mmap unsupported or failed — fall through to buffered reader.
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return bufio.NewReaderSize(f, jsonBufSize), func() { _ = f.Close() }, nil
}
