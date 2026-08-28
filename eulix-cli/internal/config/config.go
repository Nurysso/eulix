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
	"cmp"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
)

type Config struct {
	Project         ProjectConfig    `toml:"project"`
	Parser          ParserConfig     `toml:"parser"`
	Embeddings      EmbeddingsConfig `toml:"embeddings"`
	LLM             LLMConfig        `toml:"llm"`
	RetrievalConfig RetrievalConfig  `toml:"retrievalConfig"`
	Cache           CacheConfig      `toml:"cache"`
	Checksum        ChecksumConfig   `toml:"checksum"`
}

type ProjectConfig struct {
	Path        string `toml:"path"`
	MaxLines    int    `toml:"Max_Lines"`
	DebugConfig bool   `toml:"DebugConfig"`
	// EmbedIs     string `toml:"embedIs"`
}

type ParserConfig struct {
	Threads int  `toml:"threads"`
	PrismV  int  `toml:"prismVersion"`
	Verbose bool `toml:"verbose"`
}

type EmbeddingsConfig struct {
	Model     string `toml:"model"`
	Dimension int    `toml:"dimension"`
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

// --- General RAG / Graph Tuning (Additions will be made here A MASSIVE TODO) ---
type RetrievalConfig struct {
	CodeToAstRatio          float64 `toml:"code_to_ast_ratio"`
	ApplyCrossRootIsolation bool    `toml:"apply_cross_root_isolation"` // Toggles whether we care about crossing project boundaries at all.
	CrossRootPenalty        float32 `toml:"cross_root_penalty"`         // Multiplier applied to candidates outside the primary root (e.g., 0.3 = soft penalty, 0.01 = strict).
	PreMMRScoreFloorRatio   float32 `toml:"pre_mmr_score_floor_ratio"`  // Minimum relative score required to survive pre-MMR pruning (e.g., 0.05 = drops candidates < 5% of max score).
	TopKCandidates          int     `toml:"top_k_candidates"`           // How many raw vector hits to pull before applying graph expansion and MMR pruning.
	MMRDiversityFactor      float32 `toml:"mmr_diversity_factor"`       // Balances MMR relevance vs. diversity (0.0 = max diversity, 1.0 = max relevance).
	MaxGraphExpansionDepth  int     `toml:"max_graph_expansion_depth"`  // How many hops to traverse in your Rust call-graph (1 = direct deps, 2 = transitive deps).
}

type CacheConfig struct {
	Enable bool   `toml:"enable"`
	Path   string `toml:"path"`
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

func resolveAPIKeyFromEnv(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if envVar, ok := providerEnvVar[p]; ok {
		if v := os.Getenv(envVar); v != "" {
			return v
		}
	}
	return os.Getenv("LLM_API_KEY")
}

// clamp restricts v to the inclusive range [min, max].
func clamp[T cmp.Ordered](v, min, max T) T {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// max returns the larger of a or b.
func max[T cmp.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// validateAndClampConfig ensures all configuration settings fall within valid boundaries.
// Modifies and returns cfg in place, returning nil if cfg is nil.
func validateAndClampConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}

	// Retrieval Configuration
	rc := &cfg.RetrievalConfig
	rc.CodeToAstRatio = clamp(rc.CodeToAstRatio, 0.0, 1.0)
	rc.CrossRootPenalty = clamp(rc.CrossRootPenalty, 0.0, 1.0)
	rc.PreMMRScoreFloorRatio = clamp(rc.PreMMRScoreFloorRatio, 0.0, 1.0)
	rc.MMRDiversityFactor = clamp(rc.MMRDiversityFactor, 0.0, 1.0)
	rc.TopKCandidates = max(rc.TopKCandidates, 1)
	rc.MaxGraphExpansionDepth = max(rc.MaxGraphExpansionDepth, 0)

	// Checksum Configuration
	cs := &cfg.Checksum
	cs.ChangeThreshold = clamp(cs.ChangeThreshold, 0.0, 1.0)
	cs.ForceReanalyzeThreshold = clamp(cs.ForceReanalyzeThreshold, 0.0, 1.0)
	// Guarantee ForceReanalyzeThreshold >= ChangeThreshold
	cs.ForceReanalyzeThreshold = max(cs.ForceReanalyzeThreshold, cs.ChangeThreshold)

	// Project Configuration
	cfg.Project.MaxLines = max(cfg.Project.MaxLines, 1)
	cfg.Parser.Threads = max(cfg.Parser.Threads, 1)
	cfg.Embeddings.Dimension = max(cfg.Embeddings.Dimension, 1)

	// LLM Configuration
	cfg.LLM.MaxTokens = max(cfg.LLM.MaxTokens, 1)
	cfg.LLM.Temperature = clamp(cfg.LLM.Temperature, 0.0, 2.0)

	return cfg
}

// Load loads and validates the configuration
func Load() (*Config, error) {
	// Load .env into process env if present. Missing file is fine not an error.
	_ = godotenv.Load()
	var cfg Config

	// Try to load from file, fallback to defaults
	if _, err := toml.DecodeFile("eulix.toml", &cfg); err != nil {
		cfg = *DefaultConfig()
	}

	// Validate and clamp configuration values
	cfg = *validateAndClampConfig(&cfg)

	// Resolve project path
	if cfg.Project.Path != "" {
		absPath, err := filepath.Abs(cfg.Project.Path)
		if err != nil {
			return nil, err
		}
		cfg.Project.Path = absPath
	}

	// Resolve API key if not set
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = resolveAPIKeyFromEnv(cfg.LLM.Provider)
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
			// EmbedIs:     "script",
		},
		Parser: ParserConfig{
			Threads: 4,
			PrismV:  2,
			Verbose: true,
		},
		Embeddings: EmbeddingsConfig{
			Model:     "BAAI/bge-base-en-v1.5",
			Dimension: 768,
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
		RetrievalConfig: RetrievalConfig{
			CodeToAstRatio:          0.85,
			ApplyCrossRootIsolation: true,
			CrossRootPenalty:        0.32,
			PreMMRScoreFloorRatio:   0.05,
			TopKCandidates:          150,
			MMRDiversityFactor:      0.65,
			MaxGraphExpansionDepth:  1,
		},
		Cache: CacheConfig{
			Enable: true,
			Path:   ".eulix/history.db",
		},
		Checksum: ChecksumConfig{
			ChangeThreshold:         0.10,
			ForceReanalyzeThreshold: 0.30,
		},
	}
}
