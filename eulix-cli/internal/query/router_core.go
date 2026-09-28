//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query manages query routing for EULIX.

/*
Package query implements query routing, intent classification, and LLM prompt assembly for Eulix.
Key Components:
  - Router: Top-level dispatcher managing KB indexes, call graphs, and response caches
  - ContextBuilder: moved to retrieval sub-package
  - Classifier: moved to classifier sub-package
*/

package query

import (
	"fmt"
	"path/filepath"
	"sync"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/llm"
	"eulix/internal/query/classifier"
	"eulix/internal/query/retrieval"
	"eulix/internal/query/retrieval/mmap"
	"eulix/internal/utils"
)

type Router struct {
	eulixDir        string
	config          *config.Config
	classifier      *classifier.Classifier
	llmClient       *llm.Client
	cache           *cache.Manager
	contextBuilder  *retrieval.ContextBuilder
	kbIndex         *utils.KBIndices
	callGraph       *callGraph
	kb              *utils.KnowledgeBaseRef
	Patterns        *utils.PatternInfo
	cgIdx           *callGraphIndex
	cgBuild         *retrieval.CallGraphIdx
	currentChecksum string
	debug           *utils.DebugLogger
}

type callGraphIndex struct {
	mu    sync.RWMutex
	cache map[string]string // entity → pre-rendered two-level tree string
}
type callGraph struct {
	Functions map[string]cgFunction
}

type cgFunction struct {
	Location string
	Calls    []string
	CalledBy []string
}

func QueryTrafficController(
	eulixDir string,
	cfg *config.Config,
	llmClient *llm.Client,
	cacheManager *cache.Manager,
) (*Router, error) {
	cb, err := retrieval.ContextWindowCreator(eulixDir, cfg, llmClient, cfg.Project.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize context builder: %w", err)
	}
	classifier, err := classifier.QuerySheriff(filepath.Join(eulixDir, "kb_index.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to create classifier: %w", err)
	}
	debugLogger := utils.NewDebugLogger(eulixDir)

	// Flush stored init pretouch results into context_debug.log immediately
	mmap.FlushPretouchLogs(debugLogger)
	return &Router{
		eulixDir:       eulixDir,
		config:         cfg,
		classifier:     classifier,
		llmClient:      llmClient,
		cache:          cacheManager,
		contextBuilder: cb,
		kbIndex:        cb.GetKBIndex(),
		callGraph:      buildRouterCallGraph(cb.GetCallGraphRef()),
		cgIdx:          &callGraphIndex{cache: make(map[string]string)},
		cgBuild:        BuildCallGraphIndex(cb.GetCallGraphRef()),
		debug:          debugLogger,
	}, nil
}

func (r *Router) PromptOrAnswer(query string) (string, error) {
	classification := r.classifier.Classify(query)
	r.debug.Log("[ROUTE] PromptOrAnswer: query=%q type=%v", query, classification.Type)

	// Non‑LLM queries – return direct answer
	switch classification.Type {
	case classifier.QueryTypeLocation:
		return r.handleLocation(query, classification)
	case classifier.QueryTypeUsage:
		return r.handleUsage(query, classification)
	case classifier.QueryTypeDependency:
		return r.handleDependency(query, classification)
	case classifier.QueryTypeCallGraph:
		return r.handleCallGraph(query, classification)
	case classifier.QueryTypeEntryPoints:
		return r.handleEntryPoints(query, classification)
	case classifier.QueryTypeFileStructure:
		return r.handleFileStructure(query)
	case classifier.QueryTypeTodos:
		return r.handleTodosQuery(query, classification)
	case classifier.QueryTypeMetrics:
		return r.handleMetrics(query, classification)
	case classifier.QueryTypeCodeGeneration:
		return r.handleCodeGeneration()
	}

	// LLM queries build context + return the full prompt
	if err := r.ensureContextBuilder(); err != nil {
		return "", err
	}

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}

	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, classification)
	realCodeBudgetRatio := r.config.RetrievalConfig.CodeToAstRatio
	// Build the complete prompt with context and CoT
	contextPrompt := r.llmClient.BuildFullPrompt(ctx, query)
	cotPrompt := BuildPromptString(query, classification, src, taskBody, realCodeBudgetRatio)

	// Combine context inventory + CoT prompt
	fullPrompt := contextPrompt + "\n\n" + cotPrompt
	return fullPrompt, nil
}

func (r *Router) QueryEngine(query string) (string, error) {
	classification := r.classifier.Classify(query)
	r.debug.Log("[ROUTE] QueryEngine: query=%q type=%v", query, classification.Type)

	var (
		response string
		err      error
	)

	switch classification.Type {
	case classifier.QueryTypeLocation:
		r.debug.Log("[HANDLER] handleLocation")
		response, err = r.handleLocation(query, classification)
	case classifier.QueryTypeUsage:
		r.debug.Log("[HANDLER] handleUsage")
		response, err = r.handleUsage(query, classification)
	case classifier.QueryTypeUnderstanding:
		r.debug.Log("[HANDLER] handleUnderstanding")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	case classifier.QueryTypeImplementation:
		r.debug.Log("[HANDLER] handleImplementation")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleImplementation(query, classification)
	case classifier.QueryTypeArchitecture:
		r.debug.Log("[HANDLER] handleArchitecture")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleArchitecture(query, classification)
	case classifier.QueryTypeDebug:
		r.debug.Log("[HANDLER] handleDebug")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDebug(query, classification)
	case classifier.QueryTypeComparison:
		r.debug.Log("[HANDLER] handleComparison")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleComparison(query, classification)
	case classifier.QueryTypeDependency:
		r.debug.Log("[HANDLER] handleDependency")
		response, err = r.handleDependency(query, classification)
	case classifier.QueryTypeRefactoring:
		r.debug.Log("[HANDLER] handleRefactoring")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleRefactoring(query, classification)
	case classifier.QueryTypePerformance:
		r.debug.Log("[HANDLER] handlePerformance")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handlePerformance(query, classification)
	case classifier.QueryTypeDataFlow:
		r.debug.Log("[HANDLER] handleDataFlow")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDataFlow(query, classification)
	case classifier.QueryTypeSecurity:
		r.debug.Log("[HANDLER] handleSecurity")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleSecurity(query, classification)
	case classifier.QueryTypeDocumentation:
		r.debug.Log("[HANDLER] handleDocumentation")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleDocumentation(query, classification)
	case classifier.QueryTypeExample:
		r.debug.Log("[HANDLER] handleExample")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleExample(query, classification)
	case classifier.QueryTypeCodeGeneration:
		r.debug.Log("[HANDLER] handleCodeGeneration")
		return r.handleCodeGeneration()
	case classifier.QueryTypeTesting:
		r.debug.Log("[HANDLER] handleTesting")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleTesting(query, classification)
	case classifier.QueryTypeCallGraph:
		r.debug.Log("[HANDLER] handleCallGraph")
		response, err = r.handleCallGraph(query, classification)
	case classifier.QueryTypeEntryPoints:
		r.debug.Log("[HANDLER] handleEntryPoints")
		response, err = r.handleEntryPoints(query, classification)
	case classifier.QueryTypeFileStructure:
		r.debug.Log("[HANDLER] handleFileStructure")
		response, err = r.handleFileStructure(query)
	case classifier.QueryTypeTodos:
		r.debug.Log("[HANDLER] handleTodosQuery")
		response, err = r.handleTodosQuery(query, classification)
	case classifier.QueryTypeMetrics:
		r.debug.Log("[HANDLER] handleMetrics")
		response, err = r.handleMetrics(query, classification)
	default:
		r.debug.Log("[HANDLER] handleUnderstanding (default)")
		if err = r.ensureContextBuilder(); err != nil {
			return "", err
		}
		response, err = r.handleUnderstanding(query, classification)
	}

	if err != nil {
		r.debug.Log("[ROUTE] QueryEngine: handler error: %v", err)
		return "", err
	}

	if r.cache != nil {
		reasoning, answer := utils.SplitReasoningAndAnswer(response)
		if _, saveErr := r.cache.Save(query, reasoning, answer); saveErr != nil {
			r.debug.Log("[ROUTE] QueryEngine: failed to save history entry: %v", saveErr)
		}
	}

	return response, nil
}
