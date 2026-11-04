# Welcome to Eulix

Eulix turns a code repository into something you can ask questions of: where a function is defined, who calls it, how a feature works, what depends on what. It is built for large, mixed-language repositories, runs on your own machine, and uses a language model only when a question actually needs one.

> **Last verified against:** `ad744e9`

## The idea in one minute

Most questions about code do not need an AI. "Where is `applyBudget` defined?" has an exact answer in the code itself. Eulix therefore does the expensive work **once**, up front: it reads every file and records what is in it and how the pieces connect. After that:

- Factual questions (locations, callers, entry points, dependencies, metrics, TODOs) are answered straight from that record, instantly, with no chance of invention.
- Questions that need understanding ("why does the budget code split tokens this way?") go through _retrieval_: Eulix picks the few pieces of code that matter, fits them into a model's limited window, and asks the model to explain them, with instructions to say what it cannot see.

> **Parse once. Navigate many times.**

## The pieces

```text
your repository
      │
      ▼
 eulix_parser ──── uses ──── libeulix          read code → knowledge base (JSON files)
      │                       (Rust)           and a call graph (PRISM)
      ▼
 eulix_embed                                   knowledge base → vectors (optional)
      │
      ▼
 .eulix/  ◄──────────────────────────────┐
      │                                  │
      ▼                                  │
 eulix  (Go)  ── chat / query / mcp ─────┘     route the question, retrieve code,
      │                                        build the prompt, call the model
      ├── vizeulize                             browse the call graph
      └── MCP server ──► AI coding agents
```

| Piece            | What it is                                                                                                                                                     | Docs                                                                                                                                                 |
| ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| **libeulix**     | The Rust engine that parses nine languages and links calls to definitions.                                                                                     | [libeulix](libeulix/Overview.md)                                                                                                                     |
| **eulix-parser** | The command-line program that runs it over a repository and writes the JSON files. Fast on large code because it saves work to disk and skips unchanged files. | [eulix-parser](eulix-parser/Overview.md), [Spill to Disk](Architecture/Spill-To-Disk.md), [Incremental Parsing](Architecture/Incremental-Parsing.md) |
| **eulix-embed**  | The Python tool that turns code into vectors so questions can match code by meaning.                                                                           | [eulix-embed](eulix-embed/Overview.md)                                                                                                               |
| **eulix-cli**    | The `eulix` program you use: setup, analysis, chat, one-shot queries, the MCP server. Contains the question router and the two retrieval engines.              | [eulix-cli](eulix-cli/Overview.md)                                                                                                                   |
| **vizEulize**    | A browser page that draws the call graph.                                                                                                                      | [vizEulize](vizEulize/Overview.md)                                                                                                                   |
| **eulix-lsp**    | A language server built on libeulix. Not documented here yet.                                                                                                  |                                                                                                                                                      |

## Try it

Install first: see [Installation](Installation.md).

```bash
cd your-project
eulix init        # create .eulix/, .euignore and eulix.toml (a short wizard)
eulix analyze     # parse the code and build vectors; fast when little has changed
eulix chat        # ask questions in a terminal interface
```

Other ways in:

```bash
eulix query "who calls applyBudget?"   # print the answer, or the context prompt for a model
eulix mcp --repo .                      # let an AI coding agent query your code
eulix vizeulize                         # explore the call graph in a browser
```

## What to know before you rely on it

- **The call graph is approximate.** It links calls by names and scopes, not by types. Treat edges as strong hints, not proof. See [PRISM v2](libeulix/PRISM-V2.md).
- **Some languages are better covered than others.** Method calls resolve best in Python, TypeScript and Rust today. See [Known Issues](Known-Issues/Overview.md).
- **The model is optional and replaceable.** Local models (Ollama, LM Studio, vLLM) and many hosted providers work. See [Llm](eulix-cli/Llm.md).
- **Your code stays on your machine** unless you choose a hosted model, in which case the retrieved snippets are sent to that provider.
- **Parts of this project are in beta**: the MCP server, the chat screen, and retrieval tuning.

## Where to go next

| You are...                    | Start with                                                                              |
| ----------------------------- | --------------------------------------------------------------------------------------- |
| New and curious               | This page, then [eulix-cli Overview](eulix-cli/Overview.md)                             |
| Setting up a project          | [Config](eulix-cli/Config.md) and [eulix-parser Overview](eulix-parser/Overview.md)     |
| Connecting an AI agent        | [MCP Overview](eulix-cli/mcp/Overview.md)                                               |
| Wanting to understand answers | [Retrieval Overview](eulix-cli/query/retrieval/Overview.md)                             |
| Working on the code           | [libeulix Public API](libeulix/Public-API.md), [Known Issues](Known-Issues/Overview.md) |
