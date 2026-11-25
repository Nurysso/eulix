# Agent skills (experimental)

`eulix/` is an agent skill describing how to use Eulix through its MCP tools or the
`eulix` CLI, how to set it up in a repo, and how to read its replies.

**Status: experimental.** It is committed to preserve it, not as a supported part of Eulix.
It is not installed by `eulix mcp install` and nothing else in the repo depends on it.

What we know so far (agent bench, bifrost, Sonnet, 10 tasks × 2 reps, 2026-11-25):

- With the skill available, agents never invoked it (0 of 20 sessions). They went straight to
  the MCP tools, whose descriptions already carry the workflow.
- Having it available cost about 7k extra input tokens per session (the Skill tool and skill
  listing), so it measured slightly worse than the MCP server alone.
- Its content (CLI use, setup, `eulix.toml`, reading replies, call-graph limits) has not been
  shown to help yet. Measuring that needs a run where the agent is told to load it.
