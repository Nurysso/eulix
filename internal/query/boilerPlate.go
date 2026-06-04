package query

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// NewBoilerplateDetector builds the detector from all chunks in the index.
// dfThreshold is the fraction of chunks a symbol must appear in to be
// considered boilerplate. Good starting values:
//
//	0.20 — aggressive (filters common helpers, short param names)
//	0.30 — balanced   (recommended default)
//	0.50 — conservative (only truly ubiquitous tokens)
//
// minChunks is the minimum corpus size before the detector does anything
// useful; below this it returns no boilerplate (avoids false positives on
// tiny codebases).
func NewBoilerplateDetector(chunks []Chunk, dfThreshold float64, minChunks int) *BoilerplateDetector {
	d := &BoilerplateDetector{
		boilerplate: make(map[string]bool),
		df:          make(map[string]int),
		totalChunks: len(chunks),
	}
	if len(chunks) < minChunks {
		return d
	}

	// Count how many distinct chunks each symbol appears in (document freq).
	for _, c := range chunks {
		seen := make(map[string]bool, len(c.Symbols))
		for _, s := range c.Symbols {
			norm := normalizeSymbol(s)
			if norm == "" {
				continue
			}
			if !seen[norm] {
				d.df[norm]++
				seen[norm] = true
			}
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

// IsBoilerplate returns true when the symbol is too common to be useful
// for distinguishing chunks from one another.
func (d *BoilerplateDetector) IsBoilerplate(sym string) bool {
	return d.boilerplate[normalizeSymbol(sym)]
}

// TopBoilerplate returns the n most frequent symbols, useful for
// debugging / tuning the threshold.
func (d *BoilerplateDetector) TopBoilerplate(n int) []string {
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

// normalizeSymbol lowercases and strips language punctuation so that
// "ctx", "Ctx", "ctx," all map to the same key.
// Extend the replacer for languages you care about.
var symReplacer = strings.NewReplacer(
	"*", "", "&", "", ",", "", ";", "",
	"(", "", ")", "", "[", "", "]", "",
	"{", "", "}", "",
)

func normalizeSymbol(s string) string {
	s = symReplacer.Replace(strings.TrimSpace(s))
	s = strings.ToLower(s)
	// Drop single-character tokens — they're noise in every language
	// (loop vars i/j/k, C pointer *, Go err shorthand, etc.)
	if len(s) <= 1 {
		return ""
	}
	return s
}

// Call this after loading your chunk index, before serving queries:
func (cb *ContextBuilder) BuildBoilerplateDetector(dfThreshold float64) {
	// 50 chunks minimum before we trust the statistics
	cb.boilerplate = NewBoilerplateDetector(cb.chunks, dfThreshold, 50)
	cb.debugLog.Log("Boilerplate detector built: %d symbols filtered (top: %v)",
		len(cb.boilerplate.boilerplate),
		cb.boilerplate.TopBoilerplate(5))
}

// isBoilerplateSymbol is the method referenced in simBetween above.
func (cb *ContextBuilder) isBoilerplateSymbol(s string) bool {
	if cb.boilerplate == nil {
		return false // detector not built → treat nothing as boilerplate
	}
	return cb.boilerplate.IsBoilerplate(s)
}
