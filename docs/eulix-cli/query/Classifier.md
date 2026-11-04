# classifier

The `classifier` package puts each question into one of 21 types, using fixed rules and no model.

> **Crate / path:** `eulix-cli/internal/query/classifier/`  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

The router needs to know what kind of question it has been asked before it can answer. The classifier does that deterministically: the same question always gets the same type, in a fraction of a millisecond, with no network call. It uses three layers of increasing effort and stops at the first that is confident.

## Why It Exists

Asking a model to classify every question would add latency and cost to something simple, and would make behaviour harder to predict and test. Rules are fast, free, and easy to audit. The trade-off is that unusual phrasing can be missed, which the tests record openly (`TestLevel1_KnownVocabularyGaps`).

## How It Works

`Classify(query)` returns a `Classification`: the type, extracted symbols and entities, keywords, a confidence, a priority, a short reasoning string, and a flag saying whether the question needs retrieved context.

**Guard.** Questions longer than 16 KB are not analysed. They default to *Understanding* with confidence 0.5. This keeps hostile or accidental huge inputs from burning CPU in the pattern matching.

**Layer 1: patterns.** About two dozen compiled regular expressions recognise phrasings for each type ("where is", "who calls", "show the call graph", "entry points", "todos", and so on). If a pattern matches with confidence 0.95 or more, the question is classified and the rest is skipped.

**Layer 2: symbols.** Otherwise the classifier extracts identifier-looking words and **validates them against the project's real symbols**. The valid set is built from `kb_index.json` when the classifier is created, from function names and type names. If valid symbols are found, simple rules decide the type. Two or more symbols with a comparison word ("difference", "compare", "vs", "versus", "similar") give *Comparison*. A single symbol with "where", "find", "locate" or "show" gives *Location*; with "calls", "uses", "invokes", "called by" or "used by" gives *Usage*; with "example", "how to use" or "sample" gives *Example*. Any other question with several symbols gives *Understanding*. Each symbol also becomes an *entity*, tagged as a function or a type.

**Layer 3: keywords.** Finally, if nothing else decided, keyword lists pick the type, checked in this order: debug words ("error", "bug", "crash", "not working"), performance words ("slow", "optimize", "bottleneck"), refactoring words, testing words, and so on, falling back to *Understanding*.

### The 21 types

| Group | Types |
| --- | --- |
| Direct answers | Location, Usage, Dependency, CallGraph, EntryPoints, FileStructure, Todos, Metrics |
| Need retrieval and a model | Understanding, Implementation, Architecture, Debug, Comparison, Refactoring, Performance, DataFlow, Security, Documentation, Example, Testing |
| Declined | CodeGeneration |

### Main Components

| Item | Responsibility |
| --- | --- |
| `QuerySheriff(kbIndexPath)` | Builds a classifier and loads the valid symbols. |
| `Classify` | The three-layer decision. |
| `level1PatternMatch`, `level2SymbolAnalysis`, `level3KeywordAnalysis` | The layers. |
| `extractSymbols`, `validateSymbols`, `extractEntities`, `extractKeywords` | Helpers. |
| `QueryType` | The type enum, with a `String()` method. |

### Data In, Data Out

In: the question. Out: a `Classification`. Each classifier holds its own copy of the symbol sets, so classifiers are independent and safe to use from several goroutines.

## Key Algorithms

### Validating symbols against the project

**The problem.** Any capitalized or underscored word looks like a symbol, including words that are just English or names in the question's wording.

**The approach.** Keep only extracted words that actually exist in the project's index.

**Why this way.** It removes most false positives cheaply, and it makes the classifier project-aware without any training.

## Configuration

None. The length limit (`maxQueryLen`) is a constant; a source comment suggests letting users change it through an environment variable.

## Usage Examples

```go
c, _ := classifier.QuerySheriff(".eulix/kb_index.json")
cl := c.Classify("who calls applyBudget?")
fmt.Println(cl.Type, cl.Confidence) // Usage 0.95 (illustrative)
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Unusual phrasings are missed. | Rules only. | Include a symbol or a clear keyword. |
| The valid-symbol set is read once at start-up. | Built from the index when the router is created. | Restart after re-analysing. |
| The classifier file has a large test suite, including fuzzing, but some probes are recorded as known gaps. | By design. | See `classifier_test.go`. |

## Related Docs

- [query Overview](Overview.md): how the type is used.
- [Retrieval Overview](retrieval/Overview.md): note that retrieval has its own, separate "intent" classification.
