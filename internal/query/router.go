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

		Router			— top-level dispatcher; holds KB index, call graph, cache
		ContextBuilder	— assembles the context window for LLM calls
		Classifier		— maps a query string to a QueryType + confidence score

	Prompt construction follows a chain-of-thought pattern: every LLM prompt is
	composed of a shared header (cotHeader), a handler-specific reasoning body,
	and a shared footer (cotFooter). Handlers label each claim as one of:
		CONFIRMED IN SOURCE / INFERRED FROM SIGNATURE / CANNOT DETERMINE
*/

package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"eulix/internal/cache"
	"eulix/internal/config"
	"eulix/internal/llm"
	"eulix/internal/types"
)

// CoT scaffolding

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

// hasSourceCode checks whether any context chunk contains an actual code fence.
func hasSourceCode(ctx *types.ContextWindow) bool {
	for _, chunk := range ctx.Chunks {
		if strings.Contains(chunk.Content, "```") {
			return true
		}
	}
	return false
}

// buildPrompt composes header + task-specific body + footer.
func buildPrompt(query string, class *Classification, sourceAvailable bool, taskBody string) string {
	return cotHeader(query, class, sourceAvailable) +
		"=== TASK ===\n" +
		taskBody +
		cotFooter()
}

// Controller / loader (unchanged)

func (r *Router) SetCurrentChecksum(checksum string) {
	r.currentChecksum = checksum
}

func QueryTrafficController(eulixDir string, cfg *config.Config, llmClient *llm.Client, cacheManager *cache.Manager) (*Router, error) {
	kbIndex, err := loadKBIndex(eulixDir)
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
	}, nil
}

func loadKBIndex(eulixDir string) (*KBIndex, error) {
	indexPath := filepath.Join(eulixDir, "kb_index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	var index KBIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return &index, nil
}

func loadCallGraph(eulixDir string) (*CallGraph, error) {
	graphPath := filepath.Join(eulixDir, "kb_call_graph.json")
	data, err := os.ReadFile(graphPath)
	if err != nil {
		return nil, err
	}
	var graph CallGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, err
	}
	return &graph, nil
}

func (r *Router) ensureContextBuilder() error {
	if r.contextBuilder != nil {
		return nil
	}
	sourceRoot := r.config.Project.Path
	if _, err := os.Stat(sourceRoot); os.IsNotExist(err) {
		return fmt.Errorf("source root does not exist: %s", sourceRoot)
	}
	if r.config.Project.DebugConfig {
		fmt.Printf("[INFO] Initializing context builder with source root: %s\n", sourceRoot)
	}
	cb, err := ContextWindowCreator(r.eulixDir, r.config, r.llmClient, sourceRoot)
	if err != nil {
		return fmt.Errorf("failed to initialize context builder: %w", err)
	}
	r.contextBuilder = cb
	return nil
}

//  Main dispatch

func (r *Router) Query(query string) (string, error) {
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

//  Handlers

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

//  Shared helpers

// firstSymbolOrExtracted returns the first classified symbol or falls back to
// heuristic extraction from the raw query string.
func firstSymbolOrExtracted(class *Classification, query string) string {
	if len(class.Symbols) > 0 {
		return class.Symbols[0]
	}
	return extractEntityName(query)
}

func (r *Router) findTransitiveDependencies(funcName string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	visited := make(map[string]bool)
	var result []string

	var traverse func(name string, d int)
	traverse = func(name string, d int) {
		if d > depth || visited[name] {
			return
		}
		visited[name] = true
		if fn, ok := r.callGraph.Functions[name]; ok {
			for _, callee := range fn.Calls {
				if !visited[callee] && callee != funcName {
					result = append(result, callee)
					traverse(callee, d+1)
				}
			}
		}
	}
	traverse(funcName, 0)
	return result
}

func (r *Router) Close() error {
	if r.contextBuilder != nil {
		return r.contextBuilder.Close()
	}
	return nil
}

//  Entity extraction

func extractEntityName(query string) string {
	words := strings.Fields(query)
	stopWords := map[string]bool{
		"where": true, "is": true, "the": true, "function": true,
		"class": true, "method": true, "type": true, "find": true,
		"locate": true, "what": true, "does": true, "do": true,
		"who": true, "calls": true, "uses": true, "used": true,
		"a": true, "an": true, "this": true, "that": true,
		"how": true, "can": true, "will": true, "should": true,
	}
	for _, w := range words {
		if !stopWords[strings.ToLower(w)] && isLikelySymbol(w) {
			return w
		}
	}
	for _, w := range words {
		if !stopWords[strings.ToLower(w)] {
			return w
		}
	}
	return ""
}

func isLikelySymbol(word string) bool {
	if len(word) > 1 && word[0] >= 'A' && word[0] <= 'Z' {
		for _, ch := range word[1:] {
			if ch >= 'a' && ch <= 'z' {
				return true
			}
		}
	}
	return strings.Contains(word, "_")
}

//  Fuzzy search

func (r *Router) fuzzySearch(entity string) []string {
	type match struct {
		name  string
		score int
		typ   string
	}
	var matches []match
	low := strings.ToLower(entity)

	for name := range r.kbIndex.FunctionsByName {
		if s := fuzzyScore(low, strings.ToLower(name)); s > 0 {
			matches = append(matches, match{name, s, "function"})
		}
	}
	for name := range r.kbIndex.TypesByName {
		if s := fuzzyScore(low, strings.ToLower(name)); s > 0 {
			matches = append(matches, match{name, s, "type"})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].score > matches[j].score })

	var out []string
	for i, m := range matches {
		if i >= 5 {
			break
		}
		out = append(out, fmt.Sprintf("%s (%s)", m.name, m.typ))
	}
	return out
}

func fuzzyScore(pattern, target string) int {
	if pattern == target {
		return 1000
	}
	if strings.Contains(target, pattern) {
		return 500
	}
	score := 0
	for i := 0; i < len(pattern) && i < len(target); i++ {
		if pattern[i] == target[i] {
			score += 10
		}
	}
	freq := make(map[rune]int)
	for _, ch := range pattern {
		freq[ch]++
	}
	for _, ch := range target {
		if freq[ch] > 0 {
			score += 2
			freq[ch]--
		}
	}
	diff := len(target) - len(pattern)
	if diff < 0 {
		diff = -diff
	}
	return score - diff
}
