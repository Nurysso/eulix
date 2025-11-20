package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"eulix/internal/config"
	"eulix/internal/types"
)

type Client struct {
	config     *config.Config
	httpClient *http.Client
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Anthropic API structures
type AnthropicRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
}

type AnthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// Ollama API structures
type OllamaRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Options     *OllamaOptions `json:"options,omitempty"`
}

type OllamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"` // max tokens for Ollama
}

type OllamaResponse struct {
	Model     string  `json:"model"`
	CreatedAt string  `json:"created_at"`
	Message   Message `json:"message"`
	Done      bool    `json:"done"`
}

func NewClient(cfg *config.Config) (*Client, error) {
	return &Client{
		config:     cfg,
		httpClient: &http.Client{},
	}, nil
}

func (c *Client) Query(context *types.ContextWindow, userQuery string) (string, error) {
	// Build prompt
	prompt := c.buildPrompt(context, userQuery)

	// Route to appropriate provider
	if c.config.LLM.Local {
		return c.queryOllama(prompt)
	}
	return c.queryAnthropic(prompt)
}

func (c *Client) queryAnthropic(prompt string) (string, error) {
	reqBody := AnthropicRequest{
		Model: c.config.LLM.Model,
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens:   c.config.LLM.MaxTokens,
		Temperature: c.config.LLM.Temperature,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.LLM.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Anthropic API error %d: %s", resp.StatusCode, string(body))
	}

	var response AnthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	if len(response.Content) == 0 {
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	return response.Content[0].Text, nil
}

func (c *Client) queryOllama(prompt string) (string, error) {
	reqBody := OllamaRequest{
		Model: c.config.LLM.Model,
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
		Stream: false,
		Options: &OllamaOptions{
			Temperature: c.config.LLM.Temperature,
			NumPredict:  c.config.LLM.MaxTokens,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	// Use correct Ollama chat endpoint
	ollamaURL := "http://localhost:11434/api/chat"
	if c.config.LLM.BaseURL != "" {
		ollamaURL = c.config.LLM.BaseURL + "/api/chat"  // Changed from /api/generate
	}

	req, err := http.NewRequest("POST", ollamaURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to connect to Ollama: %w (make sure Ollama is running)", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Ollama API error %d: %s", resp.StatusCode, string(body))
	}

	var response OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	if response.Message.Content == "" {
		return "", fmt.Errorf("empty response from Ollama")
	}

	return response.Message.Content, nil
}

func (c *Client) buildPrompt(context *types.ContextWindow, userQuery string) string {
	prompt := "You are analyzing a codebase with the following context:\n\n"
	prompt += "═══════════════════════════════════════════════════════════════\n\n"

	for i, chunk := range context.Chunks {
		prompt += fmt.Sprintf("File: %s (Lines %d-%d)\n", chunk.File, chunk.StartLine, chunk.EndLine)
		prompt += fmt.Sprintf("Relevance: %.2f\n\n", chunk.Importance)
		prompt += chunk.Content + "\n\n"

		if i < len(context.Chunks)-1 {
			prompt += "───────────────────────────────────────────────────────────────\n\n"
		}
	}

	prompt += "═══════════════════════════════════════════════════════════════\n\n"
	prompt += fmt.Sprintf("Context Statistics:\n")
	prompt += fmt.Sprintf("  • Total chunks: %d\n", len(context.Chunks))
	prompt += fmt.Sprintf("  • Total tokens: %d\n", context.TotalTokens)
	prompt += fmt.Sprintf("  • Files covered: %d\n\n", len(context.Sources))

	prompt += fmt.Sprintf("User Question: %s\n\n", userQuery)
	prompt += "Provide a concise, accurate answer based on the context above."

	return prompt
}
