# Setting up Eulix in a repository

Part of the `eulix` skill. Read it when a repo has no index, a command says it isn't set up, or
the MCP server needs registering or reconfiguring.

## Index a repo

```bash
cd /path/to/repo
command ls .eulix/kb.fb                                        # present = already indexed
script -qc 'eulix init --yes --engine ehnas --analyze' /dev/null
```

- `--yes` skips the wizard and writes defaults (`.eulix/`, `.euignore`, `eulix.toml`); on a partly set-up repo it creates only what is missing. Every command refuses to run until all three exist.
- `--engine`: `ehnas` parses only (fast, no Python, graph and lexical retrieval). `etiya` (the default) also embeds with the Python embedder in `~/.Eulix/.venv`, using the GPU when present; it adds semantic `search` but is slower. Use `ehnas` when `~/.Eulix/.venv` doesn't exist.
- Wrap `eulix` in `script -qc '…' /dev/null` whenever stdout is not a terminal; without a TTY it can hang on a terminal colour query.
- After large edits, renames or `.euignore` changes, run `eulix analyze` again. It is incremental. Changing `engine`, `embeddings.model` or `embeddings.engine` makes the next run do the full pipeline.
- Keep files out of the index with `.euignore` (gitignore syntax). It applies to every engine.

## Register the MCP server

```bash
eulix mcp install claude /path/to/repo    # or: cursor | cline
# or by hand:
claude mcp add eulix -- eulix mcp --repo /absolute/path/to/repo
```

`install` writes the server entry (`.mcp.json`, `.cursor/mcp.json`, or prints the Cline entry)
and a short usage note into `CLAUDE.md` / `.cursor/rules/eulix.mdc` / `.clinerules/eulix.md`.
Re-running updates both. Always pass the repo: without `--repo` the server binds whichever
workspace folder the client reports first, and adds a `set_repo_path` tool to every turn.

Flags: `--timeout 10m` (how long the context builder stays loaded while idle, default 5m),
`--legacy-tools` (also lists the old free-text `query` tools; agents don't need them).

## `eulix.toml`

Every key is optional; missing keys take defaults, so `init --yes` output is enough for query and
MCP use.

- Invalid TOML is a hard error (`invalid eulix.toml: …`), not a fallback to defaults.
- Out-of-range values are clamped silently, and unknown keys are ignored silently. Key names are case-sensitive, and a few are unusual: `Max_Lines`, `DebugConfig`, `prismVersion`, `baseURL`.
- `[llm]` must be valid even though `query` and MCP never call a model; an invalid one stops the MCP server binding the repo (`failed to initialize LLM client`). `provider = "ollama"` needs no key and is never contacted, so it is the safe choice. `anthropic` and `gemini` need a key even for query and MCP. An unknown provider works only with `baseURL` set (treated as OpenAI-compatible).
- Never write an API key into `eulix.toml`. Leave `api_key = ""` and use the environment or the project's `.env` (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, …, or `LLM_API_KEY`).

Knobs agents most often need:

```toml
[project]
engine = "ehnas"                  # or "etiya"
DebugConfig = false               # true writes retrieval traces to .eulix/debug/

[parser]
threads = 8                       # about the CPU core count
verbose = false                   # quieter analyze output

[retrievalConfig]
max_graph_expansion_depth = 1     # call-graph hops for ranked retrieval; 0 turns it off
test_file_penalty = 0.3           # down-weights tests unless the question is about tests

[retrievalConfig.etiya]
lexical_exclude = ["third_party/", "*.min.js"]   # skipped by the text-search fallback

[mcp]
reply_tokens = 1800               # default budget for search/trace replies (200-32000)
max_chunk_lines = 60              # longer chunks are cut with a file:line pointer
```

Leave the other `[retrievalConfig]`, `[retrievalConfig.ehnas]` and `[checksum]` keys at their
defaults unless the user asks for tuning.

After editing: `engine` or `[embeddings]` changes need `eulix analyze`. Any change needs an MCP
server restart, because a running server keeps the config it loaded.
