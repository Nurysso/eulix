//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package utils provides Shared type and func accross project

//go:build linux

package assets

import "embed"

// parserBin holds the Linux (ELF) eulix_parser binary. Only compiled into
// linux builds.
//
//go:embed bins/eulix_parser_linux
var parserBin embed.FS

const parserBinFile = "bins/eulix_parser_linux"
const parserBinDestName = "eulix_parser"

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
