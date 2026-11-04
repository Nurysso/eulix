# query

The `query` package is the router. It decides how to answer each question: directly from the knowledge base, or by retrieving code and building a prompt for a language model.

> **Crate / path:** `eulix-cli/internal/query/` (`router_core.go`, `router_handlers.go`, `router_helpers.go`, `prompts.go`) and the sub-packages `classifier`, `retrieval` and `strip`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

Not every question needs a language model. "Where is `applyBudget` defined?" or "what are the entry points?" can be answered exactly from the data the parser produced, instantly and without any chance of invention. "Why does the budget code split tokens this way?" needs a model, and the model needs the right code in front of it. The router sorts questions into those two groups and sends each down the right path.

The package has a router and three sub-packages, each with its own page:

| Part | Page | Job |
| --- | --- | --- |
| Router and handlers | this page | Dispatch, direct answers, prompt assembly. |
| `classifier` | [Classifier](Classifier.md) | Puts each question into one of 21 types. |
| `retrieval` | [Retrieval Overview](retrieval/Overview.md) | Finds and ranks the code to show the model. |
| `strip` | [Strip](Strip.md) | Removes comments from source shown to the model. |
| Prompts | [Prompts](Prompts.md) | The instructions wrapped around the context. |

## Why It Exists

Routing cheaply and deterministically first keeps the model for the questions that need it. It also gives faster, more reliable answers for the common factual ones, and it lets the system say "I do not have the source for that" in the right situations instead of guessing.

## How It Works

### Creating the router

`QueryTrafficController(eulixDir, cfg, llmClient, cache)` builds a `Router`. It creates the classifier (which learns the project's real symbol names from `kb_index.json`), loads the index and the call graph with the memory-mapped decoder, and prepares caches. It does **not** build the retrieval engine yet. That is expensive, so `ensureContextBuilder` creates it on the first question that needs it, and `CloseContextBuilder` can release it later (the MCP server does this when idle).

### Two entry points

The router has two ways in, used by different programs.

**`PromptOrAnswer(query)`** (used by `eulix query` and by the MCP server) classifies the question and then:

- for factual types, **returns the direct answer**;
- for everything else, builds the retrieval context and **returns a prompt**: the context, followed by reasoning instructions unless they are switched off. It never calls a model.

**`QueryEngine(query)`** (used by the chat screen) does the same classification but, for understanding-type questions, builds the prompt **and sends it to the model**, returning the model's reply. If a history database is open, it then saves the reply, split into reasoning and answer.

### Question types and who handles them

| Handled directly, no model | Handled with retrieval and a model |
| --- | --- |
| Location, Usage, Dependency, CallGraph, EntryPoints, FileStructure, Todos, Metrics | Understanding, Implementation, Architecture, Debug, Comparison, Refactoring, Performance, DataFlow, Security, Documentation, Example, Testing |

`CodeGeneration` is a special case. The router declines it with a fixed explanation: it has signatures and structure but not guaranteed full source for everything, so it would have to invent logic. It offers an architecture or data-flow explanation instead.

Examples of the direct handlers: `handleLocation` finds where a symbol lives and shows its signature, read from the real file; `handleCallGraph` renders a callers-and-callees tree for an entity; `handleDependency` answers from the external-dependency list; `handleEntryPoints`, `handleTodosQuery`, `handleMetrics` and `handleFileStructure` format the matching parts of the knowledge base. When an entity name is ambiguous or missing, the handlers use fuzzy matching to suggest close names.

### Main Components

| File | Responsibility |
| --- | --- |
| `router_core.go` | The `Router`, its construction, `PromptOrAnswer` and `QueryEngine`. |
| `router_handlers.go` | One handler per question type, and the prompt builders. |
| `router_helpers.go` | Entity extraction, fuzzy search, signature extraction, dependency helpers, call-graph lookups. |
| `prompts.go` | The per-type task text. |

### Data In, Data Out

In: a question string. Out: a string, which is an answer, a prompt, or a model reply depending on the entry point and the question type.

## Key Algorithms

### Reading signatures from the real file

**The problem.** The knowledge base stores a signature for each function, but for the direct "where is it defined?" answer the router wants to show the actual declaration.

**The approach.** `extractSignature` opens the source file through a rooted file handle (so it cannot read outside the project), goes to the stored line, and parses the declaration for the file's language, handling multi-line signatures, and for PHP, attributes and modifiers. It then formats it consistently.

**Why this way.** The text shown is exactly what is in the source, and the rooted open means a crafted path in the index cannot escape the project. A recent hardening change made usage queries refuse paths outside the repository.

## Configuration

The router uses the whole `Config`; the most relevant settings are `retrievalConfig.code_to_ast_ratio` (used in the reasoning header) and `project.engine`. See [Config](../Config.md).

## Usage Examples

```bash
eulix query "where is applyBudget defined?"     # direct answer
eulix query "why does applyBudget cap the response reserve?"   # prints a prompt
```

Expected result: the first prints a location and signature; the second prints retrieved context followed by reasoning instructions.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Misclassified questions go down the wrong path. | The classifier is rule-based. See [Classifier](Classifier.md). | Rephrase with an explicit symbol or keyword. |
| `eulix query` does not call the model. | `PromptOrAnswer` returns prompts. | Use `eulix chat`. |
| Code-generation requests are declined. | By design, to avoid invented code. | Ask for an explanation instead. |
| The retrieval engine loads on first use. | It is expensive. | Expect a slower first question. |

## Related Docs

- [Classifier](Classifier.md), [Prompts](Prompts.md), [Strip](Strip.md).
- [Retrieval Overview](retrieval/Overview.md): the engine this router drives.
- [MCP Overview](../mcp/Overview.md): the other consumer of `PromptOrAnswer`.
