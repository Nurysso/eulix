//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides the context window builder and query routing for Eulix's
RAG (Retrieval-Augmented Generation) system.

This file implements source code hydration: the process of enriching chunks with
actual source code from disk, replacing KB metadata with readable code snippets.

Hydration pipeline:
 1. Token budgeting: Allocate source budget progressively to high-priority chunks
 2. Line extraction: Read source file, slice to [startLine, endLine)
 3. Comment stripping: Remove docstrings, block comments, and inline comments
    while preserving code structure (empty lines, indentation)
 4. Markup: Wrap code in triple-backtick markdown with language tag
 5. Token estimation: Estimate tokens as (code_length / 4)

Progressive budgeting:
  - Chunks are already in priority order (from mmrSelect or multiStrategySearch)
  - Remaining budget triggers automatic line-count reduction:
  - budgetFraction >= 0.50: maxLines (full)
  - budgetFraction < 0.50: maxLines / 2
  - budgetFraction < 0.25: maxLines / 4
  - Minimum viable snippet: 10 lines (hard floor)
  - Skip chunks that exceed remaining budget

Language-specific comment stripping:
  - Python: triple-quote docstrings (""" or ”'), # inline comments
  - Go/C/C++/Java/JavaScript/TypeScript/Rust: /∗ \∗/ block comments, // inline
  - Preserves blank lines and indentation for readability

Error handling:
  - Missing source root: skip hydration entirely
  - File not found: skip chunk, log warning, continue
  - Invalid line ranges: skip chunk, log diagnostic
  - Budget exhaustion: truncate line count, continue with remaining chunks

See:
  - context_mmr.go: mmrSelect returns chunks in priority order for hydration
  - context_builder.go: ContextWindow assembly calls hydrateSourceCode
  - context_loader.go: Source root configuration (cb.sourceRoot)
*/
package query

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hydrateSourceCode enriches selected chunks with actual source code from disk,
// replacing KB metadata with readable, comment-stripped code snippets.
//
// Workflow:
//  1. Iterate through chunks in priority order (already sorted by mmrSelect)
//  2. For each chunk, determine budget allocation based on remaining tokens
//  3. Read source file, extract [startLine, endLine), strip comments
//  4. Wrap in markdown code fence with language tag
//  5. Track token usage and budget exhaustion
//
// Progressive budgeting:
//   - Budget fraction triggers line-count reduction:
//   - >= 50%: full maxLinesDefault
//   - < 50%: maxLinesDefault / 2
//   - < 25%: maxLinesDefault / 4
//   - Minimum: 10 lines (hard floor to preserve context)
//   - Chunks exceeding remaining budget are returned unhydrated (metadata intact)
//
// Error handling:
//   - No source root configured: return chunks unmodified
//   - File not found / read error: skip chunk, log diagnostic, continue
//   - Invalid line range: skip chunk, log diagnostic, continue
//   - Budget exhaustion: progressively reduce line counts for later chunks
//
// Logging:
//   - Logs hydration start/end with budget and chunk count
//   - Per-chunk: file, lines added, tokens used, remaining budget
//   - Summary: success count and total token usage
//
// See:
//   - readSourceLines: Line extraction and comment stripping
//   - stripCommentsAndDocs: Language-specific comment removal
//   - detectLanguage: File extension → language mapping
//   - context_mmr.go: Chunks arrive pre-sorted by relevance
func (cb *ContextBuilder) hydrateSourceCode(chunks []Chunk, sourceBudget int, maxLinesDefault int) []Chunk {
	if cb.sourceRoot == "" {
		cb.debugLog.Log("Source hydration skipped: no source root configured")
		return chunks // no source root configured
	}

	cb.debugLog.Log("=== SOURCE HYDRATION START ===")
	cb.debugLog.Log("Source root: %s", cb.sourceRoot)
	cb.debugLog.Log("Budget: %d tokens | Max lines: %d | Chunks: %d", sourceBudget, maxLinesDefault, len(chunks))

	result := make([]Chunk, len(chunks))
	usedTokens := 0
	successCount := 0

	// Chunks are already in priority order from mmrSelect
	for i, chunk := range chunks {
		remaining := sourceBudget - usedTokens
		if remaining <= 0 {
			result[i] = chunk
			continue
		}

		maxLines := maxLinesDefault
		budgetFraction := float64(remaining) / float64(sourceBudget)
		// Progressive reduction: higher priority chunks get more lines
		if budgetFraction < 0.5 {
			maxLines = maxLines / 2
		}
		if budgetFraction < 0.25 {
			maxLines = maxLines / 4
		}
		if maxLines < 10 {
			maxLines = 10 // minimum viable snippet
		}

		// Read and clean source
		sourceCode, tokens := cb.readSourceLines(chunk.File, chunk.StartLine, chunk.EndLine, maxLines)

		// Prepend source to KB metadata (keep metadata for call graph info)
		if sourceCode == "" || tokens > remaining {
			if sourceCode == "" {
				fmt.Printf("[DEBUG] No source found for %s:%d-%d\n", chunk.File, chunk.StartLine, chunk.EndLine)
			} else {

				cb.debugLog.Log("Chunk %d: source available but exceeds remaining budget (%d > %d)", i, tokens, remaining)
			}
			result[i] = chunk
			continue
		}

		chunk.Content = fmt.Sprintf("```%s\n%s\n```",
			detectLanguage(chunk.File),
			sourceCode)
		chunk.Tokens = tokens
		usedTokens += tokens
		successCount++

		cb.debugLog.Log("Chunk %d (%s:%d-%d): ✓ Added %d lines, %d tokens (budget: %d/%d)",
			i, chunk.File, chunk.StartLine, chunk.EndLine,
			strings.Count(sourceCode, "\n")+1, tokens, usedTokens, sourceBudget)

		result[i] = chunk
	}

	cb.debugLog.Log("=== SOURCE HYDRATION COMPLETE ===")
	cb.debugLog.Log("Success: %d/%d chunks | Used: %d/%d tokens",
		successCount, len(chunks), usedTokens, sourceBudget)

	return result
}

// readSourceLines extracts and cleans source code from a file.
// Reads lines [startLine, endLine), truncates to maxLines if necessary,
// and removes comments/docstrings while preserving code structure.
//
// Workflow:
//  1. Validate source root is configured (checked by caller)
//  2. Calculate actual end line: min(endLine, startLine + maxLines)
//  3. Read entire file from disk
//  4. Extract [startLine-1, end) as 0-indexed slice (files are 1-indexed)
//  5. Detect language from file extension
//  6. Strip comments/docstrings using language-specific rules
//  7. Estimate tokens as len(code) / 4 (rough conversion: 4 chars ≈ 1 token)
//
// Error handling:
//   - File not found: return ("", 0), log warning
//   - Read error: return ("", 0), log error
//   - Line range out of bounds: return ("", 0), log diagnostic with file line count
//
// Returns: (cleaned_code_string, estimated_token_count)
//
// See:
//   - stripCommentsAndDocs: Language-specific comment removal
//   - detectLanguage: File extension → language mapping
//   - hydrateSourceCode: Budget allocation caller
func (cb *ContextBuilder) readSourceLines(filePath string, startLine, endLine, maxLines int) (string, int) {
	// Calculate actual end line respecting maxLines
	actualEnd := endLine
	if endLine-startLine+1 > maxLines {
		actualEnd = startLine + maxLines - 1
	}
	// Read source file
	sourceFile := filepath.Join(cb.sourceRoot, filePath)
	// Check if file exists
	if _, err := os.Stat(sourceFile); os.IsNotExist(err) {
		cb.debugLog.Log("Source file not found: %s", sourceFile)
		return "", 0
	}
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		cb.debugLog.Log("Error reading %s: %v", sourceFile, err)
		return "", 0
	}

	lines := strings.Split(string(data), "\n")
	if startLine > len(lines) || startLine < 1 {
		cb.debugLog.Log("Invalid line range for %s: file has %d lines, requested %d-%d",
			filePath, len(lines), startLine, endLine)
		return "", 0
	}
	// Extract range (1-indexed to 0-indexed)
	start := startLine - 1
	end := actualEnd
	if end > len(lines) {
		end = len(lines)
	}

	relevantLines := lines[start:end]
	lang := detectLanguage(filePath)
	cleaned := stripCommentsAndDocs(relevantLines, lang)
	// Strip comments and docstrings
	code := strings.Join(cleaned, "\n")
	tokens := len(code) / 4 // rough estimate: 4 chars per token

	return code, tokens
}

// stripCommentsAndDocs removes language-specific comments and docstrings
// while preserving code structure (blank lines, indentation).
//
// Supported languages and comment styles:
//
//	Python:
//	  - Docstrings: """ ... """ or ''' ... '''
//	  - Inline: # comment
//	  - Behavior: skip lines inside docstrings entirely, strip inline comments
//
//	Go, C, C++, Java, JavaScript, TypeScript, Rust:
//	  - Block: /* ... */ (multi-line)
//	  - Inline: // comment
//	  - Behavior: skip content in /* */, strip // from line end
//
//	Unknown language: keeps all content except /* */ and //
//
// Structural preservation:
//   - Blank lines that were blank in input are preserved
//   - Indentation (leading whitespace) is kept
//   - Empty lines help maintain code readability
//
// State machine:
//   - inBlockComment: tracks /* ... */ nesting
//   - inDocstring (Python): tracks """ ... """ or ”' ... ”'
//   - docstringMarker: remembers which quote style opened the docstring
//
// See:
//   - detectLanguage: Maps file extension to language name
//   - readSourceLines: Calls this after line extraction
func stripCommentsAndDocs(lines []string, lang string) []string {
	result := make([]string, 0, len(lines))
	inBlockComment := false
	inDocstring := false
	docstringMarker := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		originalLine := line

		switch lang {
		case "python":
			// Handle docstrings (""" or ''')
			if !inDocstring {
				if strings.HasPrefix(trimmed, `"""`) {
					inDocstring = true
					docstringMarker = `"""`
					if strings.Count(trimmed, `"""`) >= 2 {
						inDocstring = false // single-line docstring
					}
					continue
				} else if strings.HasPrefix(trimmed, "'''") {
					inDocstring = true
					docstringMarker = "'''"
					if strings.Count(trimmed, "'''") >= 2 {
						inDocstring = false
					}
					continue
				}
			} else {
				if strings.Contains(trimmed, docstringMarker) {
					inDocstring = false
				}
				continue
			}
			// Strip inline comments
			if idx := strings.Index(line, "#"); idx >= 0 {
				line = line[:idx]
			}

		case "go", "javascript", "typescript", "rust", "c", "cpp", "java":
			// Handle block comments /* */
			if !inBlockComment && strings.Contains(line, "/*") {
				inBlockComment = true
				if idx := strings.Index(line, "/*"); idx >= 0 {
					line = line[:idx]
				}
			}
			if inBlockComment {
				if strings.Contains(line, "*/") {
					inBlockComment = false
					if idx := strings.Index(line, "*/"); idx >= 0 && idx+2 < len(line) {
						line = line[idx+2:]
					} else {
						continue
					}
				} else {
					continue
				}
			}
			// Strip inline comments //
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
		}

		// Keep line if it has content after comment removal
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		} else if originalLine != "" && strings.TrimSpace(originalLine) == "" {
			// Preserve empty lines that were originally empty
			result = append(result, "")
		}
	}

	return result
}

// detectLanguage determines programming language from file extension.
// Used to select language-specific comment stripping rules and markdown
// code fence markup (```language).
//
// Supported extensions:
//   - .py → python
//   - .go → go
//   - .js → javascript
//   - .ts → typescript
//   - .rs → rust
//   - .c, .h → c
//   - .cpp, .cc, .cxx, .hpp → cpp
//   - .java → java
//   - (others) → unknown (no language-specific comment stripping)
//
// See:
//   - stripCommentsAndDocs: Uses language name to select comment rules
//   - readSourceLines: Calls this to markup code fence
func detectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".java":
		return "java"
	default:
		return "unknown"
	}
}
