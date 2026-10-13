// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"math"
	"strings"
	"unicode"
)

// ========== Result quality ==========
//
// assess is the scoring function the cascade uses to decide whether a stage
// result is good enough to stop. It combines three signals:
//
//	AnchorCov  fraction of the query's anchors that appear in the result
//	TermCov    fraction of the query's significant words that appear
//	Rich       evidence that the result is real code (fenced blocks, file:line
//	           refs, and a floor derived from body length)
//
// The weights differ depending on whether the query named any anchors: when
// it did, anchor coverage is the strongest signal; when it did not, term
// coverage carries the score.

const (
	// minUsefulBytes is the floor below which a result is treated as empty.
	// Set well above zero so a one-line "I don't know" cannot pass.
	minUsefulBytes = 120
)

// noResultMarkers are phrases that indicate a backend refused to answer.
// They are only checked for short results, because in a long answer the same
// phrases can legitimately appear.
var noResultMarkers = []string{
	"no relevant", "no results", "nothing found", "could not find", "couldn't find", "no matches",
}

// stopwords are common English and code words that carry no retrieval signal.
var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`the and for are how what which does with that this from into its use uses
		using when where why who can did has have not you your any all but out one per via was were been being
		there their they them than then also only some such more most each other about over under between
		inside code function method class file work works specific specifically ensure ensures get gets`) {
		m[w] = true
	}
	return m
}()

// quality is the breakdown produced by assess. Score is the combined value;
// the other fields are kept so callers (and tests) can see why a result
// scored the way it did.
type quality struct {
	Score     float64
	AnchorCov float64
	TermCov   float64
	Rich      float64
}

// assess scores how well a result answers the query, from 0 to 1.
//
// echoes are strings the backend may have copied verbatim from the (rewritten)
// query into the result, such as a CoT header; they are removed first so they
// cannot inflate coverage.
func assess(q string, anchors []Anchor, result string, echoes ...string) quality {
	body := result
	for _, e := range echoes {
		if e != "" {
			body = strings.ReplaceAll(body, e, " ")
		}
	}
	body = strings.TrimSpace(body)
	if len(body) < minUsefulBytes {
		return quality{}
	}
	lower := strings.ToLower(body)
	if len(body) < 800 {
		for _, mk := range noResultMarkers {
			if strings.Contains(lower, mk) {
				return quality{}
			}
		}
	}

	var ql quality

	if terms := queryTerms(q); len(terms) > 0 {
		hit := 0
		for _, t := range terms {
			if strings.Contains(lower, t) {
				hit++
			}
		}
		ql.TermCov = float64(hit) / float64(len(terms))
	}

	if len(anchors) > 0 {
		hit := 0
		for _, a := range anchors {
			for _, k := range a.Keys {
				if strings.Contains(lower, k) {
					hit++
					break
				}
			}
		}
		ql.AnchorCov = float64(hit) / float64(len(anchors))
	}

	// Richness is a heuristic for "this looks like real code rather than a
	// refusal": fenced blocks and file:line references count directly; a
	// length-derived floor keeps long-form answers from being penalised.
	chunks := strings.Count(body, "```")/2 + len(reFileLine.FindAllStringIndex(body, -1))
	ql.Rich = math.Max(math.Min(1, float64(chunks)/3), 0.6*math.Min(1, float64(len(body))/3000))

	if len(anchors) > 0 {
		ql.Score = 0.45*ql.AnchorCov + 0.35*ql.TermCov + 0.20*ql.Rich
	} else {
		ql.Score = 0.65*ql.TermCov + 0.35*ql.Rich
	}
	return ql
}

// ========== Query / identifier tokenisation ==========

// queryTerms returns the significant lower-cased words of a query, with
// identifiers split into their parts (resolve_expression -> resolve,
// expression). Stopwords and short tokens are dropped; duplicates are
// collapsed.
func queryTerms(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range splitIdent(q) {
		if len(w) < 3 || stopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// splitIdent splits s on non-alphanumerics and on camelCase / acronym
// boundaries, lower-casing each part. Examples:
//
//	"resolve_expression" -> ["resolve", "expression"]
//	"HTTPServer.Start"   -> ["http", "server", "start"]
//	"SQLCompiler"        -> ["sql", "compiler"]
func splitIdent(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(r) {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}
