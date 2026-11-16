# strip

The `strip` package removes comments and docstrings from source code before it is shown to the model, without disturbing line numbers.

> **Crate / path:** `eulix-cli/internal/query/strip/`  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

Comments cost tokens, and the model's context window is limited. When Eulix shows real source code, it first strips comments and documentation so that more code fits. The catch is that a comment marker inside a *string* is not a comment (`"http://example.com"` contains `//`), so stripping needs a small scanner for each language, not a simple search.

## Why It Exists

Source hydration ([Selection And Hydration](retrieval/Selection-And-Hydration.md)) is limited by a token budget. Docstrings are already carried separately in the knowledge base, so showing them again inside the code wastes space. Removing them gives the budget back.

## How It Works

`StripCommentsAndDocs(lines, lang)` joins the lines, picks a scanner by language, and splits the result back into lines.

| Language | Scanner | Notes |
| --- | --- | --- |
| Go | Go scanner | Understands raw strings, rune literals and escapes. |
| JavaScript, TypeScript | C-style, with backtick strings | Template literals are respected. |
| Rust, C, C++, Java | C-style | No backtick strings. |
| Python | Python scanner | Removes `#` comments and triple-quoted docstrings. |
| PHP | PHP scanner | Removes all three comment styles; understands heredoc and nowdoc, string interpolation, `?>` ending a line comment, and PHP attributes, and leaves inline HTML untouched. |
| Anything else | none | Lines are returned unchanged. |

Each scanner walks the text character by character and tracks whether it is inside a string, a comment or code. Every newline in the input is preserved. After scanning, the function applies a line policy: lines that were blank stay blank, lines with remaining code are kept, and a line that *became* blank only because a comment was removed is dropped.

### Main Components

| Item | Responsibility |
| --- | --- |
| `StripCommentsAndDocs` | Entry point and line policy. |
| `stripGoComments`, `stripCStyleComments`, `stripPythonComments`, `stripPHPComments` | Language scanners. |
| `HasPHPOpenTag` | Tells PHP from C for `.inc` files. |

### Data In, Data Out

In: a slice of lines and a language name. Out: a slice of lines, usually shorter.

## Key Algorithms

### Keeping lines aligned

**The problem.** The model is shown `file:startLine-endLine` references. If stripping changed the number of lines inside a chunk without the system knowing, references would be off.

**The approach.** The scanners keep every newline, and a defensive check compares line counts: if they ever differ, the function returns the original lines unchanged. The caller then works with the lines that remain.

**Why this way.** A wrong line number is worse than a few extra tokens, so the code fails safe.

### Fail safe on odd input

Tests cover unterminated block comments, a trailing slash at the end of the file, a backslash at the end of a string, and a fuzz test for PHP. In each case the scanner terminates and returns something sensible.

## Configuration

None.

## Usage Examples

```go
clean := strip.StripCommentsAndDocs(strings.Split(src, "\n"), "python")
```

Expected result: the same code, with `#` comments and docstrings removed and comment-only lines dropped.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Languages other than the table are not stripped. | No scanner. | None. |
| Dropped comment-only lines change the line count of the stripped text. | By design, to save tokens. | The retrieval code keeps its own line ranges for the original file. |
| Heavy macro use in C may confuse comment detection. | No preprocessing. | None. |

## Related Docs

- [Selection And Hydration](retrieval/Selection-And-Hydration.md): where the stripped code is used.
- [libeulix Overview](../../libeulix/Overview.md): the parser side, which extracts docstrings.
