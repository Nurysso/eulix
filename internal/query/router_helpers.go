//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides query classification functionality.

/*
This file is responsible for Helpers used in Query routing.
*/
package query

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"eulix/internal/types"
)

type language int

const (
	langGo language = iota
	langRust
	langPython
	langTS
	langC // covers C and C++
)

func (r *Router) SetCurrentChecksum(checksum string) {
	r.currentChecksum = checksum
}

func (r *Router) Close() error {
	if r.contextBuilder != nil {
		return r.contextBuilder.Close()
	}
	return nil
}

func (r *Router) ensureContextBuilder() error {
	if r.contextBuilder != nil {
		return nil
	}
	sourceRoot := r.config.Project.Path
	if _, err := os.Stat(sourceRoot); os.IsNotExist(err) {
		return fmt.Errorf("source root does not exist: %s", sourceRoot)
	}
	if r.config.Project.DebugConfig {
		fmt.Printf("[INFO] Initializing context builder with source root: %s\n", sourceRoot)
	}
	cb, err := ContextWindowCreator(r.eulixDir, r.config, r.llmClient, sourceRoot)
	if err != nil {
		return fmt.Errorf("failed to initialize context builder: %w", err)
	}
	r.contextBuilder = cb
	return nil
}

// hasSourceCode checks whether any context chunk contains an actual code fence.
func hasSourceCode(ctx *types.ContextWindow) bool {
	for _, chunk := range ctx.Chunks {
		if strings.Contains(chunk.Content, "```") {
			return true
		}
	}
	return false
}

// firstSymbolOrExtracted returns the first classified symbol or falls back to
// heuristic extraction from the raw query string.
func firstSymbolOrExtracted(class *Classification, query string) string {
	// Words that are metrics commands, not actual symbols
	metricsCommands := map[string]bool{
		"metrics":    true,
		"summary":    true,
		"overall":    true,
		"project":    true,
		"statistics": true,
	}

	if len(class.Symbols) > 0 {
		candidate := class.Symbols[0]
		// If this looks like a metrics command rather than a symbol, don't return it
		if !metricsCommands[strings.ToLower(candidate)] {
			return candidate
		}
		// Fall through to extract from query instead
	}

	extracted := extractEntityName(query)
	// Also check extracted value against metrics commands
	if metricsCommands[strings.ToLower(extracted)] {
		return ""
	}
	return extracted
}

func (r *Router) findTransitiveDependencies(funcName string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	visited := make(map[string]bool)
	var result []string

	var traverse func(name string, d int)
	traverse = func(name string, d int) {
		if d > depth || visited[name] {
			return
		}
		visited[name] = true
		if fn, ok := r.callGraph.Functions[name]; ok {
			for _, callee := range fn.Calls {
				if !visited[callee] && callee != funcName {
					result = append(result, callee)
					traverse(callee, d+1)
				}
			}
		}
	}
	traverse(funcName, 0)
	return result
}

func formatFileData(path string, fd *types.FileData) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("File: %s  [%s, %d LOC]\n", path, fd.Language, fd.LOC))

	if len(fd.Functions) > 0 {
		b.WriteString(fmt.Sprintf("\nFunctions (%d):\n", len(fd.Functions)))
		for _, fn := range fd.Functions {
			b.WriteString(fmt.Sprintf("  • %s  (lines %d–%d, complexity %d, importance %.2f)\n",
				fn.Name, fn.LineStart, fn.LineEnd, fn.Complexity, fn.ImportanceScore))
		}
	}

	if len(fd.Classes) > 0 {
		b.WriteString(fmt.Sprintf("\nClasses (%d):\n", len(fd.Classes)))
		for _, cls := range fd.Classes {
			b.WriteString(fmt.Sprintf("  • %s  (lines %d–%d, %d methods)\n",
				cls.Name, cls.LineStart, cls.LineEnd, len(cls.Methods)))
		}
	}

	if len(fd.Todos) > 0 {
		b.WriteString(fmt.Sprintf("\nTODOs (%d):\n", len(fd.Todos)))
		for _, td := range fd.Todos {
			b.WriteString(fmt.Sprintf("  [%s] line %d: %s\n", td.Priority, td.Line, td.Text))
		}
	}

	if len(fd.SecurityNotes) > 0 {
		b.WriteString(fmt.Sprintf("\nSecurity notes (%d):\n", len(fd.SecurityNotes)))
		for _, sn := range fd.SecurityNotes {
			b.WriteString(fmt.Sprintf("  [%s] line %d: %s\n", sn.NoteType, sn.Line, sn.Description))
		}
	}

	return b.String()
}

// Helper to handle the metric formatting
func formatFunctionMetrics(fn types.KBFunction, path string) string {
	return fmt.Sprintf(
		"Metrics for %s (%s, lines %d–%d)\n  Cyclomatic complexity : %d\n  LOC                   : %d\n  Importance            : %.2f\n",
		fn.Name, path, fn.LineStart, fn.LineEnd,
		fn.Complexity, fn.LineEnd-fn.LineStart+1, fn.ImportanceScore,
	)
}

// Entity extraction ─
func extractFilePath(query string) string {
	// Look for something that looks like a file path: contains / or . with extension
	for _, word := range strings.Fields(query) {
		if strings.Contains(word, "/") || (strings.Contains(word, ".") && len(word) > 3) {
			return word
		}
	}
	return ""
}

func extractEntityName(query string) string {
	words := strings.Fields(query)
	stopWords := map[string]bool{
		"where": true, "is": true, "the": true, "function": true,
		"class": true, "method": true, "type": true, "find": true,
		"locate": true, "what": true, "does": true, "do": true,
		"who": true, "calls": true, "uses": true, "used": true,
		"a": true, "an": true, "this": true, "that": true,
		"how": true, "can": true, "will": true, "should": true,
	}
	for _, w := range words {
		if !stopWords[strings.ToLower(w)] && isLikelySymbol(w) {
			return w
		}
	}
	for _, w := range words {
		if !stopWords[strings.ToLower(w)] {
			return w
		}
	}
	return ""
}

func isLikelySymbol(word string) bool {
	if len(word) > 1 && word[0] >= 'A' && word[0] <= 'Z' {
		for _, ch := range word[1:] {
			if ch >= 'a' && ch <= 'z' {
				return true
			}
		}
	}
	return strings.Contains(word, "_")
}

//  Fuzzy search

func (r *Router) fuzzySearch(entity string) []string {

	var matches []match
	low := strings.ToLower(entity)

	for name := range r.kbIndex.FunctionsByName {
		if s := fuzzyScore(low, strings.ToLower(name)); s > 0 {
			matches = append(matches, match{name, s, "function"})
		}
	}
	for name := range r.kbIndex.TypesByName {
		if s := fuzzyScore(low, strings.ToLower(name)); s > 0 {
			matches = append(matches, match{name, s, "type"})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].score > matches[j].score })

	var out []string
	for i, m := range matches {
		if i >= 5 {
			break
		}
		out = append(out, fmt.Sprintf("%s (%s)", m.name, m.typ))
	}
	return out
}

func newCallGraphIndex() *callGraphIndex {
	return &callGraphIndex{cache: make(map[string]string, 256)}
}

func fuzzyScore(pattern, target string) int {
	if pattern == target {
		return 1000
	}
	if strings.Contains(target, pattern) {
		return 500
	}
	score := 0
	for i := 0; i < len(pattern) && i < len(target); i++ {
		if pattern[i] == target[i] {
			score += 10
		}
	}
	freq := make(map[rune]int)
	for _, ch := range pattern {
		freq[ch]++
	}
	for _, ch := range target {
		if freq[ch] > 0 {
			score += 2
			freq[ch]--
		}
	}
	diff := len(target) - len(pattern)
	if diff < 0 {
		diff = -diff
	}
	return score - diff
}

// dedupe preserves order while removing duplicates, since kb_index.json
// can contain repeated entries (e.g. "Pass" appears twice at the same location).
func dedupe(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// parseLocation splits "path/to/file.go:42" into (path, lineNum, err).
func parseLocation(loc string) (string, int, error) {
	i := strings.LastIndex(loc, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("no line number in %q", loc)
	}
	line, err := strconv.Atoi(loc[i+1:])
	if err != nil {
		return "", 0, fmt.Errorf("invalid line number in %q", loc)
	}
	return loc[:i], line, nil
}

// extractSignature opens the file at the given 1-based line number,
// detects the language, and reads the full function signature.
func extractSignature(filePath string, startLine int) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		// try relative to project root via env or cwd
		return "", fmt.Errorf("open %s: %w", filePath, err)
	}

	lines := strings.Split(string(data), "\n")
	if startLine < 1 || startLine > len(lines) {
		return "", fmt.Errorf("line %d out of range (file has %d lines)", startLine, len(lines))
	}

	lang := detectLang(filePath)
	return parseSig(lines, startLine-1, lang) // convert to 0-based
}

func detectLang(path string) language {
	switch filepath.Ext(path) {
	case ".go":
		return langGo
	case ".rs":
		return langRust
	case ".py":
		return langPython
	case ".ts", ".tsx", ".js", ".jsx":
		return langTS
	case ".c", ".cpp", ".cc", ".cxx", ".h", ".hpp":
		return langC
	default:
		return langGo
	}
}

// parseSig reads lines starting at idx and collects the full signature
// up to (but not including) the opening brace / colon / arrow body.
func parseSig(lines []string, idx int, lang language) (string, error) {
	var collected []string
	depth := 0 // paren depth for multi-line signatures

	for i := idx; i < len(lines) && i < idx+40; i++ {
		line := lines[i]
		collected = append(collected, line)

		switch lang {
		case langPython:
			// def foo(a: int, b: str = "x") -> None:
			// collect until we hit the closing ) then the colon
			for _, ch := range line {
				switch ch {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth <= 0 && strings.Contains(line, ":") {
				return formatSig(collected, lang), nil
			}

		case langRust:
			// fn foo(a: i32, b: &str) -> Result<(), Error> {
			for _, ch := range line {
				switch ch {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth <= 0 && (strings.Contains(line, "{") || strings.Contains(line, ";")) {
				return formatSig(collected, lang), nil
			}

		case langTS:
			// function foo(a: string, b: number): void {
			// or arrow: const foo = (a: string): void => {
			for _, ch := range line {
				switch ch {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth <= 0 && (strings.Contains(line, "{") || strings.Contains(line, "=>")) {
				return formatSig(collected, lang), nil
			}

		case langC:
			// int foo(int a, const char* b) {
			for _, ch := range line {
				switch ch {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth <= 0 && strings.Contains(line, "{") {
				return formatSig(collected, lang), nil
			}

		default: // Go
			// func (r *Router) Foo(a int, b string) (string, error) {
			for _, ch := range line {
				switch ch {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth <= 0 && strings.Contains(line, "{") {
				return formatSig(collected, lang), nil
			}
		}
	}

	// Hit the line limit — return what we have
	return formatSig(collected, lang), nil
}

// formatSig trims the body and formats the signature block for display.
func formatSig(lines []string, lang language) string {
	// Drop everything after the opening brace / colon on the last line
	if len(lines) == 0 {
		return ""
	}

	last := lines[len(lines)-1]
	var cutAt string
	switch lang {
	case langPython:
		cutAt = ":"
	default:
		cutAt = "{"
	}

	if i := strings.Index(last, cutAt); i >= 0 {
		lines[len(lines)-1] = strings.TrimRight(last[:i], " \t")
	}

	// Trim trailing blank lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  ")
		b.WriteString(strings.TrimRight(l, " \t"))
		b.WriteByte('\n')
	}
	return b.String()
}

// stripCommandPrefix removes a leading command word/phrase from query
// so entity extraction sees only the symbol name.
func stripCommandPrefix(query string, prefixes ...string) string {
	lower := strings.ToLower(strings.TrimSpace(query))
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p+" ") {
			return strings.TrimSpace(query[len(p):])
		}
	}
	return query
}
