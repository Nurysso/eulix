// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package utils

import "testing"

func TestSplitReasoningAndAnswer(t *testing.T) {
	cases := []struct {
		name, raw, reasoning, answer string
	}{
		{"both tags", "<reasoning>think</reasoning><answer>42</answer>", "think", "42"},
		{"no answer tag: rest is the answer", "<thinking>hmm</thinking>\n  plain reply  ", "hmm", "plain reply"},
		{"untagged response", "  just text  ", "", "just text"},
		{"blocks joined, empty ones dropped", "<reasoning>a</reasoning><thinking> </thinking><reasoning>b</reasoning>x", "a\n\nb", "x"},
		{"tags ignore case, spaces and newlines", "< Reasoning >line1\nline2</ REASONING >\n<ANSWER>\nyes\n</ answer >", "line1\nline2", "yes"},
		{"text outside the answer tag is dropped", "intro <answer>x</answer> outro", "", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reasoning, answer := SplitReasoningAndAnswer(tc.raw)
			if reasoning != tc.reasoning || answer != tc.answer {
				t.Fatalf("SplitReasoningAndAnswer(%q) = (%q, %q), want (%q, %q)",
					tc.raw, reasoning, answer, tc.reasoning, tc.answer)
			}
		})
	}
}
