package query

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"unicode"
)

type QueryType int

const (
	QueryTypeLocation QueryType = iota + 1
	QueryTypeUsage
	QueryTypeUnderstanding
	QueryTypeImplementation
	QueryTypeArchitecture
)

type Classification struct {
	Type       QueryType
	Confidence float64
	Symbols    []string
	Keywords   []string
	Reasoning  string
}

type Classifier struct {
	locationPattern        *regexp.Regexp
	usagePattern          *regexp.Regexp
	architecturePattern   *regexp.Regexp
	implementationPattern *regexp.Regexp
	symbolPattern         *regexp.Regexp
	validSymbols          map[string]bool
}

// SymbolIndex is a simplified index for symbol validation
type SymbolIndex struct {
	Symbols []string `json:"symbols"`
}

func NewClassifier(kbIndexPath string) (*Classifier, error) {
	c := &Classifier{
		locationPattern:        regexp.MustCompile(`(?i)^(where|find|show|locate)\s`),
		usagePattern:          regexp.MustCompile(`(?i)(who|what).*(calls?|uses?|invokes?)`),
		architecturePattern:   regexp.MustCompile(`(?i)(architecture|structure|design|overview|diagram|flow)`),
		implementationPattern: regexp.MustCompile(`(?i)(implement|fix|debug|change|add|modify|refactor|update)`),
		symbolPattern:         regexp.MustCompile(`\b[A-Z][a-z]+(?:[A-Z][a-z]+)*\b|\b[a-z_][a-z0-9_]*\b|\b[A-Z_][A-Z0-9_]+\b`),
		validSymbols:          make(map[string]bool),
	}

	// Load symbol list from kb_index.json if path provided
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

	// Parse the full KB index structure
	var kbIndex struct {
		FunctionsByName map[string][]string `json:"functions_by_name"`
		TypesByName     map[string][]string `json:"types_by_name"`
	}

	if err := json.Unmarshal(data, &kbIndex); err != nil {
		return err
	}

	// Extract all function names
	for funcName := range kbIndex.FunctionsByName {
		c.validSymbols[funcName] = true
	}

	// Extract all type names
	for typeName := range kbIndex.TypesByName {
		c.validSymbols[typeName] = true
	}

	return nil
}

func (c *Classifier) Classify(query string) *Classification {
	query = strings.TrimSpace(query)
	queryLower := strings.ToLower(query)

	// Level 1: Fast Pattern Matching (< 1ms)
	if result := c.level1PatternMatch(query, queryLower); result != nil && result.Confidence >= 0.95 {
		return result
	}

	// Level 2: Symbol Validation (1-5ms)
	symbols := c.extractSymbols(query)
	validSymbols := c.validateSymbols(symbols)

	if len(validSymbols) > 0 {
		if result := c.level2SymbolAnalysis(query, queryLower, validSymbols); result != nil {
			return result
		}
	}

	// Level 3: Keyword Analysis
	return c.level3KeywordAnalysis(query, queryLower, validSymbols)
}

// Level 1: Fast Pattern Matching
func (c *Classifier) level1PatternMatch(query, queryLower string) *Classification {
	if c.locationPattern.MatchString(queryLower) {
		return &Classification{
			Type:       QueryTypeLocation,
			Confidence: 0.95,
			Reasoning:  "Level 1: location pattern match",
		}
	}

	if c.usagePattern.MatchString(queryLower) {
		return &Classification{
			Type:       QueryTypeUsage,
			Confidence: 0.95,
			Reasoning:  "Level 1: usage pattern match",
		}
	}

	if c.architecturePattern.MatchString(queryLower) {
		return &Classification{
			Type:       QueryTypeArchitecture,
			Confidence: 0.95,
			Reasoning:  "Level 1: architecture pattern match",
		}
	}

	if c.implementationPattern.MatchString(queryLower) {
		return &Classification{
			Type:       QueryTypeImplementation,
			Confidence: 0.95,
			Reasoning:  "Level 1: implementation pattern match",
		}
	}

	return nil
}

// Level 2: Symbol Validation
func (c *Classifier) level2SymbolAnalysis(query, queryLower string, symbols []string) *Classification {
	if len(symbols) == 0 {
		return nil
	}

	keywords := extractKeywords(queryLower)

	// Single symbol + location/usage keywords
	if len(symbols) == 1 {
		if containsAny(queryLower, []string{"where", "find", "locate", "show"}) {
			return &Classification{
				Type:       QueryTypeLocation,
				Confidence: 0.90,
				Symbols:    symbols,
				Keywords:   keywords,
				Reasoning:  "Level 2: single symbol with location keywords",
			}
		}

		if containsAny(queryLower, []string{"who", "what", "calls", "uses", "invokes"}) {
			return &Classification{
				Type:       QueryTypeUsage,
				Confidence: 0.90,
				Symbols:    symbols,
				Keywords:   keywords,
				Reasoning:  "Level 2: single symbol with usage keywords",
			}
		}
	}

	// Multiple symbols suggest understanding query
	if len(symbols) > 1 {
		return &Classification{
			Type:       QueryTypeUnderstanding,
			Confidence: 0.85,
			Symbols:    symbols,
			Keywords:   keywords,
			Reasoning:  "Level 2: multiple symbols detected",
		}
	}

	return nil
}

// Level 3: Keyword Analysis
func (c *Classifier) level3KeywordAnalysis(query, queryLower string, symbols []string) *Classification {
	keywords := extractKeywords(queryLower)

	// Check for implementation keywords
	implKeywords := []string{"implement", "fix", "debug", "change", "add", "modify", "refactor", "update", "create", "build"}
	if containsAny(queryLower, implKeywords) {
		return &Classification{
			Type:       QueryTypeImplementation,
			Confidence: 0.80,
			Symbols:    symbols,
			Keywords:   keywords,
			Reasoning:  "Level 3: implementation keywords detected",
		}
	}

	// Check for architecture keywords
	archKeywords := []string{"architecture", "structure", "design", "overview", "diagram", "flow", "pattern", "system"}
	if containsAny(queryLower, archKeywords) {
		return &Classification{
			Type:       QueryTypeArchitecture,
			Confidence: 0.80,
			Symbols:    symbols,
			Keywords:   keywords,
			Reasoning:  "Level 3: architecture keywords detected",
		}
	}

	// Default to Understanding
	return &Classification{
		Type:       QueryTypeUnderstanding,
		Confidence: 0.75,
		Symbols:    symbols,
		Keywords:   keywords,
		Reasoning:  "Level 3: general understanding query (default)",
	}
}

// Extract symbols preserving case sensitivity
func (c *Classifier) extractSymbols(query string) []string {
	matches := c.symbolPattern.FindAllString(query, -1)
	symbolMap := make(map[string]bool)
	symbols := []string{}

	for _, match := range matches {
		// Filter out common words that match symbol pattern
		if !isCommonWord(strings.ToLower(match)) && !symbolMap[match] {
			symbolMap[match] = true
			symbols = append(symbols, match)
		}
	}

	return symbols
}

// Validate symbols against KB index
func (c *Classifier) validateSymbols(symbols []string) []string {
	if len(c.validSymbols) == 0 {
		// If no KB index loaded, return all extracted symbols
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
