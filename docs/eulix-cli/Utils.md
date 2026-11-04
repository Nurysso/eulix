# utils

The `utils` package holds the constants, shared types, logging and small helpers that several other packages need.

> **Crate / path:** `eulix-cli/internal/utils/`  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

A package of shared odds and ends usually means "things that had nowhere better to go". Here it has four clear jobs: naming the files and folders the program uses, mirroring the knowledge-base data structures for the Go side, writing a debug log, and a few text helpers. The package depends only on the standard library and the terminal styling library, so every other package can import it without creating a cycle.

## Why It Exists

Constants such as `.eulix` and `checksum.json.zst` appear in many places, and a typo in one of them is a hard bug to find. Keeping them in one file removes that risk. The data types are separate from the Rust ones because Go reads the parser's JSON; they must match the output, not the Rust source.

## How It Works

### Constants

`constansts.go` (the spelling is in the file name) defines the app name and version (`eulix`, `v0.9.0`), the project folder `.eulix`, the global folder `.Eulix`, the file names `eulix.toml`, `.euignore`, `.env` and `checksum.json.zst`, the binary-file magic (`EULX`) and version (5), and the required Python version (3.11).

### Knowledge-base types

`KBStruct.go` defines the Go reading side of the parser's output: `KnowledgeBaseSimplifiedRef`, `IndexDataRef`, `CallGraphRef`, `EntryPointsRef`, `ExternalDependencyRef`, `PatternsRef`, `MetricsReport` and the records inside them (`FileData`, `KBFunction`, `KBClass`, `CallGraphNode`, `CallGraphEdge`, `Indices`, and others). They are deliberately the *simplified* shape that `eulix_parser` writes, which leaves out calls, control flow and similar data. See [Output Files](../eulix-parser/Output-Files.md).

### Context types

`ContextChunk` (a file, a line range, the text, an importance score and a token count) and `ContextWindow` (the chunks chosen for a question, the total tokens and the source files) are the objects passed from retrieval to the prompt builder.

### Reasoning split

`SplitReasoningAndAnswer` takes a model reply and separates a `<reasoning>` (or `<thinking>`) block from an `<answer>` block. If there is no `<answer>` tag, everything outside the reasoning block is treated as the answer, so untagged replies still work. The chat screen, the history and the router all use the same function so they agree.

### Token counting

`CountTokens(text, provider)` uses a real tokenizer (`cl100k_base`) for OpenAI, Anthropic and OpenRouter. For every other provider, and if the tokenizer fails, it estimates one token per four characters.

### Debug logger

`DebugLogger` is a thread-safe, buffered log writer. `NewDebugLogger(eulixDir)` creates `.eulix/debug/context_debug.log`, truncating it each run, with a 64 KiB buffer and an optional automatic flush every few seconds. If the file cannot be created, it falls back to an in-memory buffer and never panics. Setting the environment variable `debug=3` turns on extra-detailed messages in some places.

### Colours

`theme.go` lists the palette used by the terminal interface (teal accents, violet titles, amber warnings, and so on).

### Main Components

| File | Responsibility |
| --- | --- |
| `constansts.go` | Names, version, magic numbers. |
| `KBStruct.go` | Go types for the parser's JSON. |
| `utils.go` | Context types, `KnownExtensions`, reasoning split. |
| `tokenizer.go` | `CountTokens`. |
| `debugger.go` | `DebugLogger`. |
| `theme.go` | Colours. |

### Data In, Data Out

Mostly types and pure functions: text in, text or numbers out.

## Key Algorithms

None. `CountTokens` is the closest thing, and it is deliberately simple.

## Configuration

No settings, except the `debug=3` environment variable.

## Usage Examples

```go
reasoning, answer := utils.SplitReasoningAndAnswer(reply)
n := utils.CountTokens(prompt, cfg.LLM.Provider)
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Token counts are estimates for most providers. | No tokenizer for them. | Leave headroom in `max_tokens`. |
| `AppVersion` is a constant in code. | It is not read from a build flag. | Update it with each release. |
| `KnownExtensions` includes languages the parser does not parse (Ruby, Swift, Kotlin, C#). | It is a broad list used for display and file filtering. | None. |
| The debug log is truncated on every run. | Opened with truncate. | Copy it before the next run. |

## Related Docs

- [Output Files](../eulix-parser/Output-Files.md): what the types describe.
- [query](query/Overview.md): the main user of `ContextWindow`.
