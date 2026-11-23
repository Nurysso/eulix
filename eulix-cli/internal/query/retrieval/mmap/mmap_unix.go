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

// mapFile maps path read-only for WithFileBytes. The live size of the open
// file is mapped, so a stat-then-open race cannot map past EOF.
func mapFile(path string) ([]byte, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	size := fi.Size()
	if SizeOverflows(size) {
		return nil, nil, ErrFileTooLargeForPath(path, size)
	}
	if size == 0 {
		return nil, nil, fmt.Errorf("empty file: %s", path)
	}
	data, err := mmapPlatform(f, int(size))
	if err != nil {
		return nil, nil, fmt.Errorf("mmap: %w", err)
	}
	mmapAdvisePlatform(data)
	return data, func() { _ = unix.Munmap(data) }, nil
}
