# Known Issues

This section lists the bugs and limitations that Eulix has today, how serious they are, how we know about them, and what to do in the meantime.

> **Scope:** all subsystems  
> **Last verified against:** `ad744e9` (2026-11-03)

## How to read this section

Every entry has an ID, a severity, a status and a verification note, then a short description of the symptom, the cause, a workaround and the direction of a fix. The IDs are stable, so other documents can link to them.

**Severity** is a suggestion for triage, not a formal score:

| Level | Meaning |
| --- | --- |
| High | Wrong answers or wrong data in a common situation, or a security concern. |
| Medium | Wrong or missing results in a specific situation, or a real cost to users. |
| Low | Cosmetic, rare, or only affects developers. |

**Status:**

| Status | Meaning |
| --- | --- |
| Open | A bug. Not fixed. |
| Partly fixed | Some part is fixed; the rest remains. The entry says which. |
| By design | Behaviour that was chosen on purpose. Listed because it surprises people. |
| Limitation | Something Eulix does not do, and is not planned to do soon. |

**Verification** says how confident to be in the entry:

| Label | Meaning |
| --- | --- |
| Reproduced | I ran it on 2026-11-03 against the current code and saw the problem. |
| Read in code | I found it by reading the current source; I did not run it. |
| Carried over | From an earlier document written by the maintainers. I did not re-check it. |

Anything fixed is not listed here as an open problem. Fixed items are kept for the record in [Fixed Issues](Fixed-Issues.md), so you can see what used to be broken and which test guards it.

## Pages

| Page | Covers |
| --- | --- |
| [Parser And PRISM](Parser-And-PRISM.md) | Parsing gaps and call-graph resolution (`libeulix`, `eulix-parser`). |
| [Retrieval And Query](Retrieval-And-Query.md) | How context is chosen (`eulix-cli` retrieval and router). |
| [Embeddings](Embeddings.md) | The embedding tool and the query embedder. |
| [CLI, MCP And TUI](CLI-MCP-And-TUI.md) | The command line, the chat screens, the MCP server and the model client. |
| [Fixed Issues](Fixed-Issues.md) | Resolved problems and the tests that guard them. |

## Summary of open items

| ID | Issue | Severity | Status | Verification |
| --- | --- | --- | --- | --- |
| [PRISM-1](Parser-And-PRISM.md#prism-1-call-graphs-are-approximate) | Call graphs are approximate | Medium | By design | Carried over |
| [PRISM-2](Parser-And-PRISM.md#prism-2-v2-never-links-calls-to-methods-in-go-java-javascript-c-and-c) | PRISM v2 never links calls to methods in Go, Java, JavaScript, C and C++ | High | Partly fixed | Reproduced |
| [PRISM-3](Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2) | Calls on a receiver of unknown type get no edge in v2 | Medium | By design | Reproduced |
| [PRISM-4](Parser-And-PRISM.md#prism-4-calls-resolve-across-languages) | Calls resolve across languages | High | Open | Reproduced |
| [PRISM-5](Parser-And-PRISM.md#prism-5-go-and-java-receivers-never-reach-the-resolver-java-new-calls-never-resolve) | Go and Java receivers never reach the resolver; Java `new` calls never resolve | Medium | Open | Reproduced |
| [PRISM-6](Parser-And-PRISM.md#prism-6-javascript-classes-record-no-base-class) | JavaScript classes record no base class | Medium | Open | Reproduced |
| [PRISM-7](Parser-And-PRISM.md#prism-7-c-calls-are-recorded-twice) | C++ calls are recorded twice | Medium | Open | Reproduced |
| [PRISM-8](Parser-And-PRISM.md#prism-8-v2-is-very-slow-on-large-rust-workspaces-with-shared-type-names) | PRISM v2 is very slow on large Rust workspaces with shared type names | Medium | Open | Reproduced |
| [PRISM-9](Parser-And-PRISM.md#prism-9-php-call-graph-is-partial-for-dynamic-code) | PHP call graph is partial for dynamic code | Low | Limitation | Carried over |
| [PARSE-1](Parser-And-PRISM.md#parse-1-unsupported-languages) | Unsupported languages (Ruby, C# and others) | Medium | Limitation | Read in code |
| [PARSE-2](Parser-And-PRISM.md#parse-2-jsx-and-tsx-files-are-rejected) | `.jsx` and `.tsx` files are rejected | High | Open | Reproduced |
| [PARSE-3](Parser-And-PRISM.md#parse-3-c-and-c-files-parse-only-partly) | C and C++ files parse only partly | Low | By design | Carried over |
| [PARSE-4](Parser-And-PRISM.md#parse-4-dependency_graph-is-never-filled-some-cargo-features-do-nothing) | `dependency_graph` is never filled; some Cargo features do nothing | Low | Open | Read in code |
| [RET-1](Retrieval-And-Query.md#ret-1-the-kb_exact-search-strategy-never-returns-anything) | The `kb_exact` search strategy never returns anything | High | Open | Read in code |
| [RET-2](Retrieval-And-Query.md#ret-2-cross-root-demotion-only-works-for-two-folder-names) | Cross-root demotion only works for two folder names | Medium | Open | Read in code |
| [RET-3](Retrieval-And-Query.md#ret-3-deep-multi-hop-questions-may-be-answered-inaccurately) | Deep multi-hop questions may be answered inaccurately | Medium | Open | Carried over |
| [RET-4](Retrieval-And-Query.md#ret-4-ehnas-word-feedback-drift-hurts-some-conceptual-questions) | EHNAS: word-feedback drift hurts some conceptual questions | Medium | Open | Carried over |
| [RET-5](Retrieval-And-Query.md#ret-5-ehnas-several-benchmark-questions-still-fall-back-to-text-search) | EHNAS: several benchmark questions still fall back to text search | Medium | Open | Carried over |
| [RET-6](Retrieval-And-Query.md#ret-6-the-monte-carlo-lexical-layer-depends-on-map-order) | The Monte Carlo lexical layer depends on map order | Low | Open | Carried over |
| [RET-7](Retrieval-And-Query.md#ret-7-embedder-is-never-restarted-after-a-timeout) | Embedder is never restarted after a timeout | Medium | Open | Carried over |
| [RET-8](Retrieval-And-Query.md#ret-8-names-from-a-stale-knowledge-base-can-still-appear) | Names from a stale knowledge base can still appear | Low | Open | Carried over |
| [RET-9](Retrieval-And-Query.md#ret-9-goroutine-leak-per-repository-switch-debug-logging-stops-after-the-first-idle-release) | Each repository switch leaks a goroutine; debug logging stops after the first idle release | Low | Open | Carried over |
| [EMB-1](Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings) | ONNX on ROCm produces wrong embeddings | High | Partly fixed | Carried over |
| [EMB-2](Embeddings.md#emb-2-a-repository-still-chooses-which-embedding-model-is-downloaded) | A repository still chooses which embedding model is downloaded | Medium | Open | Carried over |
| [EMB-3](Embeddings.md#emb-3-rocm-prints-null-no-such-file-or-directory) | ROCm prints `(null): No such file or directory` | Low | Limitation | Carried over |
| [EMB-4](Embeddings.md#emb-4-profiling-the-launcher-misses-the-embedding-workers-memory) | Profiling the launcher misses the embedding worker's memory | Low | Limitation | Carried over |
| [EMB-5](Embeddings.md#emb-5-unused-and-unsafe-embedder-methods) | Unused and unsafe `Embedder` methods | Low | Open | Carried over |
| [EMB-6](Embeddings.md#emb-6-embedded-text-has-no-line-breaks) | Embedded chunk text has no line breaks | High | Open | Reproduced |
| [EMB-7](Embeddings.md#emb-7-cards-omit-calls-complexity-exceptions-imports-and-docstrings) | Chunk text omits calls, complexity, exceptions, imports and docstrings | Medium | Open | Reproduced |
| [CLI-1](CLI-MCP-And-TUI.md#cli-1-each-chat-answer-is-probably-saved-to-history-twice) | Each chat answer is probably saved to history twice | Medium | Open | Read in code |
| [CLI-2](CLI-MCP-And-TUI.md#cli-2-the-history-viewer-deletes-the-wrong-entry-does-not-refresh-and-has-no-way-back) | The history viewer deletes the wrong entry, does not refresh and has no way back | Medium | Open | Read in code |
| [CLI-3](CLI-MCP-And-TUI.md#cli-3-checksum-settings-are-ignored) | `[checksum]` settings are ignored | Low | Open | Read in code |
| [CLI-4](CLI-MCP-And-TUI.md#cli-4-eulix-query-prints-a-prompt-and-never-calls-the-model) | `eulix query` prints a prompt and never calls the model | Low | By design | Read in code |
| [CLI-5](CLI-MCP-And-TUI.md#cli-5-model-requests-cannot-be-cancelled-are-not-streamed-and-have-three-smaller-defects) | Model requests cannot be cancelled, are not streamed, and have three smaller defects | Medium | Open | Read in code |
| [CLI-6](CLI-MCP-And-TUI.md#cli-6-history-entries-with-equal-timestamps-list-in-arbitrary-order) | History entries with equal timestamps list in arbitrary order | Low | Open | Read in code |
| [CLI-7](CLI-MCP-And-TUI.md#cli-7-developer-facing-loose-ends) | Developer-facing loose ends (logger races, lint config, stale comments, dead repair tools) | Low | Open | Read in code |
| [MCP-1](CLI-MCP-And-TUI.md#mcp-1-a-repositorys-own-eulix-and-eulixtoml-are-trusted) | A repository's own `.eulix/` and `eulix.toml` are trusted | High | Open | Carried over |
| [MCP-2](CLI-MCP-And-TUI.md#mcp-2-a-panic-in-a-handler-takes-the-mcp-server-down) | A panic in a handler takes the MCP server down | Low | Open | Read in code |
| [MCP-3](CLI-MCP-And-TUI.md#mcp-3-mcp-text-search-fallback-gaps) | MCP text-search fallback gaps | Low | Open | Read in code |

## Related Docs

- [Fixed Issues](Fixed-Issues.md): the resolved list.
- [libeulix PRISM v2](../libeulix/PRISM-V2.md), [eulix-cli Overview](../eulix-cli/Overview.md): the subsystems the issues belong to.
