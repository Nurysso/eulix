//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

//go:build darwin

/*
mmap_darwin.go macOS mmap flags and madvise hints.
	macOS doesn't have Linux's MAP_POPULATE or MADV_HUGEPAGE, but the
	unified buffer cache (UBC) treats MAP_SHARED and MAP_PRIVATE
	identically for read-only access. We use MAP_PRIVATE to keep the
	working set exclusive no shared cache pollution between
	processes and rely on MADV_SEQUENTIAL for readahead.
*/
package query

import (
	"os"

	"golang.org/x/sys/unix"
)

// mmapPlatform macOS.
// MAP_PRIVATE is preferred over MAP_SHARED because the UBC backs
// both identically for read-only access, and MAP_PRIVATE keeps the
// working set exclusive. For a 2-4 GB JSON this means the pages
// we touch don't evict other processes' hot data.
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
