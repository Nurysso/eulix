# Eulix Documentation

Start here to find where things are. Each folder has an `Overview.md` to read first.

## Where to start

New to Eulix? Read [Welcome.md](Welcome.md) first, then [Installation.md](Installation.md).

| If you want to...                       | Read                                                                                                                                 |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| Install Eulix                           | [Installation.md](Installation.md)                                                                                                   |
| Understand how Eulix is put together    | [eulix-cli/Overview.md](eulix-cli/Overview.md) (the program you run), then [libeulix/Overview.md](libeulix/Overview.md) (the engine) |
| Run the parser or read its output files | [eulix-parser/Overview.md](eulix-parser/Overview.md)                                                                                 |
| See how a question becomes an answer    | [eulix-cli/query/retrieval/Overview.md](eulix-cli/query/retrieval/Overview.md)                                                       |
| Connect an AI coding agent              | [eulix-cli/mcp/Overview.md](eulix-cli/mcp/Overview.md)                                                                               |
| Explore a call graph in the browser     | [vizEulize/Overview.md](vizEulize/Overview.md)                                                                                       |
| Check a bug before filing it            | [Known-Issues/Overview.md](Known-Issues/Overview.md)                                                                                 |

## Folders

| Folder                                         | Covers                                                                                                                                                                                                                                                                                                                                                                                                                             |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [libeulix/](libeulix/Overview.md)              | The Rust analysis library: public API, knowledge-base schema, analysis passes, PRISM v1 and v2.                                                                                                                                                                                                                                                                                                                                    |
| [eulix-parser/](eulix-parser/Overview.md)      | The command-line parser: pipeline, output files, performance notes.                                                                                                                                                                                                                                                                                                                                                                |
| [eulix-embed/](eulix-embed/Overview.md)        | The Python embedding tool: chunking, pipeline, ONNX and PyTorch backends, binary file format, query server.                                                                                                                                                                                                                                                                                                                        |
| [eulix-cli/](eulix-cli/Overview.md)            | The Go `eulix` program, one page per package: `Cli`, `Config`, `Cache`, `Checksum`, `Embeddings`, `Llm`, `Assets`, `Fixers`, `Utils`. Sub-folders: [`mcp/`](eulix-cli/mcp/Overview.md) (MCP server), [`query/`](eulix-cli/query/Overview.md) (router, classifier, prompts, strip, and [`retrieval/`](eulix-cli/query/retrieval/Overview.md) with the two engines), [`tui/`](eulix-cli/tui/Overview.md) (chat and history screens). |
| [vizEulize/](vizEulize/Overview.md)            | The browser call-graph explorer.                                                                                                                                                                                                                                                                                                                                                                                                   |
| [Architecture/](Architecture/Spill-To-Disk.md) | Cross-cutting designs: [Spill to Disk](Architecture/Spill-To-Disk.md) and [Incremental Parsing](Architecture/Incremental-Parsing.md).                                                                                                                                                                                                                                                                                              |
| [Known-Issues/](Known-Issues/Overview.md)      | Open bugs and limitations by area, plus a record of [fixed issues](Known-Issues/Fixed-Issues.md).                                                                                                                                                                                                                                                                                                                                  |

## Not written yet

`eulix-lsp`, and more pages in `Architecture/` (for example retrieval routing). See each subsystem's own README in the repository meanwhile.

## Conventions

- Folder and file names are capitalized and hyphenated, except subsystem folders, which keep the code's name (`eulix-cli`, `vizEulize`).
- Every page records the commit it was last checked against (currently `ad744e9`).
- Problems found while documenting are in [Known-Issues](Known-Issues/Overview.md); scripts that reproduce them are in `../scripts/known_issues/`.
