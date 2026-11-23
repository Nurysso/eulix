// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

/*
File access for the knowledge-base readers (internal/kbfile), sized for
multi-gigabyte outputs on 16 GB machines (Linux, Windows 11, macOS).

	WithFileBytes          the whole file as one slice: memory-mapped from
	                       4 MiB (pages come from the page cache), read below
	OpenForSequentialRead  a 1 MiB buffered reader for one front-to-back pass
	                       (kb.fb's record walk), never mapped

Platform notes (mapFile):
	- Linux kernel (tested on 6.18 LTS): MAP_SHARED + MADV_SEQUENTIAL + MADV_HUGEPAGE.
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
)

const (
	// mmapThreshold defines the minimum file size (4 MiB) to use memory mapping (mmap).
	// Below 4 MiB, setup overhead (page-table allocation, first-access page faults)
	// exceeds the savings gained by bypassing standard read() copies on Linux and Windows 11.
	// Smaller files fall back to standard buffered I/O.
	mmapThreshold = 4 << 20

	// readBufSize is the read buffer (1 MiB) of OpenForSequentialRead.
	// This balances minimal syscall frequency on large files against low heap allocation pressure.
	readBufSize = 1 << 20
)

// ErrFileTooLarge is returned when size would overflow int on 32 bits platform
var ErrFileTooLarge = errors.New("file size overflows int on this platform")

// SizeOverflows returns true when size can't be represented as int.
func SizeOverflows(size int64) bool {
	const maxInt = int64(^uint(0) >> 1)
	return size > maxInt
}

// ErrFileTooLargeForPath formats the overflow error with path context.
func ErrFileTooLargeForPath(path string, size int64) error {
	return fmt.Errorf("%w: %s is %d bytes", ErrFileTooLarge, path, size)
}

// WithFileBytes calls fn with the whole contents of path. Files of
// mmapThreshold or more are memory-mapped (read-only, released when fn
// returns); smaller files, and any file whose mapping fails, are read into
// memory. fn must not keep the slice or any sub-slice after it returns:
// copy out what it needs (string(b) does). Used for the FlatBuffers outputs,
// which are read in place without a parse step.
//
// The parser replaces outputs by rename, so a file mapped here keeps its old
// contents even if `eulix analyze` runs meanwhile.
func WithFileBytes(path string, fn func([]byte) error) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if size := fi.Size(); size >= mmapThreshold && !SizeOverflows(size) {
		if data, release, err := mapFile(path); err == nil {
			defer release()
			return fn(data)
		}
		// mapping failed (sandbox, exotic FS, platform without mmap): read instead.
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return fn(data)
}

// OpenForSequentialRead returns a buffered reader over path for a single
// front-to-back pass, and a cleanup func that closes the file. The caller must
// always call cleanup once it is done with the reader.
//
// No mmap here, whatever the size. The streaming caller (kbfile.WalkKB)
// copies each record out anyway, so a mapping saves no copy, and every page it
// passes stays resident until unmap: measured on the JSON knowledge base this
// replaced, a 178 MB file peaked at 212 MB mapped and 31 MB read through this
// buffer, and the buffered read was ~5% faster. Reading also can't SIGBUS if
// the file is replaced or truncated underneath us; a short file just fails to
// decode.
func OpenForSequentialRead(path string) (r io.Reader, cleanup func(), err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return bufio.NewReaderSize(f, readBufSize), func() { _ = f.Close() }, nil
}
