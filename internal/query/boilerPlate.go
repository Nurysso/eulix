//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
This file Identifies common/non-distinctive symbols
(like "ctx", "err", "i", "j") that appear too frequently across code chunks,
making them useless for distinguishing between different pieces of code.
*/

package query

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"strings"

	"eulix/internal/types"
)

// adaptiveDFThreshold scales the document-frequency cutoff with
// corpus size. Large corpora need a lower threshold because truly
// universal symbols (err, ctx, ret) spread across many files but
// still don't hit 30% of 765k chunks they'd need to appear in
// 229k distinct chunks.
//
//	< 10k  chunks → 0.30 (avoids false positives on small repos)
//	< 50k  chunks → 0.15
//	< 200k chunks → 0.05
//	≥ 200k chunks → 0.02  (kernel-scale: filter anything in ≥2% of chunks)
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
// an adaptive threshold. This is the streaming-path entry point
// the non-streaming path uses buildBoilerplateFromKB which has
// access to the full KnowledgeBaseRef and can collect richer
// symbol sets (params, callees) per chunk.
//
// Operates on cb.chunks directly so it works with the streaming
// loader (which doesn't keep the full KB struct). Symbol set used
// is whatever addChunksFromFile puts in Chunk.Symbols typically
// the function/class name plus any param and callee names the
// streaming helper extracts.
func (cb *ContextBuilder) buildBoilerplate() {
	threshold := adaptiveDFThreshold(len(cb.chunks))
	cb.debugLog.Log("Boilerplate: corpus=%d, threshold=%.3f", len(cb.chunks), threshold)
	cb.boilerplate = NewBoilerplateDetector(cb.chunks, threshold, bpMinChunks)
	cb.debugLog.Log("Boilerplate detector: %d symbols filtered top: %v",
		len(cb.boilerplate.boilerplate),
		cb.boilerplate.TopBoilerplate(10),
	)
}

// buildBoilerplateFromKB is the non-streaming path: walks the full
// KnowledgeBaseRef to collect richer symbol sets per chunk
// (params, callee names) for boilerplate purposes. Each chunk in
// cb.chunks currently only carries fn.Name as its Symbols slice;
// for boilerplate we want all identifiers — params, local vars,
// callee names — so we rebuild from the KB directly.
func (cb *ContextBuilder) buildBoilerplateFromKB(kb *types.KnowledgeBaseRef) {
	type chunkSyms struct {
		syms []string
	}
	all := make([]chunkSyms, 0, len(cb.chunks))

	for _, fs := range kb.Structure {
		for _, fn := range fs.Functions {
			syms := make([]string, 0, 4+len(fn.Params)+len(fn.Calls))
			syms = append(syms, fn.Name)
			for _, p := range fn.Params {
				syms = append(syms, p.Name)
			}
			for _, c := range fn.Calls {
				syms = append(syms, c.Callee)
			}
			all = append(all, chunkSyms{syms})
		}
		for _, cls := range fs.Classes {
			syms := []string{cls.Name}
			all = append(all, chunkSyms{syms})
			for _, m := range cls.Methods {
				msyms := make([]string, 0, 4+len(m.Params)+len(m.Calls))
				msyms = append(msyms, m.Name)
				for _, p := range m.Params {
					msyms = append(msyms, p.Name)
				}
				for _, c := range m.Calls {
					msyms = append(msyms, c.Callee)
				}
				all = append(all, chunkSyms{msyms})
			}
		}
	}

	// Adaptive threshold: on huge corpora lower the bar so common
	// short identifiers (err, ctx, ret, i, buf) get filtered.
	threshold := adaptiveDFThreshold(len(all))
	cb.debugLog.Log("Boilerplate: corpus=%d, threshold=%.3f", len(all), threshold)

	// Build a synthetic []Chunk just for the detector (no alloc of content).
	synth := make([]Chunk, len(all))
	for i, cs := range all {
		synth[i] = Chunk{Symbols: cs.syms}
	}
	cb.boilerplate = NewBoilerplateDetector(synth, threshold, bpMinChunks)
	cb.debugLog.Log("Boilerplate detector: %d symbols filtered top: %v",
		len(cb.boilerplate.boilerplate),
		cb.boilerplate.TopBoilerplate(10),
	)
}

// NewBoilerplateDetector builds the detector from all chunks in
// the index. dfThreshold is the fraction of chunks a symbol must
// appear in to be considered boilerplate. Good starting values:
//
//	0.20 — aggressive (filters common helpers, short param names)
//	0.30 — balanced   (recommended default)
//	0.50 — conservative (only truly ubiquitous tokens)
//
// minChunks is the minimum corpus size before the detector does
// anything useful; below this it returns no boilerplate (avoids
// false positives on tiny codebases).
//
// Performance: O(total symbols) with a single allocation for the
// per-symbol seen-set via a small slice+sort for chunks with few
// symbols (≤ 8) and a map for larger chunks. Pre-sizes the df map
// to avoid rehashing.
func NewBoilerplateDetector(chunks []Chunk, dfThreshold float64, minChunks int) *BoilerplateDetector {
	d := &BoilerplateDetector{
		boilerplate: make(map[string]bool),
		df:          make(map[string]int, len(chunks)*4), // 4 symbols/chunk average
		totalChunks: len(chunks),
	}
	if len(chunks) < minChunks {
		return d
	}

	// Count how many distinct chunks each symbol appears in
	// (document frequency). For chunks with few symbols, dedup
	// via a sorted slice to avoid the per-chunk map allocation
	// for ~1M chunks the map allocations alone are 50-100 MB
	// of GC pressure.
	const dedupMapThreshold = 8
	for _, c := range chunks {
		syms := c.Symbols
		if len(syms) == 0 {
			continue
		}
		if len(syms) <= dedupMapThreshold {
			// Fast path: small symbol set, sort+dedup inline.
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
		// General path: per-chunk seen map.
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

// IsBoilerplate returns true when the symbol is too common to be
// useful for distinguishing chunks from one another.
func (d *BoilerplateDetector) IsBoilerplate(sym string) bool {
	if d == nil {
		return false
	}
	return d.boilerplate[normalizeSymbol(sym)]
}

// TopBoilerplate returns the n most frequent symbols, useful for
// debugging / tuning the threshold. Uses a min-heap of size n
// instead of a full sort O(N log n) vs O(N log N) for the
// full-corpus case where n is small (typically 5-10) and N is
// potentially 1M+.
func (d *BoilerplateDetector) TopBoilerplate(n int) []string {
	if n <= 0 || len(d.boilerplate) == 0 {
		return nil
	}

	// For small N (smaller than the symbol count), use a
	// min-heap of size n. For large N (>= symbol count),
	// fall back to a full sort.
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

	// Drain heap in reverse so highest-count is first.
	out := make([]string, 0, n)
	for i := h.Len() - 1; i >= 0; i-- {
		p := h.pairs[i]
		out = append(out, fmt.Sprintf("%s (%d/%d)", p.sym, p.count, d.totalChunks))
	}
	return out
}

// topNFullSort sorts all pairs and returns top n. Used when n
// >= number of boilerplate symbols (i.e., caller asked for "all").
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

// topNPair is a (symbol, count) pair for the min-heap.
type topNPair struct {
	sym   string
	count int
}

// topNHeap is a min-heap of topNPair ordered by count (smallest
// at the root). Implemented via container/heap.Interface.
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

// normalizeSymbol lowercases and strips language punctuation so
// that "ctx", "Ctx", "ctx," all map to the same key.
//
// Fast path: if the symbol is already a clean identifier (alnum
// + underscore, no leading/trailing space, length > 1), we
// just lowercase it and return without going through the
// strings.Replacer. This is the common case for Go identifiers
// and skips the per-call allocation cost of the replacer.
//
// Extend the replacer for languages you care about.
var symReplacer = strings.NewReplacer(
	"*", "", "&", "", ",", "", ";", "",
	"(", "", ")", "", "[", "", "]", "",
	"{", "", "}", "",
)

func normalizeSymbol(s string) string {
	// Trim first — leading/trailing space is the most common
	// reason normalizeSymbol gets called on dirty input.
	s = strings.TrimSpace(s)
	if len(s) <= 1 {
		return ""
	}
	// Fast path: clean identifier, no punctuation to strip.
	if isCleanIdent(s) {
		// ASCII fast path: avoid ToLower allocation if already lowercase.
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
	// Slow path: strip punctuation, then lowercase.
	s = symReplacer.Replace(s)
	return strings.ToLower(s)
}

// isCleanIdent returns true if s consists only of [A-Za-z0-9_]
// and is non-empty. Used as a fast path in normalizeSymbol.
func isCleanIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_') {
			return false
		}
	}
	return true
}

// BuildBoilerplateDetector is a public API: call this after
// loading your chunk index, before serving queries. Uses the
// given dfThreshold; pass 0 to use the default 0.30.
func (cb *ContextBuilder) BuildBoilerplateDetector(dfThreshold float64) {
	// 50 chunks minimum before we trust the statistics
	if dfThreshold <= 0 {
		dfThreshold = adaptiveDFThreshold(len(cb.chunks))
	}
	cb.boilerplate = NewBoilerplateDetector(cb.chunks, dfThreshold, 50)
	cb.debugLog.Log("Boilerplate detector built: %d symbols filtered (top: %v)",
		len(cb.boilerplate.boilerplate),
		cb.boilerplate.TopBoilerplate(5))
}

// isBoilerplateSymbol is the method referenced in simBetween and
// the symbol index construction pass.
func (cb *ContextBuilder) isBoilerplateSymbol(s string) bool {
	if cb.boilerplate == nil {
		return false // detector not built → treat nothing as boilerplate
	}
	return cb.boilerplate.IsBoilerplate(s)
}
