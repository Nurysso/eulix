# eulix-cli

`eulix-cli` is the Go program you actually type, `eulix`. It runs the parser and the embedding tool, answers questions about your code, and ties every other Eulix part together.

> **Crate / path:** `eulix-cli/` (module `eulix`, binary `eulix`)  
> **Language:** Go 1.26  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

The other Eulix subsystems each do one job: `eulix_parser` reads source code, `eulix_embed` turns code into vectors, and `libeulix` understands relationships. None of them talks to a person. `eulix` is the part that does. It sets up a project, runs the parser and the embedder when the code has changed, finds the code relevant to a question, builds a prompt, and either shows it to you, sends it to a language model, or hands it to an AI coding agent over the Model Context Protocol (MCP).

The binary is self-contained. The parser and the Python embedding scripts are packed inside it at build time and unpacked into `~/.Eulix/` on first run, so installing `eulix` installs everything.

After reading this page you will know what each command does, how a question travels through the program, and which page describes each part. The code is organized as one package per concern, and each has its own page in this folder. The two large areas, `mcp` and `query`, have their own sub-folders.

## Why It Exists

Retrieval is the hard part of answering questions about a large codebase. Pasting the whole repository into a model is impossible, and a plain keyword search misses code that does not share words with the question. `eulix-cli` is where Eulix's answer to that lives: it reads the knowledge base the parser produced, picks the handful of code chunks that matter, fits them into the model's token budget, and writes a prompt that tells the model how to stay honest about what it can and cannot see.

It is also the single place where the user's configuration (`eulix.toml`) is read, so every other part behaves the same way for a given project.

## How It Works

### Start-up

Before any command runs, `main.go` makes sure the machine is ready:

1. It finds `~/.Eulix/`.
2. It verifies the embedded assets there, extracting the parser and the embedding scripts if they are missing. A parser binary whose hash does not match the one baked into `eulix` stops the program (see [Assets](Assets.md)).
3. On the very first run only, it checks for the `uv` tool, checks for a Python 3.11 virtual environment, and installs the embedding tool's Python dependencies.

Then it hands control to the command parser.

### From code to answer

```text
eulix analyze                      eulix chat / query / mcp
     │                                      │
     ▼                                      ▼
checksum: what changed?            classifier: what kind of question?
     │                                      │
     ▼                                      ├── factual (location, usage, call graph, ...)
eulix_parser  → kb.json, ...                │      answered directly from the knowledge base
     │                                      │
     ▼                                      └── needs understanding
eulix_embed   → embeddings.bin, ...                │
                                                   ▼
                                      retrieval: find and rank code chunks
                                                   │
                                                   ▼
                                      prompt: context + reasoning instructions
                                                   │
                                          ┌────────┴────────┐
                                          ▼                 ▼
                                    shown as text      sent to the LLM
```

`eulix analyze` writes everything into a `.eulix/` folder in your project. The question-answering commands read from it. If the code changed a lot since the last analysis, `eulix chat` warns you or refuses to start.

### Commands

These are the commands registered in the code today.

| Command | What it does |
| --- | --- |
| `init` (`fix`, `force`) | Creates `.eulix/`, `.euignore` and `eulix.toml`, with an interactive wizard. |
| `analyze` | Runs the parser and the embedder, but only when something changed. |
| `chat` | Opens the interactive terminal interface. |
| `query "<question>"` | Builds the context prompt for a question and prints it. It does **not** call a language model. |
| `mcp [path]` | Starts the MCP server for AI coding agents. Beta. |
| `history` (`list`, `show`, `view`, `delete`, `clear`) | Browses recorded questions and answers. `cache` is deprecated and only prints a notice, like `checksum`. |
| `vizeulize` | Opens the call-graph explorer. See [vizEulize](../vizEulize/Overview.md). |
| `checksum` | Deprecated; prints a notice. |
| `config` | Placeholder; prints "coming soon". |
| `version` | Shows the versions of `eulix`, `eulix_parser` and `eulix_embed`. |
| `verifyBins`, `getEmbedDeps`, `printEmbedDeps` | Maintenance commands for the embedded assets and the Python environment. |
| `embed [args]`, `parser [args]` | Pass-through wrappers around the embedding tool and the parser. |

The `glados` and `aspirine` repair commands are not registered. Their definitions exist but are commented out of the registration list, and they print "deprecated" if reached. See [Fixers](Fixers.md).

### Main Components

| Package | Page | Responsibility |
| --- | --- | --- |
| `cmd/eulix` | this page | Entry point and first-run set-up. |
| `internal/cli` | [Cli](Cli.md) | Every command, and the `init` wizard. |
| `internal/config` | [Config](Config.md) | Reads and validates `eulix.toml`. |
| `internal/assets` | [Assets](Assets.md) | Embedded parser and scripts, integrity checks. |
| `internal/checksum` | [Checksum](Checksum.md) | Detects how much of the project changed. |
| `internal/embeddings` | [Embeddings](Embeddings.md) | Runs the embedding tool, including the live query embedder. |
| `internal/query` | [query](query/Overview.md) | Classifies questions and routes them. Contains `classifier`, `retrieval` and `strip`. |
| `internal/llm` | [Llm](Llm.md) | Talks to language-model providers. |
| `internal/mcp` | [mcp](mcp/Overview.md) | The MCP server. |
| `internal/tui` | [Tui](tui/Overview.md) | The chat and history screens. |
| `internal/cache` | [Cache](Cache.md) | Query history database. |
| `internal/utils` | [Utils](Utils.md) | Shared constants, types, logging and colours. |
| `internal/fixers` | [Fixers](Fixers.md) | Dormant repair tools. |

### Data In, Data Out

On disk, a project gets a `.eulix/` folder. These are the files the question-answering code needs, and `chat` refuses to start without them:

| File | Made by | Used for |
| --- | --- | --- |
| `kb.json` | parser | The knowledge base; the retrieval chunks come from it. |
| `kb_index.json` | parser | Lookup indices; also trains the classifier's symbol list. |
| `kb_call_graph.json` | parser | Call relationships. |
| `embeddings.bin`, `vectors.bin` | embedder | Vectors and the chunk-id map. Only the default retrieval engine reads them. |
| `checksum.json.zst` | parser | The baseline for change detection. |
| `analysis-complete.json` | `eulix` | Marks a finished analysis. |
| `history.db` | `eulix` | Query history. |
| `debug/` | `eulix` | Debug logs, when debugging is on. |

Global files live in `~/.Eulix/`: `bin/eulix_parser`, `eulix_embed/`, `.venv/`, and a marker file recording that set-up finished.

## Key Algorithms

The interesting algorithms are in two places. Retrieval, the process of choosing which code to show the model, is described in [Retrieval](query/retrieval/Overview.md), with two selectable engines described in [Engines](query/retrieval/Engines.md). Cooperation with AI coding agents is described in [MCP](mcp/Overview.md).

## Configuration

Everything is configured in `eulix.toml`, written by `eulix init`. The keys and their validation are in [Config](Config.md). API keys are not stored in that file; they come from environment variables or a `.env` file.

## Usage Examples

```bash
eulix init            # once per project
eulix analyze         # build the knowledge base
eulix chat            # ask questions interactively
eulix query "who calls applyBudget?"   # print the context prompt
eulix mcp --repo .    # serve an AI coding agent
```

Expected result: after `analyze`, a `.eulix/` folder with the files above; after `chat`, a full-screen terminal interface.

## Performance Notes

The retrieval engine memory-maps large files and streams the knowledge base instead of loading it whole; see [Mmap](query/retrieval/Mmap.md) and [Index Loading](query/retrieval/Index-Loading.md). The MCP server keeps the loaded index in memory between questions and frees it after five idle minutes by default.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| `eulix query` prints a prompt and does not answer. | It calls `PromptOrAnswer`, which returns the context and reasoning instructions for most question types. | Use `eulix chat` to get an answer from the model. |
| `config` and `checksum` commands do nothing useful. | One is a placeholder and one is deprecated. | Edit `eulix.toml` directly. |
| The `[checksum]` thresholds in `eulix.toml` are not used. | `chat` uses fixed values of 10% and 30%. | Treat them as fixed for now. |
| Replies are not streamed. | The LLM client waits for the full response. | None. |
| First run needs `uv` and network access. | The Python environment is built on demand. | Install `uv` first. |

## Related Docs

- [Cli](Cli.md): the commands in detail.
- [Config](Config.md): every setting.
- [Retrieval Overview](query/retrieval/Overview.md): how context is chosen.
- [MCP Overview](mcp/Overview.md): serving AI coding agents.
- [eulix-parser Overview](../eulix-parser/Overview.md): the program `analyze` runs first.
- [CLI, MCP And TUI issues](../Known-Issues/CLI-MCP-And-TUI.md) and [Retrieval And Query issues](../Known-Issues/Retrieval-And-Query.md): current problems.
