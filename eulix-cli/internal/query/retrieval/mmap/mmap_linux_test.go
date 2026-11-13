// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package mmap

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// residentFileKiB returns this process's resident file-backed pages in KiB:
// RssFile, plus RssShmem because a mapped tmpfs file (TMPDIR is often tmpfs)
// is counted there.
func residentFileKiB(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Skipf("no /proc/self/status: %v", err)
	}
	total, found := 0, 0
	for _, line := range strings.Split(string(b), "\n") {
		for _, key := range []string{"RssFile:", "RssShmem:"} {
			rest, ok := strings.CutPrefix(line, key)
			if !ok {
				continue
			}
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			n, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatalf("parsing %q: %v", line, err)
			}
			total += n
			found++
		}
	}
	if found != 2 {
		t.Skip("RssFile/RssShmem not reported by this kernel")
	}
	return total
}

// MAP_POPULATE made the mmap call read the whole file before the parse could
// start, for no speed gain. Right after mapping, almost none of the file may
// be resident in this process.
//
// Not parallel: other tests in this package map files too.
func TestMmapPlatformDoesNotPrefault(t *testing.T) {
	const size = 64 << 20
	path := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	before := residentFileKiB(t)
	data, err := mmapPlatform(f, size)
	if err != nil {
		t.Fatalf("mmapPlatform: %v", err)
	}
	defer func() { _ = unix.Munmap(data) }()
	grew := residentFileKiB(t) - before

	if grew > 8<<10 { // 8 MiB, in KiB
		t.Fatalf("resident file pages grew by %d KiB right after mapping a %d MiB file: mmapPlatform pre-faults the mapping", grew, size>>20)
	}
}
