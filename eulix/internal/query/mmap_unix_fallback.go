//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

//go:build unix && !linux && !darwin

/*
Generic unix fallback for platforms other
	than Linux and macOS (freebsd, openbsd, netbsd, etc.). We don't
	define Linux-specific constants (MAP_POPULATE, MADV_HUGEPAGE) here;
	instead we use conservative MAP_SHARED + MADV_SEQUENTIAL, which
	works on all unix variants. Build-tested behaviour is best-effort;
	if mmap is restricted by the platform, the decodeJSONFile fallback
	to the buffered reader kicks in transparently.
*/

package query

import (
	"os"

	"golang.org/x/sys/unix"
)

// mmapPlatform generic unix. MAP_SHARED is the most portable
// choice, on Linux and macOS have OS-specific files that pick
// MAP_POPULATE / MAP_PRIVATE respectively.
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

func allocEmbeddingMatrix(n, dim int) [][]float32 {
	flat := make([]float32, n*dim)
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = flat[i*dim : (i+1)*dim]
	}
	return rows
}
