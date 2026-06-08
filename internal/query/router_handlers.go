//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides query classification functionality.

/*
This file is responsible for handleing/Generating Prompts with CoT.
*/
package query

import (
	"fmt"
	"strings"

	"eulix/internal/types"
)

// BuildPromptString composes header + task-specific body + footer.
func BuildPromptString(query string, class *Classification, sourceAvailable bool, taskBody string) string {
	return cotHeader(query, class, sourceAvailable) +
		"=== TASK ===\n" +
		taskBody +
		cotFooter()
}

// cotHeader builds the universal reasoning preamble injected into every prompt.
// It instructs the model to separate "what I can see" from "what I infer",
// and to distinguish source code chunks from metadata-only chunks.
func cotHeader(query string, class *Classification, sourceAvailable bool) string {
	var b strings.Builder
	if sourceAvailable {
		b.WriteString("You have been given a mix of REAL SOURCE CODE (≈65%) and AST metadata\n")
		b.WriteString("(file paths, line ranges, signatures, call edges) for the remaining symbols.\n")
		b.WriteString("Treat source blocks as ground truth. Treat metadata as structural hints only.\n")
	} else {
		b.WriteString("You have AST metadata ONLY (file paths, line ranges, signatures, call edges).\n")
		b.WriteString("No source code is available. Do not invent implementation details.\n")
	}

	if len(class.Symbols) > 0 {
		b.WriteString(fmt.Sprintf("Relevant symbols : %v\n", class.Symbols))
	}
	if len(class.Keywords) > 0 {
		b.WriteString(fmt.Sprintf("Key terms        : %v\n", class.Keywords))
	}
	b.WriteString(fmt.Sprintf("Query type       : %s  (confidence %.1f%%)\n", class.Type.String(), class.Confidence*100))
	b.WriteString(fmt.Sprintf("Question you need to anser is  %s\n\n", query))

	b.WriteString("=== CHAIN-OF-THOUGHT INSTRUCTIONS ===\n")
	b.WriteString("Before writing your final answer, reason through the following steps.\n")
	b.WriteString("Show your work inside <reasoning>…</reasoning> tags, then give the\n")
	b.WriteString("final answer inside <answer>…</answer> tags.\n\n")
	b.WriteString("Step 1 — INVENTORY\n")
	b.WriteString("  List every symbol, file, or function that is directly relevant to the question.\n")
	b.WriteString("  For each, note whether you have (a) full source, (b) signature+metadata only, or (c) call-edge only.\n\n")
	b.WriteString("Step 2 — EVIDENCE\n")
	b.WriteString("  Quote or cite the specific source lines / signatures that support each claim you plan to make.\n")
	b.WriteString("  If a symbol appears only in metadata, state: 'metadata only — lines X–Y of file Z'.\n\n")
	b.WriteString("Step 3 — GAPS\n")
	b.WriteString("  Explicitly list what you CANNOT determine from the available context.\n")
	b.WriteString("  Do NOT paper over gaps with plausible-sounding guesses.\n\n")
	b.WriteString("Step 4 — ANSWER\n")
	b.WriteString("  Write a clear, structured answer grounded solely in Step 2 evidence.\n")
	b.WriteString("  Prefix uncertain claims with 'Likely…' or 'The signature suggests…'.\n")
	b.WriteString("  Never invent function names, variable values, or logic.\n\n")

	return b.String()
}

// cotFooter appends a honesty reminder at the end of every prompt.
func cotFooter() string {
	return "\n=== HONESTY CONTRACT ===\n" +
		"• Cite file + line range for every factual claim.\n" +
		"• Write 'Not visible in context' rather than guessing.\n" +
		"• Distinguish 'I see in source' from 'I infer from signature'.\n"
}

func (r *Router) handleCodeGeneration() (string, error) {
	return `I have semantic information (function signatures, types, call graphs) but not full source code for every symbol, so I cannot safely generate implementation code — I would likely hallucinate logic.

What I CAN help with instead:
• Showing which functions/classes are involved and their signatures
• Explaining the call flow and structural relationships
• Identifying the relevant modules and their roles

Would you like an architecture or data-flow explanation instead?`, nil
}

func (r *Router) handleUnderstanding(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleImplementation(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleArchitecture(query string, class *Classification) (string, error) {

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleDebug(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleComparison(query string, class *Classification) (string, error) {
	if len(class.Symbols) < 2 {
		return "Please specify at least two symbols to compare (e.g., 'compare FooHandler and BarHandler').", nil
	}

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleDependency(query string, class *Classification) (string, error) {
	entity := firstSymbolOrExtracted(class, query)
	if entity == "" {
		return "Could not identify an entity for dependency analysis.", nil
	}

	var results []string
	results = append(results, fmt.Sprintf("Dependency analysis for '%s':", entity))

	if fn, ok := r.callGraph.Functions[entity]; ok {
		if len(fn.Calls) > 0 {
			results = append(results, "\nDirect dependencies (calls):")
			for _, d := range fn.Calls {
				results = append(results, fmt.Sprintf("  → %s", d))
			}
		}
		if len(fn.CalledBy) > 0 {
			results = append(results, "\nDependents (called by):")
			for _, c := range fn.CalledBy {
				results = append(results, fmt.Sprintf("  ← %s", c))
			}
		}
		transitive := r.findTransitiveDependencies(entity, 2)
		if len(transitive) > 0 {
			results = append(results, "\nTransitive dependencies (depth ≤ 2):")
			for _, d := range transitive {
				results = append(results, fmt.Sprintf("  ⇒ %s", d))
			}
		}
	} else {
		results = append(results, "\nNo call-graph entry found for this symbol.")
	}

	return strings.Join(results, "\n"), nil
}

func (r *Router) handleRefactoring(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handlePerformance(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleDataFlow(query string, class *Classification) (string, error) {
	// Pre-compute the call-chain type trace for symbols in the query
	var chainInfo strings.Builder
	for _, sym := range class.Symbols {
		if fn, ok := r.callGraph.Functions[sym]; ok {
			chainInfo.WriteString(fmt.Sprintf("\n%s → %v", sym, fn.Calls))
		}
	}

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleSecurity(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleDocumentation(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleExample(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleTesting(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)
	fullPrompt := BuildPromptString(query, class, src, taskBody)
	return r.llmClient.LlmResponse(fullPrompt)
}

// NON LLM BASED HANDLERS

// handleCallGraph — renders the two-level call tree with a per-symbol cache.
// First call for a symbol: O(callers + callees + their neighbours).
// Subsequent calls: O(1) map lookup + string copy.
func (r *Router) handleCallGraph(query string, class *Classification) (string, error) {
	entity := firstSymbolOrExtracted(class, query)
	if entity == "" {
		return "Could not identify a symbol for call graph analysis.", nil
	}

	fn, ok := r.callGraph.Functions[entity]
	if !ok {
		if matches := r.fuzzySearch(entity); len(matches) > 0 {
			return fmt.Sprintf("'%s' not found. Did you mean: %s", entity, strings.Join(matches, ", ")), nil
		}
		return fmt.Sprintf("'%s' not found in call graph.", entity), nil
	}

	// Fast path: return cached render
	r.cgIdx.mu.RLock()
	if s, hit := r.cgIdx.cache[entity]; hit {
		r.cgIdx.mu.RUnlock()
		return s, nil
	}
	r.cgIdx.mu.RUnlock()

	// Slow path: build once
	// Pre-size the builder. Two-level tree for a high-fan-out node can be a few
	// KB; 2 KB is a reasonable starting allocation that avoids reallocs for
	// typical functions.
	b := strings.Builder{}
	b.Grow(2048)

	b.WriteString(fmt.Sprintf("Call graph for '%s'  (%s)\n", entity, fn.Location))

	// Callers tree (inbound)
	b.WriteString("\n┌─ Called by (inbound):\n")
	if len(fn.CalledBy) == 0 {
		b.WriteString("│  (none — likely an entry point or exported API)\n")
	} else {
		for _, caller := range fn.CalledBy {
			fmt.Fprintf(&b, "│  ← %s\n", caller)
			if callerFn, ok := r.callGraph.Functions[caller]; ok {
				for _, grandCaller := range callerFn.CalledBy {
					fmt.Fprintf(&b, "│     ← %s\n", grandCaller)
				}
			}
		}
	}

	// Callees tree (outbound)
	b.WriteString("\n└─ Calls (outbound):\n")
	if len(fn.Calls) == 0 {
		b.WriteString("   (none — leaf function)\n")
	} else {
		for _, callee := range fn.Calls {
			fmt.Fprintf(&b, "   → %s\n", callee)
			if calleeFn, ok := r.callGraph.Functions[callee]; ok {
				for _, grandCallee := range calleeFn.Calls {
					fmt.Fprintf(&b, "      → %s\n", grandCallee)
				}
			}
		}
	}

	// Fan-in / fan-out summary
	fmt.Fprintf(&b, "\nFan-in (callers) : %d\n", len(fn.CalledBy))
	fmt.Fprintf(&b, "Fan-out (callees): %d\n", len(fn.Calls))
	if len(fn.CalledBy) == 0 {
		b.WriteString("Note: No callers detected — treat as entry point.\n")
	}
	if len(fn.Calls) > 7 {
		fmt.Fprintf(&b, "⚠ High fan-out (%d) — consider splitting responsibilities.\n", len(fn.Calls))
	}

	result := b.String()

	// Store in cache — upgrade to write lock
	r.cgIdx.mu.Lock()
	r.cgIdx.cache[entity] = result
	r.cgIdx.mu.Unlock()

	return result, nil
}

// handleMetrics — O(1) for project-wide summary and per-symbol lookup.
// No longer iterates kb.Structure on the hot path.
func (r *Router) handleMetrics(query string, class *Classification) (string, error) {
	if r.kb == nil {
		return "Full KB (kb.json) not loaded — metrics query requires it.", nil
	}

	// Check if this is a project-wide metrics query (no specific symbol)
	lowerQuery := strings.ToLower(query)
	isProjectMetrics := strings.Contains(lowerQuery, "project") ||
		strings.Contains(lowerQuery, "overall") ||
		strings.Contains(lowerQuery, "summary") ||
		strings.Contains(lowerQuery, "total") ||
		strings.Contains(lowerQuery, "all functions") ||
		// If no clear symbol detected, default to project metrics
		len(class.Symbols) == 0

	entity := firstSymbolOrExtracted(class, query)

	// Only try to lookup a specific symbol if we have one AND it's not a project metrics query
	if entity != "" && !isProjectMetrics {
		// O(1) lookup via pre-built index
		if e, ok := r.metricsIdx.byName[entity]; ok {
			return formatFunctionMetrics(*e.fn, e.file), nil
		}
		return fmt.Sprintf("'%s' not found in metrics index.", entity), nil
	}

	// Project-wide summary — all pre-computed, just format
	s := r.metricsIdx.summary
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Project metrics: %s\n\n", s.name))
	b.WriteString(fmt.Sprintf("  Files       : %d\n", s.files))
	b.WriteString(fmt.Sprintf("  Total LOC   : %d\n", s.loc))
	b.WriteString(fmt.Sprintf("  Functions   : %d\n", s.functions))
	b.WriteString(fmt.Sprintf("  Languages   : %s\n", s.languages))
	b.WriteString(fmt.Sprintf("  Parsed at   : %s\n\n", s.parsedAt))

	b.WriteString("Top 10 most complex functions:\n")
	for i, e := range r.metricsIdx.topComplex {
		b.WriteString(fmt.Sprintf("  %2d. %s  (%s, complexity %d, importance %.2f)\n",
			i+1, e.fn.Name, e.file, e.fn.Complexity, e.fn.ImportanceScore))
	}
	return b.String(), nil
}

func (r *Router) handleEntryPoints(_ string, _ *Classification) (string, error) {
	if r.kb == nil {
		// fall back to call graph: functions with no callers
		var b strings.Builder
		b.WriteString("Entry points (functions with no callers in call graph):\n\n")
		count := 0
		for name, fn := range r.callGraph.Functions {
			if len(fn.CalledBy) == 0 {
				b.WriteString(fmt.Sprintf("  • %s  @ %s\n", name, fn.Location))
				count++
			}
		}
		if count == 0 {
			b.WriteString("  (none found — all functions have at least one caller)\n")
		}
		return b.String(), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Entry points for %s:\n\n", r.kb.Metadata.ProjectName))

	byType := make(map[string][]types.EntryPoint)
	for _, ep := range r.metricsIdx.entryPoints {
		byType[ep.EntryType] = append(byType[ep.EntryType], ep)
	}

	for epType, eps := range byType {
		b.WriteString(fmt.Sprintf("── %s ──\n", strings.ToUpper(epType)))
		for _, ep := range eps {
			if ep.Path != nil {
				methods := strings.Join(ep.Methods, ", ")
				b.WriteString(fmt.Sprintf("  [%s] %s → %s  (%s:%d)\n", methods, *ep.Path, ep.Handler, ep.File, ep.Line))
			} else {
				b.WriteString(fmt.Sprintf("  %s  (%s:%d)\n", ep.Handler, ep.File, ep.Line))
			}
		}
		b.WriteString("\n")
	}

	if r.index.Patterns.ArchitectureStyle != nil {
		b.WriteString(fmt.Sprintf("Architecture style : %s\n", *r.index.Patterns.ArchitectureStyle))
	}

	return b.String(), nil
}

func (r *Router) handleFileStructure(query string) (string, error) {
	if r.kb == nil {
		return "Full KB (kb.json) not loaded — file structure query requires it.", nil
	}

	// Try to extract a filename from the query
	target := extractFilePath(query)
	if target == "" {
		// List all files
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Project: %s  (%d files, %d LOC)\n\n",
			r.kb.Metadata.ProjectName, r.kb.Metadata.TotalFiles, r.kb.Metadata.TotalLOC))
		for path, fd := range r.kb.Structure {
			b.WriteString(fmt.Sprintf("  %s  [%s, %d LOC, %d fns, %d classes]\n",
				path, fd.Language, fd.LOC, len(fd.Functions), len(fd.Classes)))
		}
		return b.String(), nil
	}

	// Find matching file (partial path match)
	for path, fd := range r.kb.Structure {
		if strings.Contains(path, target) {
			return formatFileData(path, &fd), nil
		}
	}
	return fmt.Sprintf("File matching '%s' not found in knowledge base.", target), nil
}

func (r *Router) handleTodosQuery(_ string, _ *Classification) (string, error) {
	if r.kb == nil {
		return "Full KB (kb.json) not loaded — TODO query requires it.", nil
	}

	var high, medium, low []todoItem
	var secNotes []struct {
		file, noteType, desc string
		line                 int
	}

	for path, fd := range r.kb.Structure {
		for _, td := range fd.Todos {
			item := todoItem{path, td.Line, td.Text, td.Priority}
			switch td.Priority {
			case "high":
				high = append(high, item)
			case "medium":
				medium = append(medium, item)
			default:
				low = append(low, item)
			}
		}
		for _, sn := range fd.SecurityNotes {
			secNotes = append(secNotes, struct {
				file, noteType, desc string
				line                 int
			}{path, sn.NoteType, sn.Description, sn.Line})
		}
	}

	var b strings.Builder
	if len(secNotes) > 0 {
		b.WriteString(fmt.Sprintf("⚠ Security notes (%d):\n", len(secNotes)))
		for _, sn := range secNotes {
			b.WriteString(fmt.Sprintf("  [%s] %s:%d — %s\n", sn.noteType, sn.file, sn.line, sn.desc))
		}
		b.WriteString("\n")
	}

	printTodos := func(label string, items []todoItem) {
		if len(items) == 0 {
			return
		}
		b.WriteString(fmt.Sprintf("%s (%d):\n", label, len(items)))
		for _, td := range items {
			b.WriteString(fmt.Sprintf("  %s:%d — %s\n", td.file, td.line, td.text))
		}
		b.WriteString("\n")
	}
	printTodos("🔴 High priority TODOs", high)
	printTodos("🟡 Medium priority TODOs", medium)
	printTodos("⚪ Low priority TODOs", low)

	if b.Len() == 0 {
		return "No TODOs or security notes found in the knowledge base.", nil
	}
	return b.String(), nil
}

func (r *Router) handleLocation(query string, class *Classification) (string, error) {
	entity := firstSymbolOrExtracted(class, query)
	if entity == "" {
		return "Could not identify a function or class name in the query.", nil
	}

	var results []string

	if locations, ok := r.kbIndex.FunctionsByName[entity]; ok {
		results = append(results, fmt.Sprintf("Function '%s' found at:", entity))
		results = append(results, locations...)
	}
	if locations, ok := r.kbIndex.TypesByName[entity]; ok {
		results = append(results, fmt.Sprintf("Type '%s' found at:", entity))
		results = append(results, locations...)
	}

	if len(results) == 0 {
		if matches := r.fuzzySearch(entity); len(matches) > 0 {
			results = append(results, fmt.Sprintf("No exact match for '%s'. Closest symbols:", entity))
			results = append(results, matches...)
		} else {
			return fmt.Sprintf("'%s' was not found in the knowledge base.", entity), nil
		}
	}

	return strings.Join(results, "\n"), nil
}

func (r *Router) handleUsage(query string, class *Classification) (string, error) {
	entity := firstSymbolOrExtracted(class, query)
	if entity == "" {
		return "Could not identify a function or class name in the query.", nil
	}

	var results []string

	if fn, ok := r.callGraph.Functions[entity]; ok {
		results = append(results, fmt.Sprintf("Usage analysis for '%s':", entity))
		results = append(results, fmt.Sprintf("  Location : %s", fn.Location))

		if len(fn.Calls) > 0 {
			results = append(results, "\nCalls (outbound):")
			for _, c := range fn.Calls {
				results = append(results, fmt.Sprintf("  → %s", c))
			}
		}
		if len(fn.CalledBy) > 0 {
			results = append(results, "\nCalled by (inbound):")
			for _, c := range fn.CalledBy {
				results = append(results, fmt.Sprintf("  ← %s", c))
			}
		} else {
			results = append(results, "\nNot called by any indexed function (entry point or unused).")
		}
	} else if t, ok := r.callGraph.Types[entity]; ok {
		results = append(results, fmt.Sprintf("Type analysis for '%s':", entity))
		results = append(results, fmt.Sprintf("  Location : %s", t.Location))
		if len(t.Methods) > 0 {
			results = append(results, "\nMethods:")
			for _, m := range t.Methods {
				results = append(results, fmt.Sprintf("  • %s", m))
			}
		}
	} else if callers, ok := r.kbIndex.FunctionsCalling[entity]; ok {
		results = append(results, fmt.Sprintf("Functions calling '%s':", entity))
		for _, c := range callers {
			results = append(results, fmt.Sprintf("  ← %s", c))
		}
	} else {
		return fmt.Sprintf("No usage information found for '%s'.", entity), nil
	}

	return strings.Join(results, "\n"), nil
}
