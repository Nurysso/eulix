# MCP Memory And Lexical Fallback

This page covers the two supporting parts of the MCP retrieval cascade: the small per-project memory, and the plain text search that runs when everything else fails.

> **Crate / path:** `eulix-cli/internal/mcp/strategy_memory.go`, `strategy_lexical.go`, `hidden_*.go`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

The memory helps the cascade get better as a session goes on. The lexical fallback guarantees the agent gets *something* useful when the indexed search comes up empty. Both are deliberately modest. Neither is allowed to decide the search strategy by itself.

## Why It Exists

Indexed retrieval can miss in two ways: the question has no extractable symbols, or the index does not know about the code (it is stale, or the file was never indexed). A text search over the real files covers both. The memory exists because symbols that appeared together in a good answer are probably related, and remembering that helps with the next, related question.

## How It Works

### Project memory

`projectMemory` remembers which symbols appeared in *trusted* results (those that cleared `MinQuality`) and which appeared together. When a trusted result arrives, it is split into blocks at blank lines. Every symbol-like token in a block is recorded, and tokens in the same block are marked as neighbours (each symbol keeps at most 8 neighbours). Counts and recency are updated.

It does two jobs:

- **Expand** (`expand`): given the anchors of a multi-hop query, it returns remembered neighbours, ranked by how often they occur with the anchors and how prominent they are. The anchors' own keys are excluded.
- **Derive** (`derive`): given an open-ended result, it picks symbols whose name parts overlap the question's words, weighting overlap first, then how often the symbol appears in the result, then its history in memory. These become the anchors for the follow-up anchored search.

The memory holds at most 4,000 symbols. When full, it trims back to about 90%, removing the least used and least recently seen first. It lives as long as the server and survives the idle release of the index, but a different repository gets its own memory.

### Lexical fallback

`lexicalSearch` is stage 4. It walks the repository and ranks files by weighted word counts:

- Anchor keys weigh 3, other question words weigh 1. Words shorter than three characters are ignored.
- A word's count in a file saturates at 5, so a repetitive file cannot dominate.
- A word that appears in the file's *path* adds twice its weight.

The best files (8 by default) are shown as windows of lines around the best-scoring lines, up to 24,000 bytes in all.

**What it will not read.** The walk skips common build and dependency directories (`node_modules`, `vendor`, `dist`, `build`, `target`, `__pycache__`, `venv`, `coverage`), hidden files and directories, anything matched by `.euignore`, paths in `retrievalConfig.etiya.lexical_exclude`, files over 1 MiB, empty files, binary files (a NUL byte in the first 8 KiB) and anything that is not a regular file. Hidden files are skipped because they often hold credentials; `.euignore` files were left out on purpose, so the fallback respects that too. On Windows, "hidden" includes the NTFS hidden attribute, not just a leading dot.

**Bounds.** At most 20,000 files are inspected. The walk stops at its time limit (10 seconds) and returns what it has; only a cancelled request aborts it. A symbolically linked repository root is resolved first, since the directory walker does not enter a symlink.

### Main Components

| Item | Responsibility |
| --- | --- |
| `projectMemory.Observe` | Learn from a trusted result. |
| `projectMemory.expand`, `derive` | Suggest anchors. |
| `lexicalSearch` | The file walk and ranking. |
| `bestWindows` | Picks non-overlapping line windows. |
| `isHidden`, `hiddenByAttr` | Hidden-file checks per platform. |

### Data In, Data Out

In: result text (memory) or the repository and the question (lexical). Out: remembered symbols, or rendered line windows with file names.

## Key Algorithms

### Saturating counts

**The problem.** A generated file with the same word a thousand times would outrank the one file that actually explains the code.

**The approach.** A word's contribution per file is capped at five occurrences.

**Why this way.** It keeps ranking about *breadth* of matching words, not repetition.

### Trust only what was good

Memory only learns from results that scored well, and lexical results never update it. A bad result therefore cannot teach the memory wrong associations.

## Configuration

Values come from `StrategyConfig`; see [Retrieval Cascade](Retrieval-Cascade.md). The path exclusions come from `retrievalConfig.etiya.lexical_exclude` in `eulix.toml`, and are applied only when the retrieval engine is `etiya`. Entries are globs matched against the repo-relative path or the file name, and `dir/` skips a directory.

## Usage Examples

```toml
[retrievalConfig.etiya]
lexical_exclude = ["backup/", "*.min.js", "third_party/"]
```

Expected result: the MCP text search no longer reads those paths.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The fallback does not remove duplicate content across files. | It ranks files independently. | Exclude backup trees with `lexical_exclude`. |
| Exclusions apply only with the `etiya` engine. | The server reads the setting only in that case. | Use `.euignore` instead, which applies always. |
| Memory is lost when the server stops. | It is in memory only. | None. |
| The fallback reads files from disk, not from the index. | That is its purpose. | None. |

## Related Docs

- [Retrieval Cascade](Retrieval-Cascade.md): where these fit.
- [Anchors And Scoring](Anchors-And-Scoring.md): how results are judged.
- [Checksum](../Checksum.md): the `.euignore` matcher used by the walk.
