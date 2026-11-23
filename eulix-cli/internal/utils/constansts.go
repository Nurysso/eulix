// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package utils provides Shared type and func across project
package utils

const (
	AppName          = "eulix"
	AppVersion       = "v0.10.0"
	BinaryVersion    = uint32(5)
	ChecksumFileName = "checksum.fb.zst"
	ConfigPath       = "eulix.toml"
	EmbedZipPath     = "bins/eulix-embed.zip"
	EnvPath          = ".env"
	EuignorePath     = ".euignore"
	ignoreFileName   = ".euignore"
	EulixDir         = ".eulix"
	EulixGlobalDir   = ".Eulix"
	HashNameEmbedDir = "eulix_embed"
	HashNameParser   = "eulix_parser"
	// KBFormatVersion is the on-disk format of the parser outputs in .eulix/.
	// It is recorded in the analysis marker, so a folder written with another
	// format is re-analyzed instead of misread. Bump it whenever the schema in
	// schema/*.fbs or the set of FlatBuffers outputs changes.
	KBFormatVersion     = 1
	MagicBytes          = "EULX"
	ParserSubDir        = "bin"
	RequiredPythonMajor = 3
	RequiredPythonMinor = 11
	ScriptsSubDir       = "eulix_embed"
	VenvSubDir          = ".venv"
)
