// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CheckEulixDir refuses a .eulix folder that holds anything but regular files
// and folders. A cloned repository can ship this folder, and its files are read
// by path, so a symlink there could pull in a file from outside the repository.
func CheckEulixDir(eulixDir string) error {
	return filepath.WalkDir(eulixDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%s is a symlink or special file, which Eulix does not read; remove it and run 'eulix analyze' if needed", path)
		}
		return nil
	})
}

// OpenEulixFile opens rel inside eulixDir for writing, creating its folders.
// os.Root refuses links that leave the folder, so a log shipped as a symlink
// cannot overwrite a file elsewhere, even if it appears after CheckEulixDir ran.
func OpenEulixFile(eulixDir, rel string, flag int) (*os.File, error) {
	if eulixDir == "" {
		return nil, errors.New("no .eulix folder to write to")
	}
	parent, err := os.OpenRoot(filepath.Dir(eulixDir))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()

	base := filepath.Base(eulixDir)
	if err := parent.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	root, err := parent.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return nil, err
	}
	return root.OpenFile(rel, flag, 0o644)
}
