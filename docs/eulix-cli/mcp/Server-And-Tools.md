# MCP Server And Tools

This page describes the server object, the tools and prompts it exposes, and how it manages repositories and memory.

> **Crate / path:** `eulix-cli/internal/mcp/server.go`, `agent_tools.go`, `agent_render.go`, `tools.go`, `helpers.go`, `lifecycle.go`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `214b9b5` + working tree (agent cost savings, 2026-11-24)

## Overview

The `Server` type wraps the official MCP SDK. The SDK handles the protocol itself: JSON-RPC framing, the start-up handshake, capability negotiation, schema checking, ping and cancellation. This package adds what is specific to Eulix: which tools exist, how a repository is chosen, and when memory is released.

## Why It Exists

Keeping protocol handling in the SDK and policy in this package keeps the code small, and means protocol fixes arrive with SDK upgrades.

## How It Works

### Tools

Structural questions have typed tools, answered from the call graph and the symbol index without going through the keyword classifier. Open questions go to `search`.

| Tool | Arguments | What it does |
| --- | --- | --- |
| `callers` | `symbol` (required), `depth` (1–4, default 1), `limit` (default 40), `repo_path` | One line per call site: `file:line`, the enclosing function and the calling line. Test call sites are counted on a separate line. For a distinctive name, the uses the call graph has no edge for are added, found by name (see below). |
| `callees` | same as `callers` | The calls a function makes, in the same format. |
| `definition` | `symbol` (required), `body_lines` (default 40, 0 = signature only), `repo_path` | Location, signature, the first docstring paragraph and a short body. A class comes back as its header and method list, never its whole body. |
| `trace` | `from`, `to`, `question`, `max_tokens`, `repo_path` | The shortest static call path from `from` to `to` (or, with only `to`, back to an entry point), one hop per line with the calling line. Then ranked code for the hops, within the budget. Where the static chain breaks, it says where the chain starts instead (see below). |
| `search` | `question` (required), `known_symbols`, `max_tokens`, `format` (`plain` or `cot`), `fresh`, `bodies`, `repo_path` | Ranked code for a concept or open question: the best match in full, every other long match as one line (see below). `bodies: true` sends every match's code. |
| `set_repo_path` | `repo_path` (required) | Switches the active repository. Idempotent. **Listed only when the server was not started on a repository.** |
| `query` | `query` (required), `repo_path`, `include_cot`, `known_symbols`, `max_tokens` | Legacy, listed only with `--legacy-tools`. The free-text tool of earlier releases. Callers and definition questions are answered from the index first, as by the typed tools; the rest runs like `search`. |
| `query_without_cot`, `query_with_cot` | `query`, `repo_path`, `known_symbols` | Legacy, listed only with `--legacy-tools`. They keep the pre-budget output unchanged, because the retrieval benchmark compares releases on `query_without_cot`. |

`callers`, `callees` and `definition` take several symbols at once, comma-separated (`"symbol": "Bifrost.handleRequest, Bifrost.tryRequest"`, at most 8). The reply starts with a summary line, `answer: callers for 2 symbols · complete: yes for all`, then each symbol's reply with its own header. Every call is a turn, and every turn re-reads the whole conversation, so one call for several symbols is cheaper than several calls.

All tools are marked read-only. The server instructions tell the agent which questions each tool answers better than grep, and to use grep for literal strings, config values and log messages.

**The tool list is kept short.** The tool definitions and the server instructions are sent to the model on every turn of every session, whether or not the agent calls Eulix. The legacy `query` tools are therefore listed only with `--legacy-tools` (or `EULIX_MCP_LEGACY_TOOLS=1`), and `set_repo_path` only when no repository was given at start-up. A server started with `--repo` lists the five typed tools: about 4.9 KB with the instructions, against 8.4 KB for the nine tools before (a test guards the size).

**Every reply starts with a header line**, for example:

```text
answer: callers · source: call graph (exact, static) + name scan · complete: yes
answer: trace · source: call graph (static) + ranked code · complete: yes · more: no code shown for Bifrost.tryRequest (definition takes several names, comma-separated)
answer: definition · source: index (exact) · complete: yes · more: body cut; read core/bifrost.go lines 5884-5911 for the rest
answer: search · source: ranked code, 14 chunks, ~1700/1800 tokens · complete: n/a (ranked) · more: lower-ranked chunks dropped at the 1800-token budget (raise max_tokens, or narrow with known_symbols)
```

`complete` says whether the reply **answers the question**, and is `yes` only when that was checked: a call path resolved end to end, or every use of a name listed. `no` carries the reason. `n/a` marks ranked code from `search`, which retrieval cannot check; the agent judges the code. `more` names what was left out and how to get it (a cut body with its exact lines, the hops that got no code), without making the answer incomplete. Earlier releases said `complete: no` whenever anything was trimmed: in the Opus 5.5 benchmark, all 24 `search` replies and all 10 `trace` replies said `no`, including a 7-hop trace whose path was fully resolved, and agents went on reading after them.

**Uses found by name.** A static call graph has no edge for a call through a field, a variable or an interface value, and none for a function passed as a value (an HTTP handler, a callback). Earlier releases answered such a `callers` question with "none found statically, confirm with grep", and the agent spent two turns on grep and on finding the enclosing function of each hit. `callers` now does that itself for a distinctive name (at least six characters, with an inner capital or an underscore, not a dunder): it reads the indexed source files for whole-word uses of the name, skips the definition, comments, imports, strings, declarations and assignment targets, and lists what the graph missed, each with its enclosing function. For a method, only uses behind a receiver count (`x.name`, `x->name`, `T::name`). The reply is complete when no other definition shares the name; otherwise a note names the other definitions. Common names (`Close`, `run`) are not scanned. On bifrost the scan reads 2,684 Go files in about 0.2 s.

```text
answer: callers · source: call graph (exact, static) + name scan · complete: yes
callers of PluginPipeline.RunLLMPreHooks (core/bifrost.go:9943): none in the static call graph; found 4 calls by name:
  by name (the call graph could not resolve the receiver):
  core/bifrost.go:5597  Bifrost.RunStreamPreHooks        preReq, shortCircuit, preCount := pipeline.RunLLMPreHooks(ctx, req)
  core/bifrost.go:6576  Bifrost.tryRequest               preReq, shortCircuit, preCount := pipeline.RunLLMPreHooks(ctx, req)
  …
```

**Where a chain starts.** When a trace's first function has no static callers (or `from` → `to` has no static path), the reply lists where that function's name is used: the route registration, the `go worker()` statement, the call through a field. With `from` given and no path, it also shows the target's own chain from its nearest entry point. A trace whose start is found this way, with no other definition of the name, is complete.

**Lean search replies.** A `search` reply is re-read on every later turn, and agents read the files it points to anyway: in the Opus 5.5 benchmark the median reply was 2.4k tokens. By default the best match is sent in full and every other match longer than 8 lines as one line, with its rank, `file:lines`, score, signature and the question's identifiers it mentions:

````text
answer: search · source: ranked code, 17 chunks, ~2698/2500 tokens; 13 listed without code · complete: n/a (ranked) · more: listed matches carry no code (bodies: true sends it)
[1] plugins/governance/main.go:1718-1857 (score 0.95)
```go
…
```
other matches, best first:
  #4 plugins/governance/main.go:139-162 (0.95)  type BaseGovernancePlugin interface {  · mentions PostLLMHook
  #14 core/schemas/plugin.go:415-436 (0.95)  type LLMPlugin interface {  · mentions PostLLMHook
````

The cascade picks its result from the full rendering, so the lean form does not change which stage answers; the reply is rewritten afterwards (`leanSearch`), and only the chunks sent in full are remembered as sent. Short matches stay whole because a line naming them would cost about as much. `bodies: true`, `format: "cot"` and the legacy tools send full replies. On bifrost a governance question went from 11.5k characters to 4.0k.

**Inferred call-graph edges are labelled.** The parser infers three kinds of Go edges besides resolved calls ([Knowledge Base Schema](../../libeulix/Knowledge-Base-Schema.md)). `callers`, `callees` and `trace` follow them and mark each line, so an agent can tell a guess from a fact:

| Edge | Label |
| --- | --- |
| `async` | `[async: channel send → receiver, inferred]` |
| `dispatch` | `[interface dispatch: an implementation of the method called, inferred]` |
| `inferred` | `[receiver type unknown; the only method of that name, inferred]` |

**Symbol names are matched by exact spelling first.** `UpdateProvider` is `Bifrost.UpdateProvider`, not the busier handler `updateProvider`. Among several definitions spelled the same, production code comes before tests and a concrete method before an interface declaration, and the choice no longer depends on map order.

A callers reply on bifrost, about 110 tokens:

```text
answer: callers · source: call graph (exact, static) · complete: yes
callers of Bifrost.shouldTryFallbacks (core/bifrost.go:5874): 2 call sites + 1 in tests, complete
  core/bifrost.go:6193  Bifrost.handleRequest        shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
  core/bifrost.go:6425  Bifrost.handleStreamRequest  shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
  (tests: core/bifrost_test.go:8845)
```

Two **prompts** exist with the same names as the legacy CoT tools, always listed (a prompt is not sent to the model, so it costs nothing per turn) (`query_with_cot`, `query_without_cot`). A prompt is a template an editor can offer to the user directly; these take `query`, `repo_path` and a comma-separated `known_symbols` and return the context as a user message.

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
| `WithInitialRepo(path)` | Repository to bind at start-up. When it binds, `set_repo_path` is not listed and the instructions do not mention it; each tool's `repo_path` can still switch. |
| `WithLegacyTools(bool)` | Also list `query`, `query_with_cot` and `query_without_cot`. `eulix mcp --legacy-tools` or `EULIX_MCP_LEGACY_TOOLS=1` sets it. The environment variable is what the retrieval benchmark sets, because older releases ignore it (they list the tools anyway) while an unknown flag would stop them. |
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
| `trace` and `callers` miss edges that cross an async boundary the parser does not record (callbacks, event handlers, channels in local variables). A `go f()` statement, a send on a field channel, a call through a Go interface and a Go local typed from a call are followed. | The call graph is built from syntax. | For a distinctive name, the name scan lists where the function is called or passed as a value. Otherwise the reply says where the chain stops and why; follow it with `search`. |
| Uses found by name can be wrong: a same-named method of another type, a local variable named like the function. | The scan reads text, not types. | Only distinctive names are scanned; when another definition shares the name, the reply says so and is not complete. |
| Callers of an interface method are listed against the interface (`Debug (interface)`), not each implementation. | Dispatch is resolved at run time. | Ask `callers` for the implementation's qualified name. |

## Related Docs

- [Overview](Overview.md): what the server is for.
- [Retrieval Cascade](Retrieval-Cascade.md): what `ExecuteQuery` runs.
- [query Overview](../query/Overview.md): the router created per repository.
