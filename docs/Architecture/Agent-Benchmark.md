# Agent Benchmark

This page covers the benchmark that asks Claude Code the same code questions with and without Eulix and compares what each session costs. It explains the method, the runs so far, and what they say about Eulix as a retrieval engine for AI agents.

> **Path:** `bench/agent/agent_bench.py`, tasks in `bench/agent/tasks.toml`, tests in `bench/tests/test_agent_bench.py`, raw results in `bench/results/agent/<run-id>/`
> **Language:** Python 3.11+ (standard library only)
> **Last verified against:** `0a78aa7` plus the uncommitted MCP and `bench/` changes (2026-11-20)

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

Eulix had one `query` tool whose replies were sized to `[llm].max_tokens`, about 5,600 tokens at the median. Agents with the hint made fewer tool calls but read more and paid more:

| Run | Tasks | Δ cost of `eulix-hint` vs `plain` | Correct (`plain` / `eulix-hint`) |
| :-- | :-- | --: | :-: |
| [`2026-11-20-sonnet`](../../bench/results/agent/2026-11-20-sonnet/report.md), Sonnet 5.5 default effort, 5 reps | 10 on Flask, Django, ripgrep | +18% | 50/50 / 50/50 |
| [`2026-11-20-sonnet-bifrost`](../../bench/results/agent/2026-11-20-sonnet-bifrost/report.md), default effort, partial (54 sessions) | 10 on bifrost | +32% | 17/19 / 14/17 |
| [`2026-11-20-sonnet-low-bifrost`](../../bench/results/agent/2026-11-20-sonnet-low-bifrost/report.md), low effort, 1 rep | 10 on bifrost | +40% | 9/10 / 9/10 |

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

## What this says about Eulix for AI agents

Eulix works as a retrieval engine for AI agents when it answers what grep can't: code structure.

- **Structural questions are where it pays.** *Who calls X*, *what path leads from A to B* and *where is X defined* come back as one compact, complete reply. Grep needs several calls for these and can't say when it has found every caller. The `complete: yes` line tells the agent it can stop. Callers tasks went from +21% to −27% in cost.
- **"Where is X" is about even with grep.** An agent with grep and file reads is already good at finding a feature by name, so ranked code chunks (about 1,700 tokens) rarely save it much, and on an unfamiliar repo the agent sometimes skips Eulix for these. Ranked chunks matter most to a local LLM, which can't explore on its own; that use is unchanged.
- **Gaps in the call graph are the main risk.** A call through a struct field (`h.client.ChatCompletionRequest(...)`), a goroutine or a callback has no edge. At low effort this made `trace` report no callers and the agent stopped on the wrong path (`bifrost-trace-chat`). At medium effort the agent worked around it with grep. Resolving these edges is parser work (item J in [Agent Improvements](../eulix-cli/mcp/Agent-Improvements.md)).
- **The hint is needed.** Without one, agents rarely call Eulix (5 of 50 sessions in the first run). `eulix mcp install` writes it.

## Limits

- Most figures are a single session per task, so per-task differences of a few percent are noise. Totals are steadier. Eulix itself is deterministic; the variance comes from the agent.
- Only Claude Code has been measured, with Sonnet 5.5 and (on bifrost) Opus 4.8. Other agents and models may use the tools differently.
- `--resume` does not remember `--model`, `--arms`, `--corpora` or `--reps`. Pass them again, or it starts the default full run and overwrites the run's `meta.json`.
- Costs are API list prices from Claude Code's own accounting, not plan usage.
- The chained run shares context across tasks, so later turns are cheaper and easier for both arms and its per-task Δ figures are diluted. Compare its totals, not single turns.
- Known reply-size issues, logged in the run summaries: `callees` replies are too large (median about 1,300 tokens) and `search` replies slightly overshoot their budget.
