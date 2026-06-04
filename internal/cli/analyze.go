//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cli provides the command-line interface implementation for EULIX.
/*
This file is responsible for running of analyze command which auto runs
eulix_parser and eulix_embed python script by a local venv located at
$HOME/.Eulix/.venv (can be customized by eulix.toml) based on
config provided in eulix.toml
*/
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"eulix/internal/checksum"
	"eulix/internal/config"
)

// getVenvPython validates the venv and returns the python binary path and a modified environment.
func getVenvPython(venvPath string) (string, []string, error) {
	// Point directly to the python executable, not the activate script
	pythonPath := filepath.Join(venvPath, "bin", "python")

	if _, err := os.Stat(pythonPath); err != nil {
		return "", nil, fmt.Errorf("venv python not found at %s: %w", pythonPath, err)
	}

	// Check Python version - This will now work perfectly
	cmd := exec.Command(pythonPath, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", nil, fmt.Errorf("failed to check python version: %w", err)
	}
	versionStr := strings.TrimSpace(string(out))

	if !strings.HasPrefix(versionStr, "Python 3.10") && !strings.HasPrefix(versionStr, "Python 3.11") {
		return "", nil, fmt.Errorf("unsupported Python version: %s (want 3.10 or 3.11)", versionStr)
	}

	// Build modified environment: prepend venv bin to PATH
	venvBin := filepath.Join(venvPath, "bin")
	env := os.Environ()
	newEnv := make([]string, 0, len(env)+1)
	foundPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			newEnv = append(newEnv, "PATH="+venvBin+string(os.PathListSeparator)+e[5:])
			foundPath = true
		} else {
			newEnv = append(newEnv, e)
		}
	}
	if !foundPath {
		newEnv = append(newEnv, "PATH="+venvBin)
	}
	return pythonPath, newEnv, nil
}

func analyzeProject(projectPath string) error {
	startTime := time.Now()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Venv activation & verification
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	venvPath := filepath.Join(homeDir, ".Eulix", ".venv")
	pythonPath, venvEnv, err := getVenvPython(venvPath)
	if err != nil {
		return fmt.Errorf("virtual environment setup failed: %w", err)
	}
	fmt.Printf("Using Python venv: %s (interpreter: %s)\n", venvPath, pythonPath)

	eulixDir := filepath.Join(projectPath, ".eulix")

	// Calculate checksum
	detector := checksum.HashHound(projectPath)
	currentChecksum, err := detector.Calculate()
	if err != nil {
		return fmt.Errorf("checksum calculation failed: %w", err)
	}

	// Run parser
	fmt.Println("Parsing codebase...")
	kbPath := filepath.Join(eulixDir, "kb.json")
	parserCmd := exec.Command("eulix_parser",
		"--root", projectPath,
		"-o", kbPath,
		"--threads", fmt.Sprintf("%d", cfg.Parser.Threads),
	)
	parserCmd.Stdout = os.Stdout
	parserCmd.Stderr = os.Stderr
	if err := parserCmd.Run(); err != nil {
		return fmt.Errorf("parser failed: %w", err)
	}
	fmt.Println("✓ Parser completed")
	fmt.Println()

	// Generate embeddings via ~/.Eulix/eulix_embed.py
	fmt.Println("Generating embeddings...")
	embedScriptPath := filepath.Join(homeDir, ".Eulix", "eulix_embed.py")
	if _, err := os.Stat(embedScriptPath); os.IsNotExist(err) {
		return fmt.Errorf("embed script not found: %s", embedScriptPath)
	}

	embeddingsPath := filepath.Join(eulixDir)
	embedCmd := exec.Command(pythonPath, embedScriptPath,
		"embed",
		"-k", kbPath,
		"-o", embeddingsPath,
		"-m", cfg.Embeddings.Model,
	)
	embedCmd.Stdout = os.Stdout
	embedCmd.Stderr = os.Stderr
	embedCmd.Env = venvEnv
	if err := embedCmd.Run(); err != nil {
		return fmt.Errorf("embedding generation failed: %w", err)
	}
	fmt.Println("   ✓ Embeddings completed")
	fmt.Println()

	// Save checksum
	fmt.Println("Saving checksum...")
	if err := detector.Save(currentChecksum); err != nil {
		return fmt.Errorf("failed to save checksum: %w", err)
	}
	fmt.Println("   ✓ Checksum saved")
	fmt.Println()

	duration := time.Since(startTime)
	fmt.Printf("Took %s\n", duration.Round(time.Second))
	fmt.Println()
	fmt.Println("Run 'eulix chat' to start querying your codebase!")
	return nil
}
