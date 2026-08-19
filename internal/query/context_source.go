//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

package query

import (
	"eulix/internal/query/strip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// fileCache caches raw file reads within a single hydrateSourceCode call so
// that multiple chunks from the same file don't each trigger a separate
// os.ReadFile. The cache lives only for the duration of one call and is
// safe for concurrent use during the prefetch phase (see prefetchFiles).
type fileCache struct {
	mu    sync.Mutex
	files map[string][]string // path → lines (nil means "not found / error")
}

func newFileCache() *fileCache {
	return &fileCache{files: make(map[string][]string)}
}

func (fc *fileCache) get(path string, debugLog *DebugLogger) []string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if lines, ok := fc.files[path]; ok {
		return lines
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		debugLog.Log("Source file not found: %s", path)
		fc.files[path] = nil
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		debugLog.Log("Error reading %s: %v", path, err)
		fc.files[path] = nil
		return nil
	}
	lines := strings.Split(string(data), "\n")
	fc.files[path] = lines
	return lines
}

const prefetchMaxConcurrency = 8

// prefetchFiles reads every distinct file referenced by chunks into the
// cache concurrently. The budget-accounting loop in hydrateSourceCode stays
// sequential (token spend is order-dependent) but no longer pays sequential
// I/O latency per file, since reads are already cached by the time it runs.
func (cb *ContextBuilder) prefetchFiles(chunks []Chunk, cache *fileCache) {
	seen := make(map[string]struct{}, len(chunks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, prefetchMaxConcurrency)

	for _, chunk := range chunks {
		sourceFile := filepath.Join(cb.sourceRoot, chunk.File)
		if _, ok := seen[sourceFile]; ok {
			continue
		}
		seen[sourceFile] = struct{}{}

		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer wg.Done()
			defer func() { <-sem }()
			cache.get(path, cb.debugLog)
		}(sourceFile)
	}

	wg.Wait()
}

func (cb *ContextBuilder) hydrateSourceCode(chunks []Chunk, sourceBudget int, maxLinesDefault int) []Chunk {
	if cb.sourceRoot == "" {
		cb.debugLog.Log("Source hydration skipped: no source root configured")
		return chunks
	}

	cb.debugLog.Log("=== SOURCE HYDRATION START ===")
	// cb.debugLog.Log("Source root: %s", cb.sourceRoot)
	cb.debugLog.Log("Budget: %d tokens | Max lines: %d | Chunks: %d", sourceBudget, maxLinesDefault, len(chunks))

	if sourceBudget <= 0 {
		cb.debugLog.Log("Source hydration skipped: zero budget")
		return chunks
	}

	result := make([]Chunk, len(chunks))
	usedTokens := 0
	successCount := 0
	cache := newFileCache()

	cb.prefetchFiles(chunks, cache)

	for i, chunk := range chunks {
		remaining := sourceBudget - usedTokens
		if remaining <= 0 {
			result[i] = chunk
			continue
		}

		maxLines := maxLinesDefault
		budgetFraction := float64(remaining) / float64(sourceBudget)

		if budgetFraction < 0.25 {
			maxLines = maxLinesDefault / 4
		} else if budgetFraction < 0.5 {
			maxLines = maxLinesDefault / 2
		}
		if maxLines < 10 {
			maxLines = 10
		}

		sourceCode, tokens := cb.readSourceLinesFromCache(cache, chunk.File, chunk.StartLine, chunk.EndLine, maxLines)

		if sourceCode == "" {
			cb.debugLog.Log("[DEBUG] No source found for %s:%d-%d", chunk.File, chunk.StartLine, chunk.EndLine)
			result[i] = chunk
			continue
		}

		if tokens > remaining {
			cb.debugLog.Log("Chunk %d (%s:%d-%d): source available but exceeds remaining budget (%d > %d)", i, chunk.File, chunk.StartLine, chunk.EndLine, tokens, remaining)
			result[i] = chunk
			continue
		}

		lang := detectLanguage(chunk.File)
		chunk.Content = fmt.Sprintf("```%s\n%s\n```", lang, sourceCode)
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

func (cb *ContextBuilder) readSourceLinesFromCache(
	cache *fileCache,
	filePath string,
	startLine, endLine, maxLines int,
) (string, int) {
	sourceFile := filepath.Join(cb.sourceRoot, filePath)
	lines := cache.get(sourceFile, cb.debugLog)
	if lines == nil {
		return "", 0
	}

	return extractLines(lines, filePath, startLine, endLine, maxLines, cb.debugLog)
}

func extractLines(lines []string, filePath string, startLine, endLine, maxLines int, debugLog *DebugLogger) (string, int) {
	if startLine < 1 || startLine > len(lines) {
		debugLog.Log("Invalid line range for %s: file has %d lines, requested %d-%d",
			filePath, len(lines), startLine, endLine)
		return "", 0
	}

	actualEnd := endLine
	if endLine-startLine+1 > maxLines {
		actualEnd = startLine + maxLines - 1
	}
	end := actualEnd
	if end > len(lines) {
		end = len(lines)
	}

	lang := detectLanguage(filePath)
	relevantLines := lines[startLine-1 : end]
	cleaned := strip.StripCommentsAndDocs(relevantLines, lang)
	code := strings.Join(cleaned, "\n")
	tokens := len(code) / 4

	return code, tokens
}

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
