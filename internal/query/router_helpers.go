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

	"corvux/internal/types"
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

// Entity extraction
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

// resolveCallGraphEntity tries the bare name, then common prefixes,
// then a linear scan matching on the short name suffix.
// Returns the matched key, the function data, and whether it was found.
func (r *Router) resolveCallGraphEntity(name string) (string, *types.CallGraphNode, bool, []string) {
	if r.cgBuild == nil {
		return "", nil, false, nil
	}

	// exact key
	if n, ok := r.cgBuild.Nodes[name]; ok {
		return name, n, true, nil
	}

	// simple prefix variants
	for _, prefix := range []string{"func_", "method_", "class_", "struct_"} {
		key := prefix + name
		if n, ok := r.cgBuild.Nodes[key]; ok {
			return key, n, true, nil
		}
	}

	// short-name suffix scan (now correctly strips ClassName_ from methods)
	// Replace tier 3 in resolveCallGraphEntity with this
	lower := strings.ToLower(name)

	var bestKey string
	var bestNode *types.CallGraphNode
	bestScore := -1
	var ambiguous []string // all keys whose short name starts with `name`

	for key, n := range r.cgBuild.Nodes {
		short := strings.ToLower(callGraphShortName(key))

		// Exact short-name match
		if short == lower {
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

		// Prefix match,"build_call_graph" matches "build_call_graph_v2"
		if strings.HasPrefix(short, lower) {
			ambiguous = append(ambiguous, key)
		}
	}

	if bestNode != nil {
		return bestKey, bestNode, true, ambiguous
	}
	if len(ambiguous) > 0 {
		// Only prefix matches, no exact, return best by call count
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

func BuildCallGraphIndex(ref *types.CallGraphRef) *CallGraphIdx {
	if ref == nil {
		return &CallGraphIdx{
			Nodes:    make(map[string]*types.CallGraphNode),
			CalledBy: make(map[string][]string),
			Calls:    make(map[string][]string),
		}
	}

	idx := &CallGraphIdx{
		Nodes:    make(map[string]*types.CallGraphNode, len(ref.Nodes)),
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
		// inheritance edges stored separately if you need them later
	}
	// In BuildCallGraphIndex, after the edge loop, deduplicate
	for id, callers := range idx.CalledBy {
		idx.CalledBy[id] = dedupStrings(callers)
	}
	for id, callees := range idx.Calls {
		idx.Calls[id] = dedupStrings(callees)
	}
	return idx
}

// callGraphShortName extracts the bare symbol name from a prefixed/namespaced key.
// "func_some::path::build_call_graph" → "build_call_graph"
// "method_MyStruct_do_thing"          → "do_thing" (strips method_ prefix)
func callGraphShortName(key string) string {
	// Strip namespace separators first (:: for Rust paths)
	if idx := strings.LastIndex(key, "::"); idx != -1 {
		key = key[idx+2:]
	}

	// Strip known type prefixes
	for _, prefix := range []string{"func_", "method_", "class_"} {
		after, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		// For methods: "method_ClassName_methodName" → strip "ClassName_" too.
		// The class name is everything up to the first underscore in the remainder,
		// but only when another underscore exists (so bare "method_foo" stays "foo").
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
	dep      *types.ExternalDependency
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

func (cb *ContextBuilder) GetExternalDeps() []types.ExternalDependency {
	return cb.externalDeps
}

// depNameMatches returns true when the dep name is a fuzzy match for the query term.
// Handles cases like "serde" matching "serde::{Deserialize, Serialize}".
func depNameMatches(name, term string) bool {
	nameLow := strings.ToLower(name)

	// Exact substring — fastest, covers most cases
	if strings.Contains(nameLow, term) {
		return true
	}

	// The dep name may be "pkg::{A, B}" or "pkg::module::Type" —
	//    extract the root segment and check that alone.
	root := nameLow
	if idx := strings.Index(nameLow, "::"); idx != -1 {
		root = nameLow[:idx]
	} else if idx := strings.Index(nameLow, "/"); idx != -1 {
		// Go-style: "github.com/foo/bar" → check each segment
		for _, seg := range strings.Split(nameLow, "/") {
			if strings.Contains(seg, term) {
				return true
			}
		}
	}
	if strings.Contains(root, term) {
		return true
	}

	// Token match — split on non-alpha and check each token
	tokens := strings.FieldsFunc(nameLow, func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	})
	for _, tok := range tokens {
		if tok == term {
			return true
		}
	}

	return false
}

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

func classifyDepIntent(queryLow, entityLow string) depIntent {
	if containsAny(queryLow, depCountPhrases) {
		return depIntentCount
	}

	broadEntity := entityLow == "all" || entityLow == "list" || entityLow == "project"
	if broadEntity || containsAny(queryLow, depBroadPhrases) {
		return depIntentAll
	}

	// Explicit phrasing beats the filename-shape heuristic — "which files
	// use X" must win even when X itself contains a "/".
	if containsAny(queryLow, depWhoUsesPhrases) {
		return depIntentWhoUses
	}
	if containsAny(queryLow, depFilePhrases) || looksLikeFilePath(entityLow) {
		return depIntentFile
	}

	return depIntentLookup
}

func (e *depEntry) depMatches(term string) bool {
	if strings.Contains(e.nameLow, term) || strings.Contains(e.rootLow, term) {
		return true
	}
	for _, segment := range e.segments {
		if strings.Contains(segment, term) {
			return true
		}
		for _, tokens := range e.tokens {
			if tokens == term {
				return true
			}
		}
	}
	return false
}

type depIndex struct {
	entries  []depEntry
	byFile   map[string][]*types.ExternalDependency // lowercased exact path -> deps
	fileKeys []string                               // sorted keys, for substring fallback
}

func buildDepIndex(deps []types.ExternalDependency) *depIndex {
	idx := &depIndex{
		entries: make([]depEntry, len(deps)),
		byFile:  make(map[string][]*types.ExternalDependency),
	}
	isTokenSep := func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
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
			fl := strings.ToLower(f)
			idx.byFile[fl] = append(idx.byFile[fl], d)
		}
	}
	idx.fileKeys = make([]string, 0, len(idx.byFile))
	for k := range idx.byFile {
		idx.fileKeys = append(idx.fileKeys, k)
	}
	sort.Strings(idx.fileKeys)

	return idx
}

func (idx *depIndex) matchDeps(term string) []*types.ExternalDependency {
	matched := make([]*types.ExternalDependency, 0, 4)
	for i := range idx.entries {
		if idx.entries[i].matches(term) {
			matched = append(matched, idx.entries[i].dep)
		}
	}
	return matched
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

func (idx *depIndex) filesMatching(term string) []*types.ExternalDependency {
	if deps, ok := idx.byFile[term]; ok {
		return deps
	}
	seen := make(map[string]bool)
	var matched []*types.ExternalDependency
	for _, fk := range idx.fileKeys {
		if !strings.Contains(fk, term) {
			continue
		}
		for _, d := range idx.byFile[fk] {
			if !seen[d.Name] {
				seen[d.Name] = true
				matched = append(matched, d)
			}
		}
	}
	return matched
}

func formatDepCount(deps []types.ExternalDependency) string {
	bySource := make(map[string]int, 4)
	for _, d := range deps {
		src := d.Source
		if src == "" {
			src = "unknown"
		}
		bySource[src]++
	}
	sources := make([]string, 0, len(bySource))
	for s := range bySource {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	var b strings.Builder
	fmt.Fprintf(&b, "Total dependencies: %d\n", len(deps))
	for _, s := range sources {
		fmt.Fprintf(&b, "  %s: %d\n", s, bySource[s])
	}
	return b.String()
}

func formatFileImports(file string, idx *depIndex) string {
	matched := idx.filesMatching(file)
	if len(matched) == 0 {
		return fmt.Sprintf("No recorded imports found for '%s'.", file)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })

	var b strings.Builder
	fmt.Fprintf(&b, "Imports in '%s' (%d):\n", file, len(matched))
	for _, d := range matched {
		ver := "(unpinned)"
		if d.Version != nil {
			ver = *d.Version
		}
		fmt.Fprintf(&b, "  • %-40s  %-12s  [%s]\n", d.Name, ver, d.Source)
	}
	return b.String()
}

func formatMatchedDeps(query string, deps []*types.ExternalDependency) string {
	var b strings.Builder

	if len(deps) == 1 {
		d := deps[0]
		ver := "(unpinned)"
		if d.Version != nil {
			ver = *d.Version
		}
		usedBy := make([]string, len(d.UsedBy))
		copy(usedBy, d.UsedBy)
		sort.Strings(usedBy)

		fmt.Fprintf(&b, "Dependency  : %s\n", d.Name)
		fmt.Fprintf(&b, "Version     : %s\n", ver)
		fmt.Fprintf(&b, "Source      : %s\n", d.Source)
		fmt.Fprintf(&b, "Import count: %d\n", d.ImportCount)
		fmt.Fprintf(&b, "\nUsed by (%d file(s)):\n", len(usedBy))
		for _, f := range usedBy {
			fmt.Fprintf(&b, "  • %s\n", f)
		}
		return b.String()
	}

	// multiple matches — sort deps alphabetically, files within each dep too
	fmt.Fprintf(&b, "%d dependencies matched '%s':\n\n", len(deps), query)
	for _, d := range deps {
		ver := "(unpinned)"
		if d.Version != nil {
			ver = *d.Version
		}
		files := make([]string, len(d.UsedBy))
		copy(files, d.UsedBy)
		sort.Strings(files)

		fmt.Fprintf(&b, "  %-40s  %-12s  %d import(s)\n", d.Name, ver, d.ImportCount)
		for _, f := range files {
			fmt.Fprintf(&b, "      • %s\n", f)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func formatAllExternalDeps(deps []types.ExternalDependency) string {
	var b strings.Builder
	bySource := make(map[string][]types.ExternalDependency)
	for _, d := range deps {
		src := d.Source
		if src == "" {
			src = "unknown"
		}
		bySource[src] = append(bySource[src], d)
	}

	sources := make([]string, 0, len(bySource))
	for s := range bySource {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	fmt.Fprintf(&b, "All dependencies (%d total):\n", len(deps))
	for _, src := range sources {
		group := bySource[src]
		// sort deps within each source group
		sort.Slice(group, func(i, j int) bool {
			return group[i].Name < group[j].Name
		})
		fmt.Fprintf(&b, "\n[%s — %d]\n", src, len(group))
		for _, d := range group {
			ver := "(unpinned)"
			if d.Version != nil {
				ver = *d.Version
			}
			fmt.Fprintf(&b, "  %-40s  %-12s  %d file(s)\n", d.Name, ver, d.ImportCount)
		}
	}
	return b.String()
}

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
