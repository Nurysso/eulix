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
	"sort"
	"strings"

	"eulix/internal/types"
)

// buildPrompt composes header + task-specific body + footer.
func buildPrompt(query string, class *Classification, sourceAvailable bool, taskBody string) string {
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

	b.WriteString("=== CONTEXT INVENTORY ===\n")
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
	b.WriteString(fmt.Sprintf("Query type       : %s  (confidence %.2f)\n", class.Type.String(), class.Confidence))
	b.WriteString(fmt.Sprintf("User question    : %s\n\n", query))

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

func (r *Router) handleUnderstanding(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := `Explain what the queried code does and why it exists.

Your reasoning must cover:
a) What each relevant symbol does, citing source lines when available or
   noting 'signature only' when not.
b) How the pieces interact — trace at least one concrete call path from
   entry point to leaf, naming intermediate functions.
c) Any non-obvious design choices visible in the source or signatures.
d) What remains unclear due to missing source (list each gap explicitly).

Preferred answer shape:
  • One-paragraph summary
  • Bullet breakdown per key symbol (name | file:lines | role)
  • Call-path trace
  • Open questions / gaps
`
	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

func (r *Router) handleImplementation(query string, class *Classification) (string, error) {
	var relevantFiles []string
	for _, sym := range class.Symbols {
		if locs, ok := r.kbIndex.FunctionsByName[sym]; ok {
			relevantFiles = append(relevantFiles, locs...)
		}
		if locs, ok := r.kbIndex.TypesByName[sym]; ok {
			relevantFiles = append(relevantFiles, locs...)
		}
	}

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := fmt.Sprintf(`Describe how the implementation works for the queried symbols.
Known file locations: %v

Reasoning steps:
a) For each symbol that has source available, walk through the logic:
   control flow, error paths, notable patterns.
b) For each symbol with metadata only (signature + lines), describe what
   the signature implies about its contract — do NOT invent the body.
c) Identify the data types flowing between functions.
d) Note any implementation details that are invisible due to missing source.

Be explicit: "Source shows…" vs "Signature implies…" vs "Cannot determine…"
`, relevantFiles)

	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

func (r *Router) handleArchitecture(query string, class *Classification) (string, error) {
	// Pre-build call-graph summary for symbols in the query
	var cgSummary strings.Builder
	for _, sym := range class.Symbols {
		if fn, ok := r.callGraph.Functions[sym]; ok {
			cgSummary.WriteString(fmt.Sprintf("\n%s  (@ %s)\n", sym, fn.Location))
			if len(fn.Calls) > 0 {
				cgSummary.WriteString(fmt.Sprintf("  calls   : %v\n", fn.Calls))
			}
			if len(fn.CalledBy) > 0 {
				cgSummary.WriteString(fmt.Sprintf("  calledBy: %v\n", fn.CalledBy))
			}
		}
	}

	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := fmt.Sprintf(`Describe the architectural structure relevant to the question.

Pre-computed call-graph excerpt:
%s

Reasoning steps:
a) Identify the top-level entry points and the layers they delegate to.
b) Map each layer to its package/directory — use file paths from metadata.
c) Describe the dominant dependency direction (e.g., handler → service → repo).
d) Highlight any architectural violations visible in the call graph
   (e.g., cycles, cross-layer calls).
e) For each claim, cite the call edge or source file that supports it.
f) List structural facts you CANNOT determine (e.g., runtime wiring, DI config).

Output format:
  • Layer diagram (text art or bullet tree)
  • Key call paths (A → B → C, each with file:line when available)
  • Architectural observations
  • Unknowns
`, cgSummary.String())

	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

func (r *Router) handleDebug(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := `Investigate the reported problem using available evidence.

Reasoning steps:
a) REPRODUCE PATH — trace the call chain that leads to the failure.
   Use source lines when available; fall back to call-graph edges for
   symbols with metadata only.
b) ERROR SURFACE — identify all functions that return an error type
   along this path. Note which ones have visible error handling and
   which do not (source needed but absent).
c) TYPE SAFETY — flag any type mismatches, nil-pointer risks, or
   mismatched interface implementations visible in signatures.
d) STRUCTURAL ISSUES — cycles, missing dependencies, or unexpected
   call paths visible in the call graph.
e) VERDICT — for each hypothesis, state: CONFIRMED (seen in source) /
   SUSPECTED (inferred from signature) / CANNOT DETERMINE (source missing).

Do not suggest fixes for logic you cannot see.
`
	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
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

	body := fmt.Sprintf(`Compare the following symbols: %v

Reasoning steps:
a) SIGNATURES — list each symbol's parameter types and return types side by side.
b) DEPENDENCIES — compare their outbound call sets from the call graph.
   What does A call that B does not, and vice-versa?
c) SOURCE BEHAVIOUR — for symbols with source available, compare the actual
   logic (control flow, error handling style, data transformations).
   For metadata-only symbols, restrict comparison to signature-level.
d) LOCATION & OWNERSHIP — compare file paths / packages. Do they belong to
   the same layer?
e) SUMMARY TABLE — produce a markdown table: Dimension | SymbolA | SymbolB.

Flag each cell as (source), (signature), or (unknown).
`, class.Symbols)

	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
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

	body := `Identify refactoring opportunities grounded in the available evidence.

Reasoning steps:
a) STRUCTURAL SMELLS (call-graph observable):
   - Functions with fan-out > 7 (calls too many things)
   - Functions with fan-in > 10 (too many dependents — high blast radius)
   - Cycles in the call graph (mutual dependency)
   - Deep call chains (depth > 5)
   Cite the specific call edges for each finding.

b) SIGNATURE SMELLS (signature observable):
   - Functions with many parameters (≥ 5)
   - Functions returning many values (≥ 3 non-error)
   - Duplicate type definitions across packages

c) SOURCE SMELLS (only if source is available):
   - Repeated code patterns
   - Nested error handling without abstraction
   - Missing interface boundaries

d) PRIORITISED RECOMMENDATIONS:
   Rank by impact (high/med/low). For each, state:
   - What to do
   - Why (cite the evidence)
   - Risk (what could break — cite call-graph dependents)

Do NOT recommend changes to code you cannot see.
`
	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

func (r *Router) handlePerformance(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := `Analyse performance characteristics using available evidence.

Reasoning steps:
a) CALL DEPTH — trace the longest call chain reachable from the queried
   symbol. Deep chains mean latency stacks. Cite each hop.
b) ALLOCATION HOTSPOTS (signature-level) — identify functions whose
   parameter or return types include slices, maps, or pointer-heavy structs.
   These are allocation candidates.
c) SOURCE-LEVEL PATTERNS (only if source available) — look for:
   - Loops that call expensive functions
   - Repeated map lookups that could be cached
   - Unnecessary allocations (e.g., []byte conversions inside loops)
   - Goroutine spawning without bounding
d) INTERFACE OVERHEAD — note where concrete types are passed as interfaces;
   this prevents inlining.
e) VERDICT PER FINDING: CONFIRMED IN SOURCE / INFERRED FROM SIGNATURE /
   CANNOT DETERMINE.
f) SUGGESTIONS — only for findings with at least SUSPECTED confidence.
   Do not guess at optimisations for code you cannot see.
`
	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
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

	body := fmt.Sprintf(`Trace how data flows through the system for the queried operation.

Pre-computed call-chain excerpt:
%s

Reasoning steps:
a) ENTRY POINT — identify where the data enters (what type, from what caller).
b) TRANSFORMATION CHAIN — for each hop in the call chain, state:
   - Input type(s)
   - What the function does to the data (from source if available,
     from signature if not — label clearly)
   - Output type(s)
   Format: FuncName(inputType) → outputType  [source|signature|unknown]
c) BRANCHING — identify any conditional paths that change which function
   receives the data next (source required; note if invisible).
d) TERMINAL — where does the data end up? (written to DB, returned to caller,
   sent over network — infer from names/types if no source).
e) DATA LOSS POINTS — where could data be dropped or silently modified?
   (e.g., error paths that return zero values).

Show the full trace as a numbered pipeline, then explain each step.
`, chainInfo.String())

	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

func (r *Router) handleSecurity(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := `Perform a security-focused review using available evidence.

Reasoning steps:
a) ATTACK SURFACE (signature-observable):
   - Exported functions that accept raw string, []byte, or interface{} parameters
     (potential injection points — label each)
   - Functions that return errors which callers might ignore
     (check call graph for missing error propagation)
b) TRUST BOUNDARIES (call-graph observable):
   - Map where data crosses package boundaries
   - Identify calls from public-facing packages into internal ones
c) SOURCE-LEVEL CHECKS (only if source available):
   - Missing input validation before use
   - Hardcoded credentials or secrets
   - Unsafe pointer operations or cgo calls
   - Missing authentication/authorisation checks before sensitive operations
d) TYPE SAFETY:
   - Unchecked type assertions (.(ConcreteType))
   - Use of unsafe package
e) VERDICT PER FINDING: CONFIRMED IN SOURCE / SUSPECTED FROM SIGNATURE /
   CANNOT ASSESS (source missing).

Do not claim a vulnerability exists unless evidence supports it.
Prefer "potential injection point — validation not visible in context"
over fabricating a specific CVE-style claim.
`
	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

func (r *Router) handleDocumentation(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := `Generate documentation for the queried symbols.

Reasoning steps:
a) For each symbol, collect: name, package, file path, line range.
b) Extract the signature (parameters + return types).
c) If source is available, describe the actual behaviour; otherwise
   describe only what the signature implies — label the difference.
d) Document callers (who should use this) and callees (what it depends on).
e) Note preconditions and postconditions visible in the signature
   (e.g., a non-nil pointer parameter implies the caller must ensure non-nil).

Output format per symbol:
  ### FunctionName
  **Package**: pkg/name  **File**: path/to/file.go (lines X–Y)
  **Signature**: ` + "`func FunctionName(param Type) (RetType, error)`" + `
  **Purpose**: …  [from source | inferred from signature]
  **Parameters**: …
  **Returns**: …
  **Calls**: …
  **Called by**: …
  **Limitations of this doc**: (what source was missing)
`
	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

func (r *Router) handleExample(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := `Show how to use the queried symbols correctly.

Reasoning steps:
a) Identify the public API surface (exported functions/types) relevant to
   the question. List each with its exact signature.
b) If source is available, derive usage patterns from the actual
   implementation (constructor patterns, required call order, etc.).
   If source is absent, derive only from signatures — label clearly.
c) Construct a minimal, realistic call sequence:
   - Show types being constructed
   - Show the function being called with correctly typed arguments
   - Show error handling
   - Show the result being used
d) Highlight any non-obvious requirements visible in the signature
   (e.g., a context.Context param implies cancellation should be handled).
e) Explicitly state: "This example is derived from [source|signature].
   Behaviour not visible in the context may differ."

Do NOT invent parameter values or logic that you cannot ground in
the available source or signatures.
`
	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

func (r *Router) handleTesting(query string, class *Classification) (string, error) {
	ctx, err := r.contextBuilder.BuildContext(query)
	if err != nil {
		return "", fmt.Errorf("failed to build context: %w", err)
	}
	src := hasSourceCode(ctx)

	body := fmt.Sprintf(`Generate a testing strategy for: %s

Reasoning steps:
a) UNIT TEST TARGETS — list each function that should be tested directly.
   For each, note whether source is available (test cases can be exact) or
   only signature (test cases must be inferred — label them as such).
b) TEST CASES PER FUNCTION:
   - Happy path (normal input → expected output from source/signature)
   - Error paths (what error types are returned? trace them)
   - Boundary conditions (empty inputs, nil pointers, zero values)
   - Concurrency (if the function is called from multiple goroutines —
     visible from call graph fan-in)
c) MOCK STRATEGY — for each external dependency (visible in call graph),
   suggest what interface to mock and why.
d) INTEGRATION TEST SCOPE — identify call chains that should be tested
   end-to-end vs unit-tested in isolation.
e) COVERAGE GAPS — list behaviours that CANNOT be tested confidently
   because source is missing.

Format each test case as:
  TestFunctionName_Scenario / input description / expected outcome / confidence
`, query)

	return r.llmClient.Query(ctx, buildPrompt(query, class, src, body))
}

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

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Call graph for '%s'  (%s)\n", entity, fn.Location))

	// Callers tree (inbound)
	b.WriteString("\n┌─ Called by (inbound):\n")
	if len(fn.CalledBy) == 0 {
		b.WriteString("│  (none — likely an entry point or exported API)\n")
	} else {
		for _, caller := range fn.CalledBy {
			b.WriteString(fmt.Sprintf("│  ← %s\n", caller))
			// one level deeper
			if callerFn, ok := r.callGraph.Functions[caller]; ok {
				for _, grandCaller := range callerFn.CalledBy {
					b.WriteString(fmt.Sprintf("│     ← %s\n", grandCaller))
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
			b.WriteString(fmt.Sprintf("   → %s\n", callee))
			if calleeFn, ok := r.callGraph.Functions[callee]; ok {
				for _, grandCallee := range calleeFn.Calls {
					b.WriteString(fmt.Sprintf("      → %s\n", grandCallee))
				}
			}
		}
	}

	// Fan-in / fan-out summary
	b.WriteString(fmt.Sprintf("\nFan-in (callers) : %d\n", len(fn.CalledBy)))
	b.WriteString(fmt.Sprintf("Fan-out (callees): %d\n", len(fn.Calls)))
	if len(fn.CalledBy) == 0 {
		b.WriteString("Note: No callers detected — treat as entry point.\n")
	}
	if len(fn.Calls) > 7 {
		b.WriteString(fmt.Sprintf("⚠ High fan-out (%d) — consider splitting responsibilities.\n", len(fn.Calls)))
	}

	return b.String(), nil
}

func (r *Router) handleMetrics(query string, class *Classification) (string, error) {
	if r.kb == nil {
		return "Full KB (kb.json) not loaded — metrics query requires it.", nil
	}

	entity := firstSymbolOrExtracted(class, query)

	if entity != "" {
		for path, fd := range r.kb.Structure {
			for _, fn := range fd.Functions {
				if fn.Name == entity {
					return formatFunctionMetrics(fn, path), nil
				}
			}
			for _, cls := range fd.Classes {
				for _, method := range cls.Methods {
					if method.Name == entity {
						return formatFunctionMetrics(method, path), nil
					}
				}
			}
		}
	}

	// Project-wide summary
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Project metrics: %s\n\n", r.kb.Metadata.ProjectName))
	b.WriteString(fmt.Sprintf("  Files       : %d\n", r.kb.Metadata.TotalFiles))
	b.WriteString(fmt.Sprintf("  Total LOC   : %d\n", r.kb.Metadata.TotalLOC))
	b.WriteString(fmt.Sprintf("  Functions   : %d\n", r.kb.Metadata.TotalFunctions))
	b.WriteString(fmt.Sprintf("  Languages   : %s\n", strings.Join(r.kb.Metadata.Languages, ", ")))
	b.WriteString(fmt.Sprintf("  Parsed at   : %s\n\n", r.kb.Metadata.ParsedAt))
	// Top 10 most complex functions

	var all []fnEntry
	for path, fd := range r.kb.Structure {
		for i := range fd.Functions {
			all = append(all, fnEntry{path, &fd.Functions[i]})
		}
		for i := range fd.Classes {
			for j := range fd.Classes[i].Methods {
				all = append(all, fnEntry{path, &fd.Classes[i].Methods[j]})
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].fn.Complexity > all[j].fn.Complexity })

	b.WriteString("Top 10 most complex functions:\n")
	for i, e := range all {
		if i >= 10 {
			break
		}
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
	for _, ep := range r.kb.EntryPoints {
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

	if r.kb.Patterns.ArchitectureStyle != nil {
		b.WriteString(fmt.Sprintf("Architecture style : %s\n", *r.kb.Patterns.ArchitectureStyle))
	}

	return b.String(), nil
}

func (r *Router) handleFileStructure(query string, class *Classification) (string, error) {
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
