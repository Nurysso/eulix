# Known Issues

This document tracks current known issues, bugs, and architectural limitations within the project.

## Parser

- **Inaccurate Call Graphs**: We currently use PRISM (Polyglot Resolution via inverted Symbol Map), which is a call graph approximation algorithm. This inherently limits the precision of our call graph generation. Both v1 and v2 of this algorithm have its pros and cons v1 is fast but less accurate v2 is slightly slower but can track inheritance and build better call graphs.

- **Java/Ruby AST Discrepancies**: The Java/Ruby grammar file is currently under active development, leading to potential issues with AST generation.

## Embedder

- **ROCm Runtime Warning**: You may see `(null): No such file or directory` at the start of applications. This is a known issue within the ROCm stack where the runtime fails to locate `amdgpu.ids` and incorrectly reports the error path. It does not affect functional performance.

## Deep codebase queries

For queries that require deep understanding of the codebase, such as:

> “When Expression.resolve_expression() processes an OuterRef, which specific Set attribute on the inner Query object is mutated to track parent table aliases? How does SQLCompiler.get_from_clause() use this set to ensure those outer tables are omitted from the subquery's FROM SQL block?”

Eulix can currently provide good-enough retrieval, but may not provide a completely accurate final answer.

This is not necessarily a failure of retrieval or the LLM. The current limitation is primarily related to resource allocation during the source hydration phase (source.go). Deeply coupled queries may require following a chain of related functions, classes, and files, which can exceed the available source-token budget or cause important intermediate code to receive insufficient context.

You can try increasing the maximum token limit in eulix.toml. However, this does not guarantee an improvement in retrieval quality or the accuracy of the final LLM-generated answer.

We are working on improving source hydration and context allocation for these deeply coupled queries.
