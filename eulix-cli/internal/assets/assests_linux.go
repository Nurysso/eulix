// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package utils provides Shared type and func across project

//go:build linux

package assets

import "embed"

// parserBin holds the Linux (ELF) eulix_parser binary. Only compiled into
// linux builds.
//
//go:embed bins/eulix_parser_linux
var parserBin embed.FS

const (
	parserBinFile     = "bins/eulix_parser_linux"
	parserBinDestName = "eulix_parser"
)

func parserBinaryBytes() ([]byte, string, error) {
	content, err := parserBin.ReadFile(parserBinFile)
	if err != nil {
		return nil, "", err
	}
	return content, parserBinDestName, nil
}

func setHiddenAttribute(path string) error {
	return nil
}
