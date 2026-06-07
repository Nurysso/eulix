//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cli provides the command-line interface implementation for EULIX.

/*
This file is responsible for the init command whcih is responsible
for marking the project/folder ready to be used by EULIX
OFC this can be skipped by manually calling parser and embedder
but its best if we only use them seprately only for testing and
developing purpose.

If testing and developing chat/retrival (context window creation)
use the checksum subcommand so that the latest metadata+embeddings
is checked before retrival
*/

package cli

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"eulix/internal/config"

	"github.com/BurntSushi/toml"
)

const (
	eulixDir     = ".eulix"
	euignorePath = ".euignore"
	configPath   = "eulix.toml"
)

type initState struct {
	hasConfig   bool
	hasDir      bool
	hasEuignore bool
}

func checkInitState() initState {
	s := initState{}
	_, err := os.Stat(configPath)
	s.hasConfig = err == nil
	_, err = os.Stat(eulixDir)
	s.hasDir = err == nil
	_, err = os.Stat(euignorePath)
	s.hasEuignore = err == nil
	return s
}

func (s initState) fullyInitialized() bool {
	return s.hasConfig && s.hasDir && s.hasEuignore
}

func (s initState) missing() []string {
	var m []string
	if !s.hasConfig {
		m = append(m, configPath+" (configuration)")
	}
	if !s.hasDir {
		m = append(m, eulixDir+"/ (knowledge base directory)")
	}
	if !s.hasEuignore {
		m = append(m, euignorePath+" (ignore patterns)")
	}
	return m
}

// initializeProject accepts a force boolean and an optional list of specific
// components to create (nil = create all missing).
func initializeProject(force bool, targets []string) error {
	state := checkInitState()

	// --- Already fully initialized ---
	if state.fullyInitialized() && !force {
		fmt.Println("Eulix is already initialized.")
		fmt.Println("\nUse --force / -f to reset and overwrite all files.")
		return nil
	}

	// --- Partially initialized: warn and prompt ---
	if !force && (state.hasConfig || state.hasDir || state.hasEuignore) {
		if missing := state.missing(); len(missing) > 0 {
			fmt.Println("Eulix is partially initialized. The following components are missing:")
			for _, m := range missing {
				fmt.Printf("  - %s\n", m)
			}
			fmt.Println("\nOptions:")
			fmt.Println("  • Run 'eulix init --fix' to create only the missing components")
			fmt.Println("  • Run 'eulix init --force' to reset and recreate everything")
			return nil
		}
	}

	// --- Determine what to write ---
	writeAll := force || (!state.hasConfig && !state.hasDir && !state.hasEuignore)
	shouldWrite := func(name string) bool {
		if writeAll || targets == nil {
			return true
		}
		for _, t := range targets {
			if strings.EqualFold(t, name) {
				return true
			}
		}
		return false
	}

	var created []string

	// 1. Knowledge base directory
	if shouldWrite("dir") || !state.hasDir {
		if err := os.MkdirAll(eulixDir, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", eulixDir, err)
		}
		created = append(created, fmt.Sprintf("  - %-20s (knowledge base directory)", eulixDir+"/"))
	}

	// 2. .euignore
	if (shouldWrite("euignore") || !state.hasEuignore) && (!state.hasEuignore || force) {
		defaultIgnore := "# Eulix ignore patterns\n" +
			"node_modules/\n" +
			".git/\n" +
			"*.test.go\n" +
			"vendor/\n" +
			"dist/\n" +
			"test/\n" +
			"build/\n"
		if err := os.WriteFile(euignorePath, []byte(defaultIgnore), 0644); err != nil {
			return fmt.Errorf("failed to create %s: %w", euignorePath, err)
		}
		created = append(created, fmt.Sprintf("  - %-20s (ignore patterns)", euignorePath))
	}

	// 3. eulix.toml serialized from DefaultConfig() so it never drifts
	if (shouldWrite("config") || !state.hasConfig) && (!state.hasConfig || force) {
		if err := writeDefaultConfig(configPath); err != nil {
			return fmt.Errorf("failed to create config: %w", err)
		}
		created = append(created, fmt.Sprintf("  - %-20s (configuration)", configPath))
	}

	// --- Feedback ---
	if force {
		fmt.Println("Eulix configuration has been reset!")
	} else {
		fmt.Println("Eulix initialized successfully!")
	}
	if len(created) > 0 {
		fmt.Println("\nCreated/Updated:")
		for _, c := range created {
			fmt.Println(c)
		}
	}
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Edit eulix.toml to configure your setup")
	fmt.Println("  2. Run 'eulix analyze' to analyze your codebase")
	fmt.Println("  3. Run 'eulix chat' to start querying")
	return nil
}

// writeDefaultConfig serializes DefaultConfig() to TOML at dst.
// It uses BurntSushi/toml's encoder so the output is always in sync
// with the actual Config struct no hardcoded strings to maintain.
func writeDefaultConfig(dst string) error {
	cfg := config.DefaultConfig()
	var buf bytes.Buffer
	buf.WriteString("# Eulix Configuration\n\n")
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	return os.WriteFile(dst, buf.Bytes(), 0644)
}
