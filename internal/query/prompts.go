// Copyright (C) 2026 Dawood Khan
// SPDX-License-Identifier: GPL-3.0-or-later

// Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

/*
Package query provides context window building and query routing for Eulix's RAG system.
This file contains prompts based on intent.

Every prompt is assembled from three shared building blocks so all query types behave
consistently instead of each hand-rolling its own scaffold:

  - cotHeader            guarantees every mode wraps its output in <reasoning>/<answer> tags.
  - languageAgnosticNote forces the model to derive its vocabulary (null vs nil vs None,
    exceptions vs error values, interfaces vs traits, etc.) from whatever language the
    retrieved chunks are actually written in, instead of defaulting to whichever language
    the model has seen most in training.
  - confidenceLegend / citationContract give every mode the same claim-strength vocabulary
    (CONFIRMED / INFERRED / UNKNOWN) instead of the dozen slightly different ad-hoc labels
    the previous prompts used.

getTaskBody prepends the three shared blocks to whichever task-specific body is returned
by taskBodies, so no individual query type needs to repeat them.
*/

package query

import (
	"fmt"
	"strings"
)

// languageAgnosticNote stops any prompt from silently assuming a specific
// programming language, framework, or ecosystem. It is injected once by
// getTaskBody rather than duplicated in every task body, so adding support
// for a new language never requires touching the individual query types.
const languageAgnosticNote = `=== LANGUAGE GROUNDING ===
Do not assume a programming language, framework, or ecosystem in advance.
Step 0 of your reasoning: identify, from the file extensions, imports, and syntax actually present
in the retrieved chunks, which language(s) the relevant code is written in. Then describe behaviour
using that language's own vocabulary instead of defaulting to another language's idioms, e.g.:
  - absence-of-value:  null / nil / None / nullptr / undefined / Option::None use the source's own term
  - failure handling:  exceptions / error return values / Result-Either types / error codes / panics
  - polymorphism:      interfaces / traits / protocols / abstract classes / duck typing / generics
  - composition:       classes / structs / modules / structs + free functions / mixins
  - memory model:      garbage-collected / reference-counted / ownership & borrowing / manually managed
If the codebase mixes languages, keep each symbol's vocabulary consistent with the language it is
actually written in, and say so explicitly if it affects the answer. Never borrow behaviour,
vulnerabilities, or conventions from a similarly named library or framework in a DIFFERENT language
than what the context shows ground every claim only in the retrieved chunk text, never in
outside/training knowledge of "how this usually works" in some other codebase.
`

// confidenceLegend standardises how every mode labels claim strength.
const confidenceLegend = `=== CONFIDENCE LEGEND ===
Label every claim with one of:
  - CONFIRMED: directly visible in the retrieved source text.
  - INFERRED: deduced from a signature, type, or call-graph edge, but the body itself wasn't retrieved.
  - UNKNOWN: cannot be determined from the provided context.
`

// citationContract is the shared honesty-contract core. Individual task
// bodies may append extra, mode-specific rules after it, continuing the
// numbering from 5.
const citationContract = `HONESTY CONTRACT:
1. Cite the file:line where available, for every factual claim e.g. (path/to/file:214).
2. Tag every claim with a CONFIRMED / INFERRED / UNKNOWN label from the confidence legend.
3. If the answer is not present in the retrieved context, say so plainly instead of filling the gap with outside knowledge of the language, framework, or library.
4. Do not invent code bodies, logic, or behaviour that was not retrieved.
`

// taskBodies maps each query type to a function that returns the
// task-specific body string. Some types need runtime data (symbols,
// call-graph excerpts, etc.), hence the function signature. Bodies here
// should return ONLY the task-specific content the shared header,
// language note, and confidence legend are added once by getTaskBody.
var taskBodies = map[QueryType]func(r *Router, query string, class *Classification) string{
	QueryTypeUnderstanding: func(r *Router, query string, class *Classification) string {
		return `Explain what the queried code does and why it exists.

Reasoning steps:
a) For each relevant symbol, state what it does cite source lines where available, or note "signature only" when the body wasn't retrieved.
b) Trace at least one concrete call path from an entry point to a leaf, naming every intermediate function/method.
c) Note any non-obvious design choices visible in the source or signatures.
d) List what remains unclear because the source wasn't retrieved.

` + citationContract + `
Preferred <answer> shape:
  • One-paragraph summary
  • Bullet breakdown per key symbol (name | file:lines | role | confidence)
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
		format := `Describe how the implementation works for the queried symbols.
Known file locations: %v

Reasoning steps:
a) For each symbol with retrieved source, walk through the logic: control flow, error paths, notable patterns.
b) For each symbol with metadata only (signature + lines), describe what the signature implies about its contract, DO NOT INVENT the BODY.
c) Identify the data types flowing between functions.
d) Note which implementation details are invisible because the source wasn't retrieved.

` + citationContract
		return fmt.Sprintf(format, relevantFiles)
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
		format := `Describe the architectural structure relevant to the question.

Pre-computed call-graph excerpt:
%s

Reasoning steps:
a) Identify the top-level entry points and the layers they delegate to.
b) Map each layer to its module/directory using file paths from metadata.
c) Describe the dominant dependency direction actually observed in this codebase's call graph (e.g. a request/handler/service/storage-style flow), report only what you see here, not a generic assumption.
d) Flag any structural anomalies visible in the call graph (e.g., cycles, cross-layer calls).
e) List structural facts you CANNOT determine (e.g., runtime wiring, dependency-injection config).

` + citationContract + `
<answer> shape:
  • Layer diagram (text art or bullet tree)
  • Key call paths (A → B → C, each with file:line and confidence label when available)
  • Architectural observations
  • Unknowns
`
		return fmt.Sprintf(format, cgSummary.String())
	},

	QueryTypeDebug: func(r *Router, query string, class *Classification) string {
		return `Investigate the reported problem using available evidence.

Reasoning steps:
a) REPRODUCE PATH: trace the call chain leading to the failure. Use source lines when available; fall back to call-graph edges for symbols with metadata only.
b) ERROR SURFACE: identify every function along this path that can fail (raises/throws, returns an error/Result/error-code, or panics, whichever mechanism this language actually uses). Note which have visible handling and which don't.
c) TYPE SAFETY: flag type mismatches, missing-value risks (null/nil/None/etc., whichever applies here), or mismatched interface/trait/protocol implementations visible in signatures.
d) STRUCTURAL ISSUES: cycles, missing dependencies, or unexpected call paths visible in the call graph.
e) VERDICT: apply the confidence legend to each hypothesis.

` + citationContract + `
5. Do not suggest fixes for logic you cannot see.
`
	},

	QueryTypeComparison: func(r *Router, query string, class *Classification) string {
		if len(class.Symbols) < 2 {
			return `Not enough symbols were provided to perform a comparison.

Reasoning steps:
a) State how many symbols were detected, and list them.
b) Note that a meaningful comparison needs at least two.

` + citationContract + `
<answer>: Ask the user to specify at least two symbols to compare (e.g., "compare ObjectA and ObjectB"). Do not invent a second symbol to fill the gap.
`
		}
		format := `Compare the following symbols: %v

Reasoning steps:
a) SIGNATURES: list each symbol's parameter and return types side by side.
b) DEPENDENCIES: compare their outbound call sets from the call graph. What does A call that B doesn't, and vice versa?
c) SOURCE BEHAVIOUR: for symbols with retrieved source, compare the actual logic (control flow, error handling, data transformations). For metadata-only symbols, restrict the comparison to signature-level.
d) LOCATION & OWNERSHIP: compare file paths / modules. Do they belong to the same layer?
e) SUMMARY TABLE: a markdown table: Dimension | SymbolA | SymbolB | Confidence.

` + citationContract
		return fmt.Sprintf(format, class.Symbols)
	},

	QueryTypeRefactoring: func(r *Router, query string, class *Classification) string {
		return `Identify refactoring opportunities grounded in the available evidence.

Reasoning steps:
a) STRUCTURAL SMELLS (call-graph observable):
   - Functions/methods with fan-out > 7 (calls too many things)
   - Functions/methods with fan-in > 10 (too many dependents, high blast radius)
   - Cycles in the call graph (mutual dependency)
   - Deep call chains (depth > 5)
   Cite the specific call edges for each finding.
b) SIGNATURE SMELLS (signature observable):
   - Functions/methods with many parameters (≥ 5)
   - Functions returning many values or complex unstructured tuples/objects
   - Duplicate type/struct/class definitions across modules
c) SOURCE SMELLS (only where source is retrieved):
   - Repeated code patterns
   - Nested error handling/callbacks without abstraction
   - Missing interface/trait/protocol boundaries appropriate to this language
d) PRIORITISED RECOMMENDATIONS: rank by impact (high/med/low). For each: what to do, why (cite the evidence), and risk (cite call-graph dependents that could break).

` + citationContract + `
5. Do not recommend changes to code you cannot see.
`
	},

	QueryTypePerformance: func(r *Router, query string, class *Classification) string {
		return `Analyse performance characteristics using available evidence.

Reasoning steps:
a) CALL DEPTH: trace the longest call chain reachable from the queried symbol; deep chains mean latency stacks. Cite each hop.
b) ALLOCATION HOTSPOTS (signature-level): functions whose parameters or return types include large collections, dynamic arrays, or heavy objects are allocation candidates.
c) SOURCE-LEVEL PATTERNS (only where source is retrieved): loops calling expensive operations, repeated lookups that could be cached, unnecessary allocations (redundant casts, string building in loops), or unbounded thread/goroutine/task/worker spawning.
d) DISPATCH OVERHEAD: note where dynamic dispatch (virtual methods, interfaces, traits, vtables, whichever applies to this language) might prevent inlining.
e) VERDICT PER FINDING: apply the confidence legend.

` + citationContract + `
5. Suggest optimisations only for findings that are at least INFERRED; do not guess at optimisations for code you cannot see.
`
	},

	QueryTypeDataFlow: func(r *Router, query string, class *Classification) string {
		var chainInfo strings.Builder
		for _, sym := range class.Symbols {
			if fn, ok := r.callGraph.Functions[sym]; ok {
				fmt.Fprintf(&chainInfo, "\n%s → %v", sym, fn.Calls)
			}
		}
		format := `Trace how data flows through the system for the queried operation.

Pre-computed call-chain excerpt:
%s

Reasoning steps:
a) ENTRY POINT: identify where the data enters (what type, from what caller).
b) TRANSFORMATION CHAIN: for each hop, state input type(s), what the function does to the data (source if retrieved, signature if not, label clearly), and output type(s). Format: FuncName(inputType) → outputType [confidence].
c) BRANCHING: identify conditional paths that change which function receives the data next (requires retrieved source; note if invisible).
d) TERMINAL: where does the data end up (persisted, returned, sent over the network)? Infer from names/types if source wasn't retrieved.
e) DATA LOSS POINTS: where could data be dropped or silently modified (e.g., error paths returning null/empty/default values)?

` + citationContract + `
Show the full trace as a numbered pipeline inside <answer>, then explain each step.
`
		return fmt.Sprintf(format, chainInfo.String())
	},

	QueryTypeSecurity: func(r *Router, query string, class *Classification) string {
		return `Perform a security-focused review using only the evidence retrieved for this query.

Reasoning steps:
a) GROUND FIRST: list the exact function/method signatures and file:line locations you were given, quoting them from the [Chunk X] text. Do not name any symbol that is not present in the retrieved context.
b) ATTACK SURFACE (signature-observable): exported functions/methods that accept raw external input (strings, byte arrays, dynamic/any types, deserialized objects) are potential injection points; list them.
c) TRUST BOUNDARIES (call-graph observable): map where data crosses module/package/service boundaries, and where a public-facing symbol calls into an internal one.
d) SOURCE-LEVEL CHECKS (only where source is retrieved): missing input validation, hardcoded credentials/secrets, unsafe native/FFI calls, missing authentication/authorisation checks before sensitive operations. Do not assume a specific framework's protections exist (e.g. a particular web framework's built-in CSRF/XSS defenses) unless that exact code is present in the retrieved chunks, a protection you remember from a similarly named library elsewhere does not count as evidence here.
e) TYPE/MEMORY SAFETY: unchecked casts/downcasts, and, only for languages with manual or unsafe memory operations, unsafe memory access.
f) VERDICT PER FINDING: apply the confidence legend.

` + citationContract + `
5. Do not claim a vulnerability exists unless the evidence supports it; prefer "potential injection point, validation not visible in context" over a specific CVE-style claim.
6. Do not assume any particular library's or framework's known behaviour or CVEs apply unless that library's actual code appears in the retrieved chunks.
`
	},

	QueryTypeDocumentation: func(r *Router, query string, class *Classification) string {
		return `Generate documentation for the queried symbols.

Reasoning steps:
a) For each symbol, collect: name, module/package/namespace, file path, line range.
b) Extract the signature (parameters + return/result types).
c) If source is retrieved, describe the actual behaviour; otherwise describe only what the signature implies, label the difference.
d) Document callers (who should use this) and callees (what it depends on).
e) Note preconditions/postconditions visible in the signature (e.g., a non-nullable parameter implies the caller must supply a valid value).

` + citationContract + `
<answer> format per symbol:
  ### TargetName
  **Module/Package**: pkg/name  **File**: path/to/file (lines X–Y)
  **Signature**: ` + "`Function/Method Name(Parameters) -> ReturnType`" + `
  **Purpose**: …  [confidence label]
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
a) Identify the public API surface (exported functions/types/classes/methods) relevant to the question, listing each with its exact signature.
b) If source is retrieved, derive usage patterns from the actual implementation (construction order, required setup, etc.); if not, derive only from signatures, label clearly.
c) Construct a minimal, realistic call sequence: object/value construction, the call itself with correctly typed arguments, error/exception handling in this language's own idiom, and use of the result.
d) Highlight non-obvious requirements visible in the signature (e.g., a context/cancellation-token parameter implies an asynchronous or cancellable context).

` + citationContract + `
5. State explicitly whether this example is derived from source or from signature only, and that behaviour not visible in the context may differ.
6. Do not invent parameter values or logic that isn't grounded in the retrieved source or signatures.
`
	},

	QueryTypeTesting: func(r *Router, query string, class *Classification) string {
		format := `Generate a testing strategy for: %s

Reasoning steps:
a) UNIT TEST TARGETS: list each function/method that should be tested directly, noting whether source is retrieved (exact test cases possible) or only signature (test cases must be inferred, label them as such).
b) TEST CASES PER TARGET: happy path, error paths (trace what's returned/raised), boundary conditions (empty/zero/nil/None inputs), and concurrency where the call graph shows concurrent fan-in.
c) MOCK/STUB STRATEGY: for each external dependency visible in the call graph, suggest what to mock or stub and why, using this language's own testing idioms.
d) INTEGRATION SCOPE: which call chains should be tested end-to-end vs. unit-tested in isolation.
e) COVERAGE GAPS: behaviours that cannot be tested confidently because source wasn't retrieved.

` + citationContract + `
Format each test case as: TestName_Scenario / input description / expected outcome / confidence.
`
		return fmt.Sprintf(format, query)
	},
}

// getTaskBody returns the full prompt body for a query type: the shared
// chain-of-thought header, the language-agnostic grounding note, and the
// shared confidence legend, followed by the task-specific reasoning steps.
// Falls back to the Understanding body if no mapping exists.
func getTaskBody(r *Router, query string, class *Classification) string {
	fn, ok := taskBodies[class.Type]
	if !ok {
		fn = taskBodies[QueryTypeUnderstanding]
	}
	return languageAgnosticNote + "\n" + confidenceLegend + "\n" + fn(r, query, class)
}
