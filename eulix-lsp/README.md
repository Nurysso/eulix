# eulix-lsp

A language server that plumbs the [libeulix](https://crates.io/crates/libeulix) 0.8.1
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
