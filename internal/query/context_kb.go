//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides the context window builder and query routing for Eulix's
RAG (Retrieval-Augmented Generation) system.

This file implements exact symbol matching and KB-based chunk construction.
It bridges the knowledge base (kb.json) and the chunk universe, enabling:

 1. Exact symbol lookup: Extract potential identifiers from queries and
    locate matching functions, classes, and methods in the KB.
 2. Intent-driven expansion: For CalleeIntent queries, include callees of
    matched functions with lower scores to provide calling context.
 3. Chunk materialization: Build Chunk objects from KB structures (KBFunction,
    KBClass) with metadata, signatures, and docstrings.
 4. Content hydration: Fill empty chunk content on-demand in lazy-loading mode,
    avoiding bulk materialization until needed.

Chunk IDs are hierarchical:
  - Function: "path/to/file.go:123-456" (startLine-endLine)
  - Class: "path/to/file.go:100-200"
  - Method: "path/to/file.go:150-180" (stored as function, parent class tracked separately)

Lookup strategy:
 1. Query extraction via extractPotentialSymbols (case-insensitive matching)
 2. Fast path: kb_index.json FunctionsByName lookup (if available)
 3. Full path: Scan kbData.Structure for exact matches
 4. Intent filtering: Expand to callees if intent == IntentCallees

Content materialization:
  - eager: All chunk content is inlined at load time (small corpora)
  - lazy: Chunks begin with empty Content; hydrateContent fills on demand
    when selected for final context window (large corpora)

See:
  - context_loader.go: KB loading and chunk population from kb.json
  - context_search.go: Integration with multi-strategy search pipeline
  - context_intent.go: Intent detection (IntentCallees, etc.)
  - boilerplate.go: Symbol filtering during inverted index construction
*/
package query

import (
	"fmt"
	"strings"

	"eulix/internal/types"
)

// kbExactLookup performs symbol-based retrieval from the knowledge base.
// Extracts potential identifiers from the query and searches for exact matches
// in functions, classes, and methods. Returns matched chunks with match type
// and origin details for transparency.
//
// Scoring tiers (higher = more relevant):
//   - 120.0: KB index direct hit
//   - 118.0: Exact class match
//   - 115.0: Exact function match
//   - 113.0: Exact method match
//   - 110.0: Callees of matched function (IntentCallees only)
//   - 95.0: Parent class of matched method
//
// Deduplicates results by chunk ID to avoid duplicate scoring.
// Gracefully skips if KB is unavailable.
//
// See:
//   - extractPotentialSymbols: Query tokenization and candidate extraction
//   - context_intent.go: IntentCallees and other intent types
//   - expandFromKBFunction: Callee expansion for IntentCallees queries
func (cb *ContextBuilder) kbExactLookup(query string, intent QueryIntent) []ScoredChunk {
	if !cb.hasKB || cb.kbData == nil {
		return nil
	}
	potentialSymbols := extractPotentialSymbols(query)
	scored := make([]ScoredChunk, 0)
	matchedIDs := make(map[string]bool)

	for _, symbol := range potentialSymbols {
		symLow := strings.ToLower(symbol)

		if cb.kbIdx != nil {
			if locs, ok := cb.kbIdx.FunctionsByName[symbol]; ok {
				for _, loc := range locs {
					if !matchedIDs[loc] {
						matchedIDs[loc] = true
						if chunk := cb.findChunkForLocation(loc); chunk != nil {
							scored = append(scored, ScoredChunk{
								Chunk: *chunk, Score: 120.0,
								MatchType:    "kb_exact",
								MatchDetails: "KB index: " + symbol,
							})
						}
					}
				}
			}
		}

		for filePath, fs := range cb.kbData.Structure {
			for _, fn := range fs.Functions {
				if strings.ToLower(fn.Name) == symLow {
					id := fmt.Sprintf("%s:%d-%d", filePath, fn.LineStart, fn.LineEnd)
					if !matchedIDs[id] {
						matchedIDs[id] = true
						chunk := cb.buildChunkFromKBFunction(fn, filePath)
						scored = append(scored, ScoredChunk{
							Chunk: chunk, Score: 115.0,
							MatchType:    "kb_function",
							MatchDetails: "Function: " + fn.Name,
						})
						if intent.Type == IntentCallees {
							scored = append(scored, cb.expandFromKBFunction(fn, filePath, 110.0)...)
						}
					}
				}
			}
			for _, class := range fs.Classes {
				if strings.ToLower(class.Name) == symLow {
					id := fmt.Sprintf("%s:%d-%d", filePath, class.LineStart, class.LineEnd)
					if !matchedIDs[id] {
						matchedIDs[id] = true
						scored = append(scored, ScoredChunk{
							Chunk: cb.buildChunkFromKBClass(class, filePath), Score: 118.0,
							MatchType: "kb_class", MatchDetails: "Class: " + class.Name,
						})
					}
				}
				for _, method := range class.Methods {
					if strings.ToLower(method.Name) == symLow {
						id := fmt.Sprintf("%s:%d-%d", filePath, method.LineStart, method.LineEnd)
						if !matchedIDs[id] {
							matchedIDs[id] = true
							scored = append(scored, ScoredChunk{
								Chunk: cb.buildChunkFromKBFunction(method, filePath), Score: 113.0,
								MatchType: "kb_method", MatchDetails: "Method: " + class.Name + "." + method.Name,
							})
							scored = append(scored, ScoredChunk{
								Chunk: cb.buildChunkFromKBClass(class, filePath), Score: 95.0,
								Distance: 1, MatchType: "kb_parent_class",
								MatchDetails: "Parent class of " + method.Name,
							})
						}
					}
				}
			}
		}
	}
	return scored
}

// findChunkForLocation locates a chunk in cb.chunks that covers a given
// location string in the format "file:line" or "file:startLine-endLine".
// Returns nil if no matching chunk is found.
//
// Location format:
//   - Single line: "path/to/file.go:42"
//   - Range: "path/to/file.go:10-50"
//
// See:
//   - kbExactLookup: Uses this to materialize results from kb_index.json
//   - Chunk: StartLine, EndLine fields used for overlap detection
func (cb *ContextBuilder) findChunkForLocation(loc string) *Chunk {
	parts := strings.Split(loc, ":")
	if len(parts) < 2 {
		return nil
	}
	file := parts[0]
	if strings.Contains(parts[1], "-") {
		rp := strings.Split(parts[1], "-")
		if len(rp) == 2 {
			var s, e int
			fmt.Sscanf(rp[0], "%d", &s)
			fmt.Sscanf(rp[1], "%d", &e)
			for i := range cb.chunks {
				if cb.chunks[i].File == file && cb.chunks[i].StartLine <= e && cb.chunks[i].EndLine >= s {
					return &cb.chunks[i]
				}
			}
		}
	} else {
		var line int
		fmt.Sscanf(parts[1], "%d", &line)
		for i := range cb.chunks {
			if cb.chunks[i].File == file && cb.chunks[i].StartLine <= line && cb.chunks[i].EndLine >= line {
				return &cb.chunks[i]
			}
		}
	}
	return nil
}

// Extractors
func extractFnParts(fn types.KBFunction) fnParts {
	calls := make([]callPart, len(fn.Calls))
	for i, c := range fn.Calls {
		calls[i] = callPart{callee: c.Callee, line: c.Line}
	}
	return fnParts{
		name:      fn.Name,
		signature: fn.Signature,
		docstring: fn.Docstring,
		lineStart: fn.LineStart,
		lineEnd:   fn.LineEnd,
		calls:     calls,
	}
}

func extractClassParts(cls types.KBClass) classParts {
	methods := make([]methodPart, len(cls.Methods))
	for i, m := range cls.Methods {
		methods[i] = methodPart{name: m.Name, lineStart: m.LineStart, lineEnd: m.LineEnd}
	}
	return classParts{
		name:      cls.Name,
		docstring: cls.Docstring,
		lineStart: cls.LineStart,
		lineEnd:   cls.LineEnd,
		methods:   methods,
	}
}

// Content builders (mirror buildChunkFromKBFunction/Class exactly)

func (cb *ContextBuilder) buildChunkFromParts(p fnParts, filePath string) Chunk {
	content := fmt.Sprintf("Function: %s\nSignature: %s\nLines: %d-%d\n",
		p.name, p.signature, p.lineStart, p.lineEnd)
	if p.docstring != "" {
		content += "Documentation: " + p.docstring + "\n"
	}
	if len(p.calls) > 0 {
		content += "Calls:\n"
		for _, c := range p.calls {
			content += fmt.Sprintf("  - %s (line %d)\n", c.callee, c.line)
		}
	}
	return Chunk{
		ID:        fmt.Sprintf("%s:%d-%d", filePath, p.lineStart, p.lineEnd),
		ChunkType: "function", File: filePath,
		StartLine: p.lineStart, EndLine: p.lineEnd,
		Content: content, Tokens: len(content) / 4,
		Symbols: []string{p.name}, Name: p.name, Importance: 0.9,
	}
}

func (cb *ContextBuilder) buildClassFromParts(p classParts, filePath string) Chunk {
	content := fmt.Sprintf("Class: %s\nLines: %d-%d\n", p.name, p.lineStart, p.lineEnd)
	if p.docstring != "" {
		content += "Documentation: " + p.docstring + "\n"
	}
	if len(p.methods) > 0 {
		content += "Methods:\n"
		for _, m := range p.methods {
			content += fmt.Sprintf("  - %s (lines %d-%d)\n", m.name, m.lineStart, m.lineEnd)
		}
	}
	syms := make([]string, 0, len(p.methods)+1)
	syms = append(syms, p.name)
	for _, m := range p.methods {
		syms = append(syms, m.name)
	}
	return Chunk{
		ID:        fmt.Sprintf("%s:%d-%d", filePath, p.lineStart, p.lineEnd),
		ChunkType: "class", File: filePath,
		StartLine: p.lineStart, EndLine: p.lineEnd,
		Content: content, Tokens: len(content) / 4,
		Symbols: syms, Name: p.name, Importance: 0.95,
	}
}

// addChunksFromFile

func (cb *ContextBuilder) addChunksFromFile(filePath string, fs *types.FileData) {
	for _, fn := range fs.Functions {
		c := cb.buildChunkFromKBFunction(fn, filePath)
		if cb.lazyContent {
			p := extractFnParts(fn)
			fp := filePath
			if cb.hydrateIdx[filePath] == nil {
				cb.hydrateIdx[filePath] = make(map[[2]int]func() string)
			}
			cb.hydrateIdx[filePath][[2]int{c.StartLine, c.EndLine}] = func() string {
				return cb.buildChunkFromParts(p, fp).Content
			}
			c.Content = ""
		}
		cb.chunks = append(cb.chunks, c)
	}

	for _, cls := range fs.Classes {
		cc := cb.buildChunkFromKBClass(cls, filePath)
		if cb.lazyContent {
			p := extractClassParts(cls)
			fp := filePath
			if cb.hydrateIdx[filePath] == nil {
				cb.hydrateIdx[filePath] = make(map[[2]int]func() string)
			}
			cb.hydrateIdx[filePath][[2]int{cc.StartLine, cc.EndLine}] = func() string {
				return cb.buildClassFromParts(p, fp).Content
			}
			cc.Content = ""
		}
		cb.chunks = append(cb.chunks, cc)

		for _, m := range cls.Methods {
			mc := cb.buildChunkFromKBFunction(m, filePath)
			if cb.lazyContent {
				p := extractFnParts(m)
				fp := filePath
				if cb.hydrateIdx[filePath] == nil {
					cb.hydrateIdx[filePath] = make(map[[2]int]func() string)
				}
				cb.hydrateIdx[filePath][[2]int{mc.StartLine, mc.EndLine}] = func() string {
					return cb.buildChunkFromParts(p, fp).Content
				}
				mc.Content = ""
			}
			cb.chunks = append(cb.chunks, mc)
		}
	}
	// when lazyContent=false the hydrateIdx entry for this file is
	// intentionally left absent loadChunks will nil the whole map.
}

// buildChunkFromKBFunction constructs a Chunk from a KBFunction (function or method).
// Materializes signature, docstring, and call relationships into a human-readable
// chunk with metadata for retrieval scoring.
//
// Chunk fields set:
//   - ID: "file:startLine-endLine" for deduplication
//   - ChunkType: "function"
//   - Content: Signature, docstring, and calls list (formatted text)
//   - Symbols: [function_name]
//   - Importance: 0.9 (high signal for exact matches)
//   - Tokens: Estimated as content length / 4
//
// See:
//   - buildChunkFromKBClass: Similar construction for class chunks
//   - hydrateOne: Called in lazy mode to backfill Content
//   - context_loader.go: Chunk population from kb.json
func (cb *ContextBuilder) buildChunkFromKBFunction(fn types.KBFunction, filePath string) Chunk {
	content := fmt.Sprintf("Function: %s\nSignature: %s\nLines: %d-%d\n",
		fn.Name, fn.Signature, fn.LineStart, fn.LineEnd)
	if fn.Docstring != "" {
		content += "Documentation: " + fn.Docstring + "\n"
	}
	if len(fn.Calls) > 0 {
		content += "Calls:\n"
		for _, c := range fn.Calls {
			content += fmt.Sprintf("  - %s (line %d)\n", c.Callee, c.Line)
		}
	}
	return Chunk{
		ID:        fmt.Sprintf("%s:%d-%d", filePath, fn.LineStart, fn.LineEnd),
		ChunkType: "function", File: filePath,
		StartLine: fn.LineStart, EndLine: fn.LineEnd,
		Content: content, Tokens: len(content) / 4,
		Symbols: []string{fn.Name}, Name: fn.Name, Importance: 0.9,
	}
}

// buildChunkFromKBClass constructs a Chunk from a KBClass.
// Materializes class definition with docstring and methods list.
// Sets Importance to 0.95 (slightly higher than functions due to structural significance).
//
// Chunk fields set:
//   - ID: "file:startLine-endLine" for deduplication
//   - ChunkType: "class"
//   - Content: Class name, docstring, and methods list (formatted text)
//   - Symbols: [class_name, method1_name, method2_name, ...] (all methods included)
//   - Importance: 0.95
//   - Tokens: Estimated as content length / 4
//
// See:
//   - buildChunkFromKBFunction: Similar construction for function chunks
//   - hydrateOne: Called in lazy mode to backfill Content
//   - context_loader.go: Chunk population from kb.json
func (cb *ContextBuilder) buildChunkFromKBClass(class types.KBClass, filePath string) Chunk {
	content := fmt.Sprintf("Class: %s\nLines: %d-%d\n", class.Name, class.LineStart, class.LineEnd)
	if class.Docstring != "" {
		content += "Documentation: " + class.Docstring + "\n"
	}
	if len(class.Methods) > 0 {
		content += "Methods:\n"
		for _, m := range class.Methods {
			content += fmt.Sprintf("  - %s (lines %d-%d)\n", m.Name, m.LineStart, m.LineEnd)
		}
	}
	syms := []string{class.Name}
	for _, m := range class.Methods {
		syms = append(syms, m.Name)
	}
	return Chunk{
		ID:        fmt.Sprintf("%s:%d-%d", filePath, class.LineStart, class.LineEnd),
		ChunkType: "class", File: filePath,
		StartLine: class.LineStart, EndLine: class.LineEnd,
		Content: content, Tokens: len(content) / 4,
		Symbols: syms, Name: class.Name, Importance: 0.95,
	}
}

// hydrateContent fills Chunk.Content for selected chunks when in lazy-loading mode.
// Reads directly from kbData without requiring a separate content file.
// Skips chunks that already have content (materialized during eager mode or earlier).
//
// Call this just before final context assembly to backfill content for the
// selected chunks that were deferred during lazy loading.
//
// See:
//   - context_loader.go: Lazy-loading decision (cb.lazyContent)
//   - hydrateOne: Lower-level helper that retrieves a single chunk's content
//   - context_search.go: Call site where final chunks are assembled
func (cb *ContextBuilder) hydrateContent(chunks []Chunk) {
	if cb.kbData == nil {
		return
	}
	for i := range chunks {
		if chunks[i].Content != "" {
			continue
		}
		chunks[i].Content = cb.hydrateOne(chunks[i])
	}
}

// hydrateOne returns the Content for a chunk that was lazily stored.
// Uses the pre-built hydrateIdx for O(1) lookup instead of linear scan.
// Used internally by hydrateContent and during call-site scanning in lazy mode.
//
// Lookup strategy:
// 1. Find file in kbData.Structure
// 2. Match startLine and endLine against functions at file level
// 3. Match against classes and their methods
// 4. Returns empty string if no match found (chunk not in KB)
//
// See:
//   - hydrateContent: Batch hydration wrapper
//   - buildCallSiteIndex: Call-site discovery in lazy mode
//   - context_loader.go: lazyContent threshold decision

func (cb *ContextBuilder) hydrateOne(c Chunk) string {
	if cb.hydrateIdx == nil {
		return ""
	}
	if fileIdx, ok := cb.hydrateIdx[c.File]; ok {
		if builder, ok := fileIdx[[2]int{c.StartLine, c.EndLine}]; ok {
			return builder()
		}
	}
	return ""
}
