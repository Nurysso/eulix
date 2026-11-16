# MCP Server And Tools

This page describes the server object, the tools and prompts it exposes, and how it manages repositories and memory.

> **Crate / path:** `eulix-cli/internal/mcp/server.go`, `tools.go`, `helpers.go`, `lifecycle.go`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

The `Server` type wraps the official MCP SDK. The SDK handles the protocol itself: JSON-RPC framing, the start-up handshake, capability negotiation, schema checking, ping and cancellation. This package adds what is specific to Eulix: which tools exist, how a repository is chosen, and when memory is released.

## Why It Exists

Keeping protocol handling in the SDK and policy in this package keeps the code small, and means protocol fixes arrive with SDK upgrades.

## How It Works

### Tools

| Tool | Arguments | What it does |
| --- | --- | --- |
| `query` | `query` (required), `repo_path`, `include_cot`, `known_symbols` | The general retrieval tool. `include_cot` defaults to the server's `--cot` setting. |
| `query_with_cot` | `query`, `repo_path`, `known_symbols` | Same, with reasoning instructions forced on. |
| `query_without_cot` | `query`, `repo_path`, `known_symbols` | Same, with clean context only, forced. |
| `set_repo_path` | `repo_path` (required) | Switches the active repository. Idempotent. |

The three query tools are marked read-only. Their descriptions tell the assistant to include exact symbol names and file paths whenever it knows them, and to pass `known_symbols` for anything it has already seen in the session. That is the main way the assistant can improve results.

Two **prompts** exist with the same names as the CoT tools (`query_with_cot`, `query_without_cot`). A prompt is a template an editor can offer to the user directly; these take `query`, `repo_path` and a comma-separated `known_symbols` and return the context as a user message.

*Chain of thought* (CoT) here means the reasoning instructions and honesty contract that the router normally adds to a prompt ([Prompts](../query/Prompts.md)). With CoT off, the reply is context only, suitable for an assistant that has its own instructions.

### Choosing a repository

The server can serve many repositories, one at a time. `setRepoPathLocked` does the following:

1. Resolve the path to an absolute one and check that it exists and is a directory.
2. Check that `.eulix/kb.json` exists; if not, return an error telling the user to run `eulix analyze`.
3. If this repository is already active, say so and stop.
4. Load that project's `eulix.toml` (with `config.LoadFrom`), create the language-model client, and create the query router. The history database is **not** opened, because an open `history.db` stays locked and would break `eulix chat` and `eulix history` while the server runs.
5. Only after all of that succeeds, close the previous router and switch. A failure leaves the old, working repository active.

If no repository is set when the client finishes connecting, the server asks the client for its "roots" (the open workspace folders) in the background, with a 5-second timeout, and binds the first usable one. The bind and the check share one lock, so a `set_repo_path` that arrives in between is never overwritten.

### Running a query

`ExecuteQuery` takes the server lock, switches repository if `repo_path` was given, resets the idle timer, runs the [retrieval cascade](Retrieval-Cascade.md), and returns the text. Switching and querying happen under one lock, so concurrent calls can never query the wrong repository. Queries are serialized: one at a time.

### Idle release

A timer is re-armed on every activity. When it fires, the server re-checks that the idle time has really elapsed (a query may have just reset it) and then closes the router's context builder, freeing the index. The router itself and the per-project memory stay.

### Lifecycle

`Serve` starts the standard-input/output transport and runs until the client disconnects or the context is cancelled. `Close` stops the timer and closes the router, and is safe to call repeatedly. `eulix mcp` cancels on Ctrl-C and SIGTERM so memory is freed.

### Main Components

| Item | Responsibility |
| --- | --- |
| `NewServer` | Builds a server from options and registers tools and prompts. |
| `Option` functions | `WithIdleTimeout`, `WithDefaultCoT`, `WithInitialRepo`, `WithIO`, `WithLogger`, `WithStrategy`, `WithRetriever`. |
| `SetRepoPath`, `ExecuteQuery`, `GetActiveRepo` | The public methods. |
| `onInitialized`, `bindFromRoots` | Binding from the client's roots. |

### Data In, Data Out

Tool arguments are Go structs; the SDK derives each tool's JSON schema from the struct tags and validates input before the handlers run. Replies are a single text block.

## Key Algorithms

### Switch only after the new thing works

**The problem.** If switching repositories tore down the old router first, a typo in the new path would leave the server with nothing.

**The approach.** Build the new router completely, then close the old one.

**Why this way.** A failed switch costs the agent one error message, not its whole session.

## Configuration

| Option | Purpose |
| --- | --- |
| `WithIdleTimeout(d)` | Idle time before the index is freed (5 minutes by default). |
| `WithDefaultCoT(bool)` | Default for the generic `query` tool. |
| `WithInitialRepo(path)` | Repository to bind at start-up. |
| `WithIO(in, out)` | Custom streams, for tests. |
| `WithLogger(l)` | Custom `slog.Logger`; the default writes to standard error. Logs must never go to standard output. |
| `WithStrategy(cfg)` | Tune the cascade. |
| `WithRetriever(r)` | Replace the router-backed retriever, for tests. |

## Usage Examples

An agent that wants to switch projects and then ask a question:

```json
{"name": "set_repo_path", "arguments": {"repo_path": "/home/me/projects/api"}}
{"name": "query", "arguments": {"query": "where is the token validated?", "known_symbols": ["AuthMiddleware"]}}
```

Expected result: a confirmation, then retrieved source chunks with `file:line` references.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| One query at a time. | A single lock serializes all calls. | None needed for one agent. |
| Only one repository is loaded at once. | Switching closes the previous one. | Use `set_repo_path` between questions. |
| When debugging is on, each query is appended to `.eulix/debug/mcp-query-debug.log`. | Logging is per repository. | Turn `DebugConfig` off in production. |
| The "roots" feature the server relies on is marked deprecated in the SDK. | The code carries a lint suppression for it. | Use `--repo` or `set_repo_path` to avoid relying on it. |

## Related Docs

- [Overview](Overview.md): what the server is for.
- [Retrieval Cascade](Retrieval-Cascade.md): what `ExecuteQuery` runs.
- [query Overview](../query/Overview.md): the router created per repository.
