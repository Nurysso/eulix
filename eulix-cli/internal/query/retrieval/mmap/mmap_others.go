// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

//go:build !linux && !darwin && !windows

/*
Prevents mmap to happen on unsupported os
*/

package mmap

import (
	"bufio"
	"errors"
	"io"
	"os"
)

var errNoMmap = errors.New("mmap not supported on this platform")

func DecodeViaMmap(_ string, _ int64, _ any) error {
	return errNoMmap
}

func OpenForSequentialRead(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return bufio.NewReaderSize(f, jsonBufSize), func() { f.Close() }, nil
}

func AllocEmbeddingMatrix(n, dim int) *EmbeddingMatrix {
	flat := make([]float32, n*dim)
	return &EmbeddingMatrix{rows: makeRows(flat, n, dim)}
}
