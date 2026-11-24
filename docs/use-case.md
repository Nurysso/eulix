# Where Eulix helps: what the agent benchmarks show

_Updated 2026-11-20 from the agent benchmark runs (`bench/agent/`). Every figure below comes from those runs. Claims about anything the runs didn't measure are marked **untested**. Method, every run and its limits: [Agent Benchmark](./Architecture/Agent-Benchmark.md)._

## Short answer

Eulix now saves money for a coding agent on a large unfamiliar codebase, and it didn't before. The first version, with one `query` tool and ~5,700-token replies, cost **18–40% more** than grep. After the typed tools (`callers`, `callees`, `definition`, `trace`, `search`) and per-question reply budgets, the same comparison on bifrost came out **6% cheaper** with Sonnet 5.5 (medium effort, chained session) and **22% cheaper** with Opus 4.8 (default effort, one session per task), with answers at least as accurate.

The saving is concentrated in structural questions: _who calls X_, _how does A reach B_, and long multi-file traces. On short lookups and "where is X" questions Eulix is about even with grep, and on very cheap tasks it can cost more.

## What was measured

| Run                                   | Model               | Repos                                      |                Sessions |            Eulix reply size |
| :------------------------------------ | :------------------ | :----------------------------------------- | ----------------------: | --------------------------: |
| Well-known repos (before typed tools) | Sonnet 5.5          | Flask, Django, ripgrep (10 tasks)          |                     150 |               ~5,600 tokens |
| Bifrost, default effort (before)      | Sonnet 5.5          | bifrost (10 tasks)                         | 54 of 150 (usage limit) |                      ~5,700 |
| Bifrost, low effort (before)          | Sonnet 5.5 (low)    | bifrost (10 tasks)                         |                      20 |                      ~5,700 |
| Bifrost, deep traces, small replies   | Opus 4.8            | bifrost (3 deep tasks)                     |                       6 |                      ~2,200 |
| **Typed tools**, four repos           | Sonnet 5.5 (low)    | Flask, Django, ripgrep, bifrost (19 tasks) | 59 of 150 (usage limit) | callers ~110, search ~1,700 |
| **Typed tools**, chained              | Sonnet 5.5 (medium) | bifrost (13 tasks)                         |  2 sessions of 13 turns |                    as above |
| **Typed tools**, Opus                 | Opus 4.8            | bifrost (15 tasks, 5 deep)                 |                      30 |                    as above |

Bifrost is a large Go LLM gateway newer than the models' training data, so the agent has to read the code instead of answering from memory. Each run compared Claude Code with grep and file reads only against the same setup plus Eulix over MCP and one line telling the agent when to use it (the line `eulix mcp install` writes). Sessions were isolated from personal settings and plugins. Answers were graded against hand-checked ground truth. Method and limits: [`bench/agent/README.md`](./../bench/agent/README.md).

## Findings

### 1. Before the typed tools, Eulix cost more

| Run                     | Input tokens with Eulix | Cost with Eulix | Correct (no Eulix vs Eulix) |
| :---------------------- | ----------------------: | --------------: | :-------------------------: |
| Well-known repos        |                    +36% |            +18% |       50/50 vs 50/50        |
| Bifrost, default effort |                    +40% |            +32% |       17/19 vs 14/17        |
| Bifrost, low effort     |                    +41% |            +40% |        9/10 vs 9/10         |

Each reply added about 5,700 tokens to a context that every later turn re-reads. The agent often opened files to check them anyway: it answered from Eulix alone in only 12 of 40 sessions.

### 2. With typed tools and reply budgets, it costs less

| Run                                           |                            Cost with Eulix vs without | Correct (no Eulix vs Eulix) |
| :-------------------------------------------- | ----------------------------------------------------: | :-------------------------: |
| Four repos, Sonnet 5.5 low, 19 tasks          | −5% (callers −27%, where +3%, lookup −11%, trace −5%) |       17/19 vs 18/19        |
| Bifrost, Sonnet 5.5 medium, chained, 13 tasks |                               **−6%** ($7.34 → $6.92) |       13/13 vs 13/13        |
| Bifrost, Opus 4.8, 15 tasks                   |                              **−22%** ($8.67 → $6.78) |       13/15 vs 14/15        |

What changed: a `callers` reply is about 110 tokens (one line per call site) and ends in `complete: yes`, so the agent stops instead of re-checking with grep. Agents picked the typed tools on their own (41 of 45 Eulix calls in the four-repo run).

### 3. The gains are in structural questions and long traces

Opus 4.8 on bifrost, cost with Eulix against without:

| Kind (tasks) |                       Δ cost |
| :----------- | ---------------------------: |
| callers (3)  |                         −30% |
| trace (2)    |                         −39% |
| deep (5)     |                         −22% |
| where (4)    |                          −3% |
| lookup (1)   | +34% (no Eulix calls: noise) |

The best single results:

- **`should-try-fallbacks` callers question:** −68% cost, 2 tool calls instead of 6.
- **`deep-fallback-chain` and `deep-stream-chunks`:** −48% cost each.
- **HTTP chat request path (`trace-chat`):** −44% cost; plain read 1.49M input tokens.

Eulix cost more on a few cheap tasks (`retry-backoff` +125% of $0.17, `run-llm-prehooks` +106% of $0.06). At one session per task, differences on tasks under about $0.20 are noise.

### 4. Where the saving comes from depends on the session

- **One session per task (Opus):** both input tokens (−27%) and cost (−22%) fall, because Eulix replaces many read-and-grep rounds.
- **Chained session (Sonnet):** input is within 2% (both arms carry the same growing conversation); the saving is 20% fewer **output** tokens, because the agent reasons and writes less.

### 5. Eulix didn't make answers more accurate, only cheaper

Correctness was the same or one answer better with Eulix in every typed-tools run (18/19 vs 17/19; 13/13 vs 13/13; 14/15 vs 13/15). A capable model with grep also finds the right code. The exception to watch is call-graph gaps (below).

### 6. Agents don't use Eulix unless told to

With Eulix installed and nothing else, Claude called it in 5 of 50 sessions. `eulix mcp install` registers the server and adds the hint that these numbers use.

### 7. The main risk is missing call-graph edges

A call through a struct field (`h.client.ChatCompletionRequest(...)`), a goroutine or a channel has no edge. At low effort this made `trace` report no callers and sent one answer down the wrong path (`bifrost-trace-chat`). In the Opus run, `requestWorker` (reached through a channel) was missed in two of the three failed answers, in both arms, and `deep-stream-chunks` failed with and without Eulix. Resolving these edges is parser work (item J in [Agent Improvements](./eulix-cli/mcp/Agent-Improvements.md)).

## So what is Eulix for?

### Supported by the data

- **Cheaper agent sessions on large, unfamiliar codebases**, mainly for _who calls X_, _how does A reach B_ and long multi-file traces: 22–48% less cost on those in the Opus run, 27% less on callers in the four-repo run, and 6% less over a whole chained session.
- **Fewer wasted reads.** The `complete: yes|no` line lets the agent stop when the answer is whole.

### Not supported by the data

- Savings on every question. "Where is X" is about even with grep, and very cheap lookups can cost more.
- Better answers than grep on repos the model can already navigate.
- Value from installation alone, with no hint to the agent.

### Plausible, but untested

- **Call-graph answers with no model.** `eulix query "Who calls shouldTryFallbacks?"` returns the exact caller tree locally, with zero LLM tokens. For "who calls X" and "what does X call", the CLI, [`vizeulize`](./../vizEulize/), the LSP and the `libeulix` crate answer directly. These runs don't measure it.
- **Smaller or local models** with short context windows and weaker grep habits may gain more from one ranked reply than Claude does.
- **Other agents** (Cursor, Cline) and **other models**. Only Claude Code with Sonnet 5.5 and Opus 4.8 has been measured.
- **Longer sessions with more reps.** Most figures are one session per task and arm.

## Recommendations

**Product**

1. **Resolve field, goroutine and channel call edges** in the parser (item J). They are the cause of the failures that remain.
2. **Shrink `callees` replies** (median about 1,300 tokens) and stop `search` from overshooting its budget.
3. **Keep the hint** in `eulix mcp install`, and keep the tool descriptions saying: Eulix for structure and flows, grep for literals and config values.
4. **Run more reps** (3+ per task and arm) on bifrost and a second unfamiliar repo before quoting per-task numbers.

**Positioning**

- **Lead with what's measured:** on a large Go codebase newer than the model's training data, Claude Code with Eulix cost 6% (chained, Sonnet) to 22% (Opus) less at equal or better accuracy, with the biggest savings on callers and trace questions. Say how it was measured (1 rep, bifrost, Claude Code only).
- **Don't claim savings on every question,** on other agents, or on repos the model knows well.
- The first version cost more than grep. Say so when comparing with older numbers.

## Data

- Per-run summaries: [`2026-11-23-opus55-bifrost`](./../bench/results/agent/2026-11-23-opus55-bifrost/SUMMARY.md), [`…-opus48-bifrost`](./../bench/results/agent/2026-11-20-opus48-bifrost/SUMMARY.md), [`…-bifrost-chain-medium`](./../bench/results/agent/2026-11-20-bifrost-chain-medium/SUMMARY.md), [`…-sonnet55-low-mcp2`](./../bench/results/agent/2026-11-20-sonnet55-low-mcp2/SUMMARY.md)
- Tasks and ground truth: [`bench/agent/tasks.toml`](./../bench/agent/tasks.toml)
- Harness: [`bench/agent/agent_bench.py`](./../bench/agent/agent_bench.py)
