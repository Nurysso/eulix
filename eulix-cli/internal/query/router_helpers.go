//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides query classification functionality.

/*
This file is responsible for Helpers used in Query routing.
*/
package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"eulix/internal/query/classifier"
	"eulix/internal/query/retrieval"
	"eulix/internal/utils"
)

type (
	language int
	match    struct {
		name  string
		score int
		typ   string
	}
)

const (
	langUnknown language = iota
	langGo
	langRust
	langPython
	langTS
	langC // covers C and C++
	langJava
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
	//if r.config.Project.DebugConfig {
	// fmt.Printf("[INFO] Initializing context builder with source root: %s\n", sourceRoot)
	//}
	cb, err := retrieval.ContextWindowCreator(r.eulixDir, r.config, r.llmClient, sourceRoot, r.debug)
	if err != nil {
		return fmt.Errorf("failed to initialize context builder: %w", err)
	}
	r.contextBuilder = cb
	if r.kbIndex == nil {
		r.kbIndex = cb.GetKBIndex()
	}
	if r.callGraph == nil {
		r.callGraph = buildRouterCallGraph(cb.GetCallGraphRef())
	}
	if r.cgBuild == nil {
		r.cgBuild = BuildCallGraphIndex(cb.GetCallGraphRef())
	}
	return nil
}

// bareID strips the node-type prefix and file path that eulix-parser emits.
func bareID(id string) string {
	if i := strings.Index(id, "::"); i != -1 {
		id = id[:i]
	}
	prefixes := []string{"func_", "method_", "class_", "struct_", "enum_", "interface_", "type_"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			s := strings.TrimPrefix(id, prefix)
			if prefix == "method_" {
				if i := strings.Index(s, "_"); i != -1 {
					return s[:i] + "." + s[i+1:]
				}
			}
			return s
		}
	}
	return id
}

// hasSourceCode checks whether any context chunk contains an actual code fence.
func hasSourceCode(ctx *utils.ContextWindow) bool {
	for _, chunk := range ctx.Chunks {
		if strings.Contains(chunk.Content, "```") {
			return true
		}
	}
	return false
}

// firstSymbolOrExtracted returns the first classified symbol or falls back to
// heuristic extraction from the raw query string.
func firstSymbolOrExtracted(class *classifier.Classification, query string) string {
	// Words that are metrics commands, not actual symbols
	metricsCommands := map[string]bool{
		"metrics":    true,
		"summary":    true,
		"overall":    true,
		"project":    true,
		"statistics": true,
		"show":       true,
		"get":        true,
		"display":    true,
		"view":       true,
		"print":      true,
		"fetch":      true,
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

func formatFileData(path string, fd *utils.FileData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s  [%s, %d LOC]\n", path, fd.Language, fd.Loc)

	if len(fd.Functions) > 0 {
		fmt.Fprintf(&b, "\nFunctions (%d):\n", len(fd.Functions))
		for _, fn := range fd.Functions {
			fmt.Fprintf(&b, "  • %s  (lines %d–%d, complexity %d, importance %.2f)\n",
				fn.Name, fn.LineStart, fn.LineEnd, fn.Complexity, fn.ImportanceScore)
		}
	}

	if len(fd.Classes) > 0 {
		fmt.Fprintf(&b, "\nClasses (%d):\n", len(fd.Classes))
		for _, cls := range fd.Classes {
			fmt.Fprintf(&b, "  • %s  (lines %d–%d, %d methods)\n",
				cls.Name, cls.LineStart, cls.LineEnd, len(cls.Methods))
		}
	}

	if len(fd.Todos) > 0 {
		fmt.Fprintf(&b, "\nTODOs (%d):\n", len(fd.Todos))
		for _, td := range fd.Todos {
			fmt.Fprintf(&b, "  [%s] line %d: %s\n", td.Priority, td.Line, td.Text)
		}
	}

	if len(fd.SecurityNotes) > 0 {
		fmt.Fprintf(&b, "\nSecurity notes (%d):\n", len(fd.SecurityNotes))
		for _, sn := range fd.SecurityNotes {
			fmt.Fprintf(&b, "  [%s] line %d: %s\n", sn.NoteType, sn.Line, sn.Description)
		}
	}

	return b.String()
}

// Helper to handle the metric formatting
func formatFunctionMetrics(fn utils.KBFunction, path string) string {
	return fmt.Sprintf(
		"Metrics for %s (%s, lines %d–%d)\n  Cyclomatic complexity : %d\n  LOC                   : %d\n  Importance            : %.2f\n",
		fn.Name, path, fn.LineStart, fn.LineEnd,
		fn.Complexity, fn.LineEnd-fn.LineStart+1, fn.ImportanceScore,
	)
}

// Entity extraction
func extractFilePath(query string) string {
	// Exclude '.' from punctuation cutset so file extensions and relative paths stay intact
	punctuationCutset := ",;:!?()[]{}\"'`"

	for _, word := range strings.Fields(query) {
		cleaned := strings.Trim(word, punctuationCutset)
		if cleaned == "" {
			continue
		}

		// Explicit path separators take precedence regardless of length (e.g. "x/y", "internal/query/core.go")
		if strings.ContainsAny(cleaned, "/\\") {
			return cleaned
		}

		// Non-path words must be longer than 3 characters (skips "a.b", ".go")
		if len(cleaned) <= 3 {
			continue
		}

		// Valid file extension matching a known language (e.g., "main.go")
		if ext := filepath.Ext(cleaned); ext != "" && detectLang(cleaned) != langUnknown {
			return cleaned
		}
	}
	return ""
}

func extractEntityName(query string) string {
	words := strings.Fields(query)
	stopWords := map[string]bool{
		"where": true, "is": true, "are": true, "was": true, "were": true,
		"be": true, "been": true, "being": true, "the": true, "function": true,
		"class": true, "method": true, "type": true, "find": true,
		"locate": true, "what": true, "does": true, "do": true, "did": true,
		"who": true, "calls": true, "call": true, "uses": true, "used": true,
		"use": true, "using": true, "a": true, "an": true, "this": true,
		"that": true, "these": true, "those": true, "how": true, "can": true,
		"will": true, "should": true, "would": true, "could": true,
		"explain": true, "graph": true, "graphs": true, "tree": true,
		"trees": true, "build": true, "built": true, "building": true,
		"create": true, "created": true, "creating": true, "generate": true,
		"generated": true, "generating": true, "make": true, "made": true,
		"making": true, "show": true, "display": true, "get": true,
		"list": true, "view": true, "print": true, "fetch": true,
	}

	punctuationCutset := ".,;:!?()[]{}\"'`"

	// Cleaned words slice to avoid trimming repeatedly
	cleanedWords := make([]string, 0, len(words))
	for _, w := range words {
		cleaned := strings.Trim(w, punctuationCutset)
		if cleaned != "" {
			cleanedWords = append(cleanedWords, cleaned)
		}
	}

	// First pass: look for stop-word filtered words that are likely code symbols
	for _, w := range cleanedWords {
		if !stopWords[strings.ToLower(w)] && isLikelySymbol(w) {
			return w
		}
	}

	// Second pass: fallback to the first non-stop word
	for _, w := range cleanedWords {
		if !stopWords[strings.ToLower(w)] {
			return w
		}
	}

	return ""
}

func isLikelySymbol(w string) bool {
	if len(w) == 0 {
		return false
	}

	// Single-character cases: only "_" is considered a likely symbol
	if len(w) == 1 {
		return w == "_"
	}

	// Contains underscores or dots (e.g. "foo_bar", "pkg.Func")
	if strings.ContainsAny(w, "_.") {
		return true
	}

	// Must consist of valid identifier characters (letters/digits)
	for i, r := range w {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}

	hasUpper := false
	hasLower := false
	for _, r := range w {
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}

	// All-caps ("FOO", "HTTP") and all-lowercase ("foo") words are false.
	// Must be mixed casing (PascalCase like "Foo", camelCase, "HTTPServer").
	return hasUpper && hasLower
}

func (r *Router) fuzzySearch(entity string) []string {
	if r == nil || r.kbIndex == nil {
		return nil
	}

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

	// Primary sort by score (descending), secondary sort by name (ascending)
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if matches[i].name != matches[j].name {
			return matches[i].name < matches[j].name
		}
		return matches[i].typ < matches[j].typ
	})

	var out []string
	for i, m := range matches {
		if i >= 5 {
			break
		}
		out = append(out, fmt.Sprintf("%s (%s)", m.name, m.typ))
	}
	return out
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

func dedupStrings(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	out := s[:0]
	for _, v := range s {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
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
	switch strings.ToLower(filepath.Ext(path)) {
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
	if idx >= len(lines) || idx < 0 {
		return "", nil
	}

	var collected []string
	depth := 0 // paren/bracket depth for multi-line signatures

	for i := idx; i < len(lines) && i < idx+40; i++ {
		line := lines[i]
		collected = append(collected, line)

		for _, ch := range line {
			switch ch {
			case '(', '[':
				depth++
			case ')', ']':
				depth--
			}
		}

		if depth <= 0 {
			switch lang {
			case langPython:
				// def foo(a: int, b: str = "x") -> None:
				if strings.Contains(line, ":") {
					return formatSig(collected, lang), nil
				}

			case langRust:
				// fn foo(a: i32, b: &str) -> Result<(), Error> {
				if strings.Contains(line, "{") || strings.Contains(line, ";") {
					return formatSig(collected, lang), nil
				}

			case langTS:
				// function foo(a: string, b: number): void {
				// or arrow: const foo = (a: string): void => {
				if strings.Contains(line, "{") || strings.Contains(line, "=>") {
					return formatSig(collected, lang), nil
				}

			case langC:
				// int foo(int a, const char* b) {
				if strings.Contains(line, "{") {
					return formatSig(collected, lang), nil
				}

			default: // Go
				// func (r *Router) Foo(a int, b string) (string, error) {
				if strings.Contains(line, "{") {
					return formatSig(collected, lang), nil
				}
			}
		}
	}

	// Hit the line limit — return what we have
	return formatSig(collected, lang), nil
}

// formatSig trims the body and formats the signature block for display.
func formatSig(lines []string, lang language) string {
	if len(lines) == 0 {
		return ""
	}

	// Make a shallow copy of lines so we don't mutate input slice
	lines = append([]string(nil), lines...)

	// Compute accumulated paren/bracket/brace depth across lines up to each character
	// so we accurately locate top-level opening terminators ({ or :).
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		cutIdx := -1

		// Calculate total depth up to the start of line i
		lineStartDepth := 0
		for k := 0; k < i; k++ {
			for _, ch := range lines[k] {
				switch ch {
				case '(', '[':
					lineStartDepth++
				case ')', ']':
					lineStartDepth--
				}
			}
		}

		currentDepth := lineStartDepth
		if lang == langPython {
			for j, ch := range line {
				switch ch {
				case '(', '{', '[':
					currentDepth++
				case ')', '}', ']':
					currentDepth--
				case ':':
					if currentDepth == 0 {
						cutIdx = j
					}
				}
			}
		} else {
			for j, ch := range line {
				switch ch {
				case '(', '[':
					currentDepth++
				case ')', ']':
					currentDepth--
				case '{':
					if currentDepth == 0 {
						cutIdx = j
					}
				}
			}
		}

		if cutIdx >= 0 {
			lines[i] = strings.TrimRight(line[:cutIdx], " \t")
			lines = lines[:i+1] // Drop any lines after the signature cutoff
			break
		}
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
	trimmed := strings.TrimSpace(query)
	lower := strings.ToLower(trimmed)
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p+" ") {
			return strings.TrimSpace(trimmed[len(p):])
		}
	}
	return trimmed
}

// resolveCallGraphEntity tries the bare name, then common prefixes,
// then a linear scan matching on the short name suffix.
// Returns the matched key, the function data, and whether it was found.
func (r *Router) resolveCallGraphEntity(name string) (string, *utils.CallGraphNode, bool, []string) {
	if r.cgBuild == nil {
		return "", nil, false, nil
	}

	// Exact node ID match
	if n, ok := r.cgBuild.Nodes[name]; ok {
		return name, n, true, nil
	}

	// Simple prefix variants
	prefixes := []string{"func_", "method_", "class_", "struct_", "enum_", "interface_", "type_"}
	for _, prefix := range prefixes {
		key := prefix + name
		if n, ok := r.cgBuild.Nodes[key]; ok {
			return key, n, true, nil
		}
	}

	// Short-name and bareID suffix scan
	lower := strings.ToLower(name)

	var bestKey string
	var bestNode *utils.CallGraphNode
	bestScore := -1
	var ambiguous []string // all keys matching `name`

	for key, n := range r.cgBuild.Nodes {
		short := strings.ToLower(callGraphShortName(key))
		bare := strings.ToLower(bareID(key))

		// Exact match against short name or bare ID (e.g. "generate_vectors_streaming" or "Resolver.resolve")
		if short == lower || bare == lower {
			score := n.CallCountEstimate
			if n.NodeType == "function" || n.NodeType == "method" {
				score += 1000
			}
			if score > bestScore {
				bestScore = score
				bestKey = key
				bestNode = n
			}
			ambiguous = append(ambiguous, key)
			continue
		}

		// Prefix match (e.g., "build_call_graph" matches "build_call_graph_v2")
		if strings.HasPrefix(short, lower) || strings.HasPrefix(bare, lower) {
			ambiguous = append(ambiguous, key)
		}
	}

	if bestNode != nil {
		return bestKey, bestNode, true, ambiguous
	}
	if len(ambiguous) > 0 {
		// Only prefix matches; return highest score candidate
		bestScore = -1
		for _, key := range ambiguous {
			n := r.cgBuild.Nodes[key]
			score := n.CallCountEstimate
			if n.NodeType == "function" || n.NodeType == "method" {
				score += 1000
			}
			if score > bestScore {
				bestScore = score
				bestKey = key
				bestNode = n
			}
		}
		return bestKey, bestNode, true, ambiguous
	}

	return "", nil, false, nil
}

func BuildCallGraphIndex(ref *utils.CallGraphRef) *retrieval.CallGraphIdx {
	if ref == nil {
		return &retrieval.CallGraphIdx{
			Nodes:    make(map[string]*utils.CallGraphNode),
			CalledBy: make(map[string][]string),
			Calls:    make(map[string][]string),
		}
	}

	idx := &retrieval.CallGraphIdx{
		Nodes:    make(map[string]*utils.CallGraphNode, len(ref.Nodes)),
		CalledBy: make(map[string][]string, len(ref.Nodes)),
		Calls:    make(map[string][]string, len(ref.Nodes)),
	}

	for i := range ref.Nodes {
		n := &ref.Nodes[i]
		idx.Nodes[n.ID] = n
	}

	for _, e := range ref.Edges {
		if e.EdgeType == "call" {
			idx.Calls[e.From] = append(idx.Calls[e.From], e.To)
			idx.CalledBy[e.To] = append(idx.CalledBy[e.To], e.From)
		}
	}
	for id, callers := range idx.CalledBy {
		idx.CalledBy[id] = dedupStrings(callers)
	}
	for id, callees := range idx.Calls {
		idx.Calls[id] = dedupStrings(callees)
	}
	return idx
}

// callGraphShortName extracts the bare symbol name from a prefixed/namespaced key.
// "func_generate_vectors_streaming::eulix-embed/pipeline/onnx_pipeline.py" → "generate_vectors_streaming"
func callGraphShortName(key string) string {
	// Strip file path suffix separated by ::
	if idx := strings.Index(key, "::"); idx != -1 {
		key = key[:idx]
	}

	// Strip known type prefixes
	prefixes := []string{"func_", "method_", "class_", "struct_", "enum_", "interface_", "type_"}
	for _, prefix := range prefixes {
		after, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		// For methods: "method_ClassName_methodName" → strip "ClassName_" too.
		if prefix == "method_" {
			if idx := strings.Index(after, "_"); idx != -1 {
				return after[idx+1:] // "Analyzer_build_call_graph" → "build_call_graph"
			}
		}
		return after
	}
	return key
}

type depIntent int

type depEntry struct {
	dep      *utils.ExternalDependency
	nameLow  string
	rootLow  string
	segments []string
	tokens   []string
}

const (
	depIntentLookup  depIntent = iota // default: find a specific dep by name
	depIntentAll                      // list everything
	depIntentFile                     // what does file X import
	depIntentWhoUses                  // who imports dep X
	depIntentCount                    // how many deps total
)

var externalDeps []utils.ExternalDependency

func getExternalDeps() []utils.ExternalDependency {
	return externalDeps
}

func loadExternalDeps(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read deps file: %w", err)
	}

	var wrapper utils.ExternalDependencyRef
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	externalDeps = wrapper.ExternalDependencies
	return nil
}

// nolint: unused
var (
	depCountPhrases = []string{"how many", "count", "total", "number of"}
	depBroadPhrases = []string{
		"all dependencies", "list dependencies", "all imports",
		"all dependency", "dependencies of", "dependency of",
		"what dependencies", "which dependencies", "show dependencies",
		"show all", "what are the", "project dependencies",
		"project imports", "this project",
	}
	depWhoUsesPhrases = []string{
		"who imports", "who uses", "what imports", "what uses",
		"which files import", "which files use", "used by", "depends on",
	}
	depFilePhrases = []string{"imports in", "what does", "used in", "file imports"}
)

var depCountRegex = regexp.MustCompile(`(?i)\b(count|how\s+many|total)\b`)

func classifyDepIntent(queryLow, entityLow string) depIntent {
	if depCountRegex.MatchString(queryLow) || strings.Contains(queryLow, "number of") {
		return depIntentCount
	}

	broadEntity := entityLow == "all" || entityLow == "list" || entityLow == "project" || entityLow == "this" || entityLow == "everything"

	// Only return all dependencies if the entity itself is broad
	if broadEntity || classifier.ContainsAny(queryLow, depBroadPhrases) {
		return depIntentAll
	}

	// Explicit phrasing beats the filename-shape heuristic — "which files
	// use X" must win even when X itself contains a "/".
	if classifier.ContainsAny(queryLow, depWhoUsesPhrases) {
		return depIntentWhoUses
	}
	if classifier.ContainsAny(queryLow, depFilePhrases) || looksLikeFilePath(entityLow) {
		return depIntentFile
	}

	return depIntentLookup
}

type depIndex struct {
	entries  []depEntry
	byFile   map[string][]*utils.ExternalDependency // lowercased exact path -> deps
	fileKeys []string                               // sorted keys, for substring fallback
}

func buildDepIndex(deps []utils.ExternalDependency) *depIndex {
	idx := &depIndex{
		entries: make([]depEntry, len(deps)),
		byFile:  make(map[string][]*utils.ExternalDependency),
	}
	isTokenSep := func(r rune) bool {
		return !unicode.IsLower(r) && !unicode.IsDigit(r)
	}
	for i := range deps {
		d := &deps[i]
		nameLow := strings.ToLower(d.Name)

		root := nameLow
		if j := strings.Index(nameLow, "::"); j != -1 {
			root = nameLow[:j]
		}

		var segments []string
		if strings.Contains(nameLow, "/") {
			segments = strings.Split(nameLow, "/")
		}
		idx.entries[i] = depEntry{
			dep:      d,
			nameLow:  nameLow,
			rootLow:  root,
			segments: segments,
			tokens:   strings.FieldsFunc(nameLow, isTokenSep),
		}
		for _, f := range d.UsedBy {
			idx.byFile[f] = append(idx.byFile[f], d)
		}
	}
	idx.fileKeys = make([]string, 0, len(idx.byFile))
	for k := range idx.byFile {
		idx.fileKeys = append(idx.fileKeys, k)
	}
	sort.Strings(idx.fileKeys)

	return idx
}

func (idx *depIndex) filesMatching(term string) []*utils.ExternalDependency {
	termLow := strings.ToLower(term)
	if deps, ok := idx.byFile[term]; ok {
		return deps
	}
	if deps, ok := idx.byFile[termLow]; ok {
		return deps
	}

	var matched []*utils.ExternalDependency
	seen := make(map[*utils.ExternalDependency]bool)

	for _, fk := range idx.fileKeys {
		if !strings.Contains(strings.ToLower(fk), termLow) {
			continue
		}
		for _, d := range idx.byFile[fk] {
			// For general directory matching, keep file hits
			// For file extension queries (starting with '.'), deduplicate unique dependencies
			if strings.HasPrefix(termLow, ".") {
				if !seen[d] {
					seen[d] = true
					matched = append(matched, d)
				}
			} else {
				matched = append(matched, d)
			}
		}
	}
	return matched
}

func matchDeps(deps []utils.ExternalDependency, queryLow string) []utils.ExternalDependency {
	var matched []utils.ExternalDependency
	for _, dep := range deps {
		if strings.Contains(strings.ToLower(dep.Name), queryLow) {
			matched = append(matched, dep)
		}
	}
	return matched
}

func formatMatchedDeps(entity string, deps []utils.ExternalDependency) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d dependencies matching '%s':\n\n", len(deps), entity)
	for _, dep := range deps {
		versionStr := "N/A"
		if dep.Version != nil {
			versionStr = *dep.Version
		}

		fmt.Fprintf(&sb, "• %s (v%s)\n", dep.Name, versionStr)
		fmt.Fprintf(&sb, "  Source: %s\n", dep.Source)
		fmt.Fprintf(&sb, "  Import Count: %d\n", dep.ImportCount)
		if len(dep.UsedBy) > 0 {
			fmt.Fprintf(&sb, "  Used by (%d files):\n", len(dep.UsedBy))
			for _, file := range dep.UsedBy {
				fmt.Fprintf(&sb, "    - %s\n", file)
			}
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

func formatAllExternalDeps(deps []utils.ExternalDependency) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Total External Dependencies: %d\n\n", len(deps))

	for _, dep := range deps {
		versionStr := "N/A"
		if dep.Version != nil {
			versionStr = *dep.Version
		}
		fmt.Fprintf(&sb, "• %s (v%s) [%s] - Imported %d times across %d files\n",
			dep.Name, versionStr, dep.Source, dep.ImportCount, len(dep.UsedBy))
	}

	return strings.TrimSpace(sb.String())
}

func formatDepCount(deps []utils.ExternalDependency) string {
	return fmt.Sprintf("Total external dependencies tracked: %d", len(deps))
}

func (e *depEntry) matches(term string) bool {
	term = strings.ToLower(term)

	// Check if term matches the lowercased name
	if strings.Contains(e.nameLow, term) {
		return true
	}

	// Check if term matches the lowercased root
	if strings.Contains(e.rootLow, term) {
		return true
	}

	// Check if term matches any segment
	for _, seg := range e.segments {
		if strings.Contains(seg, term) {
			return true
		}
	}

	// Check if term matches any token
	for _, token := range e.tokens {
		if strings.Contains(token, term) {
			return true
		}
	}

	return false
}

// func formatDepCount(deps []utils.ExternalDependency) string {
// 	bySource := make(map[string]int, 4)
// 	for _, d := range deps {
// 		src := d.Source
// 		if src == "" {
// 			src = "unknown"
// 		}
// 		bySource[src]++
// 	}
// 	sources := make([]string, 0, len(bySource))
// 	for s := range bySource {
// 		sources = append(sources, s)
// 	}
// 	sort.Strings(sources)

// 	var b strings.Builder
// 	fmt.Fprintf(&b, "Total dependencies: %d\n", len(deps))
// 	for _, s := range sources {
// 		fmt.Fprintf(&b, "  %s: %d\n", s, bySource[s])
// 	}
// 	return b.String()
// }

func formatFileImports(file string, idx *depIndex) string {
	matched := idx.filesMatching(file)
	if len(matched) == 0 {
		return fmt.Sprintf("No recorded imports found for '%s'.", file)
	}

	// Sort deterministically by Name, then Source, then Version
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Name != matched[j].Name {
			return matched[i].Name < matched[j].Name
		}
		if matched[i].Source != matched[j].Source {
			return matched[i].Source < matched[j].Source
		}
		vI, vJ := "", ""
		if matched[i].Version != nil {
			vI = *matched[i].Version
		}
		if matched[j].Version != nil {
			vJ = *matched[j].Version
		}
		return vI < vJ
	})

	var b strings.Builder
	fmt.Fprintf(&b, "Imports in '%s' (%d):\n", file, len(matched))
	for i, d := range matched {
		ver := "(unpinned)"
		if d.Version != nil && *d.Version != "" {
			ver = fmt.Sprintf("(v%s)", *d.Version)
		}
		fmt.Fprintf(&b, "\u2022 %s %s [%s]", d.Name, ver, d.Source)
		if i < len(matched)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// func formatMatchedDeps(query string, deps []*utils.ExternalDependency) string {
// 	var b strings.Builder

// 	if len(deps) == 1 {
// 		d := deps[0]
// 		ver := "(unpinned)"
// 		if d.Version != nil {
// 			ver = *d.Version
// 		}
// 		usedBy := make([]string, len(d.UsedBy))
// 		copy(usedBy, d.UsedBy)
// 		sort.Strings(usedBy)

// 		fmt.Fprintf(&b, "Dependency  : %s\n", d.Name)
// 		fmt.Fprintf(&b, "Version     : %s\n", ver)
// 		fmt.Fprintf(&b, "Source      : %s\n", d.Source)
// 		fmt.Fprintf(&b, "Import count: %d\n", d.ImportCount)
// 		fmt.Fprintf(&b, "\nUsed by (%d file(s)):\n", len(usedBy))
// 		for _, f := range usedBy {
// 			fmt.Fprintf(&b, "  • %s\n", f)
// 		}
// 		return b.String()
// 	}

// 	// multiple matches — sort deps alphabetically, files within each dep too
// 	fmt.Fprintf(&b, "%d dependencies matched '%s':\n\n", len(deps), query)
// 	for _, d := range deps {
// 		ver := "(unpinned)"
// 		if d.Version != nil {
// 			ver = *d.Version
// 		}
// 		files := make([]string, len(d.UsedBy))
// 		copy(files, d.UsedBy)
// 		sort.Strings(files)

// 		fmt.Fprintf(&b, "  %-40s  %-12s  %d import(s)\n", d.Name, ver, d.ImportCount)
// 		for _, f := range files {
// 			fmt.Fprintf(&b, "      • %s\n", f)
// 		}
// 		b.WriteString("\n")
// 	}
// 	return b.String()
// }

// func formatAllExternalDeps(deps []utils.ExternalDependency) string {
// 	var b strings.Builder
// 	bySource := make(map[string][]utils.ExternalDependency)
// 	for _, d := range deps {
// 		src := d.Source
// 		if src == "" {
// 			src = "unknown"
// 		}
// 		bySource[src] = append(bySource[src], d)
// 	}

// 	sources := make([]string, 0, len(bySource))
// 	for s := range bySource {
// 		sources = append(sources, s)
// 	}
// 	sort.Strings(sources)

// 	fmt.Fprintf(&b, "All dependencies (%d total):\n", len(deps))
// 	for _, src := range sources {
// 		group := bySource[src]
// 		// sort deps within each source group
// 		sort.Slice(group, func(i, j int) bool {
// 			return group[i].Name < group[j].Name
// 		})
// 		fmt.Fprintf(&b, "\n[%s — %d]\n", src, len(group))
// 		for _, d := range group {
// 			ver := "(unpinned)"
// 			if d.Version != nil {
// 				ver = *d.Version
// 			}
// 			fmt.Fprintf(&b, "  %-40s  %-12s  %d file(s)\n", d.Name, ver, d.ImportCount)
// 		}
// 	}
// 	return b.String()
// }

var sourceFileExts = []string{
	".go", ".py", ".rs", ".ts", ".tsx", ".js", ".jsx",
	".java", ".c", ".h", ".cpp", ".hpp", ".rb", ".php",
}

func looksLikeFilePath(s string) bool {
	for _, ext := range sourceFileExts {
		if strings.HasSuffix(s, ext) {
			return true
		}
	}
	return false
}

var depStopWords = map[string]bool{
	"depends": true, "depend": true, "on": true,
	"what": true, "which": true, "who": true,
	"imports": true, "import": true, "uses": true,
	"is": true, "are": true, "the": true, "a": true,
	"external": true, "dependency": true, "dependencies": true,
	"required": true, "by": true, "third": true, "party": true,
	"show": true, "list": true, "find": true, "all": true,
}

func extractDepQueryTerm(query string) string {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	for i := len(words) - 1; i >= 0; i-- {
		if w := words[i]; !depStopWords[w] && len(w) >= 2 {
			return w
		}
	}
	return ""
}
