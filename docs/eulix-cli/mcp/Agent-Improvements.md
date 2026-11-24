# Agent improvements in the MCP server

*A record of the changes that made the MCP server pay off for AI agents, the problem each one fixed, and the benchmark evidence behind it. Items A–M here are done (`eulix-cli`, 2026-11-20; E, J and M landed 2026-11-23). The later cost-focused round — items N–S — is in [Cutting agent cost](Agent-Cost-Savings.md).*

## The starting point

Eulix was built for a person exploring a codebase. The MCP server was added later on top of the same query router, which showed in how it behaved with an agent in the loop. In the first benchmarks, an agent using Eulix (with a "query Eulix first" hint) paid **18–40% more** than the same agent using only grep, and gave answers no more accurate. The few clear wins were all long, multi-file traces.

None of the problems was in PRISM or the call graph, which are precise. They were all in what reached the agent and how. The changes below closed most of that gap: on all 15 bifrost tasks, Opus 4.8 went from paying more than grep to **22% less**, and Opus 5.5 to **10% less** ([runs](#references)).

## What was wrong

The problems that motivated each fix, tagged by how they were found — **measured** (a benchmark run or transcript), **code** (a read of `eulix-cli/internal/mcp` and `internal/query`), or a **bug**.

1. **"What calls X?" returned the definition, not the callers** (measured, bug). The classifier sent any one-symbol question containing *calls*, *uses*, *invokes*, *called by* or *used by* to `QueryTypeUsage`, which printed only where the symbol was defined. Only *who calls*, *callers of* or *call graph* reached the call graph. Agents phrase it as "What calls X?", got a non-answer, grepped anyway, and paid for both — on bifrost's callers tasks, **5 → 12 tool calls and 119% more cost**.
2. **Replies were sized for a chat answer, not a tool result** (measured). The budget was `[llm].max_tokens − 2048 − ~140`, so every reply was about 6,000 tokens regardless of question type. A reply stays in context and is re-read on every later turn, so one call plus the median 3 follow-ups meant ~22k input tokens for a single reply.
3. **Some chunks were whole classes** (measured, [RET-13](../../Known-Issues/Retrieval-And-Query.md)). One Flask reply was the entire `Flask` class (`app.py:81-1536`) as a single chunk, so a method question could retrieve the class summary instead.
4. **The quality gate checked word overlap, not whether the question was answered** (code). `assess()` scored anchor coverage, query-word coverage and richness, so the wrong-type "Usage of" reply passed, the cascade stopped, and the better stages never ran.
5. **The lexical fallback was large and not always warranted** (measured). Replies reached 10k+ characters (cap 24,000), including plain method lookups that should have resolved earlier.
6. **Nothing said "this is complete", so agents re-checked** (measured). With the hint, the agent answered from Eulix alone in only 12 of 40 sessions; in the rest it made a median of 3 more tool calls to verify.
7. **Agents weren't told when to use it** (measured). With Eulix installed and no hint, Claude called it in 5 of 50 sessions. The instructions only explained how to choose a repo, not which questions Eulix answers better than grep.
8. **The tool surface was one natural-language box** (code). Three query tools differed only in a formatting flag, none structured, and every request went through the keyword classifier — the cause of problem 1.
9. **Async hops were invisible to the call graph** (measured). On bifrost a request goes `tryRequest` → *channel* → `requestWorker`; a syntax call graph has no edge across the channel. This was `requestWorker` missing from two of three failed Opus answers and the `deep-stream-chunks` failure in both arms.
10. **Setup assumed a person** (code). `eulix init` was an interactive wizard, and every command needed `uv` and the venv on first run even under EHNAS, which never runs Python.

## What shipped

Each change keeps its letter from the original plan. Status, measured effect, and the code it lives in are in the tables.

### Correctness (A, B, C)

- **A. Caller questions go to the call graph.** The classifier routes *calls / called by / invokes / callers / uses / used by* with one symbol to `QueryTypeCallGraph`; `QueryTypeUsage` is kept only for "show me how to use X". `handleUsage` now lists call sites instead of a bare definition.
- **B. A compact caller/callee format for agents.** Depth 1 by default, one line per call site with the enclosing function and the calling line — about 120 tokens instead of ~5,600, with nothing to re-check. `depth` (1–4) and `limit` are arguments.

  ```text
  callers of Bifrost.shouldTryFallbacks (core/bifrost.go:5874) — 2 call sites + 1 in tests, complete (call graph)
    core/bifrost.go:6193  Bifrost.handleRequest        shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
    core/bifrost.go:6425  Bifrost.handleStreamRequest  shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
    (tests: core/bifrost_test.go:8845)
  ```

- **C. The quality gate checks the answer type.** A callers question must get back call sites, a definition question a definition; a mismatch scores 0 and the cascade continues.

### Reply size (D, E, F, G)

- **D. MCP has its own budget, apart from `[llm].max_tokens`.** `[mcp] reply_tokens` with no answer reserve, overridable per call with `max_tokens`, and shrunk by question type: callers/callees ~300, definition/where ~600, explain ~1,500, trace ~2,000.
- **E. Symbol-sized chunks, skeletons before bodies.** The top chunk is cut at `[mcp] max_chunk_lines`; long lower-ranked chunks become skeletons (signature, docstring, the lines naming a question identifier or trace hop, `…` gaps, and a `file:line` range to read). Classes in `definition` are a header plus methods. (The index's own chunking is unchanged, so RET-13 still holds for the CLI.)
- **F. No resending within a session.** On top of the per-project memory, the server tracks which chunks it has sent and refers to repeats by reference; an all-repeat reply is resent with `fresh: true`.
- **G. A smaller lexical fallback.** One line per hit with its enclosing function, ~2,000 tokens, and a note saying why it fell back.

### Fewer follow-up calls (H, I, J)

- **H. A completeness line at the top of every reply**, stating what the reply is, how it was produced, and whether anything was left out — the groundwork the cost round (item N) later tightened into a checked `complete: yes|no|n/a`.

  ```text
  answer: callers · source: call graph (exact) · complete: yes
  answer: trace · source: call graph + ranked code · complete: no — budget hit, 9 chunks dropped
  ```

- **I. A real `trace` answer: path first, code second.** The chain from an entry point to the target as an ordered list of hops, each with `file:line` and the one or two calling lines, then code within the budget.
- **J. Async and inferred edges.** For Go, a send on a field channel links to the same-package functions receiving from that field (`edge_type: "async"`, 54 edges on bifrost including `tryRequest → requestWorker`), followed by `callers` / `callees` / `trace` and labelled inferred. Extended in the cost round ([item R](Agent-Cost-Savings.md)): sibling-module imports, locals typed from the callee's result, `dispatch` edges to interface implementers, and `inferred` edges by unique method name. Callbacks and Python/JS event handlers are still not bridged; replies name those as where a chain may stop.

### Discovery and setup (K, L, M)

- **K. A typed tool surface:** `callers`, `callees`, `definition`, `trace`, `search`, `set_repo_path`. Agents pick these well, which skips the keyword classifier. `query` is kept and routed; `query_with_cot` / `query_without_cot` stay as deprecated aliases the retrieval benchmark uses.
- **L. Telling agents when to use Eulix.** Server instructions and tool descriptions say to use `callers`, `callees` and `trace` for structure and multi-file flows and grep for literal strings; `eulix mcp install claude|cursor|cline` registers the server and writes a CLAUDE.md / `.cursor/rules` hint. The hint is what makes agents use Eulix (5/50 → 40/50 sessions).
- **M. Setup an agent can do:** `eulix init --yes [--engine ehnas] [--analyze]` with no wizard; `uv` and the venv are required only when ETIYA embeddings are generated; an unindexed repo gets a one-line `eulix init` instruction rather than a wizard or silent auto-indexing.

### Where each change lives

| Change | Status | Code |
|:--|:--|:--|
| A. Caller questions → call graph | done | `classifier_patterns.go` (`callGraphPattern`), level-2 keywords; `handleUsage` lists call sites |
| B. Compact caller / callee format | done, `depth` 1–4, `limit` | `Router.Calls`, `FormatCallReport` (`query/router_agent*.go`) |
| C. Answer-type quality gate | done for callers / callees | `retrieveWith` in `strategy_orchestration.go` |
| D. MCP budget apart from `[llm]` | done: `[mcp] reply_tokens`, `max_tokens`, per-type defaults | `budgetFor`, `ContextBuilder.BuildContextBudget` |
| E. Symbol-sized chunks, skeletons | done for agents | `skeletonChunk`, `focusTerms`, `capChunk`, `Router.Definitions` |
| F. No resending in a session | done (`fresh: true` resends) | `markSentLocked`, `renderContext` |
| G. Smaller lexical fallback | done: one line per hit, ~2,000 tokens | `strategy_lexical.go` |
| H. Completeness line | done on every reply | `header` in `agent_render.go` |
| I. `trace` answer | done: path first, then code | `Router.Trace`, `traceLocked` |
| J. Async / inferred edges | done for Go (channels; extended in [item R](Agent-Cost-Savings.md)) | `grammar_files/go.rs`, `prism_v2/channels.rs`, `prism_v2/dispatch.rs`, `receivers/typed.rs`, `BuildCallGraphIndex`, `edgeLabels` |
| K. Typed tools | done; `query` kept and routed, CoT tools deprecated aliases | `agent_tools.go`, `server.go` |
| L. Telling agents when to use it | done: instructions, descriptions, `eulix mcp install` | `server.go`, `cli/mcp_install.go` |
| M. Agent-friendly setup | done: `eulix init --yes`, lazy uv/venv, one-line index hint | `cli/init.go`, `assets.EnsureEmbedEnv`, `mcp/helpers.go` |

## Measured effect

The harness in `bench/agent/` measures all of this. Baselines are in `bench/results/agent/`, and no new README claim goes in until a run meets its target.

| Changes | Metric | Result |
|:--|:--|:--|
| A, B, C (callers) | cost and follow-up calls on callers tasks | with the hint, −27% on callers tasks ([Sonnet low run](../../../bench/results/agent/2026-11-20-sonnet55-low-mcp2/SUMMARY.md)) |
| D, G (size) | median reply tokens | per-type budgets of ~300–2,000 replace a flat ~6,000 |
| overall | cost vs. plain grep | +36% → −6% on bifrost (Sonnet low); −22% (Opus 4.8, 15 tasks); −10% and −10% time (Opus 5.5) |
| H (completeness) | tool calls after an Eulix reply | the mechanism the cost round (N) then measured |
| I, J (traces) | cost and correctness on deep tasks | Opus 4.8 14/15 correct vs. plain 13/15; `requestWorker` (item J) was the remaining miss |

**Not yet benchmarked:** E (median reply tokens on `trace` / `search`), J (`bifrost-deep-stream-chunks` and `bifrost-callers-pick-rotating-pool`, for cost and correctness), and M (a fresh-`HOME` `eulix init --yes --engine ehnas --analyze` with no `uv`).

## References

- [Cutting agent cost](Agent-Cost-Savings.md): the later round (items N–S) and its data.
- Benchmark method and limits: [`bench/agent/README.md`](../../../bench/agent/README.md).
- Runs: [Sonnet 5.5 low](../../../bench/results/agent/2026-11-20-sonnet55-low-mcp2/SUMMARY.md), [Opus 4.8 bifrost](../../../bench/results/agent/2026-11-20-opus48-bifrost/SUMMARY.md), [Opus 5.5 bifrost](../../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md).
- What the runs say about use cases: [`docs/launch/eulix-use-cases.md`](../../launch/eulix-use-cases.md).
- How the cascade works: [Retrieval-Cascade.md](Retrieval-Cascade.md), [Anchors-And-Scoring.md](Anchors-And-Scoring.md).
