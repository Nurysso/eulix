package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sort"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/llm"
	"eulix/internal/types"
)



func (r *Router) SetCurrentChecksum(checksum string) {
	r.currentChecksum = checksum
}

func QueryTrafficController(eulixDir string, cfg *config.Config, llmClient *llm.Client, cacheManager *cache.Manager) (*Router, error) {
	kbIndex, err := loadKBIndex(eulixDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load KB index: %w", err)
	}

	callGraph, err := loadCallGraph(eulixDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load call graph: %w", err)
	}

	kbIndexPath := filepath.Join(eulixDir, "kb_index.json")
	classifier, err := QuerySheriff(kbIndexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create classifier: %w", err)
	}

	return &Router{
		eulixDir:       eulixDir,
		config:         cfg,
		classifier:     classifier,
		llmClient:      llmClient,
		cache:          cacheManager,
		contextBuilder: nil,
		kbIndex:        kbIndex,
		callGraph:      callGraph,
	}, nil
}

func loadKBIndex(eulixDir string) (*KBIndex, error) {
	indexPath := filepath.Join(eulixDir, "kb_index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}

	var index KBIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}

	return &index, nil
}

func loadCallGraph(eulixDir string) (*CallGraph, error) {
	graphPath := filepath.Join(eulixDir, "kb_call_graph.json")
	data, err := os.ReadFile(graphPath)
	if err != nil {
		return nil, err
	}

	var graph CallGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, err
	}

	return &graph, nil
}

func (r *Router) ensureContextBuilder() error {
	if r.contextBuilder != nil {
		return nil
	}

	contextBuilder, err := ContextWindowCreator(r.eulixDir, r.config, r.llmClient)
	if err != nil {
		return fmt.Errorf("failed to initialize context builder: %w", err)
	}

	r.contextBuilder = contextBuilder
	return nil
}

func (r *Router) Query(query string) (string, error) {
	// Check cache first
	if r.cache != nil && r.currentChecksum != "" {
		cached, found, err := r.cache.Get(query, r.currentChecksum)
		if err == nil && found {
			return cached, nil
		}
	}

	// Classify query
	classification := r.classifier.Classify(query)

	var response string
	var err error

	// Route to appropriate handler
	switch classification.Type {
	case QueryTypeLocation:
		response, err = r.handleLocation(query, classification)
	case QueryTypeUsage:
		response, err = r.handleUsage(query, classification)
	case QueryTypeUnderstanding:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	case QueryTypeImplementation:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleImplementation(query, classification)
	case QueryTypeArchitecture:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleArchitecture(query, classification)
	case QueryTypeDebug:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDebug(query, classification)
	case QueryTypeComparison:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleComparison(query, classification)
	case QueryTypeDependency:
		response, err = r.handleDependency(query, classification)
	case QueryTypeRefactoring:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleRefactoring(query, classification)
	case QueryTypePerformance:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handlePerformance(query, classification)
	case QueryTypeDataFlow:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDataFlow(query, classification)
	case QueryTypeSecurity:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleSecurity(query, classification)
	case QueryTypeDocumentation:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDocumentation(query, classification)
	case QueryTypeExample:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleExample(query, classification)
	case QueryTypeTesting:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleTesting(query, classification)
	default:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	}

	if err != nil {
		return "", err
	}

	// Cache the response with current checksum
	if r.cache != nil && r.currentChecksum != "" {
		if err := r.cache.Set(query, response, r.currentChecksum); err != nil {
			// Log error but don't fail the query
			// TODO add failed logger
		}
	}

	return response, nil
}

func (r *Router) handleLocation(query string, class *Classification) (string, error) {
	var entity string
	if len(class.Symbols) > 0 {
		entity = class.Symbols[0]
	} else {
		entity = extractEntityName(query)
	}

	if entity == "" {
		return "Could not identify function or class name in query", nil
	}

	var results []string

	if locations, ok := r.kbIndex.FunctionsByName[entity]; ok {
		results = append(results, fmt.Sprintf("Function '%s' found at:", entity))
		for _, loc := range locations {
			results = append(results, fmt.Sprintf("%s", loc))
		}
	}

	if locations, ok := r.kbIndex.TypesByName[entity]; ok {
		results = append(results, fmt.Sprintf("Type '%s' found at:", entity))
		for _, loc := range locations {
			results = append(results, fmt.Sprintf("%s", loc))
		}
	}

	if len(results) == 0 {
		matches := r.fuzzySearch(entity)
		if len(matches) > 0 {
			results = append(results, fmt.Sprintf("No exact match for '%s'. Did you mean:", entity))
			for _, match := range matches {
				results = append(results, fmt.Sprintf("%s", match))
			}
		} else {
			return fmt.Sprintf("Function or class '%s' not found in the codebase", entity), nil
		}
	}

	return strings.Join(results, "\n"), nil
}

func (r *Router) handleUsage(query string, class *Classification) (string, error) {
	var entity string
	if len(class.Symbols) > 0 {
		entity = class.Symbols[0]
	} else {
		entity = extractEntityName(query)
	}

	if entity == "" {
		return "Could not identify function or class name in query", nil
	}

	var results []string

	if funcNode, ok := r.callGraph.Functions[entity]; ok {
		results = append(results, fmt.Sprintf("Usage Analysis for '%s':", entity))
		results = append(results, fmt.Sprintf("Location: %s", funcNode.Location))
		results = append(results, "")

		if len(funcNode.Calls) > 0 {
			results = append(results, "Calls:")
			for _, callee := range funcNode.Calls {
				results = append(results, fmt.Sprintf("%s", callee))
			}
			results = append(results, "")
		}

		if len(funcNode.CalledBy) > 0 {
			results = append(results, "Called by:")
			for _, caller := range funcNode.CalledBy {
				results = append(results, fmt.Sprintf("%s", caller))
			}
		} else {
			results = append(results, "Not called by any other function (possibly unused or entry point)")
		}
	} else if typeNode, ok := r.callGraph.Types[entity]; ok {
		results = append(results, fmt.Sprintf("Type Analysis for '%s':", entity))
		results = append(results, fmt.Sprintf("Location: %s", typeNode.Location))
		results = append(results, "")

		if len(typeNode.Methods) > 0 {
			results = append(results, "Methods:")
			for _, method := range typeNode.Methods {
				results = append(results, fmt.Sprintf("%s", method))
			}
		}
	} else {
		if callers, ok := r.kbIndex.FunctionsCalling[entity]; ok {
			results = append(results, fmt.Sprintf("Functions calling '%s':", entity))
			for _, caller := range callers {
				results = append(results, fmt.Sprintf("  â† %s", caller))
			}
		} else {
			return fmt.Sprintf("No usage information found for '%s'", entity), nil
		}
	}

	return strings.Join(results, "\n"), nil
}

func (r *Router) handleUnderstanding(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := r.buildAntiHallucinationPrompt(query, class, context)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleImplementation(query string, class *Classification) (string, error) {
	var relevantFiles []string
	for _, symbol := range class.Symbols {
		if locs, ok := r.kbIndex.FunctionsByName[symbol]; ok {
			relevantFiles = append(relevantFiles, locs...)
		}
		if locs, ok := r.kbIndex.TypesByName[symbol]; ok {
			relevantFiles = append(relevantFiles, locs...)
		}
	}

	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Provide implementation guidance for: %s

INSTRUCTIONS:
1. ONLY reference code that is explicitly shown in the provided context
2. If the context doesn't contain the necessary code, say "The relevant code is not in the current context"
3. Focus on implementation details, key functions, and control flow
4. Cite specific line numbers or function names from the context
5. Do NOT invent or assume code that isn't shown

SYMBOLS MENTIONED: %v
RELEVANT FILES: %v

Question: %s`, query, class.Symbols, relevantFiles, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleArchitecture(query string, class *Classification) (string, error) {
	var architectureInfo strings.Builder

	for _, symbol := range class.Symbols {
		if funcNode, ok := r.callGraph.Functions[symbol]; ok {
			architectureInfo.WriteString(fmt.Sprintf("\n%s:\n", symbol))
			architectureInfo.WriteString(fmt.Sprintf("Location: %s\n", funcNode.Location))
			if len(funcNode.Calls) > 0 {
				architectureInfo.WriteString(fmt.Sprintf("Calls: %v\n", funcNode.Calls))
			}
			if len(funcNode.CalledBy) > 0 {
				architectureInfo.WriteString(fmt.Sprintf("Called by: %v\n", funcNode.CalledBy))
			}
		}
	}

	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Explain the architecture for: %s

CALL GRAPH:
%s

INSTRUCTIONS:
1. Base your analysis ONLY on the code shown in the context and call graph above
2. Identify architectural patterns (MVC, layering, dependency injection, etc.)
3. Describe component relationships and data flow
4. Highlight design decisions evident from the code structure
5. If you cannot determine something from the context, explicitly state that
6. Do NOT make assumptions about code you cannot see

Question: %s`, query, architectureInfo.String(), query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleDebug(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Debug analysis for: %s

INSTRUCTIONS:
1. Analyze the code in the context for potential issues related to the query
2. Look for common error patterns: null checks, off-by-one errors, race conditions, etc.
3. Suggest specific fixes with exact function/variable names from the context
4. If the problematic code isn't in the context, say so explicitly
5. Provide step-by-step debugging approach
6. Do NOT speculate about code you cannot see

SYMBOLS: %v

Question: %s`, query, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleComparison(query string, class *Classification) (string, error) {
	if len(class.Symbols) < 2 {
		return "Comparison requires at least two entities. Please specify which functions/types to compare.", nil
	}

	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Compare: %v

INSTRUCTIONS:
1. Compare ONLY based on code visible in the provided context
2. Highlight similarities and differences in:
   - Purpose and functionality
   - Implementation approach
   - Parameters and return types
   - Error handling
   - Performance characteristics (if evident)
3. Use specific examples from the context
4. If either entity is not fully visible in the context, state what information is missing
5. Do NOT make assumptions about unseen code

Question: %s`, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleDependency(query string, class *Classification) (string, error) {
	var entity string
	if len(class.Symbols) > 0 {
		entity = class.Symbols[0]
	} else {
		entity = extractEntityName(query)
	}

	if entity == "" {
		return "Could not identify entity for dependency analysis", nil
	}

	var results []string
	results = append(results, fmt.Sprintf("— Dependency Analysis for '%s':", entity))

	if funcNode, ok := r.callGraph.Functions[entity]; ok {
		// Direct dependencies
		if len(funcNode.Calls) > 0 {
			results = append(results, "\nDirect Dependencies (functions it calls):")
			for _, dep := range funcNode.Calls {
				results = append(results, fmt.Sprintf("  â†’ %s", dep))
			}
		}

		// Dependents (reverse dependencies)
		if len(funcNode.CalledBy) > 0 {
			results = append(results, "\nDependent Functions (functions that call it):")
			for _, caller := range funcNode.CalledBy {
				results = append(results, fmt.Sprintf("  â† %s", caller))
			}
		}

		// Transitive dependencies (2 levels deep)
		transitive := r.findTransitiveDependencies(entity, 2)
		if len(transitive) > 0 {
			results = append(results, "\nTransitive Dependencies:")
			for _, dep := range transitive {
				results = append(results, fmt.Sprintf("  â‡’ %s", dep))
			}
		}
	} else {
		results = append(results, "\nNo dependency information found")
	}

	return strings.Join(results, "\n"), nil
}

func (r *Router) handleRefactoring(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Refactoring suggestions for: %s

INSTRUCTIONS:
1. Analyze the code in the context for refactoring opportunities
2. Look for: code duplication, long functions, deep nesting, unclear naming, tight coupling
3. Suggest specific improvements with reference to actual code in the context
4. Explain the benefits of each suggestion
5. Prioritize suggestions by impact
6. Base suggestions ONLY on visible code - if context is insufficient, say so
7. Do NOT invent problems that don't exist in the shown code

SYMBOLS: %v

Question: %s`, query, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handlePerformance(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Performance analysis for: %s

INSTRUCTIONS:
1. Analyze the code in context for performance characteristics
2. Look for: loops with nested operations, repeated allocations, unnecessary copies, inefficient algorithms
3. Consider: time complexity, space complexity, I/O operations, concurrency
4. Suggest specific optimizations referencing actual code
5. Explain trade-offs (readability vs performance)
6. Base analysis ONLY on visible code
7. Do NOT speculate about performance without seeing the actual implementation

SYMBOLS: %v

Question: %s`, query, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleDataFlow(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	var callGraphInfo string
	if len(class.Symbols) > 0 {
		var builder strings.Builder
		for _, symbol := range class.Symbols {
			if funcNode, ok := r.callGraph.Functions[symbol]; ok {
				builder.WriteString(fmt.Sprintf("\n%s â†’ %v", symbol, funcNode.Calls))
			}
		}
		callGraphInfo = builder.String()
	}

	prompt := fmt.Sprintf(`TASK: Trace data flow for: %s

CALL FLOW:
%s

INSTRUCTIONS:
1. Trace how data flows through the functions in the context
2. Identify transformations, validations, and state changes
3. Note where data enters and exits the system
4. Highlight any data validation or sanitization
5. Use actual variable/parameter names from the context
6. If the full data path isn't visible, clearly state what's missing
7. Do NOT invent data flow that isn't shown

SYMBOLS: %v

Question: %s`, query, callGraphInfo, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleSecurity(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Security analysis for: %s

INSTRUCTIONS:
1. Analyze code in context for security concerns
2. Check for: input validation, injection vulnerabilities, authentication/authorization, sensitive data handling
3. Identify specific security issues with line references
4. Suggest concrete fixes using actual code structure
5. Prioritize by severity
6. Base analysis ONLY on visible code
7. Do NOT flag issues that don't exist in the shown code

SYMBOLS: %v

Question: %s`, query, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleDocumentation(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Document the code for: %s

INSTRUCTIONS:
1. Explain the purpose and behavior based ONLY on code in the context
2. Document parameters, return values, and side effects
3. Note any important edge cases or error handling
4. Use clear, concise language
5. If the full implementation isn't visible, note what documentation is incomplete
6. Do NOT document behavior you cannot verify from the code

SYMBOLS: %v

Question: %s`, query, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleExample(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Provide usage examples for: %s

INSTRUCTIONS:
1. Create examples based on the actual function signatures in the context
2. Show typical use cases with realistic parameters
3. Include error handling examples if relevant
4. Explain what each example demonstrates
5. Use actual types and function names from the context
6. If the function signature isn't fully visible, state what information is needed
7. Do NOT create examples for functions you cannot see

SYMBOLS: %v

Question: %s`, query, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleTesting(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`TASK: Testing guidance for: %s

INSTRUCTIONS:
1. Suggest test cases based on the actual implementation in the context
2. Identify edge cases, error conditions, and boundary values
3. Recommend mocking strategies for dependencies
4. Structure tests logically (arrange-act-assert)
5. Use actual function signatures and types from the context
6. If the implementation isn't fully visible, note what test coverage is uncertain
7. Do NOT suggest tests for behavior you cannot verify

SYMBOLS: %v

Question: %s`, query, class.Symbols, query)

	response, err := r.llmClient.Query(context, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

// Anti-hallucination prompt builder 
func (r *Router) buildAntiHallucinationPrompt(query string, class *Classification, context *types.ContextWindow) string {
	var promptBuilder strings.Builder

	promptBuilder.WriteString("CRITICAL INSTRUCTIONS:\n")
	promptBuilder.WriteString("1. Answer ONLY based on the code provided in the context below\n")
	promptBuilder.WriteString("2. If the answer requires code not in the context, explicitly say: 'This information is not available in the current context'\n")
	promptBuilder.WriteString("3. When referencing code, cite specific function names, file paths, or line indicators\n")
	promptBuilder.WriteString("4. Do NOT invent function names, variables, or code behavior\n")
	promptBuilder.WriteString("5. If you're uncertain, express that uncertainty clearly\n")
	promptBuilder.WriteString("6. Distinguish between what you see in the code vs. what you infer\n\n")

	if len(class.Symbols) > 0 {
		promptBuilder.WriteString(fmt.Sprintf("SYMBOLS MENTIONED: %v\n", class.Symbols))
	}

	if len(class.Keywords) > 0 {
		promptBuilder.WriteString(fmt.Sprintf("KEY TERMS: %v\n", class.Keywords))
	}

	promptBuilder.WriteString(fmt.Sprintf("\nQUERY TYPE: %s\n", class.Type.String()))
	promptBuilder.WriteString(fmt.Sprintf("CONFIDENCE: %.2f\n\n", class.Confidence))
	promptBuilder.WriteString(fmt.Sprintf("USER QUESTION: %s\n", query))

	return promptBuilder.String()
}

// Helper functions
func (r *Router) findTransitiveDependencies(funcName string, depth int) []string {
	if depth <= 0 {
		return []string{}
	}

	visited := make(map[string]bool)
	var result []string

	var traverse func(name string, currentDepth int)
	traverse = func(name string, currentDepth int) {
		if currentDepth > depth || visited[name] {
			return
		}
		visited[name] = true

		if funcNode, ok := r.callGraph.Functions[name]; ok {
			for _, callee := range funcNode.Calls {
				if !visited[callee] && callee != funcName {
					result = append(result, callee)
					traverse(callee, currentDepth+1)
				}
			}
		}
	}

	traverse(funcName, 0)
	return result
}

func (r *Router) Close() error {
	if r.contextBuilder != nil {
		return r.contextBuilder.Close()
	}
	return nil
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

	for _, word := range words {
		wordLower := strings.ToLower(word)
		if stopWords[wordLower] {
			continue
		}
		if isLikelySymbol(word) {
			return word
		}
	}

	for _, word := range words {
		if !stopWords[strings.ToLower(word)] {
			return word
		}
	}

	return ""
}

func isLikelySymbol(word string) bool {
	if len(word) > 1 && word[0] >= 'A' && word[0] <= 'Z' {
		hasLower := false
		for _, ch := range word[1:] {
			if ch >= 'a' && ch <= 'z' {
				hasLower = true
				break
			}
		}
		if hasLower {
			return true
		}
	}

	if strings.Contains(word, "_") {
		return true
	}

	if word == strings.ToUpper(word) && strings.Contains(word, "_") {
		return true
	}

	return false
}

func (r *Router) fuzzySearch(entity string) []string {
	type match struct {
		name  string
		score int
		typ   string
	}

	var matches []match
	entityLower := strings.ToLower(entity)

	// Search in functions
	for funcName := range r.kbIndex.FunctionsByName {
		score := fuzzyScore(entityLower, strings.ToLower(funcName))
		if score > 0 {
			matches = append(matches, match{funcName, score, "function"})
		}
	}

	// Search in types
	for typeName := range r.kbIndex.TypesByName {
		score := fuzzyScore(entityLower, strings.ToLower(typeName))
		if score > 0 {
			matches = append(matches, match{typeName, score, "type"})
		}
	}

	// Sort by score descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	// Convert to string results
	var results []string
	for i, m := range matches {
		if i >= 5 {
			break
		}
		results = append(results, fmt.Sprintf("%s (%s)", m.name, m.typ))
	}

	return results
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

	patternChars := make(map[rune]int)
	for _, ch := range pattern {
		patternChars[ch]++
	}

	for _, ch := range target {
		if patternChars[ch] > 0 {
			score += 2
			patternChars[ch]--
		}
	}

	lenDiff := len(target) - len(pattern)
	if lenDiff < 0 {
		lenDiff = -lenDiff
	}
	score -= lenDiff

	return score
}
