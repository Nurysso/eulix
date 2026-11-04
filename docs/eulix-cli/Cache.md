# cache

The `cache` package keeps a history of the questions you asked and the answers you got.

> **Crate / path:** `eulix-cli/internal/cache/manager.go`  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

Despite its name, this package is no longer a cache. It is a plain history log. Every question and the reasoning and answer the model produced are appended to a small embedded database, so you can list and re-read them later from `eulix history` or the `/history` screen in the chat. There is no time limit, no checksum-based expiry, and no shortcut that returns a stored answer instead of asking the model again. The file's own comment says that layer was removed on purpose.

## Why It Exists

A model's answer is expensive and not repeatable, and people often want to find "that explanation from last week". A history is the simple answer. Skipping real caching avoids a harder problem: deciding when a stored answer is stale after the code has changed.

## How It Works

The database is a single file, `.eulix/history.db` by default, using the embedded key-value store bbolt. One bucket, `entries`, holds one JSON record per turn. Each record has an auto-incrementing numeric ID, the question, the reasoning, the answer and a timestamp. IDs are stored as fixed-width keys so that listing returns them in order, and the list is returned newest first.

`CacheController(cfg)` opens the file with a 2-second lock timeout and creates the bucket. If `cache.enable` is false it returns no manager at all, and every caller treats a missing manager as "history is off". The manager supports `Save`, `Get`, `ListAll`, `Delete` and `Clear`.

### Main Components

| Item | Responsibility |
| --- | --- |
| `Manager` | Wraps the database. |
| `CacheEntry` | One stored turn. |
| `CacheController` | Opens the database, or returns none when disabled. |

### Data In, Data Out

Entries are written when a question is answered in chat, both by the router's `QueryEngine` and by the chat screen itself. Because `eulix chat` gives the same manager to both, each answer is probably stored twice (see [Chat Screen](tui/Chat-Screen.md)). The reasoning and answer are split out of the model's reply, which uses `<reasoning>` and `<answer>` tags (see [Utils](Utils.md)).

## Key Algorithms

None. It is a straightforward store.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `cache.enable` | `eulix.toml` | true | Turn the history off. |
| `cache.path` | `eulix.toml` | `.eulix/history.db` | Move the database. |

## Usage Examples

```bash
eulix history --no-tui     # list all entries as text
eulix history show 12      # print entry 12 in full
eulix history delete 12
```

Expected result: the saved question, reasoning and answer for the chosen entry.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Only one program can open the database at a time. | bbolt takes an exclusive file lock. | Close other `eulix` sessions. The MCP server avoids this by never opening it. |
| The old README calls this a SQLite database. | It is bbolt. | Ignore the README on this point. |
| Entries are never expired. | By design. | `eulix history clear`. |
| Duplicate entries from chat. | Two writers, one store. | Delete duplicates by ID. |

## Related Docs

- [Cli](Cli.md): the `history` commands.
- [Tui](tui/Overview.md): the history viewer.
- [MCP Overview](mcp/Overview.md): why MCP skips the history.
