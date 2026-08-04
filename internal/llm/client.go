//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

// Package llm handles communication with LLM providers (Anthropic and Ollama).

/*
	It builds context-aware prompts from a ContextWindow and routes queries to
	the configured provider — remote (Anthropic) or local (Ollama).

Usage:
	client, err := MouthClient(cfg)
	response, err := client.Query(contextWindow, "What does this function do?")

	Provider selection is controlled by cfg.LLM.Local:
	- false → Anthropic API (requires cfg.LLM.APIKey)
	- true  → Ollama at localhost:11434 (or cfg.LLM.BaseURL if set)
*/

package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"eulix/internal/config"
	"eulix/internal/types"
)

// Provider constants — matches toml provider = "..."
const (
	ProviderAnthropic = "anthropic"
	ProviderOllama    = "ollama"
	ProviderOpenAI    = "openai" // also: groq, together, lm-studio, vllm, mistral, etc.
	ProviderGemini    = "gemini"
)

// openAICompatible lists every provider name that speaks the OpenAI
// chat-completions wire format. Add entries here — no other code changes needed.
var openAICompatible = map[string]bool{
	ProviderOpenAI: true,
	"groq":         true,
	"together":     true,
	"mistral":      true,
	"deepseek":     true,
	"lm-studio":    true,
	"lmstudio":     true,
	"vllm":         true,
	"openrouter":   true,
	"fireworks":    true,
}

type Client struct {
	config     *config.Config
	httpClient *http.Client
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

//OpenAI-compatible (OpenAI, Groq, Together, vLLM, LM Studio, Mistral …)

type openAIRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
}

type openAIResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
type ollamaRequest struct {
	Model    string        `json:"model"`
	Messages []Message     `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  ollamaOptions `json:"options,omitempty"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type ollamaResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
	Error   string  `json:"error,omitempty"`
}

// Gemini

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Client

// MouthClient — cause that's what llm is used for, to speak.
func MouthClient(cfg *config.Config) (*Client, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &Client{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

func validateConfig(cfg *config.Config) error {
	l := cfg.LLM
	p := resolveProvider(l)

	switch {
	case p == ProviderAnthropic:
		if l.APIKey == "" {
			return fmt.Errorf("llm: anthropic requires api_key")
		}
	case p == ProviderGemini:
		if l.APIKey == "" {
			return fmt.Errorf("llm: gemini requires api_key")
		}
	case p == ProviderOllama:
		// no key needed
	case openAICompatible[p]:
		// api_key optional for local/self-hosted servers
	case l.BaseURL != "":
		// Unknown provider name but explicit base_url — treat as OpenAI-compatible.
		// Covers any custom or future server without a code change.
	default:
		return fmt.Errorf(
			"llm: unknown provider %q — use anthropic | openai | ollama | gemini | lm-studio | groq | mistral | deepseek | vllm\n"+
				"     or set base_url to use any OpenAI-compatible endpoint",
			l.Provider,
		)
	}

	if l.Model == "" {
		return fmt.Errorf("llm: model must be set")
	}
	return nil
}

// resolveProvider normalises the provider string.
// If Local = true and provider is blank, it defaults to ollama.
func resolveProvider(l config.LLMConfig) string {
	p := strings.ToLower(strings.TrimSpace(l.Provider))
	if p == "" && l.Local {
		return ProviderOllama
	}
	return p
}

func (c *Client) LlmResponse(prompt string) (string, error) {
	// Debug logging
	if c.config.Project.DebugConfig {
		logDir := filepath.Join(c.config.Project.Path, ".eulix")
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create debug directory: %w", err)
		}

		logFile := filepath.Join(logDir, "llmdebug.log")
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open debug log file: %w", err)
		}
		defer f.Close()

		debugEntry := fmt.Sprintf("\n%s\n=== LLM DEBUG ENTRY ===\nTimestamp: %s\nProvider: %s\nModel: %s\n\n=== PROMPT ===\n%s\n=== END PROMPT ===\n%s\n",
			strings.Repeat("=", 80),
			time.Now().Format("2006-01-02 15:04:05"),
			resolveProvider(c.config.LLM),
			c.config.LLM.Model,
			prompt,
			strings.Repeat("=", 80))

		if _, err := f.WriteString(debugEntry); err != nil {
			return "", fmt.Errorf("failed to write debug entry: %w", err)
		}
	}

	// Use the prompt directly
	switch p := resolveProvider(c.config.LLM); {
	case p == ProviderAnthropic:
		return c.queryAnthropic(prompt)
	case p == ProviderOllama:
		return c.queryOllama(prompt)
	case p == ProviderGemini:
		return c.queryGemini(prompt)
	default:
		return c.queryOpenAI(prompt)
	}
}

// Provider implementations

func (c *Client) queryAnthropic(prompt string) (string, error) {
	l := c.config.LLM

	baseURL := resolveBaseURL(l.Provider, l.BaseURL)
	endpoint := resolveEndpoint(l.Provider, l.Endpoint)

	body, err := json.Marshal(anthropicRequest{
		Model:       l.Model,
		Messages:    []Message{{Role: "user", Content: prompt}},
		MaxTokens:   l.MaxTokens,
		Temperature: l.Temperature,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", baseURL+endpoint, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", l.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	var resp anthropicResponse
	if err := c.do(req, &resp); err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("anthropic: %s", resp.Error.Message)
	}
	if len(resp.Content) == 0 {
		return "", fmt.Errorf("anthropic: empty response")
	}
	return resp.Content[0].Text, nil
}

func (c *Client) queryOpenAI(prompt string) (string, error) {
	l := c.config.LLM

	baseURL := resolveBaseURL(l.Provider, l.BaseURL)
	endpoint := resolveEndpoint(l.Provider, l.Endpoint)

	body, err := json.Marshal(openAIRequest{
		Model:       l.Model,
		Messages:    []Message{{Role: "user", Content: prompt}},
		MaxTokens:   l.MaxTokens,
		Temperature: l.Temperature,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", baseURL+endpoint, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.APIKey)
	}

	var resp openAIResponse
	if err := c.do(req, &resp); err != nil {
		return "", fmt.Errorf("%s: %w", l.Provider, err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%s: %s", l.Provider, resp.Error.Message)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("%s: empty response", l.Provider)
	}
	return resp.Choices[0].Message.Content, nil
}

func (c *Client) queryOllama(prompt string) (string, error) {
	l := c.config.LLM

	baseURL := resolveBaseURL(l.Provider, l.BaseURL)
	endpoint := resolveEndpoint(l.Provider, l.Endpoint)

	body, err := json.Marshal(ollamaRequest{
		Model:    l.Model,
		Messages: []Message{{Role: "user", Content: prompt}},
		Stream:   false,
		Options: ollamaOptions{
			Temperature: l.Temperature,
			NumPredict:  l.MaxTokens,
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", baseURL+endpoint, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	var resp ollamaResponse
	if err := c.do(req, &resp); err != nil {
		return "", fmt.Errorf("ollama: %w (is Ollama running?)", err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("ollama: %s", resp.Error)
	}
	if resp.Message.Content == "" {
		return "", fmt.Errorf("ollama: empty response")
	}
	return resp.Message.Content, nil
}

func (c *Client) queryGemini(prompt string) (string, error) {
	l := c.config.LLM

	baseURL := "https://generativelanguage.googleapis.com"
	if l.BaseURL != "" {
		baseURL = strings.TrimRight(l.BaseURL, "/")
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", baseURL, l.Model, l.APIKey)

	body, err := json.Marshal(geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}, Role: "user"},
		},
		GenerationConfig: geminiGenerationConfig{
			Temperature:     l.Temperature,
			MaxOutputTokens: l.MaxTokens,
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	var resp geminiResponse
	if err := c.do(req, &resp); err != nil {
		return "", fmt.Errorf("gemini: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("gemini: %s", resp.Error.Message)
	}
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini: empty response")
	}
	return resp.Candidates[0].Content.Parts[0].Text, nil
}

// Helpers

// do executes req, checks the status code, and decodes JSON into out.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// providerDefaults holds the canonical base URL and endpoint path for each
// known provider.
type providerDefaults struct {
	baseURL  string
	endpoint string
}

var knownProviders = map[string]providerDefaults{
	ProviderOpenAI:    {"https://api.openai.com", "/v1/chat/completions"},
	"groq":            {"https://api.groq.com/openai", "/v1/chat/completions"},
	"together":        {"https://api.together.xyz", "/v1/chat/completions"},
	"mistral":         {"https://api.mistral.ai", "/v1/chat/completions"},
	"deepseek":        {"https://api.deepseek.com", "/v1/chat/completions"},
	"lm-studio":       {"http://localhost:1234", "/v1/chat/completions"},
	"lmstudio":        {"http://localhost:1234", "/v1/chat/completions"},
	"vllm":            {"http://localhost:8000", "/v1/chat/completions"},
	"openrouter":      {"https://openrouter.ai/api", "/v1/chat/completions"},
	"fireworks":       {"https://api.fireworks.ai/inference", "/v1/chat/completions"},
	ProviderAnthropic: {"https://api.anthropic.com", "/v1/messages"},
	ProviderOllama:    {"http://localhost:11434", "/api/chat"},
}

// resolveBaseURL: config base_url wins, then known default, then OpenAI fallback.
func resolveBaseURL(provider, cfgBase string) string {
	if cfgBase != "" {
		return strings.TrimRight(cfgBase, "/")
	}
	if d, ok := knownProviders[strings.ToLower(provider)]; ok {
		return d.baseURL
	}
	return "https://api.openai.com"
}

// resolveEndpoint: config endpoint wins, then known default, then /v1/chat/completions.
func resolveEndpoint(provider, cfgEndpoint string) string {
	if cfgEndpoint != "" {
		if !strings.HasPrefix(cfgEndpoint, "/") {
			return "/" + cfgEndpoint
		}
		return cfgEndpoint
	}
	if d, ok := knownProviders[strings.ToLower(provider)]; ok {
		return d.endpoint
	}
	return "/v1/chat/completions"
}

// BuildPromptString returns the full prompt string that would be sent to the LLM
// without making an actual API call.
// BuildFullPrompt returns the complete prompt string that would be sent to the LLM,
// including context chunks, user query, and any additional system instructions.
// This is used when you want the prompt without making an API call.
func (c *Client) BuildFullPrompt(context *types.ContextWindow, userQuery string, additionalPrompt ...string) string {
	var sb strings.Builder

	// Context chunks section
	sb.WriteString(c.buildPrompt(context, userQuery))

	// Any additional prompt content (e.g., CoT instructions from query package)
	for _, p := range additionalPrompt {
		sb.WriteString("\n")
		sb.WriteString(p)
	}

	return sb.String()
}

// buildPrompt builds only the context payload — no instruction boilerplate.
// Instructions belong in buildSystemPrompt() which is passed as the system role.
func (c *Client) buildPrompt(context *types.ContextWindow, userQuery string) string {
	var sb strings.Builder

	// Separate source vs metadata chunks
	var srcChunks, metaChunks []types.ContextChunk
	for _, chunk := range context.Chunks {
		if strings.TrimSpace(chunk.Content) != "" && !strings.HasPrefix(chunk.Content, "[METADATA]") {
			srcChunks = append(srcChunks, chunk)
		} else {
			metaChunks = append(metaChunks, chunk)
		}
	}

	// Compact inventory header — just counts, no per-token explanation
	sb.WriteString(fmt.Sprintf(
		"Context: %d source chunks, %d metadata, %d tokens across %d files\n\n",
		len(srcChunks), len(metaChunks), context.TotalTokens, len(context.Sources),
	))

	if len(srcChunks) > 0 {
		sb.WriteString("── SOURCE ──\n\n")
		for i, chunk := range srcChunks {
			sb.WriteString(fmt.Sprintf("[%d] %s:%d-%d (score %.2f)\n",
				i+1, chunk.File, chunk.StartLine, chunk.EndLine, chunk.Importance))
			sb.WriteString(chunk.Content)
			sb.WriteString("\n\n")
		}
	}

	if len(metaChunks) > 0 {
		sb.WriteString("── METADATA ──\n\n")
		for i, chunk := range metaChunks {
			sb.WriteString(fmt.Sprintf("[M%d] %s:%d-%d\n",
				i+1, chunk.File, chunk.StartLine, chunk.EndLine))
			sb.WriteString(chunk.Content)
			sb.WriteString("\n\n")
		}
	}

	sb.WriteString("QUERY\n")
	sb.WriteString(userQuery)
	sb.WriteString("\n")

	return sb.String()
}

// fillResidualBudget does a second bin-packing pass over skipped chunks,
// fitting any that are small enough to fill the remaining token budget.
// Call this after the primary greedy hydration loop.
//
// skipped: chunks that didn't fit in order (index → token cost)
// remaining: tokens left after the greedy pass
// addChunk: callback that appends the chunk content and returns new remaining
func fillResidualBudget(
	skipped []types.ContextChunk,
	remaining int,
	addChunk func(chunk types.ContextChunk) int, // returns new remaining
) int {
	if remaining <= 0 || len(skipped) == 0 {
		return remaining
	}

	// Sort skipped by token cost ascending — fit smallest first
	sorted := make([]types.ContextChunk, len(skipped))
	copy(sorted, skipped)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Tokens < sorted[j].Tokens
	})

	for _, chunk := range sorted {
		if remaining <= 0 {
			break
		}
		if chunk.Tokens <= remaining {
			remaining = addChunk(chunk)
		}
	}
	return remaining
}
