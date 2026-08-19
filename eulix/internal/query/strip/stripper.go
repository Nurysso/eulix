package strip

import "strings"

// StripCommentsAndDocs is a drop-in replacement for the original
// stripCommentsAndDocs: same signature, same "drop lines that become blank
// after stripping, but keep lines that were already blank" contract used
// downstream by extractLines/hydrateSourceCode for token accounting.
//
// The difference is *how* comments are found: each language dispatches to
// a real scanner (go/scanner for Go) or a quote-aware state machine
// (everything else) instead of a raw substring search, so a comment marker
// that appears inside a string literal is never stripped.
func StripCommentsAndDocs(lines []string, lang string) []string {
	if len(lines) == 0 {
		return lines
	}

	src := strings.Join(lines, "\n")

	var cleaned string
	switch lang {
	case "go":
		cleaned = stripGoComments(src)
	case "javascript", "typescript":
		cleaned = stripCStyleComments(src, true) // backtick template literals
	case "rust", "c", "cpp", "java":
		cleaned = stripCStyleComments(src, false)
	case "python":
		cleaned = stripPythonComments(src)
	default:
		return lines // unknown language: don't touch it
	}

	cleanedLines := strings.Split(cleaned, "\n")

	// Defensive: every branch above preserves every '\n' byte from the
	// input, so line counts must match. If something upstream ever changes
	// that invariant, fail safe by returning the original lines rather than
	// producing misaligned output.
	if len(cleanedLines) != len(lines) {
		return lines
	}

	result := make([]string, 0, len(lines))
	for i, original := range lines {
		originalBlank := strings.TrimSpace(original) == ""
		cleanedLine := cleanedLines[i]
		cleanedBlank := strings.TrimSpace(cleanedLine) == ""

		switch {
		case originalBlank:
			result = append(result, "") // preserve genuinely blank lines
		case !cleanedBlank:
			result = append(result, cleanedLine) // real code remains
		default:
			// Non-blank line that became blank purely from comment
			// removal (a comment-only line, or a line fully consumed by
			// a multi-line block comment) — drop it, matching the
			// original function's token-saving intent.
		}
	}

	return result
}
