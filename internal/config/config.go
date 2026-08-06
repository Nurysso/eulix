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
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
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
	Threads int  `toml:"threads"`
	PrismV  int  `toml:"prismVersion"`
	Verbose bool `toml:"verbose"`
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

var providerEnvVar = map[string]string{
	"Anthropic":  "ANTHROPIC_API_KEY",
	"Gemini":     "GEMINI_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"groq":       "GROQ_API_KEY",
	"together":   "TOGETHER_API_KEY",
	"mistral":    "MISTRAL_API_KEY",
	"deepseek":   "DEEPSEEK_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"fireworks":  "FIREWORKS_API_KEY",
}

// resolveAPIKeyFromEnv checks the provider-specific var first, then falls
// back to a generic LLM_API_KEY so custom/self-hosted setups still work.
func resolveAPIKeyFromEnv(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if envVar, ok := providerEnvVar[p]; ok {
		if v := os.Getenv(envVar); v != "" {
			return v
		}
	}
	return os.Getenv("LLM_API_KEY")
}

func Load() (*Config, error) {
	// Load .env into process env if present. Missing file is fine not an error.
	_ = godotenv.Load()

	var cfg Config

	if _, err := toml.DecodeFile("eulix.toml", &cfg); err != nil {
		cfg = *DefaultConfig()
	}

	if cfg.Project.Path != "" {
		absPath, err := filepath.Abs(cfg.Project.Path)
		if err != nil {
			return nil, err
		}
		cfg.Project.Path = absPath
	}

	// toml api_key wins if set; otherwise pull from env, keyed by provider.
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = resolveAPIKeyFromEnv(cfg.LLM.Provider)
	}

	if cfg.Cache.SQL.Enabled && !filepath.IsAbs(cfg.Cache.SQL.DSN) {
		absPath, err := filepath.Abs(cfg.Cache.SQL.DSN)
		if err == nil {
			cfg.Cache.SQL.DSN = absPath
		}
	}

	return &cfg, nil
}

func DefaultConfig() *Config {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	return &Config{
		Project: ProjectConfig{
			Path:        cwd,
			MaxLines:    100,
			DebugConfig: false,
		},
		Parser: ParserConfig{
			Threads: 4,
			PrismV:  2,
			Verbose: true,
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
	sourceRoot := filepath.Dir(eulixDir)
	testFile := "testproject/django/shortcuts.py"
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
