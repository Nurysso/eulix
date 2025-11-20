package config

import (
	// "fmt"
	"os"

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
	Path string `toml:"path"`
}

type ParserConfig struct {
	Threads int `toml:"threads"`
}

type EmbeddingsConfig struct {
	Model     string `toml:"model"`
	Backend   string `toml:"backend"`
	Dimension int    `toml:"dimension"`
}

type LLMConfig struct {
	Local  		bool `toml:"local"`
	Provider    string  `toml:"provider"`
	Model       string  `toml:"model"`
	APIKey      string  `toml:"api_key"`
	MaxTokens   int     `toml:"max_tokens"`
	Temperature float64 `toml:"temperature"`
	BaseURL     string `toml:"baseURL"`
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
	ChangeThreshold          float64 `toml:"change_threshold"`
	ForceReanalyzeThreshold float64 `toml:"force_reanalyze_threshold"`
}

func Load() (*Config, error) {
	var cfg Config

	// Try to read from eulix.toml
	if _, err := toml.DecodeFile("eulix.toml", &cfg); err != nil {
		// Return default config
		return defaultConfig(), nil
	}

	// Override API key from environment if not set
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	return &cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Project: ProjectConfig{
			Path: ".",
		},
		Parser: ParserConfig{
			Threads: 4,
		},
		Embeddings: EmbeddingsConfig{
			Model:     "BAAI/bge-small-en-v1.5",
			Backend:   "auto",
			Dimension: 384,
		},
		LLM: LLMConfig{
			Local: 		true,
			Provider:    "ollama",
			Model:       "llama3.2:3b",
			MaxTokens:   8192,
			Temperature: 0.7,
			BaseURL: "http://localhost:11434",
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
			ChangeThreshold:          0.10,
			ForceReanalyzeThreshold: 0.30,
		},
	}
}
