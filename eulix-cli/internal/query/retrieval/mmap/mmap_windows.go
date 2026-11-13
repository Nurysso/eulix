// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mmap hold os specific mmap helper code.

//go:build windows

/*
Windows mmap implementation.

	FILE_FLAG_SEQUENTIAL_SCAN at file-open time: a read-ahead hint for
	the cache manager, which serves ReadFile. Page faults on a mapped
	view don't go through the cache manager's read-ahead, so this does
	little for DecodeViaMmap. It is kept because it is harmless and
	removing it hasn't been measured on Windows.

	PrefetchVirtualMemory (Win 8+) after MapViewOfFile: asks Windows to
	read the whole mapped range in large I/Os before sonic starts, so
	the parse doesn't fault the file in page by page. DecodeViaMmap
	parses every byte anyway, so prefetching all of it adds no memory.

SEC_LARGE_PAGES on CreateFileMapping would enable 2 MB large
pages, but it requires SeLockMemoryPrivilege (not granted to most
user processes). Skipped by default.

Reference: https://learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-prefetchvirtualmemory
*/

package mmap

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procPrefetchVirtualMemory is resolved once, on first use, from the
// system directory's kernel32.dll.
var procPrefetchVirtualMemory = windows.NewLazySystemDLL("kernel32.dll").NewProc("PrefetchVirtualMemory")

// memoryRangeEntry mirrors WIN32_MEMORY_RANGE_ENTRY.
type memoryRangeEntry struct {
	VirtualAddress uintptr
	NumberOfBytes  uintptr
}

// prefetchVirtualMemory is best effort. Find is checked first so a missing
// export returns an error instead of panicking inside Call.
func prefetchVirtualMemory(process windows.Handle, count uint32, entries *memoryRangeEntry, flags uint32) error {
	if err := procPrefetchVirtualMemory.Find(); err != nil {
		return err
	}
	ret, _, err := procPrefetchVirtualMemory.Call(
		uintptr(process),
		uintptr(count),
		uintptr(unsafe.Pointer(entries)),
		uintptr(flags),
	)
	if ret == 0 {
		return err
	}
	return nil
}

// openSequentialWindows opens path with FILE_FLAG_SEQUENTIAL_SCAN and
// returns an *os.File whose Close() releases the underlying Win32 handle.
// FILE_SHARE_READ only: a writer (eulix analyze) is refused while we decode,
// and a mapped view blocks truncation. Won't fix: RET-10 in
// docs/Known-Issues/Retrieval-And-Query.md.
func openSequentialWindows(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_SEQUENTIAL_SCAN,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

// viewBytes exposes a mapped view as a slice. MapViewOfFile returns the address
// as a uintptr; it points outside the Go heap, so the GC never moves it.
func viewBytes(addr uintptr, size int64) []byte {
	return unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&addr))), size)
}

// DecodeViaMmap maps path read-only via the Windows file-mapping API and
// decodes JSON directly from the view. size comes from the caller's os.Stat;
// the open handle is re-stat'd, and the live size is what gets mapped.
func DecodeViaMmap(path string, size int64, v any) error {
	if SizeOverflows(size) {
		return ErrFileTooLargeForPath(path, size)
	}

	f, err := openSequentialWindows(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Close the stat-then mmap TOCTOU windows
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size = fi.Size()
	if SizeOverflows(size) {
		return ErrFileTooLargeForPath(path, size)
	}
	if size == 0 {
		return fmt.Errorf("empty file:%s", path)
	}

	h, err := windows.CreateFileMapping(
		windows.Handle(f.Fd()),
		nil,
		windows.PAGE_READONLY,
		0, 0, // map the whole file
		nil,
	)
	if err != nil {
		return fmt.Errorf("CreateFileMapping: %w", err)
	}
	defer windows.CloseHandle(h)

	addr, err := windows.MapViewOfFile(h, windows.FILE_MAP_READ, 0, 0, 0)
	if err != nil {
		return fmt.Errorf("MapViewOfFile: %w", err)
	}
	defer windows.UnmapViewOfFile(addr)

	data := viewBytes(addr, size)

	memRange := memoryRangeEntry{
		VirtualAddress: addr,
		NumberOfBytes:  uintptr(size),
	}
	_ = prefetchVirtualMemory(windows.CurrentProcess(), 1, &memRange, 0)
	return SonicCopy.Unmarshal(data, v)
}

func AllocEmbeddingMatrix(n, dim int) [][]float32 {
	flat := make([]float32, n*dim)
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = flat[i*dim : (i+1)*dim]
	}
	return rows
}
