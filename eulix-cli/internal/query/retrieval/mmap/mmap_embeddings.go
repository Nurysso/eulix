// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build !ignore

// Package mmap hold os specific mmap helper code.
// this file exists for experimentation and currently isn't used anywhere
// in the code base
package mmap

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"eulix/internal/utils"
)

type EmbeddingMeta struct {
	ModelName string
	Count     int
	Dim       int
	Quantized bool
}

// EmbeddingMatrix owns an mmap'd slab and the row-slice view over it.
// Call Close when the matrix is no longer needed; the GC will NOT release
// the underlying anonymous mapping on its own.
type EmbeddingMatrix struct {
	rows [][]float32 // public view
	raw  []byte      // non-nil for mmap'd allocations
}

func (m *EmbeddingMatrix) Len() int            { return len(m.rows) }
func (m *EmbeddingMatrix) Row(i int) []float32 { return m.rows[i] }
func (m *EmbeddingMatrix) Rows() [][]float32   { return m.rows }

// nolint: unused
func makeRows(flat []float32, n, dim int) [][]float32 {
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = flat[i*dim : (i+1)*dim]
	}
	return rows
}

func loadEmbeddingsBin(path string) (*EmbeddingMatrix, EmbeddingMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, EmbeddingMeta{}, err
	}
	defer func() { _ = f.Close() }()

	magic := make([]byte, 4)
	if _, err := f.Read(magic); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read magic: %w", err)
	}
	if string(magic) != utils.MagicBytes {
		return nil, EmbeddingMeta{}, fmt.Errorf("bad magic: %q", magic)
	}

	var version uint32
	if err := binary.Read(f, binary.LittleEndian, &version); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read version: %w", err)
	}
	if version != utils.BinaryVersion {
		return nil, EmbeddingMeta{}, fmt.Errorf("unsupported version %d", version)
	}

	// model name: uint32 len + UTF-8 bytes
	var nameLen uint32
	if err := binary.Read(f, binary.LittleEndian, &nameLen); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read model name len: %w", err)
	}
	nameBytes := make([]byte, nameLen)
	if _, err := f.Read(nameBytes); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read model name: %w", err)
	}

	var count, dim uint32
	if err := binary.Read(f, binary.LittleEndian, &count); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read count: %w", err)
	}
	if err := binary.Read(f, binary.LittleEndian, &dim); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read dim: %w", err)
	}

	var quantFlag [1]byte
	if _, err := f.Read(quantFlag[:]); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read quant flag: %w", err)
	}
	quantized := quantFlag[0] == 1

	meta := EmbeddingMeta{
		ModelName: string(nameBytes),
		Count:     int(count),
		Dim:       int(dim),
		Quantized: quantized,
	}

	matrix := AllocEmbeddingMatrix(int(count), int(dim)) // single return value on all platforms
	rows := matrix.Rows()

	if !quantized {
		flat := rows[0][:int(count)*int(dim)]
		if err := binary.Read(f, binary.LittleEndian, flat); err != nil {
			_ = matrix.Close()
			return nil, meta, fmt.Errorf("read vectors: %w", err)
		}
	} else {
		// SQ8 path: decode scale + int8 per embedding
		for i := 0; i < int(count); i++ {
			var scale float32
			if err := binary.Read(f, binary.LittleEndian, &scale); err != nil {
				_ = matrix.Close()
				return nil, meta, fmt.Errorf("embedding %d scale: %w", i, err)
			}
			q8 := make([]int8, dim)
			if err := binary.Read(f, binary.LittleEndian, q8); err != nil {
				_ = matrix.Close()
				return nil, meta, fmt.Errorf("embedding %d data: %w", i, err)
			}
			for j, q := range q8 {
				rows[i][j] = float32(q) * scale / math.MaxInt8
			}
		}
	}

	return matrix, meta, nil
}
