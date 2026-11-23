# Making the MCP server pay off for agents

*Proposal, 2026-11-20. Based on four agent benchmark runs (`bench/agent/`, about 230 Claude Code sessions) and a read of `eulix-cli/internal/mcp` and `internal/query`. Findings marked **measured** come from those runs or from code; the rest are proposals with a way to test them.*

## Why this is needed

Eulix was built for a person exploring a codebase. The MCP server was added later on top of the same query router, which shows in how it behaves with an agent in the loop. In the benchmarks, an agent using Eulix (with a "query Eulix first" hint) paid **18–40% more** than the same agent using only grep, and gave answers no more accurate. The few clear wins were all long, multi-file traces.

The problems below explain most of that gap. None of them is in PRISM or the call graph, which are precise. They're all in what reaches the agent and how.

## What goes wrong today

### 1. "What calls X?" returns the definition, not the callers (measured, bug)

The classifier sends any one-symbol question containing *calls*, *uses*, *invokes*, *called by* or *used by* to `QueryTypeUsage` (`query/classifier/classifier_core.go:333`). `handleUsage` (`query/router_handlers.go:700`) then prints only where the symbol is **defined**:

```text
Usage of 'shouldTryFallbacks'

  core/bifrost.go:5874
  func (bifrost *Bifrost) shouldTryFallbacks(ctx …) bool
```

Only the exact phrases *who calls*, *callers of* or *call graph* (`callGraphPattern`, `classifier_patterns.go:90`) reach the call graph, which answers correctly:

```text
Call Graph: Bifrost.shouldTryFallbacks
  Metrics : Fan-in 3 | Fan-out 6
┌── Called by (Inbound):
│   ← Bifrost.handleRequest …
```

Agents phrase it as "What calls X?", which is how every "callers" task in the benchmark was worded. They got a non-answer, grepped anyway, and paid for both. On bifrost's "callers" tasks, the Eulix setup used **5 → 12 tool calls and 119% more cost** (low-effort run).

### 2. Replies are sized for a chat answer, not a tool result (measured)

The context budget is `[llm].max_tokens − 2048 (answer reserve) − ~140`, floored at 512 (`allocateBudget`, `query/retrieval/intent.go:166`). With the default `max_tokens = 8192`, every reply is about 6,000 tokens; the median measured was **~5,600–5,700**. MCP never generates an answer, so the 2,048-token reserve does nothing there. The budget doesn't depend on the question type either: a one-symbol lookup gets the same ~6k as a full trace.

A reply stays in context and is re-read on every later turn. At the measured size, one Eulix call followed by 3 more tool calls (the median) means about 5,600 × 4 ≈ 22k input tokens for that one reply. That cost exceeds the 1–2 greps it was meant to replace.

### 3. Some chunks are whole classes (measured, RET-13)

One Flask reply contained `src/flask/app.py:81-1536`, the entire `Flask` class, as a single chunk. Questions about a Python method name can retrieve the class summary instead of the method.

### 4. The quality gate checks word overlap, not whether the question was answered (code)

`assess()` (`mcp/strategy_quality.go:66`) scores a reply on anchor coverage, query-word coverage and "richness". The "Usage of" reply in problem 1 names the anchor symbol, so it passes the gate. The cascade then stops, and the better stages (call graph, multi-hop) never run.

### 5. The lexical fallback is large and not always for a good reason (measured)

In the first run, 7 replies to "callers" questions fell to the grep fallback, including Django's `QuerySet._fetch_all`, a plain method lookup. Fallback replies reached **10k+ characters** (cap: `LexicalMaxBytes = 24,000`).

### 6. Nothing says "this is complete", so agents re-check (measured)

With the hint, the agent answered from Eulix alone in only **12 of 40** sessions. In the rest, it made a median of 3 more tool calls to check. Replies give per-chunk scores but never state whether the answer is complete ("these are all 2 call sites") or cut short ("budget hit, 9 chunks dropped").

### 7. Agents aren't told when to use it (measured)

With Eulix installed and no hint, Claude called it in **5 of 50** sessions. The server instructions (`mcp/server.go:46`) only explain how to choose a repo. The `query` description explains how to phrase a question, but not which questions Eulix answers better than grep.

### 8. The tool surface is one natural-language box (code)

There are three query tools (`query`, `query_with_cot`, `query_without_cot`) that differ only in a formatting flag, and none of them is structured. Every request goes through the keyword classifier, which is how problem 1 happens. Each extra tool's description is also sent to the model on every turn.

### 9. Async hops are invisible to the call graph (measured)

On bifrost, a request goes `tryRequest` → *message sent on a channel* → `requestWorker` goroutine. A syntax-based call graph has no edge across that channel. In the Opus deep run, the agent using Eulix described the streaming path well but skipped exactly this step, and its answer failed grading. The full 15-task Opus run ([`2026-11-20-opus48-bifrost`](../../../bench/results/agent/2026-11-20-opus48-bifrost/SUMMARY.md)) repeated it: `deep-stream-chunks` failed in both arms, and `requestWorker` was missing from two of the three failed answers. Agents ask about queues, workers, callbacks and event handlers often, and that's where an agent with grep spends the most tool calls.

### 10. Setup assumes a person (code)

- `eulix init` is an interactive wizard.
- Every command needs `uv` and `~/.Eulix/.venv` on first run, even with EHNAS, which never runs Python (`cmd/eulix/main.go`).

An agent asked to "set up Eulix for this repo" gets stuck on both.

## Proposed changes

Ordered by expected payoff per unit of work.

### P0: fix what is broken

**A. Route caller questions to the call graph.** The cheapest, highest-value fix.
- In the classifier, send *calls / called by / invokes / callers / uses / used by* with one symbol to `QueryTypeCallGraph`. Keep `QueryTypeUsage` only for "show me how to use X" (examples).
- Rewrite `handleUsage` to list call sites, so it never returns just a definition.
- Add tests for each phrasing: "What calls X", "Who calls X", "X is called by?", "Where is X used".

**B. A compact caller and callee format for agents.** The CLI tree is for people: it walks callers transitively, and on bifrost it listed dozens of indirect callers for a fan-in of 3. For MCP, return **depth 1 by default**, one line per call site, with the enclosing function and the calling line:

```text
callers of Bifrost.shouldTryFallbacks (core/bifrost.go:5874) — 2 call sites + 1 in tests, complete (call graph)
  core/bifrost.go:6193  Bifrost.handleRequest        shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
  core/bifrost.go:6425  Bifrost.handleStreamRequest  shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
  (tests: core/bifrost_test.go:8845)
```

That's about 120 tokens instead of ~5,600, and the agent has nothing to re-check. Offer `depth` as an argument.

**C. Make the quality gate check the answer type.** A callers question must get back call sites, and a definition question a definition. If the reply type doesn't match the question type, score it 0 and continue the cascade.

### P1: cut reply size

**D. Give MCP its own budget, separate from `[llm].max_tokens`.**
- Add `[mcp] reply_tokens` (default ~1,500–2,000) with no answer reserve.
- Let each call pass `max_tokens`.
- Shrink it by question type:

| Question | Default budget |
|:--|--:|
| callers / callees | ~300 |
| definition / where is X | ~600 |
| explain a concept | ~1,500 |
| trace a flow | ~2,000 |

Measured points: with the budgets above (`max_tokens = 8192`, typed tools), Opus 4.8 on all 15 bifrost tasks cost 22% less than plain, and Opus 5.5 (2–3 sessions per task) cost 10% less and took 10% less time, with or without the hint ([run](../../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md)). Earlier, replies of ~2,200 tokens (`max_tokens = 4200`) on Opus deep traces cut the Eulix overhead on the tasks it didn't win to about +10% tokens, from +40% in the Sonnet runs. That run changed other things too (see the use-cases doc).

**E. Chunk by symbol, and send skeletons before bodies.**
- Never return a chunk larger than one function or method. A class becomes its signature plus a list of its methods.
- For anything that isn't the top match, send the signature, docstring and the lines that make the relevant calls, with `…` for the rest and a `file:line` range the agent can read if it needs to.
- This fixes RET-13 for agents and should roughly halve trace replies. To be measured.

**F. Don't resend what this session already got.** The server already keeps per-project memory. Also track which chunks it has sent in the session, and refer to repeats by reference (`[already sent: core/bifrost.go:6083-6300]`). Follow-up queries in one session then get cheaper, not more expensive.

**G. Shrink the lexical fallback.**
- Cap it at ~2,000 tokens by default.
- Prefer one line per hit with its enclosing function (cheap from the index) over 7-line blocks.
- Say why it fell back.

### P2: answers that remove follow-up calls

**H. A completeness line at the top of every reply.** State what the reply is, how it was produced, and whether anything was left out:

```text
answer: callers · source: call graph (exact) · complete: yes
answer: trace · source: call graph + ranked code · complete: no — budget hit, 9 chunks dropped; ask with known_symbols=[...] for more
```

The aim is fewer "let me verify with grep" turns. The measurable target is the follow-up tool calls after an Eulix reply, today a median of 3.

**I. A real `trace` answer: the path first, code second.** For "how does X get to Y" questions:
1. Find the chain in the call graph from an entry point (HTTP handler, `main`, an exported API) to the target.
2. Return it as an ordered list of hops, each with `file:line` and the one or two lines that make the call.
3. Only then add code, within the budget.

This is the work agents spent 15–25 tool calls on in the deep tasks, and the call graph already holds most of it.

**J. Bridge or flag async edges.** Add heuristic edges for common async patterns, labelled as inferred:
- **Go:** a `go f(…)` statement; a send on a channel field matched to the goroutines that receive from that same field (`pq.queue <- msg` ↔ `for msg := range pq.queue` in `requestWorker`).
- **Callbacks:** functions passed as arguments, such as the `keyProvider` and `postHookRunner` closures in bifrost.
- **Python and JS:** event handlers and executors.

Where a chain can't be bridged, say so in the reply ("chain breaks here: value sent on `pq.queue`; receivers: `requestWorker`") rather than leaving the gap silent.

### P3: discovery and setup

**K. A typed tool surface.** Replace the three query tools with:

| Tool | Arguments | Returns |
|:--|:--|:--|
| `callers` | `symbol`, `depth=1` | call sites (format B) |
| `callees` | `symbol`, `depth=1` | calls made by the symbol |
| `definition` | `symbol` | signature, docstring, location, short body |
| `trace` | `question` or `from` / `to` | ordered path (I) + code |
| `search` | `question`, `max_tokens` | ranked chunks (today's `query`) |
| `set_repo_path` | `path` | unchanged |

Agents choose typed tools well, and that skips the keyword classifier. Keep `query` as an alias for one release, and fold CoT into a `format` argument.

**L. Tell agents when to use Eulix.**
- Server instructions and tool descriptions should say: *use `callers`, `callees` and `trace` for structure and multi-file flows; use grep for literal strings and config values.*
- Ship the agent-side hint too: `eulix mcp install claude|cursor|cline` registers the server and adds a CLAUDE.md / `.cursor/rules` line. The benchmark showed this hint is what makes agents use Eulix (5/50 → 40/50 sessions).

**M. Setup an agent can do.**
- `eulix init --yes [--engine ehnas]`, with no wizard.
- Let `eulix mcp` index on first query when `.eulix/` is missing, or return a one-line instruction to do so.
- Require `uv` and the venv only when ETIYA embeddings are actually used.

## Implementation status (2026-11-20)

Implemented in `eulix-cli` (Go only; parser and `libeulix` unchanged). First measurement: [run `2026-11-20-sonnet55-low-mcp2`](../../../bench/results/agent/2026-11-20-sonnet55-low-mcp2/SUMMARY.md) (Sonnet 5.5 low, 1 session per task and arm, 19 tasks). With the hint, Eulix went from +36% cost vs plain to −6% on the bifrost tasks of the earlier low-effort run, and to −27% on callers tasks. `bifrost-trace-chat` regressed on an unresolved field call (J). A later run with Opus 4.8 on all 15 bifrost tasks, deep ones included ([`2026-11-20-opus48-bifrost`](../../../bench/results/agent/2026-11-20-opus48-bifrost/SUMMARY.md)), cost 22% less than plain at 14/15 correct against 13/15. Its misses (`requestWorker`, behind a channel) are item J again. More reps are still to run.

| Change | Status | Where |
|:--|:--|:--|
| A. Caller questions → call graph | done | `classifier_patterns.go` (`callGraphPattern`), level-2 keywords; `handleUsage` lists call sites |
| B. Compact caller / callee format | done, `depth` 1–4, `limit` | `Router.Calls`, `FormatCallReport` (`query/router_agent*.go`) |
| C. Answer-type quality gate | done for callers / callees | `retrieveWith` in `strategy_orchestration.go` |
| D. MCP budget apart from `[llm]` | done: `[mcp] reply_tokens`, `max_tokens`, per-type defaults | `budgetFor`, `ContextBuilder.BuildContextBudget` |
| E. Symbol-sized chunks, skeletons | partial: chunks cut at `[mcp] max_chunk_lines` with a `file:line` pointer; classes in `definition` are header + methods | `capChunk`, `Router.Definitions` |
| F. No resending in a session | done (all-repeat replies are resent; `fresh: true`) | `markSentLocked`, `renderContext` |
| G. Smaller lexical fallback | done: one line per hit + enclosing function, ~2,000 tokens | `strategy_lexical.go` |
| H. Completeness line | done on every reply | `header` in `agent_render.go` |
| I. `trace` answer | done: path first, then code for the hops | `Router.Trace`, `traceLocked` |
| J. Async edges | not done (parser / `libeulix` work). `trace` and `callers` say where a chain stops and name async boundaries as the likely reason. `go f()` edges already exist in the graph. | — |
| K. Typed tools | done; `query` kept and routed; `query_with_cot` / `query_without_cot` kept unchanged as deprecated aliases because the retrieval benchmark uses them | `agent_tools.go`, `server.go` |
| L. Telling agents when to use it | done: server instructions, tool descriptions, `eulix mcp install claude\|cursor\|cline` | `server.go`, `cli/mcp_install.go` |
| M. Agent-friendly setup | not done (CLI / installer work). An unindexed repo still gets a one-line "run `eulix analyze`" error. | — |

## How to know it worked

The harness in `bench/agent/` measures all of this already. For each change, run plain vs. `eulix-hint` on the affected task types with **at least 3 sessions per task**, and look at:

| Change | Main metric | Target to call it a win |
|:--|:--|:--|
| A, B, C (callers) | cost and follow-up tool calls on `callers` tasks | Eulix at or below plain cost, 0–1 follow-up calls |
| D, E, G (size) | median Eulix reply tokens; input tokens on `where` tasks | replies < 2,000 tokens; `where` tasks no longer more expensive |
| H (completeness) | tool calls after an Eulix reply | median below 3 (today) |
| I, J (traces) | cost and correctness on `deep` tasks | lower cost, no drop in correct answers |
| K, L (discovery) | sessions that use Eulix without a hint | well above 5/50 |

Each target is set relative to a measured baseline in `bench/results/agent/`. No new claim should go into the README until a run meets it.

## Suggested order

1. **A + B + C.** Small, contained in the router and the MCP formatter, and fixes the worst measured case.
2. **D + G.** A config change and two caps.
3. **H + L.** Text changes with likely large effect on agent behaviour.
4. **E, F, I, K.** Real design work.
5. **J and M.** Separate tracks: J is call-graph work in `libeulix`, M is CLI and installer work.

## References

- Benchmark method and limits: [`bench/agent/README.md`](../../../bench/agent/README.md)
- Run results: `bench/results/agent/2026-11-20-*`
- What the runs say about use cases: [`docs/launch/eulix-use-cases.md`](../../launch/eulix-use-cases.md)
- How the cascade works today: [Retrieval-Cascade.md](Retrieval-Cascade.md), [Anchors-And-Scoring.md](Anchors-And-Scoring.md)
