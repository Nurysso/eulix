# Agent Cost Savings: Next Changes

This page proposes the next changes to the Eulix MCP server to make AI agents cheaper. It follows [Agent Improvements](../eulix-cli/mcp/Agent-Improvements.md), whose items A–M are mostly done, and continues its lettering from N. Every proposal is grounded in the transcripts of the Opus 5.5 benchmark run.

> **Status:** proposal, 2026-11-23. Nothing here is implemented.
> **Based on:** run [`2026-11-23-opus55-bifrost`](../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md): 105 Claude Code sessions, 70 of them with Eulix, transcripts in `~/.Eulix/bench/agent/transcripts/2026-11-23-opus55-bifrost/`.
> **Last verified against:** `3470ec3` plus uncommitted changes on `feat/flatbuffers-kb`

## Where the saving comes from today

On Opus 5.5, Eulix cut agent cost and time by about 10% (see [Agent Benchmark](../Architecture/Agent-Benchmark.md#why-opus-48-saved-more-than-opus-55)). Input tokens did not change. The saving came from **fewer turns** (12.0 → 10.8 per session), and so from less output and fewer cache writes.

A turn is expensive because the agent re-reads the whole conversation on every turn. So the strongest lever is anything that lets the agent stop earlier: fewer calls, fewer follow-up reads, and replies the agent can trust as final. Making replies smaller matters less than it did on Opus 4.8, which over-read on its own.

## What the transcripts show

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

## Proposed changes

Ordered by expected payoff for the effort.

### N. Make `complete:` mean "the question is answered"

**Problem.** `search` and `trace` report `complete: no` for reasons that don't affect the answer: lower-ranked results were trimmed, or code excerpts were cut while the path itself was found. The agent trusts the line and goes on reading, which costs the turns that Eulix is meant to save. One `trace` reply found all 7 hops of `bifrost-trace-chat` and still said `complete: no — code budget hit; raise max_tokens for more`. An agent can't raise `max_tokens`, so that advice is no use to it.

**Change.** Split the header into two signals:

- **Answered:** whether the reply answers the question. A `trace` whose path is resolved end to end is answered. A `callers` reply that lists every static call site is answered.
- **More available:** what was left out and how to get it (`3 of 7 hop excerpts shown`, `top 5 of 40 matches; pass offset to see more`).

Remove advice the agent can't act on.

**Risk.** Saying "answered" when it isn't would cost accuracy. Only say it when it can be checked: a fully resolved path, or a complete list of static call sites with no unresolved field, interface or channel calls in the callers' bodies.

**Expected effect.** Fewer follow-up reads after `trace` and `search`, on the tasks that cost the most (traces and deep questions).

### O. Cut the fixed tool overhead

**Problem.** Eulix's tool definitions and instructions add about 3k tokens to every turn of every session, including sessions where the agent never calls Eulix. That is why input tokens didn't fall although Eulix replaced reading: the reading it saved roughly equals what the definitions add.

**Change.**

- Hide `query`, `query_with_cot` and `query_without_cot` from agent sessions. The retrieval benchmark still needs them, so expose them behind a server flag instead of removing them.
- Hide `set_repo_path` when the server was started with `--repo`.
- Shorten the remaining tool descriptions and the server instructions. Measure each in tokens.

**Expected effect.** A guaranteed saving on every turn, largest on long sessions. Nothing about answer quality changes.

### P. Several symbols per call

**Problem.** Agents call `definition` (31 times) and `callers` (38 times) one symbol at a time, often back to back. Each call is a turn, and each turn re-reads the context.

**Change.** Let `definition`, `callers` and `callees` take a list of symbols and answer all of them in one reply, each in its own section with its own `complete:` line. Mention it in the tool description so agents use it.

**Expected effect.** Fewer turns on multi-symbol questions: deep tasks and "how does A use B" questions.

### Q. A lean `search` reply for agents

**Problem.** `search` sends the largest replies (median 2.4k tokens), "where is X" questions break even with grep, and the agent reads the files afterwards anyway. The code in the reply is paid for twice.

**Change.** For agents, return a ranked list first: `file:line`, signature, and the reason it matched, about 300 tokens. Send bodies when asked (for example `search` with `bodies: true`, or a `definition` call). Keep the full ranked chunks for `eulix chat` and other local-LLM uses, which can't explore on their own.

**Risk.** For a question that one chunk fully answers, the agent needs an extra call. Measure this on the "where" tasks.

### R. Async and interface edges (item J)

**Problem.** The static call graph stops at channels, goroutines started through a variable, interface calls and struct-field calls. This causes the "no static path" and "none found statically" replies above, two of the three misses in the Opus 4.8 run (`requestWorker`, behind a channel), and probably the `deep-stream-chunks` overrun (+37% to +56% cost against `plain`). The agent falls back to grep and file reads, on the most expensive kinds of task.

**Change.** Item J of [Agent Improvements](../eulix-cli/mcp/Agent-Improvements.md): add inferred edges for common async and dispatch patterns, labelled as inferred, so `trace` and `callers` can cross them. This is parser and `libeulix` work.

**Expected effect.** The largest effect per task of anything on this page, for Go and other repos with heavy async code, at the highest cost to build.

### S. Exact ranges for follow-up reads

**Problem.** When a reply cuts a body (`definition`: "body cut, read the named lines for the rest", 5 times), the agent may read the whole file.

**Change.** Always give `file:start-end` for anything cut, so the agent can read just those lines with offset and limit.

## Not worth chasing

- **Literal lookups** (config values, log strings). Grep is already close to optimal. The lookup task made no Eulix calls in either Eulix arm.
- **"Where is X" by name.** Grep finds a name in one or two calls. Q may make these break even at lower cost, but they won't become a large saving.
- **The hint line.** On Opus 5.5, `eulix` without the hint saved as much as `eulix-hint`. More wording there won't move cost.

## Suggested order

1. **N and O** first. Both are small, low-risk changes to the Go MCP server, and they act on the mechanism behind the Opus 5.5 saving.
2. **P and S.** Small changes to the tool interface.
3. **Q.** Needs a measurement on "where" tasks to confirm it doesn't add calls.
4. **R.** The largest piece of work, and the largest effect on traces and deep questions.

## How to know it worked

Use the agent benchmark (`bench/agent/agent_bench.py`) with `plain` and `eulix` only; the hint made no difference on Opus 5.5. Run only the tasks a change affects, with at least 3 reps, and test with the permutation test used in the [Opus 5.5 summary](../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md):

| Change | Tasks to run | Sessions (2 arms, 3 reps) |
| :-- | :-- | --: |
| N, R | trace and deep (7 tasks) | 42 |
| O | all 15 bifrost tasks, since it applies to every session | 90 |
| P, S | deep and callers (8 tasks) | 48 |
| Q | where (4 tasks) | 24 |

Besides cost and time, track the mechanisms the changes target, all readable from the transcripts:

- share of `complete: yes` replies per tool (N, R)
- tool calls after the last Eulix call (N, Q, S)
- context tokens at turn 1 (O)
- turns per session (all)

## References

- [Agent Improvements](../eulix-cli/mcp/Agent-Improvements.md): items A–M, including J (async edges)
- [Agent Benchmark](../Architecture/Agent-Benchmark.md): method, every run, and why Opus 4.8 and Opus 5.5 differ
- [Opus 5.5 run summary](../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md)
- MCP server code: `eulix-cli/internal/mcp/agent_tools.go` (typed tools), `server.go` (legacy `query` tools, instructions), `agent_render.go` (`complete:` header)
