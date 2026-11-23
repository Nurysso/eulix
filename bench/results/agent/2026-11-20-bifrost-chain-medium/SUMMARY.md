# Agent bench: bifrost, chained session, Sonnet 5.5 medium

Run `2026-11-20-bifrost-chain-medium`, 2026-11-20.

## Setup

- `claude-sonnet-5-5`, effort `medium`, $3 per turn. Arms `plain` and `eulix-hint` only. The hint is the line `eulix mcp install` writes, which names the typed tools.
- Bifrost only. The model is unlikely to know it from training, which flask, django and ripgrep don't guarantee. 13 tasks: all 10 regular ones plus 3 of the 5 deep ones (`deep-provider-update` and `deep-stream-chunks` left out).
- `--chain`: one session per arm, each task one turn (`claude -p --resume`), in `tasks.toml` order. Each turn starts a fresh Eulix MCP server, so its dedup of already-sent replies doesn't carry across tasks.
- Index rebuilt for this run with `build/eulix_linux_amd` (JSON index, 5.7 s).

## Result

| Arm | Input tokens | Output tokens | Cost (USD) | Δ cost | Tool calls | Correct |
|:--|--:|--:|--:|--:|--:|:-:|
| `plain` | 17.65M | 186k | 7.34 | | 60 | 13/13 |
| `eulix-hint` | 17.92M (+2%) | 150k (−20%) | 6.92 | **−6%** | 67 (30 Eulix) | 13/13 |

- Eulix with the hint cost 6% less and wrote 20% fewer output tokens, at the same accuracy. It saved cost on 11 of 13 turns. It cost more on `trace-chat` (+9%, the first turn, 7 Eulix calls) and `should-try-fallbacks` (+2%).
- `trace-chat` passed in both arms this time. At low effort, the Eulix arms failed it because the call through the `h.client` field is missing from the call graph. At medium effort the agent filled the gap with grep.
- The agent used Eulix on 10 of 13 turns. It skipped Eulix on `default-retries`, `plugin-hook-order` and `key-selection`, where it found the answer with grep or from earlier turns.

## What chaining did

- **The conversation history dominates cost.** The first turn read about 0.2M tokens; the last read about 3.5M, because every model call re-reads the whole conversation so far. Eulix and grep calls are a small part of each turn, so per-turn Δ figures from 4% to 16% are diluted by the history both arms share.
- **The history read is the same in both arms.** Total input was within 2% between them, so the saving comes from output: 36k fewer output tokens (about $0.5 at list price) covers the whole $0.41 difference. The agent with Eulix made slightly more tool calls (67 vs 60) but reasoned and wrote less.
- **Chaining cost more than separate sessions.** 35.6M tokens and $14.26 in total for 26 turns. Separate sessions per task would have been roughly 5–7M tokens and $5–7 (estimate from earlier per-task runs). Chained sessions are closer to how agents are used day to day, but they are not the cheap way to run the bench. For cost comparisons, use separate sessions with one rep and two arms.
