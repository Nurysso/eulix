//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package classifier deterministically categorizes incoming queries to streamline CoT routing and short-circuit non-LLM requests.

/*
Package query provides query classification functionality.
This file is responsible for classifying user queries into different
types such as location, usage, debugging, architecture, etc.
*/

package classifier

import (
	"fmt"
	"regexp"
)

type QueryType int

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
	QueryTypeCodeGeneration
	QueryTypeCallGraph
	QueryTypeEntryPoints
	QueryTypeFileStructure
	QueryTypeTodos
	QueryTypeMetrics
)

func (qt QueryType) String() string {
	names := [...]string{
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
		"CodeGeneration",
		"CallGraph",
		"EntryPoints",
		"FileStructure",
		"Todos",
		"Metrics",
	}
	if int(qt) >= len(names) {
		return fmt.Sprintf("QueryType(%d)", int(qt))
	}
	return names[qt]
}

func QuerySheriff(kbIndexPath string) (*Classifier, error) {
	c := &Classifier{
		locationPattern:       regexp.MustCompile(`(?i)^(where\s+(is|are|can\s+i\s+find)|find\s+the|show\s+me|\blocate\b|\blocation\s+of\b)\s+`),
		usagePattern:          regexp.MustCompile(`(?i)\b(who|what|which)\b.*\b(calls?|uses?|invokes?|depends\s+on|references?)\b`),
		architecturePattern:   regexp.MustCompile(`(?i)\b(architectures?|overall\s+structures?|high[\s-]level|system\s+design|component\s+diagrams?|module\s+organizations?)\b`),
		implementationPattern: regexp.MustCompile(`(?i)\b(implements?|implemented|implementing|add\s+features?|create\s+new|build\s+a\b|how\s+is\s+\w+\s+implemented)\b`),
		debugPattern:          regexp.MustCompile(`(?i)\b(why\s+(is|does|doesn't\b)|debugs?|debugging|errors?|bugs?|issues?|problems?|not\s+working|fails?|crashes?|exceptions?)\b`),
		comparisonPattern:     regexp.MustCompile(`(?i)\b(differences?\s+between|compares?|vs\.?\b|versus\b|similar\s+to|differs?\s+from|what's\s+the\s+difference)\b`),
		dependencyPattern:     regexp.MustCompile(`(?i)\b(depends?\s+on|depends?\b|dependenc(y|ies)\b|required\s+by|imports?\b|external\b|third[\s-]party|which\s+files\s+(use|import)|who\s+(uses?|imports?))\b`),
		refactoringPattern:    regexp.MustCompile(`(?i)\b(refactors?|refactoring|improves?|improving|optimizes?|optimizing|clean\s+up|restructures?|restructuring|simplif(y|ies|ying)|better\s+way)\b`),
		performancePattern:    regexp.MustCompile(`(?i)\b(performance|slow\b|fast\b|optimize|optimization|bottleneck|efficient|efficiency|speed|speeds|latency|memory\s+usage|benchmarks?)\b`),
		dataFlowPattern:       regexp.MustCompile(`(?i)\b(data\s+flows?|how\s+data|trace\s+data|data\s+paths?|value\s+propagat|passes?\s+through)\b`),
		securityPattern:       regexp.MustCompile(`(?i)\b(security|vulnerab\w+|sanitize|sanitiz\w+|validat(e|es|ion|ing)|injections?|xss|csrf|authenticat(e|es|ion|ing)|authoriz(e|es|ation|ing))\b`),
		examplePattern:        regexp.MustCompile(`(?i)\b(examples?|how\s+to\s+use|usage\s+examples?|samples?|demonstrat(e|es|ion|ing)|show\s+me\s+how)\b`),
		testingPattern:        regexp.MustCompile(`(?i)\b(tests?|testing|unit\s+tests?|integration\s+tests?|mocks?|mocking|coverage|test\s+cases?)\b`),
		codeGenPattern:        regexp.MustCompile(`(?i)\b(show\s+me\s+code|write\s+code|code\s+examples?|sample\s+code|how\s+to\s+implement|generate\s+code)\b`),
		callGraphPattern:      regexp.MustCompile(`(?i)\b(call\s+graphs?|call\s+trees?|who\s+calls?|calls?\s+chains?|callers?\s+of|callees?\s+of|call\s+hierarchies?)\b`),
		entryPointPattern:     regexp.MustCompile(`(?i)\b(entry\s+points?|api\s+routes?|cli\s+commands?|main\s+functions?)\b|(?i)\b(list|show|find|what\s+are)\b.{0,40}\bendpoints?\b`),
		fileStructPattern:     regexp.MustCompile(`(?i)\b(what('?s|\s+is)\s+in\s+file|contents?\s+of\s+file|functions?\s+in\s+file|classes?\s+in\s+file|show\s+file|(project|directory|repo|codebase|file)\s+layouts?)\b`),
		todosPattern:          regexp.MustCompile(`(?i)\b(todos?|fixmes?|hacks?|security\s+notes?|technical\s+debt)\b`),
		metricsPattern:        regexp.MustCompile(`(?i)\b(complexit(y|ies)|metrics?|loc|lines\s+of\s+code|importan(ce|t)|hotspots?)\b`),
		usagePrefixPattern:    regexp.MustCompile(`(?i)^(usage|use|uses\s+of|show\s+usage|find\s+usage|usage\s+of)\s+\S+`),
		understandingPattern:  regexp.MustCompile(`(?i)^(how\s+does\s+\w+\s+(works?|authenticate|set|process)|explain\s+\w+|what\s+does\s+\w+\s+do|how\s+is\s+\w+\s+used)\b`),
		symbolPattern:         regexp.MustCompile(`[A-Z][a-zA-Z0-9]*(?:[A-Z][a-zA-Z0-9]*)*|_?[a-z][a-zA-Z0-9_]{2,}`),
		validSymbols:          make(map[string]bool),
		validTypes:            make(map[string]bool),
	}
	return c, nil
}
