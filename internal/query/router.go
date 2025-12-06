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

	prompt := fmt.Sprintf(`You have AST and semantic information, NOT source code.

AST/SEMANTIC DATA:
%s

QUESTION: %s

WHAT YOU HAVE:
- Function signatures, types, relationships
- Call graphs, dependencies
- Symbol locations

WHAT YOU DON'T HAVE:
- Actual implementation logic
- Variable values or control flow details
- Complete business logic

ANSWER USING:
- Function names and signatures from the data
- Type information and relationships
- Call patterns and dependencies

SAY CLEARLY:
- "The AST shows function X calls Y"
- "I cannot see the implementation details"
- "Based on the signature, this function..."

SYMBOLS: %v
FILES: %v`, context, query, class.Symbols, relevantFiles)

	return r.llmClient.Query(context, prompt)
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

	prompt := fmt.Sprintf(`Analyze architecture using call graph and AST data.

CALL GRAPH:
%s

AST DATA:
%s

QUESTION: %s

YOU CAN DESCRIBE:
- Which functions call which (from call graph)
- Module/package structure (from AST)
- Type relationships and dependencies
- Layer separation (if evident from calls)

YOU CANNOT DESCRIBE:
- Why functions are called (no logic visible)
- Implementation patterns inside functions
- Specific algorithms used

Focus on structural relationships visible in the graph and AST.`,
		architectureInfo.String(), context, query)

	return r.llmClient.Query(context, prompt)
}

func (r *Router) handleDebug(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`Debug using AST/semantic information only.

AST DATA:
%s

PROBLEM: %s

YOU CAN CHECK:
- Type mismatches (from AST)
- Missing error handling (if return types show errors)
- Unused variables/functions
- Circular dependencies (from call graph)

YOU CANNOT CHECK:
- Logic errors (need actual code)
- Runtime behavior
- Specific error conditions

Be honest: "I can see X might return an error but cannot verify handling without code"

SYMBOLS: %v`, context, query, class.Symbols)

	return r.llmClient.Query(context, prompt)
}

func (r *Router) handleComparison(query string, class *Classification) (string, error) {
	if len(class.Symbols) < 2 {
		return "Comparison requires at least two entities. Please specify which functions/types to compare.", nil
	}

	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`Compare using AST/type information.

AST DATA:
%s

COMPARE: %v

QUESTION: %s

COMPARE BY:
- Function signatures (parameters, returns)
- Types used
- Dependencies and calls
- Package/module location

State clearly: "Signature-wise they differ in..." or "Cannot compare logic without source code"

Use actual symbols from the AST data.`, context, class.Symbols, query)

	return r.llmClient.Query(context, prompt)
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

	prompt := fmt.Sprintf(`Suggest refactoring from AST structure.

AST DATA:
%s

QUESTION: %s

YOU CAN DETECT:
- Functions with many dependencies (from call graph)
- Large parameter lists (from signatures)
- Duplicate type definitions
- Deep call chains
- Circular dependencies

YOU CANNOT DETECT:
- Code duplication (need source)
- Complex logic (need implementation)
- Naming quality inside functions

Focus on structural issues visible in AST/call graph.

SYMBOLS: %v`, context, query, class.Symbols)

	return r.llmClient.Query(context, prompt)
}


func (r *Router) handlePerformance(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`Performance analysis from AST data.

AST DATA:
%s

QUESTION: %s

YOU CAN INFER:
- Call depth and complexity (from call graph)
- Allocation patterns (from type info: slices, maps)
- Potential N+1 issues (from repeated calls in loops - if visible)
- Interface vs concrete types

YOU CANNOT DETERMINE:
- Actual algorithm complexity (need code)
- Loop behavior
- Memory usage patterns

Be explicit: "The call graph suggests..." or "Without seeing loops, I cannot assess..."

SYMBOLS: %v`, context, query, class.Symbols)

	return r.llmClient.Query(context, prompt)
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

	prompt := fmt.Sprintf(`Trace data flow using call graph and types.

CALL GRAPH:
%s

AST DATA:
%s

QUESTION: %s

TRACE BY:
- Parameter types flowing through calls
- Return values passed to next function
- Type transformations (input type -> output type)

EXAMPLE FORMAT:
"Function A returns *User -> passed to B -> B returns []UserDTO"

YOU CANNOT SEE:
- Data transformations inside functions
- Validation logic
- State mutations

Focus on type flow through the call chain.

SYMBOLS: %v`, callGraphInfo, context, query, class.Symbols)

	return r.llmClient.Query(context, prompt)
}

func (r *Router) handleSecurity(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`Security analysis from AST/types.

AST DATA:
%s

QUESTION: %s

CHECK:
- Exported vs unexported functions (from AST)
- Error return types (functions that might fail)
- Pointer vs value types (mutation risk)
- Interface usage (abstraction)

BE HONEST:
"I see function X takes user input (string parameter) but cannot verify validation without code"

YOU CANNOT CHECK:
- Input sanitization (need code)
- Actual auth logic
- SQL/XSS vulnerabilities

Focus on API surface and type safety.

SYMBOLS: %v`, context, query, class.Symbols)

	return r.llmClient.Query(context, prompt)
}

func (r *Router) handleDocumentation(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`Document from AST/signatures.

AST DATA:
%s

QUESTION: %s

DOCUMENT:
- Function name and purpose (from name + signature)
- Parameters: names, types, meanings
- Return values: types, error conditions
- Relationships: what it calls, what calls it

EXAMPLE:
"ProcessUser takes a *User and returns (*ProcessedUser, error)
Calls: ValidateUser, TransformUser
Called by: HandleRequest"

Cannot document: actual behavior, edge cases, implementation details

SYMBOLS: %v`, context, query, class.Symbols)

	return r.llmClient.Query(context, prompt)
}

func (r *Router) handleExample(query string, class *Classification) (string, error) {
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	prompt := fmt.Sprintf(`Create examples from function signatures.

AST DATA:
%s

QUESTION: %s

CREATE EXAMPLES FOR:
- Function calls with correct types
- Error handling (if returns error)
- Type construction

EXAMPLE FORMAT:
user := &User{Name: "test"}
result, err := ProcessUser(user)
if err != nil { ... }

Be clear: "This example shows correct types but I cannot verify the actual behavior"

SYMBOLS: %v`, context, query, class.Symbols)

	return r.llmClient.Query(context, prompt)
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
