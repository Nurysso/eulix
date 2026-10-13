# Eulix MCP Server

Model Context Protocol server for Eulix code intelligence. Lets AI coding agents
(Windsurf, Cline, Claude Code, Continue, LM Studio, etc.) query any analyzed
codebase on your machine and receive ranked source context in return.

Built on the official Go SDK (`github.com/modelcontextprotocol/go-sdk`) and runs
over stdio.

---

## What it does

Eulix keeps a knowledge base and context builder cached in memory between
queries, so an agent can ask many questions against the same repository without
paying the analysis cost each time. The MCP server exposes that capability as
four tools and two prompts.

Unlike the one-off `eulix query` command, the MCP server:

- stays warm across queries (releases memory after an idle timeout, default 5m)
- can serve multiple repositories, switching on demand
- adapts its retrieval strategy to what the calling agent already knows

---

## Quick start

```bash
# 1. Analyze a repository once
eulix analyze /path/to/repo

# 2. Run the MCP server (stdio transport)
eulix mcp --repo /path/to/repo
```

The server prints nothing and waits for JSON-RPC on stdin. Point any MCP client
at that command.

### Example client config (generic stdio)

```json
{
  "mcpServers": {
    "eulix": {
      "command": "/absolute/path/to/eulix_mcp",
      "args": ["mcp", "--repo", "/path/to/repo"]
    }
  }
}
```

The binary name may be `eulix` or `eulix_mcp` depending on your install; the
`mcp` subcommand is required either way.

---

## Tools

### `query`

The general-purpose retrieval tool.

| Argument        | Type     | Required | Purpose                                                    |
| --------------- | -------- | -------- | ---------------------------------------------------------- |
| `query`         | string   | yes      | The question, symbol, or search string                     |
| `repo_path`     | string   | no       | Switch/target a repository for this call                   |
| `include_cot`   | bool     | no       | Include Chain-of-Thought headers (default: server setting) |
| `known_symbols` | []string | no       | Symbols/files the agent already knows from its session     |

`known_symbols` is the important one. Passing it enables multi-hop retrieval —
see _How retrieval works_ below.

### `query_with_cot`

Same as `query` with `include_cot=true` forced. Returns retrieved context plus
reasoning instructions, an honesty contract, and a citation legend. Use when
you want the model to reason over the context before answering.

### `query_without_cot`

Same as `query` with `include_cot=false` forced. Returns clean context only.
Use when injecting into a model that already has its own instructions.

### `set_repo_path`

Switches the active repository.

| Argument    | Type   | Required | Purpose                                 |
| ----------- | ------ | -------- | --------------------------------------- |
| `repo_path` | string | yes      | Absolute path to an analyzed repository |

Idempotent. The repository must have been run through `eulix analyze` first.

---

## Prompts

- `query_with_cot` — builds a prompt with retrieved context and CoT headers
- `query_without_cot` — builds a prompt with context only

Both accept `query`, `repo_path`, and `known_symbols` (comma-separated).

---

## How retrieval works

Retrieval is a **cascade**: four strategies tried in order, scored after each
attempt, stopping at the first one that clears a quality bar.

```
1. multi-hop      agent knows the project (anchors in query and/or known_symbols)
2. open-ended     plain semantic retrieval
3. anchored       re-query using anchors derived from what stage 2 returned
4. lexical        grep-style filesystem search
```

### What is an "anchor"

An anchor is a piece of text that names something concrete: a file path, a
qualified name (`SQLCompiler.get_from_clause`), a function call, or an
identifier-shaped token (`snake_case`, `camelCase`, `PascalCase`,
acronym-prefixed). Anchors drive exact lookup and are used as evidence when
scoring a result.

If a query contains no anchors and the agent supplies no `known_symbols`,
**stage 1 is skipped**. That is deliberate — a semantic query should not be
forced through a symbol-graph path it cannot use.

### Scoring

Each stage's result is scored 0..1:

```
score = 0.45*AnchorCov + 0.35*TermCov + 0.20*Rich     (query has anchors)
score = 0.65*TermCov + 0.35*Rich                       (query has none)
```

- `AnchorCov` — fraction of query anchors present in the result
- `TermCov` — fraction of significant query words present
- `Rich` — fenced code blocks, `file:line` references, body length floor

The cascade stops when a stage scores ≥ `MinQuality` (default 0.55). If no stage
clears it, the highest-scoring candidate is returned anyway, prefixed with a
note explaining what happened.

### Why a lexical fallback exists

The router's `grepSymbolSearch` strategy only sees chunks already in the
in-memory index and only matches when the query yields symbols. `lexicalSearch`
walks the actual filesystem, so it can rescue queries that carry no extractable
symbols or that the index has missed or gone stale on. It is deliberately last
because it is the most expensive and least precise.

### Project memory

A small per-repository memory records which symbols appeared together in
**trusted** results (those that cleared `MinQuality`). It does two jobs:

- expand multi-hop queries with remembered neighbour symbols
- derive follow-up anchors from an open-ended result

Memory never decides the strategy on its own. That decision depends solely on
what the calling agent knows.

---

## Configuration

### CLI flags

```
eulix mcp [path] [flags]

  --cot                Default to CoT when 'include_cot' is not specified
  --repo string        Initial repository (can also be set via set_repo_path)
  --timeout duration   Idle timeout before releasing memory (default 5m)
```

### Server options (library use)

| Option                  | Purpose                                           |
| ----------------------- | ------------------------------------------------- |
| `WithIdleTimeout(d)`    | Idle timeout before freeing the context builder   |
| `WithDefaultCoT(bool)`  | Default CoT behavior for the generic `query` tool |
| `WithInitialRepo(path)` | Bind a repository at startup                      |
| `WithIO(in, out)`       | Custom streams (tests)                            |
| `WithLogger(l)`         | Custom `slog.Logger` (default: stderr)            |
| `WithStrategy(cfg)`     | Tune the retrieval cascade                        |
| `WithRetriever(r)`      | Replace the Router-backed retriever               |

### Strategy tuning

```go
type StrategyConfig struct {
    MinQuality        float64       // stop threshold, default 0.55
    MaxExpansion      int           // multi-hop neighbours, default 8
    MaxDerivedAnchors int           // anchors from open-ended, default 8
    MaxMemorySymbols  int           // per-repo symbol memory, default 4000
    LexicalTimeout    time.Duration // filesystem walk bound, default 10s
    LexicalMaxFiles   int           // files in lexical result, default 8
    LexicalMaxBytes   int           // size cap, default 24000
}
```

Zero fields fall back to defaults.

---

## Connecting a client

### Continue (VS Code)

Create `.continue/mcpServers/eulix.yaml` at the workspace root:

```yaml
name: Eulix
version: 0.0.1
schema: v1
mcpServers:
  - name: eulix
    command: /absolute/path/to/eulix_mcp
    args:
      - mcp
      - --repo
      - /path/to/repo
```

MCP tools are only available in **Agent mode**. If tools don't appear, check
the Continue output panel for connection errors.

### LM Studio

Edit `mcp.json` via **Program → Install → Edit mcp.json**:

```json
{
  "mcpServers": {
    "eulix": {
      "command": "/absolute/path/to/eulix_mcp",
      "args": ["mcp", "--repo", "/path/to/repo"]
    }
  }
}
```

LM Studio shows a confirmation dialog before each tool call, which is useful
for inspecting exactly what the model is asking for.

### MCP Inspector

The official debugging tool. Connects directly to the server with no model in
the loop:

```bash
npx @modelcontextprotocol/inspector \
  /absolute/path/to/eulix_mcp \
  mcp --repo /path/to/repo
```

Opens a browser UI at `http://localhost:5173`. Useful for verifying tool
schemas and testing calls with raw JSON arguments.

---

## Testing without a model

MCP stdio is newline-delimited JSON-RPC. You can drive the server directly:

```python
#!/usr/bin/env python3
import json, subprocess, sys, time

p = subprocess.Popen(
    ["/absolute/path/to/eulix_mcp", "mcp", "--repo", "/path/to/repo"],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE,
    stderr=subprocess.PIPE, text=True, bufsize=1,
)

def send(obj):
    p.stdin.write(json.dumps(obj) + "\n"); p.stdin.flush()

def recv(timeout=60):
    import select
    r, _, _ = select.select([p.stdout], [], [], timeout)
    return json.loads(p.stdout.readline()) if r else None

send({"jsonrpc":"2.0","id":1,"method":"initialize","params":{
    "protocolVersion":"2025-06-18","capabilities":{},
    "clientInfo":{"name":"py","version":"1.0"}}})
recv()

send({"jsonrpc":"2.0","method":"notifications/initialized"})
time.sleep(0.3)

send({"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
    "name":"query","arguments":{"query": sys.argv[1]}}})
print(json.dumps(recv(), indent=2))

p.terminate()
```

Run it with a query string. Cold queries should complete in 1–3 seconds on an
analyzed repository.

---

## Debugging retrieval

Add a trace log line in `retrieveLocked` before returning:

```go
s.log.Info("retrieval", "trace", trace.String(),
    "chosen_score", fmt.Sprintf("%.2f", best.score))
```

Each query then emits one line showing which stages ran, what each scored, and
which was chosen:

```
retrieval trace="anchors=[applyBudget] path=[multi-hop(0.42) -> open-ended(0.38) -> lexical(0.51)] chosen=lexical"
```

This is the fastest way to answer "why did this query return bad context?" —
you can see whether the router failed and lexical rescued it, or whether the
router returned something that scored just below `MinQuality`.

### Interpreting results

- **Result starts with `> Semantic retrieval was low-confidence...`** — the
  lexical fallback fired. The router failed on this query.
- **Result starts with `Context: N source chunks...`** — the router answered
  directly. This is the normal path.
- **Result is `No relevant context was found...`** — every stage failed. The
  repo may not be analyzed, or the query is too vague.

---

## Model compatibility

MCP tool calling requires a model that emits structured `tool_calls`. Not all
models do.

| Model               | Tool calling | Notes                                        |
| ------------------- | ------------ | -------------------------------------------- |
| `qwen2.5-coder:14b` | Yes          | Reliable; verify `ollama show` lists `tools` |
| `qwen2.5:14b`       | Yes          | Base model, not coder                        |
| `llama3.1:8b`       | Yes          | Reliable function calling                    |
| `mistral-nemo:12b`  | Yes          | Good balance                                 |
| `deepseek-r1:14b`   | No           | Reasoning-only; emits prose, not calls       |

**Ollama caveat:** even models with `tools` in `ollama show` may return tool
calls as plain text in `message.content` instead of `message.tool_calls` if
their chat template lacks a `{{ .ToolCalls }}` block. Test with a raw curl
before blaming the client:

```bash
curl http://localhost:11434/api/chat -d '{
  "model": "qwen2.5-coder:14b",
  "messages": [{"role": "user", "content": "search for applyBudget"}],
  "tools": [{"type":"function","function":{
    "name":"query",
    "parameters":{"type":"object","properties":{"query":{"type":"string"}}}
  }}],
  "stream": false
}' | jq '.message | {content, tool_calls}'
```

If `tool_calls` is populated, the model works. If `content` contains a JSON
blob, the model's template is misconfigured — switch models or patch the
template.

---

## Status

Beta. The stack is verified working:

- MCP protocol handshake and capability negotiation
- Tool schema generation and validation
- Cascade execution with stage selection
- Router path for anchored and open-ended queries
- Lexical fallback for symbol-less or index-miss queries
- Response formatting

Known gaps:

- Retrieval trace is not logged by default; add it before deploying to users.
- The lexical fallback does not skip directories present in `.euignore` or dedupe
  identical content across files, which wastes budget on repos with backup
  trees.
- `composeAnchored` relies on the router parsing a trailing `Key symbols:`
  line. This is an implicit contract; changing the router's prompt format can
  silently break multi-hop.

---

## License

Apache-2.0. See `LICENSE`.
