//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

//go:build linux

/*
	This file owns the Linux-specific constants (MAP_POPULATE,
	MADV_HUGEPAGE) so that the macOS / BSD build (which doesn't have
	them) still compiles. mmap_unix.go calls into mmapPlatform /
	mmapAdvisePlatform, which are defined here for Linux and in
	mmap_darwin.go (and any future mmap_*bsd.go) for the other targets.
*/

package query

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// mmapPlatform [tested] on Linux kernel 6.18 LTS

// MAP_POPULATE pre-faults the page tables at mmap time, eliminating
// the minor-fault spike on first access. For multi-GB files this is
// a real win without it, sonic's first scan over the mapping pays
// a fault for every 4 KiB page. With it, the cost is paid up-front
// during the mmap syscall, where the kernel can parallelise the I/O.
//
// MAP_SHARED: we read but the kernel also keeps the file in the page
// cache for other processes / future reads. With MADV_SEQUENTIAL the
// pages are released from the cache as we move past them, so cache
// pressure is bounded by the readahead window (capped at 2 MiB on
// Linux 6.x after the kernel's doubling pattern stabilises).
func mmapPlatform(f *os.File, size int) ([]byte, error) {
	return unix.Mmap(
		int(f.Fd()),
		0,
		size,
		unix.PROT_READ,
		unix.MAP_SHARED|unix.MAP_POPULATE,
	)
}

// mmapAdvisePlatform
// MADV_SEQUENTIAL: the kernel doubles readahead on each call (capped
// at 2 MiB) and releases consumed pages from the page cache. For a
// 4 GB JSON file, peak page-cache usage during a single linear scan
// stays under 100 MB even though the entire file is read once.
//
// MADV_HUGEPAGE: prefer 2 MiB transparent huge pages. For a 4 GB
// mapping this cuts TLB pressure by ~512× (4 GB / 2 MiB ≈ 2 K TLB
// entries vs ~1 M for base pages). Best-effort: ENOMEM or EINVAL
// just means we fall back to base pages, which is still correct.
//
// MADV_COLLAPSE (5.18+) would be a stronger hint that actively
// collapses pages, but it requires the range to already be populated
// — combined with MAP_POPULATE it could replace MADV_HUGEPAGE for a
// small additional win. Not added by default; revisit if you see TLB
// misses in perf traces.
func mmapAdvisePlatform(data []byte) {
	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)
	_ = unix.Madvise(data, unix.MADV_HUGEPAGE)
}

func allocHugepageAligned(n int) []float32 {
	const hugepage = 2 << 20 // 2MB
	size := n * 4            // Allocate raw bytes, aligned to hugepage boundary
	// mmap anonymous gives page-aligned memory; request extra for alignment
	b, err := unix.Mmap(-1, 0, size+hugepage,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	if err != nil {
		// fallback to make()
		return make([]float32, n)
	}
	// Align to 2MB boundary
	addr := uintptr(unsafe.Pointer(&b[0]))
	aligned := (addr + uintptr(hugepage) - 1) &^ (uintptr(hugepage) - 1)
	offset := int(aligned - addr)
	aligned_b := b[offset : offset+size]
	_ = unix.Madvise(b, unix.MADV_HUGEPAGE)
	// Return as float32 slice
	ptr := (*float32)(unsafe.Pointer(&aligned_b[0]))
	return unsafe.Slice(ptr, n)
}
