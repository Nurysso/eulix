package strip

// stripCStyleComments removes // and /* */ comments from C-family source
// (javascript, typescript, rust, c, cpp, java) using a small character-level
// state machine that tracks whether we're inside a string/char literal.
// Unlike a substring search, it will not strip "//" or "/*" that appear
// inside a quoted string (a URL, an SQL fragment, etc.) because it never
// even looks for comment markers while stringQuote != 0.
//
// allowBacktick enables backtick template-literal strings (javascript /
// typescript only).
//
// Known limitations (documented rather than silently wrong):
//   - JS/TS regex literals (/foo\/bar/) are indistinguishable from division
//     by a lexer this simple; a "//" opening a regex could be misread.
//   - Rust raw strings with hashes (r#"..."#) are treated as ordinary
//     double-quoted strings, so an embedded unescaped '"' will end the
//     string early. Plain r"..." works fine.
//
// Both are inherent to line/character-level lexing without a full grammar;
// a tree-sitter grammar per language is the next step up if these edge
// cases matter for your corpus.
func stripCStyleComments(src string, allowBacktick bool) string {
	runes := []rune(src)
	n := len(runes)
	out := make([]rune, 0, n)

	inLineComment := false
	inBlockComment := false
	var stringQuote rune // 0 means "not in a string"

	for i := 0; i < n; i++ {
		c := runes[i]

		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
				out = append(out, c)
			}
			continue

		case inBlockComment:
			if c == '\n' {
				out = append(out, c) // keep newlines so line counts stay aligned
			}
			if c == '*' && i+1 < n && runes[i+1] == '/' {
				inBlockComment = false
				i++
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

		// Normal state.
		switch {
		case c == '"' || c == '\'' || (allowBacktick && c == '`'):
			stringQuote = c
			out = append(out, c)
		case c == '/' && i+1 < n && runes[i+1] == '/':
			inLineComment = true
			i++
		case c == '/' && i+1 < n && runes[i+1] == '*':
			inBlockComment = true
			i++
		default:
			out = append(out, c)
		}
	}

	return string(out)
}
