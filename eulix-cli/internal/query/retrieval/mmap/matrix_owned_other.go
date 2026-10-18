// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package mmap

// AllocEmbeddingMatrixOwned uses GC-owned storage on non-Linux platforms.
func AllocEmbeddingMatrixOwned(n, dim int) ([][]float32, func() error) {
	return AllocEmbeddingMatrix(n, dim), func() error { return nil }
}
