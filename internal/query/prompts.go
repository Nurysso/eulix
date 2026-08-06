// Copyright (C) 2026 Dawood Khan
// SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides context window building and query routing for Eulix's RAG system.
This files contains prompts based on intent
*/

package query

import (
	"fmt"
	"strings"
)

// taskBodies maps each query type to a function that returns the
// task-specific body string. Some types need runtime data (symbols,
// call-graph excerpts, etc.), hence the function signature.
var taskBodies = map[QueryType]func(r *Router, query string, class *Classification) string{
	QueryTypeUnderstanding: func(r *Router, query string, class *Classification) string {
		return `Explain what the queried code does and why it exists.

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
	},

	QueryTypeImplementation: func(r *Router, query string, class *Classification) string {
		var relevantFiles []string
		for _, sym := range class.Symbols {
			if locs, ok := r.kbIndex.FunctionsByName[sym]; ok {
				relevantFiles = append(relevantFiles, locs...)
			}
			if locs, ok := r.kbIndex.TypesByName[sym]; ok {
				relevantFiles = append(relevantFiles, locs...)
			}
		}
		return fmt.Sprintf(`Describe how the implementation works for the queried symbols.
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
	},

	QueryTypeArchitecture: func(r *Router, query string, class *Classification) string {
		var cgSummary strings.Builder
		for _, sym := range class.Symbols {
			if fn, ok := r.callGraph.Functions[sym]; ok {
				fmt.Fprintf(&cgSummary, "\n%s  (@ %s)\n", sym, fn.Location)
				if len(fn.Calls) > 0 {
					fmt.Fprintf(&cgSummary, "  calls   : %v\n", fn.Calls)
				}
				if len(fn.CalledBy) > 0 {
					fmt.Fprintf(&cgSummary, "  calledBy: %v\n", fn.CalledBy)
				}
			}
		}
		return fmt.Sprintf(`Describe the architectural structure relevant to the question.

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
	},

	QueryTypeDebug: func(r *Router, query string, class *Classification) string {
		return `Investigate the reported problem using available evidence.

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
	},

	QueryTypeComparison: func(r *Router, query string, class *Classification) string {
		if len(class.Symbols) < 2 {
			return "Please specify at least two symbols to compare (e.g., 'compare FooHandler and BarHandler')."
		}
		return fmt.Sprintf(`Compare the following symbols: %v

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
	},

	QueryTypeRefactoring: func(r *Router, query string, class *Classification) string {
		return `Identify refactoring opportunities grounded in the available evidence.

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
	},

	QueryTypePerformance: func(r *Router, query string, class *Classification) string {
		return `Analyse performance characteristics using available evidence.

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
	},

	QueryTypeDataFlow: func(r *Router, query string, class *Classification) string {
		var chainInfo strings.Builder
		for _, sym := range class.Symbols {
			if fn, ok := r.callGraph.Functions[sym]; ok {
				fmt.Fprintf(&chainInfo, "\n%s → %v", sym, fn.Calls)
			}
		}
		return fmt.Sprintf(`Trace how data flows through the system for the queried operation.

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
	},

	QueryTypeSecurity: func(r *Router, query string, class *Classification) string {
		return `Perform a security-focused review using available evidence.

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
	},

	QueryTypeDocumentation: func(r *Router, query string, class *Classification) string {
		return `Generate documentation for the queried symbols.

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
	},

	QueryTypeExample: func(r *Router, query string, class *Classification) string {
		return `Show how to use the queried symbols correctly.

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
	},

	QueryTypeTesting: func(r *Router, query string, class *Classification) string {
		return fmt.Sprintf(`Generate a testing strategy for: %s

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
	},
}

// getTaskBody returns the task-specific prompt body for a query type.
// Falls back to the Understanding body if no mapping exists.
func getTaskBody(r *Router, query string, class *Classification) string {
	if fn, ok := taskBodies[class.Type]; ok {
		return fn(r, query, class)
	}
	// Default fallback
	return taskBodies[QueryTypeUnderstanding](r, query, class)
}
