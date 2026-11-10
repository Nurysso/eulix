# fixers

The `fixers` package contains two repair tools for the binary files in `.eulix/`. They are written but not connected to any command today.

> **Crate / path:** `eulix-cli/internal/fixers/` (`GLaDOS.go`, `Aspirine.go`)  
> **Language:** Go  
> **Status:** dormant; no code imports this package  
> **Last verified against:** `ad744e9`

## Overview

Two files in `.eulix/`, `embeddings.bin` and `vectors.bin`, are binary and are written by the embedding tool. If one is cut short or damaged, retrieval fails in confusing ways. The package offers a checker, `GLaDOS`, and a repairer, `Aspirine`, for that situation. The commands that used to call them (`eulix glados` and `eulix aspirine`) are now stubs that print "deprecated", and they are not registered with the command parser.

## Why It Exists

When an embedding run is interrupted, the file header may claim more vectors than the file contains. These tools were written to detect that and, where possible, repair the header, instead of making the user re-embed a large codebase.

## How It Works

Both tools understand the same binary layout. A file starts with a header: 4 bytes of magic (`EULX`), 4 bytes of format version, a length-prefixed model name, a 4-byte vector count and a 4-byte dimension. The payload is a series of entries, each a length-prefixed identifier followed by the vector.

**GLaDOS** (the checker) prints a diagnostic for each file: decodes the header, walks the payload to count the entries it really contains, compares that with the count in the header, and loads `kb.json` to cross-check against the number of functions in the knowledge base.

**Aspirine** (the repairer) validates both files, spot-checks entries one by one, compares the embedding count with the number of functions, and, if the header is wrong, backs up the file and rewrites the header with the true count. It has options to skip the backup and to force a rebuild.

### Main Components

| Item | Responsibility |
| --- | --- |
| `GLaDOS(eulixDir)` | Validates `embeddings.bin`, `vectors.bin` and `kb.json`. |
| `Aspirine(eulixDir, opts)` | Validates and heals the headers. |
| `binHeader` | The decoded header. |

### Data In, Data Out

In: the `.eulix/` folder. Out: printed diagnostics and, for Aspirine, a rewritten file plus a backup.

## Key Algorithms

None beyond reading the layout.

## Configuration

`AspirineOptions` has `NoBackup`, `Force` and `FixTarget`. They were meant to be command flags; the flag definitions are commented out in the CLI.

## Usage Examples

None are supported today.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Not reachable from the command line. | The commands are stubs and unregistered. | Re-run `eulix analyze` after deleting the damaged file. |
| The binary layout described here may be out of date. | The code reads a float32 payload, but `analyze` asks the embedder for quantized output and the loader reads an int8 format with a scale. | Check the loader in [Index Loading](query/retrieval/Index-Loading.md) before reviving these tools. |

## Related Docs

- [Overview](Overview.md): the commands that are and are not registered.
- [Index Loading](query/retrieval/Index-Loading.md): the current reader of these files.
