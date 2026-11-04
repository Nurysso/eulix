# tui

The `tui` package is the full-screen terminal interface of `eulix chat`: a chat screen and a history browser, built on the Bubble Tea framework.

> **Crate / path:** `eulix-cli/internal/tui/` (`app.go`, `history-tui.go`)  
> **Language:** Go, with Bubble Tea, Bubbles and Lip Gloss  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

When you run `eulix chat`, the program takes over the terminal (the "alternate screen", so your shell history is untouched when you leave) and shows a conversation. You type a question at the bottom, wait for the spinner, and read the answer above. A second screen lists your past questions. About 1,600 lines of Go make this work, and most of them are not about chatting: they are about drawing text well in a terminal of unknown size.

This folder has three more pages:

| Page | Covers |
| --- | --- |
| [Chat Screen](Chat-Screen.md) | The model, the update loop, layout, commands, the question flow, saving to history. |
| [Markdown Rendering](Markdown-Rendering.md) | How the model's Markdown becomes styled terminal text, including tables and code blocks. |
| [History Viewer](History-Viewer.md) | The list and detail screens, and their known problems. |

## Why It Exists

A prompt that prints text and exits cannot scroll, cannot show a table, and cannot collapse a long chain of reasoning. Model replies are Markdown with headings, lists, code and tables. Shown raw, they are hard to read. The interface does the formatting so that the router and the model client can stay simple.

## How It Works

### The Bubble Tea model

Bubble Tea is built on the *Elm architecture*: a program is a **model** (all state in one value), an **update** function that takes a message (a key press, a window resize, the result of background work) and returns a new model plus optional follow-up work, and a **view** function that turns the model into the string to draw. Nothing else is allowed to change the screen. Every part of this package follows that pattern:

```text
key press / resize / reply ──► Update(msg) ──► new Model ──► View() ──► terminal
                                   │
                                   └─► Cmd (background work) ──► reply message
```

Long work, such as calling the router, runs as a *command* on another goroutine. When it finishes, it sends a message back, which is how the screen stays responsive while the model thinks.

### Where it is started

`eulix chat` ([Cli](../Cli.md)) first checks that the knowledge base exists and is fresh enough, opens the history database, builds the language-model client and the query router, and only then starts the program with `MainModel(router, cfg, cacheManager)`, the alternate screen and cell-motion mouse reporting. It runs until you quit.

### Two screens, one program

The chat model and the history model are two separate Bubble Tea models. Switching between them is done by returning the other model from `Update`, which replaces the first entirely. The consequences of that choice are described in [History Viewer](History-Viewer.md).

### Main Components

| Item | Responsibility |
| --- | --- |
| `Model` (in `app.go`) | The chat screen's state. |
| `Message` | One transcript entry plus its render cache. |
| `renderMarkdownBody` and helpers | The Markdown renderer. |
| `CacheViewerModel` (in `history-tui.go`) | The history list and detail screens. |
| Style variables | Lip Gloss styles built once at package start, using the colours in [Utils](../Utils.md). |

### Data In, Data Out

In: key presses, resize events and the router's replies. Out: terminal output, and history entries written through the cache manager.

## Key Algorithms

The interesting parts are layout ([Chat Screen](Chat-Screen.md)) and Markdown ([Markdown Rendering](Markdown-Rendering.md)). Both are about being cheap to redo, because the interface redraws on every key press.

## Configuration

The interface has no settings of its own. It shows whether history is on (from `cache.enable`) and uses the shared colour palette.

## Usage Examples

```bash
eulix chat
```

Expected result: a title bar, a transcript with a welcome message, an input box and a one-line key help at the bottom.

## Performance Notes

Styles are created once, not per frame. Each message caches its rendered text. The input box height is computed from the text with an early exit for huge pastes. There are no unit tests in this package; the Markdown renderer in particular has none.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Replies are not streamed. | The model client returns a whole response. | None yet. |
| No test coverage. | No `_test.go` files exist in `tui`. | Test by hand. |
| `StateTyping` is defined but never used. | Leftover from an earlier design. | None; it does no harm. |
| The CLI README mentions model selection and streaming in the interface. | I found neither in the code. | Trust this page. |
| Opening history takes you out of the chat for good. | See [History Viewer](History-Viewer.md). | Re-run `eulix chat`. |

## Related Docs

- [Chat Screen](Chat-Screen.md), [Markdown Rendering](Markdown-Rendering.md), [History Viewer](History-Viewer.md).
- [Cli](../Cli.md): the `chat` command and its checks.
- [query Overview](../query/Overview.md): the router the chat calls.
- [Cache](../Cache.md): the history store.
