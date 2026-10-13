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

// routerRetriever adapts *query.Router to the Retriever interface. The
// router's only public entry point is PromptOrAnswerWithCoT, so anchors are
// passed by appending them to the query text; the router's own symbol
// detection then picks them up.
//
// PLUG POINT: if query.Router grows dedicated anchored or graph-expansion
// methods, call them here instead of rewriting the query text.
type routerRetriever struct{ router *query.Router }

// OpenEnded forwards to the router.
func (r routerRetriever) OpenEnded(_ context.Context, q string, cot bool) (string, error) {
	return r.router.PromptOrAnswerWithCoT(q, cot)
}

// Anchored rewrites the query to include the given anchors.
func (r routerRetriever) Anchored(_ context.Context, q string, anchors []string, cot bool) (string, error) {
	return r.router.PromptOrAnswerWithCoT(composeAnchored(q, anchors), cot)
}

// MultiHop is Anchored with seeds and remembered expansion neighbours
// concatenated into a single anchor list.
func (r routerRetriever) MultiHop(_ context.Context, q string, seeds, expansion []string, cot bool) (string, error) {
	all := append(append([]string{}, seeds...), expansion...)
	return r.router.PromptOrAnswerWithCoT(composeAnchored(q, all), cot)
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
