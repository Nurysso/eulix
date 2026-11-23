# MCP Server And Tools

This page describes the server object, the tools and prompts it exposes, and how it manages repositories and memory.

> **Crate / path:** `eulix-cli/internal/mcp/server.go`, `agent_tools.go`, `agent_render.go`, `tools.go`, `helpers.go`, `lifecycle.go`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `0a78aa7` + working tree (agent improvements)

## Overview

The `Server` type wraps the official MCP SDK. The SDK handles the protocol itself: JSON-RPC framing, the start-up handshake, capability negotiation, schema checking, ping and cancellation. This package adds what is specific to Eulix: which tools exist, how a repository is chosen, and when memory is released.

## Why It Exists

Keeping protocol handling in the SDK and policy in this package keeps the code small, and means protocol fixes arrive with SDK upgrades.

## How It Works

### Tools

Structural questions have typed tools, answered from the call graph and the symbol index without going through the keyword classifier. Open questions go to `search`.

| Tool | Arguments | What it does |
| --- | --- | --- |
| `callers` | `symbol` (required), `depth` (1–4, default 1), `limit` (default 40), `repo_path` | One line per call site: `file:line`, the enclosing function and the calling line. Test call sites are counted on a separate line. |
| `callees` | same as `callers` | The calls a function makes, in the same format. |
| `definition` | `symbol` (required), `body_lines` (default 40, 0 = signature only), `repo_path` | Location, signature, the first docstring paragraph and a short body. A class comes back as its header and method list, never its whole body. |
| `trace` | `from`, `to`, `question`, `max_tokens`, `repo_path` | The shortest static call path from `from` to `to` (or, with only `to`, back to an entry point), one hop per line with the calling line. Then ranked code for the hops, within the budget. |
| `search` | `question` (required), `known_symbols`, `max_tokens`, `format` (`plain` or `cot`), `fresh`, `repo_path` | Ranked code for a concept or open question. |
| `query` | `query` (required), `repo_path`, `include_cot`, `known_symbols`, `max_tokens` | The free-text tool of earlier releases. Callers and definition questions are answered from the index first, as by the typed tools; the rest runs like `search`. |
| `query_without_cot`, `query_with_cot` | `query`, `repo_path`, `known_symbols` | Deprecated. They keep the pre-budget output unchanged, because the retrieval benchmark compares releases on `query_without_cot`. |
| `set_repo_path` | `repo_path` (required) | Switches the active repository. Idempotent. |

All query tools are marked read-only. The server instructions tell the agent which questions each tool answers better than grep, and to use grep for literal strings, config values and log messages.

**Every reply starts with a completeness line**, for example:

```text
answer: callers · source: call graph (exact, static) · complete: yes
answer: search · source: ranked code, 14 chunks, ~1700/1800 tokens · complete: no — budget hit (1800 tokens), lower-ranked chunks dropped; raise max_tokens or narrow with known_symbols for more
```

It says what the reply is, how it was produced, and whether anything was left out, so an agent does not re-check a complete answer with grep.

A callers reply on bifrost, about 110 tokens:

```text
answer: callers · source: call graph (exact, static) · complete: yes
callers of Bifrost.shouldTryFallbacks (core/bifrost.go:5874): 2 call sites + 1 in tests, complete
  core/bifrost.go:6193  Bifrost.handleRequest        shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
  core/bifrost.go:6425  Bifrost.handleStreamRequest  shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
  (tests: core/bifrost_test.go:8845)
```

Two **prompts** exist with the same names as the CoT tools (`query_with_cot`, `query_without_cot`). A prompt is a template an editor can offer to the user directly; these take `query`, `repo_path` and a comma-separated `known_symbols` and return the context as a user message.

*Chain of thought* (CoT) here means the reasoning instructions and honesty contract that the router normally adds to a prompt ([Prompts](../query/Prompts.md)). With CoT off, the reply is context only, suitable for an assistant that has its own instructions.

### Reply size

A tool reply stays in the agent's context and is re-read on every later turn, so replies are budgeted apart from `[llm].max_tokens` (which also reserves 2,048 tokens for an answer the MCP server never generates).

| Question | Default budget |
| :-- | --: |
| callers / callees | compact list, at most `limit` sites |
| definition | ~600 tokens (40 body lines) |
| explain a concept | ~1,500 |
| search | `[mcp] reply_tokens` (1,800) |
| trace a flow | ~2,000 |

`max_tokens` on a call overrides the default (200 to 16,000). Inside a reply:

- A chunk longer than `[mcp] max_chunk_lines` (60 for the top chunk, half that for the rest) is cut, with the `file:line` range to read for the rest.
- A chunk already sent in this session is replaced by a one-line reference (`(already sent this session) file:a-b  <first line>`). When every chunk of a reply is a repeat, the agent has evidently lost them, so they are sent in full. `fresh: true` always resends.
- The lexical fallback lists one line per hit with its enclosing function, at most 5 lines per file, capped at the reply budget.

### Choosing a repository

The server can serve many repositories, one at a time. `setRepoPathLocked` does the following:

1. Resolve the path to an absolute one and check that it exists and is a directory.
2. Check that `.eulix/kb.fb` exists; if not, return an error telling the user to run `eulix analyze`.
3. If this repository is already active, say so and stop.
4. Load that project's `eulix.toml` (with `config.LoadFrom`), create the language-model client, and create the query router. The history database is **not** opened, because an open `history.db` stays locked and would break `eulix chat` and `eulix history` while the server runs.
5. Only after all of that succeeds, close the previous router and switch. A failure leaves the old, working repository active.

If no repository is set when the client finishes connecting, the server asks the client for its "roots" (the open workspace folders) in the background, with a 5-second timeout, and binds the first usable one. The bind and the check share one lock, so a `set_repo_path` that arrives in between is never overwritten.

### Running a query

Every tool takes the server lock through `withRepo`, switches repository if `repo_path` was given, runs, and resets the idle timer. Switching and querying happen under one lock, so concurrent calls can never query the wrong repository. Queries are serialized: one at a time.

`query` (and `ExecuteQuery`) first asks the router what kind of answer the question wants (`ClassifyForAgent`). A callers, callees or definition question whose symbol is in the index is answered directly. Anything else runs the [retrieval cascade](Retrieval-Cascade.md) with a budget sized by question type. In the cascade, a callers question only accepts a reply that lists call sites; any other reply scores 0 and the cascade continues.

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
| `addTypedTools`, `traceLocked` | The typed tools (`agent_tools.go`). |
| `renderContext`, `renderCalls`, `renderDefinitions`, `budgetFor` | Reply budgets, the completeness line, chunk cuts and session references (`agent_render.go`). |
| `Router.Calls`, `Definitions`, `Trace`, `AgentContextFor` | The structured router API the tools call (`internal/query/router_agent.go`). |
| `onInitialized`, `bindFromRoots` | Binding from the client's roots. |

### Data In, Data Out

Tool arguments are Go structs; the SDK derives each tool's JSON schema from the struct tags and validates input before the handlers run. Replies are a single text block.

## Key Algorithms

### Switch only after the new thing works

**The problem.** If switching repositories tore down the old router first, a typo in the new path would leave the server with nothing.

**The approach.** Build the new router completely, then close the old one.

**Why this way.** A failed switch costs the agent one error message, not its whole session.

## Configuration

In `eulix.toml`:

```toml
[mcp]
reply_tokens = 1800    # default budget for ranked context (200–32,000)
max_chunk_lines = 60   # lines of one chunk sent in full (10–2,000)
```

Server options:

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

Register the server with an agent, together with the instruction that tells the agent when to use it:

```bash
eulix mcp install claude     # .mcp.json + a block in CLAUDE.md
eulix mcp install cursor     # .cursor/mcp.json + .cursor/rules/eulix.mdc
eulix mcp install cline      # .clinerules/eulix.md; prints the server entry to paste
```

An agent tracing a request across files:

```json
{"name": "callers", "arguments": {"symbol": "shouldTryFallbacks"}}
{"name": "trace", "arguments": {"from": "handleRequest", "to": "requestWorker"}}
{"name": "search", "arguments": {"question": "how are retries counted per provider", "known_symbols": ["requestWorker"]}}
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| One query at a time. | A single lock serializes all calls. | None needed for one agent. |
| Only one repository is loaded at once. | Switching closes the previous one. | Use `set_repo_path` between questions. |
| When debugging is on, each query is appended to `.eulix/debug/mcp-query-debug.log`. | Logging is per repository. | Turn `DebugConfig` off in production. |
| The "roots" feature the server relies on is marked deprecated in the SDK. | The code carries a lint suppression for it. | Use `--repo` or `set_repo_path` to avoid relying on it. |
| `trace` and `callers` miss edges that cross an async boundary the parser does not record (channel sends, callbacks, event handlers). A `go f()` statement is recorded. | The call graph is built from syntax. | The reply says where the chain stops and why; follow it with `search`. |
| Callers of an interface method are listed against the interface (`Debug (interface)`), not each implementation. | Dispatch is resolved at run time. | Ask `callers` for the implementation's qualified name. |

## Related Docs

- [Overview](Overview.md): what the server is for.
- [Retrieval Cascade](Retrieval-Cascade.md): what `ExecuteQuery` runs.
- [query Overview](../query/Overview.md): the router created per repository.
