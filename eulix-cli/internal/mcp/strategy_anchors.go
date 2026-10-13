// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ========== Anchor extraction ==========
//
// An anchor is a piece of text in a query that names something concrete in
// the codebase: a file path, a qualified name like SQLCompiler.get_from_clause,
// a bare function call, or an identifier-shaped token (snake_case, camelCase,
// PascalCase, acronym-prefixed). Anchors drive exact lookup and are also used
// as evidence when scoring a result — a result that mentions the anchors the
// caller asked about is more likely to be the right one.

const (
	// maxAnchors caps how many anchors a single query is allowed to produce,
	// so a pathological query cannot flood the scoring signal.
	maxAnchors = 12

	// maxHints caps how many anchors are derived from the caller-supplied
	// 'known_symbols'. Hints are context, not requirements.
	maxHints = 20
)

// Anchor is a symbol, qualified name or path mentioned in a query.
type Anchor struct {
	// Text is the anchor as written, e.g. "SQLCompiler.get_from_clause".
	Text string

	// Keys are lower-cased substrings that count as evidence for the anchor
	// in a result. Typically the full text plus its last segment.
	Keys []string
}

var (
	// reTick matches `backtick-quoted` spans, which are always treated as
	// code regardless of their shape.
	reTick = regexp.MustCompile("`[^`\\s]+`")

	// rePath matches relative file paths (with slashes) and bare filenames
	// with a known code/documentation extension.
	rePath = regexp.MustCompile(`(?:[\w.\-]+/)+[\w.\-]+\.[A-Za-z0-9]{1,5}\b|\b[\w\-]+\.(?:go|py|js|jsx|ts|tsx|java|rs|rb|php|cs|kt|swift|scala|c|cc|cpp|h|hpp|sh|sql|md|json|ya?ml|toml)\b`)

	// reQual matches qualified names such as A.B, A::B, A->B, A#B, with an
	// optional trailing "()".
	reQual = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*(?:(?:\.|::|->|#)[A-Za-z_][A-Za-z0-9_]*)+(?:\(\))?`)

	// reCall matches bare function-call syntax: ident(.
	reCall = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\(`)

	// reIdent matches any identifier-shaped word.
	reIdent = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

	// reSep splits a qualified name into its parts.
	reSep = regexp.MustCompile(`\.|::|->|#`)

	// reUpper matches acronym-prefixed names like SQLCompiler.
	reUpper = regexp.MustCompile(`[A-Z]{2,}[a-z]`)

	// reFileLine matches file:line references inside a result; they are one
	// of the richness signals used when scoring.
	reFileLine = regexp.MustCompile(`[\w./\-]+\.[A-Za-z0-9]{1,5}:\d+`)
)

// ExtractAnchors pulls file paths, qualified names (Class.method()), calls and
// identifier-shaped tokens (snake_case, camelCase, PascalCase) out of a query,
// in the order they appear.
//
// The function is order-preserving and de-duplicates case-insensitively, so
// the caller can rely on the first occurrence of each anchor.
func ExtractAnchors(q string) []Anchor {
	var out []Anchor
	seen := map[string]bool{}
	add := func(as ...Anchor) {
		for _, a := range as {
			k := strings.ToLower(a.Text)
			if a.Text == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, a)
		}
	}

	// mask replaces each match of re with spaces after handing it to fn, so
	// later, broader patterns cannot re-match the same span.
	mask := func(re *regexp.Regexp, s string, fn func(string)) string {
		return re.ReplaceAllStringFunc(s, func(m string) string {
			fn(m)
			return strings.Repeat(" ", len(m))
		})
	}

	text := q
	text = mask(reTick, text, func(m string) { add(classify(strings.Trim(m, "`"))...) })
	text = mask(rePath, text, func(m string) { add(pathAnchor(m)) })
	text = mask(reQual, text, func(m string) { add(qualifiedAnchors(m)...) })
	text = mask(reCall, text, func(m string) { add(identAnchor(strings.TrimSuffix(m, "("))) })
	for _, tok := range reIdent.FindAllString(text, -1) {
		if isSymbolLike(tok) {
			add(identAnchor(tok))
		}
	}

	if len(out) > maxAnchors {
		out = out[:maxAnchors]
	}
	return out
}

// classify turns a piece of code-looking text into the appropriate anchor(s).
// Returns nil for text that is not recognisably a path, qualified name or
// identifier.
func classify(s string) []Anchor {
	s = strings.TrimSuffix(s, "()")
	switch {
	case s == "":
		return nil
	case strings.Contains(s, "/"):
		return []Anchor{pathAnchor(s)}
	case reQual.FindString(s) == s:
		return qualifiedAnchors(s)
	case reIdent.FindString(s) == s:
		return []Anchor{identAnchor(s)}
	}
	return nil
}

// pathAnchor builds an anchor for a file path. It matches both the full path
// and its base name, so a result mentioning either is counted as evidence.
func pathAnchor(p string) Anchor {
	keys := []string{strings.ToLower(p)}
	if base := strings.ToLower(filepath.Base(p)); base != keys[0] {
		keys = append(keys, base)
	}
	return Anchor{Text: p, Keys: keys}
}

// identAnchor builds an anchor for a single identifier.
func identAnchor(t string) Anchor {
	return Anchor{Text: t, Keys: []string{strings.ToLower(t)}}
}

// qualifiedAnchors builds anchors for a qualified name such as A.B.C. The
// full text is always an anchor; the head segment is added as a second anchor
// when it is symbol-shaped. Short segments (< 2 chars) disqualify the whole
// match, which filters out "e.g" and "i.e".
func qualifiedAnchors(m string) []Anchor {
	m = strings.TrimSuffix(m, "()")
	parts := reSep.Split(m, -1)
	for _, p := range parts {
		if len(p) < 2 {
			return nil
		}
	}
	last := parts[len(parts)-1]
	keys := []string{strings.ToLower(m)}
	if len(last) >= 4 {
		keys = append(keys, strings.ToLower(last))
	}
	res := []Anchor{{Text: m, Keys: keys}}
	if head := parts[0]; isSymbolLike(head) {
		res = append(res, identAnchor(head))
	}
	return res
}

// isSymbolLike reports whether a bare token looks like code: snake_case,
// camelCase, PascalCase, or an acronym-prefixed name like SQLCompiler. Plain
// English words never qualify, which is what keeps vague queries from being
// treated as anchored.
func isSymbolLike(t string) bool {
	if len(t) < 3 {
		return false
	}
	if strings.Contains(t, "_") && strings.Trim(t, "_") != "" {
		return true
	}
	for i := 1; i < len(t); i++ {
		if t[i-1] >= 'a' && t[i-1] <= 'z' && t[i] >= 'A' && t[i] <= 'Z' {
			return true
		}
	}
	return reUpper.MatchString(t)
}

// anchorsFromHints turns the caller-supplied 'known_symbols' (symbols,
// qualified names, paths) into anchors. Hints are context rather than
// requirements, so they are never used to score a result.
func anchorsFromHints(known []string) []Anchor {
	var out []Anchor
	for _, k := range known {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out = append(out, classify(k)...)
		if len(out) >= maxHints {
			break
		}
	}
	return out
}

// anchorTexts projects the Text field of each anchor.
func anchorTexts(as []Anchor) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Text)
	}
	return out
}

// dedupeStrings removes case-insensitive duplicates, preserving order. The
// returned slice does not alias the input.
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}

// scanSymbols calls fn for every symbol occurrence in text: paths (by full
// path and base name), qualified-name parts, and identifier-shaped tokens.
// Matches are blanked out as they are consumed, so broader patterns cannot
// re-report the same span.
func scanSymbols(text string, fn func(sym string)) {
	text = rePath.ReplaceAllStringFunc(text, func(p string) string {
		fn(p)
		fn(filepath.Base(p))
		return " "
	})
	text = reQual.ReplaceAllStringFunc(text, func(m string) string {
		parts := reSep.Split(strings.TrimSuffix(m, "()"), -1)
		for i, p := range parts {
			if isSymbolLike(p) || (i == len(parts)-1 && len(p) >= 4) {
				fn(p)
			}
		}
		return " "
	})
	for _, tok := range reIdent.FindAllString(text, -1) {
		if isSymbolLike(tok) {
			fn(tok)
		}
	}
}

// symbolsIn returns up to limit distinct symbols from text, in the order
// scanSymbols encounters them.
func symbolsIn(text string, limit int) []string {
	seen := map[string]bool{}
	var out []string
	scanSymbols(text, func(s string) {
		k := strings.ToLower(s)
		if len(out) >= limit || len(s) < 3 || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, s)
	})
	return out
}
