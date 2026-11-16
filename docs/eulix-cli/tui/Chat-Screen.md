# Chat Screen

This page explains how the chat screen holds its state, reacts to input, lays itself out, and turns a typed question into a displayed answer.

> **Crate / path:** `eulix-cli/internal/tui/app.go`  
> **Language:** Go  
> **Status:** beta  
> **Last verified against:** `ad744e9`

## Overview

The chat screen is a single `Model`. It holds the conversation, the text input, the scrollable view of the conversation, a spinner, and a few flags. Its `Update` function is the only place state changes, and its `View` function draws the whole screen from that state.

```text
┌──────────────────────────────────────────────┐
│ ◆ EULIX — LOCAL-FIRST CODE INTELLIGENCE      │  title bar
│ state: Idle • messages: 3 • cache on • ...   │  subtitle (status)
│ ╭──────────────────────────────────────────╮ │
│ │ › YOU                                    │ │
│ │ how does retrieval work?                 │ │  transcript
│ │ ◆ EULIX                                  │ │  (scrollable)
│ │ ...                                      │ │
│ ╰──────────────────────────────────────────╯ │
│  ▲ more above                                │  one-row status line
│ ╭──────────────────────────────────────────╮ │
│ │ ❯ ask a question or type /help           │ │  input (grows to 6 rows)
│ ╰──────────────────────────────────────────╯ │
│  Enter send  Alt+Enter newline  Esc quit ... │  key hints
└──────────────────────────────────────────────┘
```

## Why It Exists

Drawing a terminal interface well is mostly bookkeeping: remembering what is on screen, keeping the input box and transcript from fighting for space, and not redoing expensive work on every key press. This page describes how that bookkeeping is organized.

## How It Works

### State

| Field | Meaning |
| --- | --- |
| `messages` | The transcript. Each `Message` has a role (`user`, `assistant`, `system`, `error`, `warning`) and text. |
| `input` | A Bubbles text area. |
| `viewport` | A scrollable window onto the rendered transcript. |
| `spinner` | The "Thinking…" animation. |
| `router`, `config`, `cacheManager` | Connections to the rest of the program. |
| `width`, `height` | The terminal size, from resize events. |
| `processing` | True while a question is being answered. |
| `showReasoning` | Whether reasoning blocks are expanded. |
| `state` | A label for the status line. |

The state label takes four values in practice: Idle, Processing, Displaying and Error. A fifth, Typing, exists in the code but is never set.

For assistant messages, the reply is split into reasoning and answer **once**, when the message is created, and both parts are stored.

### The update loop

`Update` handles messages in this order:

| Message | What happens |
| --- | --- |
| `Ctrl+C` | Quit immediately. |
| `Esc` | If the input has text, clear it. If empty, quit. |
| `↑ ↓ PgUp PgDn` | Scroll the transcript only. They are not passed to the text area, so the cursor does not also move. |
| `Enter` | Ignored while processing or when the input is empty. A line starting with `/` is a command. Anything else is a question (below). |
| `queryResultMsg` | The answer (or error) has arrived. |
| `spinner.TickMsg` | Advance the spinner, only while processing. |
| `tea.WindowSizeMsg` | Resize the viewport and input; re-render the transcript if the width changed. |
| `switchToCacheViewerMsg` | Open the history browser. |
| anything else | Passed to the text area, then the layout is recomputed. |

Only the arrow and page keys scroll the transcript. The scroll library's defaults also bind space, `f`, `b`, `d`, `u`, `j` and `k`, which used to scroll the transcript while you were typing, so the key map was replaced with just those four. Mouse-wheel scrolling is also switched off.

### Asking a question

1. You press Enter. The input is cleared and shrunk back, `processing` is set, the state becomes Processing, and your question is added to the transcript.
2. Two things start: the spinner, and `processQuery`, a background command that calls `router.QueryEngine(question)`.
3. The spinner animates and the status line shows "Thinking…". You can keep typing. Submitting is blocked until the answer arrives.
4. The result comes back as a `queryResultMsg`. On error, an `error` message is added and the state becomes Error. On success, an `assistant` message is added and the state becomes Displaying.
5. If history is on, a second background command saves the question, reasoning and answer.

`QueryEngine` is the router entry point that calls the language model for understanding-type questions and answers factual ones directly ([query](../query/Overview.md)).

### Layout

The terminal height is shared between fixed parts and two flexible ones. Eight rows are fixed (title, subtitle, the transcript's border, the status line, the input's border, and the footer). The input takes between 1 and 6 rows. The transcript takes whatever is left, with a minimum of 3.

`desiredInputRows` works out how tall the input should be by measuring how many screen rows each line of text needs at the current width, counting a full last row as pushing the cursor to a new one. It stops counting once it reaches the cap, so a huge paste stays cheap. `layout` applies the result, and only touches the components when something actually changed. If the transcript was scrolled to the bottom before a resize, it stays at the bottom; otherwise its position is clamped.

The status line is always exactly one row, so the rest of the layout never jumps. It shows the spinner while processing, or a hint about content above or below the visible part.

### Commands

| Command | Does |
| --- | --- |
| `/help` | Adds a system message listing commands and keys. |
| `/history` | Opens the history browser. |
| `/think`, `/reasoning` | Toggle reasoning blocks. |
| `/clear` | Replaces the transcript with a single system message. |
| `/stats` | Shows message counts, state, history status and reasoning visibility. |
| `/quit` | Exits. |

Anything else beginning with `/` produces an error message.

### Reasoning blocks

Prompts ask the model to put its work in `<reasoning>` tags and its reply in `<answer>` tags ([Prompts](../query/Prompts.md)). By default the screen shows one dim line, "◔ REASONING — N lines hidden — /think to expand", so the answer is the first thing you read. `/think` expands every reasoning block in place, with a bar down the left side.

### The render cache

Each `Message` stores its last rendered text, plus the width and reasoning setting it was rendered for. `renderMessages` re-renders a message only if one of those has changed. Without this, every new message would re-render the entire transcript, including every table and code block.

### Saving history

On a successful answer, if a history manager exists, a background command calls `Save(question, reasoning, answer)`. This runs off the interface's own goroutine so disk access never delays drawing.

### Main Components

| Item | Responsibility |
| --- | --- |
| `MainModel` | Builds the text area, spinner, scroll view and welcome message. |
| `Update`, `View` | The loop and the drawing. |
| `layout`, `desiredInputRows`, `visualRows` | Sizing. |
| `handleCommand` | Slash commands. |
| `processQuery` | Background question. |
| `renderMessages`, `renderMessage` | Transcript rendering with the cache. |

### Data In, Data Out

In: user input and router replies. Out: the screen, and a history entry per answer.

## Key Algorithms

### Measuring wrapped text

**The problem.** The input box must be as tall as its text, but the text wraps at the box's width, and wide characters, tabs and long words complicate counting.

**The approach.** `visualRows` wraps each logical line with an ANSI-aware wrapper and counts the rows, adding one if the last row is exactly full.

**Why this way.** It uses the same wrapping the text area uses, so the box is neither too short (hiding text) nor too tall (wasting space).

## Configuration

Newlines in the input: Enter sends, so use `Alt+Enter` or `Ctrl+J`. There is no character limit on the input.

## Usage Examples

A multi-line question:

```text
❯ explain this error:⏎(Alt+Enter)
  nil pointer in applyBudget⏎(Enter)
```

Expected result: the input grows to two rows while you type, then clears and the question appears as a `› YOU` message.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| **Each chat answer is probably saved twice.** | `chat` gives the same history manager to both the router and the interface. `QueryEngine` saves the reply when the router has a manager, and the interface saves it again when its result arrives. I read this in the code and have not run it to confirm. | Treat duplicates in `eulix history` as a known bug; do not rely on entry counts. |
| The welcome message is fixed text. | A TODO in the code says to make it dynamic. | None. |
| Nothing is shown until the full reply arrives. | The model client does not stream. | None. |
| `/clear` clears the screen only. | The history is separate. | `eulix history clear`. |
| An error from the model is shown as a plain error message. | No retry logic. | Send the question again. |
| The title bar says "LOCAL-FIRST CODE INTELLIGENCE" even with a cloud provider. | Fixed text. | None. |

## Related Docs

- [Overview](Overview.md): the architecture of the package.
- [Markdown Rendering](Markdown-Rendering.md): how answers are drawn.
- [History Viewer](History-Viewer.md): the `/history` screen.
- [query Overview](../query/Overview.md): `QueryEngine`.
