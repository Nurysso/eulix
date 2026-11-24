// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"strings"

	"eulix/internal/query"
)

// ========== Retriever abstraction ==========

// Retriever is the semantic retrieval backend. It exists so the cascade can
// be exercised without a real query.Router: tests install a stub that returns
// canned text, and the production wiring uses routerRetriever.
//
// Implement it directly on top of the router's real anchored / graph-expansion
// entry points if it ever grows them.
type Retriever interface {
	// OpenEnded retrieves using semantic and subsystem signals only.
	OpenEnded(ctx context.Context, q string, cot bool) (string, error)

	// Anchored retrieves with explicit anchor symbols to drive exact lookup.
	Anchored(ctx context.Context, q string, anchors []string, cot bool) (string, error)

	// MultiHop retrieves from seed anchors and follows graph neighbours.
	// expansion holds neighbour symbols remembered from earlier queries.
	MultiHop(ctx context.Context, q string, seeds, expansion []string, cot bool) (string, error)
}

// ========== Router-backed Retriever ==========

// routerRetriever adapts *query.Router to the Retriever interface. Anchors are
// passed by appending them to the query text; the router's own symbol
// detection then picks them up.
//
// Questions the index answers directly (location, call graph, dependencies,
// ...) get the router's direct answer; callers/callees questions get the
// compact call report. Everything else is ranked context built with the
// agent budget (no answer reserve) and rendered for an agent.
type routerRetriever struct {
	router   *query.Router
	budget   int             // context tokens; 0 = the router's own LLM-sized budget
	maxLines int             // per-chunk line cap
	sent     map[string]bool // chunk ranges already sent this session
	answer   string          // answer type for the reply header
}

func (r routerRetriever) retrieve(q string, cot bool) (string, error) {
	if r.budget <= 0 {
		return r.router.PromptOrAnswerWithCoT(q, cot)
	}
	if r.router.IsDirectAnswer(q) {
		ac := r.router.ClassifyForAgent(q)
		if (ac.Intent == query.IntentCallersQ || ac.Intent == query.IntentCalleesQ) && ac.Symbol != "" {
			if rep, err := r.router.Calls(ac.Symbol, ac.Intent == query.IntentCalleesQ, 1, callSiteLimit); err == nil && rep.Found {
				return renderCalls(rep), nil
			}
		}
		return r.router.PromptOrAnswerWithCoT(q, cot)
	}
	ctx, err := r.router.AgentContextFor(q, r.budget, r.maxLines)
	if err != nil {
		return "", err
	}
	o := renderOpts{answer: r.answer, maxLines: r.maxLines, sent: r.sent, focus: focusTerms(q)}
	if o.answer == "" {
		o.answer = "search"
	}
	if cot {
		o.cot = r.router.CoTFor(q, ctx.Window)
	}
	return renderContext(ctx, o), nil
}

// OpenEnded forwards to the router.
func (r routerRetriever) OpenEnded(_ context.Context, q string, cot bool) (string, error) {
	return r.retrieve(q, cot)
}

// Anchored rewrites the query to include the given anchors.
func (r routerRetriever) Anchored(_ context.Context, q string, anchors []string, cot bool) (string, error) {
	return r.retrieve(composeAnchored(q, anchors), cot)
}

// MultiHop is Anchored with seeds and remembered expansion neighbours
// concatenated into a single anchor list.
func (r routerRetriever) MultiHop(_ context.Context, q string, seeds, expansion []string, cot bool) (string, error) {
	all := append(append([]string{}, seeds...), expansion...)
	return r.retrieve(composeAnchored(q, all), cot)
}

// composeAnchored appends a "Key symbols: ..." hint to the query so the
// router's symbol detector treats them as anchors. Returns q unchanged when
// there are no anchors.
func composeAnchored(q string, anchors []string) string {
	if len(anchors) == 0 {
		return q
	}
	return q + "  Key symbols: " + strings.Join(anchors, ", ")
}
