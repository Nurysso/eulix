package query

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"unicode"
)


const (
	QueryTypeLocation QueryType = iota + 1
	QueryTypeUsage
	QueryTypeUnderstanding
	QueryTypeImplementation
	QueryTypeArchitecture
	QueryTypeDebug
	QueryTypeComparison
	QueryTypeDependency
	QueryTypeRefactoring
	QueryTypePerformance
	QueryTypeDataFlow
	QueryTypeSecurity
	QueryTypeDocumentation
	QueryTypeExample
	QueryTypeTesting
)

func (qt QueryType) String() string {
	return [...]string{
		"Unknown",
		"Location",
		"Usage",
		"Understanding",
		"Implementation",
		"Architecture",
		"Debug",
		"Comparison",
		"Dependency",
		"Refactoring",
		"Performance",
		"DataFlow",
		"Security",
		"Documentation",
		"Example",
		"Testing",
	}[qt]
}

type Classification struct {
	Type         QueryType
	Confidence   float64
	Symbols      []string
	Keywords     []string
	Reasoning    string
	Priority     int
	NeedsContext bool
	Entities     []Entity
}

type Entity struct {
	Name string
	Type string
}

type Classifier struct {
	// Existing patterns
	locationPattern        *regexp.Regexp
	usagePattern          *regexp.Regexp
	architecturePattern   *regexp.Regexp
	implementationPattern *regexp.Regexp

	// New patterns
	debugPattern          *regexp.Regexp
	comparisonPattern     *regexp.Regexp
	dependencyPattern     *regexp.Regexp
	refactoringPattern    *regexp.Regexp
	performancePattern    *regexp.Regexp
	dataFlowPattern       *regexp.Regexp
	securityPattern       *regexp.Regexp
	documentationPattern  *regexp.Regexp
	examplePattern        *regexp.Regexp
	testingPattern        *regexp.Regexp

	symbolPattern         *regexp.Regexp
	validSymbols          map[string]bool
	validTypes            map[string]bool
}

type SymbolIndex struct {
	Symbols []string `json:"symbols"`
}

func QuerySheriff(kbIndexPath string) (*Classifier, error) {
	c := &Classifier{
		// Existing patterns - more specific
		locationPattern:        regexp.MustCompile(`(?i)^(where\s+(is|are|can\s+i\s+find)|find\s+the|show\s+me|locate)\s`),
		usagePattern:          regexp.MustCompile(`(?i)(who|what|which).*(calls?|uses?|invokes?|depends\s+on|references?)`),
		architecturePattern:   regexp.MustCompile(`(?i)(architecture|overall\s+structure|high[\s-]level|system\s+design|component\s+diagram|module\s+organization)`),
		implementationPattern: regexp.MustCompile(`(?i)(implement|add\s+feature|create\s+new|build\s+a)`),

		// New patterns
		debugPattern:          regexp.MustCompile(`(?i)(why\s+(is|does|doesn't)|debug|error|bug|issue|problem|not\s+working|fails?|crash|exception)`),
		comparisonPattern:     regexp.MustCompile(`(?i)(difference\s+between|compare|vs\.?|versus|similar\s+to|differs?\s+from|what's\s+the\s+difference)`),
		dependencyPattern:     regexp.MustCompile(`(?i)(depends?\s+on|dependencies|required\s+by|imports?|external|third[\s-]party)`),
		refactoringPattern:    regexp.MustCompile(`(?i)(refactor|improve|optimize|clean\s+up|restructure|simplify|better\s+way)`),
		performancePattern:    regexp.MustCompile(`(?i)(performance|slow|fast|optimize|bottleneck|efficient|speed|latency|memory\s+usage)`),
		dataFlowPattern:       regexp.MustCompile(`(?i)(data\s+flow|how\s+data|trace\s+data|data\s+path|value\s+propagat|passes?\s+through)`),
		securityPattern:       regexp.MustCompile(`(?i)(security|vulnerable|sanitize|validation|injection|xss|csrf|authentication|authorization)`),
		documentationPattern:  regexp.MustCompile(`(?i)(document|comment|explain|describe|what\s+does|purpose\s+of|meant\s+to\s+do)`),
		examplePattern:        regexp.MustCompile(`(?i)(example|how\s+to\s+use|usage\s+example|sample|demonstrate|show\s+me\s+how)`),
		testingPattern:        regexp.MustCompile(`(?i)(test|unit\s+test|integration\s+test|mock|coverage|test\s+case)`),

		symbolPattern:         regexp.MustCompile(`\b[A-Z][a-z]+(?:[A-Z][a-z]+)*\b|\b[a-z_][a-z0-9_]*\b|\b[A-Z_][A-Z0-9_]+\b`),
		validSymbols:          make(map[string]bool),
		validTypes:            make(map[string]bool),
	}

	if kbIndexPath != "" {
		if err := c.loadSymbols(kbIndexPath); err != nil {
			return nil, err
		}
	}

	return c, nil
}

func (c *Classifier) loadSymbols(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var kbIndex struct {
		FunctionsByName map[string][]string `json:"functions_by_name"`
		TypesByName     map[string][]string `json:"types_by_name"`
	}

	if err := json.Unmarshal(data, &kbIndex); err != nil {
		return err
	}

	for funcName := range kbIndex.FunctionsByName {
		c.validSymbols[funcName] = true
	}

	for typeName := range kbIndex.TypesByName {
		c.validSymbols[typeName] = true
		c.validTypes[typeName] = true
	}

	return nil
}

func (c *Classifier) Classify(query string) *Classification {
	query = strings.TrimSpace(query)
	queryLower := strings.ToLower(query)

	// Level 1: Fast Pattern Matching with new types
	if result := c.level1PatternMatch(query, queryLower); result != nil && result.Confidence >= 0.95 {
		return result
	}

	// Level 2: Symbol Validation with entity extraction
	symbols := c.extractSymbols(query)
	validSymbols := c.validateSymbols(symbols)
	entities := c.extractEntities(validSymbols)

	if len(validSymbols) > 0 {
		if result := c.level2SymbolAnalysis(query, queryLower, validSymbols, entities); result != nil {
			return result
		}
	}

	// Level 3: Enhanced Keyword Analysis
	return c.level3KeywordAnalysis(query, queryLower, validSymbols, entities)
}

func (c *Classifier) level1PatternMatch(query, queryLower string) *Classification {
	// Priority order matters - check more specific patterns first

	// Debug queries (high priority - often urgent)
	if c.debugPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeDebug,
			Confidence:   0.95,
			Reasoning:    "Level 1: debug/error pattern match",
			NeedsContext: true,
			Priority:     1,
		}
	}

	// Comparison queries
	if c.comparisonPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeComparison,
			Confidence:   0.95,
			Reasoning:    "Level 1: comparison pattern match",
			NeedsContext: true,
			Priority:     2,
		}
	}

	// Example queries
	if c.examplePattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeExample,
			Confidence:   0.95,
			Reasoning:    "Level 1: example/usage pattern match",
			NeedsContext: true,
			Priority:     2,
		}
	}

	// Data flow queries
	if c.dataFlowPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeDataFlow,
			Confidence:   0.95,
			Reasoning:    "Level 1: data flow pattern match",
			NeedsContext: true,
			Priority:     3,
		}
	}

	// Security queries
	if c.securityPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeSecurity,
			Confidence:   0.95,
			Reasoning:    "Level 1: security pattern match",
			NeedsContext: true,
			Priority:     1,
		}
	}

	// Performance queries
	if c.performancePattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypePerformance,
			Confidence:   0.95,
			Reasoning:    "Level 1: performance pattern match",
			NeedsContext: true,
			Priority:     2,
		}
	}

	// Refactoring queries
	if c.refactoringPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeRefactoring,
			Confidence:   0.95,
			Reasoning:    "Level 1: refactoring pattern match",
			NeedsContext: true,
			Priority:     3,
		}
	}

	// Dependency queries
	if c.dependencyPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeDependency,
			Confidence:   0.95,
			Reasoning:    "Level 1: dependency pattern match",
			NeedsContext: false,
			Priority:     2,
		}
	}

	// Testing queries
	if c.testingPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeTesting,
			Confidence:   0.95,
			Reasoning:    "Level 1: testing pattern match",
			NeedsContext: true,
			Priority:     3,
		}
	}

	// Documentation queries
	if c.documentationPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeDocumentation,
			Confidence:   0.95,
			Reasoning:    "Level 1: documentation pattern match",
			NeedsContext: true,
			Priority:     3,
		}
	}

	// Original patterns
	if c.locationPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeLocation,
			Confidence:   0.95,
			Reasoning:    "Level 1: location pattern match",
			NeedsContext: false,
			Priority:     5,
		}
	}

	if c.usagePattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeUsage,
			Confidence:   0.95,
			Reasoning:    "Level 1: usage pattern match",
			NeedsContext: false,
			Priority:     4,
		}
	}

	if c.architecturePattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeArchitecture,
			Confidence:   0.95,
			Reasoning:    "Level 1: architecture pattern match",
			NeedsContext: true,
			Priority:     3,
		}
	}

	if c.implementationPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeImplementation,
			Confidence:   0.95,
			Reasoning:    "Level 1: implementation pattern match",
			NeedsContext: true,
			Priority:     2,
		}
	}

	return nil
}

func (c *Classifier) level2SymbolAnalysis(query, queryLower string, symbols []string, entities []Entity) *Classification {
	if len(symbols) == 0 {
		return nil
	}

	keywords := extractKeywords(queryLower)

	// Multiple symbols + comparison keywords
	if len(symbols) >= 2 && containsAny(queryLower, []string{"difference", "compare", "vs", "versus", "similar"}) {
		return &Classification{
			Type:         QueryTypeComparison,
			Confidence:   0.92,
			Symbols:      symbols,
			Keywords:     keywords,
			Entities:     entities,
			Reasoning:    "Level 2: multiple symbols with comparison keywords",
			NeedsContext: true,
			Priority:     2,
		}
	}

	// Single symbol queries
	if len(symbols) == 1 {
		if containsAny(queryLower, []string{"where", "find", "locate", "show"}) {
			return &Classification{
				Type:         QueryTypeLocation,
				Confidence:   0.90,
				Symbols:      symbols,
				Keywords:     keywords,
				Entities:     entities,
				Reasoning:    "Level 2: single symbol with location keywords",
				NeedsContext: false,
				Priority:     5,
			}
		}

		if containsAny(queryLower, []string{"calls", "uses", "invokes", "called by", "used by"}) {
			return &Classification{
				Type:         QueryTypeUsage,
				Confidence:   0.90,
				Symbols:      symbols,
				Keywords:     keywords,
				Entities:     entities,
				Reasoning:    "Level 2: single symbol with usage keywords",
				NeedsContext: false,
				Priority:     4,
			}
		}

		if containsAny(queryLower, []string{"example", "how to use", "sample"}) {
			return &Classification{
				Type:         QueryTypeExample,
				Confidence:   0.90,
				Symbols:      symbols,
				Keywords:     keywords,
				Entities:     entities,
				Reasoning:    "Level 2: single symbol with example keywords",
				NeedsContext: true,
				Priority:     2,
			}
		}
	}

	// Multiple symbols suggest understanding query
	if len(symbols) > 1 {
		return &Classification{
			Type:         QueryTypeUnderstanding,
			Confidence:   0.85,
			Symbols:      symbols,
			Keywords:     keywords,
			Entities:     entities,
			Reasoning:    "Level 2: multiple symbols detected",
			NeedsContext: true,
			Priority:     3,
		}
	}

	return nil
}

func (c *Classifier) level3KeywordAnalysis(query, queryLower string, symbols []string, entities []Entity) *Classification {
	keywords := extractKeywords(queryLower)

	// Check for debug keywords
	debugKeywords := []string{"debug", "error", "bug", "issue", "problem", "crash", "exception", "not working", "fails"}
	if containsAny(queryLower, debugKeywords) {
		return &Classification{
			Type:         QueryTypeDebug,
			Confidence:   0.85,
			Symbols:      symbols,
			Keywords:     keywords,
			Entities:     entities,
			Reasoning:    "Level 3: debug keywords detected",
			NeedsContext: true,
			Priority:     1,
		}
	}

	// Check for performance keywords
	perfKeywords := []string{"performance", "slow", "optimize", "bottleneck", "efficient", "speed", "memory"}
	if containsAny(queryLower, perfKeywords) {
		return &Classification{
			Type:         QueryTypePerformance,
			Confidence:   0.85,
			Symbols:      symbols,
			Keywords:     keywords,
			Entities:     entities,
			Reasoning:    "Level 3: performance keywords detected",
			NeedsContext: true,
			Priority:     2,
		}
	}

	// Check for refactoring keywords
	refactorKeywords := []string{"refactor", "improve", "clean up", "restructure", "simplify", "better way"}
	if containsAny(queryLower, refactorKeywords) {
		return &Classification{
			Type:         QueryTypeRefactoring,
			Confidence:   0.85,
			Symbols:      symbols,
			Keywords:     keywords,
			Entities:     entities,
			Reasoning:    "Level 3: refactoring keywords detected",
			NeedsContext: true,
			Priority:     3,
		}
	}

	// Check for testing keywords
	testKeywords := []string{"test", "unit test", "mock", "coverage", "test case"}
	if containsAny(queryLower, testKeywords) {
		return &Classification{
			Type:         QueryTypeTesting,
			Confidence:   0.85,
			Symbols:      symbols,
			Keywords:     keywords,
			Entities:     entities,
			Reasoning:    "Level 3: testing keywords detected",
			NeedsContext: true,
			Priority:     3,
		}
	}

	// Check for implementation keywords
	implKeywords := []string{"implement", "add", "create", "build"}
	if containsAny(queryLower, implKeywords) {
		return &Classification{
			Type:         QueryTypeImplementation,
			Confidence:   0.80,
			Symbols:      symbols,
			Keywords:     keywords,
			Entities:     entities,
			Reasoning:    "Level 3: implementation keywords detected",
			NeedsContext: true,
			Priority:     2,
		}
	}

	// Check for architecture keywords
	archKeywords := []string{"architecture", "structure", "design", "overview", "system"}
	if containsAny(queryLower, archKeywords) {
		return &Classification{
			Type:         QueryTypeArchitecture,
			Confidence:   0.80,
			Symbols:      symbols,
			Keywords:     keywords,
			Entities:     entities,
			Reasoning:    "Level 3: architecture keywords detected",
			NeedsContext: true,
			Priority:     3,
		}
	}

	// Default to Understanding
	return &Classification{
		Type:         QueryTypeUnderstanding,
		Confidence:   0.75,
		Symbols:      symbols,
		Keywords:     keywords,
		Entities:     entities,
		Reasoning:    "Level 3: general understanding query (default)",
		NeedsContext: true,
		Priority:     3,
	}
}

func (c *Classifier) extractSymbols(query string) []string {
	matches := c.symbolPattern.FindAllString(query, -1)
	symbolMap := make(map[string]bool)
	symbols := []string{}

	for _, match := range matches {
		if !isCommonWord(strings.ToLower(match)) && !symbolMap[match] {
			symbolMap[match] = true
			symbols = append(symbols, match)
		}
	}

	return symbols
}

func (c *Classifier) validateSymbols(symbols []string) []string {
	if len(c.validSymbols) == 0 {
		return symbols
	}

	validated := []string{}
	for _, symbol := range symbols {
		if c.validSymbols[symbol] {
			validated = append(validated, symbol)
		}
	}

	return validated
}

func (c *Classifier) extractEntities(symbols []string) []Entity {
	entities := []Entity{}

	for _, symbol := range symbols {
		entityType := "unknown"

		if c.validTypes[symbol] {
			entityType = "type"
		} else if c.validSymbols[symbol] {
			entityType = "function"
		}

		entities = append(entities, Entity{
			Name: symbol,
			Type: entityType,
		})
	}

	return entities
}

func extractKeywords(queryLower string) []string {
	stopWords := map[string]bool{
		"how": true, "does": true, "the": true, "a": true, "an": true,
		"is": true, "are": true, "what": true, "where": true, "when": true,
		"can": true, "will": true, "should": true, "would": true, "could": true,
		"this": true, "that": true, "these": true, "those": true, "of": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "with": true,
	}

	words := strings.FieldsFunc(queryLower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
	})

	keywords := []string{}
	for _, word := range words {
		if !stopWords[word] && len(word) > 2 {
			keywords = append(keywords, word)
		}
	}

	return keywords
}

func isCommonWord(word string) bool {
	commonWords := map[string]bool{
		"the": true, "this": true, "that": true, "these": true, "those": true,
		"what": true, "where": true, "when": true, "why": true, "how": true,
		"can": true, "will": true, "should": true, "would": true, "could": true,
		"does": true, "has": true, "have": true, "been": true, "are": true,
	}
	return commonWords[word]
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}
