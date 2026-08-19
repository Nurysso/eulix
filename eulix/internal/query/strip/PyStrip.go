package strip

// stripPythonComments removes # comments and ”'/""" docstrings from Python
// source using a character-level state machine that tracks string state,
// so a "#" inside a string (a URL fragment, a hex color, etc.) is never
// mistaken for a comment.
//
// Known limitation: an escaped quote immediately before a triple-quote
// terminator (e.g. \""" inside a triple string) isn't specially handled —
// an inherent edge case of line/character-level lexing without a full
// tokenizer (Python's own `tokenize` module would be the exact-fidelity
// option, at the cost of shelling out to a Python process).
func stripPythonComments(src string) string {
	runes := []rune(src)
	n := len(runes)
	out := make([]rune, 0, n)

	inLineComment := false
	inTriple := false
	var tripleQuote rune
	var stringQuote rune // single/double quoted, non-triple

	for i := 0; i < n; i++ {
		c := runes[i]

		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
				out = append(out, c)
			}
			continue

		case inTriple:
			if c == '\n' {
				out = append(out, c)
			}
			if c == tripleQuote && i+2 < n && runes[i+1] == tripleQuote && runes[i+2] == tripleQuote {
				inTriple = false
				i += 2
			}
			continue

		case stringQuote != 0:
			out = append(out, c)
			if c == '\\' && i+1 < n {
				out = append(out, runes[i+1])
				i++
				continue
			}
			if c == stringQuote {
				stringQuote = 0
			}
			continue
		}

		switch {
		case (c == '"' || c == '\'') && i+2 < n && runes[i+1] == c && runes[i+2] == c:
			inTriple = true
			tripleQuote = c
			i += 2
		case c == '"' || c == '\'':
			stringQuote = c
			out = append(out, c)
		case c == '#':
			inLineComment = true
		default:
			out = append(out, c)
		}
	}

	return string(out)
}
