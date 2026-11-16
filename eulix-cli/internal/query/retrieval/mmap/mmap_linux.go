// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

//go:build linux

/*
	This file owns the Linux-specific constants (MADV_HUGEPAGE)
	so that the macOS / BSD build (which doesn't have
	them) still compiles. mmap_unix.go calls into mmapPlatform /
	mmapAdvisePlatform, which are defined here for Linux and in
	mmap_darwin.go (and any future mmap_*bsd.go) for the other targets.
*/

package mmap

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// mmapPlatform [tested] on Linux kernel 6.18 LTS
//
// MAP_SHARED, read-only: the pages are the file's page-cache pages, so
// nothing is copied and other readers share them.
//
// No MAP_POPULATE. It pre-faults the whole file inside the mmap call,
// so the load waits for the full disk read before sonic starts. On a
// 228 MB kb_index.json it was no faster (276 ms with it vs 254 ms
// without, cold cache; 198 vs 199 ms warm). Peak memory is the same
// either way: sonic touches every page, and touched pages stay mapped
// until Munmap. TestMmapPlatformDoesNotPrefault guards this.
func mmapPlatform(f *os.File, size int) ([]byte, error) {
	return unix.Mmap(
		int(f.Fd()),
		0,
		size,
		unix.PROT_READ,
		unix.MAP_SHARED,
	)
}

// mmapAdvisePlatform
// MADV_SEQUENTIAL: more aggressive readahead, and pages already passed
// are treated as cheap to reclaim under memory pressure. It does not
// drop them: they stay resident until Munmap, so a whole-file decode
// holds about the file's size in page cache at its peak.
//
// MADV_HUGEPAGE: prefer 2 MiB transparent huge pages to cut TLB
// pressure on large mappings. Best-effort: ENOMEM or EINVAL just
// means we fall back to base pages, which is still correct. For file
// mappings it only applies where the filesystem supports large folios.
//
// MADV_COLLAPSE (5.18+) would be a stronger hint that actively
// collapses pages, but it requires the range to already be populated.
// Not added; revisit if you see TLB misses in perf traces.
func mmapAdvisePlatform(data []byte) {
	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)
	_ = unix.Madvise(data, unix.MADV_HUGEPAGE)
}

// AllocEmbeddingMatrix returns a GC-owned matrix for callers without a lifecycle.
func AllocEmbeddingMatrix(n, dim int) [][]float32 {
	flat := make([]float32, n*dim)
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = flat[i*dim : (i+1)*dim]
	}
	return rows
}

// AllocEmbeddingMatrixOwned retains the entire mapping for deterministic release.
// After release the rows are unmapped: a query still reading them segfaults.
// Only chat's quit path can do that. Won't fix: RET-11 in
// docs/Known-Issues/Retrieval-And-Query.md.
func AllocEmbeddingMatrixOwned(n, dim int) ([][]float32, func() error) {
	if n == 0 || dim == 0 {
		return AllocEmbeddingMatrix(n, dim), func() error { return nil }
	}
	flat, release := allocHugepageAligned(n * dim)
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = flat[i*dim : (i+1)*dim]
	}
	return rows, release
}

// allocHugepageAligned also returns a release func that unmaps the whole
// mapping; the aligned slice alone can't be passed to Munmap.
func allocHugepageAligned(n int) ([]float32, func() error) {
	const hugepage = 2 << 20 // 2MB
	size := n * 4            // Allocate raw bytes, aligned to hugepage boundary
	// mmap anonymous gives page-aligned memory; request extra for alignment
	b, err := unix.Mmap(-1, 0, size+hugepage,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	if err != nil {
		// fallback to make()
		return make([]float32, n), func() error { return nil }
	}
	// Align to 2MB boundary
	addr := uintptr(unsafe.Pointer(&b[0]))
	aligned := (addr + uintptr(hugepage) - 1) &^ (uintptr(hugepage) - 1)
	offset := int(aligned - addr)
	alignedBytes := b[offset : offset+size]
	_ = unix.Madvise(b, unix.MADV_HUGEPAGE)
	// Return as float32 slice
	flat := unsafe.Slice((*float32)(unsafe.Pointer(&alignedBytes[0])), n)
	return flat, func() error {
		if b == nil {
			return nil
		}
		if err := unix.Munmap(b); err != nil {
			return err
		}
		b = nil
		return nil
	}
}
