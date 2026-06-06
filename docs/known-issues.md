# Known Issues

This document tracks current known issues, bugs, and architectural limitations within the project.

## Parser

- **Inaccurate Call Graphs**: We currently use PRISM (Polyglot Resolution via inverted Symbol Map), which is a call graph approximation algorithm. This inherently limits the precision of our call graph generation.
- **False Positives for Similar Functions**: Accurate resolution would require comprehensive type analysis, which is currently computationally expensive and complex to implement. As a workaround, we default to the first function identified.
- **TypeScript AST Discrepancies**: The TypeScript grammar file is currently under active development, leading to potential issues with AST generation.
- **Misses some functions**: This is due to poor grammar files.

## Embedder

- **ROCm Runtime Warning**: You may see `(null): No such file or directory` at the start of applications. This is a known issue within the ROCm stack where the runtime fails to locate `amdgpu.ids` and incorrectly reports the error path. It does not affect functional performance.
- **Call Depth Becomes 0**: This occurs when the BFS algorithm fails to locate an entry point or when embedding is restricted to a specific sub-folder of the codebase.

## Query

- **Small functions are being ignored in retrival**:

  This is due to applyBudget is a small helper function and is only used once and mentioned 3 time in the entire (~23k)code base once in defination once in comment and another is when it was called, the retrival managed to get where it was called but coudnt retrival actual implementation

```bash
  [YOU]
  how does applyBudget is used

  [EULIX]
  <reasoning>
    1. Inventory:
    • applyBudget is mentioned but its definition or usage is not provided directly.

    2. Evidence:
    • The query "how does applyBudget is used" implies that there should be some evidence of how this function is called or referenced within the context.
    • However, no specific lines or files related to applyBudget are provided in the given context.

    3. Gaps:
    • There is no source code or metadata provided for applyBudget.
    • The query asks about its usage, but without any concrete evidence of where it is called or referenced, it's impossible to determine how it is used.

    4. Answer:
    • Since there is no visible context related to applyBudget, it is not possible to provide a detailed explanation of what the queried code does and why it
  exists.

  </reasoning>

  <answer>
  Not visible in context
  </answer>
```

retrival code

````go
...more code.....
``` Importance:0.9} {File:internal/query/context_builder.go StartLine:1729 EndLine:1766 Content:```go
func (cb *ContextBuilder) buildContextWithoutGraph(candidates []ScoredChunk, budget int) []ScoredChunk {
	if len(candidates) < 20 {
		return cb.applyBudget(candidates, budget)
	}
	fileGroups := make(map[string][]ScoredChunk)
	for _, c := range candidates {
... more code...
````
