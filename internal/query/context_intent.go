//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides the context window builder and query routing for Eulix's
// RAG (Retrieval-Augmented Generation) system.

/*
This implements query intent classification and token budget
allocation for adaptive context window sizing.

INTENT CLASSIFICATION

Classifies user queries into eight intent types, each triggering different retrieval
and ranking strategies:

	IntentCallers       — "who calls X?" / "call sites of X"
                        High priority: call-site discovery + graph expansion
                        Focus: IntentCallers relationships

	IntentCallees       — "what does X call?" / "dependencies of X"
                        High priority: call-site discovery + graph expansion
                        Focus: IntentCallees relationships

	IntentSymbolExact   — "show me function foo" / explicit symbol lookup
                        High priority: kb.json exact match
                        Focus: definition + signature only, no dependency chasing

	IntentSymbolFuzzy   — rare; reserved for future fuzzy symbol matching

	IntentFlow          — "trace the execution" / "sequence of operations"
                        High priority: call graph + control flow
                        Focus: chaining related functions across files

	IntentConcept       — "how does X work?" / "explain Y"
                        High priority: semantic similarity + related symbols
                        Focus: multi-faceted understanding

	IntentDebug         — "why does this crash?" / error analysis
                        High priority: exception handling + error paths
                        Focus: error propagation + debugging context

	IntentUnknown       — fallback for ambiguous queries

Detection uses keyword matching with phrase-level priority (call-graph keywords
take precedence), combined with heuristics on code symbols (CamelCase, underscores)
and keyword frequency scoring. Specificity is computed from code symbol density
(0 for pure English, up to 1.0 for symbol-heavy queries).

BUDGET ALLOCATION

Distributes the LLM's context window across four retrieval strategies:

	kb_exact  — kb.json symbol table lookups (exact definitions)
	keyword   — inverted index term matching
	semantic  — embedding-based similarity search
	graph     — call graph expansion (callers/callees)

Strategy weights adapt to intent type:
	- Callers/Callees: 45% kb_exact (call-site priority) + 15% graph
	- SymbolExact: 55% kb_exact (definition-only)
	- Concept: 50% semantic (multi-faceted understanding)
	- Flow: 40% graph (execution sequencing)
	- Debug: 35% kb_exact + 35% keyword (error traces + error handling)

Token accounting reserves:
	- System prompt overhead: 150 tokens
	- Query encoding: len(query) / 4
	- Safety buffer: 200 tokens
	- Response reserve: 2000 tokens for LLM output
	- Context budget: 85% of remaining capacity

Example: 8k model (8192 tokens) with 50-char query:
	Query: ~12 tokens
	Reserves: 150 + 200 + 2000 = 2350 tokens
	Available: 8192 - 12 - 2350 = 5830 tokens
	Context budget: 5830 * 0.85 ≈ 4955 tokens

INTEGRATION

classifyQueryIntent: runs first in buildContextInternal, informs all downstream
decisions (candidate limit, strategy weights, selection method).

allocateBudget: runs after intent classification, determines per-strategy token
limits for multi-strategy search.
*/

package query

import (
	"math"
	"strings"
)

const (
	IntentUnknown IntentType = iota
	IntentSymbolExact
	IntentSymbolFuzzy
	IntentConcept
	IntentFlow
	IntentDebug
	IntentCallers
	IntentCallees
)

func (cb *ContextBuilder) classifyQueryIntent(query string) QueryIntent {
	lower := strings.ToLower(query)
	words := strings.Fields(lower)
	wordSet := make(map[string]bool, len(words))
	for _, w := range words {
		wordSet[w] = true
	}

	// Call-graph direction detection — checked first, highest priority
	callersKW := []string{"call sites", "who calls", "called by", "callers of", "invokes", "usage of", "uses of"}
	calleesKW := []string{"what does", "calls internally", "invokes inside", "dependencies of", "callees"}

	containsPhrase := func(kws []string) bool {
		for _, kw := range kws {
			if strings.Contains(lower, kw) {
				return true
			}
		}
		return false
	}

	debugKW := []string{"error", "panic", "nil", "crash", "fail", "bug", "exception", "segfault", "undefined", "invalid"}
	flowKW := []string{"trace", "flow", "lifecycle", "sequence", "step", "chain", "order", "when", "path"}
	conceptKW := []string{"how", "why", "interact", "implement", "how does", "what", "explain", "describe", "understand", "difference", "between", "purpose"}

	score := func(list []string) float64 {
		s := 0.0
		for _, kw := range list {
			if strings.Contains(lower, kw) {
				s++
			}
		}
		return s
	}

	dbg, flow, concept := score(debugKW), score(flowKW), score(conceptKW)
	symbols := extractPotentialSymbols(query)
	kwds := extractQueryKeywords(lower)

	codeSymbols := 0
	for _, s := range symbols {
		if strings.ContainsAny(s, "_") || (len(s) > 1 && s[0] >= 'A' && s[0] <= 'Z') {
			codeSymbols++
		}
	}
	specificity := math.Min(1.0, float64(codeSymbols)*0.35)

	switch {
	case containsPhrase(callersKW):
		return QueryIntent{
			Type:        IntentCallers,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.9,
			Confidence:  0.9,
		}
	case containsPhrase(calleesKW):
		return QueryIntent{
			Type:        IntentCallees,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.9,
			Confidence:  0.9,
		}
	case dbg > 0:
		return QueryIntent{
			Type:        IntentDebug,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: specificity,
			Confidence:  math.Min(1, dbg/2),
		}
	case flow > 0:
		return QueryIntent{
			Type:        IntentFlow,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: specificity,
			Confidence:  math.Min(1, flow/2),
		}
	case concept > 1 && codeSymbols >= 1:
		return QueryIntent{
			Type:        IntentConcept,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: math.Min(0.9, specificity),
			Confidence:  math.Min(1, concept/3),
		}
	case flow > 1 && codeSymbols >= 1:
		return QueryIntent{
			Type:        IntentFlow,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: math.Min(0.9, specificity),
			Confidence:  math.Min(1, flow/2),
		}
	case codeSymbols >= 2 && len(words) <= 5:
		// Short, symbol-heavy query → likely a "what does X call?"
		return QueryIntent{
			Type:        IntentCallees,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.9,
			Confidence:  0.85,
		}
	case codeSymbols >= 1:
		// IntentSymbolExact look up the symbol, don't chase its dependencies.
		return QueryIntent{
			Type:        IntentSymbolExact,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: specificity,
			Confidence:  0.7,
		}
	case concept > 0:
		return QueryIntent{
			Type:        IntentConcept,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.2,
			Confidence:  math.Min(1, concept/3),
		}
	default:
		return QueryIntent{
			Type:        IntentConcept,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.3,
			Confidence:  0.5,
		}
	}
}

func (qi QueryIntent) Budget() map[string]float64 {
	return map[string]float64{"keyword": qi.Specificity, "semantic": 1 - qi.Specificity}
}

func (cb *ContextBuilder) allocateBudget(query string, intent QueryIntent) BudgetAllocation {
	sysReserve := 150
	qTokens := len(query) / 4
	safetyBuf := 200
	respReserve := 2000
	total := cb.config.LLM.MaxTokens
	ctxBudget := int(float64(total-qTokens-sysReserve-safetyBuf-respReserve) * 0.85)

	w := map[string]float64{
		"kb_exact": 0.30, "keyword": 0.25, "semantic": 0.25, "graph": 0.20,
	}

	switch intent.Type {
	case IntentCallers, IntentCallees:
		// callsite scan is the primary signal — reduce semantic noise
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.45, 0.30, 0.10, 0.15
	case IntentSymbolExact:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.55, 0.20, 0.10, 0.15
	case IntentSymbolFuzzy:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.40, 0.25, 0.20, 0.15
	case IntentConcept:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.15, 0.20, 0.50, 0.15
	case IntentFlow:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.20, 0.15, 0.25, 0.40
	case IntentDebug:
		w["kb_exact"], w["keyword"], w["semantic"], w["graph"] = 0.35, 0.35, 0.15, 0.15
	}

	return BudgetAllocation{
		MaxTokens:       total,
		SystemReserve:   sysReserve,
		QueryCost:       qTokens,
		ResponseReserve: respReserve,
		ContextBudget:   ctxBudget,
		StrategyWeights: w,
	}
}

// applyBudget truncates a pre-sorted slice to fit within the token budget.
func (cb *ContextBuilder) applyBudget(chunks []ScoredChunk, budget int) []ScoredChunk {
	used := 0
	for i, sc := range chunks {
		used += sc.Chunk.Tokens
		if used > budget {
			return chunks[:i]
		}
	}
	return chunks
}
