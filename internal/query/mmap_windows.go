//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

//go:build windows

/*
[UNTESTED]
Responsible for mmap on windows
*/

package query

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/bytedance/sonic"
	"golang.org/x/sys/windows"
)

// decodeViaMmap maps path read-only via the Windows file-mapping API and
// decodes JSON directly from the view.  size must equal fi.Size() for the
// file — passing it in avoids a redundant stat syscall.
func decodeViaMmap(path string, size int64, v any) error {
	if sizeOverflows(size) {
		return errFileTooLarge(path, size)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if size == 0 {
		return fmt.Errorf("empty file: %s", path)
	}

	// CreateFileMapping — golang.org/x/sys/windows exposes the full Win32 API;
	// the deprecated syscall package lacks constants and unicode helpers.
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

	// sonicCopy copies all decoded strings out of data before UnmapViewOfFile.
	// Using sonic.ConfigDefault (CopyString: false) would leave string headers
	// pointing into the unmapped region — use-after-free.
	_ = sonic.ConfigDefault // referenced to keep the import visible in godoc
	return sonicCopy.Unmarshal(data, v)
}
