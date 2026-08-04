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
	"time"

	"eulix/internal/checksum"
	"eulix/internal/config"
	"eulix/internal/embeddings"
)

func analyzeProject(projectPath string) error {
	startTime := time.Now()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	// Resolve venv interpreter and environment using the shared helper.
	venvPath := filepath.Join(homeDir, ".Eulix", ".venv")
	pythonPath, venvEnv, err := embeddings.GetVenvPython(venvPath)
	if err != nil {
		return fmt.Errorf("virtual environment setup failed: %w", err)
	}
	fmt.Printf("Using Python venv: %s (interpreter: %s)\n", venvPath, pythonPath)

	eulixDir := filepath.Join(projectPath, ".eulix")

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
		"--prism", fmt.Sprintf("%d", cfg.Parser.PrismV),
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
		"--quantize",
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
