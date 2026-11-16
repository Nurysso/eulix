# Markdown Rendering

This page explains how the chat screen turns the Markdown a language model writes into styled, correctly wrapped terminal text.

> **Crate / path:** `eulix-cli/internal/tui/app.go` (`renderMarkdownBody` and the functions below it)  
> **Language:** Go  
> **Status:** beta; no unit tests  
> **Last verified against:** `ad744e9`

## Overview

Language models answer in Markdown: headings, bullet lists, code fences, tables, bold text. A terminal can show colour, bold, italic, underline and strike-through, but it knows nothing about Markdown. The renderer is a small hand-written converter that reads the reply line by line and emits text with ANSI style codes, wrapped to the terminal width.

It is not a full Markdown implementation. It handles what models commonly produce, and treats everything else as ordinary text.

## Why It Exists

Existing Markdown libraries for terminals are heavy and make their own layout choices. Eulix wants specific things: a code-block look that matches its palette, tables that never overflow the terminal, and a cheap renderer that can be re-run on every resize.

## How It Works

### The main pass

`renderMarkdownBody(text, width)` walks the lines in order, keeping a little state: whether it is inside a code block, which fence marker opened it, the fence's indentation, whether the previous line was a list item or a paragraph. For each line it applies the first rule that fits:

| Order | Rule | What is drawn |
| --- | --- | --- |
| 1 | A fence line: ```` ``` ```` or `~~~` | Opens or closes a code block. The language name, if given, is shown as `[GO]`. |
| 2 | Inside a code block | Each line on the code background. Tabs become four spaces, the fence's own indentation is removed, and long lines wrap. Empty lines stay in the block so the background does not break. |
| 3 | Blank line | A blank line; ends a list. |
| 4 | A table: a row followed by a separator row | The table block (below). |
| 5 | `#` to `######` heading | Styled heading. Levels 1, 2 and 3+ have three styles. |
| 6 | Three or more `-`, `*` or `_` | A horizontal rule the width of the screen. |
| 7 | `> ` block quote | A bar and italic text. |
| 8 | `-`, `*`, `+` or `1.` list item, at any indent | A bullet or number, with a hanging indent. |
| 9 | Indented line directly after a list item | A continuation of that item. |
| 10 | Anything else | A paragraph line with inline formatting. |

Code is matched before the blank-line check, and a fence only closes with the same marker that opened it, so a `~~~` inside a ```` ``` ```` block is just text. Fences are detected on the trimmed line, so fences inside list items work.

### Lists and task lists

`parseListItem` accepts bullets and numbers at any indentation (a tab counts as four spaces). `formatListItem` indents by nesting depth and wraps with a **hanging indent**, so wrapped lines line up under the item's text, not under the bullet. A bullet item that starts with `[ ]` or `[x]` becomes a checkbox, `☐` or `☑`, and checked items are struck through.

### Inline formatting

`processInlineMarkdown` handles `` `code` ``, `[text]` followed by `(url)`, `**bold**`, `__bold__` and `~~strike~~`. A quick check skips all of it for plain prose. Otherwise the order matters, and the trick is **placeholders**: each styled span is rendered, stored in a list, and replaced in the text by a marker made of a NUL byte, a number and another NUL. Later patterns therefore cannot match inside text that is already styled, or inside its invisible ANSI codes. At the end the markers are swapped back, up to four rounds, because bold text may itself contain a code span. Links are shown as underlined text followed by the address in parentheses, since a terminal cannot reliably click them.

### Wrapping

All wrapping uses an ANSI-aware wrapper. It measures visible characters only, ignoring style codes, keeps styled spans intact, preserves leading indentation, and breaks words that are longer than the width, such as paths and URLs.

### Tables

A row that contains `|`, followed by a separator row (`|---|:---:|---:|`), starts a table, and all following rows are part of it. `renderTable` produces a bordered table:

1. **Alignment** comes from the separator: `:---` left, `---:` right, `:---:` centre.
2. **Column widths** start from the widest cell in each column, measured on text with the Markdown markers stripped, then are clamped between 3 and 40.
3. **Fitting.** If the table is wider than the terminal, the widest column is made one character narrower, repeatedly, until it fits or every column is at the minimum.
4. **Cells wrap** within their column, and a row is as tall as its tallest cell.
5. Rows with too many cells are cut, and rows with too few are padded. A literal `|` can be escaped as `\|`.
6. Borders are drawn with box characters, and the header has its own background.

### Why styles are applied line by line

Lip Gloss pads multi-line text to a common width, which would paint ragged blocks. `styleLines` styles each line separately. Headings are styled after their Markdown markers are stripped, because style codes inside already-styled text can cancel the outer style.

### Main Components

| Function | Responsibility |
| --- | --- |
| `renderMarkdownBody` | The line-by-line pass. |
| `fenceOf` | Detects fence lines. |
| `parseListItem`, `formatListItem` | Lists. |
| `isTableRow`, `isTableSeparator`, `splitTableRow`, `renderTable` | Tables. |
| `processInlineMarkdown`, `stripInlineMarkers` | Inline spans and width measurement. |
| `wrapText`, `styleLines`, `clampWidth` | Wrapping and styling helpers. |

### Data In, Data Out

In: the answer text and a width. Out: a string with ANSI styles. The reasoning block is not rendered as Markdown; it is only wrapped.

## Key Algorithms

### Placeholders for nested spans

**The problem.** Regular expressions for `**bold**` and `` `code` `` would otherwise see the style codes that earlier replacements inserted, and could match inside them.

**The approach.** Replace each finished span with a short unique marker, run the next pattern, and restore everything at the end.

**Why this way.** It is simple, keeps each pattern independent, and cannot corrupt the escape codes. Any NUL byte already in the text is removed first so it cannot be confused with a marker.

## Configuration

None. Colours come from [Utils](../Utils.md). The width is the viewport width minus padding, never below 20.

## Usage Examples

A reply containing:

```text
| Stage | Cost |
|:--|--:|
| anchors | O(a×c) |
```

renders as a bordered table with the first column left-aligned and the second right-aligned.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| No nested emphasis, images, footnotes, HTML or numbered sub-lists with parentheses. | Not implemented. | Such text is shown plainly. |
| `*italic*` and `_italic_` are not styled. | Only bold, strike, code and links are handled. | None. |
| Underscores in identifiers can be mistaken for bold markers in rare cases. | The `__x__` rule is word-boundary based but heuristic. | Wrap identifiers in backticks. |
| Block quotes lose inline formatting. | Their text is stripped of markers before styling. | None. |
| Very narrow terminals force a minimum width of 8 for most elements. | `clampWidth`. | Widen the window. |
| No tests. | None written. | Test by hand with tables and nested lists. |

## Related Docs

- [Chat Screen](Chat-Screen.md): the caller and the render cache.
- [Overview](Overview.md): the package.
- [Utils](../Utils.md): colours.
