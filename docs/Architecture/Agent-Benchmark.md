# Agent Benchmark

This page covers the benchmark that asks Claude Code the same code questions with and without Eulix and compares what each session costs. It explains the method, the runs so far, and what they say about Eulix as a retrieval engine for AI agents.

> **Path:** `bench/agent/agent_bench.py`, tasks in `bench/agent/tasks.toml`, tests in `bench/tests/test_agent_bench.py`, raw results in `bench/results/agent/<run-id>/`
> **Language:** Python 3.11+ (standard library only)
> **Last verified against:** `3470ec3` plus uncommitted changes on `feat/flatbuffers-kb` (2026-11-23)

## Method

Each task is a question a developer would ask a coding agent on a repo it has never seen: *what calls X* (callers), *where is X* (where), a literal config value (lookup), *trace how A reaches B* (trace), and longer multi-file questions (deep). Every task has a hand-checked ground truth and a regex rubric; an answer is correct when it names every required file, function or value.

Each task runs in up to three arms:

| Arm | What the agent has |
| :-- | :-- |
| `plain` | Bash, Read, Grep and Glob in a copy of the repo with no `.eulix/` |
| `eulix` | The same tools plus the Eulix MCP server, added the way `claude mcp add` adds it. Nothing tells the agent to use it. |
| `eulix-hint` | As `eulix`, plus one system-prompt line saying when to use Eulix, as a CLAUDE.md line would. Since 2026-11-20 this is the line `eulix mcp install` writes. |

Sessions load no user settings, CLAUDE.md, skills, plugins or other MCP servers (`--setting-sources ""`, `--strict-mcp-config`), so the arms differ only in Eulix. Token counts and cost come from Claude Code's own `result` event, at API list prices. `input tokens` counts everything the model read: uncached input, cache reads and cache writes.

There are two ways to run it:

- **One session per task** (default). Every task, arm and rep is a fresh `claude -p` session. This is the cheapest way to compare cost per task.
- **Chained** (`--chain`). One session per repo, arm and rep; each task is one turn of it (`claude -p --resume`), in `tasks.toml` order. This is closer to how an agent is used through a working day, but every turn re-reads the whole conversation so far, so it costs about twice as much in total.

```bash
python3 bench/agent/agent_bench.py run --reps 1 --arms plain,eulix-hint \
    --model claude-sonnet-5-5 --effort medium            # one session per task
python3 bench/agent/agent_bench.py run --chain --corpora bifrost ...   # one session per repo
python3 bench/agent/agent_bench.py run --resume <run-id> [--chain]     # finish an interrupted run
```

The corpora are Flask 3.1.3, Django 6.1.3, ripgrep 15.2.0 and [bifrost](https://github.com/maximhq/bifrost) pinned to a commit from 2026-11-19. Bifrost is the most telling of the four: it is a large Go codebase newer than the model's training data, so the agent can't answer from memory and has to read the code.

## Results

### Before the agent improvements (v0.9.0 MCP)

Eulix had one `query` tool whose replies were sized to `[llm].max_tokens`, about 5,600 tokens at the median. Agents with the hint made fewer tool calls but read more and paid more. On ten tasks across Flask, Django and ripgrep (Sonnet 5.5, default effort, 5 reps) `eulix-hint` cost 18% more than `plain` at equal accuracy (50/50 either way). On bifrost it cost more still: +32% in a partial default-effort run and +40% in a low-effort run, both at 1–2 reps per task. Those v0.9.0 runs are not kept in-repo; the figures below compare against them.

### After: typed tools and reply budgets

The [agent improvements](../eulix-cli/mcp/Agent-Improvements.md) added the `callers`, `callees`, `definition`, `trace` and `search` tools, a `complete: yes|no` line on every reply, and per-question budgets (a caller list is now about 110 tokens).

**Run [`2026-11-20-sonnet55-low-mcp2`](../../bench/results/agent/2026-11-20-sonnet55-low-mcp2/SUMMARY.md):** Sonnet 5.5 low effort, one session per task, 19 tasks on all four repos, 1 rep. The run stopped at a usage limit after 59 of 150 planned sessions, so it has no deep tasks.

| Kind (tasks) | Δ cost before (default effort) | Δ cost now |
| :-- | --: | --: |
| callers (5) | +21% | **−27%** |
| where (7) | +41% | +3% |
| lookup (2) | +105% | −11% |
| trace (5) | +8% | −5% |
| all (19) | +25% | **−5%** |

- On the 9 bifrost tasks both low-effort runs finished, `eulix-hint` went from +36% to −6% cost against `plain`.
- Correct: 18/19 with Eulix, 17/19 plain.
- Agents picked the typed tools on their own: 41 of 45 Eulix calls, although that run's hint still named `query`.

**Run [`2026-11-20-bifrost-chain-medium`](../../bench/results/agent/2026-11-20-bifrost-chain-medium/SUMMARY.md):** Sonnet 5.5 medium effort, chained, bifrost only, 13 tasks (all 10 regular plus 3 deep), `plain` and `eulix-hint`, the `eulix mcp install` hint.

| Arm | Input tokens | Output tokens | Cost | Correct |
| :-- | --: | --: | --: | :-: |
| `plain` | 17.65M | 186k | $7.34 | 13/13 |
| `eulix-hint` | 17.92M (+2%) | 150k (−20%) | $6.92 (**−6%**) | 13/13 |

- Eulix with the hint was cheaper on 11 of 13 turns, by 4–16%. The first trace turn cost 9% more.
- The saving comes from 20% fewer output tokens: the agent reasoned and wrote less. Input was within 2%, because both arms carry the same growing conversation.
- The agent called Eulix on 10 of the 13 turns. On 3 "where" and lookup turns it used grep or what it had learned in earlier turns.

**Run [`2026-11-20-opus48-bifrost`](../../bench/results/agent/2026-11-20-opus48-bifrost/SUMMARY.md):** Opus 4.8 default effort, one session per task, bifrost only, all 15 tasks (10 regular, 5 deep), `plain` and `eulix-hint`, 1 rep.

| Arm | Input tokens | Output tokens | Cost | Correct |
| :-- | --: | --: | --: | :-: |
| `plain` | 5.16M | 60k | $8.67 | 13/15 |
| `eulix-hint` | 3.75M (−27%) | 59k (−3%) | $6.78 (**−22%**) | 14/15 |

| Kind (tasks) | Δ cost |
| :-- | --: |
| callers (3) | −30% |
| trace (2) | −39% |
| where (4) | −3% |
| lookup (1) | +34% |
| deep (5) | −22% |

- Eulix was cheaper on 9 of 15 tasks. Savings were largest where plain had to read a lot: `deep-fallback-chain` −48%, `deep-stream-chunks` −48%, `trace-chat` −44%. The agent called Eulix 73 times in 185 tool calls.
- It cost more on cheap tasks (`retry-backoff` +125% of $0.17, `run-llm-prehooks` +106% of $0.06). `default-retries` was +34% with no Eulix calls at all, so differences on tasks under $0.20 are session noise at 1 rep.
- `deep-stream-chunks` failed in both arms and `callers-pick-rotating-pool` failed on `plain`. Two of the three misses are `requestWorker`, which is reached through a channel and has no call-graph edge.
- Opus saves more than Sonnet did in the chained run (−22% against −6%), mostly because it reads more on its own: on `trace-chat` `plain` read 1.49M input tokens. The runs differ in effort, model and session mode, so this is not a controlled comparison.

**Run [`2026-11-23-opus55-bifrost`](../../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md):** Opus 5.5 default effort, one session per task, bifrost only, all 15 tasks, all three arms, Eulix v0.10.0 with the FlatBuffers index. Planned at 5 reps; stopped at a usage limit after 105 of 225 sessions, with 2–3 reps for every task and arm. Sums of per-task medians:

| Arm | Input tokens | Output tokens | Cost | Time | Correct |
| :-- | --: | --: | --: | --: | :-: |
| `plain` | 3.00M | 59k | $4.80 | 555 s | 33/35 |
| `eulix` | 3.01M (+0%) | 52k (−11%) | $4.37 (**−9%**) | 502 s (−10%) | 35/35 |
| `eulix-hint` | 3.02M (+1%) | 52k (−12%) | $4.34 (**−10%**) | 505 s (−9%) | 35/35 |

| Kind (tasks) | Δ cost `eulix` | Δ cost `eulix-hint` |
| :-- | --: | --: |
| callers (3) | −24% | −33% |
| trace (2) | −32% | −28% |
| where (4) | −7% | +4% |
| lookup (1) | −43% | −30% |
| deep (5) | +5% | −2% |

- How sure: 12 of 15 tasks were cheaper in both Eulix arms (sign test p = 0.02). A permutation test that shuffles arm labels within each task gives p ≈ 0.05 for cost in each arm (0.03 with both arms pooled) and p ≈ 0.005 for time. Leaving out any one task gives a cost saving between 2% and 19%, so 10% is the best estimate, not a precise one. Output tokens fell 12–13%; input tokens did not change.
- The saving comes from fewer turns (12.0 → 10.8 per session): less output and fewer cache writes. Unlike Opus 4.8, plain Opus 5.5 does not over-read on bifrost (3.0M input tokens against 5.2M), so there is less reading for Eulix to replace, and the saving is about half the Opus 4.8 figure.
- Both Eulix arms were cheaper on 11 of 15 tasks. `trace-chat` −46%/−47% and the callers tasks gained most. `deep-stream-chunks` cost 37–56% more: the agent made one Eulix call, then read more than `plain`. The lookup task made no Eulix calls in either arm, so its saving is noise.
- **The hint barely matters here.** Without it, Opus 5.5 called Eulix in 22 of 35 sessions (53 calls); with it, in 30 of 35 (70 calls). Cost was the same.
- `plain` missed `governance-budget` both times (no `PreLLMHook`). The two `requestWorker` tasks that failed in the Opus 4.8 run passed in every arm.

## Why Opus 4.8 saved more than Opus 5.5

The same 15 bifrost tasks saved 22% with Opus 4.8 and 10% with Opus 5.5. The difference is in the `plain` arm: Opus 5.5 explores a large repo with far less reading on its own, so it leaves less for Eulix to replace.

| Kind (tasks) | `plain` input, Opus 4.8 | `plain` input, Opus 5.5 | `eulix-hint` input, Opus 4.8 | `eulix-hint` input, Opus 5.5 |
| :-- | --: | --: | --: | --: |
| trace (2) | 1.59M | 0.96M | 0.80M | 0.61M |
| deep (5) | 2.69M | 1.34M | 1.88M | 1.59M |
| where (4) | 0.72M | 0.50M | 0.89M | 0.66M |
| callers (3) | 0.12M | 0.15M | 0.11M | 0.14M |
| all (15) | 5.16M | 3.00M | 3.75M | 3.02M |

Sums of per-task medians.

- **Eulix sets a floor; Opus 4.8 started further above it.** Without Eulix, Opus 4.8 read 1.49M input tokens on `trace-chat` alone, re-reading the files it had opened on every later turn. Opus 5.5 read half as much on traces and deep tasks. With Eulix, the gap between the models shrinks to about 20% (3.75M against 3.02M). Eulix cut Opus 4.8's input by 27%; Opus 5.5's input didn't change at all.
- **The saving moved from input to turns.** On Opus 4.8, Eulix replaced reading (input −27%, output −3%). On Opus 5.5 it replaced steps: 10% fewer turns, 12% less output and fewer cache writes, at the same input. Cost and time fell by about 10% each.
- **What carried over:** structural questions. Callers and traces were cheaper on both models (callers −30% and −24% to −33%; traces −39% and −28% to −32%). "Where is X" stayed about even on both.
- **Not a controlled comparison.** The runs also differ in Eulix build (v0.9.0 with the JSON index against v0.10.0 with FlatBuffers, and MCP server changes merged from `main` in between; the same five tools and `max_tokens = 8192`), Claude Code (2.1.329 against 2.1.330) and reps (1 against 2–3). Percentages are against each model's own `plain` arm, so the models' different prices cancel out.

## Long sessions on large repos (not measured)

Bifrost is a 1.6M-line Go repo, and these runs cover single questions and one 13-question chained session. A working day with an agent is longer: dozens of tasks in one session, each re-reading the conversation so far. Two things suggest the saving grows there:

- Eulix saves most where the agent would otherwise read most (traces, and on Opus 4.8 the deep tasks). Larger repos and less familiar code mean more of that reading.
- In a long session, every file read early on is carried, and paid for as cache reads, on every later turn. A 110-token `callers` reply in place of several file reads is paid for again on each of those turns.

The one long-session run so far points the other way, which is why this stays a hypothesis: the Sonnet 5.5 chained run saved 6%, less than its single-question runs, because both arms carry the same growing conversation. Opus 5.5 was not run chained, and multi-hour sessions on repos larger than bifrost were not run at all. Each would cost several times the 225-session run, which is more than the token budget available. Until it is measured, use the figures above.

## What this says about Eulix for AI agents

Eulix works as a retrieval engine for AI agents when it answers what grep can't: code structure.

- **Structural questions are where it pays.** *Who calls X*, *what path leads from A to B* and *where is X defined* come back as one compact, complete reply. Grep needs several calls for these and can't say when it has found every caller. The `complete: yes` line tells the agent it can stop. Callers tasks went from +21% to −27% in cost.
- **"Where is X" is about even with grep.** An agent with grep and file reads is already good at finding a feature by name, so ranked code chunks (about 1,700 tokens) rarely save it much, and on an unfamiliar repo the agent sometimes skips Eulix for these. Ranked chunks matter most to a local LLM, which can't explore on its own; that use is unchanged.
- **Gaps in the call graph are the main risk.** A call through a struct field (`h.client.ChatCompletionRequest(...)`), a goroutine or a callback has no edge. At low effort this made `trace` report no callers and the agent stopped on the wrong path (`bifrost-trace-chat`). At medium effort the agent worked around it with grep. Resolving these edges is parser work (item J in [Agent Improvements](../eulix-cli/mcp/Agent-Improvements.md)).
- **The hint helps weaker setups.** With the v0.9.0 `query` tool, agents without a hint rarely called Eulix (5 of 50 sessions). With the typed tools, Opus 5.5 called them in 22 of 35 unhinted sessions and saved as much as with the hint. `eulix mcp install` writes the hint either way.

## Limits

- Most figures are a single session per task, so per-task differences of a few percent are noise. Totals are steadier. The Opus 5.5 run has 2–3 sessions per task and bootstrap intervals. Eulix itself is deterministic; the variance comes from the agent.
- Only Claude Code has been measured, with Sonnet 5.5 and (on bifrost) Opus 4.8 and Opus 5.5. Other agents and models may use the tools differently.
- `--resume` does not remember `--model`, `--arms`, `--corpora` or `--reps`. Pass them again, or it starts the default full run and overwrites the run's `meta.json`.
- Costs are API list prices from Claude Code's own accounting, not plan usage.
- The chained run shares context across tasks, so later turns are cheaper and easier for both arms and its per-task Δ figures are diluted. Compare its totals, not single turns.
- Known reply-size issues, logged in the run summaries: `callees` replies are too large (median about 1,300 tokens) and `search` replies slightly overshoot their budget.
