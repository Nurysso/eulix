//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cli provides the command-line interface implementation for EULIX.
/*
This file is responsible for running of analyze command which auto runs
eulix_parser and eulix_embed based on config provided in eulix.toml.

eulix_parser is always run from the binary embedded inside the eulix executable.

eulix_embed is run in one of two modes depending on config.Project.Embedis:
  - "script" → uses the Python venv at $HOME/.Eulix/.venv and the script at
    $HOME/.Eulix/eulix_embed/main.py  (original behaviour)
  - "bin"    → uses the eulix_embed binary embedded inside the eulix executable
*/
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	a "eulix/internal/assets"
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

	eulixDir := filepath.Join(projectPath, ".eulix")

	detector := checksum.HashHound(projectPath)
	currentChecksum, err := detector.Calculate()
	if err != nil {
		return fmt.Errorf("checksum calculation failed: %w", err)
	}

	// Eulix Parser (always uses embedded binary)
	fmt.Println("Parsing codebase...")

	parserBin, err := a.ParserPath()
	if err != nil {
		return fmt.Errorf("could not extract embedded parser: %w", err)
	}
	if cfg.Project.DebugConfig {
		fmt.Printf("Using embedded parser: %s\n", parserBin)
	}

	kbPath := filepath.Join(eulixDir, "kb.json")
	parseArgs := []string{
		"--root", projectPath,
		"-o", kbPath,
		"--threads", fmt.Sprintf("%d", cfg.Parser.Threads),
		"--prism", fmt.Sprintf("%d", cfg.Parser.PrismV),
	}
	if cfg.Parser.Verbose {
		parseArgs = append(parseArgs, "--verbose")
	}

	parserCmd := exec.Command(parserBin, parseArgs...)
	parserCmd.Stdout = os.Stdout
	parserCmd.Stderr = os.Stderr
	if err := parserCmd.Run(); err != nil {
		return fmt.Errorf("parser failed: %w", err)
	}
	fmt.Println("✓ Parser completed")
	fmt.Println()

	fmt.Println("Generating embeddings...")
	embeddingsPath := eulixDir
	// Original behaviour: use venv Python + eulix_embed/main.py script.
	venvPath := filepath.Join(homeDir, ".Eulix", ".venv")
	pythonPath, venvEnv, err := embeddings.GetVenvPython(venvPath)
	if err != nil {
		return fmt.Errorf("virtual environment setup failed: %w", err)
	}
	if cfg.Project.DebugConfig {
		fmt.Printf("Using Python venv: %s (interpreter: %s)\n", venvPath, pythonPath)
	}

	embedScriptPath := filepath.Join(homeDir, ".Eulix", "eulix_embed", "main.py")
	if _, err := os.Stat(embedScriptPath); os.IsNotExist(err) {
		return fmt.Errorf("embed script not found: %s", embedScriptPath)
	}

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
		return fmt.Errorf("embedding generation failed (script mode): %w", err)
	}

	fmt.Println("   ✓ Embeddings completed")
	fmt.Println()

	// Checksum save
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
