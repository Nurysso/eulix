package utils

import (
	"strings"

	"github.com/tiktoken-go/tokenizer"
)

// CountTokens estimates the number of tokens in a string based on the provider.
func CountTokens(content, provider string) int {
	if content == "" {
		return 0
	}
	
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "openai", "anthropic", "openrouter":
		// Use tiktoken for OpenAI-like tokenization
		enc, err := tokenizer.Get(tokenizer.Cl100kBase)
		if err == nil {
			ids, _, _ := enc.Encode(content)
			return len(ids)
		}
	}
	
	// Fallback heuristic: 1 token ~= 4 characters
	return len(content) / 4
}
