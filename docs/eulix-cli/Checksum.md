# checksum

The `checksum` package works out how much of a project has changed since the last analysis, without reading any file contents.

> **Crate / path:** `eulix-cli/internal/checksum/detector.go`  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

`eulix analyze` and `eulix chat` both need to know whether the knowledge base still matches the source code. This package answers that with a single number, the **change ratio**: the share of files that were added, deleted or modified. It does so by comparing file sizes and modification times against a baseline that the parser wrote, so the check takes a moment even on a huge project.

## Why It Exists

Re-parsing a repository to find out whether it changed would defeat the purpose. A cheap check on file metadata is enough to decide between "up to date", "slightly stale, warn the user" and "stale, re-analyze".

## How It Works

`Run()` loads the configuration, reads the baseline `.eulix/checksum.json.zst` (compressed with zstd), scans the project, and compares the two. If the baseline is missing or unreadable, it reports a first run with a change ratio of 1.0, meaning "everything changed".

The scan walks the project, skips anything matched by `.euignore` and always skips `.eulix/`, and records each file's size and modification time. The file extensions to include are not hard-coded; they are taken from the baseline itself, so the scan looks at exactly the kinds of files the parser looked at.

The per-file hash is the 64-bit xxh3 hash of the size and modification time, each as a little-endian 64-bit number. This **must** match the Rust file walker's calculation exactly, because the two programs compare each other's output. See [Public API](../libeulix/Public-API.md) for the walker.

The comparison counts files that are new, gone, or whose hash differs. The change ratio is that total divided by the larger of the old and new file counts, capped at 1.

### Main Components

| Item | Responsibility |
| --- | --- |
| `Run()` | The one entry point; returns a `Result`. |
| `Detector` | Loads ignore rules, scans, loads and saves the checksum. |
| `Result` | `ChangedRatio`, counts of added, deleted and modified files, and `FirstRun`. |
| `IgnoreMatcher`, `ServableMatcher` | Reusable `.euignore` matchers, also used by other packages. |
| `MetaHash` | The hash both programs share. |

### Data In, Data Out

In: the project tree and `checksum.json.zst`. Out: a `Result`. The checksum file records the project path, the total files and lines, a combined hash and per-file entries.

## Key Algorithms

### Reading `.euignore`

**The problem.** The checksum must ignore exactly what the parser ignores, or the two will disagree and the change ratio will be wrong.

**The approach.** The package has its own implementation of `.gitignore`-style patterns: comments, negation, anchored paths, `**` wildcards, trailing-slash directories, and nested `.euignore` files that apply to their own sub-tree. Pattern matching is translated to regular expressions where needed.

**Why this way.** Both programs need to treat `**` and nested files identically. A recent hardening change reads `.euignore` fully (including very long lines and Windows line endings), and the Go side mirrors the Rust side.

### Dividing by the larger count

Using the larger of the two file counts keeps the ratio between 0 and 1 whether a project grew or shrank. Dividing by the old count alone would let a project that doubled in size report 100% change from a handful of added files, and one that was almost entirely deleted look barely changed.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `.euignore` | project root, plus nested ones | written by `init` | Which paths are left out. Must be kept in sync with what the parser sees. |
| `checksum.*` thresholds | `eulix.toml` | 0.10 and 0.30 | Currently unused. |

## Usage Examples

The package is used internally by `analyze` and `chat`. The `eulix checksum` command is deprecated and prints a notice.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| A touched file counts as modified. | Only size and time are compared. | None; this is the price of not reading contents. |
| The checksum directory is fixed at `.eulix/`. | A source comment notes the TODO to follow the parser's output path. | Keep the default output folder. |
| The baseline is created by the parser, not by this package. | `Run()` never writes a first baseline itself in the normal flow. | Run `eulix analyze`. |

## Related Docs

- [Cli](Cli.md): how `analyze` and `chat` use the result.
- [Public API](../libeulix/Public-API.md): the Rust file walker with the matching hash.
- [Output Files](../eulix-parser/Output-Files.md): where `checksum.json.zst` is written.
