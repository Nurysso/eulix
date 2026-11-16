// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

//go:build unix

/*
This file is responsible for mmap on unix based system uses
MADV_SEQUENTIAL for improved performance
*/

package mmap

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// decodeViaMmap maps path read-only and decodes JSON directly from mmaping
// The caller passes size from an os.Stat; re-stat the open fd to close
// the stat-then-mmap TOCTOU window without this a concurrent truncation produces
// a mapping that extends past EOF and get SIGBUS on the first access.
// Truncation after mapping (during the parse) still raises SIGBUS. Won't fix:
// RET-10 in docs/Known-Issues/Retrieval-And-Query.md.
func DecodeViaMmap(path string, size int64, v any) error {
	if SizeOverflows(size) {
		return ErrFileTooLargeForPath(path, size)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Close the stat-then-mmap TOCTOU race: re-stat the open fd and
	// use the live size. If the file chanfed, retry with the new size.
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size = fi.Size()
	if SizeOverflows(size) {
		return ErrFileTooLargeForPath(path, size)
	}
	if size == 0 {
		return fmt.Errorf("empty file: %s", path)
	}

	data, err := mmapPlatform(f, int(size))
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}
	defer func() { _ = unix.Munmap(data) }()

	mmapAdvisePlatform(data)

	// sonicCopy copies all decoded strings out of data before we Munmap.
	// Using sonic.ConfigDefault (CopyString: false) here would leave decoded
	// string headers pointing into the now-unmapped region — use-after-free.
	return SonicCopy.Unmarshal(data, v)
}
