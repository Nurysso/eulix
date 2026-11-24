# Known Issues

This section lists the bugs and limitations that Eulix has today, how serious they are, how we know about them, and what to do in the meantime.

> **Scope:** all subsystems
> **Last verified against:** `ad744e9` (2026-11-03); newer entries name their own commit

## How to read this section

Every entry has an ID, a severity, a status and a verification note, then a short description of the symptom, the cause, a workaround and the direction of a fix. The IDs are stable, so other documents can link to them.

**Severity** is a suggestion for triage, not a formal score:

| Level  | Meaning                                                                    |
| ------ | -------------------------------------------------------------------------- |
| High   | Wrong answers or wrong data in a common situation, or a security concern.  |
| Medium | Wrong or missing results in a specific situation, or a real cost to users. |
| Low    | Cosmetic, rare, or only affects developers.                                |

**Status:**

| Status       | Meaning                                                                   |
| ------------ | ------------------------------------------------------------------------- |
| Open         | A bug. Not fixed.                                                         |
| Partly fixed | Some part is fixed; the rest remains. The entry says which.               |
| By design    | Behaviour that was chosen on purpose. Listed because it surprises people. |
| Limitation   | Something Eulix does not do, and is not planned to do soon.               |
| Won't fix    | A known bug we chose not to fix. The entry says why.                      |

**Verification** says how confident to be in the entry:

| Label        | Meaning                                                                     |
| ------------ | --------------------------------------------------------------------------- |
| Reproduced   | I ran it against the commit the page names (2026-11-03 unless the entry gives another date) and saw the problem. |
| Read in code | I found it by reading the current source; I did not run it.                 |
| Carried over | From an earlier document written by the maintainers. I did not re-check it. |

Anything fixed is not listed here as an open problem. Fixed items are kept for the record in [Fixed Issues](Fixed-Issues.md), so you can see what used to be broken and which test guards it.

## Pages

| Page                                          | Covers                                                                   |
| --------------------------------------------- | ------------------------------------------------------------------------ |
| [Parser And PRISM](Parser-And-PRISM.md)       | Parsing gaps and call-graph resolution (`libeulix`, `eulix-parser`).     |
| [Retrieval And Query](Retrieval-And-Query.md) | How context is chosen (`eulix-cli` retrieval and router).                |
| [Embeddings](Embeddings.md)                   | The embedding tool and the query embedder.                               |
| [CLI, MCP And TUI](CLI-MCP-And-TUI.md)        | The command line, the chat screens, the MCP server and the model client. |
| [LSP](LSP.md)                                 | The `eulix-lsp` language server: rename, URIs, indexing cost and memory. |
| [Fixed Issues](Fixed-Issues.md)               | Resolved problems and the tests that guard them.                         |
| [Open — Re-verified](Open-Reverified.md)      | A fresh re-run of the repro scripts, recording which issues still happen. |

## Summary of open items

| ID                                                                                                                          | Issue                                                                                      | Severity | Status       | Verification |
| --------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ | -------- | ------------ | ------------ |
| [PRISM-1](Parser-And-PRISM.md#prism-1-call-graphs-are-approximate)                                                          | Call graphs are approximate                                                                | Medium   | By design    | Carried over |
| [PRISM-2](Parser-And-PRISM.md#prism-2-v2-never-links-calls-to-methods-in-go-java-javascript-c-and-c)                        | PRISM v2 never links calls to methods in Go, Java, JavaScript, C and C++                   | High     | Partly fixed | Reproduced   |
| [PRISM-3](Parser-And-PRISM.md#prism-3-calls-on-a-receiver-of-unknown-type-get-no-edge-in-v2)                                | Calls on a receiver of unknown type get no edge in v2                                      | Medium   | By design    | Reproduced   |
| [PRISM-4](Parser-And-PRISM.md#prism-4-calls-resolve-across-languages)                                                       | Calls resolve across languages                                                             | High     | Open         | Reproduced   |
| [PRISM-5](Parser-And-PRISM.md#prism-5-go-and-java-receivers-never-reach-the-resolver-java-new-calls-never-resolve)          | Go and Java receivers never reach the resolver; Java `new` calls never resolve             | Medium   | Fixed        | Reproduced   |
| [PRISM-6](Parser-And-PRISM.md#prism-6-javascript-classes-record-no-base-class)                                              | JavaScript classes record no base class                                                    | Medium   | Open         | Reproduced   |
| [PRISM-7](Parser-And-PRISM.md#prism-7-c-calls-are-recorded-twice)                                                           | C++ calls are recorded twice                                                               | Medium   | Open         | Reproduced   |
| [PRISM-8](Parser-And-PRISM.md#prism-8-v2-is-very-slow-on-large-rust-workspaces-with-shared-type-names)                      | PRISM v2 is very slow on large Rust workspaces with shared type names                      | Medium   | Open         | Reproduced   |
| [PRISM-9](Parser-And-PRISM.md#prism-9-php-call-graph-is-partial-for-dynamic-code)                                           | PHP call graph is partial for dynamic code                                                 | Low      | Limitation   | Carried over |
| [PRISM-10](Parser-And-PRISM.md#prism-10-go-calls-link-to-same-named-struct-types-and-to-look-alike-functions)                 | Go calls link to same-named struct types and to look-alike functions                       | High     | Fixed        | Reproduced   |
| [PRISM-11](Parser-And-PRISM.md#prism-11-javascript-calls-on-built-in-objects-link-to-project-functions-of-the-same-name)      | JavaScript calls on built-in objects link to project functions of the same name            | Medium   | Open         | Reproduced   |
| [PRISM-12](Parser-And-PRISM.md#prism-12-typescript-ignores-declared-types-new-calls-never-link)                               | TypeScript ignores declared types; `new` calls never link                                  | Medium   | Open         | Reproduced   |
| [PRISM-13](Parser-And-PRISM.md#prism-13-c-member-calls-on-library-objects-link-to-project-methods-of-the-same-name)           | C++ member calls on library objects link to project methods of the same name               | High     | Open         | Reproduced   |
| [PARSE-1](Parser-And-PRISM.md#parse-1-unsupported-languages)                                                                | Unsupported languages (Ruby, C# and others)                                                | Medium   | Limitation   | Read in code |
| [PARSE-2](Parser-And-PRISM.md#parse-2-jsx-and-tsx-files-are-rejected)                                                       | `.jsx` and `.tsx` files are rejected                                                       | High     | Open         | Reproduced   |
| [PARSE-3](Parser-And-PRISM.md#parse-3-c-and-c-files-parse-only-partly)                                                      | C and C++ files parse only partly                                                          | Low      | By design    | Carried over |
| [PARSE-4](Parser-And-PRISM.md#parse-4-dependency_graph-is-never-filled-some-cargo-features-do-nothing)                      | `dependency_graph` is never filled; some Cargo features do nothing                         | Low      | Open         | Read in code |
| [PARSE-7](Parser-And-PRISM.md#parse-7-java-methods-inside-anonymous-classes-are-not-extracted)                                | Java methods inside anonymous classes are not extracted                                    | Medium   | Open         | Reproduced   |
| [PARSE-8](Parser-And-PRISM.md#parse-8-overloads-and-same-name-definitions-in-one-file-share-one-id)                           | Overloads and same-name definitions in one file share one id                               | Medium   | Open         | Reproduced   |
| [PARSE-9](Parser-And-PRISM.md#parse-9-javascript-functions-that-are-not-declared-or-bound-to-a-name-have-no-node)             | JavaScript functions that are not declared or bound to a name have no node                 | Medium   | Open         | Reproduced   |
| [RET-1](Retrieval-And-Query.md#ret-1-the-kb_exact-search-strategy-never-returns-anything)                                   | The `kb_exact` search strategy never returns anything                                      | High     | Open         | Read in code |
| [RET-2](Retrieval-And-Query.md#ret-2-cross-root-demotion-only-works-for-two-folder-names)                                   | Cross-root demotion only works for two folder names                                        | Medium   | Open         | Read in code |
| [RET-3](Retrieval-And-Query.md#ret-3-deep-multi-hop-questions-may-be-answered-inaccurately)                                 | Deep multi-hop questions may be answered inaccurately                                      | Medium   | Open         | Carried over |
| [RET-4](Retrieval-And-Query.md#ret-4-ehnas-word-feedback-drift-hurts-some-conceptual-questions)                             | EHNAS: word-feedback drift hurts some conceptual questions                                 | Medium   | Open         | Carried over |
| [RET-5](Retrieval-And-Query.md#ret-5-ehnas-several-benchmark-questions-still-fall-back-to-text-search)                      | EHNAS: several benchmark questions still fall back to text search                          | Medium   | Open         | Carried over |
| [RET-6](Retrieval-And-Query.md#ret-6-the-monte-carlo-lexical-layer-depends-on-map-order)                                    | The Monte Carlo lexical layer depends on map order                                         | Low      | Open         | Carried over |
| [RET-7](Retrieval-And-Query.md#ret-7-embedder-is-never-restarted-after-a-timeout)                                           | Embedder is never restarted after a timeout                                                | Medium   | Open         | Carried over |
| [RET-8](Retrieval-And-Query.md#ret-8-names-from-a-stale-knowledge-base-can-still-appear)                                    | Names from a stale knowledge base can still appear                                         | Low      | Open         | Carried over |
| [RET-10](Retrieval-And-Query.md#ret-10-on-windows-eulix-analyze-can-fail-while-eulix-reads-the-same-knowledge-base) | On Windows, `eulix analyze` can fail while Eulix reads the same knowledge base | Low | Won't fix | Reproduced |
| [RET-11](Retrieval-And-Query.md#ret-11-quitting-chat-during-a-vector-search-can-crash-on-linux)                             | Quitting chat during a vector search can crash on Linux                                    | Low      | Won't fix    | Read in code |
| [RET-13](Retrieval-And-Query.md#ret-13-a-question-naming-a-python-method-gets-the-class-not-the-method)                     | A question naming a Python method gets the class, not the method                           | Medium   | Open         | Reproduced   |
| [EMB-1](Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings)                                                         | ONNX on ROCm produces wrong embeddings                                                     | High     | Partly fixed | Carried over |
| [EMB-2](Embeddings.md#emb-2-a-repository-still-chooses-which-embedding-model-is-downloaded)                                 | A repository still chooses which embedding model is downloaded                             | Medium   | Open         | Carried over |
| [EMB-3](Embeddings.md#emb-3-rocm-prints-null-no-such-file-or-directory)                                                     | ROCm prints `(null): No such file or directory`                                            | Low      | Limitation   | Carried over |
| [EMB-4](Embeddings.md#emb-4-profiling-the-launcher-misses-the-embedding-workers-memory)                                     | Profiling the launcher misses the embedding worker's memory                                | Low      | Limitation   | Carried over |
| [EMB-5](Embeddings.md#emb-5-unused-and-unsafe-embedder-methods)                                                             | Unused and unsafe `Embedder` methods                                                       | Low      | Open         | Carried over |
| [EMB-6](Embeddings.md#emb-6-embedded-text-has-no-line-breaks)                                                               | Embedded chunk text has no line breaks                                                     | High     | Open         | Reproduced   |
| [EMB-7](Embeddings.md#emb-7-cards-omit-calls-complexity-exceptions-imports-and-docstrings)                                  | Chunk text omits calls, complexity, exceptions, imports and docstrings                     | Medium   | Open         | Reproduced   |
| [CLI-1](CLI-MCP-And-TUI.md#cli-1-each-chat-answer-is-probably-saved-to-history-twice)                                       | Each chat answer is probably saved to history twice                                        | Medium   | Open         | Read in code |
| [CLI-2](CLI-MCP-And-TUI.md#cli-2-the-history-viewer-deletes-the-wrong-entry-does-not-refresh-and-has-no-way-back)           | The history viewer deletes the wrong entry, does not refresh and has no way back           | Medium   | Open         | Read in code |
| [CLI-3](CLI-MCP-And-TUI.md#cli-3-checksum-settings-are-ignored)                                                             | `[checksum]` settings are ignored                                                          | Low      | Open         | Read in code |
| [CLI-4](CLI-MCP-And-TUI.md#cli-4-eulix-query-prints-a-prompt-and-never-calls-the-model)                                     | `eulix query` prints a prompt and never calls the model                                    | Low      | By design    | Read in code |
| [CLI-5](CLI-MCP-And-TUI.md#cli-5-model-requests-cannot-be-cancelled-are-not-streamed-and-have-three-smaller-defects)        | Model requests cannot be cancelled, are not streamed, and have three smaller defects       | Medium   | Open         | Read in code |
| [CLI-6](CLI-MCP-And-TUI.md#cli-6-history-entries-with-equal-timestamps-list-in-arbitrary-order)                             | History entries with equal timestamps list in arbitrary order                              | Low      | Open         | Read in code |
| [CLI-7](CLI-MCP-And-TUI.md#cli-7-developer-facing-loose-ends)                                                               | Developer-facing loose ends (logger races, lint config, stale comments)                    | Low      | Open         | Read in code |
| [CLI-11](CLI-MCP-And-TUI.md#cli-11-where-are-x-run-is-answered-with-where-x-is-defined)                                     | "Where are X run" is answered with where X is defined                                      | Medium   | Open         | Reproduced   |
| [CLI-12](CLI-MCP-And-TUI.md#cli-12-the-classifier-never-learns-the-projects-symbols) | The classifier never learns the project's symbols | Medium   | Open         | Read in code |
| [MCP-1](CLI-MCP-And-TUI.md#mcp-1-a-repositorys-own-eulix-and-eulixtoml-are-trusted)                                         | A repository's own `.eulix/` and `eulix.toml` are trusted                                  | High     | Open         | Carried over |
| [MCP-2](CLI-MCP-And-TUI.md#mcp-2-a-panic-in-a-handler-takes-the-mcp-server-down)                                            | A panic in a handler takes the MCP server down                                             | Low      | Open         | Read in code |
| [MCP-3](CLI-MCP-And-TUI.md#mcp-3-mcp-text-search-fallback-gaps)                                                             | MCP text-search fallback gaps                                                              | Low      | Open         | Read in code |
| [MCP-6](CLI-MCP-And-TUI.md#mcp-6-project-memory-adds-unrelated-key-symbols-to-later-queries)                                | Project memory adds unrelated "Key symbols" to later queries                               | Medium   | Open         | Reproduced   |
| [LSP-1](LSP.md#lsp-1-rename-edits-names-that-the-workspace-does-not-define)                                                 | Rename edits names that the workspace does not define                                      | High     | Open         | Reproduced   |
| [LSP-2](LSP.md#lsp-2-rename-and-go-to-definition-misbehave-with-editor-encoded-uris)                                        | Rename and go-to-definition misbehave with editor-encoded URIs                             | High     | Open         | Reproduced   |
| [LSP-3](LSP.md#lsp-3-watcher-and-reparse-cost-still-grows-with-the-callers-of-common-names)                                 | Watcher and reparse cost still grows with the callers of common names                      | Medium   | Open         | Reproduced   |
| [LSP-4](LSP.md#lsp-4-references-and-incoming-calls-on-common-names-are-slow-and-memory-heavy)                               | References and incoming calls on common names are slow and memory-heavy                    | Medium   | Open         | Reproduced   |
| [LSP-5](LSP.md#lsp-5-the-index-takes-as-much-memory-as-the-parsed-files)                                                    | The index takes as much memory as the parsed files                                         | Medium   | Open         | Reproduced   |
| [LSP-6](LSP.md#lsp-6-the-scan-memory-budget-and-lazy-scan-options-do-nothing)                                               | The scan memory budget and lazy scan options do nothing                                    | Low      | Open         | Read in code |
| [LSP-7](LSP.md#lsp-7-parsing-never-uses-more-than-4-threads)                                                                | Parsing never uses more than 4 threads                                                     | Low      | Open         | Reproduced   |
| [LSP-8](LSP.md#lsp-8-shutdown-waits-for-a-running-index-commit)                                                             | Shutdown waits for a running index commit                                                  | Low      | Open         | Reproduced   |
| [LSP-9](LSP.md#lsp-9-c-macros-are-not-indexed)                                                                               | C macros are not indexed (structs in headers fixed)                                        | Medium   | Partly fixed | Reproduced   |
| [LSP-10](LSP.md#lsp-10-hover-shows-nothing-on-calls-into-other-files)                                                       | Hover shows nothing on calls into other files                                              | Low      | Limitation   | Reproduced   |

## Related Docs

- [Fixed Issues](Fixed-Issues.md): the resolved list.
- [libeulix PRISM v2](../libeulix/PRISM-V2.md), [eulix-cli Overview](../eulix-cli/Overview.md): the subsystems the issues belong to.
