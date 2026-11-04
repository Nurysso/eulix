# Server

This page documents the `server` command: a long-running process that keeps a model loaded and embeds questions on request.

> **Crate / path:** `eulix-embed/server/server.py`  
> **Language:** Python  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

Loading an embedding model takes seconds; embedding one sentence takes milliseconds. `eulix chat` and `eulix mcp` therefore start `server` once and keep it. They talk to it through its standard input and output, one JSON message per line. The Go client for this is described in [Embeddings](../eulix-cli/Embeddings.md).

## Why It Exists

Starting a Python process and loading a model for every question would make each question feel slow. A daemon pays that cost once.

## How It Works

Start it with `python main.py server -m <model> -e <engine> [-d <device>]`. It loads the model, then writes a first line telling the caller it is ready, and then waits for requests. **Standard output carries only protocol messages; all logs go to standard error.**

| You send | You receive |
| --- | --- |
| *(start-up)* | `{"ready": true, "model": "...", "dim": N}`, or `{"ready": false, "error": "model load failed: ..."}` and exit code 1 |
| `{"query": "text"}` | `{"embedding": [..], "dimension": N, "model": "..."}` |
| `{"queries": ["a", "b"]}` | `{"embeddings": [[..], [..]], "dimension": N, "model": "..."}` |
| `{"ping": true}` | `{"pong": true, "model": "...", "dim": N, "uptime_s": F, "requests": N}` |
| `{"shutdown": true}` | `{"shutdown": "ok"}`, then the server exits |
| anything invalid | `{"error": "message"}` |

Safety limits protect the server from a broken caller: a request line over 10 MB is refused ("request too large"), a batch over 512 texts is refused, and bad JSON, a non-object, an empty query, or a non-string gives an error message but does not stop the server. A long-running server must survive one bad request, so unexpected exceptions are also turned into error replies. `SIGTERM` and `SIGINT` shut it down cleanly; if the parent disappears (a broken pipe), it exits.

The messages that `eulix` currently sends are single `query` requests; the batch form and `ping` exist for other callers.

### Main Components

| Item | Responsibility |
| --- | --- |
| `cmd_serve` | The load, the request loop, signal handling. |
| `_write` | Writes one protocol line, exiting quietly on a broken pipe. |
| `_configure_logging` | Sends logs to standard error and quiets noisy libraries. |

### Data In, Data Out

In: JSON lines on standard input. Out: JSON lines on standard output.

## Key Algorithms

None. The protocol is deliberately plain so that any language can speak it.

## Configuration

| Flag | Default | Meaning |
| --- | --- | --- |
| `-m, --model` | `BAAI/bge-base-en-v1.5` | Model to load. |
| `-e, --engine` | `onnx` | Backend. `eulix` passes the configured one. |
| `-d, --device` | auto | Not passed by `eulix` today ([EMB-1](../Known-Issues/Embeddings.md#emb-1-onnx-on-rocm-produces-wrong-embeddings)). |
| `-b, --batch-size` | auto | For batch requests. |
| log level, max batch size | `info`, 512 | Read from the arguments if present; there are no command-line flags for them. |

## Usage Examples

By hand:

```bash
printf '{"ping": true}\n{"query": "hello"}\n{"shutdown": true}\n' | python main.py server -m BAAI/bge-small-en-v1.5 2>/dev/null
```

Expected result: the ready line, a pong, an embedding, and `{"shutdown": "ok"}`.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| One request at a time. | A single loop reads stdin. | The caller serializes (the Go client does). |
| No timeout inside the server. | The caller times out and kills it. | See [RET-7](../Known-Issues/Retrieval-And-Query.md#ret-7-embedder-is-never-restarted-after-a-timeout). |
| No query instruction prefix. | See [Embedders](Embedders.md). | None. |

## Related Docs

- [Embeddings (eulix-cli)](../eulix-cli/Embeddings.md): the client.
- [Embedders](Embedders.md): what runs behind it.
