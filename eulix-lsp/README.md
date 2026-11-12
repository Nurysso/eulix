# eulix-lsp

> **Status: beta (0.2.0).** Fast and stable, but it matches names, not types.
> Read [Where to use it](#where-to-use-it) before relying on rename or
> references.

A language server that plumbs the [libeulix](https://crates.io/crates/libeulix) 0.9.0
knowledge base over LSP. Supports C, C++, Go, Java, JavaScript, PHP, Python, Rust,
and TypeScript: symbols, go-to-definition, references, hover, completion, call
hierarchy, semantic tokens, and workspace diagnostics.

```zsh
cargo build --release
./target/release/eulix-lsp
```

## Features

|                    |                                                             |
| ------------------ | ----------------------------------------------------------- |
| Document outline   | Nested, declaration-ordered                                 |
| Hover              | Signature, docstring, complexity                            |
| Go to definition   | Same-file and cross-file, single result                     |
| Find references    | Workspace-wide, optional declaration                        |
| Document highlight | Same-file occurrences of the symbol under the cursor        |
| Completion         | Prefix-filtered, proximity-ranked                           |
| Rename             | Refuses when the name is ambiguous in the workspace         |
| Semantic tokens    | Symbol-level, delta-encoded (full document only)            |
| Call hierarchy     | Incoming and outgoing                                       |
| Folding ranges     | Functions and classes                                       |
| Workspace symbols  | Non-empty query required                                    |
| Diagnostics        | Syntax errors, duplicate definitions, TODOs, security notes |
| File watching      | Detects changes on disk                                     |

## Where to use it

eulix-lsp matches **names, not types**. It indexes every definition and call
by name and answers from that index, without type checking or import
resolution. That makes it fast and language-agnostic, and less precise than a
compiler-backed server.

**Good fit:**

- Polyglot repositories where you want one server for outline, search and
  navigation across all languages.
- Large trees where a full language server is too slow or too heavy. The Linux
  kernel (50,000 files) indexes in about 49 s and 2.5 GB, and most requests
  then answer in under a millisecond.
- Languages or projects with no working language server, or code that doesn't
  build.
- Reading and exploring unfamiliar code: outline, workspace symbols,
  go-to-definition for plain function calls, and call hierarchy.

**Use your language's own server instead** (rust-analyzer, pyright, gopls,
clangd) when you need:

- Type-aware navigation on method calls (`a.get()` vs `b.get()`).
- Safe refactoring. Treat rename here as a suggestion and check every edit
  (see below).
- Precise references for common names such as `new`, `get` or `len`.

The two can run side by side; most editors let you pick which server answers
which request.

## What it can and can't do

Things you might expect from a language server that are **not** implemented:

- **No code actions, formatting, or signature help.** The parse result doesn't
  carry the information these need, and inventing it would be guesswork.
- **No type hover.** Hover shows what the parser extracted (signature, doc,
  modifiers, complexity). It doesn't resolve types through assignments or
  calls.
- **Semantic tokens are symbol-level, not token-level.** Every name the parser
  extracted gets a token; the rest of the file keeps the editor's own syntax
  highlighting. There is no lexer pass behind this.
- **Rename only edits open buffers.** The reverse index gives us file and line
  for call sites, but to build a precise edit we need the file's text. Editing
  a whole line in a file we've never read would be worse than skipping it, so
  closed files are left alone. The rename still completes for open files.
- **Document highlights are same-file only.** Cross-file highlights would
  require opening every file that mentions the symbol, which isn't what the
  request is for.
- **Go-to-definition returns one target, not a list.** When a name has several
  definitions, the server picks the closest one deterministically (same file,
  then same directory, then shortest path, then lexicographic). Use references
  if you want to see them all.
- **Completion is prefix-match only.** No fuzzy matching, no ranking beyond
  proximity and sort order.
- **Workspace symbol search requires a non-empty query.** An empty query
  returns an empty list rather than dumping the entire index.

## Measured behaviour

From `bench/bench_lsp.py` on this branch (details in
[LSP Benchmark](../docs/Architecture/LSP-Benchmark.md); open issues in
[LSP Known Issues](../docs/Known-Issues/LSP.md)):

| Works                                                                                          | Doesn't, or only partly                                                                                                                              |
| ---------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Indexing is linear: 4,000 files in 0.43 s; the Linux kernel's 50,000 in about 49 s.            | Methods resolve by name, not receiver type, so references and call hierarchy over-count common names (36,000 results for one `helper`).              |
| Outline, folding, semantic tokens, highlight and hover on declarations answered every request. | Hover on a call into another file shows nothing (60 of 60 empty).                                                                                    |
| Go-to-definition on calls into workspace functions resolved 60 of 60 in synthetic code.        | On the eulix repository, 47 of 60 calls had no target, mostly calls into standard libraries, which aren't indexed.                                   |
| Typing: diagnostics about 5 ms after the 200 ms debounce; 100 fast edits give one update.      | **Rename** of a name with no workspace definition (e.g. `len`) edits every call to it in every open file and language. Check the preview.            |
| No crashes, protocol errors or leaks in any run, including the kernel.                         | C and C++: macros, structs and enums aren't indexed; 11 of 20 kernel headers had an empty outline.                                                   |
|                                                                                                | With VS Code's encoded URIs (Windows drive colons, `+` in paths), rename skips call sites and cross-file definition selects the whole line.          |
|                                                                                                | Calls at module level (outside any function) aren't recorded. Only the first workspace folder is indexed, and at most `maxScanFiles` (50,000) files. |

## Limitations

- JSX and TSX are not supported. `libeulix` detects them but has no parser arm
  for either, so the server excludes them rather than offering features it
  can't back up.
- Syntax diagnostics are line-granular. `SyntaxHealth` reports a count and a
  single `first_error_line`, so there are no per-error underlines to draw from.
- `reportUnresolvedCalls` is off by default. At this analysis level, a call
  into the standard library, a macro, a dynamic dispatch, and a typo all look
  the same, so turning it on without a curated allowlist produces noise.
- Definitions are recorded with line numbers but no columns, so the server
  finds the name in the text when it needs a precise range. This is fine on
  well-formed code and degrades to a whole-line range when the name isn't
  findable.

### [LICENSE](../LICENSE)
