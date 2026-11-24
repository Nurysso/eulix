// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

//go:build !unix && !windows

/*
Prevents mmap to happen on unsupported os
*/

package mmap

import (
	"errors"
)

var errNoMmap = errors.New("mmap not supported on this platform")

func AllocEmbeddingMatrix(n, dim int) [][]float32 {
	flat := make([]float32, n*dim)
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = flat[i*dim : (i+1)*dim]
	}
	return rows
}

func mapFile(_ string) ([]byte, func(), error) {
	return nil, nil, errNoMmap
}
