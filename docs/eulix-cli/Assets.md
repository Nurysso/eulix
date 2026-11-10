# assets

The `assets` package holds the programs that travel inside the `eulix` binary, unpacks them on first use, and checks they have not been tampered with.

> **Crate / path:** `eulix-cli/internal/assets/` (`assets.go` plus one file per operating system)  
> **Language:** Go  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

`eulix` needs two helpers: the Rust parser (`eulix_parser`) and the Python embedding scripts (`eulix_embed`). Rather than asking you to install them separately, the build embeds both in the Go executable using `go:embed`. On first run, this package writes them to `~/.Eulix/`, and on every run it checks that the parser is still the one that was shipped.

## Why It Exists

A single self-contained file is easy to install and to update. But a program that runs another program on your behalf is also a security risk: if someone replaced the extracted parser with something else, `eulix` would run it. The integrity check closes that gap.

## How It Works

**What is embedded.** `eulix-embed.zip` is embedded on every platform. The parser is embedded per platform through build tags: `eulix_parser_linux`, `eulix_parser_darwin` or `eulix_parser_windows.exe`. The expected files live in `internal/assets/bins/`.

**What is extracted.** `ExtractAll` writes the parser to `~/.Eulix/bin/eulix_parser` (marked executable; on Windows it is also hidden) and unpacks the zip into `~/.Eulix/eulix_embed/`. If every entry in the zip shares one top-level folder, that folder is removed so `main.py` ends up directly in `eulix_embed/`. Entries that would land outside the target directory are rejected. A file of the same size already on disk is skipped, which is a quick "already extracted" check, not an integrity check.

**What is verified.** On every start, `VerifyOrExtract` extracts anything missing and then computes the SHA-256 of the extracted parser. It compares that with the hash baked in at build time through a linker setting (`-X eulix/internal/assets.ParserHash=...`). A mismatch is fatal: the program prints a boxed explanation, tells you to delete the file and re-run, and exits with status 1. Changes to the *scripts* are tolerated. Hashes of the embedded artifacts are also computed and cached so a tampered build binary can be detected.

**First-run set-up.** The first time `eulix` runs, `main.go` also calls:

- `CheckUv`, to confirm the `uv` tool is on `PATH`;
- `CheckVenv`, to confirm `~/.Eulix/.venv` exists and reports Python 3.11;
- `GlobalInitialized`, which writes a hidden `~/.Eulix/.initialized` JSON file with the app name, version and install time;
- `InstallEmbedDeps`, which runs `uv pip install` against the embedding tool's requirements file into that environment.

`.initialized` is written *before* the dependencies are installed (`cmd/eulix/main.go`), so if `InstallEmbedDeps` fails, the next run does not retry it.

The requirements file name is chosen at build time with another linker setting (`embed_requirements`, for example `onnx-amd.txt`).

### Main Components

| Item | Responsibility |
| --- | --- |
| `EulixRoot` | `~/.Eulix`. |
| `ExtractAll`, `ParserPath`, `EmbedScriptsDir` | Unpack and locate the assets. |
| `VerifyOrExtract` | The integrity check. |
| `Hashes` | Computes and caches SHA-256 hashes of the embedded files. |
| `CheckUv`, `CheckVenv`, `InstallEmbedDeps` | First-run environment set-up. |
| `PrintReqFileCmd` | Prints the requirements for the `printEmbedDeps` command. |

### Data In, Data Out

In: the embedded files. Out: files under `~/.Eulix/` and a pass-or-exit result.

## Key Algorithms

### Hash comparison against a build-time value

**The problem.** Checking a file against a hash stored *next to it* proves nothing, because an attacker can change both.

**The approach.** The expected parser hash is compiled into the `eulix` executable. The check compares the on-disk parser against that value. If the build did not supply one, the check relies on runtime checks only, as the package comment notes.

**Why this way.** To fool it, an attacker would need to modify `eulix` itself, which is a much higher bar than replacing one extracted file.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `ParserHash` | linker flag at build time | none | Expected SHA-256 of the parser. Set it in release builds. |
| `embed_requirements` | linker flag at build time | none | Which requirements file to install, such as `onnx-amd.txt`. |

## Usage Examples

```bash
eulix verifyBins     # prints nothing if everything is correct
eulix getEmbedDeps   # reinstall the Python dependencies
eulix printEmbedDeps # show what would be installed
```

If the parser is reported as modified:

```bash
rm ~/.Eulix/bin/eulix_parser
eulix analyze        # extracts a fresh copy
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The extracted scripts are not verified. | A script change is allowed by design. | Treat `~/.Eulix/eulix_embed/` as trusted local state. |
| The macOS build file does not embed or hide anything extra. | `setHiddenAttribute` is a no-op there. | None needed. |
| The requirements path in code is spelled `requiremenents`. | A typo in the directory name that is consistent between the code and the zip. | Do not "fix" one side only. |
| First run needs `uv` and network access. | The Python environment is built on demand. | Install `uv`; run `eulix getEmbedDeps` later if it failed. |
| `bins/` holds no macOS parser in the repository snapshot I read. | Only the Linux and Windows files are present. | Build it as described in the `eulix-cli` README. |

## Related Docs

- [Overview](Overview.md): the start-up sequence.
- [Embeddings](Embeddings.md): how the extracted scripts are used.
- [eulix-parser Overview](../eulix-parser/Overview.md): what the embedded parser is.
