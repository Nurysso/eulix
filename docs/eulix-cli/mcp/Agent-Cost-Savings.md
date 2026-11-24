# Cutting agent cost in the MCP server

How the Eulix MCP server was changed to make AI agents cheaper, the data behind each change, and why each part works the way it does. It continues [Agent Improvements](Agent-Improvements.md), whose items A–M are done, and uses the same lettering from N. Every change is grounded in the transcripts of the Opus 5.5 agent benchmark.

> **Status:** all six changes (N–S) are implemented but **not yet measured** with the agent benchmark. Each change below has its own status line.
> **Based on:** the [`2026-11-23-opus55-bifrost`](../../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md) run — 105 Claude Code sessions, 70 of them with Eulix.

## Where the saving comes from

On Opus 5.5, Eulix cut agent cost and time by about 10% (see [Agent Benchmark](../../Architecture/Agent-Benchmark.md#why-opus-48-saved-more-than-opus-55)). Input tokens did not change. The saving came from **fewer turns** (12.0 → 10.8 per session), and so from less output and fewer cache writes.

A turn is expensive because the agent re-reads the whole conversation on every turn. So the strongest lever is anything that lets the agent stop earlier: fewer calls, fewer follow-up reads, and replies the agent can trust as final. Making replies smaller matters less than it did on Opus 4.8, which over-read on its own.

Every change below serves that end in one of three ways:

1. **Replies the agent can trust as final** (N, S, the name scan, R). A reply that claims completeness only when it is true, and that names exactly what is missing, removes the "let me check with grep" turn.
2. **Less fixed cost per turn** (O, Q). Tool definitions and earlier replies are re-read on every turn.
3. **Fewer calls** (P). Several symbols answered in one call.

## What the transcripts showed

| Finding | Data from the 70 Eulix sessions |
| :-- | :-- |
| Eulix adds a fixed cost to every turn | Context at turn 1 is **10.2k tokens with Eulix, 7.3k without**: about 3k tokens of tool definitions and server instructions, carried on every turn, about 33k per session |
| Agents keep working after Eulix replies | A median of **5 more tool calls** after the last Eulix call |
| `search` never reports done | **24 of 24** replies say `complete: no`, every time with "budget hit, lower-ranked chunks dropped". Median reply 2.4k tokens. |
| `trace` never reports done | **10 of 10** say `complete: no`: 6 "no static path", 2 "chain starts at a function with no static callers", 2 "code budget hit" where the call path itself was fully resolved |
| `callers` often finds nothing | 10 of 38 replies say "none found statically", and the agent falls back to grep |
| `callers` and `definition` work | Median 120 and 560 tokens, mostly `complete: yes`. Callers tasks saved 24–33%. |
| Unused tools are still listed | The server registers 9 tools. Agents in the bench used 5 (`callers`, `callees`, `definition`, `trace`, `search`), never `query`, `query_with_cot`, `query_without_cot` or `set_repo_path`. |

Tool-call counts per reply type: `callers` 38, `definition` 31, `search` 24, `callees` 20, `trace` 10.

## Changes

### N. Make `complete:` mean "the question is answered"

**Problem.** `search` and `trace` report `complete: no` for reasons that don't affect the answer: lower-ranked results were trimmed, or code excerpts were cut while the path itself was found. The agent trusts the line and goes on reading, which costs the turns Eulix is meant to save. One `trace` reply found all 7 hops of `bifrost-trace-chat` and still said `complete: no — code budget hit; raise max_tokens for more`. An agent can't raise `max_tokens`, so that advice is no use to it.

**Change.** Split the header into two signals: whether the reply **answers** the question (a trace whose path resolves end to end; a `callers` reply that lists every static call site), and what was **left out** and how to get it. Remove advice the agent can't act on.

**Status: done.** The header is `answer: … · source: … · complete: yes|no|n/a [— why] [· more: …]`. A resolved `trace` path, a found `definition`, and a `callers` list checked by the name scan say `yes`; ranked `search` code says `n/a (ranked)`; `more:` carries what was cut and how to get it. Two additions came from re-reading the transcripts:

- **Name scan for `callers`.** For a distinctive name, `callers` reads the indexed files for the calls the graph could not resolve and the uses as a value, with their enclosing functions. This turns the "none found statically" replies (10 of 38) into answers: `callers PluginPipeline.RunLLMPreHooks` on bifrost now lists the 4 call sites the agents found with grep and awk. The scan is also what makes a `callers` "yes" checkable.
- **Where a chain starts.** A `trace` whose first function nothing calls statically (and a `trace` with no static path) lists where that function's name is used, and the target's own chain from its nearest entry point.

The same transcripts showed `trace to=UpdateProvider` resolving to the handler `ProviderHandler.updateProvider`; symbol lookup now prefers the exact spelling.

### O. Cut the fixed tool overhead

**Problem.** Eulix's tool definitions and instructions add about 3k tokens to every turn of every session, including sessions where the agent never calls Eulix. That is why input tokens didn't fall although Eulix replaced reading: the reading it saved roughly equals what the definitions add.

**Change.** Hide `query`, `query_with_cot` and `query_without_cot` from agent sessions behind a server flag (the retrieval benchmark still needs them); hide `set_repo_path` when the server was started with `--repo`; shorten the remaining tool descriptions and instructions.

**Status: done.** `query`, `query_with_cot` and `query_without_cot` are listed only with `eulix mcp --legacy-tools` or `EULIX_MCP_LEGACY_TOOLS=1` (the retrieval benchmark sets the variable, which older releases ignore). `set_repo_path` and the instruction sentence about it are left out when the server starts on a repository. With the shorter descriptions and instructions, a server started with `--repo` sends 4.9 KB per turn instead of 8.4 KB (JSON of `tools/list` plus the instructions); `TestAgentToolListIsLean` guards the size.

### P. Several symbols per call

**Problem.** Agents call `definition` (31 times) and `callers` (38 times) one symbol at a time, often back to back. Each call is a turn, and each turn re-reads the context.

**Change.** Let `definition`, `callers` and `callees` take a list of symbols and answer all of them in one reply, each in its own section with its own `complete:` line.

**Status: done.** `symbol` takes up to 8 comma-separated names; the reply starts with `answer: callers for 2 symbols · complete: yes for all`, then each symbol's reply with its own header. A comma-separated string was chosen over an array argument because it adds no schema to the tool list.

### Q. A lean `search` reply for agents

**Problem.** `search` sends the largest replies (median 2.4k tokens), "where is X" questions break even with grep, and the agent reads the files afterwards anyway. The code in the reply is paid for twice.

**Change.** For agents, return a ranked list first (`file:line`, signature, and why it matched), and send bodies only when asked. Keep the full ranked chunks for `eulix chat` and other local-LLM uses, which can't explore on their own.

**Status: done, unmeasured.** The best match is sent in full, so a question one chunk answers still costs one call; every other match longer than 8 lines is listed as one line (`#4 file:a-b (score)  signature  · mentions X`), shorter ones stay whole. `bodies: true` restores the full reply. The rewrite (`leanSearch`) runs after the cascade has picked its result from the full rendering, so stage selection is unchanged, and only chunks sent in full are remembered as sent. On bifrost a governance question went from 11.5k to 4.0k characters. The "where" tasks still need a measurement to confirm the lean reply doesn't add calls.

### R. Async and interface edges (item J)

**Problem.** The static call graph stops at channels, goroutines started through a variable, interface calls and struct-field calls. This causes the "no static path" and "none found statically" replies above, two of the three misses in the Opus 4.8 run (`requestWorker`, behind a channel), and probably the `deep-stream-chunks` overrun (+37% to +56% cost against `plain`). The agent falls back to grep and file reads, on the most expensive kinds of task.

**Change.** Item J of [Agent Improvements](Agent-Improvements.md): add inferred edges for common async and dispatch patterns, labelled as inferred, so `trace` and `callers` can cross them. This is parser and `libeulix` work.

**Status: done for Go, unmeasured.** Reading why bifrost's chains broke found four causes, now handled in the parser and PRISM v2:

| Cause | Change | Edge type |
| :-- | :-- | :-- |
| Imports of another module in the same repository counted as external (bifrost has `core`, `framework`, `transports` and plugin modules) | Imports map to the project module with the longest matching path | `call` (resolved) |
| Locals declared from a call had no type (`pipeline := bifrost.getPluginPipeline()`) | Typed from the callee's declared result | `call` (resolved) |
| A call through an interface stopped at the interface's method declaration | An edge to the same-named method of each implementer (at most 6) | `dispatch` |
| A receiver of unknown type (an untyped package variable, a call result) | The project's only method of that name, for distinctive names only | `inferred` |

Together with the channel edges already done (`async`), bifrost's resolved call edges went from 103,605 to 132,585, plus 6,956 `dispatch`, 1,221 `inferred` and 124 `async` edges. `trace ProviderHandler.updateProvider → Bifrost.UpdateProvider` (the `deep-provider-update` task) used to report no static path and is now 2 resolved hops; `trace CompletionHandler.chatCompletion → Bifrost.handleProviderRequest` resolves from the HTTP handler in 8 hops; `callers PluginPipeline.RunLLMPreHooks` lists its 4 call sites from the graph. `callers`, `callees` and `trace` label every inferred edge. Not covered: callbacks passed as values (the MCP name scan lists them as "used as a value"), channels in local variables, and languages other than Go.

### S. Exact ranges for follow-up reads

**Problem.** When a reply cuts a body (`definition`: "body cut, read the named lines for the rest", 5 times), the agent may read the whole file.

**Change.** Always give `file:start-end` for anything cut, so the agent can read just those lines with offset and limit.

**Status: done.** A cut definition body is named in the header, `more: body cut; read core/bifrost.go lines 5884-5911 for the rest`, as well as under the body. Ranked chunks still name the whole chunk's range: their text is not always the file's lines (summaries, stripped comments), so the remaining range can't be computed for them.

## Measured on bifrost

bifrost is 2,684 Go files across several modules. These figures come from indexing it with the new parser and querying through `eulix mcp`, not from the agent benchmark.

| What | Before | After |
| :-- | :-- | :-- |
| Tool list + instructions per turn (`--repo` server) | 8,393 bytes, 9 tools | about 4,900 bytes, 5 tools |
| `callers PluginPipeline.RunLLMPreHooks` | "none found statically, confirm with grep" | 4 call sites from the call graph, `complete: yes` |
| `trace ProviderHandler.updateProvider → Bifrost.UpdateProvider` | no static path | 2 resolved hops, `complete: yes` |
| `trace CompletionHandler.chatCompletion → Bifrost.handleProviderRequest` | broke at the HTTP layer | 8 resolved hops |
| A 7-hop resolved trace | `complete: no — code budget hit` | `complete: yes`, hops without code named under `more:` |
| `search` reply, governance question | 11.5k characters | 4.0k characters |
| Resolved call edges in the bifrost call graph | 103,605 | 132,585, plus 6,956 `dispatch` and 1,221 `inferred` |

## Design decisions

**`complete:` keeps its name.** Renaming it to `answered:` would have been cleaner, but the server instructions, the skill, the hint `eulix mcp install` writes into `CLAUDE.md`, and the benchmark's per-tool metric all key on `complete:`. Changing what it means kept all of them working. `n/a` for ranked search, not `yes` or `no`: retrieval cannot check that ranked code answers an open question, so `yes` would be a false claim, and `no` was the old behaviour that sent agents on reading. `more:` advice must be something the agent can do cheaply — "raise max_tokens" re-sends everything in a larger reply, so the trace instead names the hops that got no code and points at `definition`, which takes several names at once.

**The name scan lives in the MCP server, for distinctive names only.** It works for every language today and needs no re-indexing; R later fixed the root cause for Go, while the scan still covers other languages and callbacks passed as values. A name counts as distinctive at six or more characters with an inner capital or underscore and no dunder: a common name (`Close`, `run`) matches other types' methods, so scanning it would add noise and a false "complete". When another definition shares the name the uses are still listed (that saves the grep) with a note, and the reply says `complete: no`. Only real uses count — for a method, `x.name`, `x->name`, `T::name`; definitions, comments, imports, strings and assignment targets are skipped. One read per indexed file per call, about 0.2 s on bifrost, stopping at 1,000 uses.

**Symbol resolution is deterministic.** A name matches the node spelled exactly as asked; among several, production code before tests, a concrete method before an interface declaration, then the most called, then by key. `trace to=UpdateProvider` had resolved once to a test mock and once to an interface declaration because equal-scoring candidates were picked in Go map order; a fixed order makes the answer reproducible.

**Legacy tools hide behind an option, not removed.** The retrieval benchmark compares releases on `query_without_cot`. An environment variable as well as a flag, because older releases ignore an unknown variable but would exit on an unknown flag. `repo_path` stays on every tool (the plan only asked to hide `set_repo_path`); dropping it would save ~140 tokens but remove repository switching from the default `eulix mcp install` setup. Editor prompts stay — they are templates offered to the user, not sent to the model. Size is measured in bytes (JSON of `tools/list` plus instructions); with no offline tokenizer, token figures are estimates of bytes / 4.

**Several symbols as a comma-separated string, not an array.** A new array property adds schema to every turn of every session; the existing string field with one sentence of description costs almost nothing, and models follow "comma-separated" reliably. The cap of 8 keeps a long list from becoming one huge reply.

**Lean search keeps the best match whole and is on by default.** The plan's risk was that a question one chunk answers would need a second call, so the top chunk stays in full. Short matches stay whole because a line naming a three-line chunk costs as much as the chunk. The rewrite runs after the cascade so stage selection is unchanged. Listed matches are indented so the session's "already sent" tracking does not record them; `bodies: true` sends them. On by default, because an opt-in agents would have to discover would rarely be used.

**R fixes causes, not patterns, and labels anything guessed.** The two precise fixes (sibling modules, typed locals) involve no guessing and account for most of the new edges; the sibling-module fix alone added 11,942. No knowledge-base format change: the parser records each local's initializer call in `GoInfo::local_calls`, which lives only in memory until the call graph is built, and `edge_type` was already a string. Dispatch is structural and capped: a Go type implements an interface when its methods include every method of the interface by name, and an interface with more than 6 implementers gets no edges (linking them all would make `callers` and `trace` useless). Unique-name inference is the last resort and refuses when a type is known — a receiver that is the method's own receiver, a variable or field of a declared type, a local from another package, or an imported package never gets an inferred edge, because its method may be promoted from an embedded type outside the project. Inferred edges feed only the labelled tools (`callers`, `callees`, `trace`); ranked search, `called_by` and `call_count_estimate` use resolved calls only, so a guess never widens results silently, and where a resolved call and an inferred edge join the same two functions the resolved call wins.

## Verification

- Go: `go test ./...` in `eulix-cli` passes; `go vet` and `gofmt` clean.
- Rust: `cargo test` and `cargo clippy --all-targets` pass in `libeulix` and `eulix-parser`.
- New tests: `internal/mcp/agent_cost_test.go`, `internal/query/router_agent_names_test.go`, `TestInferredEdgesAreLabelled`, `libeulix/tests/go_inferred_edges.rs`, and `slimming_keeps_what_typed_locals_need`.
- bifrost: a copy indexed with the new parser (about 6 s) and queried through `eulix mcp`, giving the numbers above.
- **Not run:** the agent benchmark (`bench/agent`).

## Known limits

- **Unmeasured:** cost and turn savings are expected, not shown.
- **Ranked search quality is unchanged:** on the governance question the top match is still unrelated code. Q only makes such a miss cheaper.
- **R covers Go only:** callbacks passed as values, channels in local variables, and other languages are not bridged in the call graph; the name scan lists callbacks as "used as a value".
- `eulix` embeds the parser, so R reaches users only after `./build.sh` and a fresh `eulix analyze` (the parse cache invalidates itself when the parser changes).

## How to confirm it worked

Use the agent benchmark (`bench/agent/agent_bench.py`) with `plain` and `eulix` only; the hint made no difference on Opus 5.5. Run only the tasks a change affects, with at least 3 reps, and the permutation test used in the [Opus 5.5 summary](../../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md):

| Change | Tasks to run | Sessions (2 arms, 3 reps) |
| :-- | :-- | --: |
| N, R | trace and deep (7 tasks) | 42 |
| O | all 15 bifrost tasks, since it applies to every session | 90 |
| P, S | deep and callers (8 tasks) | 48 |
| Q | where (4 tasks) | 24 |

Besides cost and time, track the mechanisms the changes target, all readable from the transcripts: share of `complete: yes` replies per tool (N, R), tool calls after the last Eulix call (N, Q, S), context tokens at turn 1 (O), and turns per session (all).

## References

- [Agent Improvements](Agent-Improvements.md): items A–M, including J (async edges)
- [Agent Benchmark](../../Architecture/Agent-Benchmark.md): method, every run, and why Opus 4.8 and Opus 5.5 differ
- [Opus 5.5 run summary](../../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md)
- MCP server code: `eulix-cli/internal/mcp/agent_tools.go` (typed tools), `server.go` (legacy `query` tools, instructions), `agent_render.go` (`complete:` header)
