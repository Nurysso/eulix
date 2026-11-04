# MCP Retrieval Cascade

This page explains how the MCP server decides how to search for a question, and when to stop.

> **Crate / path:** `eulix-cli/internal/mcp/strategy.go`, `strategy_orchestration.go`, `strategy_retriever.go`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

A coding agent sends two very different kinds of question. One is precise: "what does `SQLCompiler.get_from_clause` do?" The other is vague: "how are database joins compiled?" A single search method is good at only one of them. The cascade tries several methods, from the most precise to the most forgiving, and stops as soon as one produces a result that looks good enough.

## Why It Exists

The underlying router is built around one search mode. When the agent already knows the relevant symbols, using them directly gives much better results than throwing the whole question at semantic search. When it does not know, forcing a symbol path would waste time. The cascade lets the server pick the right mode per question, automatically.

## How It Works

The cascade has four stages. Each runs only if the earlier ones did not produce a result scoring at least `MinQuality` (0.55 by default).

| Stage | Name | When it runs | What it does |
| --- | --- | --- | --- |
| 1 | `multi-hop` | Only if there are **anchors**: symbols or paths named in the question, or passed in `known_symbols`. | Asks the router with those anchors plus related symbols remembered from earlier good results. |
| 2 | `open-ended` | Always attempted next. | Plain retrieval from the question alone. |
| 3 | `anchored-from-open-ended` | If any anchors exist, including ones *derived* from stage 2's result. | Asks again with those anchors, so a vague question becomes a concrete one. |
| 4 | `lexical` | If nothing above scored well. | Text search through the files on disk. See [Memory And Lexical Fallback](Memory-And-Lexical-Fallback.md). |

After each stage the result is scored (see [Anchors And Scoring](Anchors-And-Scoring.md)). The first stage at or above `MinQuality` wins. If none reaches it, the best-scoring result is returned anyway. A lexical result is prefixed with a note, "Semantic retrieval was low-confidence for this query, so these are lexical (text-search) matches", so the agent knows it is reading search output, not a ranked answer. If every stage produced nothing, the reply is a message that suggests trying exact symbol names or file paths.

Only results that clear the quality bar are fed into the project memory. A fallback result never teaches the memory anything.

The stages respect the request: the server checks for cancellation between stages, and the lexical stage runs under its own timeout so a huge repository cannot stall the request.

### How the router is called

The router has one public entry point for this purpose, so the retriever adapter passes anchors by appending a line to the question: `Key symbols: A, B, C`. The router's own symbol detection then picks them up. This is the "implicit contract" noted in the package README: changing the router's prompt format can silently break stages 1 and 3. A `Retriever` interface (`OpenEnded`, `Anchored`, `MultiHop`) separates the cascade from the router, so tests can plug in canned results.

### Main Components

| Item | Responsibility |
| --- | --- |
| `retrieveLocked` | Runs the cascade. |
| `Stage` | The four stage names. |
| `StrategyConfig` | Tuning values. |
| `RetrievalTrace`, `StageAttempt` | A record of what each stage scored, for logs. |
| `Retriever`, `routerRetriever` | The seam to the router. |
| `composeAnchored` | Appends the `Key symbols:` line. |

### Data In, Data Out

In: the question, optional `known_symbols`, and whether to include reasoning instructions. Out: text and a trace such as `anchors=[applyBudget] path=[multi-hop(0.42) -> open-ended(0.38) -> lexical(0.51)] chosen=lexical`.

## Key Algorithms

### Anchors decide whether stage 1 runs

**The problem.** A vague, English-language question has nothing for a symbol-graph search to hold on to.

**The approach.** Stage 1 is skipped unless there is at least one anchor. The server's memory only enriches stage 1 with neighbours; it never switches stage 1 on by itself.

**Why this way.** What the agent *knows* should decide the strategy, not what the server happens to remember.

### Stop at the first good result

Stopping early saves time, especially because each router call may involve embedding the question and scoring thousands of chunks. The price is that a later stage might have scored higher. The cascade accepts that trade, because the early stages are the more precise ones.

## Configuration

`StrategyConfig` fields, with the defaults that apply when a field is zero:

| Field | Default | Meaning |
| --- | --- | --- |
| `MinQuality` | 0.55 | Score needed to stop. |
| `MaxExpansion` | 8 | Remembered neighbours added to a multi-hop query. |
| `MaxDerivedAnchors` | 8 | Anchors derived from an open-ended result. |
| `MaxMemorySymbols` | 4000 | Size of the per-project memory. |
| `LexicalTimeout` | 10s | Limit on the text search. |
| `LexicalMaxFiles` | 8 | Files in a lexical result. |
| `LexicalMaxBytes` | 24,000 | Size cap of a lexical result. |

## Usage Examples

Every served query logs one line to standard error with the trace (`query served repo=... retrieval="..."`). With `DebugConfig` on, `.eulix/debug/mcp-query-debug.log` also records each query, its output and the trace.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The trace goes to standard error, which some clients hide. | Standard output carries the protocol. | Check the client's server log, or enable `DebugConfig` for the file. |
| The scoring is heuristic. | See [Anchors And Scoring](Anchors-And-Scoring.md). | Adjust `MinQuality`. |
| Early results can be returned even when a later stage would be better. | First-good-result stopping. | Raise `MinQuality`. |

## Related Docs

- [Anchors And Scoring](Anchors-And-Scoring.md): the judge.
- [Memory And Lexical Fallback](Memory-And-Lexical-Fallback.md): stage 4 and the memory.
- [query Overview](../query/Overview.md): the router stages 1 to 3 call.
