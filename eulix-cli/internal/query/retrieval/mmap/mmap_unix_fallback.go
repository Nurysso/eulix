// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

//go:build unix && !linux && !darwin

/*
Generic unix fallback for platforms other
	than Linux and macOS (freebsd, openbsd, netbsd, etc.). We don't
	define Linux-specific constants (MADV_HUGEPAGE) here;
	instead we use conservative MAP_SHARED + MADV_SEQUENTIAL, which
	works on all unix variants. Build-tested behaviour is best-effort;
	if mmap is restricted by the platform, the decodeJSONFile fallback
	to the buffered reader kicks in transparently.
*/

package mmap

import (
	"os"

	"golang.org/x/sys/unix"
)

// mmapPlatform generic unix. MAP_SHARED is the most portable
// choice. Linux and macOS have their own files: MAP_SHARED with
// MADV_HUGEPAGE on Linux, MAP_PRIVATE on macOS.
func mmapPlatform(f *os.File, size int) ([]byte, error) {
	return unix.Mmap(
		int(f.Fd()),
		0,
		size,
		unix.PROT_READ,
		unix.MAP_SHARED,
	)
}

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
