// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestProviderEnvironmentKeys(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	for provider, env := range map[string]string{"Anthropic": "ANTHROPIC_API_KEY", "Gemini": "GEMINI_API_KEY"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv(env, "test-provider-key")
			for _, spelling := range []string{provider, "  " + provider + "  "} {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "eulix.toml"), []byte(fmt.Sprintf("[llm]\nprovider = %q\n", spelling)), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg, err := LoadFrom(dir)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.LLM.APIKey != "test-provider-key" {
					t.Error("provider environment key not resolved")
				}
			}
		})
	}
}

func TestLoadFromDoesNotLeakRepositoryKeys(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "LLM_API_KEY"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(dir, "eulix.toml"), []byte("[llm]\nprovider = 'openai'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(a, ".env"), []byte("OPENAI_API_KEY=repo-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".env"), []byte("OPENAI_API_KEY=repo-b-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ dir, key string }{{a, "repo-a-key"}, {b, "repo-b-key"}, {t.TempDir(), ""}} {
		if tc.key == "" {
			if err := os.WriteFile(filepath.Join(tc.dir, "eulix.toml"), []byte("[llm]\nprovider = 'openai'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cfg, err := LoadFrom(tc.dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLM.APIKey != tc.key {
			t.Error("key leaked across repository boundaries")
		}
	}
	if _, ok := os.LookupEnv("OPENAI_API_KEY"); ok {
		t.Error("LoadFrom mutated process environment")
	}
}
