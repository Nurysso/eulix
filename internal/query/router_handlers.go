//  Copyright (C) 2026 Dawood Khan
//  SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
// Package query provides query classification functionality.

/*
This file is responsible for handleing/Generating Prompts with CoT.
*/
package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"eulix/internal/types"
)

// BuildPromptString composes header + task-specific body + footer.
func BuildPromptString(query string, class *Classification, sourceAvailable bool, taskBody string) string {
	return cotHeader(query, class, sourceAvailable) +
		" TASK \n" +
		taskBody +
		cotFooter()
}

// buildFullPromptWithContext assembles the context inventory + CoT prompt,
// the same way PromptOrAnswer does. Every LLM-calling handler should use
// this instead of calling BuildPromptString alone, or the retrieved code
// context never reaches the model.
func (r *Router) buildFullPromptWithContext(ctx *types.ContextWindow, query string, class *Classification) string {
	src := hasSourceCode(ctx)
	taskBody := getTaskBody(r, query, class)

	contextPrompt := r.llmClient.BuildFullPrompt(ctx, query)
	cotPrompt := BuildPromptString(query, class, src, taskBody)

	return contextPrompt + "\n\n" + cotPrompt
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
		fmt.Fprintf(&b, "Relevant symbols : %v\n", class.Symbols)
	}
	if len(class.Keywords) > 0 {
		fmt.Fprintf(&b, "Key terms        : %v\n", class.Keywords)
	}
	fmt.Fprintf(&b, "Query type       : %s  (confidence %.1f%%)\n", class.Type.String(), class.Confidence*100)
	fmt.Fprintf(&b, "Question you need to anser is  %s\n\n", query)

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
	return "\n HONESTY CONTRACT \n" +
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
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleImplementation(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleArchitecture(query string, class *Classification) (string, error) {

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleDebug(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
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
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleRefactoring(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handlePerformance(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleDataFlow(query string, class *Classification) (string, error) {
	// Pre-compute the call-chain type trace for symbols in the query
	var chainInfo strings.Builder
	for _, sym := range class.Symbols {
		if fn, ok := r.callGraph.Functions[sym]; ok {
			fmt.Fprintf(&chainInfo, "\n%s → %v", sym, fn.Calls)
		}
	}

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleSecurity(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleDocumentation(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleExample(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

func (r *Router) handleTesting(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	fullPrompt := r.buildFullPromptWithContext(ctx, query, class)
	return r.llmClient.LlmResponse(fullPrompt)
}

// NON LLM BASED HANDLERS

// handleCallGraph renders the two-level call tree with a per-symbol cache.
// First call for a symbol: O(callers + callees + their neighbours).
// Subsequent calls: O(1) map lookup + string copy.
func buildRouterCallGraph(ref *types.CallGraphRef) *CallGraph {
	if ref == nil {
		return &CallGraph{Functions: map[string]CGFunction{}}
	}

	fns := make(map[string]CGFunction, len(ref.Nodes))
	for _, n := range ref.Nodes {
		fns[bareID(n.ID)] = CGFunction{Location: n.File}
	}

	for _, e := range ref.Edges {
		if e.EdgeType != "call" {
			continue
		}
		from := bareID(e.From)
		to := bareID(e.To)

		caller := fns[from]
		caller.Calls = append(caller.Calls, to)
		fns[from] = caller

		callee := fns[to]
		callee.CalledBy = append(callee.CalledBy, from)
		fns[to] = callee
	}

	return &CallGraph{Functions: fns}
}

// bareID strips the node-type prefix that eulix-parser emits.
// "func_firstSymbolOrExtracted"   → "firstSymbolOrExtracted"
// "method_Router_handleLocation"  → "Router.handleLocation"
// "type_KBIndices"                → "KBIndices"
func bareID(id string) string {
	for _, prefix := range []string{"func_", "method_", "type_"} {
		if strings.HasPrefix(id, prefix) {
			s := strings.TrimPrefix(id, prefix)
			if prefix == "method_" {
				if i := strings.Index(s, "_"); i != -1 {
					return s[:i] + "." + s[i+1:]
				}
			}
			return s
		}
	}
	return id
}

func (r *Router) handleDependency(query string, _ *Classification) (string, error) {
	entity := extractDepQueryTerm(query)
	if entity == "" {
		return "Could not identify an entity for dependency analysis.", nil
	}

	if len(r.contextBuilder.GetExternalDeps()) == 0 {
		if err := r.contextBuilder.loadExternalDeps(); err != nil {
			return "", fmt.Errorf("could not load external deps: %w", err)
		}
	}
	deps := r.contextBuilder.GetExternalDeps()
	idx := r.contextBuilder.GetDepIndex()

	entityLow := strings.ToLower(entity)
	queryLow := strings.ToLower(query)

	switch classifyDepIntent(queryLow, entityLow) {
	case depIntentAll:
		return formatAllExternalDeps(deps), nil

	case depIntentFile:
		return formatFileImports(entityLow, idx), nil

	case depIntentCount:
		return formatDepCount(deps), nil

	default: // depIntentWhoUses and depIntentLookup share the same matching logic
		matched := idx.matchDeps(entityLow)
		if len(matched) == 0 {
			return fmt.Sprintf("No dependency named '%s' found.\nTip: use 'list dependencies' to see all.", entity), nil
		}
		sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })
		return formatMatchedDeps(entity, matched), nil
	}
}

func (r *Router) handleCallGraph(query string, class *Classification) (string, error) {
	entity := firstSymbolOrExtracted(class, query)
	if entity == "" {
		return "Could not identify a symbol for call graph analysis.", nil
	}
	if r.cgBuild == nil {
		return "", fmt.Errorf("call graph not found while running")
	}

	resolvedKey, node, ok, ambiguous := r.resolveCallGraphEntity(entity)
	if !ok {
		if matches := r.fuzzySearch(entity); len(matches) > 0 {
			return fmt.Sprintf("'%s' not found. Did you mean: %s",
				entity, strings.Join(matches, ", ")), nil
		}
		return fmt.Sprintf("'%s' not found in call graph.", entity), nil
	}

	r.cgIdx.mu.RLock()
	if s, hit := r.cgIdx.cache[resolvedKey]; hit {
		r.cgIdx.mu.RUnlock()
		return s, nil
	}
	r.cgIdx.mu.RUnlock()

	callers := r.cgBuild.CalledBy[resolvedKey]
	callees := r.cgBuild.Calls[resolvedKey]

	var b strings.Builder
	b.Grow(2048)

	// Ambiguity note goes first in the output
	if len(ambiguous) > 1 {
		fmt.Fprintf(&b, "Note: '%s' matched %d symbols. Showing highest-traffic one. Others:\n",
			entity, len(ambiguous))
		for _, k := range ambiguous {
			if k == resolvedKey {
				continue
			}
			n := r.cgBuild.Nodes[k]
			fmt.Fprintf(&b, "  • %s  (fan-in: %d, file: %s)\n",
				callGraphShortName(k), n.CallCountEstimate, n.File)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Call graph for '%s'\n", resolvedKey)
	fmt.Fprintf(&b, "File     : %s\n", node.File)
	fmt.Fprintf(&b, "Type     : %s\n", node.NodeType)
	if node.IsEntryPoint {
		b.WriteString("Role     : entry point\n")
	}

	b.WriteString("\n┌─ Called by (inbound):\n")
	if len(callers) == 0 {
		b.WriteString("│  (none — likely an entry point or exported API)\n")
	} else {
		for _, callerID := range callers {
			fmt.Fprintf(&b, "│  ← %s\n", callerID)
			for _, gc := range r.cgBuild.CalledBy[callerID] {
				fmt.Fprintf(&b, "│     ← %s\n", gc)
			}
		}
	}

	b.WriteString("\n└─ Calls (outbound):\n")
	if len(callees) == 0 {
		b.WriteString("   (none — leaf function)\n")
	} else {
		for _, callee := range callees {
			fmt.Fprintf(&b, "   → %s\n", callee)
			for _, gc := range r.cgBuild.Calls[callee] {
				fmt.Fprintf(&b, "      → %s\n", gc)
			}
		}
	}

	fmt.Fprintf(&b, "\nFan-in : %d\nFan-out: %d\n", len(callers), len(callees))
	if len(callers) == 0 {
		b.WriteString("Note: No callers detected — treat as entry point.\n")
	}
	if len(callees) > 7 {
		fmt.Fprintf(&b, "⚠ High fan-out (%d) — consider splitting.\n", len(callees))
	}

	result := b.String()
	r.cgIdx.mu.Lock()
	r.cgIdx.cache[resolvedKey] = result
	r.cgIdx.mu.Unlock()
	return result, nil
}

// handleMetrics for project-wide summary and per-symbol lookup.
func (r *Router) handleMetrics(query string, class *Classification) (string, error) {
	metricsPath := filepath.Join(r.config.Project.Path, ".eulix", "kb_metrics.json")

	data, err := os.ReadFile(metricsPath)
	if err != nil {
		return "", fmt.Errorf("failed to read metrics file at %s: %w", metricsPath, err)
	}

	var ref types.MetricsRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return "", fmt.Errorf("failed to parse metrics JSON: %w", err)
	}

	byName := make(map[string]metricsEntry)
	var topComplex []metricsEntry

	for _, fn := range ref.TopComplexFunctions {
		// Allocate a local copy to safely take its address
		kbFn := types.KBFunction{
			Name:            fn.Name,
			LineStart:       fn.LineStart,
			LineEnd:         fn.LineEnd,
			Complexity:      fn.Complexity,
			ImportanceScore: fn.ImportanceScore,
		}

		entry := metricsEntry{
			fn:   &kbFn,
			file: fn.File,
		}

		byName[fn.Name] = entry
		topComplex = append(topComplex, entry)
	}

	// Parse the query intent
	lowerQuery := strings.ToLower(query)
	isProjectMetrics := strings.Contains(lowerQuery, "project") ||
		strings.Contains(lowerQuery, "overall") ||
		strings.Contains(lowerQuery, "summary") ||
		strings.Contains(lowerQuery, "total") ||
		strings.Contains(lowerQuery, "all functions") ||
		len(class.Symbols) == 0

	entity := firstSymbolOrExtracted(class, query)

	if entity != "" && !isProjectMetrics {
		if e, ok := byName[entity]; ok {
			return formatFunctionMetrics(*e.fn, e.file), nil
		}
		return fmt.Sprintf("'%s' not found in metrics index.", entity), nil
	}
	var b strings.Builder
	meta := ref.Metadata

	fmt.Fprintf(&b, "Project metrics: %s\n\n", meta.ProjectName)
	fmt.Fprintf(&b, "  Files       : %d\n", meta.TotalFiles)
	fmt.Fprintf(&b, "  Total LOC   : %d\n", meta.TotalLOC)
	fmt.Fprintf(&b, "  Functions   : %d\n", meta.TotalFunctions)
	fmt.Fprintf(&b, "  Languages   : %s\n", strings.Join(meta.Languages, ", "))
	fmt.Fprintf(&b, "  Parsed at   : %s\n\n", meta.ParsedAt)

	b.WriteString("Top 10 most complex functions:\n")

	// Safely boundary-check slice sizing
	limit := len(topComplex)
	if limit > 10 {
		limit = 10
	}

	for i := 0; i < limit; i++ {
		e := topComplex[i]
		fmt.Fprintf(&b, "  %2d. %s  (%s, complexity %d, importance %.2f)\n",
			i+1, e.fn.Name, e.file, e.fn.Complexity, e.fn.ImportanceScore)
	}

	return b.String(), nil
}

func (r *Router) handleEntryPoints(_ string, _ *Classification) (string, error) {
	entryPath := filepath.Join(r.config.Project.Path, ".eulix", "kb_entry_points.json")
	data, err := os.ReadFile(entryPath)
	if err != nil {
		return "", fmt.Errorf("failed to read entry points at %s: %w", entryPath, err)
	}

	var ref types.EntryPointsRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return "", fmt.Errorf("failed to parse kb_entry_points.json: %w", err)
	}
	// TODO: uncomment when level based debugger
	// log.Printf("[DEBUG] entry points loaded: %d entries, raw: %s", len(ref.EntryPoint), string(data[:min(200, len(data))]))
	var b strings.Builder
	b.WriteString("Entry points \n")

	byType := make(map[string][]types.EntryPoint)
	for _, ep := range ref.EntryPoint {
		byType[ep.EntryType] = append(byType[ep.EntryType], ep)
	}

	for epType, eps := range byType {
		fmt.Fprintf(&b, "── %s ──\n", strings.ToUpper(epType))
		for _, ep := range eps {
			if ep.Path != nil {
				methods := strings.Join(ep.Methods, ", ")
				fmt.Fprintf(&b, "  [%s] %s → %s  (%s:%d)\n", methods, *ep.Path, ep.Handler, ep.File, ep.Line)
			} else {
				fmt.Fprintf(&b, "  %s  (%s:%d)\n", ep.Handler, ep.File, ep.Line)
			}
		}
		b.WriteString("\n")
	}
	if r.Patterns != nil && r.Patterns.ArchitectureStyle != nil {
		fmt.Fprintf(&b, "Architecture style : %s\n", *r.Patterns.ArchitectureStyle)
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
		fmt.Fprintf(&b, "Project: %s  (%d files, %d LOC)\n\n",
			r.kb.Metadata.ProjectName, r.kb.Metadata.TotalFiles, r.kb.Metadata.TotalLOC)
		for path, fd := range r.kb.Structure {
			fmt.Fprintf(&b, "  %s  [%s, %d LOC, %d fns, %d classes]\n",
				path, fd.Language, fd.LOC, len(fd.Functions), len(fd.Classes))
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
		fmt.Fprintf(&b, "⚠ Security notes (%d):\n", len(secNotes))
		for _, sn := range secNotes {
			fmt.Fprintf(&b, "  [%s] %s:%d — %s\n", sn.noteType, sn.file, sn.line, sn.desc)
		}
		b.WriteString("\n")
	}

	printTodos := func(label string, items []todoItem) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s (%d):\n", label, len(items))
		for _, td := range items {
			fmt.Fprintf(&b, "  %s:%d — %s\n", td.file, td.line, td.text)
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
	if r.kbIndex == nil {
		return "", fmt.Errorf("kb index unavailable — router was not fully initialized")
	}
	r.contextBuilder.debugLog.Log("[DEBUG] kbIndex ptr=%p len(FunctionsByName)=%d\n", r.kbIndex, len(r.kbIndex.FunctionsByName))
	entity := firstSymbolOrExtracted(class, query)
	r.contextBuilder.debugLog.Log("[DEBUG] handleLocation: entity=%q\n", entity)
	if entity == "" {
		return "Could not identify a function or class name in the query.", nil
	}

	var b strings.Builder
	found := false

	if locations, ok := r.kbIndex.FunctionsByName[entity]; ok {
		fmt.Fprintf(&b, "Function '%s' defined at:\n", entity)
		for _, loc := range dedupe(locations) {
			fmt.Fprintf(&b, "  %s\n", loc)
		}
		found = true
	}

	if locations, ok := r.kbIndex.TypesByName[entity]; ok {
		fmt.Fprintf(&b, "Type '%s' defined at:\n", entity)
		for _, loc := range dedupe(locations) {
			fmt.Fprintf(&b, "  %s\n", loc)
		}
		found = true
	}

	if callers, ok := r.kbIndex.FunctionsCalling[entity]; ok {
		fmt.Fprintf(&b, "'%s' is called by:\n", entity)
		for _, caller := range dedupe(callers) {
			fmt.Fprintf(&b, "  %s\n", caller)
		}
		found = true
	}

	if !found {
		if matches := r.fuzzySearch(entity); len(matches) > 0 {
			fmt.Fprintf(&b, "No exact match for '%s'. Closest symbols:\n", entity)
			for _, m := range matches {
				fmt.Fprintf(&b, "  %s\n", m)
			}
		} else {
			return fmt.Sprintf("'%s' was not found in the knowledge base.", entity), nil
		}
	}

	return b.String(), nil
}

func (r *Router) handleUsage(query string, class *Classification) (string, error) {
	query = stripCommandPrefix(query, "usage of", "usage", "use", "uses of", "show usage", "find usage")
	entity := firstSymbolOrExtracted(class, query)
	if entity == "" {
		return "Could not identify a function or class name in the query.", nil
	}

	locations, ok := r.kbIndex.FunctionsByName[entity]
	if !ok || len(locations) == 0 {
		return fmt.Sprintf("'%s' not found in knowledge base.", entity), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Usage of '%s'\n\n", entity)

	for _, loc := range dedupe(locations) {
		file, line, err := parseLocation(loc) // "path/to/file.go:42"
		if err != nil {
			continue
		}
		sig, err := extractSignature(file, line)
		if err != nil {
			fmt.Fprintf(&b, "  %s — could not read signature: %v\n", loc, err)
			continue
		}
		fmt.Fprintf(&b, "  %s\n%s\n", loc, sig)
	}

	return b.String(), nil
}
