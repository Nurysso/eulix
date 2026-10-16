// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"eulix/internal/utils"
)

func writeReleaseFixture(t *testing.T, dir string, truncated bool) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, "embeddings.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(utils.MagicBytes); err != nil {
		t.Fatal(err)
	}
	for _, v := range []uint32{utils.BinaryVersion, 0, 1, 2} {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if !truncated {
		if err := binary.Write(f, binary.LittleEndian, []float32{1, 0}); err != nil {
			t.Fatal(err)
		}
	}
}

func mappedAddress(t *testing.T, addr uintptr) bool {
	t.Helper()
	data, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		var lo, hi uintptr
		if _, err := fmt.Sscanf(line, "%x-%x", &lo, &hi); err == nil && lo <= addr && addr < hi {
			return true
		}
	}
	return false
}

func TestCloseReleasesAnonymousEmbeddings(t *testing.T) {
	cb := &ContextBuilder{eulixDir: t.TempDir()}
	writeReleaseFixture(t, cb.eulixDir, false)
	if err := cb.loadEmbeddings(); err != nil {
		t.Fatal(err)
	}
	addr := uintptr(unsafe.Pointer(&cb.embeddings[0][0]))
	if !mappedAddress(t, addr) {
		t.Fatal("embedding memory is not mapped")
	}
	if err := cb.Close(); err != nil {
		t.Fatal(err)
	}
	if mappedAddress(t, addr) {
		t.Fatal("embedding mapping survived Close")
	}
	if err := cb.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(cb)
}

func TestFailedEmbeddingLoadClearsAllocation(t *testing.T) {
	cb := &ContextBuilder{eulixDir: t.TempDir()}
	writeReleaseFixture(t, cb.eulixDir, true)
	if err := cb.loadEmbeddings(); err == nil {
		t.Fatal("expected truncated payload error")
	}
	if cb.embeddings != nil {
		t.Fatal("failed load retained embedding allocation")
	}
}
