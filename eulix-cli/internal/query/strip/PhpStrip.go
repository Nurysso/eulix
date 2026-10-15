// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package strip handles stripping of comments in retrieval stage to safe tokens
package strip

import (
	"strings"
	"unicode"
)

const (
	phpCode = iota
	phpHTML
	phpLineComment
	phpBlockComment
	phpString
	phpHeredoc
)

// stripPHPComments removes //, # and /* */ comments from PHP source using a
// character-level state machine that knows PHP's own traps:
//
//   - text outside <?php ... ?> is HTML and is copied untouched, so a "//" in
//     a URL or a "#" in a CSS selector or anchor survives;
//   - a // or # comment ends at the newline or at "?>", whichever comes
//     first, and the "?>" is kept because it switches back to HTML;
//   - '...', "..." and `...` strings, including {$a["key"]} interpolation,
//     and heredoc/nowdoc bodies are copied verbatim; "{$" or "${" opens
//     interpolation only when a variable name or "$" follows, so a stray
//     one in broken code cannot swallow the closing quote;
//   - "#[" opens a PHP 8 attribute, not a comment.
//
// Segments are usually cut from the middle of a file, so the starting mode
// is a guess: PHP, unless the segment opens with markup ("<" that is not
// "<?" or a "<<<" heredoc).
//
// Known limitation: a segment that starts inside a multi-line string or
// heredoc is read as code until the string closes.
func stripPHPComments(src string) string {
	runes := []rune(src)
	n := len(runes)
	out := make([]rune, 0, n)

	state := phpCode
	if phpOpensWithMarkup(runes) {
		state = phpHTML
	}
	var quote rune       // phpString: the opening quote
	interp := 0          // phpString: brace depth inside {$...} / ${...}
	var heredocID []rune // phpHeredoc: terminator identifier
	lineStart := func(i int) bool { return i == 0 || runes[i-1] == '\n' }

	for i := 0; i < n; i++ {
		c := runes[i]
		next := rune(0)
		if i+1 < n {
			next = runes[i+1]
		}

		switch state {
		case phpHTML:
			if c == '<' && next == '?' {
				if tag := phpOpenTag(runes, i); tag > 0 {
					out = append(out, runes[i:i+tag]...)
					i += tag - 1
					state = phpCode
					continue
				}
			}
			out = append(out, c)
			continue

		case phpLineComment:
			switch {
			case c == '\n':
				out = append(out, c)
				state = phpCode
			case c == '?' && next == '>':
				out = append(out, c, next)
				i++
				state = phpHTML
			}
			continue

		case phpBlockComment:
			if c == '\n' {
				out = append(out, c) // keep newlines so line counts stay aligned
			}
			if c == '*' && next == '/' {
				i++
				state = phpCode
			}
			continue

		case phpString:
			if interp > 0 {
				// Code inside "{$a['k']}": it may hold quotes of its own.
				switch c {
				case '\'', '"':
					end := phpSkipQuoted(runes, i)
					out = append(out, runes[i:end]...)
					i = end - 1
					continue
				case '{':
					interp++
				case '}':
					interp--
				}
				out = append(out, c)
				continue
			}
			out = append(out, c)
			switch {
			case c == '\\' && i+1 < n:
				out = append(out, next)
				i++
			case quote != '\'' && ((c == '{' && next == '$') || (c == '$' && next == '{')) &&
				i+2 < n && phpOpensInterp(runes[i+2]):
				out = append(out, next)
				i++
				interp = 1
			case c == quote:
				state = phpCode
			}
			continue

		case phpHeredoc:
			if lineStart(i) {
				// PHP 7.3+ allows the closing identifier to be indented.
				j := i
				for j < n && (runes[j] == ' ' || runes[j] == '\t') {
					j++
				}
				if end := j + len(heredocID); end <= n && string(runes[j:end]) == string(heredocID) &&
					(end == n || !phpIsIdentRune(runes[end])) {
					out = append(out, runes[i:end]...)
					i = end - 1
					state = phpCode
					continue
				}
			}
			out = append(out, c)
			continue
		}

		// phpCode
		switch {
		case c == '?' && next == '>':
			out = append(out, c, next)
			i++
			state = phpHTML
		case c == '\'' || c == '"' || c == '`':
			out = append(out, c)
			quote = c
			interp = 0
			state = phpString
		case c == '/' && next == '/':
			i++
			state = phpLineComment
		case c == '#' && next != '[':
			state = phpLineComment
		case c == '/' && next == '*':
			i++
			state = phpBlockComment
		case c == '<':
			if id, end := phpHeredocHeader(runes, i); id != nil {
				out = append(out, runes[i:end]...)
				i = end - 1
				heredocID = id
				state = phpHeredoc
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}

	return string(out)
}

// HasPHPOpenTag reports whether the source opens PHP code anywhere: "<?=",
// or "<?php" or a short "<?" followed by whitespace or the end of the text
// ("<?xml" is markup). C, C++ and assembler projects name files ".inc" too,
// and this tells the PHP ones apart. It matches libeulix's has_open_tag.
func HasPHPOpenTag(lines []string) bool {
	for _, l := range lines {
		if !strings.Contains(l, "<?") {
			continue
		}
		runes := []rune(l)
		for i := 0; i+1 < len(runes); i++ {
			if runes[i] == '<' && runes[i+1] == '?' && phpOpenTag(runes, i) > 0 {
				return true
			}
		}
	}
	return false
}

// phpOpensWithMarkup reports whether the first non-blank text is HTML.
func phpOpensWithMarkup(runes []rune) bool {
	for i, c := range runes {
		if unicode.IsSpace(c) {
			continue
		}
		if c != '<' || i+1 >= len(runes) {
			return false
		}
		return runes[i+1] != '?' && runes[i+1] != '<'
	}
	return false
}

// phpOpenTag returns the length of the PHP open tag at runes[i] ("<?php",
// "<?=" or a short "<?"), or 0. "<?xml" is markup, not PHP.
func phpOpenTag(runes []rune, i int) int {
	rest := runes[i:]
	if len(rest) >= 3 && rest[2] == '=' {
		return 3
	}
	if len(rest) >= 5 && phpEqualFold(rest[2:5], "php") {
		if len(rest) == 5 || phpIsTagSpace(rest[5]) {
			return 5
		}
		return 0
	}
	if len(rest) == 2 || phpIsTagSpace(rest[2]) {
		return 2
	}
	return 0
}

// phpIsTagSpace reports whether c may follow an open tag: PHP accepts only
// space, tab, CR and LF there.
func phpIsTagSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// phpHeredocHeader parses "<<<ID", "<<<\"ID\"" or "<<<'ID'" at runes[i] and
// returns the identifier and the index of the newline that ends the header.
func phpHeredocHeader(runes []rune, i int) ([]rune, int) {
	n := len(runes)
	if i+2 >= n || runes[i+1] != '<' || runes[i+2] != '<' {
		return nil, 0
	}
	j := i + 3
	for j < n && (runes[j] == ' ' || runes[j] == '\t') {
		j++
	}
	var q rune
	if j < n && (runes[j] == '\'' || runes[j] == '"') {
		q = runes[j]
		j++
	}
	start := j
	for j < n && phpIsIdentRune(runes[j]) {
		j++
	}
	if j == start || unicode.IsDigit(runes[start]) {
		return nil, 0
	}
	id := runes[start:j]
	if q != 0 {
		if j >= n || runes[j] != q {
			return nil, 0
		}
		j++
	}
	if j < n && runes[j] == '\r' {
		j++
	}
	if j >= n || runes[j] != '\n' {
		return nil, 0
	}
	return id, j
}

// phpSkipQuoted returns the index just past the quoted string starting at i.
func phpSkipQuoted(runes []rune, i int) int {
	q := runes[i]
	for j := i + 1; j < len(runes); j++ {
		switch runes[j] {
		case '\\':
			j++
		case q:
			return j + 1
		}
	}
	return len(runes)
}

// phpOpensInterp reports whether c, read right after "{$" or "${" in a
// string, starts what PHP interpolates there: a variable name or another "$"
// ("{$$name}"). PHP opens on any "{$" and "${", so "{$" with nothing after it
// is a parse error; segments come from broken or half-written files too, and
// this keeps such a pair literal instead of reading code until a later quote.
func phpOpensInterp(c rune) bool {
	return c == '$' || phpIsLabelStart(c)
}

// phpIsLabelStart reports whether c may start a PHP name: a letter, "_" or
// any byte from 0x80 up, but not a digit.
func phpIsLabelStart(c rune) bool {
	return c == '_' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func phpIsIdentRune(c rune) bool {
	return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c)
}

func phpEqualFold(r []rune, s string) bool {
	if len(r) != len(s) {
		return false
	}
	for i, c := range s {
		if unicode.ToLower(r[i]) != c {
			return false
		}
	}
	return true
}
