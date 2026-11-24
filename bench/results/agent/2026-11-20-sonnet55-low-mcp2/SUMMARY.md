# Agent bench: MCP agent improvements, Sonnet 5.5 low

Run `2026-11-20-sonnet55-low-mcp2`, 2026-11-20. It measures the MCP changes in [Agent-Improvements.md](../../../../docs/eulix-cli/mcp/Agent-Improvements.md) (typed tools, reply budgets, completeness line, routing callers questions to the call graph).

## Setup and limits

- `claude-sonnet-5-5`, effort `low`, Claude Code 2.1.329, the same harness, tasks, arms, HINT and $3 session budget as the earlier runs. Eulix is `build/eulix_linux_amd`, built from this checkout (JSON index, EHNAS), run through a PATH shim in a separate bench home (`~/.Eulix/bench-mcp2`).
- **One session per task and arm, not two.** 2 reps were planned. The run was stopped at the account's usage limit after 59 of 150 sessions, which is all of rep 1 except `bifrost-callers-run-llm-prehooks` / `eulix-hint`. The 5 `deep` tasks never started. So there are no means of 2: every figure is one session, and totals cover the 19 tasks all three arms finished.
- The harness HINT still names the `query` tool. Agents with the hint used the typed tools anyway, from the server instructions and tool descriptions.

## Result

Totals over the 19 tasks (sum of each task's single session):

| Arm | Cost (USD) | Δ cost | Input tokens | Δ input | Tool calls | Correct |
|:--|--:|--:|--:|--:|--:|:-:|
| `plain` | 0.901 | | 739,354 | | 84 | 17/19 |
| `eulix` | 0.922 | +2% | 957,715 | +30% | 74 | 18/19 |
| `eulix-hint` | 0.859 | **−5%** | 892,515 | +21% | 59 | 18/19 |

Input tokens rise, but cost falls with the hint. The Eulix arms use fewer turns and fewer output tokens (−22% on bifrost), and an MCP reply is mostly read from cache on later turns.

### Against the earlier runs (Δ of `eulix-hint` vs `plain` in the same run, same tasks)

| Kind (tasks) | Before, Sonnet default, 5 reps | Before, Sonnet low, bifrost, 1 rep | **Now, Sonnet low, 1 rep** |
|:--|--:|--:|--:|
| callers (5) | +21% | +101% (2 tasks) | **−27%** |
| where (7) | +41% | +60% (4) | **+3%** |
| lookup (2) | +105% | +328% (1) | **−11%** |
| trace (5) | +8% | −7% (2) | **−5%** |
| all (19) | +25% | +36% (9) | **−5%** |

The fairest comparison is the earlier low-effort bifrost run, with the same model and effort. On the same 9 bifrost tasks, Eulix with the hint went from **+36% cost vs plain to −6%**. Correctness on those tasks is unchanged at 8/9, but the miss moved: `pick-rotating-pool` is now right and `trace-chat` is now wrong (see below).

### Per kind, now

- **callers:** the clearest win. On every callers task the agent answered from one `callers` call plus at most one more, for 25–41% less cost than plain (except `run-llm-prehooks` without the hint, +13%). It was correct on all 5, while plain missed one (`pick-rotating-pool`: it found the closures but not `requestWorker`).
- **where / lookup:** about break-even (+3%, −11%). `search` replies are about 1,700 tokens, so where an agent with grep needs 1–3 calls, Eulix rarely saves much. Flask and Django `where` tasks got cheaper; `plugin-hook-order` (+17%) and `ripgrep-binary-detection` (+26%) got more expensive.
- **trace:** −5% with the hint, +19% without it, and 1 of 5 wrong in both Eulix arms (`bifrost-trace-chat`).

## What the transcripts show

From the 31 Eulix-arm sessions that called Eulix:

| Tool | Calls | Median reply | Max |
|:--|--:|--:|--:|
| `callers` | 14 | 113 tokens | 143 |
| `search` | 11 | 1,692 | 2,212 |
| `trace` | 8 | 2,069 | 2,137 |
| `callees` | 6 | 1,279 | 2,858 |
| `query` | 4 | 1,717 | 2,074 |
| `definition` | 2 | 558 | 558 |

- Agents picked the typed tools: 41 of 45 calls. `query` (the tool the HINT names) was used only 4 times.
- Before the change, replies had a median of about 5,600 tokens. Now `callers` replies are about 110 tokens and ranked replies stay near their budget (1,500–2,000).
- 10 of the 31 sessions answered from Eulix alone (was 12 of 40). The median is 2 tool calls after the last Eulix call (was 3).
- 18 of 45 replies said `complete: yes`.

## Problems found

1. **`bifrost-trace-chat` fails in both Eulix arms (passed before).** `trace` to `ChatCompletionRequest` reports no callers, because `h.client.ChatCompletionRequest(...)` is a call through a struct field that the call graph doesn't resolve. The agent then walked down with `callees` and grep, stopped at `handleProviderRequest`, and never named `executeRequestWithRetries`. This is item J (async edges and edges through fields), parser work.
2. **`callees` replies are too large** (median 1,279 tokens, max 2,858). Long calling lines across 40 sites add up. Lower the default limit or show only the callee name and line for repeated lines.
3. **`search` replies sit at or a little over budget** ("~1,686/1,500"). The budget counts chunk tokens before the per-chunk header and the cut markers. Budget the rendered text instead.

## Next run

Re-run with `--resume 2026-11-20-sonnet55-low-mcp2` to finish rep 2 and the deep tasks (91 sessions). The harness skips finished sessions. Use the same PATH shim and `EULIX_BENCH_HOME=~/.Eulix/bench-mcp2`.
