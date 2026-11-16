# eulix-parser

`eulix-parser` is the command-line tool that parses a whole repository with `libeulix` and writes the results as a set of JSON files.

> **Crate / path:** `eulix-parser/` (binary name `eulix_parser`)  
> **Language:** Rust  
> **Status:** stable  
> **Last verified against:** `ad744e9`

## Overview

`eulix-parser` is the batch front-end of Eulix. You point it at a repository, and it walks the source tree, hands every file to `libeulix`, runs the analysis passes, and writes a group of synchronized JSON artifacts. Everything downstream, such as the query engine, the embedding tool and the language server, reads those files instead of parsing the repository again.

The tool is mostly orchestration. All of the parsing and analysis logic lives in [`libeulix`](../libeulix/Overview.md). What `eulix-parser` adds is the parts that depend on the machine and the file system: finding files, running them in parallel, keeping memory under control with [spill files](../Architecture/Spill-To-Disk.md), skipping unchanged files with an [incremental cache](../Architecture/Incremental-Parsing.md), and writing the output.

After reading this page you will know how to run the tool, what each option does, and which page to read for the details of each part.

## Why It Exists

`libeulix` is a library, and a library cannot decide where files are written, how many threads to use, or what to print. Those decisions belong to a program. Keeping them in a separate binary means `libeulix` stays small and embeddable, and it gives every other Eulix tool one dependable producer of the knowledge base.

The tool also has a job of its own: making large repositories practical. A repository with tens of millions of lines produces more data than is comfortable to hold in memory, and parsing it from scratch on every run is slow. The spill and incremental designs exist for that reason.

## How It Works

A run has four phases, and they run one after the other. Each phase is explained in [Parsing Pipeline](Parsing-Pipeline.md).

1. **File discovery and parsing.** The tool walks the repository, respects `.euignore`, and parses all matching files in parallel. Each file's output is saved to disk right away.
2. **Analysis.** The call graph is built with PRISM, then patterns and entry points are detected.
3. **Summary.** A project summary and a list of the most complex functions are produced.
4. **Output.** Eight JSON files are written.

With `--no-analyze`, phases 2 and 3 are skipped and only the main knowledge base file is written.

### Main Components

| Component | Location | Responsibility |
| --- | --- | --- |
| `main.rs` | `src/main.rs` | Command-line options, the four phases, progress output. |
| `Engine` | `src/engine.rs` | The one thread pool used by the whole run. |
| `parse/` | `src/parse/` | File discovery, per-file parsing, slimming and the run accumulator. |
| `spill.rs`, `cache.rs` | `src/` | The spill files and the incremental cache. |
| `analysis_pipeline.rs` | `src/analysis_pipeline.rs` | Runs PRISM and the pattern and entry-point passes. |
| `views.rs`, `output.rs` | `src/` | Borrowed views and streaming writers for the JSON files. |
| `stats.rs`, `report.rs` | `src/` | Counts of clean, partial and failed files, and the optional TSV report. |

### Data In, Data Out

The input is a repository root. The output is a set of files in the directory that contains your `--output` path. See [Output Files](Output-Files.md) for what each file holds.

## Key Algorithms

The tool's own algorithms are about moving data efficiently, not about understanding code. Two of them have their own pages:

- [Spill to Disk](../Architecture/Spill-To-Disk.md): why each file's detailed output is written to disk immediately, and how `kb.json` is assembled from those pieces without being serialized again.
- [Incremental Parsing](../Architecture/Incremental-Parsing.md): how unchanged files are recognized and reused on later runs, and the safeguards that keep the result identical to a full parse.

The call-graph engine that phase 2 uses is [PRISM](../libeulix/PRISM-V2.md), which is part of `libeulix`.

## Configuration

```text
eulix_parser [OPTIONS] --root <ROOT> --prism <PRISM>
```

| Option | Short | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `--root <ROOT>` | `-r` | required | The repository to analyze. |
| `--prism <1\|2>` | `-p` | required | Which call-graph engine to use. `2` is scope-aware and more precise; `1` is simpler and faster. Any other value is rejected. See [PRISM v1](../libeulix/PRISM-V1.md) and [PRISM v2](../libeulix/PRISM-V2.md). |
| `--output <OUTPUT>` | `-o` | `knowledge_base.json` | Path of the main file. Every other artifact is written next to it, named after its file stem. The cache directory is created here too. |
| `--threads <N>` | `-t` | `4` | Number of worker threads. `0` means the number of physical cores. This also bounds phase 2. |
| `--languages <LANGS>` | `-l` | `all` | Comma-separated languages: `c`, `cpp`, `python`, `javascript`, `typescript`, `go`, `rust`, `java`, `php`, or `all`. Unknown names are ignored (with a warning when verbose). |
| `--no-analyze` | `-n` | off | Parse only. Skips the call graph and every analysis file. Useful on very large repositories. |
| `--euignore <PATH>` | `-e` | `<root>/.euignore` | Use a different ignore file. |
| `--parse-report <FILE>` | `-P` | off | Write a TSV listing every partial and failed file, which is handy for comparing two runs. |
| `--verbose` | `-v` | off | Print each phase, timings and peak memory. |
| `--version` | `-V` | | Print the version. |

### `.euignore`

The parser uses its own ignore file, in the same pattern syntax as `.gitignore`, and it does **not** read `.gitignore`. Put `.euignore` in the repository root (or pass `--euignore`):

```text
fixtures/
generated/
vendor/
*.generated.go
```

## Usage Examples

A full run with PRISM v2:

```bash
eulix_parser --root /path/to/project --prism 2 --output .eulix/kb.json --threads 12 --verbose
```

Only some languages:

```bash
eulix_parser --root /path/to/project --languages rust,go --prism 2 --output out/kb.json
```

Parse only, with no analysis:

```bash
eulix_parser --root /path/to/huge-repo --prism 1 --no-analyze --output out/kb.json
```

Expected result: without `--verbose`, one summary line such as `✓ Parsed 363 files (363 clean, 0 partial, 0 failed; … LOC) in 1.20s → out/kb.json`.

Build from source with `cargo build --release`. The binary is `target/release/eulix_parser`, and a C/C++ toolchain is needed because the Tree-sitter grammars compile C code.

## Performance Notes

See [Performance Notes](Performance-Notes.md) for the Linux-kernel benchmark and the lessons recorded from earlier tuning.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| `--prism` has no default. | The choice changes results, so it is required. | Pass `-p 2` unless you have a reason not to. |
| No call graph above 100,000 files. | A memory guard in `analysis_pipeline.rs`. | None in the CLI. |
| Output files are written next to `--output`, not to a separate directory option. | The output directory is derived from the output path. | Choose the `--output` path accordingly. |
| Syntax newer than the bundled grammars gives partial parses. | Tree-sitter recovers around what it does not understand. | Such files are listed as partial; see [Parsing Pipeline](Parsing-Pipeline.md). |
| Macro expansion in C and C++ is limited. | The goal is a good syntax tree for retrieval, not a compiler. | None. |
| `.jsx` and `.tsx` are not parsed. | See the language limitation in [libeulix Overview](../libeulix/Overview.md). | None yet. |

## Related Docs

- [Parsing Pipeline](Parsing-Pipeline.md): the four phases in detail.
- [Output Files](Output-Files.md): what each JSON file contains.
- [Performance Notes](Performance-Notes.md): benchmark and tuning lessons.
- [Spill to Disk](../Architecture/Spill-To-Disk.md) and [Incremental Parsing](../Architecture/Incremental-Parsing.md): the two architecture designs behind large-repository support.
- [libeulix Overview](../libeulix/Overview.md): the engine this tool drives.
- [Parser And PRISM issues](../Known-Issues/Parser-And-PRISM.md): current problems.
