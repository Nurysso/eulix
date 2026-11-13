// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

//go:build darwin

/*
mmap_darwin.go macOS mmap flags and madvise hints.

	macOS doesn't have Linux's MAP_POPULATE or MADV_HUGEPAGE. The
	mapping is read-only MAP_PRIVATE, and MADV_SEQUENTIAL drives
	readahead.
*/
package mmap

import (
	"os"

	"golang.org/x/sys/unix"
)

// mmapPlatform macOS.
// MAP_PRIVATE and MAP_SHARED behave the same here: we never write, so
// no copy-on-write happens, and both map the same pages of the unified
// buffer cache (UBC). The flag has no effect on caching or on what
// other processes keep in memory; MAP_SHARED would work just as well.
func mmapPlatform(f *os.File, size int) ([]byte, error) {
	return unix.Mmap(
		int(f.Fd()),
		0,
		size,
		unix.PROT_READ,
		unix.MAP_PRIVATE,
	)
}

// mmapAdvisePlatform macOS.
//
// MADV_SEQUENTIAL triggers readahead via the UBC. There's no direct
// THP equivalent superpage promotion happens implicitly for large
// mappings on Apple silicon, so no extra hint is needed.
//
// On Darwin the MADV_SEQUENTIAL constant is defined in <sys/mman.h>
// and exposed by golang.org/x/sys/unix.
func mmapAdvisePlatform(data []byte) {
	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)
}

func AllocEmbeddingMatrix(n, dim int) [][]float32 {
	flat := make([]float32, n*dim)
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = flat[i*dim : (i+1)*dim]
	}
	return rows
}
