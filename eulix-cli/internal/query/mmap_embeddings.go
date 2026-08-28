//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

// this file exists for experimentation and currenlty isnt used anywhere
// in the code base

// nolint:unused
package query

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

const (
	eulxMagic     = "EULX"
	binaryVersion = 5
)

type EmbeddingMeta struct {
	ModelName string
	Count     int
	Dim       int
	Quantized bool
}

func loadEmbeddingsBin(path string) ([][]float32, EmbeddingMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, EmbeddingMeta{}, err
	}
	defer func() { _ = f.Close() }()

	magic := make([]byte, 4)
	if _, err := f.Read(magic); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read magic: %w", err)
	}
	if string(magic) != eulxMagic {
		return nil, EmbeddingMeta{}, fmt.Errorf("bad magic: %q", magic)
	}

	var version uint32
	if err := binary.Read(f, binary.LittleEndian, &version); err != nil {
		return nil, EmbeddingMeta{}, fmt.Errorf("read version: %w", err)
	}
	if version != binaryVersion {
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

	// allocate the matrix (hugepage-aligned on Linux, flat on others)
	matrix := allocEmbeddingMatrix(int(count), int(dim))

	if !quantized {
		// float32 path: read the entire payload into the flat backing buffer
		// matrix[0] is the start of the contiguous allocation
		flat := matrix[0][:int(count)*int(dim)]
		if err := binary.Read(f, binary.LittleEndian, flat); err != nil {
			return nil, meta, fmt.Errorf("read vectors: %w", err)
		}
	} else {
		// SQ8 path: decode scale + int8 per embedding
		for i := 0; i < int(count); i++ {
			var scale float32
			if err := binary.Read(f, binary.LittleEndian, &scale); err != nil {
				return nil, meta, fmt.Errorf("embedding %d scale: %w", i, err)
			}
			quantized := make([]int8, dim)
			if err := binary.Read(f, binary.LittleEndian, quantized); err != nil {
				return nil, meta, fmt.Errorf("embedding %d data: %w", i, err)
			}
			for j, q := range quantized {
				matrix[i][j] = float32(q) * scale / math.MaxInt8
			}
		}
	}

	return matrix, meta, nil
}
