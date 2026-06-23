//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
Package query provides query classification functionality.
This file is responsible for core classifying logic with 3 layers of query identification
Pattern -> symbol -> keyword
*/

package query

import (
	"regexp"
	"strings"
)

type Classifier struct {
	locationPattern       *regexp.Regexp
	usagePattern          *regexp.Regexp
	architecturePattern   *regexp.Regexp
	implementationPattern *regexp.Regexp
	debugPattern          *regexp.Regexp
	comparisonPattern     *regexp.Regexp
	dependencyPattern     *regexp.Regexp
	refactoringPattern    *regexp.Regexp
	performancePattern    *regexp.Regexp
	dataFlowPattern       *regexp.Regexp
	securityPattern       *regexp.Regexp
	understandingPattern  *regexp.Regexp
	examplePattern        *regexp.Regexp
	testingPattern        *regexp.Regexp
	codeGenPattern        *regexp.Regexp
	symbolPattern         *regexp.Regexp
	validSymbols          map[string]bool
	validTypes            map[string]bool
	callGraphPattern      *regexp.Regexp
	entryPointPattern     *regexp.Regexp
	fileStructPattern     *regexp.Regexp
	todosPattern          *regexp.Regexp
	metricsPattern        *regexp.Regexp
	usagePrefixPattern    *regexp.Regexp
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

func (c *Classifier) Classify(query string) *Classification {
	query = strings.TrimSpace(query)
	queryLower := strings.ToLower(query)

	// Level 1: Fast Pattern Matching with new types
	if result := c.level1PatternMatch(queryLower); result != nil && result.Confidence >= 0.95 {
		return result
	}

	// Level 2: Symbol Validation with entity extraction
	symbols := c.extractSymbols(query)
	validSymbols := c.validateSymbols(symbols)
	entities := c.extractEntities(validSymbols)

	if len(validSymbols) > 0 {
		if result := c.level2SymbolAnalysis(queryLower, validSymbols, entities); result != nil {
			return result
		}
	}

	// Level 3: Enhanced Keyword Analysis
	return c.level3KeywordAnalysis(queryLower, validSymbols, entities)
}

func (c *Classifier) level1PatternMatch(queryLower string) *Classification {
	// Priority order matters check more specific patterns first
	if c.codeGenPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeCodeGeneration,
			Confidence:   0.95,
			Reasoning:    "Level 1: code generation request",
			NeedsContext: false, // We can't help with this
			Priority:     1,
		}
	}
	// Debug queries
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
	// if c.documentationPattern.MatchString(queryLower) {
	// 	return &Classification{
	// 		Type:         QueryTypeDocumentation,
	// 		Confidence:   0.95,
	// 		Reasoning:    "Level 1: documentation pattern match",
	// 		NeedsContext: true,
	// 		Priority:     3,
	// 	}
	// }

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
	// Bare "usage <symbol>" prefix — must be first to avoid falling through to Understanding
	if c.usagePrefixPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeUsage,
			Confidence:   0.97,
			Reasoning:    "Level 1: usage prefix pattern",
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

	if c.callGraphPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeCallGraph,
			Confidence:   0.95,
			Reasoning:    "Level 1: call graph pattern",
			NeedsContext: false,
			Priority:     4,
		}
	}
	if c.entryPointPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeEntryPoints,
			Confidence:   0.95,
			Reasoning:    "Level 1: entry point pattern",
			NeedsContext: false,
			Priority:     4,
		}
	}
	if c.fileStructPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeFileStructure,
			Confidence:   0.95,
			Reasoning:    "Level 1: file structure pattern",
			NeedsContext: false,
			Priority:     5,
		}
	}
	if c.todosPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeTodos,
			Confidence:   0.95,
			Reasoning:    "Level 1: todos/security pattern",
			NeedsContext: false,
			Priority:     5,
		}
	}
	if c.metricsPattern.MatchString(queryLower) {
		return &Classification{
			Type:         QueryTypeMetrics,
			Confidence:   0.95,
			Reasoning:    "Level 1: metrics pattern",
			NeedsContext: false,
			Priority:     5,
		}
	}

	return nil
}

func (c *Classifier) level2SymbolAnalysis(queryLower string, symbols []string, entities []Entity) *Classification {
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
		if containsAny((queryLower), []string{"calls", "uses", "used by", "usage"}) {
			return &Classification{
				Type:         QueryTypeUsage,
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

func (c *Classifier) level3KeywordAnalysis(queryLower string, symbols []string, entities []Entity) *Classification {
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
