// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"eulix/internal/utils"
)

type closingEmbedder struct {
	closes int
	err    error
}

func (*closingEmbedder) EmbedQueryBinary(string) ([]float32, error) { return nil, nil }
func (e *closingEmbedder) Close() error                             { e.closes++; return e.err }

func TestCloseClosesEmbedderOnce(t *testing.T) {
	wantErr := errors.New("embedder close")
	embedder := &closingEmbedder{err: wantErr}
	cb := &ContextBuilder{queryEmbedder: embedder}
	if err := cb.Close(); !errors.Is(err, wantErr) {
		t.Errorf("Close error = %v, want %v", err, wantErr)
	}
	if err := cb.Close(); err != nil {
		t.Fatal(err)
	}
	if embedder.closes != 1 {
		t.Errorf("embedder closed %d times, want 1", embedder.closes)
	}
}

func TestLoadEmptyEmbeddings(t *testing.T) {
	for _, quantized := range []byte{0, 1} {
		t.Run(map[byte]string{0: "float32", 1: "sq8"}[quantized], func(t *testing.T) {
			cb := &ContextBuilder{eulixDir: t.TempDir()}
			f, err := os.Create(filepath.Join(cb.eulixDir, "embeddings.bin"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(utils.MagicBytes); err != nil {
				t.Fatal(err)
			}
			for _, v := range []uint32{utils.BinaryVersion, 0, 0, 2} {
				if err := binary.Write(f, binary.LittleEndian, v); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.Write([]byte{quantized}); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := cb.loadEmbeddings(); err != nil {
				t.Fatal(err)
			}
			if len(cb.embeddings) != 0 {
				t.Fatalf("loaded %d rows", len(cb.embeddings))
			}
		})
	}
}
