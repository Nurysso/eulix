// Copyright 2025-2026 Dawood K. (Nurysso)
// SPDX-License-Identifier: Apache-2.0

// Package mcp contains the MCP server surface for the Eulix code-intelligence
// backend.
//
// The adaptive retrieval strategy used by the search tools is split across
// several files so each concern stays small and reviewable:
//
//	strategy.go              overview, configuration, shared result/trace types
//	strategy_orchestrator.go the cascade itself (retrieveLocked)
//	strategy_retriever.go    Retriever abstraction over the query router
//	strategy_anchors.go      extraction of anchors (symbols, paths, calls)
//	strategy_memory.go       per-repository symbol memory used for multi-hop
//	strategy_quality.go      scoring a result against a query
//	strategy_lexical.go      grep-style lexical fallback
package mcp

import (
	"fmt"
	"strings"
	"time"
)

// ========== Retrieval stages ==========

// Stage identifies a single step of the adaptive retrieval cascade.
type Stage string

const (
	// StageMultiHop is the most informed path: the calling agent already
	// knows the project (anchors appear in the query and/or 'known_symbols'
	// was supplied from the agent's own session), so the router can follow
	// symbol-graph neighbours directly.
	StageMultiHop Stage = "multi-hop"

	// StageOpenEnded is plain semantic retrieval with no explicit anchors.
	StageOpenEnded Stage = "open-ended"

	// StageAnchoredFromOpenEnded re-queries the router using anchors derived
	// from the open-ended result, so exact lookup gets a second chance once
	// the query is grounded.
	StageAnchoredFromOpenEnded Stage = "anchored-from-open-ended"

	// StageLexical is the grep-style text-search fallback. It is never
	// trusted to update project memory.
	StageLexical Stage = "lexical"
)

// ========== Strategy configuration ==========

// StrategyConfig tunes the adaptive retrieval cascade. A zero value is valid:
// every field falls back to the DefaultStrategyConfig counterpart through
// normalized().
type StrategyConfig struct {
	// MinQuality is the minimum score (0..1) a stage result must reach for
	// the cascade to stop. Higher values force more stages to run.
	MinQuality float64

	// MaxExpansion caps how many remembered neighbour symbols are appended
	// to a multi-hop query as graph-expansion seeds.
	MaxExpansion int

	// MaxDerivedAnchors caps how many anchors are derived from an open-ended
	// result to seed the follow-up anchored stage.
	MaxDerivedAnchors int

	// MaxMemorySymbols caps the per-repository symbol memory (see
	// projectMemory in strategy_memory.go).
	MaxMemorySymbols int

	// LexicalTimeout bounds the repository walk in the lexical fallback. The
	// walk is context-aware and returns partial results on timeout.
	LexicalTimeout time.Duration

	// LexicalMaxFiles caps how many files are rendered in a lexical result.
	LexicalMaxFiles int

	// LexicalExclude lists paths the lexical walk skips. Set per query from
	// [retrievalConfig.etiya] lexical_exclude, and only under etiya.
	LexicalExclude []string

	// LexicalMaxBytes is an approximate byte cap for the rendered lexical
	// result, so it cannot blow up the model's context window.
	LexicalMaxBytes int
}

// DefaultStrategyConfig returns the tuned defaults used when a field is left
// at its zero value.
func DefaultStrategyConfig() StrategyConfig {
	return StrategyConfig{
		MinQuality:        0.55,
		MaxExpansion:      8,
		MaxDerivedAnchors: 8,
		MaxMemorySymbols:  4000,
		LexicalTimeout:    10 * time.Second,
		LexicalMaxFiles:   8,
		LexicalMaxBytes:   24_000,
	}
}

// normalized fills every zero field with its default, so callers can rely on
// the returned value being fully populated.
func (c StrategyConfig) normalized() StrategyConfig {
	d := DefaultStrategyConfig()
	if c.MinQuality <= 0 {
		c.MinQuality = d.MinQuality
	}
	if c.MaxExpansion <= 0 {
		c.MaxExpansion = d.MaxExpansion
	}
	if c.MaxDerivedAnchors <= 0 {
		c.MaxDerivedAnchors = d.MaxDerivedAnchors
	}
	if c.MaxMemorySymbols <= 0 {
		c.MaxMemorySymbols = d.MaxMemorySymbols
	}
	if c.LexicalTimeout <= 0 {
		c.LexicalTimeout = d.LexicalTimeout
	}
	if c.LexicalMaxFiles <= 0 {
		c.LexicalMaxFiles = d.LexicalMaxFiles
	}
	if c.LexicalMaxBytes <= 0 {
		c.LexicalMaxBytes = d.LexicalMaxBytes
	}
	return c
}

// ========== Result reporting ==========

const (
	// noContextMessage is returned when every stage fails to produce a
	// usable result. It tells the agent how to make the next attempt
	// succeed instead of leaving it with a bare "not found".
	noContextMessage = "No relevant context was found for this query. Try again with exact symbol names, " +
		"function names, or file paths (for example `Class.method()` or `foo/bar.go`), or set the " +
		"repository with 'set_repo_path'."

	// lexicalPreface prefixes every lexical result so the agent understands
	// that the text below is grep output, not a semantic answer.
	lexicalPreface = "> Semantic retrieval was low-confidence for this query, so these are lexical " +
		"(text-search) matches from the repository.\n\n"
)

// StageAttempt records what one stage of the cascade did: its score on
// success, or the error/note that explains why it produced nothing.
type StageAttempt struct {
	Stage Stage
	Score float64
	Err   error
	Note  string
}

// RetrievalTrace explains how a retrieval result was obtained. It is attached
// to logs and the debug file so a bad answer can be traced back to the stage
// that produced it.
type RetrievalTrace struct {
	// Anchors are the symbols/paths the cascade treated as evidence, in the
	// order they were discovered. They are named by the query and, for
	// multi-hop, may include seeds from the agent's session.
	Anchors []string

	// Attempts is every stage that ran, in order.
	Attempts []StageAttempt

	// Chosen is the stage whose result was returned, empty if none passed.
	Chosen Stage
}

// String renders the trace as a single human-readable line.
func (t RetrievalTrace) String() string {
	var parts []string
	for _, a := range t.Attempts {
		switch {
		case a.Err != nil:
			parts = append(parts, fmt.Sprintf("%s(error: %v)", a.Stage, a.Err))
		case a.Note != "":
			parts = append(parts, fmt.Sprintf("%s(%s)", a.Stage, a.Note))
		default:
			parts = append(parts, fmt.Sprintf("%s(%.2f)", a.Stage, a.Score))
		}
	}
	return fmt.Sprintf("anchors=%v path=[%s] chosen=%s", t.Anchors, strings.Join(parts, " -> "), t.Chosen)
}

// candidate is one stage result kept while the cascade is running.
type candidate struct {
	stage Stage
	text  string
	score float64
}
