//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package assets

import (
	"embed"
	"syscall"
)

// parserBin holds the Windows (PE) eulix_parser.exe binary. Only compiled
// into windows builds.
//
//go:embed bins/eulix_parser_windows.exe
var parserBin embed.FS

const parserBinFile = "bins/eulix_parser_windows.exe"
const parserBinDestName = "eulix_parser.exe"

func parserBinaryBytes() ([]byte, string, error) {
	content, err := parserBin.ReadFile(parserBinFile)
	if err != nil {
		return nil, "", err
	}
	return content, parserBinDestName, nil
}

func setHiddenAttribute(path string) error {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return syscall.SetFileAttributes(pointer, syscall.FILE_ATTRIBUTE_HIDDEN)
}
