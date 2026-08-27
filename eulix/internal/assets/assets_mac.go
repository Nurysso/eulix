//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package utils provides Shared type and func accross project

//go:build darwin

package assets

import "embed"

// parserBin holds the macOS (Mach-O) eulix_parser binary. Only compiled
// into darwin builds. this bin was build using cargo zigbuild --release --target aarch64-apple-darwin
// so may not work perfectly
//
//go:embed bins/eulix_parser_darwin
var parserBin embed.FS

const parserBinFile = "bins/eulix_parser_darwin"
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
