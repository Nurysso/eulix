//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later
// Package embeddings provides the command-line interface implementation for EULIX.

/*
This file is responsible for finding and executing python venv.
*/
package embeddings

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// venvPythonCandidates returns the platform-correct interpreter paths inside a venv.
func venvPythonCandidates(venvPath string) []string {
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(venvPath, "Scripts", "python.exe"),
			filepath.Join(venvPath, "Scripts", "python3.exe"),
		}
	}
	return []string{
		filepath.Join(venvPath, "bin", "python3"),
		filepath.Join(venvPath, "bin", "python"),
	}
}

// venvBinDir returns the platform-correct bin/Scripts directory for a venv.
func venvBinDir(venvPath string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvPath, "Scripts")
	}
	return filepath.Join(venvPath, "bin")
}

// GetVenvPython validates the venv at venvPath and returns the Python interpreter
// path and a venv-activated environment slice.
// Exported so the cli package can use it without duplicating the logic.
func GetVenvPython(venvPath string) (string, []string, error) {
	var pythonPath string
	for _, c := range venvPythonCandidates(venvPath) {
		if _, err := os.Stat(c); err == nil {
			pythonPath = c
			break
		}
	}
	if pythonPath == "" {
		return "", nil, fmt.Errorf("venv python not found in %s (tried: %s)",
			venvPath, strings.Join(venvPythonCandidates(venvPath), ", "))
	}

	out, err := exec.Command(pythonPath, "--version").Output()
	if err != nil {
		return "", nil, fmt.Errorf("failed to check python version: %w", err)
	}
	ver := strings.TrimSpace(string(out))
	if !strings.HasPrefix(ver, "Python 3.10") && !strings.HasPrefix(ver, "Python 3.11") {
		return "", nil, fmt.Errorf("unsupported Python version: %s (want 3.10 or 3.11)", ver)
	}

	// Prepend the venv bin/Scripts dir to PATH.
	binDir := venvBinDir(venvPath)
	env := os.Environ()
	newEnv := make([]string, 0, len(env)+1)
	foundPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			newEnv = append(newEnv, "PATH="+binDir+string(os.PathListSeparator)+e[5:])
			foundPath = true
		} else {
			newEnv = append(newEnv, e)
		}
	}
	if !foundPath {
		newEnv = append(newEnv, "PATH="+binDir)
	}
	return pythonPath, newEnv, nil
}

// FindEulixEmbed locates eulix_embed.py and resolves a usable Python interpreter
// from the canonical venv at ~/.Eulix/.venv.
// Exported so the cli package can reuse it if needed.
func FindEulixEmbed() (scriptPath, pythonPath string, venvEnv []string, err error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	script := filepath.Join(homeDir, ".Eulix", "eulix_embed.py")
	if _, err := os.Stat(script); err != nil {
		return "", "", nil, fmt.Errorf("eulix_embed.py not found (expected at %s)", script)
	}

	venvPath := filepath.Join(homeDir, ".Eulix", ".venv")
	python, env, err := GetVenvPython(venvPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("found embed script but no usable Python: %w", err)
	}
	return script, python, env, nil
}
