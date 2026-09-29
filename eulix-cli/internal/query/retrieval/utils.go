//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides the context window builder and query routing for Eulix's
RAG (Retrieval-Augmented Generation) system.

This file contains utility functions for tokenization, text processing, binary
parsing, debug logging, and math operations used throughout the context builder.
*/
package retrieval

import (
	"encoding/json"
	"eulix/internal/config"
	"eulix/internal/utils"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"gonum.org/v1/gonum/blas/blas32"
)

var debugLevel3 = utils.IsDebugLevel3()

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

// fillRelevance normalizes each candidate's score into roughly [0,1] so the
// MMR redundancy term (also [0,1]) doesn't get drowned out by 300-point exact
// hits. Uses log1p so ordering is preserved but outliers don't flatten the rest.
// Blends in cosine similarity when embeddings are available, and adds a small
// bonus for chunks in files the query mentioned by name.
func fillRelevance(cs []mmrCand, qEmb []float32, anchorFiles map[string]bool) {
	maxLog := 0.0
	for i := range cs {
		if l := math.Log1p(math.Max(cs[i].sc.Score, 0)); l > maxLog {
			maxLog = l
		}
	}
	if maxLog == 0 {
		maxLog = 1
	}

	useSem := mmrSemanticWeight > 0 && len(qEmb) > 0
	var sims []float64
	var hasSim []bool
	lo, hi := math.MaxFloat64, -math.MaxFloat64
	if useSem {
		sims = make([]float64, len(cs))
		hasSim = make([]bool, len(cs))
		for i := range cs {
			if cs[i].emb == nil {
				continue
			}
			s := float64(dotProduct(qEmb, cs[i].emb))
			sims[i], hasSim[i] = s, true
			lo, hi = math.Min(lo, s), math.Max(hi, s)
		}
		useSem = hi > lo
	}

	for i := range cs {
		rel := math.Log1p(math.Max(cs[i].sc.Score, 0)) / maxLog
		if useSem && hasSim[i] {
			sem := (sims[i] - lo) / (hi - lo)
			rel = (1-mmrSemanticWeight)*rel + mmrSemanticWeight*sem
		}
		if anchorFiles[cs[i].sc.File] {
			rel += anchorFileBonus // additive: the old min(1, x*1.25) capped anchors BELOW everything once scores were >1
		}
		cs[i].rel = rel
	}
}

func joinContent(a, b string) string {
	if a == "" && b == "" {
		return "" // keep "empty" meaning "needs hydration"
	}
	return a + "\n" + b
}

func spliceOverlap(first, second Chunk) string {
	sl := strings.Split(second.Content, "\n")
	skip := first.EndLine - second.StartLine + 1
	if second.Content != "" && len(sl) == second.EndLine-second.StartLine+1 && skip > 0 && skip < len(sl) {
		return first.Content + "\n" + strings.Join(sl[skip:], "\n")
	}
	return first.Content
}

func unionStrings(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, s := range list {
			if _, ok := seen[s]; !ok {
				seen[s] = struct{}{}
				out = append(out, s)
			}
		}
	}
	return out
}

func (cb *ContextBuilder) nonBoilerplate(syms []string) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		if !cb.isBoilerplateSymbol(s) {
			out = append(out, s)
		}
	}
	return out
}

// mmrWatchMatch reports whether the debug watch substring matches any of the
// identifying fields of a candidate. Previously this only checked File, which
// meant EULIX_MMR_WATCH=resolve_expression silently matched nothing (method
// names live in ID and Name, not in the file path). Matches ID, Name, and File.
// Empty watch string -> always false (watch logging disabled).
func mmrWatchMatch(id, name, file string) bool {
	if debugWatchSubstr == "" {
		return false
	}
	return strings.Contains(id, debugWatchSubstr) ||
		strings.Contains(name, debugWatchSubstr) ||
		strings.Contains(file, debugWatchSubstr)
}

// writeContextToFile serializes a ContextWindow to a debug file.
// Uses timestamp in filename for uniqueness.
// Intended for offline analysis; not used in production path.
func (cb *ContextBuilder) writeContextToFile(ctx *utils.ContextWindow) error {
	logDir := filepath.Join(cb.config.Project.Path, utils.EulixDir, "debug", "retrieval")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	fileName := fmt.Sprintf("retrieval_debug_%s.txt", time.Now().Format("20060102_150405"))
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

// cosineSimilarity computes normalized dot product of two vectors.
// Returns cosine distance in [0, 1] for normalized vectors, or 0 if either is zero.
//
//nolint:unused
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

// Helper to prevent unnecessary memory allocations in strings.ToLower
func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
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

	prevLower := false
	upperCount := 0

	// Single pass for both camelCase and PascalCase/Acronyms
	for _, r := range w {
		isUpper := unicode.IsUpper(r)
		if isUpper {
			upperCount++
			// camelCase transition (e.g., aB)
			if prevLower {
				return true
			}
		}
		prevLower = unicode.IsLower(r)
	}

	// PascalCase with multiple capitals or acronyms
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
