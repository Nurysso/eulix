//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package utils provides Shared type and func accross project

package utils

import (
	"regexp"
	"strings"
)

// ContextChunk represents a piece of code with metadata
type ContextChunk struct {
	File       string
	StartLine  int
	EndLine    int
	Content    string
	Importance float64
	Tokens     int
}

// ContextWindow represents the full context for a query
type ContextWindow struct {
	Chunks      []ContextChunk
	TotalTokens int
	Sources     []string
}

// reasoningTagRe / answerTagRe pull the model's <reasoning>/<thinking> and
// <answer> blocks apart. Shared here so the app.go's show-reason
// command, and the TUI all agree on exactly the same parsing.
var (
	reasoningTagRe = regexp.MustCompile(`(?is)<\s*(?:reasoning|thinking)\s*>(.*?)<\s*/\s*(?:reasoning|thinking)\s*>`)
	answerTagRe    = regexp.MustCompile(`(?is)<\s*answer\s*>(.*?)<\s*/\s*answer\s*>`)
)

// SplitReasoningAndAnswer pulls a <reasoning>/<thinking> block and an
// <answer> block out of a raw model response. If there's no <answer> tag
// at all, the whole response (minus any reasoning block) is treated as
// the answer, so untagged responses still split cleanly.
func SplitReasoningAndAnswer(raw string) (reasoning, answer string) {
	var reasoningParts []string
	for _, m := range reasoningTagRe.FindAllStringSubmatch(raw, -1) {
		if trimmed := strings.TrimSpace(m[1]); trimmed != "" {
			reasoningParts = append(reasoningParts, trimmed)
		}
	}
	reasoning = strings.Join(reasoningParts, "\n\n")

	if m := answerTagRe.FindStringSubmatch(raw); m != nil {
		answer = strings.TrimSpace(m[1])
		return reasoning, answer
	}

	answer = strings.TrimSpace(reasoningTagRe.ReplaceAllString(raw, ""))
	return reasoning, answer
}
