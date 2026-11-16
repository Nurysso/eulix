# embeddings

The `embeddings` package starts the Python embedding tool and talks to it, so that a question can be turned into a vector while you wait.

> **Crate / path:** `eulix-cli/internal/embeddings/` (`embedder.go`, `venv.go`)  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

Embedding the whole codebase happens once, during `eulix analyze`, and is done by the separate `eulix_embed` Python tool. But to search with those vectors, every *question* also has to be embedded, using the same model, and quickly. This package starts `eulix_embed` as a long-running helper process, keeps the model loaded, and sends it one question at a time.

It also contains the small helpers that find the Python interpreter inside the `~/.Eulix/.venv` virtual environment, which `analyze` uses to launch the embedder.

## Why It Exists

Loading an embedding model takes seconds (3 to 5, according to the code comment), but encoding one sentence takes a few milliseconds (5 to 50). Starting a fresh Python process per question would make every question feel slow. Keeping one process alive pays the load cost once.

## How It Works

`VectorWeaver(model, engine, debug)` starts the embedding tool's `server` mode inside the virtual environment, with pipes for its input, output and error streams. It then waits for a "ready" message, with a time limit, and returns an `Embedder`.

The conversation is one JSON message per line:

```text
send:     {"query": "text"}
receive:  {"embedding": [f32, ...], "dimension": N, "model": "..."}
error:    {"error": "message"}
```

A mutex makes requests one-at-a-time, since the pipe carries one conversation. Each request has a 30-second timeout; if it expires, the process is killed and the embedder is marked dead, so later calls fail fast instead of hanging. The reader buffer is 4 MiB, to hold large replies. The process's error output is read continuously into a rolling buffer of the last 50 lines, which is included in the error message if start-up fails or the process dies. With `embeddings.debug` on, those lines are also streamed live.

### Main Components

| Item | Responsibility |
| --- | --- |
| `VectorWeaver` | Starts the helper and returns an `Embedder`. |
| `Embedder.EmbedQueryBinary` | Embeds one question, returning a vector of 32-bit floats. |
| `Embedder.Close`, `IsAlive` | Shut the helper down; check that it is running. |
| `GetVenvPython` | Validates the virtual environment and returns the interpreter and an environment with the venv's `bin` first on `PATH`. |
| `FindEulixEmbed` | Locates `main.py` and a usable interpreter. |

### Data In, Data Out

In: a question string. Out: a vector whose size is the model's dimension. The retrieval code uses it for [vector search](query/retrieval/Search-Pipeline.md).

## Key Algorithms

### Using the same space for questions and code

**The problem.** A question vector is only meaningful next to code vectors made by the same model *and* the same inference engine. A different engine (`onnx` versus `torch`) can produce slightly different vectors.

**The approach.** `VectorWeaver` takes the engine as an argument, and its comment states that it must be the one that built the index. The retrieval code passes the engine from `eulix.toml`, the same value `analyze` used.

**Why this way.** Failing to match them gives plausible-looking but wrong results, which is hard to notice. Passing the setting explicitly makes the dependency visible.

### Windows `PATH` handling

When adding the venv to `PATH`, the code looks for the variable case-insensitively, because Windows usually spells it `Path`. Adding a second `PATH` instead would leave the child process with no system path at all.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `embeddings.model` | `eulix.toml` | `BAAI/bge-base-en-v1.5` | The model. Re-run `analyze` after changing it. |
| `embeddings.engine` | `eulix.toml` | `onnx` | `onnx` or `torch`. |
| `embeddings.debug` | `eulix.toml` | false | Stream the helper's log output. |

## Usage Examples

The package has no command of its own. `eulix embed ...` passes its arguments straight to the embedding tool.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Questions are embedded one at a time. | One pipe, one mutex. | None needed for interactive use. |
| A slow first question. | The helper must load the model. | Expected. |
| Only the default retrieval engine starts it. | The other engine uses no vectors. See [Engines](query/retrieval/Engines.md). | Set `engine = "ehnas"` to avoid the helper entirely. |
| A comment mentions a `config.Project.Embedis == "script"` setting. | That setting no longer exists; script mode is the only mode. | Ignore the comment. |

## Related Docs

- [Engines](query/retrieval/Engines.md): which engine needs the embedder.
- [Assets](Assets.md): where the embedding scripts come from.
- [Cli](Cli.md): how `analyze` launches the embedder.
- [Embedding issues](../Known-Issues/Embeddings.md): ROCm and related problems.
