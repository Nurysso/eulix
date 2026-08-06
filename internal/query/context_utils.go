//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides the context window builder and query routing for Eulix's
RAG (Retrieval-Augmented Generation) system.

This file contains utility functions for tokenization, text processing, binary
parsing, debug logging, and math operations used throughout the context builder.
*/
package query

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"eulix/internal/types"
)

// Close is a no-op placeholder for resource cleanup.
// Kept for interface compatibility.
func (cb *ContextBuilder) Close() error { return nil }

func getHeapAlloc() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// GetLastTrace returns the most recent DebugTrace from the last query execution.
// Thread-safe via mutex protection.
//
// See:
//   - multiStrategySearch: Populates trace with strategy results
//   - context_builder.go: DebugTrace storage and lifecycle
func (cb *ContextBuilder) GetLastTrace() *DebugTrace {
	cb.mu.Lock()
	defer func() {
		cb.mu.Unlock()
	}()
	return cb.lastTrace
}

// writeContextToFile serializes a ContextWindow to a debug file.
// Uses timestamp in filename for uniqueness.
// Intended for offline analysis; not used in production path.
func (cb *ContextBuilder) writeContextToFile(ctx *types.ContextWindow) error {
	logDir := filepath.Join(cb.config.Project.Path, ".eulix", "debug", "retrieval")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	fileName := fmt.Sprintf("retrival_debug_%s.txt", time.Now().Format("20060102_150405"))
	logPath := filepath.Join(logDir, fileName)
	f, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "%+v\n", ctx)
	return err
}

func jsonSkipToKey(dec *json.Decoder, target string) error {
	// Consume the opening '{' of the top-level object
	if _, err := dec.Token(); err != nil {
		return err
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected string key, got %T", tok)
		}
		if key == target {
			return nil // dec is now positioned just before the value
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return fmt.Errorf("skipping key %q: %w", key, err)
		}
	}
	return fmt.Errorf("key %q not found", target)
}

// NewDebugLogger creates a thread-safe debug logger writing to eulixDir/context_debug.log.
// If file creation fails, returns a silent (no-op) logger rather than panicking.
func NewDebugLogger(eulixDir string) *DebugLogger {
	logPath := filepath.Join(eulixDir, "debug", "context_debug.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return &DebugLogger{} // silent fallback
	}
	return &DebugLogger{file: f}
}

// Log writes a timestamped debug message to the log file.
// Thread-safe via mutex. Silent fallback if file is nil.
// Automatically appends newline if not present.
//
// Format: [HH:MM:SS] <formatted message>
func (d *DebugLogger) Log(format string, args ...interface{}) {
	if d.file == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	msg := fmt.Sprintf("[%s] ", time.Now().Format("15:04:05"))
	msg += fmt.Sprintf(format, args...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	_, _ = d.file.WriteString(msg)
}

// Close closes the debug log file.
// Safe to call multiple times (checks for nil).
func (d *DebugLogger) Close() error {
	if d.file != nil {
		return d.file.Close()
	}
	return nil
}

// cosineSimilarity computes normalized dot product of two vectors.
// Returns cosine distance in [0, 1] for normalized vectors, or 0 if either is zero.
//
// Formula: (a · b) / (‖a‖ * ‖b‖)
//
// Used for:
//   - Vector embedding comparison (vectorSearch, vectorSearchIVF)
//   - Centroid distance in k-means clustering (buildIVFIndex)
//
// Edge cases:
//   - Mismatched lengths: return 0 (invalid input)
//   - Zero vectors: return 0 (avoid division by zero)
//
// See:
//   - vectorSearch: Brute-force nearest-neighbor search
//   - vectorSearchIVF: IVF cluster probing
//   - buildIVFIndex: Centroid assignment in k-means
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// extractQueryKeywords tokenizes a lowercased query and filters stop words.
// Returns keywords with length > 2 (to exclude "a", "is", etc.).
// Splits on whitespace and punctuation; also splits snake_case identifiers.
//
// Stop words (filtered):
// Common English: how, does, the, a, an, is, are, what, where, when, etc.
// Prepositions: of, in, on, at, to, for, with
// Pronouns: this, that, these, those
// Modals: can, will, should, would, could
//
// Snake_case splitting:
//   - "load_chunks" → ["load", "chunks"]
//   - Applies stop-word filter to each part
//
// Example:
//
//	"how does load_chunks work" → ["load", "chunks"] (filters "how", "does", "work")
//
// See:
//   - keywordSearch / invertedKeywordSearch: Uses extracted keywords
//   - extractPotentialSymbols: Complementary to this; extracts code identifiers
func extractQueryKeywords(queryLower string) []string {
	stop := map[string]bool{
		"how": true, "does": true, "the": true, "a": true, "an": true,
		"is": true, "are": true, "what": true, "where": true, "when": true,
		"can": true, "will": true, "should": true, "would": true, "could": true,
		"this": true, "that": true, "these": true, "those": true, "of": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "with": true,
	}
	words := strings.FieldsFunc(queryLower, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '!' || r == '?' ||
			r == ';' || r == ':' || r == '(' || r == ')' || r == '[' || r == ']'
	})
	kws := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.Trim(w, "\"'")
		if len(w) > 2 && !stop[w] {
			kws = append(kws, w)
			if strings.Contains(w, "_") {
				for _, p := range strings.Split(w, "_") {
					if len(p) > 2 && !stop[p] {
						kws = append(kws, p)
					}
				}
			}
		}
	}
	return kws
}

// splitIdentifierToTokens decomposes camelCase and snake_case identifiers into tokens.
// Handles: snake_case, camelCase, PascalCase.
//
// Algorithm:
//  1. Split on underscores: "build_context" → ["build", "context"]
//  2. For each part, split on uppercase transitions:
//     - "BuildContext" → ["build", "context"]
//     - "buildContext" → ["build", "context"]
//     - "IOUtils" → ["i", "o", "utils"]
//  3. Lowercase all tokens
//
// Examples:
//   - "loadChunksFromKB" → ["load", "chunks", "from", "kb"]
//   - "kb_index_cache" → ["kb", "index", "cache"]
//   - "HTTPSConnection" → ["https", "connection"]
//
// See:
//   - partialIdentifierMatch: Uses to compare query tokens with chunk tokens
//   - extractPotentialSymbols: Uses to expand single symbols into subtokens
func splitIdentifierToTokens(s string) []string {
	toks := []string{}
	for _, part := range strings.Split(s, "_") {
		start := 0
		for i := 1; i < len(part); i++ {
			if unicode.IsUpper(rune(part[i])) {
				if tok := strings.ToLower(part[start:i]); tok != "" {
					toks = append(toks, tok)
				}
				start = i
			}
		}
		if tok := strings.ToLower(part[start:]); tok != "" {
			toks = append(toks, tok)
		}
	}
	return toks
}

// isCodeIdentifier returns true if word looks like a source-code identifier
// rather than plain English. Matches:
//   - snake_case: contains "_" and length > 3 (load_chunks, KB_INDEX)
//   - camelCase: lowercase→uppercase transition (buildContext, mmrSelect)
//   - PascalCase: ≥2 uppercase letters (BuildContext, KBIndex, IVFIndex)
//
// Plain words like "how", "does", "work" return false.
//
// Used by extractPotentialSymbols to filter out English words from queries
// and avoid polluting symbol searches with articles, verbs, etc.
//
// Examples:
//   - "BuildContext" → true (PascalCase, 2 uppercase)
//   - "loadChunks" → true (camelCase transition at 'C')
//   - "kb_index" → true (snake_case)
//   - "how" → false (plain English)
//   - "does" → false (plain English)
//   - "work" → false (plain English)
//
// See:
//   - extractPotentialSymbols: Uses this for filtering
func isCodeIdentifier(w string) bool {
	// snake_case: load_chunks, kb_index, QUERY_BATCH_SIZE
	if strings.Contains(w, "_") && len(w) > 3 {
		return true
	}
	// camelCase: buildContext, mmrSelect, loadChunksFromKB
	prevLower := false
	for _, r := range w {
		if unicode.IsUpper(r) && prevLower {
			return true
		}
		prevLower = unicode.IsLower(r)
	}
	// PascalCase with multiple capitals: BuildContext, IVFIndex, KBIndex
	upperCount := 0
	for _, r := range w {
		if unicode.IsUpper(r) {
			upperCount++
		}
	}
	return upperCount >= 2

}

// extractPotentialSymbols extracts tokens that look like code identifiers from
// the query. Plain English words are excluded so that queries like
// "how does BuildContext work" don't pollute symbol searches with "how", "does",
// and "work".
//
// Algorithm:
//  1. Split query on whitespace
//  2. Strip punctuation: .,!?;:'\"()[]{}
//  3. Filter by length > 2
//  4. Filter by isCodeIdentifier (snake_case, camelCase, PascalCase, ≥2 uppercase)
//  5. For each symbol, add both whole symbol and subtokens (splitIdentifierToTokens)
//  6. Deduplicate via uniqueStrings
//
// Example:
//
//	"how does BuildContext load_chunks work"
//	  → matches: "BuildContext", "load_chunks"
//	  → subtokens: "build", "context", "load", "chunks"
//	  → filters out: "how", "does", "work" (plain English)
//
// See:
//   - exactSymbolSearch: Uses symbols for direct chunk name/symbol matching
//   - partialIdentifierMatch: Uses symbols for token-level matching
//   - kbExactLookup: Uses symbols for KB lookup
//   - extractQueryKeywords: Complementary extraction for English keywords
func extractPotentialSymbols(query string) []string {
	syms := make([]string, 0)
	for _, w := range strings.Fields(query) {
		w = strings.Trim(w, ".,!?;:'\"()[]{}")
		if len(w) <= 2 || !isCodeIdentifier(w) {
			continue
		}
		syms = append(syms, w)
		syms = append(syms, splitIdentifierToTokens(w)...)
	}
	return uniqueStrings(syms)
}

// uniqueStrings removes duplicates from a string slice, preserving first-seen order.
// Also filters out empty strings.
//
// See:
//   - extractPotentialSymbols: Uses to deduplicate symbols and subtokens
//   - partialIdentifierMatch: Uses to deduplicate matched tokens
//   - invertedKeywordSearch: Uses to deduplicate terms before lookup
func uniqueStrings(input []string) []string {
	seen := make(map[string]bool, len(input))
	out := make([]string, 0, len(input))
	for _, s := range input {
		if !seen[s] && s != "" {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
