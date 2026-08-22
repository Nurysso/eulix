//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Package embedded holds the OS-specific binaries that ship inside the eulix
// executable.  Callers must not import this package directly; use the helper
// functions below to extract a binary into a temp directory and run it.
//
// Directory layout expected at build time:
//
//	embedded/
//	  bins/
//	    eulix_parser        ← Linux/macOS (ELF / Mach-O)
//	    eulix_parser.exe    ← Windows PE
//	    eulix_embed         ← Linux/macOS standalone binary
//	    eulix_embed.exe     ← Windows standalone binary
package embedded

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Bins holds every file under the bins/ sub-directory.
// The glob must match the filenames committed to the repository.
//
//go:embed bins/eulix_parser bins/eulix_parser.exe bins/eulix_embed
var Bins embed.FS

// extractOnce guards the per-process temp directory so we only unpack each
// binary once even when multiple goroutines race to use them.
var (
	extractMu   sync.Mutex
	extractedTo string // set after first successful extraction
)

// parserBinName returns the embedded path for the parser binary on the
// current OS.
func parserBinName() string {
	if runtime.GOOS == "windows" {
		return "bins/eulix_parser.exe"
	}
	return "bins/eulix_parser"
}

// embedBinName returns the embedded path for the standalone embed binary on
// the current OS.
func embedBinName() string {
	if runtime.GOOS == "windows" {
		return "bins/eulix_embed.exe"
	}
	return "bins/eulix_embed"
}

// ExtractAll unpacks every embedded binary into a private temp directory once
// per process and returns the directory path.  Subsequent calls return the
// same path without re-extracting.
func ExtractAll() (string, error) {
	extractMu.Lock()
	defer extractMu.Unlock()

	if extractedTo != "" {
		return extractedTo, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}

	dir := filepath.Join(homeDir, ".cache", "eulix", "bins")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create bin cache dir: %w", err)
	}

	for _, embedded := range []string{parserBinName(), embedBinName()} {
		if err := extractOne(dir, embedded); err != nil {
			return "", err
		}
	}

	extractedTo = dir
	return dir, nil
}

// ParserPath returns the on-disk path of the extracted eulix_parser binary,
// extracting it first if necessary.
func ParserPath() (string, error) {
	dir, err := ExtractAll()
	if err != nil {
		return "", err
	}
	name := filepath.Base(parserBinName())
	return filepath.Join(dir, name), nil
}

// EmbedBinPath returns the on-disk path of the extracted eulix_embed binary,
// extracting it first if necessary.
func EmbedBinPath() (string, error) {
	dir, err := ExtractAll()
	if err != nil {
		return "", err
	}
	name := filepath.Base(embedBinName())
	return filepath.Join(dir, name), nil
}

// extractOne copies a single file from the embedded FS to dir, preserving
// only the base filename.  The file is made executable (0755 on Unix).
func extractOne(dir, embeddedPath string) error {
	destName := filepath.Base(embeddedPath)
	destPath := filepath.Join(dir, destName)

	// Read embedded content first so we can compare.
	content, err := Bins.ReadFile(embeddedPath)
	if err != nil {
		return fmt.Errorf("open embedded binary %q: %w", embeddedPath, err)
	}

	// If the file already exists and is the same size, skip extraction.
	// A size check is cheap and catches the common "already extracted" case.
	if info, err := os.Stat(destPath); err == nil {
		if info.Size() == int64(len(content)) {
			return nil // already extracted, skip
		}
	}

	if err := os.WriteFile(destPath, content, 0755); err != nil {
		return fmt.Errorf("write extracted binary %q: %w", destPath, err)
	}
	return nil
}
