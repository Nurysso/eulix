# eulix-lsp

> A language server built on `libeulix`: one fast server for outline, search and navigation across nine languages.

| Language | Version | Status | Docs |
| :-- | :-- | :-- | :-- |
| Rust | 0.2.0 | Beta | [LSP indexing](../docs/Architecture/LSP-Indexing.md) |

`eulix-lsp` serves the [libeulix](https://crates.io/crates/libeulix) 0.9.0 knowledge base over the Language Server Protocol for C, C++, Go, Java, JavaScript, PHP, Python, Rust and TypeScript: symbols, go-to-definition, references, hover, completion, call hierarchy, semantic tokens and workspace diagnostics. It is fast and stable, and it matches **names, not types**. Read [Where to use it](#where-to-use-it) before relying on rename or references.

## Install

```bash
cd eulix-lsp
cargo build --release
./target/release/eulixlsp
```

From the repository root, `make install lsp` builds and installs it. Point your editor's LSP client at the built binary.

## Features

| Feature | Behaviour |
| :-- | :-- |
| Document outline | Nested, declaration-ordered |
| Hover | Signature, docstring, complexity |
| Go to definition | Same-file and cross-file, single result |
| Find references | Workspace-wide, optional declaration |
| Document highlight | Same-file occurrences of the symbol under the cursor |
| Completion | Prefix-filtered, proximity-ranked |
| Rename | Refuses when the name is ambiguous in the workspace |
| Semantic tokens | Symbol-level, delta-encoded (full document only) |
| Call hierarchy | Incoming and outgoing |
| Folding ranges | Functions and classes |
| Workspace symbols | Non-empty query required |
| Diagnostics | Syntax errors, duplicate definitions, TODOs, security notes |
| File watching | Detects changes on disk |

## Where to use it

`eulix-lsp` indexes every definition and call by name and answers from that index, without type checking or import resolution. That makes it fast and language-agnostic, and less precise than a compiler-backed server.

**Good fit:**

- Polyglot repositories where you want one server for outline, search and navigation across all languages.
- Large trees where a full language server is too slow or too heavy. The Linux kernel (50,000 files) indexes in about 49 s and 2.5 GB, and most requests then answer in under a millisecond.
- Languages or projects with no working language server, or code that doesn't build.
- Reading and exploring unfamiliar code: outline, workspace symbols, go-to-definition for plain function calls, and call hierarchy.

**Use your language's own server instead** (rust-analyzer, pyright, gopls, clangd) when you need:

- Type-aware navigation on method calls (`a.get()` vs `b.get()`).
- Safe refactoring. Treat rename here as a suggestion and check every edit.
- Precise references for common names such as `new`, `get` or `len`.

The two can run side by side; most editors let you choose which server answers which request.

## What it does not do

- **No code actions, formatting or signature help.** The parse result doesn't carry the information these need, and inventing it would be guesswork.
- **No type hover.** Hover shows what the parser extracted (signature, doc, modifiers, complexity). It doesn't resolve types through assignments or calls.
- **Semantic tokens are symbol-level, not token-level.** Every name the parser extracted gets a token; the rest of the file keeps the editor's own highlighting. There is no lexer pass behind this.
- **Rename only edits open buffers.** The reverse index gives file and line for call sites, but a precise edit needs the file's text. Editing a whole line in a file the server has never read would be worse than skipping it, so closed files are left alone. The rename still completes for open files.
- **Document highlights are same-file only.** Cross-file highlights would require opening every file that mentions the symbol.
- **Go-to-definition returns one target.** When a name has several definitions, the server picks the closest one deterministically: same file, then same directory, then shortest path, then lexicographic. Use references to see them all.
- **Completion is prefix-match only.** No fuzzy matching, and no ranking beyond proximity and sort order.
- **Workspace symbol search requires a non-empty query.** An empty query returns an empty list rather than the whole index.

## Measured behaviour

From `bench/bench_lsp.py` (details in [LSP Benchmarking](../docs/Architecture/LSP-Benchmarking.md); open issues in [LSP Known Issues](../docs/Known-Issues/LSP.md)):

| Works | Doesn't, or only partly |
| :-- | :-- |
| Indexing is linear: 4,000 files in 0.43 s; the Linux kernel's 50,000 in about 49 s. | Methods resolve by name, not receiver type, so references and call hierarchy over-count common names (36,000 results for one `helper`). |
| Outline, folding, semantic tokens, highlight and hover on declarations answered every request. | Hover on a call into another file shows nothing (60 of 60 empty). |
| Go-to-definition on calls into workspace functions resolved 60 of 60 in synthetic code. | On the eulix repository, 47 of 60 calls had no target, mostly calls into standard libraries, which aren't indexed. |
| Typing: diagnostics about 5 ms after the 200 ms debounce; 100 fast edits give one update. | **Rename** of a name with no workspace definition (e.g. `len`) edits every call to it in every open file and language. Check the preview. |
| No crashes, protocol errors or leaks in any run, including the kernel. | C and C++: macros, structs and enums aren't indexed; 11 of 20 kernel headers had an empty outline. |
| | With VS Code's encoded URIs (Windows drive colons, `+` in paths), rename skips call sites and cross-file definition selects the whole line. |
| | Calls at module level (outside any function) aren't recorded. Only the first workspace folder is indexed, and at most `maxScanFiles` (50,000) files. |

## Limitations

- JSX and TSX are not supported. `libeulix` detects them but has no parser arm for either, so the server excludes them rather than offering features it can't back up.
- Syntax diagnostics are line-granular. `SyntaxHealth` reports a count and a single `first_error_line`, so there are no per-error underlines to draw.
- `reportUnresolvedCalls` is off by default. At this analysis level, a call into the standard library, a macro, a dynamic dispatch and a typo all look the same, so turning it on without a curated allowlist produces noise.
- Definitions are recorded with line numbers but no columns, so the server finds the name in the text when it needs a precise range. This is fine on well-formed code and falls back to a whole-line range when the name isn't found.

## Development

```bash
cargo test
cargo fmt --check
cargo clippy
```

## Related components

| Component | Language | Role |
| :-- | :-- | :-- |
| [libeulix](../libeulix/) | Rust | Analysis engine: parsing, PRISM call graphs, indexes |
| [eulix-parser](../eulix-parser/) | Rust | CLI that runs libeulix over a repository and writes `.eulix/` |
| [eulix-embed](../eulix-embed/) | Python | Embedding pipeline for the ETIYA engine |
| [eulix-cli](../eulix-cli/) | Go | The `eulix` binary: retrieval engines, MCP server, chat |
| [eulix-lsp](../eulix-lsp/) | Rust | Language server built on libeulix (beta) |
| [vizEulize](../vizEulize/) | HTML/JS | Call-graph explorer in your browser |

## License

Apache 2.0, like every Eulix component. See [LICENSE](../LICENSE).

## Thank you, Gabriel

Much of what makes this server dependable comes from [Gabriel Maia](https://github.com/gabrielmaialva33). He wrote 9 of the 22 commits in this directory, and seven of his pull requests were for this server:

- Renames reach every call site, including the file that declares the symbol ([#106](https://github.com/Nurysso/eulix/pull/106)).
- Positions count characters, not bytes, so lines with non-ASCII text get the right ranges ([#105](https://github.com/Nurysso/eulix/pull/105)).
- Symbol names, ranges and position bounds are correct ([#154](https://github.com/Nurysso/eulix/pull/154)).
- PHP method calls resolve in references and the call hierarchy ([#89](https://github.com/Nurysso/eulix/pull/89)).
- Stale scans stop, and open buffers and the file watcher stay within bounds ([#162](https://github.com/Nurysso/eulix/pull/162)).
- The server builds against libeulix 0.9.0 ([#150](https://github.com/Nurysso/eulix/pull/150)), and its tests run with the rest of the Rust tests in `make test-rust` ([#156](https://github.com/Nurysso/eulix/pull/156)).

He also wrote the PHP support in libeulix that this server reads its symbols from. Gabriel, thank you: this server got a lot sturdier in your hands.

— Nurysso
