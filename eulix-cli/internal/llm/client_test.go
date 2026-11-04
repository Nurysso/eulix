// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eulix/internal/config"
	"eulix/internal/utils"
)

type request struct {
	path, query string
	header      http.Header
	body        map[string]any
}

// fakeLLM answers every request with status/reply and hands the request it
// saw back over a channel (which also orders it before the test reads it).
func fakeLLM(t *testing.T, status int, reply string) (string, <-chan request) {
	t.Helper()
	seen := make(chan request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := request{path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone()}
		_ = json.NewDecoder(r.Body).Decode(&req.body)
		seen <- req
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, seen
}

func newClient(t *testing.T, l config.LLMConfig) *Client {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.LLM = l
	cfg.Project.Path = t.TempDir()
	c, err := MouthClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name    string
		llm     config.LLMConfig
		wantErr string // "" means valid
	}{
		{"anthropic needs a key", config.LLMConfig{Provider: "anthropic", Model: "m"}, "api_key"},
		{"gemini needs a key", config.LLMConfig{Provider: "Gemini", Model: "m"}, "api_key"},
		{"ollama needs no key", config.LLMConfig{Provider: "ollama", Model: "m"}, ""},
		{"self-hosted openai-compatible needs no key", config.LLMConfig{Provider: "vllm", Model: "m"}, ""},
		{"blank provider in local mode is ollama", config.LLMConfig{Local: true, Model: "m"}, ""},
		{"unknown provider with a base URL is openai-compatible", config.LLMConfig{Provider: "custom", BaseURL: "http://h", Model: "m"}, ""},
		// the hint must name the key eulix.toml actually reads
		{"unknown provider without a base URL", config.LLMConfig{Provider: "custom", Model: "m"}, "set baseURL"},
		{"model is required", config.LLMConfig{Provider: "ollama"}, "model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfig(&config.Config{LLM: tc.llm})
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("validateConfig = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestProviderWireFormats(t *testing.T) {
	const (
		anthropicReply = `{"content":[{"type":"text","text":"hi"}]}`
		openAIReply    = `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`
		ollamaReply    = `{"message":{"role":"assistant","content":"hi"},"done":true}`
		geminiReply    = `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`
	)
	cases := []struct {
		name  string
		llm   config.LLMConfig
		reply string
		path  string
		check func(t *testing.T, r request)
	}{
		{"anthropic", config.LLMConfig{Provider: "anthropic", Model: "claude-x", APIKey: "sk-a", MaxTokens: 100, Temperature: 0.2}, anthropicReply, "/v1/messages",
			func(t *testing.T, r request) {
				if r.header.Get("x-api-key") != "sk-a" || r.header.Get("anthropic-version") == "" || r.body["max_tokens"] != 100.0 || r.body["model"] != "claude-x" {
					t.Errorf("headers=%v body=%v", r.header, r.body)
				}
			}},
		{"openai-compatible", config.LLMConfig{Provider: "groq", Model: "m", APIKey: "sk-g", MaxTokens: 10}, openAIReply, "/v1/chat/completions",
			func(t *testing.T, r request) {
				if r.header.Get("Authorization") != "Bearer sk-g" {
					t.Errorf("Authorization = %q", r.header.Get("Authorization"))
				}
			}},
		{"self-hosted sends no auth header", config.LLMConfig{Provider: "vllm", Model: "m", MaxTokens: 10}, openAIReply, "/v1/chat/completions",
			func(t *testing.T, r request) {
				if _, ok := r.header["Authorization"]; ok {
					t.Errorf("unexpected Authorization header")
				}
			}},
		{"ollama sends temperature 0", config.LLMConfig{Provider: "ollama", Model: "m", MaxTokens: 10, Temperature: 0}, ollamaReply, "/api/chat",
			func(t *testing.T, r request) {
				opts, _ := r.body["options"].(map[string]any)
				if t0, ok := opts["temperature"]; !ok || t0 != 0.0 || r.body["stream"] != false {
					t.Errorf("a configured temperature of 0 must reach Ollama, body=%v", r.body)
				}
			}},
		{"local with blank provider talks to ollama", config.LLMConfig{Local: true, Model: "m", MaxTokens: 10}, ollamaReply, "/api/chat", nil},
		{"gemini keeps the key out of the URL", config.LLMConfig{Provider: "gemini", Model: "gemini-x", APIKey: "g-key", MaxTokens: 10}, geminiReply, "/v1beta/models/gemini-x:generateContent",
			func(t *testing.T, r request) {
				if strings.Contains(r.query, "g-key") || r.header.Get("x-goog-api-key") != "g-key" {
					t.Errorf("query=%q x-goog-api-key=%q", r.query, r.header.Get("x-goog-api-key"))
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, seen := fakeLLM(t, http.StatusOK, tc.reply)
			tc.llm.BaseURL = url + "/" // trailing slash must not double up
			got, err := newClient(t, tc.llm).LlmResponse("hello")
			r := <-seen
			if r.path != tc.path {
				t.Fatalf("POST %s, want %s", r.path, tc.path)
			}
			if err != nil || got != "hi" {
				t.Fatalf("LlmResponse = (%q, %v), want \"hi\"", got, err)
			}
			if tc.check != nil {
				tc.check(t, r)
			}
		})
	}
}

func TestProviderErrors(t *testing.T) {
	cases := []struct {
		provider string
		status   int
		reply    string
		want     []string
	}{
		{"anthropic", http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, []string{"anthropic", "401", "bad key"}},
		{"openai", http.StatusOK, `{"error":{"message":"rate limited"}}`, []string{"rate limited"}},
		{"openai", http.StatusOK, `{"choices":[]}`, []string{"empty response"}},
		{"ollama", http.StatusOK, `{"error":"model not found"}`, []string{"model not found"}},
		{"gemini", http.StatusOK, `{"candidates":[]}`, []string{"empty response"}},
		{"anthropic", http.StatusOK, `not json`, []string{"anthropic"}},
	}
	for _, tc := range cases {
		url, _ := fakeLLM(t, tc.status, tc.reply)
		c := newClient(t, config.LLMConfig{Provider: tc.provider, Model: "m", APIKey: "k", BaseURL: url, MaxTokens: 1})
		_, err := c.LlmResponse("q")
		for _, w := range tc.want {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Errorf("%s %d %s: error %v should contain %q", tc.provider, tc.status, tc.reply, err, w)
			}
		}
	}
}

func TestErrorsDoNotLeakAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens here now: every request fails at connect
	for _, p := range []string{"anthropic", "openai", "gemini"} {
		c := newClient(t, config.LLMConfig{Provider: p, Model: "m", APIKey: "SECRET-KEY", BaseURL: url, MaxTokens: 1})
		if _, err := c.LlmResponse("q"); err == nil || strings.Contains(err.Error(), "SECRET-KEY") {
			t.Errorf("%s: connection error leaks the API key: %v", p, err)
		}
	}
}

func TestResolveURLs(t *testing.T) {
	if got := resolveBaseURL("Groq", ""); got != "https://api.groq.com/openai" {
		t.Errorf("known provider default: %q", got)
	}
	if got := resolveBaseURL("groq", "http://proxy/"); got != "http://proxy" {
		t.Errorf("configured base URL should win, trimmed: %q", got)
	}
	if got := resolveEndpoint("anthropic", "v2/x"); got != "/v2/x" {
		t.Errorf("endpoint without a leading slash: %q", got)
	}
	if got := resolveBaseURL("unknown", "") + resolveEndpoint("unknown", ""); got != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("unknown provider fallback: %q", got)
	}
}

func TestBuildFullPrompt(t *testing.T) {
	ctx := &utils.ContextWindow{
		Chunks: []utils.ContextChunk{
			{File: "deps.json", Content: "[METADATA] deps"},
			{File: "a.go", StartLine: 1, EndLine: 5, Content: "func a() {}", Importance: 0.9},
			{File: "empty.go", Content: "  \n"},
		},
		TotalTokens: 42,
		Sources:     []string{"a.go", "deps.json"},
	}
	want := "Context: 1 source chunks, 1 metadata, 42 tokens across 2 files\n\n" +
		"── SOURCE ──\n\n[1] a.go:1-5 (score 0.90)\nfunc a() {}\n\n" +
		"── METADATA ──\n\n[M1] deps.json:0-0\n[METADATA] deps\n\n" +
		"QUERY\nwhat is a?\n" +
		"\nbe brief"
	c := &Client{config: config.DefaultConfig()}
	if got := c.BuildFullPrompt(ctx, "what is a?", "be brief"); got != want {
		t.Fatalf("prompt:\n%q\nwant:\n%q", got, want)
	}
}

func TestDebugLogRecordsPromptNotKey(t *testing.T) {
	url, _ := fakeLLM(t, http.StatusOK, `{"choices":[{"message":{"content":"hi"}}]}`)
	c := newClient(t, config.LLMConfig{Provider: "openai", Model: "m", APIKey: "SECRET-KEY", BaseURL: url, MaxTokens: 1})
	c.config.Project.DebugConfig = true
	if _, err := c.LlmResponse("the prompt"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(c.config.Project.Path, utils.EulixDir, "debug", "llmdebug.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "the prompt") || strings.Contains(string(data), "SECRET-KEY") {
		t.Fatalf("debug log should hold the prompt and never the key:\n%s", data)
	}
}
