# MCP Anchors And Scoring

This page explains what an anchor is, how anchors are found, and how the server scores a result to decide whether to keep searching.

> **Crate / path:** `eulix-cli/internal/mcp/strategy_anchors.go`, `strategy_quality.go`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

The cascade needs a judge. After each search stage it must answer one question: is this result good enough to stop? The server cannot ask a language model, so it uses a small scoring formula built from simple evidence: did the result mention the specific things the question named, did it contain the question's important words, and does it look like real code? The "specific things" are called anchors.

## Why It Exists

A result can look plausible and still be about the wrong code. Counting how many of the question's anchors appear in the result is a cheap, surprisingly strong signal. Without a judge, the cascade could not decide when to stop, and would either always run every stage or always trust the first.

## How It Works

### What an anchor is

An anchor is a piece of text in a question that names something concrete: a file path, a qualified name like `SQLCompiler.get_from_clause`, a function call like `applyBudget(`, or an identifier-shaped word. `ExtractAnchors` finds them with regular expressions:

| Kind | Example | How it is recognized |
| --- | --- | --- |
| Backtick span | `` `parse_args` `` | Always treated as code. |
| Path | `internal/mcp/server.go`, `main.py` | Slashes with an extension, or a bare file name with a known extension. |
| Qualified name | `A.B`, `A::B`, `A->B`, `A#B`, optional `()` | Identifiers joined by separators. |
| Call | `foo(` | An identifier followed by an opening parenthesis. |
| Identifier | `snake_case`, `camelCase`, `PascalCase`, `SQLCompiler` | Looks like code, not an English word. |

Plain English words never qualify, which is what stops vague questions from being treated as anchored. Anchors are kept in the order they appear, without duplicates (ignoring case), and capped at 12 per question. A qualified name such as `a.b` adds its head segment too; very short segments disqualify the match, which filters out "e.g" and "i.e".

Each anchor carries a list of lower-case **keys**: substrings that count as evidence in a result. A path anchor has both the full path and the file name. A qualified name has the whole text and the last segment.

**Hints.** The `known_symbols` the agent supplies become anchors too, up to 20. They are used as extra seeds for searching, but are **never used for scoring**. They are context from the agent's session, not requirements the result must satisfy.

### How a result is scored

`assess(question, anchors, result)` returns a number between 0 and 1, built from three parts:

- **AnchorCov**: the fraction of the question's anchors for which at least one key appears in the result.
- **TermCov**: the fraction of the question's significant words that appear. Words come from `queryTerms`, which drops stopwords and very short tokens and splits identifiers into parts (`resolve_expression` becomes `resolve` and `expression`).
- **Rich**: evidence that this is real code: fenced code blocks and `file:line` references count directly (three or more is full marks), with a floor that grows with the length of the text, up to 0.6, so long answers are not penalized.

The score is a weighted sum:

```text
with anchors:     0.45 * AnchorCov + 0.35 * TermCov + 0.20 * Rich
without anchors:  0.65 * TermCov  + 0.35 * Rich
```

Some results are scored zero outright. A result shorter than 120 bytes counts as empty. A short result (under 800 bytes) that contains a "no result" phrase also counts as empty; the phrase check is skipped for longer text because such phrases can legitimately appear in a real answer.

Two details keep the score honest. Only the text *before* the `QUERY` line counts as evidence, because the router appends the question and reasoning header after the context. And any multi-word text the backend copied from the rewritten question is removed first, so echoing the question cannot raise the coverage. A one-word echo is left in, because it is usually the very symbol being looked up.

### Main Components

| Item | Responsibility |
| --- | --- |
| `Anchor` | Text plus evidence keys. |
| `ExtractAnchors` | Finds anchors in a question. |
| `anchorsFromHints` | Turns `known_symbols` into anchors. |
| `assess`, `quality` | Scoring and its breakdown. |
| `queryTerms`, `splitIdent` | Word extraction and identifier splitting. |

### Data In, Data Out

In: a question, anchors, and candidate text. Out: a `quality` value with `AnchorCov`, `TermCov`, `Rich` and `Score`.

## Key Algorithms

### Why the weights shift when there are no anchors

**The problem.** If the question names nothing concrete, anchor coverage is zero for every result, which would cap every score unfairly low.

**The approach.** Without anchors, the formula drops that term and spends its weight on word coverage and richness instead.

**Why this way.** It keeps scores comparable between precise and vague questions, so one `MinQuality` threshold works for both.

## Configuration

`MinQuality` (default 0.55) is the one value that interacts with the score. The weights are constants in code.

## Usage Examples

A question such as ``how does `applyBudget` split tokens?`` has one anchor, `applyBudget`. A result that mentions `applyBudget`, contains two fenced code blocks, and covers most of the other words will score about `0.45*1 + 0.35*0.8 + 0.20*0.67`, which is roughly 0.86, above the 0.55 bar.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The score rewards mentions, not correctness. | It measures coverage and shape, not meaning. | Treat a passing result as likely relevant, not proven. |
| Code in languages with unusual naming may not look like an anchor. | The identifier rules assume common conventions. | Quote the name in backticks. |
| Plain-English technical terms are not anchors. | By design, to avoid false anchoring. | Pass them in `known_symbols`. |

## Related Docs

- [Retrieval Cascade](Retrieval-Cascade.md): where the score is used.
- [Memory And Lexical Fallback](Memory-And-Lexical-Fallback.md): what happens after a low score.
