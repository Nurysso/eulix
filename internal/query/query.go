package query

import (
	"fmt"
	"strings"
	"time"

	"eulix/internal/config"
	"eulix/internal/kb"
	"eulix/internal/llm"
)

// Result represents a query result
type Result struct {
	Answer   string
	Sources  []string
	Type     string        // "simple_lookup", "explanation", "readme", "complex"
	Source   string        // "index", "summary", "kb", "llm"
	Duration time.Duration
}

// Router handles query classification and routing
type Router struct {
	kbLoader *kb.Loader
	cfg      *config.Config
}

// NewRouter creates a new query router
func NewRouter(kbLoader *kb.Loader) *Router {
	return &Router{
		kbLoader: kbLoader,
		cfg:      config.Load(),
	}
}

// Query processes a query and returns a result
func (r *Router) Query(queryText string) (*Result, error) {
	start := time.Now()

	fmt.Printf("[DEBUG] Query: %s\n", queryText)

	// Normalize query
	normalized := strings.TrimSpace(strings.ToLower(queryText))

	// Classify query
	queryType := r.classifyQuery(normalized)

	fmt.Printf("[DEBUG] Query type: %s\n", queryType)

	var result *Result
	var err error

	// Route based on type
	switch queryType {
	case "simple_lookup":
		result, err = r.handleSimpleLookup(queryText, normalized)
	case "explanation":
		result, err = r.handleExplanation(queryText, normalized)
	case "readme":
		result, err = r.handleREADME()
	case "complex":
		result, err = r.handleComplex(queryText, normalized)
	default:
		result, err = r.handleGeneral(queryText, normalized)
	}

	if err != nil {
		fmt.Printf("[DEBUG] Error: %v\n", err)
		return nil, err
	}

	result.Duration = time.Since(start)
	result.Type = queryType

	fmt.Printf("[DEBUG] Result answer length: %d\n", len(result.Answer))

	return result, nil
}

// classifyQuery determines the query type
func (r *Router) classifyQuery(normalized string) string {
	// Simple lookups (where is X, find X)
	if strings.HasPrefix(normalized, "where is") ||
		strings.HasPrefix(normalized, "find ") ||
		strings.HasPrefix(normalized, "show me") ||
		strings.HasPrefix(normalized, "locate ") {
		return "simple_lookup"
	}

	// README generation
	if strings.Contains(normalized, "readme") ||
		strings.Contains(normalized, "generate doc") ||
		strings.Contains(normalized, "document") {
		return "readme"
	}

	// Complex queries
	if strings.Contains(normalized, "refactor") ||
		strings.Contains(normalized, "optimize") ||
		strings.Contains(normalized, "security") ||
		strings.Contains(normalized, "architecture") ||
		strings.Contains(normalized, "improve") {
		return "complex"
	}

	// Explanation queries (requires deeper context)
	if strings.HasPrefix(normalized, "what ") ||
		strings.HasPrefix(normalized, "how ") ||
		strings.HasPrefix(normalized, "explain ") ||
		strings.Contains(normalized, "implement") ||
		strings.Contains(normalized, "work") ||
		strings.Contains(normalized, "does ") {
		return "explanation"
	}

	return "general"
}

// handleSimpleLookup handles simple lookups using index only
func (r *Router) handleSimpleLookup(original, normalized string) (*Result, error) {
	index := r.kbLoader.GetIndex()

	// Extract entity name from query
	keywords := r.extractKeywords(normalized)

	// Try exact match in functions
	for _, keyword := range keywords {
		if loc, ok := index.Functions[keyword]; ok {
			return &Result{
				Answer:  fmt.Sprintf("📍 Found: %s\nLocation: %s:%d\nType: %s", keyword, loc.File, loc.Line, loc.Type),
				Sources: []string{fmt.Sprintf("%s:%d", loc.File, loc.Line)},
				Source:  "index",
			}, nil
		}
	}

	// Try exact match in classes
	for _, keyword := range keywords {
		if loc, ok := index.Classes[keyword]; ok {
			methodList := ""
			if len(loc.Methods) > 0 {
				methodList = fmt.Sprintf("\nMethods: %s", strings.Join(loc.Methods, ", "))
			}
			return &Result{
				Answer:  fmt.Sprintf("📍 Found class: %s\nLocation: %s:%d%s", keyword, loc.File, loc.Line, methodList),
				Sources: []string{fmt.Sprintf("%s:%d", loc.File, loc.Line)},
				Source:  "index",
			}, nil
		}
	}

	// Fuzzy search in KB
	return r.searchKBForEntity(keywords)
}

// handleExplanation handles explanation queries with dynamic context window
func (r *Router) handleExplanation(original, normalized string) (*Result, error) {
	keywords := r.extractKeywords(normalized)

	fmt.Printf("[DEBUG] Keywords: %v\n", keywords)

	// Build dynamic context with 2-level deep traversal
	context, sources, err := r.buildDynamicContext(keywords, 2)
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}

	fmt.Printf("[DEBUG] Context length: %d, Sources: %d\n", len(context), len(sources))

	if context == "" {
		return &Result{
			Answer: " No relevant code found for your query.",
			Source: "kb",
		}, nil
	}

	// Build prompt
	prompt := r.buildPrompt(context, original)

	fmt.Printf("[DEBUG] Prompt length: %d\n", len(prompt))

	// Query LLM based on config
	answer, err := r.queryLLM(prompt)
	if err != nil {
		fmt.Printf("[DEBUG] LLM error: %v\n", err)
		return nil, fmt.Errorf("LLM query failed: %w", err)
	}

	fmt.Printf("[DEBUG] LLM response length: %d\n", len(answer))

	return &Result{
		Answer:  answer,
		Sources: sources,
		Source:  r.cfg.LLM.Provider,
	}, nil
}

// handleREADME generates README from summary
func (r *Router) handleREADME() (*Result, error) {
	summary := r.kbLoader.GetSummary()

	prompt := fmt.Sprintf(`Generate a professional README.md for this project:

Project: %s
Files: %d
Lines of Code: %d
Languages: %s

Structure:
%s

Key Features:
%s

Entry Points:
%s

Generate a comprehensive README with sections:
- Overview
- Features
- Installation
- Usage
- Project Structure
- Contributing

Keep it concise and accurate.`,
		summary.ProjectName,
		summary.TotalFiles,
		summary.TotalLOC,
		strings.Join(summary.Languages, ", "),
		formatCategories(summary.Categories),
		strings.Join(summary.KeyFeatures, "\n"),
		strings.Join(summary.EntryPoints, "\n"),
	)

	answer, err := r.queryLLM(prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate README: %w", err)
	}

	return &Result{
		Answer: answer,
		Source: r.cfg.LLM.Provider,
	}, nil
}

// handleComplex handles complex queries with larger context
func (r *Router) handleComplex(original, normalized string) (*Result, error) {
	keywords := r.extractKeywords(normalized)

	// Build deeper context for complex queries (3 levels)
	context, sources, err := r.buildDynamicContext(keywords, 3)
	if err != nil {
		return nil, err
	}

	if context == "" {
		return &Result{
			Answer: " No relevant code found for your query.",
			Source: "kb",
		}, nil
	}

	prompt := r.buildPrompt(context, original)

	answer, err := r.queryLLM(prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM query failed: %w", err)
	}

	return &Result{
		Answer:  answer,
		Sources: sources,
		Source:  r.cfg.LLM.Provider,
	}, nil
}

// handleGeneral handles general queries
func (r *Router) handleGeneral(original, normalized string) (*Result, error) {
	return r.handleExplanation(original, normalized)
}

// buildDynamicContext builds context with N-level deep traversal
func (r *Router) buildDynamicContext(keywords []string, maxDepth int) (string, []string, error) {
	kbData, err := r.kbLoader.GetKB()
	if err != nil {
		return "", nil, err
	}

	index := r.kbLoader.GetIndex()

	// Track visited entities to avoid duplicates
	visited := make(map[string]bool)
	var context strings.Builder
	var sources []string

	// Find initial matches using index
	initialMatches := r.findInitialMatches(index, keywords)

	fmt.Printf("[DEBUG] Found %d initial matches\n", len(initialMatches))

	// Build context with depth traversal
	for _, match := range initialMatches {
		r.traverseAndBuild(kbData, match, 0, maxDepth, visited, &context, &sources)
	}

	return context.String(), sources, nil
}

// findInitialMatches finds initial entities matching keywords
func (r *Router) findInitialMatches(index *kb.Index, keywords []string) []EntityRef {
	var matches []EntityRef

	for _, keyword := range keywords {
		// Check functions
		if loc, ok := index.Functions[keyword]; ok {
			matches = append(matches, EntityRef{
				Type: "function",
				Name: keyword,
				File: loc.File,
				Line: loc.Line,
			})
		}

		// Check classes
		if loc, ok := index.Classes[keyword]; ok {
			matches = append(matches, EntityRef{
				Type: "class",
				Name: keyword,
				File: loc.File,
				Line: loc.Line,
			})
		}

		// Fuzzy match in functions
		for name, loc := range index.Functions {
			if strings.Contains(strings.ToLower(name), keyword) && !contains(matches, name) {
				matches = append(matches, EntityRef{
					Type: "function",
					Name: name,
					File: loc.File,
					Line: loc.Line,
				})
			}
		}

		// Fuzzy match in classes
		for name, loc := range index.Classes {
			if strings.Contains(strings.ToLower(name), keyword) && !contains(matches, name) {
				matches = append(matches, EntityRef{
					Type: "class",
					Name: name,
					File: loc.File,
					Line: loc.Line,
				})
			}
		}
	}

	return matches
}

// traverseAndBuild traverses relationships and builds context
func (r *Router) traverseAndBuild(
	kbData *kb.KnowledgeBase,
	ref EntityRef,
	currentDepth, maxDepth int,
	visited map[string]bool,
	context *strings.Builder,
	sources *[]string,
) {
	if currentDepth >= maxDepth {
		return
	}

	entityKey := fmt.Sprintf("%s:%s", ref.Type, ref.Name)
	if visited[entityKey] {
		return
	}
	visited[entityKey] = true

	fileInfo, ok := kbData.Structure[ref.File]
	if !ok {
		return
	}

	// Add current entity to context
	if ref.Type == "function" {
		for _, fn := range fileInfo.Functions {
			if fn.Name == ref.Name {
				r.addFunctionToContext(fn, ref.File, context, sources, currentDepth)

				// Traverse to called functions (depth + 1)
				for _, calledFn := range fn.Calls {
					calledRef := r.findEntityInKB(kbData, calledFn)
					if calledRef != nil {
						r.traverseAndBuild(kbData, *calledRef, currentDepth+1, maxDepth, visited, context, sources)
					}
				}

				// Traverse to callers (depth + 1)
				for _, caller := range fn.CalledBy {
					callerRef := r.findEntityInKB(kbData, caller)
					if callerRef != nil {
						r.traverseAndBuild(kbData, *callerRef, currentDepth+1, maxDepth, visited, context, sources)
					}
				}
				break
			}
		}
	} else if ref.Type == "class" {
		for _, cls := range fileInfo.Classes {
			if cls.Name == ref.Name {
				r.addClassToContext(cls, ref.File, fileInfo, context, sources, currentDepth)

				// Traverse to methods (depth + 1)
				for _, methodName := range cls.Methods {
					methodRef := r.findEntityInKB(kbData, methodName)
					if methodRef != nil {
						r.traverseAndBuild(kbData, *methodRef, currentDepth+1, maxDepth, visited, context, sources)
					}
				}
				break
			}
		}
	}
}

// addFunctionToContext adds function details to context
func (r *Router) addFunctionToContext(fn kb.FunctionInfo, file string, context *strings.Builder, sources *[]string, depth int) {
	indent := strings.Repeat("  ", depth)
	context.WriteString(fmt.Sprintf("%s[DEPTH %d] File: %s\n", indent, depth, file))
	context.WriteString(fmt.Sprintf("%sFunction: %s (line %d-%d)\n", indent, fn.Name, fn.LineStart, fn.LineEnd))

	if fn.Signature != "" {
		context.WriteString(fmt.Sprintf("%sSignature: %s\n", indent, fn.Signature))
	}

	if fn.Docstring != "" {
		context.WriteString(fmt.Sprintf("%sDescription: %s\n", indent, fn.Docstring))
	}

	if len(fn.Params) > 0 {
		params := make([]string, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = fmt.Sprintf("%s: %s", p.Name, p.Type)
		}
		context.WriteString(fmt.Sprintf("%sParameters: %s\n", indent, strings.Join(params, ", ")))
	}

	if fn.ReturnType != "" {
		context.WriteString(fmt.Sprintf("%sReturns: %s\n", indent, fn.ReturnType))
	}

	if len(fn.Calls) > 0 {
		context.WriteString(fmt.Sprintf("%sCalls: %s\n", indent, strings.Join(fn.Calls, ", ")))
	}

	if len(fn.CalledBy) > 0 {
		context.WriteString(fmt.Sprintf("%sCalled by: %s\n", indent, strings.Join(fn.CalledBy, ", ")))
	}

	context.WriteString("\n")
	*sources = append(*sources, fmt.Sprintf("%s:%d", file, fn.LineStart))
}

// addClassToContext adds class details to context
func (r *Router) addClassToContext(cls kb.ClassInfo, file string, fileInfo *kb.FileInfo, context *strings.Builder, sources *[]string, depth int) {
	indent := strings.Repeat("  ", depth)
	context.WriteString(fmt.Sprintf("%s[DEPTH %d] File: %s\n", indent, depth, file))
	context.WriteString(fmt.Sprintf("%sClass: %s (line %d-%d)\n", indent, cls.Name, cls.LineStart, cls.LineEnd))

	if cls.Docstring != "" {
		context.WriteString(fmt.Sprintf("%sDescription: %s\n", indent, cls.Docstring))
	}

	if len(cls.BaseClasses) > 0 {
		context.WriteString(fmt.Sprintf("%sInherits: %s\n", indent, strings.Join(cls.BaseClasses, ", ")))
	}

	if len(cls.Methods) > 0 {
		context.WriteString(fmt.Sprintf("%sMethods: %s\n", indent, strings.Join(cls.Methods, ", ")))
	}

	context.WriteString("\n")
	*sources = append(*sources, fmt.Sprintf("%s:%d", file, cls.LineStart))
}

// findEntityInKB finds an entity reference in the knowledge base
func (r *Router) findEntityInKB(kbData *kb.KnowledgeBase, entityName string) *EntityRef {
	for filepath, fileInfo := range kbData.Structure {
		// Search functions
		for _, fn := range fileInfo.Functions {
			if fn.Name == entityName {
				return &EntityRef{
					Type: "function",
					Name: entityName,
					File: filepath,
					Line: fn.LineStart,
				}
			}
		}

		// Search classes
		for _, cls := range fileInfo.Classes {
			if cls.Name == entityName {
				return &EntityRef{
					Type: "class",
					Name: entityName,
					File: filepath,
					Line: cls.LineStart,
				}
			}
		}
	}
	return nil
}

// queryLLM queries the LLM based on config
func (r *Router) queryLLM(prompt string) (string, error) {
	ollamaURL := "http://localhost:11434"
	if r.cfg.LLM.Provider == "openai" {
		// return llm.QueryOpenAI(prompt)
		return llm.QueryOllama(ollamaURL, r.cfg.LLM.Model, prompt)
	}


	// Default to Ollama
	return llm.QueryOllama(ollamaURL, r.cfg.LLM.Model, prompt)
}

// extractKeywords extracts meaningful keywords from query
func (r *Router) extractKeywords(normalized string) []string {
	stopWords := map[string]bool{
		"the": true, "is": true, "where": true, "what": true,
		"how": true, "does": true, "do": true, "a": true,
		"an": true, "in": true, "on": true, "at": true,
		"to": true, "for": true, "of": true, "and": true,
		"work": true, "works": true, "implement": true, "implemented": true,
	}

	words := strings.Fields(normalized)
	var keywords []string

	for _, word := range words {
		word = strings.Trim(word, "?.,!;:")
		if !stopWords[word] && len(word) > 2 {
			keywords = append(keywords, word)
		}
	}

	return keywords
}

// searchKBForEntity searches the full KB for an entity
func (r *Router) searchKBForEntity(keywords []string) (*Result, error) {
	kbData, err := r.kbLoader.GetKB()
	if err != nil {
		return nil, err
	}

	var matches []SearchMatch

	// Search through all files
	for filepath, fileInfo := range kbData.Structure {
		// Search functions
		for _, fn := range fileInfo.Functions {
			score := r.matchScore(fn.Name, fn.Docstring, keywords)
			if score > 0 {
				matches = append(matches, SearchMatch{
					Type:      "function",
					Name:      fn.Name,
					File:      filepath,
					Line:      fn.LineStart,
					Docstring: fn.Docstring,
					Score:     score,
				})
			}
		}

		// Search classes
		for _, cls := range fileInfo.Classes {
			score := r.matchScore(cls.Name, cls.Docstring, keywords)
			if score > 0 {
				matches = append(matches, SearchMatch{
					Type:      "class",
					Name:      cls.Name,
					File:      filepath,
					Line:      cls.LineStart,
					Docstring: cls.Docstring,
					Score:     score,
				})
			}
		}
	}

	if len(matches) == 0 {
		return &Result{
			Answer: " No relevant code found for your query.",
			Source: "kb",
		}, nil
	}

	// Sort by score
	sortMatches(matches)

	// Format results
	var answer strings.Builder
	answer.WriteString(fmt.Sprintf("Found %d matches:\n\n", len(matches)))

	limit := 5
	if len(matches) < limit {
		limit = len(matches)
	}

	var sources []string
	for i := 0; i < limit; i++ {
		match := matches[i]
		answer.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, match.Type, match.Name))
		answer.WriteString(fmt.Sprintf("   Location: %s:%d\n", match.File, match.Line))
		if match.Docstring != "" {
			answer.WriteString(fmt.Sprintf("   %s\n", truncate(match.Docstring, 80)))
		}
		answer.WriteString("\n")
		sources = append(sources, fmt.Sprintf("%s:%d", match.File, match.Line))
	}

	if len(matches) > limit {
		answer.WriteString(fmt.Sprintf("... and %d more matches\n", len(matches)-limit))
	}

	return &Result{
		Answer:  answer.String(),
		Sources: sources,
		Source:  "kb",
	}, nil
}

// buildPrompt builds a prompt for the LLM
func (r *Router) buildPrompt(context, query string) string {
	return fmt.Sprintf(`You are a helpful coding assistant analyzing a codebase.

Context from codebase (with relationship depth):
%s

User question: %s

Provide a concise, accurate answer based on the context above. Include file paths and line numbers when relevant. If the context doesn't contain enough information to answer fully, say so.`, context, query)
}

// matchScore calculates relevance score for a match
func (r *Router) matchScore(name, docstring string, keywords []string) float64 {
	score := 0.0
	nameLower := strings.ToLower(name)
	docLower := strings.ToLower(docstring)

	for _, keyword := range keywords {
		// Exact match in name (highest weight)
		if nameLower == keyword {
			score += 10.0
		} else if strings.Contains(nameLower, keyword) {
			score += 5.0
		}

		// Match in docstring (lower weight)
		if strings.Contains(docLower, keyword) {
			score += 1.0
		}
	}

	return score
}

// Helper types

type EntityRef struct {
	Type string
	Name string
	File string
	Line int
}

type SearchMatch struct {
	Type      string
	Name      string
	File      string
	Line      int
	Signature string
	Docstring string
	Calls     []string
	CalledBy  []string
	Score     float64
}

func sortMatches(matches []SearchMatch) {
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].Score > matches[i].Score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func formatCategories(categories map[string][]string) string {
	var lines []string
	for category, files := range categories {
		lines = append(lines, fmt.Sprintf("- %s: %d files", category, len(files)))
	}
	return strings.Join(lines, "\n")
}

func contains(refs []EntityRef, name string) bool {
	for _, ref := range refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}
