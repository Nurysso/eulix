//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
This file identifies common/non-distinctive symbols (like "ctx", "err", "i", "j")
that appear too frequently across code chunks, making them non-distinctive for
code search and indexing.
*/

package query

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"strings"
)

// adaptiveDFThreshold scales the document-frequency cutoff with corpus size.
//
//	< 10k  chunks → 0.30
//	< 50k  chunks → 0.15
//	< 200k chunks → 0.05
//	≥ 200k chunks → 0.02
func adaptiveDFThreshold(n int) float64 {
	switch {
	case n < 10_000:
		return 0.30
	case n < 50_000:
		return 0.15
	case n < 200_000:
		return 0.05
	default:
		return 0.02
	}
}

// buildBoilerplate populates cb.boilerplate from cb.chunks using
// an adaptive document frequency threshold.
func (cb *ContextBuilder) buildBoilerplate() {
	threshold := adaptiveDFThreshold(len(cb.chunks))
	cb.debugLog.Log("Boilerplate: corpus=%d, threshold=%.3f", len(cb.chunks), threshold)
	cb.boilerplate = NewBoilerplateDetector(cb.chunks, threshold, bpMinChunks)
	cb.debugLog.Log("Boilerplate detector: %d symbols filtered top: %v",
		len(cb.boilerplate.boilerplate),
		cb.boilerplate.TopBoilerplate(10),
	)
}

// NewBoilerplateDetector builds the detector from all chunks in the index.
// dfThreshold is the fraction of chunks a symbol must appear in to be considered boilerplate.
func NewBoilerplateDetector(chunks []Chunk, dfThreshold float64, minChunks int) *BoilerplateDetector {
	d := &BoilerplateDetector{
		boilerplate: make(map[string]bool),
		df:          make(map[string]int, len(chunks)*4),
		totalChunks: len(chunks),
	}
	if len(chunks) < minChunks {
		return d
	}

	const dedupMapThreshold = 8
	for _, c := range chunks {
		syms := c.Symbols
		if len(syms) == 0 {
			continue
		}
		if len(syms) <= dedupMapThreshold {
			norms := make([]string, 0, len(syms))
			for _, s := range syms {
				if n := normalizeSymbol(s); n != "" {
					norms = append(norms, n)
				}
			}
			if len(norms) == 0 {
				continue
			}
			sort.Strings(norms)
			prev := norms[0]
			d.df[prev]++
			for i := 1; i < len(norms); i++ {
				if norms[i] != prev {
					d.df[norms[i]]++
					prev = norms[i]
				}
			}
			continue
		}

		seen := make(map[string]struct{}, len(syms))
		for _, s := range syms {
			norm := normalizeSymbol(s)
			if norm == "" {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			d.df[norm]++
		}
	}

	cutoff := int(math.Ceil(dfThreshold * float64(len(chunks))))
	for sym, count := range d.df {
		if count >= cutoff {
			d.boilerplate[sym] = true
		}
	}
	return d
}

// IsBoilerplate returns true when the symbol exceeds the document frequency cutoff.
func (d *BoilerplateDetector) IsBoilerplate(sym string) bool {
	if d == nil {
		return false
	}
	return d.boilerplate[normalizeSymbol(sym)]
}

// TopBoilerplate returns formatted string descriptors for the n most frequent boilerplate symbols.
func (d *BoilerplateDetector) TopBoilerplate(n int) []string {
	if n <= 0 || len(d.boilerplate) == 0 {
		return nil
	}

	if n >= len(d.boilerplate) {
		return d.topNFullSort(len(d.boilerplate))
	}

	h := &topNHeap{
		pairs: make([]topNPair, 0, n),
	}
	for sym := range d.boilerplate {
		if h.Len() < n {
			heap.Push(h, topNPair{sym: sym, count: d.df[sym]})
		} else if d.df[sym] > h.pairs[0].count {
			heap.Pop(h)
			heap.Push(h, topNPair{sym: sym, count: d.df[sym]})
		}
	}

	out := make([]string, 0, n)
	for i := h.Len() - 1; i >= 0; i-- {
		p := h.pairs[i]
		out = append(out, fmt.Sprintf("%s (%d/%d)", p.sym, p.count, d.totalChunks))
	}
	return out
}

func (d *BoilerplateDetector) topNFullSort(n int) []string {
	type kv struct {
		sym   string
		count int
	}
	pairs := make([]kv, 0, len(d.boilerplate))
	for sym := range d.boilerplate {
		pairs = append(pairs, kv{sym, d.df[sym]})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].count > pairs[j].count
	})
	out := make([]string, 0, n)
	for i := 0; i < n && i < len(pairs); i++ {
		out = append(out, fmt.Sprintf("%s (%d/%d)", pairs[i].sym, pairs[i].count, d.totalChunks))
	}
	return out
}

type topNPair struct {
	sym   string
	count int
}

type topNHeap struct {
	pairs []topNPair
}

func (h *topNHeap) Len() int { return len(h.pairs) }
func (h *topNHeap) Less(i, j int) bool {
	return h.pairs[i].count < h.pairs[j].count
}
func (h *topNHeap) Swap(i, j int) { h.pairs[i], h.pairs[j] = h.pairs[j], h.pairs[i] }
func (h *topNHeap) Push(x any)    { h.pairs = append(h.pairs, x.(topNPair)) }
func (h *topNHeap) Pop() any {
	old := h.pairs
	n := len(old)
	x := old[n-1]
	h.pairs = old[:n-1]
	return x
}

var symReplacer = strings.NewReplacer(
	"*", "", "&", "", ",", "", ";", "",
	"(", "", ")", "", "[", "", "]", "",
	"{", "", "}", "",
)

// normalizeSymbol trims whitespace, strips punctuation, and lowercases the input symbol.
func normalizeSymbol(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 1 {
		return ""
	}
	if isCleanIdent(s) {
		hasUpper := false
		for i := 0; i < len(s); i++ {
			if s[i] >= 'A' && s[i] <= 'Z' {
				hasUpper = true
				break
			}
		}
		if !hasUpper {
			return s
		}
		return strings.ToLower(s)
	}
	s = symReplacer.Replace(s)
	return strings.ToLower(s)
}

// isCleanIdent returns true if s consists exclusively of ASCII alphanumeric characters or underscores.
func isCleanIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' {
			continue
		}
		return false
	}
	return true
}

// BuildBoilerplateDetector builds the boilerplate detector for ContextBuilder.
// If dfThreshold <= 0, an adaptive threshold based on chunk count is used.
func (cb *ContextBuilder) BuildBoilerplateDetector(dfThreshold float64) {
	if dfThreshold <= 0 {
		dfThreshold = adaptiveDFThreshold(len(cb.chunks))
	}
	cb.boilerplate = NewBoilerplateDetector(cb.chunks, dfThreshold, 50)
	cb.debugLog.Log("Boilerplate detector built: %d symbols filtered (top: %v)",
		len(cb.boilerplate.boilerplate),
		cb.boilerplate.TopBoilerplate(5))
}

func (cb *ContextBuilder) isBoilerplateSymbol(s string) bool {
	if cb.boilerplate == nil {
		return false
	}
	return cb.boilerplate.IsBoilerplate(s)
}
