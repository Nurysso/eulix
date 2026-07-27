//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing and retrival for EULIX.

/*
       Package query implements the query routing, classification, and LLM prompt
       pipeline for eulix. Incoming natural-language questions are classified by
       QuerySheriff, dispatched to a type-specific handler, and answered using a
       mix of real source code (≈65%) and AST metadata retrieved by ContextBuilder.

       Key types

               Router                  — top-level dispatcher; holds KB index, call
graph, cache
               ContextBuilder  — assembles the context window for LLM calls
               Classifier              — maps a query string to a QueryType + confidence score

       Prompt construction follows a chain-of-thought pattern: every LLM prompt is
       composed of a shared header (cotHeader), a handler-specific reasoning body,
       and a shared footer (cotFooter). Handlers label each claim as one of:
               CONFIRMED IN SOURCE / INFERRED FROM SIGNATURE / CANNOT DETERMINE
*/

package query

import (
	"fmt"
	"path/filepath"

	"corvux/internal/cache"
	"corvux/internal/config"
	"corvux/internal/llm"
)

func QueryTrafficController(
	eulixDir string,
	cfg *config.Config,
	llmClient *llm.Client,
	cacheManager *cache.Manager,
) (*Router, error) {
	cb, err := ContextWindowCreator(eulixDir, cfg, llmClient, cfg.Project.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize context builder: %w", err)
	}
	classifier, err := QuerySheriff(filepath.Join(eulixDir, "kb_index.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to create classifier: %w", err)
	}
	return &Router{
		eulixDir:       eulixDir,
		config:         cfg,
		classifier:     classifier,
		llmClient:      llmClient,
		cache:          cacheManager,
		contextBuilder: cb,
		kbIndex:        cb.kbIdx,
		callGraph:      buildRouterCallGraph(cb.cgRef),
		cgIdx:          &callGraphIndex{cache: make(map[string]string)},
		cgBuild:        BuildCallGraphIndex(cb.cgRef),
	}, nil
}

func (r *Router) PromptOrAnswer(query string) (string, error) {
	classification := r.classifier.Classify(query)
	r.contextBuilder.debugLog.Log("[ROUTE] PromptOrAnswer: query=%q type=%v", query, classification.Type)

	// ── Non‑LLM queries – return direct answer ──
	switch classification.Type {
	case QueryTypeLocation:
		return r.handleLocation(query, classification)
	case QueryTypeUsage:
		return r.handleUsage(query, classification)
	case QueryTypeDependency:
		return r.handleDependency(query, classification)
	case QueryTypeCallGraph:
		return r.handleCallGraph(query, classification)
	case QueryTypeEntryPoints:
		return r.handleEntryPoints(query, classification)
	case QueryTypeFileStructure:
		return r.handleFileStructure(query)
	case QueryTypeTodos:
		return r.handleTodosQuery(query, classification)
	case QueryTypeMetrics:
		return r.handleMetrics(query, classification)
	case QueryTypeCodeGeneration:
		return r.handleCodeGeneration()
	}

	// LLM queries – build context + return the full prompt
	if err := r.ensureContextBuilder(); err != nil {
		return "", err
	}

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, classification)

	// Build the complete prompt with context and CoT
	contextPrompt := r.llmClient.BuildFullPrompt(ctx, query)
	cotPrompt := BuildPromptString(query, classification, src, taskBody)

	// Combine context inventory + CoT prompt
	fullPrompt := contextPrompt + "\n\n" + cotPrompt
	return fullPrompt, nil
}

// Main dispatch

func (r *Router) QueryEngine(query string) (string, error) {
	if r.cache != nil && r.currentChecksum != "" {
		if cached, found, err := r.cache.Get(query, r.currentChecksum); err == nil && found {
			r.contextBuilder.debugLog.Log("[ROUTE] QueryEngine: cache hit for query=%q", query)
			return cached, nil
		}
	}

	classification := r.classifier.Classify(query)
	r.contextBuilder.debugLog.Log("[ROUTE] QueryEngine: query=%q type=%v", query, classification.Type)

	var (
		response string
		err      error
	)

	switch classification.Type {
	case QueryTypeLocation:
		r.contextBuilder.debugLog.Log("[HANDLER] handleLocation")
		response, err = r.handleLocation(query, classification)
	case QueryTypeUsage:
		r.contextBuilder.debugLog.Log("[HANDLER] handleUsage")
		response, err = r.handleUsage(query, classification)
	case QueryTypeUnderstanding:
		r.contextBuilder.debugLog.Log("[HANDLER] handleUnderstanding")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	case QueryTypeImplementation:
		r.contextBuilder.debugLog.Log("[HANDLER] handleImplementation")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleImplementation(query, classification)
	case QueryTypeArchitecture:
		r.contextBuilder.debugLog.Log("[HANDLER] handleArchitecture")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleArchitecture(query, classification)
	case QueryTypeDebug:
		r.contextBuilder.debugLog.Log("[HANDLER] handleDebug")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDebug(query, classification)
	case QueryTypeComparison:
		r.contextBuilder.debugLog.Log("[HANDLER] handleComparison")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleComparison(query, classification)
	case QueryTypeDependency:
		r.contextBuilder.debugLog.Log("[HANDLER] handleDependency")
		response, err = r.handleDependency(query, classification)
	case QueryTypeRefactoring:
		r.contextBuilder.debugLog.Log("[HANDLER] handleRefactoring")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleRefactoring(query, classification)
	case QueryTypePerformance:
		r.contextBuilder.debugLog.Log("[HANDLER] handlePerformance")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handlePerformance(query, classification)
	case QueryTypeDataFlow:
		r.contextBuilder.debugLog.Log("[HANDLER] handleDataFlow")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDataFlow(query, classification)
	case QueryTypeSecurity:
		r.contextBuilder.debugLog.Log("[HANDLER] handleSecurity")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleSecurity(query, classification)
	case QueryTypeDocumentation:
		r.contextBuilder.debugLog.Log("[HANDLER] handleDocumentation")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDocumentation(query, classification)
	case QueryTypeExample:
		r.contextBuilder.debugLog.Log("[HANDLER] handleExample")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleExample(query, classification)
	case QueryTypeCodeGeneration:
		r.contextBuilder.debugLog.Log("[HANDLER] handleCodeGeneration")
		return r.handleCodeGeneration()
	case QueryTypeTesting:
		r.contextBuilder.debugLog.Log("[HANDLER] handleTesting")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleTesting(query, classification)
	case QueryTypeCallGraph:
		r.contextBuilder.debugLog.Log("[HANDLER] handleCallGraph")
		response, err = r.handleCallGraph(query, classification)
	case QueryTypeEntryPoints:
		r.contextBuilder.debugLog.Log("[HANDLER] handleEntryPoints")
		response, err = r.handleEntryPoints(query, classification)
	case QueryTypeFileStructure:
		r.contextBuilder.debugLog.Log("[HANDLER] handleFileStructure")
		response, err = r.handleFileStructure(query)
	case QueryTypeTodos:
		r.contextBuilder.debugLog.Log("[HANDLER] handleTodosQuery")
		response, err = r.handleTodosQuery(query, classification)
	case QueryTypeMetrics:
		r.contextBuilder.debugLog.Log("[HANDLER] handleMetrics")
		response, err = r.handleMetrics(query, classification)
	default:
		r.contextBuilder.debugLog.Log("[HANDLER] handleUnderstanding (default)")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	}

	if err != nil {
		r.contextBuilder.debugLog.Log("[ROUTE] QueryEngine: handler error: %v", err)
		return "", err
	}

	if r.cache != nil && r.currentChecksum != "" {
		_ = r.cache.Set(query, response, r.currentChecksum)
	}

	return response, nil
}
