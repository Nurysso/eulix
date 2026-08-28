//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides context window building and query routing for Eulix.

package query

import (
	"eulix/internal/utils"
	"math"
	"sort"
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

// classifyQueryIntent categorizes the query into an intent type to guide retrieval and scoring.
func (cb *ContextBuilder) classifyQueryIntent(query string) QueryIntent {
	lower := strings.ToLower(query)
	words := strings.Fields(lower)
	wordSet := make(map[string]bool, len(words))
	for _, w := range words {
		wordSet[w] = true
	}

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
		return QueryIntent{
			Type:        IntentCallees,
			Symbols:     symbols,
			Keywords:    kwds,
			Specificity: 0.9,
			Confidence:  0.85,
		}
	case codeSymbols >= 1:
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

// Budget calculates strategy weights based on query specificity.
func (qi QueryIntent) Budget() map[string]float64 {
	return map[string]float64{"keyword": qi.Specificity, "semantic": 1 - qi.Specificity}
}

// allocateBudget divides token capacity between reserves and retrieval strategies based on query intent.
func (cb *ContextBuilder) allocateBudget(query string, intent QueryIntent) BudgetAllocation {
	total := cb.config.LLM.MaxTokens

	sysPromptTokens := 130
	qTokens := utils.CountTokens(query, cb.config.LLM.Provider) + 10
	respReserve := 2048

	ctxBudget := total - sysPromptTokens - qTokens - respReserve
	if ctxBudget < 512 {
		ctxBudget = 512
	}

	type weights struct{ kbExact, keyword, semantic, graph float64 }
	var ww weights
	switch intent.Type {
	case IntentCallers, IntentCallees:
		ww = weights{0.45, 0.30, 0.10, 0.15}
	case IntentSymbolExact:
		ww = weights{0.55, 0.20, 0.10, 0.15}
	case IntentSymbolFuzzy:
		ww = weights{0.40, 0.25, 0.20, 0.15}
	case IntentConcept:
		ww = weights{0.15, 0.20, 0.50, 0.15}
	case IntentFlow:
		ww = weights{0.20, 0.15, 0.25, 0.40}
	case IntentDebug:
		ww = weights{0.35, 0.35, 0.15, 0.15}
	default:
		ww = weights{0.30, 0.25, 0.25, 0.20}
	}

	return BudgetAllocation{
		MaxTokens:       total,
		SystemReserve:   sysPromptTokens,
		QueryCost:       qTokens,
		ResponseReserve: respReserve,
		ContextBudget:   ctxBudget,
		StrategyWeights: map[string]float64{
			"kb_exact": ww.kbExact,
			"keyword":  ww.keyword,
			"semantic": ww.semantic,
			"graph":    ww.graph,
		},
	}
}

// applyBudget selects scored chunks greedily and bin-packs remaining capacity to fit the token budget.
func (cb *ContextBuilder) applyBudget(chunks []ScoredChunk, budget int) []ScoredChunk {
	if len(chunks) == 0 || budget <= 0 {
		return nil
	}

	included := make([]ScoredChunk, 0, len(chunks))
	var skipped []ScoredChunk
	remaining := budget

	for _, sc := range chunks {
		cost := sc.Tokens
		if cost <= 0 {
			cost = 1
		}
		if cost <= remaining {
			included = append(included, sc)
			remaining -= cost
		} else {
			skipped = append(skipped, sc)
		}
	}

	if remaining > 0 && len(skipped) > 0 {
		sort.Slice(skipped, func(i, j int) bool {
			return skipped[i].Tokens < skipped[j].Tokens
		})
		for _, sc := range skipped {
			if remaining <= 0 {
				break
			}
			cost := sc.Tokens
			if cost <= remaining {
				included = append(included, sc)
				remaining -= cost
			}
		}
	}

	cb.debugLog.Log("applyBudget: %d/%d chunks fit, %d tokens used of %d (%d remaining)",
		len(included), len(chunks), budget-remaining, budget, remaining)

	return included
}
