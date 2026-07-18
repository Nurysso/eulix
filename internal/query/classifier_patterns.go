//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package embeddings provides the command-line interface implementation for EULIX.

/*
Package query provides query classification functionality.
This file is responsible for classifying user queries into different
types such as location, usage, debugging, architecture, etc.
*/

package query

import (
	"fmt"
	"regexp"
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
	QueryTypeCodeGeneration
	// QueryTypeCallGraph QueryType = iota + 17
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
		architecturePattern:   regexp.MustCompile(`(?i)\b(architecture|overall\s+structure|high[\s-]level|system\s+design|component\s+diagram|module\s+organization)\b`),
		implementationPattern: regexp.MustCompile(`(?i)\b(implement\b|add\s+feature|create\s+new|build\s+a\b)`),
		debugPattern:          regexp.MustCompile(`(?i)\b(why\s+(is|does|doesn't\b)|debug|error|bug|issue|problem|not\s+working|fails?|crash|exception)\b`),
		comparisonPattern:     regexp.MustCompile(`(?i)\b(difference\s+between|compare\b|vs\.?\b|versus\b|similar\s+to|differs?\s+from|what's\s+the\s+difference)\b`),
		dependencyPattern:     regexp.MustCompile(`(?i)\b(depends?\s+on|depends?\b|dependency\b|dependencies\b|required\s+by|imports?\b|external\b|third[\s-]party|which\s+files\s+(use|import)|who\s+(uses?|imports?))\b`),
		refactoringPattern:    regexp.MustCompile(`(?i)\b(refactor\b|improve\b|optimize\b|clean\s+up|restructure\b|simplify\b|better\s+way)\b`),
		performancePattern:    regexp.MustCompile(`(?i)\b(performance|slow\b|fast\b|optimize\b|bottleneck|efficient\b|speed\b|latency|memory\s+usage)\b`),
		dataFlowPattern:       regexp.MustCompile(`(?i)\b(data\s+flow|how\s+data|trace\s+data|data\s+path|value\s+propagat|passes?\s+through)\b`),
		securityPattern:       regexp.MustCompile(`(?i)\b(security|vulnerable|sanitize|validation|injection|xss|csrf|authentication|authorization)\b`),
		examplePattern:        regexp.MustCompile(`(?i)\b(example\b|how\s+to\s+use|usage\s+example|sample\b|demonstrate\b|show\s+me\s+how)\b`),
		testingPattern:        regexp.MustCompile(`(?i)\b(test\b|unit\s+test|integration\s+test|mock\b|coverage\b|test\s+case)\b`),
		codeGenPattern:        regexp.MustCompile(`(?i)\b(show\s+me\s+code|write\s+code|code\s+example|sample\s+code|how\s+to\s+implement|generate\s+code)\b`),
		callGraphPattern:      regexp.MustCompile(`(?i)\b(call\s+graph|call\s+tree|who\s+calls|calls?\s+chain|callers?\s+of|callees?\s+of|call\s+hierarchy)\b`),
		entryPointPattern:     regexp.MustCompile(`(?i)\b(entry\s+points?|api\s+routes?|cli\s+commands?|main\s+functions?)\b|(?i)\b(list|show|find|what\s+are)\b.{0,40}\bendpoints?\b`),
		fileStructPattern:     regexp.MustCompile(`(?i)\b(what('?s|\s+is)\s+in\s+file|contents?\s+of\s+file|functions?\s+in\s+file|classes?\s+in\s+file|show\s+file)\b`),
		todosPattern:          regexp.MustCompile(`(?i)\b(todo\b|fixme\b|hack\b|security\s+note|technical\s+debt)\b`),
		metricsPattern:        regexp.MustCompile(`(?i)\b(complexity|metrics?|loc|lines\s+of\s+code|importance|hotspot)\b`),
		usagePrefixPattern:    regexp.MustCompile(`(?i)^(usage|use|uses\s+of|show\s+usage|find\s+usage|usage\s+of)\s+\S+`),
		understandingPattern:  regexp.MustCompile(`(?i)^(how\s+does\s+\w+\s+(work|authenticate|set|process)|explain\s+\w+|what\s+does\s+\w+\s+do|how\s+is\s+\w+\s+used)\b`),
		symbolPattern:         regexp.MustCompile(`[A-Z][a-zA-Z0-9]*(?:[A-Z][a-zA-Z0-9]*)*|[a-z][a-zA-Z0-9]{2,}`),
		validSymbols:          make(map[string]bool),
		validTypes:            make(map[string]bool),
	}
	return c, nil
}
