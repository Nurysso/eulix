//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

//go:build unix

/*
This file is responsible for mmap on unix based system uses
MADV_SEQUENTIAL for improved performance
*/

package query

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// decodeViaMmap maps path read-only and decodes JSON directly from mmaping
// The caller passes size from an os.Stat; re-stat the open fd to close
// the stat-then-mmap TOCTOU window without this a concurrent truncation produces
// a mapping that extends past EOF and get SIGBUS on the first access.
func decodeViaMmap(path string, size int64, v any) error {
	if sizeOverflows(size) {
		return errFileTooLargeForPath(path, size)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Close the stat-then-mmap TOCTOU race: re-stat the open fd and
	// use the live size. If the file chanfed, retry with the new size.
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size = fi.Size()
	if sizeOverflows(size) {
		return errFileTooLargeForPath(path, size)
	}
	if size == 0 {
		return fmt.Errorf("empty file: %s", path)
	}

	data, err := mmapPlatform(f, int(size))
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}
	defer unix.Munmap(data)

	mmapAdvisePlatform(data)

	// sonicCopy copies all decoded strings out of data before we Munmap.
	// Using sonic.ConfigDefault (CopyString: false) here would leave decoded
	// string headers pointing into the now-unmapped region — use-after-free.
	return sonicCopy.Unmarshal(data, v)
}

// mmapForSequentialRead mmaps path read-only and advises the kernel to
// prefetch sequentially (MADV_SEQUENTIAL), matching JSON parse order.
func mmapForSequentialRead(path string, size int64) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}

	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	size = fi.Size()
	if sizeOverflows(size) || size == 0 {
		f.Close()
		return nil, nil, fmt.Errorf("mmapForSequentialRead: invalid size %d for %s", size, path)
	}

	// mapped, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	mmaped, err := mmapPlatform(f, int(size))
	if err != nil {
		f.Close()
		return nil, nil, err
	}

	// _ = unix.Madvise(mapped, unix.MADV_SEQUENTIAL)
	_ = f.Close() // mapping holds the pages; fd not needed after mmap
	mmapAdvisePlatform(mmaped)
	return bytes.NewReader(mmaped), func() { _ = unix.Munmap(mmaped) }, nil
}
