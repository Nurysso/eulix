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
	"bufio"
	"encoding/json"
	"eulix/internal/config"
	"eulix/internal/utils"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	"unicode"

	"gonum.org/v1/gonum/blas/blas32"
)

// Close cleans up resources used by ContextBuilder
func (cb *ContextBuilder) Close() error {
	if cb.debugLog != nil {
		cb.debugLog.Log("Closing ContextBuilder...")
		cb.debugLog.Close()
	}
	return nil
}
func getHeapAlloc() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// GetLastTrace returns the most recent DebugTrace from the last query execution.
// Thread-safe via mutex protection.
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
func (cb *ContextBuilder) writeContextToFile(ctx *utils.ContextWindow) error {
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
			return nil
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return fmt.Errorf("skipping key %q: %w", key, err)
		}
	}
	return fmt.Errorf("key %q not found", target)
}

func ApplyPreMMRFloor(candidates []ScoredChunk, cfg *config.RetrievalConfig) []ScoredChunk {
	if len(candidates) == 0 {
		return candidates
	}

	// Find the maximum score among all current candidates
	maxScore := 0.0
	for _, c := range candidates {
		if c.Score > maxScore {
			maxScore = c.Score
		}
	}

	// Compute cutoff floor using configured ratio
	cutoff := maxScore * float64(cfg.PreMMRScoreFloorRatio)

	filtered := make([]ScoredChunk, 0, len(candidates))
	for _, c := range candidates {
		if c.Score >= cutoff {
			filtered = append(filtered, c)
		}
	}

	return filtered
}

// NewDebugLogger creates a thread-safe debug logger writing to eulixDir/debug/context_debug.log.
// If file creation fails, returns a silent (no-op) logger rather than panicking.
func NewDebugLogger(eulixDir string) *DebugLogger {
	logPath := filepath.Join(eulixDir, "debug", "context_debug.log")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return &DebugLogger{} // silent fallback
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return &DebugLogger{} // silent fallback
	}

	return &DebugLogger{
		file:   f,
		writer: bufio.NewWriterSize(f, 64*1024), // 64KB buffer
	}
}

// Log writes a timestamped debug message to the log file.
// Thread-safe via mutex. Silent fallback if file is nil.
// Automatically appends newline if not present.
//
// Format: [HH:MM:SS] <formatted message>
func (d *DebugLogger) Log(format string, args ...interface{}) {
	if d.file == nil || d.closed {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	msg := fmt.Sprintf("[%s] ", time.Now().Format("15:04:05.000"))
	msg += fmt.Sprintf(format, args...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	_, _ = d.file.WriteString(msg)
	if d.writer.Buffered() > 50*1024 {
		_ = d.writer.Flush()
	}
}

// Flush forces all buffered logs to disk
func (d *DebugLogger) Flush() {
	if d.file == nil || d.closed {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.writer != nil {
		_ = d.writer.Flush()
	}
}

// Close flushes and closes the logger
func (d *DebugLogger) Close() {
	if d.file == nil || d.closed {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.writer != nil {
		_ = d.writer.Flush()
	}
	if d.file != nil {
		_ = d.file.Close()
	}
	d.closed = true
}

func (cb *ContextBuilder) safeExecute(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			cb.debugLog.Log("PANIC RECOVERED: %v\nStack trace: %s", r, debug.Stack())
			cb.debugLog.Flush() // Force flush on panic
			// Re-panic if you want the program to still crash
			panic(r)
		}
	}()
	fn()
}

// StartAutoFlush starts a goroutine that periodically flushes logs to disk
func (d *DebugLogger) StartAutoFlush(interval time.Duration) {
	if d.file == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			d.Flush()
		}
	}()
}

func (cb *ContextBuilder) setupSignalHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		cb.debugLog.Log("Received shutdown signal, flushing logs...")
		cb.debugLog.Close()
		os.Exit(0)
	}()
}

// cosineSimilarity computes normalized dot product of two vectors.
// Returns cosine distance in [0, 1] for normalized vectors, or 0 if either is zero.
func cosineSimilarity(a, b []float32) float64 {
	n := len(a)
	if n != len(b) || n == 0 {
		return 0
	}
	_ = b[n-1]
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na * nb))
}

// dotProduct computes the dot product of two equal-length float32 vectors.
// Callers MUST guarantee both vectors are pre-normalized to unit L2 norm —
// this does no normalization itself. For unit vectors, dot product IS
// cosine similarity, without paying for two sqrt calls and two extra
// accumulators per call.
func dotProduct(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	return blas32.Dot(
		blas32.Vector{N: len(a), Data: a, Inc: 1},
		blas32.Vector{N: len(b), Data: b, Inc: 1},
	)
}

// Normalize scales v in-place to unit L2 norm. Sum-of-squares accumulates
// in float64 for precision; the final scale pass is float32. No-op on a
// zero vector, so a zero vector stays zero and always dot-products to 0
// rather than producing NaN.
func Normalize(v []float32) {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	if sumSq == 0 {
		return
	}
	invNorm := float32(1.0 / math.Sqrt(sumSq))
	for i := range v {
		v[i] *= invNorm
	}
}

// extractQueryKeywords tokenizes a lowercased query and filters stop words.
// Returns keywords with length > 2 (to exclude "a", "is", etc.).
// Splits on whitespace and punctuation; also splits snake_case identifiers.
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
//   - PascalCase: ≥2 uppercase letters
//
// Plain words like "how", "does", "work" return false.
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

func byScoreDesc(a, b ScoredChunk) int {
	switch {
	case a.Score > b.Score:
		return -1
	case a.Score < b.Score:
		return 1
	default:
		return 0
	}
}

// hydrationCache memoizes lazily-loaded chunk content for the lifetime of
// one multiStrategySearch call, so grep/keyword/BM25-proximity don't each
// pay for hydrating the same chunk.
type hydrationCache struct {
	cb    *ContextBuilder
	cache map[string]string
}

func newHydrationCache(cb *ContextBuilder) *hydrationCache {
	return &hydrationCache{cb: cb, cache: make(map[string]string, 64)}
}

func (h *hydrationCache) content(c *Chunk) string {
	if c.Content != "" {
		return c.Content
	}
	if v, ok := h.cache[c.ID]; ok {
		return v
	}
	v := h.cb.hydrateOne(*c)
	h.cache[c.ID] = v
	return v
}

// maxContentFallbackHydrations bounds worst-case I/O when a query has
// no metadata hits at all on a large corpus -- without this, every chunk
// falls through to the (expensive) content-scan branch.
const maxContentFallbackHydrations = 500
