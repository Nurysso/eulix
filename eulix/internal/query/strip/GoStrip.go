package strip

import (
	"go/scanner"
	"go/token"
	"strings"
)

// stripGoComments removes comment text from Go source using the real Go
// tokenizer (go/scanner), rather than naive substring search. Because it's
// a proper lexer, it never mistakes "//" or "/*" inside a string, raw
// string, or rune literal for the start of a comment.
//
// The scanner is run with a nil error handler so that scanning a partial
// snippet (chunks are arbitrary line ranges, not whole files) degrades
// gracefully instead of stopping early.
func stripGoComments(src string) string {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))

	var s scanner.Scanner
	s.Init(file, []byte(src), nil, scanner.ScanComments)

	type byteRange struct{ start, end int }
	var ranges []byteRange

	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			start := file.Offset(pos)
			end := start + len(lit)
			if end > len(src) {
				end = len(src)
			}
			if start >= 0 && start <= len(src) {
				ranges = append(ranges, byteRange{start, end})
			}
		}
	}

	if len(ranges) == 0 {
		return src
	}

	var b strings.Builder
	b.Grow(len(src))
	last := 0
	for _, r := range ranges {
		if r.start < last || r.start > len(src) {
			// Defensive: scanner shouldn't emit overlapping/out-of-order
			// ranges, but never let a bad range corrupt output on
			// malformed/truncated input.
			continue
		}
		b.WriteString(src[last:r.start])
		// A multi-line block comment's literal includes its internal
		// newlines. Drop everything else in the range but keep those
		// newlines, so line numbers stay aligned with the input — the
		// caller relies on a 1:1 line count to re-attach per-line
		// metadata (blank-line preservation, etc.).
		for _, ch := range src[r.start:r.end] {
			if ch == '\n' {
				b.WriteRune(ch)
			}
		}
		last = r.end
	}
	b.WriteString(src[last:])
	return b.String()
}
