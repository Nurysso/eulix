//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package cli provides the command-line interface implementation for EULIX.

/*
This file is responsible for the init command which is responsible
for marking the project/folder ready to be used by EULIX
OFC this can be skipped by manually calling parser and embedder
*/

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"eulix/internal/cli/setup"
	"eulix/internal/config"

	"github.com/BurntSushi/toml"
)

const (
	eulixDir     = ".eulix"
	euignorePath = ".euignore"
	configPath   = "eulix.toml"
	envPath      = ".env"
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

// missingTargets returns short keys matching shouldWrite() in initializeProject.
func (s initState) missingTargets() []string {
	var m []string
	if !s.hasConfig {
		m = append(m, "config")
	}
	if !s.hasDir {
		m = append(m, "dir")
	}
	if !s.hasEuignore {
		m = append(m, "euignore")
	}
	return m
}

// missingDescriptions returns human-readable lines for printing to the user.
func (s initState) missingDescriptions() []string {
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
// components to create (nil = create all missing). Once files are written it
// walks the user through the interactive wizard for whichever of
// .euignore / eulix.toml were just (re)created.
func initializeProject(force bool, targets []string) error {
	state := checkInitState()

	if state.fullyInitialized() && !force {
		fmt.Println("Eulix is already initialized.")
		fmt.Println("\nUse --force / -f to reset and overwrite all files.")
		return nil
	}

	if !force && targets == nil && (state.hasConfig || state.hasDir || state.hasEuignore) {
		if missing := state.missingDescriptions(); len(missing) > 0 {
			fmt.Println("Eulix is partially initialized. The following components are missing:")
			for _, m := range missing {
				fmt.Printf("  - %s\n", m)
			}
			fmt.Println("\nOptions:")
			fmt.Println("  • Run 'eulix init fix' to create only the missing components")
			fmt.Println("  • Run 'eulix init force' to reset and recreate everything")
			return nil
		}
	}

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
	euignoreWritten := false
	configWritten := false

	if shouldWrite("dir") || !state.hasDir {
		if err := os.MkdirAll(eulixDir, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", eulixDir, err)
		}
		created = append(created, fmt.Sprintf("  - %-20s (knowledge base directory)", eulixDir+"/"))
	}

	// .euignore is written immediately so there's a real file for the wizard's
	if (shouldWrite("euignore") || !state.hasEuignore) && (!state.hasEuignore || force) {
		defaultIgnore := "# Eulix ignore patterns\n" +
			"*.test*\n" +
			"vendor/\n" +
			"dist/\n" +
			"test/\n" +
			"build/\n"
		if err := os.WriteFile(euignorePath, []byte(defaultIgnore), 0644); err != nil {
			return fmt.Errorf("failed to create %s: %w", euignorePath, err)
		}
		created = append(created, fmt.Sprintf("  - %-20s (ignore patterns)", euignorePath))
		euignoreWritten = true
	}

	// eulix.toml built in memory from DefaultConfig() first; the
	// wizard (if the user opts in) edits this struct.
	var cfg *config.Config
	if (shouldWrite("config") || !state.hasConfig) && (!state.hasConfig || force) {
		cfg = config.DefaultConfig()
		configWritten = true
	}

	// Interactive wizard for whatever was just (re)created
	env := setup.LoadEnvFile(envPath)
	if euignoreWritten {
		fmt.Println()
		err := setup.RunWizard(setup.BuildEuignoreSteps(euignorePath), cfg, env)
		if err != nil && !errors.Is(err, setup.ErrWizardCancelled) {
			return fmt.Errorf("euignore wizard: %w", err)
		}
	}

	if configWritten {
		fmt.Println()
		err := setup.RunWizard(setup.BuildConfigSteps(), cfg, env)
		switch {
		case errors.Is(err, setup.ErrWizardCancelled):
			fmt.Println("\neulix.toml was not written (wizard cancelled).")
		case err != nil:
			return fmt.Errorf("config wizard: %w", err)
		default:
			if err := writeConfig(cfg, configPath); err != nil {
				return fmt.Errorf("failed to create config: %w", err)
			}
			created = append(created, fmt.Sprintf("  - %-20s (configuration)", configPath))
		}
	}

	if env.Changed() {
		if err := env.Save(); err != nil {
			return fmt.Errorf("failed to write %s: %w", envPath, err)
		}
		created = append(created, fmt.Sprintf("  - %-20s (API keys)", envPath))
	}

	// Feedback
	fmt.Println()
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
	fmt.Println("  1. Review eulix.toml / .euignore if you skipped the wizard")
	fmt.Println("  2. Run 'eulix analyze' to analyze your codebase")
	fmt.Println("  3. Run 'eulix chat' to start querying")
	return nil
}

// writeConfig serializes cfg to TOML at dst using BurntSushi/toml's encoder
// so the output is always in sync with the actual Config struct - no
// hardcoded strings to maintain.
func writeConfig(cfg *config.Config, dst string) error {
	var buf bytes.Buffer
	buf.WriteString("# Eulix Configuration\n\n")
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	return os.WriteFile(dst, buf.Bytes(), 0644)
}
