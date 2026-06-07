//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package config handles configs defined by user for their project.
/*
Responsible for parsing toml based config and writes default config
when init is ran
*/

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Project    ProjectConfig    `toml:"project"`
	Parser     ParserConfig     `toml:"parser"`
	Embeddings EmbeddingsConfig `toml:"embeddings"`
	LLM        LLMConfig        `toml:"llm"`
	Cache      CacheConfig      `toml:"cache"`
	Checksum   ChecksumConfig   `toml:"checksum"`
}

type ProjectConfig struct {
	Path        string `toml:"path"`
	MaxLines    int    `toml:"Max_Lines"`
	DebugConfig bool   `toml:"DebugConfig"`
}

type ParserConfig struct {
	Threads int `toml:"threads"`
}

type EmbeddingsConfig struct {
	Model     string `toml:"model"`
	Dimension int    `toml:"dimension"`
	// VenvPath  string `toml:"venvPath"`
}

type LLMConfig struct {
	Local       bool    `toml:"local"`
	Provider    string  `toml:"provider"`
	Model       string  `toml:"model"`
	APIKey      string  `toml:"api_key"`
	MaxTokens   int     `toml:"max_tokens"`
	Temperature float64 `toml:"temperature"`
	BaseURL     string  `toml:"baseURL"`
	Endpoint    string  `toml:"endpoint"`
}

type CacheConfig struct {
	Redis RedisConfig `toml:"redis"`
	SQL   SQLConfig   `toml:"sql"`
}

type RedisConfig struct {
	Enabled  bool   `toml:"enabled"`
	URL      string `toml:"url"`
	TTLHours int    `toml:"ttl_hours"`
}

type SQLConfig struct {
	Enabled bool   `toml:"enabled"`
	Driver  string `toml:"driver"`
	DSN     string `toml:"dsn"`
}

type ChecksumConfig struct {
	ChangeThreshold         float64 `toml:"change_threshold"`
	ForceReanalyzeThreshold float64 `toml:"force_reanalyze_threshold"`
}

func Load() (*Config, error) {
	var cfg Config

	// Try to read from eulix.toml
	if _, err := toml.DecodeFile("eulix.toml", &cfg); err != nil {
		// Return default config
		cfg = *DefaultConfig()
	}

	// Resolve project path to absolute path (cross-platform)
	if cfg.Project.Path != "" {
		absPath, err := filepath.Abs(cfg.Project.Path)
		if err != nil {
			return nil, err
		}
		if cfg.Project.DebugConfig {
			fmt.Printf("[CONFIG] Project path: %s → %s\n", cfg.Project.Path, absPath)
		}
		cfg.Project.Path = absPath
	}

	// Override API key from environment if not set
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	// Normalize cache DSN path (make it absolute if relative)
	if cfg.Cache.SQL.Enabled && !filepath.IsAbs(cfg.Cache.SQL.DSN) {
		absPath, err := filepath.Abs(cfg.Cache.SQL.DSN)
		if err == nil {
			cfg.Cache.SQL.DSN = absPath
		}
	}

	return &cfg, nil
}

func DefaultConfig() *Config {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	// home, _ := os.UserHomeDir()
	// PathVenv := filepath.Join(home, ".Eulix", ".venv")
	return &Config{
		Project: ProjectConfig{
			Path:        cwd,
			MaxLines:    100,
			DebugConfig: false,
		},
		Parser: ParserConfig{
			Threads: 4,
		},
		Embeddings: EmbeddingsConfig{
			Model:     "BAAI/bge-base-en-v1.5",
			Dimension: 768,
			// VenvPath:  PathVenv,
		},
		LLM: LLMConfig{
			Local:       true,
			Provider:    "ollama",
			Model:       "llama3.2:3b",
			MaxTokens:   8192,
			Temperature: 0.7,
			BaseURL:     "http://localhost:11434",
			Endpoint:    "",
		},
		Cache: CacheConfig{
			Redis: RedisConfig{
				Enabled:  false,
				URL:      "redis://localhost:6379",
				TTLHours: 6,
			},
			SQL: SQLConfig{
				Enabled: true,
				Driver:  "sqlite",
				DSN:     ".eulix/history.db",
			},
		},
		Checksum: ChecksumConfig{
			ChangeThreshold:         0.10,
			ForceReanalyzeThreshold: 0.30,
		},
	}
}

func TestSourcePaths() {
	eulixDir := "/path/to/.eulix"
	sourceRoot := filepath.Dir(eulixDir) // parent of .eulix

	// Test file from your KB
	testFile := "testproject/django/shortcuts.py" // example from Django
	fullPath := filepath.Join(sourceRoot, testFile)

	fmt.Printf("Eulix dir: %s\n", eulixDir)
	fmt.Printf("Source root: %s\n", sourceRoot)
	fmt.Printf("Test file: %s\n", fullPath)

	if _, err := os.Stat(fullPath); err == nil {
		fmt.Println("✓ Source file exists")
	} else {
		fmt.Println("✗ Source file not found")
	}
}
