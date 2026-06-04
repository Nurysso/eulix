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
	"fmt"
	"os"
	"strings"
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
		missing := state.missing()
		if len(missing) > 0 {
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
		if writeAll {
			return true
		}
		// targets == nil means "fill in missing"
		if targets == nil {
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
	if shouldWrite("euignore") || !state.hasEuignore {
		defaultIgnore := `# Eulix ignore patterns
node_modules/
.git/
*.test.go
vendor/
dist/
test/
build/
`
		if !state.hasEuignore || force {
			if err := os.WriteFile(euignorePath, []byte(defaultIgnore), 0644); err != nil {
				return fmt.Errorf("failed to create %s: %w", euignorePath, err)
			}
			created = append(created, fmt.Sprintf("  - %-20s (ignore patterns)", euignorePath))
		}
	}

	// 3. eulix.toml
	if shouldWrite("config") || !state.hasConfig {
		defaultConfig := `# Eulix Configuration
[project]
path = "."

[parser]
threads = 4

[embeddings]
model = "BAAI/bge-small-en-v1.5"

[llm]
local       = true
provider    = "ollama"
model       = "llama3.2:3b"
max_tokens  = 8192
temperature = 0.7
baseURL     = "http://localhost:11434"

[cache]

[cache.redis]
enabled  = false
url      = "redis://localhost:6379"
ttl_hours = 6

[cache.sql]
enabled = true
driver  = "sqlite"
dsn     = ".eulix/history.db"

[checksum]
change_threshold       = 0.10
force_reanalyze_threshold = 0.30
`
		if !state.hasConfig || force {
			if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
				return fmt.Errorf("failed to create config: %w", err)
			}
			created = append(created, fmt.Sprintf("  - %-20s (configuration)", configPath))
		}
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
