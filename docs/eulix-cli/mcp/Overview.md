# MCP Server

The `mcp` package lets AI coding agents ask Eulix questions about your code over the Model Context Protocol.

> **Crate / path:** `eulix-cli/internal/mcp/`  
> **Language:** Go, using the official `go-sdk` for MCP  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

AI coding assistants such as Claude Code, Cursor, Windsurf, Cline and Continue can use external "tools". The Model Context Protocol (MCP) is the standard way they discover and call them. `eulix mcp` starts a tool server that speaks that protocol over standard input and output. The assistant calls a tool named `query` with a question, and Eulix replies with ranked source code and call-graph facts that the assistant can use in its own answer.

The server does the retrieval, not the answering. It never calls a language model itself; the assistant brings its own. That is why the MCP replies are *context*, not *answers*.

This folder documents the package in four pages: [Server And Tools](Server-And-Tools.md) for the protocol surface and lifecycle, [Retrieval Cascade](Retrieval-Cascade.md) for how a question is answered, [Anchors And Scoring](Anchors-And-Scoring.md) for how results are judged, and [Memory And Lexical Fallback](Memory-And-Lexical-Fallback.md) for the last-resort search and the per-project memory.

## Why It Exists

Querying from the command line costs the full start-up each time: load the knowledge base, build the indices, start the embedder. An assistant may ask dozens of questions in one session. The MCP server stays running, keeps the loaded index in memory between questions, and frees it after an idle period. It also adapts: it can tell when the assistant already knows which symbols matter, and use that to search more precisely.

## How It Works

1. You start `eulix mcp [path]` (or the assistant starts it from its configuration). The server prints nothing of its own to standard output, because that stream carries the protocol; all logs go to standard error.
2. The assistant connects and the server announces its four tools and two prompts.
3. A repository is chosen: from the command line, from a `repo_path` argument on a call, from the `set_repo_path` tool, or, if none of those happened, from the client's own "roots" (the folders open in the editor).
4. Each `query` call runs the [retrieval cascade](Retrieval-Cascade.md): up to four search strategies in order, stopping at the first one whose result scores well enough.
5. After 5 idle minutes (configurable), the server frees the loaded index. The next question reloads it.

The repository must already have been analyzed with `eulix analyze`. If not, the server answers with an instruction to run it.

### Main Components

| File | Responsibility |
| --- | --- |
| `server.go` | The server object, options, tool and prompt registration, binding from client roots. |
| `tools.go` | The tool and prompt handlers. |
| `helpers.go` | Switching repositories, running queries, the idle timer, debug log. |
| `lifecycle.go` | `Serve` and `Close`. |
| `strategy*.go` | The retrieval cascade, anchors, scoring, memory and the text-search fallback. |
| `hidden_*.go` | Detecting hidden files on each operating system. |

### Data In, Data Out

In: JSON-RPC messages on standard input. Out: JSON-RPC replies on standard output, containing text. A reply is the retrieved context, optionally with reasoning instructions, or a short message explaining why nothing was found.

## Key Algorithms

The cascade and the scoring are covered in their own pages. In one sentence: try the most precise strategy the assistant's input allows, score each result against the question, stop at the first good one, and fall back to plain text search if nothing scores well.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `--repo <path>` or positional `[path]` | command line | the current folder, if it has a knowledge base | The starting repository. |
| `--timeout` | command line | 5m | Idle time before the index is released. |
| `--cot` | command line | off | Default for `include_cot` when a call does not specify it. |
| Strategy settings | code (`StrategyConfig`) | see [Retrieval Cascade](Retrieval-Cascade.md) | Tuning for library users. |
| `retrievalConfig.etiya.lexical_exclude` | `eulix.toml` | none | Paths the text-search fallback skips (applied only with the `etiya` engine). |

## Usage Examples

A generic client configuration:

```json
{
  "mcpServers": {
    "eulix": {
      "command": "eulix",
      "args": ["mcp", "--repo", "/path/to/repo"]
    }
  }
}
```

To try the server without any model, the official inspector works:

```bash
npx @modelcontextprotocol/inspector eulix mcp --repo /path/to/repo
```

Expected result: a browser page listing the tools, where you can call `query` with raw arguments.

## Performance Notes

The package README says cold queries on an analyzed repository should complete in 1 to 3 seconds. I did not measure this. Keeping the index warm is what makes later queries fast.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Beta. | The protocol surface is verified, but retrieval quality under real agents is still being tuned. | Report bad results with the debug log. |
| The assistant must support tool calling. | MCP only works with models that emit structured tool calls. Some local models do not. | Choose a tool-capable model. |
| Multi-hop retrieval relies on an implicit contract. | Anchors are passed to the router by appending a `Key symbols:` line to the question. If the router's prompt format changes, multi-hop can silently weaken. | Test after changing the router. |

## Related Docs

- [Server And Tools](Server-And-Tools.md): the protocol surface.
- [Retrieval Cascade](Retrieval-Cascade.md): how a question is answered.
- [query Overview](../query/Overview.md): the router the MCP server wraps.
- [Retrieval Overview](../query/retrieval/Overview.md): the engine underneath.
