// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"strings"
)

// ========== The retrieval cascade ==========
//
// Symbols, function names and file paths are "anchors": they drive exact
// lookup and graph expansion. Open-ended questions have no anchors to hold
// on to, so they rely on semantic and subsystem signals. The strategy picks
// the most precise approach the calling agent allows and degrades gracefully:
//
//	1. multi-hop      the agent already knows the project: it named anchors in
//	                  the query and/or supplied 'known_symbols' from its own
//	                  session context
//	2. open-ended     plain semantic retrieval
//	3. anchored       re-query using anchors derived from what step 2 returned
//	4. lexical        grep-style text search over the repository(Different
//                      from grepSymbolSearch due to its limitations and working)
//
// After every stage the result is scored (see strategy_quality.go). The first
// stage that clears StrategyConfig.MinQuality wins; if none does, the
// best-scoring candidate is returned anyway, with a note explaining what
// happened.

// retrieveLocked runs the cascade.
//
// The caller must hold s.mu and must have a non-nil router, unless a test
// Retriever has been installed on the server.
//
// known holds symbols/files the calling agent says it already knows from its
// own session context. Together with the anchors named in the query, they are
// the signal that the agent has project context — which is what selects the
// multi-hop path.
func (s *Server) retrieveLocked(ctx context.Context, q string, known []string, cot bool) (string, RetrievalTrace, error) {
	cfg := s.strategy
	retr := s.retriever
	if retr == nil {
		retr = routerRetriever{router: s.router}
	}

	mem := s.memoryLocked()
	anchors := ExtractAnchors(q)      // named in the query: used for scoring and evidence
	hinted := anchorsFromHints(known) // from the agent's session: extra seeds only
	all := append(append([]Anchor{}, anchors...), hinted...)
	seeds := dedupeStrings(anchorTexts(all))
	agentKnows := len(seeds) > 0

	trace := RetrievalTrace{Anchors: seeds}
	var best *candidate
	var lastErr error

	// try records the outcome of one stage and reports whether the result
	// cleared cfg.MinQuality. echoes are strings the backend may have copied
	// verbatim from the rewritten query (e.g. a CoT header); assess strips
	// them so they cannot inflate coverage scores.
	try := func(stage Stage, text string, err error, echoes ...string) bool {
		att := StageAttempt{Stage: stage}
		switch {
		case err != nil:
			att.Err = err
			lastErr = err
			trace.Attempts = append(trace.Attempts, att)
			return false
		case strings.TrimSpace(text) == "":
			att.Note = "empty"
			trace.Attempts = append(trace.Attempts, att)
			return false
		}
		qual := assess(q, anchors, text, echoes...)
		att.Score = qual.Score
		trace.Attempts = append(trace.Attempts, att)
		if best == nil || qual.Score > best.score {
			best = &candidate{stage: stage, text: text, score: qual.Score}
		}
		return qual.Score >= cfg.MinQuality
	}

	// finish converts the winning candidate (if any) into the return value.
	// Project memory only learns from results we trust, i.e. those that
	// cleared the quality bar.
	finish := func() (string, RetrievalTrace, error) {
		if best == nil {
			if lastErr != nil {
				return "", trace, lastErr
			}
			return noContextMessage, trace, nil
		}
		trace.Chosen = best.stage
		if best.score >= cfg.MinQuality {
			mem.Observe(best.text)
		}
		text := best.text
		if best.stage == StageLexical {
			text = lexicalPreface + text
		}
		return text, trace, nil
	}

	// multi-hop. Only attempted when the agent has shown it knows
	// the project. Server-side memory only enriches the hops with remembered
	// neighbours; it never decides the strategy on its own.
	if agentKnows {
		expansion := mem.expand(all, cfg.MaxExpansion)
		text, err := retr.MultiHop(ctx, q, seeds, expansion, cot)
		full := composeAnchored(q, append(append([]string{}, seeds...), expansion...))
		if try(StageMultiHop, text, err, full, q) {
			return finish()
		}
	}
	if err := ctx.Err(); err != nil {
		return "", trace, err
	}

	// open-ended semantic retrieval.
	openText, err := retr.OpenEnded(ctx, q, cot)
	if try(StageOpenEnded, openText, err, q) {
		return finish()
	}
	if err := ctx.Err(); err != nil {
		return "", trace, err
	}

	// anchored re-query, seeded with what stage 2 taught us about
	// the query. This is where a vague question becomes concrete.
	derived := mem.derive(q, openText, cfg.MaxDerivedAnchors)
	anchoredSeeds := dedupeStrings(append(append([]string{}, seeds...), derived...))
	if len(anchoredSeeds) > 0 {
		text, err := retr.Anchored(ctx, q, anchoredSeeds, cot)
		if try(StageAnchoredFromOpenEnded, text, err, composeAnchored(q, anchoredSeeds), q) {
			return finish()
		}
		if err := ctx.Err(); err != nil {
			return "", trace, err
		}
	}

	// lexical (grep-style) fallback. Runs under its own timeout so
	// a large repository cannot stall the whole request.
	lctx, cancel := context.WithTimeout(ctx, cfg.LexicalTimeout)
	text, err := lexicalSearch(lctx, s.activeRepo, q, all, cfg)
	cancel()
	if cerr := ctx.Err(); cerr != nil {
		return "", trace, cerr
	}
	try(StageLexical, text, err)

	return finish()
}

// memoryLocked returns the projectMemory for the active repository, lazily
// creating it. Caller must hold s.mu. The memory is small and survives the
// idle release of the ContextBuilder.
func (s *Server) memoryLocked() *projectMemory {
	if s.memories == nil {
		s.memories = make(map[string]*projectMemory)
	}
	m, ok := s.memories[s.activeRepo]
	if !ok {
		m = newProjectMemory(s.strategy.MaxMemorySymbols)
		s.memories[s.activeRepo] = m
	}
	return m
}
