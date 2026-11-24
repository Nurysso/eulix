# Agent token-savings benchmark

Does Eulix make a coding agent cheaper? This harness gives Claude Code the same questions on the same repos, with and without Eulix, and records what each session cost.

```sh
python3 bench/agent/agent_bench.py prepare                       # fetch pinned corpora, build Eulix indexes
python3 bench/agent/agent_bench.py run --reps 5 --jobs 4         # every task x arm x rep
python3 bench/agent/agent_bench.py run --chain --corpora bifrost # one session per repo and arm, each task a turn
python3 bench/agent/agent_bench.py run --resume <run-id>         # finish an interrupted run (add --chain for a chained run)
#   --resume forgets --model/--arms/--corpora/--reps/--effort: pass them again
python3 bench/agent/agent_bench.py report bench/results/agent/<run-id>
```

It needs `claude` (logged in) and `eulix` on PATH. Results go to `bench/results/agent/<run-id>/`: `meta.json` (versions, model, settings), `runs.jsonl` (one line per session with metrics and the final answer) and `report.md`. Full transcripts go to `~/.Eulix/bench/agent/transcripts/<run-id>/`.

## Method

**Corpora.** Flask 3.1.3, Django 6.1.3 and ripgrep 15.2.0, each pinned to a commit in [`tasks.toml`](tasks.toml) and fetched through the main bench's snapshot code. Each arm gets its own copy. The plain copy has no `eulix.toml` and no `.eulix/`, so the agent can't stumble on the index.

**Tasks.** Ten questions, three or four per repo, in four kinds: *where* (find the code for a behaviour), *callers* (what calls X), *trace* (follow a flow step by step) and *lookup* (a literal config value, where grep should win). The prompt is the question plus one fixed sentence asking for files and functions and saying not to modify anything.

**Arms.**

| Arm | Tools | What it represents |
| :-- | :-- | :-- |
| `plain` | Bash, Read, Grep, Glob | Claude Code with no Eulix |
| `eulix` | the same + Eulix MCP (`eulix mcp --repo <copy>`, EHNAS engine) | Exactly what `claude mcp add eulix -- eulix mcp` gives you, with nothing telling the agent to use it |
| `eulix-hint` | as `eulix`, plus one system-prompt line (`HINT` in the script) telling the agent to query Eulix first | The same setup with a CLAUDE.md line |

**Isolation.** Every session is `claude -p` with `--setting-sources ""`, `--strict-mcp-config`, `--disable-slash-commands`, `--no-session-persistence` and `--tools Bash,Read,Grep,Glob`. The machine's own CLAUDE.md, skills, plugins' hooks and MCP servers are not loaded. Claude Code's built-in plugins still load, identically in every arm. Each session starts its own Eulix server, and Eulix's project memory lives only in that process, so runs don't learn from each other.

**Metrics.** All come from the session's `stream-json` transcript:
- **Input tokens:** everything the model read across all turns: uncached input + cache reads + cache writes. A file read at turn 2 is counted again on every later turn, which is the cost the README describes.
- **Output tokens** and **cost** in USD, both from Claude Code's own `result` event, priced at API list price. Cache reads are about 10× cheaper than fresh input, so cost and input tokens can move in different directions.
- **Tool calls:** `tool_use` blocks, with Eulix calls counted separately. **Time:** Claude Code's `duration_ms`.
- **Correct:** the final answer is graded against hand-checked ground truth (`truth` in `tasks.toml`). Every `require` group must match one of its regexes. This catches answers that miss the key function or file, but it can't catch a wrong explanation that still names the right symbols. Transcripts are kept so anyone can review them.

**Statistics.** Five reps per task and arm. Tables show medians per task; totals are sums of the per-task medians. Sessions run four at a time, with arms interleaved so drift in API latency hits all arms alike.

## Limits

- **The model knows these repos.** Flask, Django and ripgrep are in every model's training data, so plain grep often goes straight to the right file in one or two calls. That favours the `plain` arm. Code the model has never seen (a private repo, a young project) is where Eulix should matter most, and this run doesn't measure it.
- **The tasks are short.** Each is a single question in a fresh session. Savings that build up over long sessions (the README's "a file read at step two is still in context at step twenty") aren't measured.
- **One agent:** Claude Code, with Claude Sonnet 5.5, Opus 4.8 and Opus 5.5 (bifrost). Other agents (Cursor, Cline) and models may use the tools differently.
- **Wall time is noisy:** it is mostly API latency, and four sessions share the account's rate limits.
- **The rubric is regex-based** and was written by the person who wrote the tool. The answers and transcripts are published so others can re-grade them.
