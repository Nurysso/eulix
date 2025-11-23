package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/llm"
)

type Router struct {
	eulixDir       string
	config         *config.Config
	classifier     *Classifier
	llmClient      *llm.Client
	cache          *cache.Manager
	contextBuilder *ContextBuilder // Will be initialized lazily
	kbIndex        *KBIndex
	callGraph      *CallGraph
}

// KBIndex represents the parsed kb_index.json
type KBIndex struct {
	FunctionsByName  map[string][]string `json:"functions_by_name"`
	FunctionsCalling map[string][]string `json:"functions_calling"`
	FunctionsByTag   map[string][]string `json:"functions_by_tag"`
	TypesByName      map[string][]string `json:"types_by_name"`
}

// CallGraph represents the parsed kb_call_graph.json
type CallGraph struct {
	Functions map[string]FunctionNode `json:"functions"`
	Types     map[string]TypeNode     `json:"types"`
}

type FunctionNode struct {
	Name     string   `json:"name"`
	Location string   `json:"location"`
	Calls    []string `json:"calls"`
	CalledBy []string `json:"called_by"`
}

type TypeNode struct {
	Name     string   `json:"name"`
	Location string   `json:"location"`
	Methods  []string `json:"methods"`
}

// QueryTrafficController creates router WITHOUT initializing embeddings
func QueryTrafficController(eulixDir string, cfg *config.Config, llmClient *llm.Client, cacheManager *cache.Manager) (*Router, error) {
	// Load KB index
	kbIndex, err := loadKBIndex(eulixDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load KB index: %w", err)
	}

	// Load call graph
	callGraph, err := loadCallGraph(eulixDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load call graph: %w", err)
	}

	// Create classifier with KB index path for symbol validation
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
		contextBuilder: nil, // Lazy initialization
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

// ensureContextBuilder initializes the context builder if not already done
func (r *Router) ensureContextBuilder() error {
	if r.contextBuilder != nil {
		return nil // Already initialized
	}

	// fmt.Println("🔧 Initializing embeddings (first LLM query)...")

	contextBuilder, err := ContextWindowCreator(r.eulixDir, r.config, r.llmClient)
	if err != nil {
		return fmt.Errorf("failed to initialize context builder: %w", err)
	}

	r.contextBuilder = contextBuilder
	// fmt.Println("✅ Embeddings ready")
	return nil
}

func (r *Router) Query(query string) (string, error) {
	// Check cache first
	if r.cache != nil {
		if cached, err := r.cache.Get(query); err == nil && cached != "" {
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
		// Only NOW initialize embeddings when needed
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
	default:
		if err := r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	}

	if err != nil {
		return "", err
	}

	// Cache the response
	if r.cache != nil {
		r.cache.Set(query, response)
	}

	return response, nil
}

func (r *Router) handleLocation(query string, class *Classification) (string, error) {
	// First try to use symbols from classification (case-preserved)
	var entity string
	if len(class.Symbols) > 0 {
		entity = class.Symbols[0]
	} else {
		// Fallback to extraction
		entity = extractEntityName(query)
	}

	if entity == "" {
		return "Could not identify function or class name in query", nil
	}

	var results []string

	// Search in functions (exact match, case-sensitive)
	if locations, ok := r.kbIndex.FunctionsByName[entity]; ok {
		results = append(results, fmt.Sprintf("Function '%s' found at:", entity))
		for _, loc := range locations {
			results = append(results, fmt.Sprintf("  📍 %s", loc))
		}
	}

	// Search in types/classes (exact match, case-sensitive)
	if locations, ok := r.kbIndex.TypesByName[entity]; ok {
		results = append(results, fmt.Sprintf("Type '%s' found at:", entity))
		for _, loc := range locations {
			results = append(results, fmt.Sprintf("  📍 %s", loc))
		}
	}

	// Try fuzzy matching if exact match fails
	if len(results) == 0 {
		matches := r.fuzzySearch(entity)
		if len(matches) > 0 {
			results = append(results, fmt.Sprintf("No exact match for '%s'. Did you mean:", entity))
			for _, match := range matches {
				results = append(results, fmt.Sprintf("  • %s", match))
			}
		} else {
			return fmt.Sprintf("Function or class '%s' not found in the codebase", entity), nil
		}
	}

	return strings.Join(results, "\n"), nil
}

func (r *Router) handleUsage(query string, class *Classification) (string, error) {
	// First try to use symbols from classification (case-preserved)
	var entity string
	if len(class.Symbols) > 0 {
		entity = class.Symbols[0]
	} else {
		// Fallback to extraction
		entity = extractEntityName(query)
	}

	if entity == "" {
		return "Could not identify function or class name in query", nil
	}

	var results []string

	// Check if it's in the call graph
	if funcNode, ok := r.callGraph.Functions[entity]; ok {
		results = append(results, fmt.Sprintf("📊 Usage Analysis for '%s':", entity))
		results = append(results, fmt.Sprintf("Location: %s", funcNode.Location))
		results = append(results, "")

		// Show what this function calls
		if len(funcNode.Calls) > 0 {
			results = append(results, "Calls:")
			for _, callee := range funcNode.Calls {
				results = append(results, fmt.Sprintf("  → %s", callee))
			}
			results = append(results, "")
		}

		// Show what calls this function
		if len(funcNode.CalledBy) > 0 {
			results = append(results, "Called by:")
			for _, caller := range funcNode.CalledBy {
				results = append(results, fmt.Sprintf("  ← %s", caller))
			}
		} else {
			results = append(results, "⚠️  Not called by any other function (possibly unused or entry point)")
		}
	} else if typeNode, ok := r.callGraph.Types[entity]; ok {
		results = append(results, fmt.Sprintf("📊 Type Analysis for '%s':", entity))
		results = append(results, fmt.Sprintf("Location: %s", typeNode.Location))
		results = append(results, "")

		if len(typeNode.Methods) > 0 {
			results = append(results, "Methods:")
			for _, method := range typeNode.Methods {
				results = append(results, fmt.Sprintf("  • %s", method))
			}
		}
	} else {
		// Try to find it in the index
		if callers, ok := r.kbIndex.FunctionsCalling[entity]; ok {
			results = append(results, fmt.Sprintf("Functions calling '%s':", entity))
			for _, caller := range callers {
				results = append(results, fmt.Sprintf("  ← %s", caller))
			}
		} else {
			return fmt.Sprintf("No usage information found for '%s'", entity), nil
		}
	}

	return strings.Join(results, "\n"), nil
}

func (r *Router) handleUnderstanding(query string, class *Classification) (string, error) {
	// Build context window (this uses embeddings)
	context, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	// Query LLM
	response, err := r.llmClient.Query(context, query)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return response, nil
}

func (r *Router) handleImplementation(query string, class *Classification) (string, error) {
	return r.handleUnderstanding(query, class)
}

func (r *Router) handleArchitecture(query string, class *Classification) (string, error) {
	return r.handleUnderstanding(query, class)
}

// Close cleans up resources
func (r *Router) Close() error {
	if r.contextBuilder != nil {
		return r.contextBuilder.Close()
	}
	return nil
}

// extractEntityName extracts function/class name from natural language query
func extractEntityName(query string) string {
	// Create a lowercase version for comparison only
	queryLower := strings.ToLower(query)

	// Remove common question words by their positions
	words := strings.Fields(query)       // Keep original case
	wordsLower := strings.Fields(queryLower)

	stopWords := map[string]bool{
		"where": true, "is": true, "the": true, "function": true,
		"class": true, "method": true, "type": true, "find": true,
		"locate": true, "what": true, "does": true, "do": true,
		"who": true, "calls": true, "uses": true, "used": true,
		"a": true, "an": true, "this": true, "that": true,
	}

	// Filter out stop words while preserving case
	var filtered []string
	for i, word := range words {
		if !stopWords[wordsLower[i]] {
			filtered = append(filtered, word)
		}
	}

	if len(filtered) > 0 {
		return filtered[0]
	}

	return ""
}

// fuzzySearch performs fuzzy matching on function/class names
func (r *Router) fuzzySearch(entity string) []string {
	entityLower := strings.ToLower(entity)
	var matches []string

	// Search in functions
	for funcName := range r.kbIndex.FunctionsByName {
		if strings.Contains(strings.ToLower(funcName), entityLower) {
			matches = append(matches, funcName+" (function)")
		}
	}

	// Search in types
	for typeName := range r.kbIndex.TypesByName {
		if strings.Contains(strings.ToLower(typeName), entityLower) {
			matches = append(matches, typeName+" (type)")
		}
	}

	// Limit to top 5 matches
	if len(matches) > 5 {
		matches = matches[:5]
	}

	return matches
}
