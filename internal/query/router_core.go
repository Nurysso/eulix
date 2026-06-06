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

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/llm"
)

func QueryTrafficController(eulixDir string, cfg *config.Config, llmClient *llm.Client, cacheManager *cache.Manager) (*Router, error) {
	kbIndex, err := loadKBIndex(eulixDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load KB index: %w", err)
	}

	// in QueryTrafficController, after loading kbIndex:
	kb, err := loadKBFull(eulixDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load KB index: %w", err)
	}

	callGraph, err := loadCallGraph(eulixDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load call graph: %w", err)
	}

	kbIndexPath := filepath.Join(eulixDir, "kb_index.json")
	classifier, err := QuerySheriff(kbIndexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create classifier: %w", err)
	}

	return &Router{
		eulixDir:       eulixDir,
		config:         cfg,
		classifier:     classifier,
		llmClient:      llmClient,
		cache:          cacheManager,
		contextBuilder: nil,
		kbIndex:        kbIndex,
		callGraph:      callGraph,
		kb:             kb,
	}, nil
}

//  Main dispatch ─

func (r *Router) QueryEngine(query string) (string, error) {
	if r.cache != nil && r.currentChecksum != "" {
		if cached, found, err := r.cache.Get(query, r.currentChecksum); err == nil && found {
			return cached, nil
		}
	}

	classification := r.classifier.Classify(query)

	var (
		response string
		err      error
	)

	switch classification.Type {
	case QueryTypeLocation:
		response, err = r.handleLocation(query, classification)
	case QueryTypeUsage:
		response, err = r.handleUsage(query, classification)
	case QueryTypeUnderstanding:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	case QueryTypeImplementation:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleImplementation(query, classification)
	case QueryTypeArchitecture:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleArchitecture(query, classification)
	case QueryTypeDebug:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDebug(query, classification)
	case QueryTypeComparison:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleComparison(query, classification)
	case QueryTypeDependency:
		response, err = r.handleDependency(query, classification)
	case QueryTypeRefactoring:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleRefactoring(query, classification)
	case QueryTypePerformance:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handlePerformance(query, classification)
	case QueryTypeDataFlow:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDataFlow(query, classification)
	case QueryTypeSecurity:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleSecurity(query, classification)
	case QueryTypeDocumentation:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDocumentation(query, classification)
	case QueryTypeExample:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleExample(query, classification)
	case QueryTypeCodeGeneration:
		return r.handleCodeGeneration()
	case QueryTypeTesting:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleTesting(query, classification)
	case QueryTypeCallGraph:
		response, err = r.handleCallGraph(query, classification)
	case QueryTypeEntryPoints:
		response, err = r.handleEntryPoints(query, classification)
	case QueryTypeFileStructure:
		response, err = r.handleFileStructure(query, classification)
	case QueryTypeTodos:
		response, err = r.handleTodosQuery(query, classification)
	case QueryTypeMetrics:
		response, err = r.handleMetrics(query, classification)
	default:
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	}

	if err != nil {
		return "", err
	}

	if r.cache != nil && r.currentChecksum != "" {
		_ = r.cache.Set(query, response, r.currentChecksum)
	}

	return response, nil
}
