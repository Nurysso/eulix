// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package utils

import "testing"

func TestCountTokens(t *testing.T) {
	if got := CountTokens("", "openai"); got != 0 {
		t.Fatalf("empty content: got %d, want 0", got)
	}
	if got := CountTokens("0123456789", "ollama"); got != 2 {
		t.Fatalf("unknown provider should fall back to len/4: got %d, want 2", got)
	}

	// 43 bytes, so the len/4 fallback would give 10; cl100k gives 12.
	const code = `func main() { fmt.Println("hello, world") }`
	for _, provider := range []string{"openai", "anthropic", "openrouter", "  OpenAI "} {
		if got := CountTokens(code, provider); got != 12 {
			t.Errorf("provider %q should use cl100k: got %d, want 12", provider, got)
		}
	}
}
