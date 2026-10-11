# eulix-parser

> CLI for [`libeulix`](../libeulix/). Parses a repository and writes
> structured JSON artifacts — symbols, call graphs, indices, metrics, entry
> points, dependencies.

`eulix-parser` is the batch front-end of the [Eulix](../README.md) toolchain.
It walks a source tree, hands each file to `libeulix`, runs the analysis
passes, and writes a set of synchronized JSON artifacts that downstream
tools — the Eulix query engine, the LSP, retrieval pipelines — can consume
without ever reparsing the repository.

> **Parse the repository once. Navigate it many times.**

---

## Install

### From crates.io

```bash
cargo install eulix-parser
```

### From source

```bash
git clone https://github.com/Nurysso/eulix
cd eulix/eulix-parser
cargo build --release
```

The binary is written to `target/release/eulix_parser`.

Prerequisites: Rust stable, a C/C++ toolchain (the tree-sitter grammar
crates compile C at build time).

---

## What it produces

A normal run emits a set of synchronized JSON artifacts:

| File                                | Purpose                                                                                               |
| ----------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `knowledge_base.json`               | Main knowledge base containing file, symbol, AST-derived, and language metadata                       |
| `knowledge_base_call_graph.json`    | Call-graph nodes and edges, including call sites and relationship metadata                            |
| `knowledge_base_index.json`         | Fast lookup indexes such as functions-by-name, caller relationships, tags, types, and file categories |
| `knowledge_base_summary.json`       | Project summary and language / LOC information                                                        |
| `knowledge_base_metrics.json`       | Complexity and code metrics                                                                           |
| `knowledge_base_entry_points.json`  | Discovered application entry points and route / command metadata                                      |
| `knowledge_base_external_deps.json` | External package / dependency information                                                             |
| `knowledge_base_patterns.json`      | Detected structural and architectural patterns                                                        |
| `checksum.json.zst`                 | Check Sum of the parsed files                                                                         |

With`--no-analyze`, only the main knowledge-base.json artifact is written.

> you can change out file name by providing -o flag with baseName.json

---

## CLI

```text
eulix_parser [OPTIONS] --root <ROOT> --prism <PRISM>
```

| Option                | Short | Description                                     | Default               |
| --------------------- | ----- | ----------------------------------------------- | --------------------- |
| `--root <ROOT>`       | `-r`  | Repository root                                 | Required              |
| `--output <OUTPUT>`   | `-o`  | Knowledge-base output path                      | `knowledge_base.json` |
| `--threads <N>`       | `-t`  | Number of parser workers                        | `4`                   |
| `--languages <LANGS>` | `-l`  | Comma-separated languages or `all`              | `all`                 |
| `--prism <1\|2>`      | `-p`  | PRISM relationship engine version               | Required              |
| `--no-analyze`        |       | Parse files without relationship/analysis phase | `false`               |
| `--euignore <PATH>`   |       | Custom `.euignore` path                         | `<root>/.euignore`    |
| `--verbose`           | `-v`  | Detailed phase output                           | `false`               |
| `--version`           | `-V`  | Print version                                   |                       |

---

## Examples

### Full analysis with PRISM v2

```bash
eulix_parser \
  --root /path/to/project \
  --prism 2 \
  --output .eulix/kb.json \
  --threads 12 \
  --verbose
```

### Faster relationship analysis with PRISM v1

```bash
eulix_parser \
  --root /path/to/project \
  --prism 1 \
  --output .eulix/kb.json \
  --threads 12 \
  --verbose
```

### Parse selected languages

```bash
eulix_parser \
  --root /path/to/project \
  --languages rust,go \
  --prism 2 \
  --output out/kb.json
```

### Parse without analysis

```bash
eulix_parser \
  --root /path/to/huge-repo \
  --languages all \
  --prism 1 \
  --no-analyze \
  --output out/kb.json
```

`--no-analyze` is useful when you only need the parsed knowledge base and
want to skip call-graph and related analysis work.

---

## `.euignore`

`eulix_parser` uses its own ignore file rather than relying exclusively on
`.gitignore`. Create `.euignore` in the repository root. It uses
GitIgnore-style patterns.

```gitignore
fixtures/
generated/
vendor/
*.generated.go
```

---

## Performance

The CLI is built to process repository-scale codebases using parallel workers
and to perform structural analysis in the same pass rather than repeatedly
rediscovering repository information.

### Benchmark — Eulix Parser v0.8.1, Linux kernel

Run on an AMD Ryzen 5 5600X with 12 parser threads and PRISM v2:

```bash
eulix_parser --root linux -o test/kb.json --threads 12 --verbose --prism 2
```

|                           |            |
| ------------------------- | ---------- |
| Files discovered / parsed | 64,460     |
| Lines of code             | 35,810,828 |
| Functions                 | 612,250    |
| Classes                   | 206,142    |
| Methods                   | 3,324      |
| Parse time                | 37.98s     |
| Relationship + index      | 6.57s      |
| Summary + metrics         | 0.35s      |
| Output generation         | ~4.08s     |
| Parser-reported total     | 48.98s     |
| PRISM version             | v2         |
| Graph nodes               | 732,364    |
| Graph edges               | 1,301,641  |
| Maximum RSS               | ~8.32 GB   |
| User CPU time             | 346.68s    |
| System CPU time           | 20.54s     |
| CPU utilization           | 694%       |
| /usr/bin/time elapsed     | 52.85s     |

Zero skipped files, zero failed files.

> [!NOTE]
> The benchmark numbers depend on hardware, storage, repository layout, thread
> count, parser version, and `.euignore` configuration. They are measurements
> from a local system, not universal guarantees.

### Output size

`v0.8.1` reduces the main knowledge-base representation substantially
compared with `v0.7`:

| file                  | size                    |
| --------------------- | ----------------------- |
| kb.json               | 361,232.00 KB (~353 MB) |
| kb_index.json         | 389,107.86 KB           |
| kb_summary.json       | 2,921.43 KB             |
| kb_call_graph.json    | 372,749.91 KB           |
| kb_metrics.json       | 4.58 KB                 |
| kb_entry_points.json  | 34.75 KB                |
| kb_external_deps.json | 12,408.44 KB            |
| kb_patterns.json      | 0.13 KB                 |

The main `kb.json` went from ~2.06 GB in v0.7.6 to ~673 MB in v0.7.7 — a
67.3% reduction — while output-generation time dropped from ~6.89s to ~4.08s.
The reduction targets the serialized representation, not the parser's
in-memory structures.

---

## Architecture

At a high level:

```text
    Repository
        ⮟
    File Walker                (ignore-aware)
        ⮟
    libeulix::grammar_files    (Tree-sitter parsing)
        ⮟
    FileData (one per file)
        ⮟
    libeulix::analyze          (PRISM, indices, patterns, metrics, deps)
        ⮟
    KnowledgeBase
        ⮟
    JSON artifacts
```

The CLI itself does file discovery, spill-file handling, output writing,
and progress reporting. All parsing and analysis work lives in
[`libeulix`](../libeulix/). If you want to embed the engine in your own Rust
project instead of shelling out to `eulix_parser`, depend on `libeulix`
directly.

The important distinction is that Tree-sitter is the syntax foundation, not
the complete analysis system. Eulix adds repository-specific and
language-specific analysis on top of the syntax tree and materializes the
results into specialized artifacts so the downstream Eulix query engine can
answer questions without reparsing the repository.

---

## Security and pattern analysis

The parser can identify selected code and architectural signals useful to
downstream analysis, including TODOs, entry points, dependency information,
and architectural conventions.

These signals are analysis aids, not a replacement for dedicated security
tooling, testing, or manual review. PRISM and the generated call graph
should not be treated as a complete security data-flow or
vulnerability-analysis system.

---

## Development

```bash
cargo test
cargo fmt --check
cargo clippy
cargo build --release
```

---

## Related projects

| Project                        | Purpose                                                               |
| ------------------------------ | --------------------------------------------------------------------- |
| [libeulix](../libeulix/)       | The analysis engine (Rust library)                                    |
| [eulix-lsp](../eulix-lsp/)     | Language server built on `libeulix`                                   |
| [eulix-cli](../eulix-cli/)     | Main CLI, query routing, retrieval, context building, LLM integration |
| [eulix-embed](../eulix-embed/) | Local embedding generation and embedding storage                      |

---

<div align="center">

**eulix-parser**

_Fast enough to index the codebase. Structured enough to navigate it._

</div>
