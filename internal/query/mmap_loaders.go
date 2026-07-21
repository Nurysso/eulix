//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing, content loading and retrival for EULIX.

/*
Mmap backed json loader, optimised for 2-4GB kb.json
on machines with 16gb RAM OS(win11/ linux kernel / darwin)
Memory model:
	raw file bytes parsed struct (heap)
	os.ReadFile + Unmarshal 	in heap 	in heap		~2x file
	mmap + Unmarshal	page cache 		in heap		->~ file
	mmap + streaming Decode 	page cache 		built incrementally -> bounded

Strategy:
	1.  Files >= mmapThreshold: mmap the files, advise the kernel for
		sequential / huge-page access, decode directly. On Linux kernel
		use MAP_POPULATE so the page tables are pre-faulted before
		sonic starts walking the bytes, this hides page-fault latency
		behind syscall overhead.

	2.  Files < mmapThreshold, or mmap unavailable: buffered reader +
		sonic streaming decoder. The 1MiB read buffer (jsonBufSize)
		keeps syscall frequency low without bloating heap.

why mmap for large JSON files?
	A 4 GB file read into heap and then parsed gives 4 GB raw + 4-12 GB
	parsed (string headers, slice buffers, map overhead) → OOM. With
	mmap, the raw bytes live in the kernel page cache, sonic reads
	them directly, and the only heap cost is the parsed struct + the
	copied strings. For huge kb.json that's the difference between a
	~14 GB peak RSS and a ~6-8 GB peak RSS.

Platform notes:
	- Linux kernel(tested on 6.18 lts): MAP_POPULATE + MADV_SEQUENTIAL + MADV_HUGEPAGE.
	- Windows 11: FILE_FLAG_SEQUENTIAL_ONLY at file-open time +
	- PrefetchVirtualMemory after MapViewOfFile.
	- macOS: MADV_SEQUENTIAL via the UBC; MAP_PRIVATE preferred to keep the working set exclusive.
*/

package query

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"

	"eulix/internal/types"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/option"
)

const (
	// mmapThreshold: files at or above this size uses mmap; smaller files
	// go through buffered reader. 4MiB is the empirical break-even
	// point on linux and win11 below that the mmap setup cost
	// (page-table allocation, page faults on first access) exceeds
	// the savings from avoiding the extra read() copy
	mmapThreshold = 4 << 20

	// jsonBufSize is the I/O read buffer for the non-mmap path.
	// 1 MiB minimises syscall frequency on large files without adding
	// significant heap pressure
	jsonBufSize = 1 << 20
)

// sonicCopy is a sonic config that always copies strings out of the input
// buffer. This is required on the mmap path: the mapped region is unmapped
// before the caller uses the decoded value, so any decoded string that
// references the raw bytes directly would become a dangling pointer.
// CopyString: true adds a small allocation cost but is safe on all paths.
var sonicCopy = sonic.Config{CopyString: true}.Froze()

// errFileTooLarge is returned when size would overflow int on 32 bits platform
var errFileTooLarge = errors.New("file size overflows int on this platform")

func (cb *ContextBuilder) init() {
	cb.debugLog.Log("Initializing context: starting JIT pretouching for target types...")

	targets := []struct {
		name string
		typ  reflect.Type
	}{
		{"FileData", reflect.TypeOf(types.FileData{})},
		{"IndexRef", reflect.TypeOf(types.IndexRef{})},
		{"ExternalDependencyRef", reflect.TypeOf(types.ExternalDependencyRef{})},
		{"CallGraphRef", reflect.TypeOf(types.CallGraphRef{})},
	}

	successCount := 0
	for _, t := range targets {
		if err := sonic.Pretouch(
			t.typ,
			option.WithCompileRecursiveDepth(8),
		); err != nil {
			cb.debugLog.Log("Failed to pretouch sonic type %s: %v", t.name, err)
		} else {
			cb.debugLog.Log("Successfully pretouched type: %s", t.name)
			successCount++
		}
	}

	cb.debugLog.Log("Context initialization complete (%d/%d types pretouched)", successCount, len(targets))
}

// decodeJSONFile decodes path into v using the fastest available strategy:
//
//	Files ≥ mmapThreshold: mmap + sonic.Unmarshal
//	Files < mmapThreshold: buffered reader + sonic streaming decoder.
//
// Mmap failures fall back transparently to buffered reader. The
// fallback s intentional and silent at this layer, same os.Open
// will fail again in the fallback path, surfacing the real cause.
func decodeJSONFile(path string, v any) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}

	size := fi.Size()

	if size >= mmapThreshold && !sizeOverflows(size) {
		if err := decodeViaMmap(path, size, v); err == nil {
			return nil
		}
		// mmap failed (sandbox, exotic FS, OOM on mapping) fall through.
	}

	return decodeViaReader(path, v)
}

// decodeViaReader is the non-mmap fallback. sonicCopy is used because
// the bufio.Reader is heap-allocated and its buffer is reused across
// reads, strings that span two reads would otherwise refrence the wrong buffer.
func decodeViaReader(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return sonicCopy.
		NewDecoder(bufio.NewReaderSize(f, jsonBufSize)).
		Decode(v)
}

// sizeFitsInt returns true when size can't be represented as int.
func sizeOverflows(size int64) bool {
	const maxInt = int64(^uint(0) >> 1)
	return size > maxInt
}

// errFileTooLargeForPath formats the overflow error with path context.
func errFileTooLargeForPath(path string, size int64) error {
	return fmt.Errorf("%w: %s is %d bytes", errFileTooLarge, path, size)
}

// openForSequentialRead returns an io.Reader over path, using mmap with
// sequential-read hints for large files and a buffered file reader otherwise.
//
// The returned cleanup func must always be called once reader is no longer
// in use (this is true even for the buffered-reader fallback,
// releasing the file handle on cleanup so the caller has a single shutdown path
// regardless of which backend was used
func openForSequentialRead(path string) (r io.Reader, cleanup func(), err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	size := fi.Size()

	if size >= mmapThreshold && !sizeOverflows(size) {
		if r, cleanup, err := mmapForSequentialRead(path, size); err == nil {
			return r, cleanup, nil
		}
		// mmap unsupported or failed — fall through to buffered reader.
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return bufio.NewReaderSize(f, jsonBufSize), func() { f.Close() }, nil
}
