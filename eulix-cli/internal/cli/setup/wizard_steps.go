//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer mnae (Nurysso) contact - nurysso [at] proton.me

// package setup contains code related to init flow of eulix

package setup

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"eulix/internal/config"
)

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

// providerNames returns the provider names in a stable, sorted order
// so the picker looks the same on every run.
func providerNames() []string {
	names := make([]string, 0, len(providerEnvVar))
	for name := range providerEnvVar {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// BuildEuignoreSteps shows what's currently ignored and offers to add more patterns
func BuildEuignoreSteps(path string) []Step {
	currentPatterns := formatPatternList(readEuignorePatterns(path))
	return []Step{
		{
			Key:    "euignore_patterns",
			Kind:   StepText,
			Prompt: "Add more ignore patterns? (comma-separated, or press enter to skip)",
			Help:   "These patterns are currently ignored:\n" + currentPatterns,
			Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
				if answer == "" {
					return nil // user pressed enter to skip
				}
				return appendEuignorePatterns(path, answer)
			},
		},
	}
}

// readEuignorePatterns reads the .euignore file and returns the real
// patterns in it, skipping blank lines and #-comments. A missing file
// just means nothing is ignored yet.
func readEuignorePatterns(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// formatPatternList renders the patterns for display in a step's Help
// text, indented two spaces and with a friendly message when empty.
func formatPatternList(patterns []string) string {
	if len(patterns) == 0 {
		return "  (none yet)"
	}

	var builder strings.Builder
	for _, pattern := range patterns {
		builder.WriteString("  ")
		builder.WriteString(pattern)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

// appendEuignorePatterns splits the user's comma-separated answer and
// appends each non-empty pattern to the .euignore file, one per line.
func appendEuignorePatterns(path, answer string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to update %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	for _, pattern := range strings.Split(answer, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue // tolerate "a,,b" and stray commas
		}
		if _, err := fmt.Fprintln(f, pattern); err != nil {
			return fmt.Errorf("failed to update %s: %w", path, err)
		}
	}
	return nil
}

// BuildConfigSteps asks whether to tune eulix.toml, then walks through
// parser threads and LLM setup.
func BuildConfigSteps() []Step {
	return []Step{
		{
			Key:    "edit_config",
			Kind:   StepConfirm,
			Prompt: "Edit eulix.toml settings now?",
			Help:   "Set parser threads and your LLM provider/model.",
			Next: func(answer string, a Answers) []Step {
				if answer != "Yes" {
					return nil
				}
				return []Step{threadsStep(), llmModeStep()}
			},
		},
	}
}

// threadsStep asks how many goroutines the parser should use, defaulting
// to the number of CPUs on this machine.
func threadsStep() Step {
	return Step{
		Key:     "parser_threads",
		Kind:    StepText,
		Prompt:  "How many threads should the parser use?",
		Default: strconv.Itoa(runtime.NumCPU()),
		Validate: func(v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return fmt.Errorf("enter a whole number greater than 0")
			}
			return nil
		},
		Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
			n, _ := strconv.Atoi(answer) // already validated above
			cfg.Parser.Threads = n
			return nil
		},
	}
}

// llmModeStep ask users to choose local/remote or llm
func llmModeStep() Step {
	return Step{
		Key:     "llm_mode",
		Kind:    StepSelect,
		Prompt:  "Will the LLM run locally or via a remote API?",
		Options: []string{"Local", "Remote"},
		Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
			cfg.LLM.Local = answer == "Local"
			return nil
		},
		Next: func(answer string, a Answers) []Step {
			if answer == "Local" {
				return []Step{localModelStep(), baseURLStep(), endpointStep()}
			}
			return []Step{providerStep()}
		},
	}
}

// localModelStep asks for the local model tag (e.g. llama3.1:8b).
func localModelStep() Step {
	return Step{
		Key:    "llm_model_local",
		Kind:   StepText,
		Prompt: "Model name (e.g. llama3.1:8b)",
		Validate: func(v string) error {
			if v == "" {
				return fmt.Errorf("model name can't be empty")
			}
			return nil
		},
		Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
			cfg.LLM.Model = answer
			return nil
		},
	}
}

// baseURLStep asks where the local model server lives.
func baseURLStep() Step {
	return Step{
		Key:     "llm_base_url",
		Kind:    StepText,
		Prompt:  "Base URL for the local model server",
		Default: "http://localhost:11434", // Ollama's default port
		Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
			cfg.LLM.BaseURL = answer
			return nil
		},
	}
}

// endpointStep asks for the chat completions path on the local server.
func endpointStep() Step {
	return Step{
		Key:     "llm_endpoint",
		Kind:    StepText,
		Prompt:  "Endpoint path",
		Default: "/v1/chat/completions",
		Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
			cfg.LLM.Endpoint = answer
			return nil
		},
	}
}

// providerStep asks which remote provider to use. Its Next() pulls in
// the model and API-key steps, passing the chosen provider along so
// they can tailor their prompts and pick the right env var.
func providerStep() Step {
	return Step{
		Key:     "llm_provider",
		Kind:    StepSelect,
		Prompt:  "Choose your LLM provider",
		Options: providerNames(),
		Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
			cfg.LLM.Provider = answer
			return nil
		},
		Next: func(answer string, a Answers) []Step {
			return []Step{remoteModelStep(answer), apiKeyStep(answer)}
		},
	}
}

// remoteModelStep asks for a model name, with a provider-specific hint.
func remoteModelStep(provider string) Step {
	return Step{
		Key:    "llm_model_remote",
		Kind:   StepText,
		Prompt: fmt.Sprintf("Model name for %s (e.g. claude-sonnet-4-6)", provider),
		Validate: func(v string) error {
			if v == "" {
				return fmt.Errorf("model name can't be empty")
			}
			return nil
		},
		Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
			cfg.LLM.Model = answer
			return nil
		},
	}
}

// apiKeyStep collects the provider's API key and stashes it in .env.
// This step only runs after providerStep, so the env var name is known
// up front and can be captured in the closure instead of looked up again.
func apiKeyStep(provider string) Step {
	envVar := providerEnvVar[provider]
	return Step{
		Key:    "llm_api_key",
		Kind:   StepPassword,
		Prompt: fmt.Sprintf("%s API key", provider),
		Help:   fmt.Sprintf("Stored in .env as %s - never written into eulix.toml.", envVar),
		Validate: func(v string) error {
			if v == "" {
				return fmt.Errorf("API key can't be empty")
			}
			return nil
		},
		Apply: func(answer string, cfg *config.Config, env *EnvFile) error {
			env.Set(envVar, answer)
			return nil
		},
	}
}
