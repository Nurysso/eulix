# Agent bench: bifrost, Opus 4.8, one session per task

Run `2026-11-20-opus48-bifrost`, 2026-11-20.

## Setup

- `claude-opus-4-8`, default effort, $3 per session. Arms `plain` and `eulix-hint` (the line `eulix mcp install` writes, which names the typed tools). 1 rep.
- Bifrost only, all 15 tasks: 10 regular and 5 deep. 30 sessions, one per task and arm.
- Index built with `build/eulix_linux_amd` (JSON index, 5.9 s); `[llm] max_tokens = 8192`.
- The run hit the account's 5-hour limit partway through. Two sessions that returned the limit message instead of an answer were deleted and re-run, so every session in `runs.jsonl` is a real answer.

## Result

| Arm | Input tokens | Output tokens | Cost (USD) | Δ cost | Tool calls | Correct |
|:--|--:|--:|--:|--:|--:|:-:|
| `plain` | 5.16M | 60k | 8.67 | | 182 | 13/15 |
| `eulix-hint` | 3.75M (−27%) | 59k (−3%) | 6.78 | **−22%** | 185 (73 Eulix) | 14/15 |

| Kind (tasks) | `plain` cost | `eulix-hint` cost | Δ |
|:--|--:|--:|--:|
| callers (3) | 0.39 | 0.27 | −30% |
| trace (2) | 2.13 | 1.31 | −39% |
| where (4) | 1.70 | 1.66 | −3% |
| lookup (1) | 0.13 | 0.17 | +34% |
| deep (5) | 4.32 | 3.38 | −22% |

- Eulix cost less on 9 of 15 tasks. The largest savings were on the long traces: `trace-chat` −44%, `deep-fallback-chain` −48%, `deep-stream-chunks` −48%, and `should-try-fallbacks` −68%.
- The largest overruns were on cheap tasks: `retry-backoff` +125% (a $0.17 task), `run-llm-prehooks` +106% (a $0.06 task), and `default-retries` +34% with zero Eulix calls, so that last one is session noise. Differences on tasks under $0.20 are not meaningful at 1 rep.
- Compared with Sonnet 5.5, Opus read far more on the plain arm for the long traces (`trace-chat` 1.49M input tokens), which is where Eulix replaced the most reading.

## Correctness

- `deep-stream-chunks` failed in **both** arms. Plain missed `requestWorker`; with Eulix the agent missed the streaming handlers (`handleStreamingResponse`, `handleStreamRequest`).
- `callers-pick-rotating-pool` failed on `plain` (missed `requestWorker`) and passed with Eulix.
- `requestWorker` is reached through a channel, so the call graph has no edge to it (item J in [Agent Improvements](../../../../docs/eulix-cli/mcp/Agent-Improvements.md)). Two of the three failures involve it.

## Caveats

- 1 rep per task and arm. Totals are steadier than single tasks.
- `--resume` does not remember the original `--model`, `--arms`, `--corpora` or `--reps`: resuming without them starts the default full run (Sonnet, three arms, all repos) and overwrites `meta.json`. This run was resumed once by mistake; the stray rows were removed and `meta.json` restored before the final resume.
