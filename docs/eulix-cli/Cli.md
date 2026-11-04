# cli

The `cli` package defines every `eulix` command and the set-up wizard behind `eulix init`.

> **Crate / path:** `eulix-cli/internal/cli/` and `eulix-cli/internal/cli/setup/`  
> **Language:** Go  
> **Status:** stable, except where noted  
> **Last verified against:** `ad744e9`

## Overview

This package is the front door. It uses the `cobra` command framework to define commands, checks that a project is ready for each one, and then calls into the other packages to do the work. It contains very little logic of its own; where logic exists, it is mostly about being safe: not running on half-initialized projects, not re-running slow work that is already done.

## Why It Exists

Every command needs the same few checks: are you inside a Eulix project, is it set up, is the knowledge base there? Putting those checks in one place keeps the commands short and makes the error messages consistent.

## How It Works

### Finding the project

Most commands first call `requireProjectRoot`. It starts at the current directory and walks upward looking for a `.eulix/` folder or a `.euignore` file. When it finds one, it changes into that directory, so you can run `eulix` from any sub-folder of a project. If it reaches the filesystem root without finding a marker, it tells you to run `eulix init`.

### `init`

`eulix init` creates three things: the `.eulix/` folder, a starter `.euignore`, and `eulix.toml`. It then walks you through an interactive wizard for the ignore file and the configuration. Three modes exist:

- `eulix init` creates what is missing. If the project is partly set up, it lists what is absent and points at the next two options.
- `eulix init fix` creates only the missing pieces and leaves everything else alone.
- `eulix init force` rewrites the configuration, ignore file and `.eulix/`. It leaves `.env` untouched.

The wizard lives in `cli/setup`. It is a small step framework: each question is a `Step` with a key, a kind (text, select, confirm or password), and optional functions to skip itself or decide the next question. This is how the wizard asks for a base URL only for a local model, and for an API key only for a cloud provider. API keys the wizard collects are written to `.env` through `EnvFile`, which edits only the keys that changed and keeps comments and other lines intact.

### `analyze`

`analyze` is the command that does the expensive work, and most of its code is about avoiding it.

1. Load the configuration and run the checksum comparison ([Checksum](Checksum.md)).
2. If nothing changed *and* the last analysis completed with the same settings, say so and stop.
3. Otherwise delete the old completion marker, run the parser, run the embedder, and write a new marker.

The marker is `analysis-complete.json`. It records the embedding model, the embedding engine and the PRISM version used, plus the size and modification time of six output files (the checksum, `kb.json`, `kb_index.json`, `kb_call_graph.json`, `embeddings.bin`, `vectors.bin`). The analysis is treated as complete only if those settings are unchanged and every file is still present, non-empty and unmodified. The marker is written to a temporary file and renamed, so a crash cannot leave a half-written marker that looks valid. API keys are deliberately not recorded in it.

The parser is run from the embedded binary with `--root`, `-o .eulix/kb.json`, `--threads` and `--prism` from your configuration, plus `--verbose` if enabled. The embedder is run through the Python virtual environment with `embed -k kb.json -o .eulix -m <model> --quantize -e <engine>`. Because the parser keeps its own incremental cache ([Incremental Parsing](../Architecture/Incremental-Parsing.md)), re-running `analyze` after a small change is much faster than the first run.

### `chat`

`chat` is careful before it starts. It checks that the knowledge base and all five data files exist, runs the checksum comparison, and then:

- above **30%** change it refuses and asks you to run `analyze`;
- above **10%** change it warns and asks whether to continue;
- otherwise it opens the history database, builds the language-model client and the query router ([query](query/Overview.md)), and starts the terminal interface ([Tui](tui/Overview.md)).

### `query`

`query` builds a router and calls `PromptOrAnswer`. For questions that the classifier can answer from the knowledge base alone, it prints the answer. For the rest it prints the *prompt*: the retrieved context followed by reasoning instructions. It does not send anything to a model. If debugging is enabled, the question and output are appended to `.eulix/debug/query-debug.log`.

### `history`

`history` with no sub-command opens the terminal history viewer (or a plain list with `--no-tui`). `list`, `show <id>`, `view`, `delete <id>` and `clear` do what they say; deletions ask for confirmation. `cache` is a leftover alias. The data comes from [Cache](Cache.md).

### Other commands

`mcp` is described in [MCP](mcp/Overview.md). `vizeulize` starts a local web server for the call-graph explorer, as documented in [vizEulize](../vizEulize/Overview.md). `embed` and `parser` pass their arguments straight to the embedding tool and the parser. `getEmbedDeps`, `printEmbedDeps` and `verifyBins` call into [Assets](Assets.md).

### Main Components

| File | Responsibility |
| --- | --- |
| `commands.go` | Declares and registers all commands and their flags. |
| `init.go`, `setup/` | Project initialization and the wizard. |
| `analyze.go`, `analyze_state.go` | The analysis pipeline and its completion marker. |
| `chat.go` | Pre-flight checks and starting the terminal interface. |
| `history.go` | The history sub-commands. |
| `mcp.go` | The `mcp` command and its flags. |
| `helpers.go` | Project-root search, opening a browser, debug logging. |

### Data In, Data Out

Inputs are command-line arguments and `eulix.toml`. Outputs are files under `.eulix/` and terminal text.

## Key Algorithms

### Skipping work safely

**The problem.** Analysis is slow, so repeating it needlessly wastes minutes. But skipping it when it should have run gives stale or broken answers.

**The approach.** Two independent checks must both pass to skip. The checksum says the *source files* are unchanged since the parser last ran. The completion marker says the *outputs* of the last run are intact and were made with the *current settings*. An unchanged source with a changed embedding model, for example, still re-runs.

**Why this way.** The parser rewrites the checksum early in its run. If only the checksum were checked, a run that crashed during embedding would look finished on the next attempt. The marker is deleted before the pipeline starts and written only after it succeeds, so a crash leaves no marker and forces a retry.

## Configuration

The commands read `eulix.toml`; see [Config](Config.md). Flags specific to a command are shown by `eulix <command> --help`. The `mcp` command takes `--repo`, `--timeout` and `--cot`.

## Usage Examples

```bash
eulix init force        # start over, keeping .env
eulix analyze           # prints "No changes detected – skipping analysis." when up to date
eulix history --no-tui  # plain text list
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| `config` does nothing. | Placeholder. | Edit `eulix.toml`. |
| `chat`'s staleness limits are fixed. | The 10% and 30% values are written in code; the `[checksum]` settings are read but unused. | None yet. |
| `analyze` passes `--quantize` to the embedder unconditionally. | It is hard-coded. | None. |

## Related Docs

- [Overview](Overview.md): the command table.
- [Config](Config.md), [Checksum](Checksum.md), [Assets](Assets.md): packages this one calls.
- [query](query/Overview.md): what `chat` and `query` call.
