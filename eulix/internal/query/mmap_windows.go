//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

//go:build windows

/*
[UNTESTED]
Windows mmap implementation.

	FILE_FLAG_SEQUENTIAL_ONLY at file-open time: tells the NTFS /
	ReFS filesystem layer to readahead aggressively. Without it,
	the FS readahead is generic (~256 KB) and sonic out-runs it
	within the first megabyte of a 4 GB parse.

	PrefetchVirtualMemory (Win 8+) after MapViewOfFile: the
	closest equivalent to Linux's MADV_SEQUENTIAL. We pass the
	full mapping range so the OS can fault in the first ~MB of
	pages before sonic touches them, eliminating the page-fault
	latency spike.

SEC_LARGE_PAGES on CreateFileMapping would enable 2 MB large
pages, but it requires SeLockMemoryPrivilege (not granted to most
user processes). Skipped by default.

Reference: https://learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-prefetchvirtualmemory
*/

package query

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const FILE_FLAG_SEQUENTIAL_ONLY = 0x08000000

type Win32MemoryRangeEntry struct {
	VirtualAddress uintptr
	NumberOfBytes  uintptr
}

func prefetchvirtualmemory(process windows.Handle, count uint32, entries *Win32MemoryRangeEntry, flags uint32) error {
	mod := windows.NewLazyDLL("kernel32.dll")
	proc := mod.NewProc("PrefetchVirtualMemory")
	ret, _, err := proc.Call(
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

// openSeqyentialWindows opens path with FILE_FLAG_SEQUENTIAL_ONLY so
// the fs readhead matches access patter. Returns an
// *os.File whose Close() release the underlying Win32 handle.
func openSeqyentialWindows(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_ATTRIBUTE_NORMAL,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|FILE_FLAG_SEQUENTIAL_ONLY,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

// decodeViaMmap maps path read-only via the Windows file-mapping API and
// decodes JSON directly from the view.  size must equal fi.Size() for the
// file — passing it in avoids a redundant stat syscall.
func decodeViaMmap(path string, size int64, v any) error {
	if sizeOverflows(size) {
		return errFileTooLargeForPath(path, size)
	}

	// Use sequential scan open so the FS readhead is tuned
	f, err := openSeqyentialWindows(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Close the stat-then mmap TOCTOU windows
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size = fi.Size()
	if sizeOverflows(size) {
		return errFileTooLargeForPath(path, size)
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

	data := unsafe.Slice((*byte)(unsafe.Pointer(addr)), size)

	memRange := Win32MemoryRangeEntry{
		VirtualAddress: addr,
		NumberOfBytes:  uintptr(size),
	}
	_ = prefetchvirtualmemory(windows.CurrentProcess(), 1, &memRange, 0)
	return sonicCopy.Unmarshal(data, v)
}

// mmapForSequentialRead Windows supports the io.Reader variant
// returning a bytes.Reader over the mmap'd view, but the
// caller must use the FILE_FLAG_SEQUENTIAL_ONLY open path to get
// the FS readahead hint. The returned cleanup releases both the
// view and the mapping object handle.
func mmapForSequentialRead(path string, size int64) (io.Reader, func(), error) {
	f, err := openSeqyentialWindows(path)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	size = fi.Size()
	if sizeOverflows(size) || size == 0 {
		f.Close()
		return nil, nil, fmt.Errorf("mmapForSequentialRead: invalid size %d for %s", size, path)
	}

	h, err := windows.CreateFileMapping(
		windows.Handle(f.Fd()),
		nil,
		windows.PAGE_READONLY,
		0, 0,
		nil,
	)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("CreateFileMapping: %w", err)
	}

	addr, err := windows.MapViewOfFile(h, windows.FILE_MAP_READ, 0, 0, 0)
	if err != nil {
		windows.CloseHandle(h)
		f.Close()
		return nil, nil, fmt.Errorf("MapViewOfFile: %w", err)
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(addr)), size)

	// Use a different name – 'range' is a keyword
	memRange := Win32MemoryRangeEntry{
		VirtualAddress: addr,
		NumberOfBytes:  uintptr(size),
	}
	// Prefetch the memory (ignore error or handle as needed)
	_ = prefetchvirtualmemory(windows.CurrentProcess(), 1, &memRange, 0)

	// Close the file handle – the mapping holds its own reference
	_ = f.Close()

	return bytes.NewReader(data), func() {
		_ = windows.UnmapViewOfFile(addr)
		_ = windows.CloseHandle(h)
	}, nil
}

func allocEmbeddingMatrix(n, dim int) [][]float32 {
	flat := make([]float32, n*dim)
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = flat[i*dim : (i+1)*dim]
	}
	return rows
}
