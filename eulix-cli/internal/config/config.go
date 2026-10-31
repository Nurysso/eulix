// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

/*
Package config handles configs defined by user for their project.

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
	Engine      string `toml:"engine"`
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
	Engine    string `toml:"engine"`
	Debug     bool   `toml:"debug"`
}

type LLMConfig struct {
	Provider    string  `toml:"provider"`
	Model       string  `toml:"model"`
	APIKey      string  `toml:"api_key"`
	BaseURL     string  `toml:"baseURL"`
	Endpoint    string  `toml:"endpoint"`
	Temperature float64 `toml:"temperature"`
	MaxTokens   int     `toml:"max_tokens"`
	Local       bool    `toml:"local"`
}

// RetrievalConfig is for General RAG / Graph Tuning (Additions will be made here this is A MASSIVE TODO)
type RetrievalConfig struct {
	CodeToAstRatio          float64 `toml:"code_to_ast_ratio"`          // How much code with respect to ast will be there in context window.
	SemanticMinSimilarity   float64 `toml:"semantic_min_similarity"`    // Minimum cosine similarity threshold for vector search hits (e.g., 0.15 - 0.40).
	PreMMRScoreFloorRatio   float64 `toml:"pre_mmr_score_floor_ratio"`  // Minimum relative score required to survive pre-MMR pruning (e.g., 0.05 = drops candidates < 5% of max score).
	MMRDiversityFactor      float32 `toml:"mmr_diversity_factor"`       // Balances MMR relevance vs. diversity (0.0 = max diversity, 1.0 = max relevance).
	CrossRootPenalty        float32 `toml:"cross_root_penalty"`         // Multiplier applied to candidates outside the primary root (e.g., 0.3 = soft penalty, 0.01 = strict).
	TestFilePenalty         float32 `toml:"test_file_penalty"`          // Score multiplier applied to test/mock files when query is not testing-specific (default 0.3).
	EnableSubsystemBoosting bool    `toml:"enable_subsystem_boosting"`  // Enables dynamic subsystem/directory boosting based on query context (default true).
	ApplyCrossRootIsolation bool    `toml:"apply_cross_root_isolation"` // Toggles whether we care about crossing project boundaries at all.
	// ========== If any bool is in need to be added add here for better memory alignment. ==========
	TopKCandidates         int `toml:"top_k_candidates"`          // How many raw vector hits to pull before applying graph expansion and MMR pruning.
	MaxGraphExpansionDepth int `toml:"max_graph_expansion_depth"` // How many hops to traverse in your Rust call-graph (1 = direct deps, 2 = transitive deps).
	MaxGraphExpansions     int `toml:"max_graph_expansions"`      // Maximum number of call-graph relationship edges to expand (default 15).
	MaxExactAnchors        int `toml:"max_exact_anchors"`         // Maximum number of exact symbol anchors to pin to context (default 2).
	MaxContextChunks       int `toml:"max_context_chunks"`        // Maximum number of chunks allowed in the final context window (default 30).

	Etiya EtiyaConfig `toml:"etiya"` // [retrievalConfig.etiya] overrides, applied by ForEngine.
	Ehnas EhnasConfig `toml:"ehnas"` // [retrievalConfig.ehnas] overrides and EHNAS-only tuning.
}

// EtiyaConfig overrides shared retrieval keys for the etiya engine. A nil
// field (key absent from eulix.toml) inherits the shared [retrievalConfig] value.
type EtiyaConfig struct {
	PreMMRScoreFloorRatio *float64 `toml:"pre_mmr_score_floor_ratio,omitempty"`
	MMRDiversityFactor    *float32 `toml:"mmr_diversity_factor,omitempty"`
	SemanticMinSimilarity *float64 `toml:"semantic_min_similarity,omitempty"`
	MaxContextChunks      *int     `toml:"max_context_chunks,omitempty"`

	// LexicalExclude: paths the MCP grep fallback skips while etiya is active.
	// Globs match the repo-relative path or the file name; "dir/" skips a directory.
	LexicalExclude []string `toml:"lexical_exclude,omitempty"`
}

// EhnasConfig overrides shared retrieval keys for the ehnas engine (nil =
// inherit, as in EtiyaConfig) and carries the EHNAS-only tuning knobs.
type EhnasConfig struct {
	PreMMRScoreFloorRatio *float64 `toml:"pre_mmr_score_floor_ratio,omitempty"`
	MMRDiversityFactor    *float32 `toml:"mmr_diversity_factor,omitempty"`
	MaxContextChunks      *int     `toml:"max_context_chunks,omitempty"`

	MMRStopFloor           float64 `toml:"mmr_stop_floor"`            // structuralMMR stops once the best marginal value is <= this (default 0.0).
	NoveltyGain            float64 `toml:"novelty_gain"`              // Max score swing noveltyPack applies to novel vs redundant blocks (default 0.85).
	MCSeedMinScore         float64 `toml:"mc_seed_min_score"`         // Minimum score for a hit to seed the Monte Carlo walk (default 60).
	MCWalkers              int     `toml:"mc_walkers"`                // Monte Carlo walkers started per query (default 256).
	MCSeedCap              int     `toml:"mc_seed_cap"`               // Maximum number of Monte Carlo seeds (default 16).
	CommonSymbolDF         int     `toml:"common_symbol_df"`          // Symbols in more chunks than this carry no novelty cost (default 200).
	InvertedIndexMinChunks int     `toml:"inverted_index_min_chunks"` // Build the inverted index (PRF, lexical walk layer) above this many chunks; 0 = always (default 5000).
}

// ForEngine returns a copy of rc with the engine's [retrievalConfig.<engine>]
// overrides applied to the shared fields, so callers read one resolved value.
func (rc RetrievalConfig) ForEngine(engine string) RetrievalConfig {
	switch engine {
	case "etiya":
		e := rc.Etiya
		override(&rc.PreMMRScoreFloorRatio, e.PreMMRScoreFloorRatio)
		override(&rc.MMRDiversityFactor, e.MMRDiversityFactor)
		override(&rc.SemanticMinSimilarity, e.SemanticMinSimilarity)
		override(&rc.MaxContextChunks, e.MaxContextChunks)
	case "ehnas":
		e := rc.Ehnas
		override(&rc.PreMMRScoreFloorRatio, e.PreMMRScoreFloorRatio)
		override(&rc.MMRDiversityFactor, e.MMRDiversityFactor)
		override(&rc.MaxContextChunks, e.MaxContextChunks)
	}
	return rc
}

func override[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// clampPtr clamps *p in place when the key was set.
func clampPtr[T cmp.Ordered](p *T, lo, hi T) {
	if p != nil {
		*p = clamp(*p, lo, hi)
	}
}

type CacheConfig struct {
	Path   string `toml:"path"`
	Enable bool   `toml:"enable"`
}

type ChecksumConfig struct {
	ChangeThreshold         float64 `toml:"change_threshold"`
	ForceReanalyzeThreshold float64 `toml:"force_reanalyze_threshold"`
}

var providerEnvVar = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"gemini":     "GEMINI_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"groq":       "GROQ_API_KEY",
	"together":   "TOGETHER_API_KEY",
	"mistral":    "MISTRAL_API_KEY",
	"deepseek":   "DEEPSEEK_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"fireworks":  "FIREWORKS_API_KEY",
}

func resolveAPIKeyFromEnv(provider string, repoEnv map[string]string) string {
	lookup := func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return repoEnv[key]
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	if envVar, ok := providerEnvVar[p]; ok {
		if v := lookup(envVar); v != "" {
			return v
		}
	}
	return lookup("LLM_API_KEY")
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
	rc.SemanticMinSimilarity = clamp(rc.SemanticMinSimilarity, 0.0, 1.0)
	rc.MaxGraphExpansions = max(rc.MaxGraphExpansions, 0)
	rc.MaxExactAnchors = max(rc.MaxExactAnchors, 1)
	rc.TestFilePenalty = clamp(rc.TestFilePenalty, 0.0, 1.0)
	rc.MaxContextChunks = max(rc.MaxContextChunks, 1)

	for _, ov := range []struct {
		floor *float64
		div   *float32
		maxCh *int
	}{
		{rc.Etiya.PreMMRScoreFloorRatio, rc.Etiya.MMRDiversityFactor, rc.Etiya.MaxContextChunks},
		{rc.Ehnas.PreMMRScoreFloorRatio, rc.Ehnas.MMRDiversityFactor, rc.Ehnas.MaxContextChunks},
	} {
		clampPtr(ov.floor, 0.0, 1.0)
		clampPtr(ov.div, 0.0, 1.0)
		clampPtr(ov.maxCh, 1, 1<<30)
	}
	clampPtr(rc.Etiya.SemanticMinSimilarity, 0.0, 1.0)

	eh := &rc.Ehnas
	eh.NoveltyGain = clamp(eh.NoveltyGain, 0.0, 1.0)
	eh.MCSeedMinScore = max(eh.MCSeedMinScore, 0.0)
	eh.MCWalkers = max(eh.MCWalkers, 1)
	eh.MCSeedCap = max(eh.MCSeedCap, 1)
	eh.CommonSymbolDF = max(eh.CommonSymbolDF, 1)
	eh.InvertedIndexMinChunks = max(eh.InvertedIndexMinChunks, 0)

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
	// Read repository keys without leaking them into other repositories.
	repoEnv, _ := godotenv.Read(".env")
	cfg := *DefaultConfig()

	// Overlay configuration from file if present
	_, _ = toml.DecodeFile("eulix.toml", &cfg)

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
		cfg.LLM.APIKey = resolveAPIKeyFromEnv(cfg.LLM.Provider, repoEnv)
	}

	return &cfg, nil
}

// LoadFrom loads and validates the configuration from a specific project repository directory.
func LoadFrom(repoDir string) (*Config, error) {
	absPath, err := filepath.Abs(repoDir)
	if err != nil {
		return nil, err
	}
	repoEnv, _ := godotenv.Read(filepath.Join(absPath, ".env"))
	cfg := *DefaultConfig()

	tomlPath := filepath.Join(absPath, "eulix.toml")
	_, _ = toml.DecodeFile(tomlPath, &cfg)

	cfg = *validateAndClampConfig(&cfg)
	cfg.Project.Path = absPath

	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = resolveAPIKeyFromEnv(cfg.LLM.Provider, repoEnv)
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
			Engine:    "onnx",
			Debug:     false,
		},
		LLM: LLMConfig{
			Local:       true,
			Provider:    "ollama",
			Model:       "llama3.2:3b",
			MaxTokens:   8192,
			Temperature: 0.2,
			BaseURL:     "http://localhost:11434",
			Endpoint:    "",
		},
		RetrievalConfig: RetrievalConfig{
			CodeToAstRatio:          0.85,
			ApplyCrossRootIsolation: true,
			CrossRootPenalty:        0.32,
			PreMMRScoreFloorRatio:   0.05,
			TopKCandidates:          150,
			MMRDiversityFactor:      0.85,
			MaxGraphExpansionDepth:  1,
			SemanticMinSimilarity:   0.15,
			MaxGraphExpansions:      15,
			MaxExactAnchors:         2,
			TestFilePenalty:         0.30,
			EnableSubsystemBoosting: true,
			MaxContextChunks:        30,
			Ehnas: EhnasConfig{
				MMRStopFloor:           0.0,
				NoveltyGain:            0.85,
				MCSeedMinScore:         60,
				MCWalkers:              256,
				MCSeedCap:              16,
				CommonSymbolDF:         200,
				InvertedIndexMinChunks: 5000,
			},
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
