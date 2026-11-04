# History Viewer

This page describes the screen that lists past questions and shows one in full, and the problems I found in how it behaves.

> **Crate / path:** `eulix-cli/internal/tui/history-tui.go`  
> **Language:** Go  
> **Status:** beta, with known bugs  
> **Last verified against:** `ad744e9`

## Overview

The history viewer is a second Bubble Tea model, `CacheViewerModel`. It is reached from the chat screen with `/history`, and from the command line with `eulix history` (which starts it on its own). It has a list of entries, newest first, and a detail view for one entry.

## Why It Exists

You often want to re-read an earlier answer, and a transcript that scrolls away is a poor record. The history is stored ([Cache](../Cache.md)); this screen is how you browse it.

## How It Works

### The list

Built with the Bubbles list component, which supplies selection, paging, a status bar and filtering (press `/` to filter by question text). Each row has:

- a title: a small marker, the entry's number in square brackets, and the question cut to 60 characters. The marker is `◈` when the entry has saved reasoning and `·` when it does not;
- a description: the date and time, and the first 72 characters of the answer.

The colours are copied from the chat screen so the two look alike.

### The detail view

Pressing `Enter` opens a scrollable view of the entry: its ID, its time, then **Query**, **Reasoning** (or "(none)") and **Answer**, each wrapped to the width. Unlike in the chat screen, the answer is wrapped as plain text and is **not** run through the [Markdown renderer](Markdown-Rendering.md), so tables and code blocks look raw.

### Keys

| Where | Key | Does |
| --- | --- | --- |
| List | `Enter` | Open the highlighted entry. |
| List | `d`, `Delete` | Delete an entry (see the bug below). |
| List | `q`, `Ctrl+C` | Quit the program. |
| Detail | `Esc`, `b`, `q` | Back to the list. |
| Detail | `d`, `Delete` | Delete the open entry. |
| Detail | `↑ ↓` | Scroll. |

### Switching from the chat

`/history` makes the chat send itself a message. The handler checks that history is enabled and that entries exist (otherwise it adds an error or a note to the transcript), builds a `CacheViewerModel` from the entries, sizes it to the current window and **returns it as the new model**. The chat model is dropped.

### Main Components

| Item | Responsibility |
| --- | --- |
| `HistoryView` | Builds the list and viewport from entries. |
| `cacheItem` | One list row: title, description, filter text. |
| `themedDelegate` | Styles the list. |
| `renderListView`, `renderDetailView`, `renderDetail` | Drawing. |
| `deleteCurrentEntry` | Deletion command. |

### Data In, Data Out

In: entries from the history manager. Out: terminal output, and deletions from the database.

## Key Algorithms

None.

## Configuration

None.

## Usage Examples

```bash
eulix history          # opens this screen directly
```

Inside it: arrow keys to move, `/` then text to filter, `Enter` to read, `Esc` to go back.

## Limitations

These come from reading the code. I have not run the screen, so treat them as likely bugs to confirm.

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| **`d` in the list may delete the wrong entry.** | The deletion uses `m.selected`, which is set only when you press `Enter` to open an entry. In the list it still holds its initial value (the first entry) or the last one you opened, not the one highlighted. | Open the entry with `Enter` first, then press `d` in the detail view. |
| **After a deletion the list does not refresh.** | The delete command works on a copy of the model and returns nothing, so the visible list and state are not updated. The database entry is removed, though. | Quit and reopen. |
| **There is no way back to the chat.** | `/history` replaces the chat model; `q` in the list quits the whole program. | Re-run `eulix chat`. |
| Answers are not rendered as Markdown. | Plain wrapping only. | Use `eulix history show <id>` for plain text. |
| Long titles are cut by bytes, not characters. | Slicing at 57 and 69 bytes can split a multi-byte character. | None. |
| Possible duplicate entries. | See [Chat Screen](Chat-Screen.md). | Delete duplicates by ID. |

## Related Docs

- [Cache](../Cache.md): the store.
- [Chat Screen](Chat-Screen.md): `/history`.
- [Cli](../Cli.md): `eulix history`.
- [CLI-1 and CLI-2](../../Known-Issues/CLI-MCP-And-TUI.md#cli-1-each-chat-answer-is-probably-saved-to-history-twice): the issues described here, tracked.
