# Agent bench: bifrost, Opus 5.5, one session per task, three arms

Run `2026-11-23-opus55-bifrost`, 2026-11-23.

## Setup

- `claude-opus-5-5`, default effort, $3 per session, Claude Code 2.1.330. Arms `plain`, `eulix` (MCP server only, no hint) and `eulix-hint` (the line `eulix mcp install` writes).
- Bifrost only, all 15 tasks: 10 regular and 5 deep. Planned 5 reps (225 sessions), 3 at a time.
- Eulix v0.10.0 from `build/eulix_linux_amd` built on `feat/flatbuffers-kb` at `3470ec3` plus uncommitted changes. FlatBuffers index (`kb*.fb`), built in 5.8 s; `[llm] max_tokens = 8192`.
- **Stopped early at 105 of 225 sessions** to stay inside the account's 5-hour usage window. Every task and arm has 2 or 3 reps (35 sessions per arm, none failed or hit a limit). `report.md` says "5 reps" because it reads the planned value from `meta.json`; the data has 2–3.

## Result

Sums of per-task medians:

| Arm | Input tokens | Output tokens | Cost (USD) | Δ cost | Time | Δ time | Tool calls | Correct |
|:--|--:|--:|--:|--:|--:|--:|--:|:-:|
| `plain` | 3.00M | 59.0k | 4.80 | | 555 s | | 166 | 33/35 |
| `eulix` | 3.01M (+0%) | 52.4k (−11%) | 4.37 | **−9%** | 502 s | **−10%** | 155 | 35/35 |
| `eulix-hint` | 3.02M (+1%) | 51.8k (−12%) | 4.34 | **−10%** | 505 s | **−9%** | 154 | 35/35 |

Projected to the planned 5 reps (each missing session filled with its task and arm's observed mean), with 95% bootstrap intervals from resampling reps within each task and arm. With only 2–3 reps per cell this bootstrap understates the noise; see *How sure* below for stricter tests.

| | `eulix` vs `plain` | `eulix-hint` vs `plain` |
|:--|--:|--:|
| Cost | −10% [−14%, −6%] | −10% [−16%, −4%] |
| Time | −10% [−14%, −5%] | −10% [−14%, −5%] |
| Output tokens | −12% [−16%, −7%] | −13% [−16%, −9%] |
| Input tokens | +2% [−7%, +14%] | +1% [−11%, +15%] |
| Projected cost of 75 sessions | $21.69 vs $24.09 | $21.69 vs $24.09 |
| Projected agent time of 75 sessions | 42.0 min vs 46.7 min | 42.2 min vs 46.7 min |

**How sure.** 12 of 15 tasks were cheaper in both Eulix arms (sign test p = 0.02). A permutation test that shuffles arm labels within each task gives p ≈ 0.05 for cost in each arm (0.03 with the two Eulix arms pooled) and p ≈ 0.005 for time. Leaving out any one task gives a cost saving between 2% and 19%. So the saving is real but its size is loose: 10% is the best estimate, somewhere around 2–18% is plausible. The input-token change does not differ from zero.

| Kind (tasks) | `plain` cost | `eulix` | `eulix-hint` |
|:--|--:|--:|--:|
| callers (3) | 0.29 | −24% | −33% |
| trace (2) | 1.19 | −32% | −28% |
| where (4) | 0.91 | −7% | +4% |
| lookup (1) | 0.07 | −43%\* | −30%\* |
| deep (5) | 2.33 | +5% | −2% |

\* No Eulix calls; noise.

- Both Eulix arms cost less on 11 of 15 tasks (by median). The largest savings: `trace-chat` −46%/−47%, `should-try-fallbacks` −36%/−56%, `run-llm-prehooks` −52%/−50%. The lookup task (`default-retries`, −43%/−30%) made no Eulix calls in either arm, so its saving is session noise on a $0.07 task.
- The largest overrun is `deep-stream-chunks`: +56% without the hint, +37% with it. The agent made one Eulix call and then read more than `plain` did (844k against 380k input tokens). That one task cancels out the savings on the other deep tasks.
- **Where the saving comes from:** fewer turns (12.0 → 10.8 per session on average), so less output (3,853 → 3,330 tokens) and fewer cache writes (25k → 21k). Total input read is the same: unlike Opus 4.8, plain Opus 5.5 does not over-read on bifrost.
- **The hint barely matters on Opus 5.5.** Without it, the agent called Eulix in 22 of 35 sessions (53 calls); with it, in 30 of 35 (70 calls). Cost was the same. Unhinted, it picked `definition`, `callers` and `callees`; hinted, it also used `search` (20 calls).

## Correctness

- Both Eulix arms answered all 35 sessions correctly. `plain` missed `bifrost-governance-budget` both times, never naming `PreLLMHook`.
- `deep-stream-chunks` and `callers-pick-rotating-pool`, which failed in the Opus 4.8 run (both involve `requestWorker`, reached through a channel), passed in every arm here.

## Compared with Opus 4.8

See [Why Opus 4.8 saved more than Opus 5.5](../../../../docs/Architecture/Agent-Benchmark.md#why-opus-48-saved-more-than-opus-55). In short: the Opus 4.8 run ([`2026-11-20-opus48-bifrost`](../2026-11-20-opus48-bifrost/SUMMARY.md), 1 rep) saved 22% with the hint. Opus 5.5 saves about 10%. The difference is mostly the baseline: plain Opus 5.5 cost $4.80 against $8.67 for plain Opus 4.8 on the same tasks, and read 3.0M input tokens against 5.2M, so there was less wasted reading for Eulix to replace. The two runs also differ in Eulix build (v0.10.0 FlatBuffers index against v0.9.0 JSON, plus MCP server changes merged from `main`) and reps.

## Caveats

- 2–3 reps per task and arm: single-task figures are noisy, totals and kinds are steadier. The projection assumes the missing reps look like the observed ones, and the `~70/75` correct for `plain` assumes the governance-budget miss repeats.
- Resume with `python3 bench/agent/agent_bench.py run --resume 2026-11-23-opus55-bifrost --model claude-opus-5-5 --corpora bifrost --reps 5 --jobs 3` to run the remaining 120 sessions and replace the projection with measurements.
- Costs are API list prices from Claude Code's own accounting, not plan usage. Actual spend for the 105 sessions: $29.88.
