# Eulix Parser

> Despite the name, Eulix Parser is closer to a static code analysis and indexing engine than a traditional parser.

_It started as a parser, but has evolved into a repository analysis engine that builds structured representations of a codebase, including symbols, relationships, call graphs, language-specific metadata, architectural patterns, importance signals, metrics, and indexes._

`eulix_parser` is the Rust analysis engine behind Eulix.

It takes a source tree and turns it into structured code data: files, symbols, relationships, call graphs, project metrics, entry points, dependencies, and other metadata used by Eulix's retrieval and navigation layers.

Built with Rust + Tree-sitter, it is designed for large, mixed-language repositories where repeatedly walking the source tree is expensive.

> **Parse the repository once. Navigate it many times.**

---

## What it does

```text
    source tree
        ⮟
    file discovery
        ⮟
Tree-sitter parsing
        |
        ├── functions / methods
        ├── classes / structs / traits / interfaces
        ├── imports / dependencies
        ├── symbols and source locations
        ⮟
    relationship analysis
        |
        ├── call graph
        ├── reverse call graph
        ├── inheritance / type relationships
        ├── entry points
        ⮟
        indexes + metrics + project metadata
        ⮟
structured JSON knowledge base
```

This is what happens in a general term

```text
    code Base
        ⮟
      parse
        ⮟
    AST / symbols
        ⮟
    relationships / call graph
        ⮟
    language-specific analysis
        ⮟
    security/TODO patterns
        ⮟
    function tagging
        ⮟
    importance scoring
        ⮟
    complexity metrics
        ⮟
    indexes / KB
```

The parser is useful outside the full [eulix_cli](../eulix-cli/) too. Its output is structured data that can be consumed by retrieval systems, analysis tools, RAG pipelines, research tooling?? maybe, or other programs.

---

## Key features

### Multi-language parsing

Tree-sitter provides the syntax layer while Eulix extracts higher-level structures such as:

- functions and methods
- classes and structs
- interfaces and traits
- imports
- variables
- source locations and line ranges
- language-specific metadata

Current stable parser targets:

- C
- C++
- Go
- Python
- Rust
- TypeScript / TSX

> JavaScript / JSX and Java support exists but are in active development.

---

| Language             | File Extensions                       | Extracted Constructs & Specific Metadata                                                                                                                                                                                                                         |
| -------------------- | ------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **C**                | `.c`, `.h`                            | Functions, structs, unions, enums, `#include`, `#define` macros, typedefs, inline assembly, POSIX threads, syscalls, `malloc`/`free` tracking, security patterns.                                                                                                |
| **C++**              | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hxx` | Classes, structs, methods, constructors/destructors, operator overloads, templates, concepts, virtual/override/final methods, access specifiers, exception safety.                                                                                               |
| **Go**               | `.go`                                 | Packages, imports, functions, methods (pointer/value receivers), structs, interfaces, embedded types, goroutines, channels, `select`, `defer`, build tags, cgo, directives (`//go:embed`).                                                                       |
| **Python**           | `.py`, `.pyw`, `.pyi`                 | Functions, classes, async functions, decorators, dataclasses, class/static methods, properties, Flask/API routes, exception blocks, docstrings.                                                                                                                  |
| **Rust**             | `.rs`                                 | Functions, structs, enums, unions, traits, `impl` blocks (inherent & trait), generics, lifetimes, where-clauses, macros (`macro_rules!` and invocations), `unsafe` blocks, derives, `?` operator.                                                                |
| **TypeScript / TSX** | `.ts`, `.tsx`, `.mts`, `.cts`         | Classes, interfaces, types, functions, arrow functions, methods, decorators, generic parameters, abstract classes, optional properties, DOM/XSS security flags.                                                                                                  |
| **JavaScript / JSX** | `.js`, `.mjs`, `.cjs`, `.jsx`         | Classes, functions, arrow functions, methods, async functions, generators, prototype manipulations, React components/JSX, `eval`/`Function` code injection, DOM XSS sinks (`innerHTML`), child_process execution, prototype pollution, CORS wildcards.           |
| **Java**             | `.java`                               | Classes, interfaces, enums, records, annotations, constructors, methods, fields, package statements, imports, generics, `synchronized` blocks, try-with-resources, SQL injection, command exec, deserialization, XXE, reflection, trust manager vulnerabilities. |

### PRISM relationship analysis

`eulix_parser` includes the **PRISM** engine for large-scale approximate relationship resolution.

PRISM exists for one reason:

> **Recover useful code relationships without making whole-program analysis prohibitively expensive.**

Depending on the selected version, PRISM uses symbol metadata, scope information, class information, inheritance relationships, and language-aware heuristics to connect call sites to likely definitions.
This is **retrieval-oriented analysis**, not a formal compiler or proof system.
That distinction matters.

PRISM is designed to give Eulix useful structural context for navigation and retrieval at repository scale, while accepting that dynamic dispatch, reflection, generated code, and other language features can make perfect resolution impossible.

#### PRISM v1

The simpler, high-throughput relationship path.
Useful when you want:

- fast symbol-based resolution
- broad call-graph coverage
- lower analysis cost

#### PRISM v2

The more scope-aware relationship path.
It keeps more information about:

- modules / files
- classes and methods
- inheritance
- shadowing
- receiver / method context

This allows common ambiguous relationships to be resolved more accurately than a simple global-name lookup.

See the PRISM [research](https://dawood.page/comming-soon) and parser [architecture documentation](../docs/eulix-parser/Architecture.md) for implementation details.

---

## Performance

The parser is built to process repository-scale codebases using parallel workers and to perform structural analysis in the same pass rather than repeatedly rediscovering repository information.
Eulix Parser `v0.7.7` large repository benchmark

Run on an AMD Ryzen 5 5600X with 12 parser threads and PRISM v2:

```bash
eulix_parser --root linux -o test/kb.json --threads 12 --verbose --prism 2

Files discovered / parsed: 59,242
Lines of code: 35,810,828
Functions: 612,250
Classes: 206,142
Methods: 3,324

Parse time: 37.98s
Relationship + index analysis: 6.57s
Summary + metrics: 0.35s
Output generation: ~4.08s
Parser-reported total: 48.98s

PRISM: v2
Graph nodes: 732,364
Graph edges: 1,301,641

Maximum RSS: ~8.32 GB
User CPU time: 346.68s
System CPU time: 20.54s
CPU utilization: 694%

Filesystem input blocks: 897,312
Filesystem output blocks: 2,900,712
Major page faults: 13
Minor page faults: 1,639,639

/usr/bin/time elapsed: 52.85s
```

The benchmark successfully parsed 59,242 files with zero skipped and zero failed files while constructing the repository's structural representations and analysis artifacts.

Output size
`v0.7.7` also substantially reduces the main knowledge-base representation compared with `v0.7.6`:

- Artifacts v0.7.7

```go
kb.json:               673,063.99 KB (~673 MB)
kb_index.json:         389,107.86 KB
kb_summary.json:       2,921.43 KB
kb_call_graph.json:    372,749.91 KB
kb_metrics.json:       4.58 KB
kb_entry_points.json:  34.75 KB
kb_external_deps.json: 12,408.44 KB
kb_patterns.json:      0.13 KB
```

The main kb.json went from 2,060,391.87 KB (~2.06 GB) in v0.7.6 to 673,063.99 KB (~673 MB) in v0.7.7 — a 67.3% reduction.

This reduction targets the serialized representation rather than replacing the parser's rich in-memory structures. The parser can keep the information needed during analysis while writing a more compact representation for downstream consumers.

The smaller representation also reduces filesystem output from 5,676,432 blocks to 2,900,712 blocks, while output-generation time dropped from roughly 6.89s to 4.08s in the compared runs.

The exact benchmark numbers depend on hardware, storage, repository layout, thread count, parser version, and .euignore configuration. They are measurements from a local AMD Ryzen 5 5600X system, not universal performance guarantees.

## Why Rust?

The parser spends most of its time doing work that benefits from:

- predictable memory usage
- cheap concurrency
- explicit data ownership
- fast file and serialization paths
- low runtime overhead

Parallel file processing is built with Rayon.
The parser also uses optimized memory and I/O paths where useful, including `mimalloc`, memory mapping, and platform-aware file access.
The goal is not benchmark theater.
The goal is to make repository-scale analysis cheap enough that Eulix can actually rebuild or refresh its view of a large codebase.

---

# Supported languages

| Language         | Extensions                            | Status         |
| ---------------- | ------------------------------------- | -------------- |
| C                | `.c`, `.h`                            | Stable         |
| C++              | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hxx` | Stable         |
| Go               | `.go`                                 | Stable         |
| Python           | `.py`, `.pyw`, `.pyi`                 | Stable         |
| Rust             | `.rs`                                 | Stable         |
| TypeScript / TSX | `.ts`, `.tsx`, `.mts`, `.cts`         | Stable         |
| JavaScript / JSX | `.js`, `.mjs`, `.cjs`, `.jsx`         | Needs testing  |
| Java             | `.java`                               | Needs testing  |
| Ruby             | `.rb`                                 | In development |

Language support is not just syntax support. Some language features make relationship analysis substantially harder than parsing alone, including dynamic dispatch, reflection, generated code, macros, and runtime metaprogramming.

---

# Installation

## Prerequisites

- Rust stable
- Cargo
- C/C++ build tools required by the Tree-sitter grammar dependencies

A Rust toolchain can be installed with [rustup](https://rustup.rs).

## Build

```bash
cd eulix-parser
cargo build --release
```

---

# CLI

```text
eulix_parser [OPTIONS] --root <ROOT> --prism <PRISM>
```

## Options

| Option                | Short | Description                                     | Default               |
| --------------------- | ----- | ----------------------------------------------- | --------------------- |
| `--root <ROOT>`       | `-r`  | Repository root                                 | Required              |
| `--output <OUTPUT>`   | `-o`  | Knowledge base output path                      | `knowledge_base.json` |
| `--threads <N>`       | `-t`  | Number of parser workers                        | `4`                   |
| `--languages <LANGS>` | `-l`  | Comma-separated languages or `all`              | `all`                 |
| `--prism <1\|2>`      | `-p`  | PRISM relationship engine version               | Required              |
| `--no-analyze`        |       | Parse files without relationship/analysis phase | `false`               |
| `--euignore <PATH>`   |       | Custom `.euignore` path                         | `<root>/.euignore`    |
| `--verbose`           | `-v`  | Detailed phase output                           | `false`               |
| `--version`           | `-V`  | Print version                                   |                       |

---

# Examples

### Full analysis with PRISM v2

```bash
./target/release/eulix_parser \
--root /path/to/project \
--prism 2 \
--output .eulix/kb.json \
--threads 12 \
--verbose
```

### Faster relationship analysis with PRISM v1

```bash
./target/release/eulix_parser \
--root /path/to/project \
--prism 1 \
--output .eulix/kb.json \
--threads 12 \
--verbose
```

### Parse selected languages

```bash
./target/release/eulix_parser \
--root /path/to/project \
--languages rust,go \
--prism 2 \
--output out/kb.json
```

### Parse without analysis

```bash
./target/release/eulix_parser \
--root /path/to/huge-repo \
--languages all \
--prism 1 \
--no-analyze \
--output out/kb.json
```

`--no-analyze` is useful when you only need the parsed knowledge base and want to skip call-graph and related analysis work.

---

# Output

A normal analysis produces a set of synchronized JSON artifacts.

| File                        | Purpose                                                                                               |
| --------------------------- | ----------------------------------------------------------------------------------------------------- |
| `<base>.json`               | Main knowledge base containing file, symbol, AST-derived, and language metadata                       |
| `<base>_call_graph.json`    | Call-graph nodes and edges, including call sites and relationship metadata                            |
| `<base>_index.json`         | Fast lookup indexes such as functions-by-name, caller relationships, tags, types, and file categories |
| `<base>_summary.json`       | Project summary and language / LOC information                                                        |
| `<base>_metrics.json`       | Complexity and code metrics                                                                           |
| `<base>_entry_points.json`  | Discovered application entry points and route / command metadata                                      |
| `<base>_external_deps.json` | External package / dependency information                                                             |
| `<base>_patterns.json`      | Detected structural and architectural patterns                                                        |

With `--no-analyze`, the parser writes only the main knowledge base artifact.

---

# Call graphs

The generated call graph is intended to answer questions such as:

```text
Who calls this function?
What does this function call?
What functions are connected to this subsystem?
Where does this execution path continue?
Which files participate in this flow?
```

A relationship can contain information such as:

```text
from
to
edge type
call site
conditional state
```

The graph can also be traversed in reverse to support caller-oriented queries.

Because PRISM is approximate, graph consumers should treat relationships as **navigation evidence**, not absolute proof of runtime behavior.

---

# `.euignore`

Eulix_parser uses its own ignore file rather than relying exclusively on `.gitignore`.

Create `.euignore` in the repository root. It uses GitIgnore-style patterns.

Example:

```gitignore
fixtures/
generated/
vendor/
*.generated.go
```

---

# Architecture

At a high level:

                         Repository
                             🡓
                      +--------------+
                      | File Walker  |
                      +------+-------+
                             🡓
                      +--------------+
                      | Tree-sitter  |
                      | AST parsing  |
                      +------+-------+
                             |
               +-------------+-------------+
               🡓             🡓             🡓
           Symbols       Metadata     Source info
               |             |             |
               +-------------+-------------+
                             🡓
                  +----------------------+
                  | Repository Analysis  |
                  |                      |
                  | PRISM relationships  |
                  | Call graphs          |
                  | Language analysis    |
                  | Pattern detection    |
                  | Importance / tags    |
                  | Complexity metrics   |
                  | Entry points         |
                  | Dependencies         |
                  +----------+-----------+
                             |
               +-------------+-------------+
               🡓             🡓             🡓
          Call Graph      Indexes       Metrics
               |             |             |
               +-------------+-------------+
                             🡓
                  JSON knowledge artifacts

The important distinction is that Tree-sitter is the syntax foundation, not the complete analysis system. Eulix adds repository-specific and language-specific analysis on top of the syntax tree and materializes the results into specialized artifacts.

The downstream Eulix query engine can then use those artifacts without reparsing the repository for every question.

            +-------+--------+
            |  File Walker   |
            +-------+-------+
                     🡓
             +---------------+
             | Tree-sitter   |
             | AST parsing   |
             +-------+-------+


         +------------+------------+
         🡓           🡓           🡓
      Symbols     Metadata    Source info
         |            |            |
         +------------+------------+
                     |
                     v
             +---------------+
             |    PRISM      |
             | relationships |
             +-------+-------+
                     |
         +-----------+-----------+
         🡓           🡓         🡓
    Call Graph     Indexes     Metrics
         |           |           |
         +-----------+-----------+
                     🡓
              JSON artifacts

The downstream Eulix query engine can then use those artifacts without reparsing the repository for every question.

---

# Security and pattern analysis

The parser can identify selected code and architectural signals that are useful to downstream analysis.

Depending on the language and available metadata, this can include:

=- TODOs

- entry points
- dependency information
- architectural conventions

These signals are analysis aids, not a replacement for dedicated security tooling, testing, or manual review.

In particular, PRISM and the generated call graph should not be treated as a complete security data-flow or vulnerability-analysis system.

---

# Design goals

### 1. Parse large repositories quickly

A 30M+ LOC repository should be something the tool can actually process in consumer grade cpus, not a theoretical upper bound that can only achieve on threadripper cpu.

### 2. Keep the output useful

The output is consumed by other Eulix components, so stable identifiers and useful source relationships matter as much as raw parse throughput.

### 3. Prefer cheap approximations when they are good enough

Not every downstream task needs compiler-grade whole-program reasoning.

For code navigation, a fast and useful relationship can be better than an expensive attempt at perfect resolution.

### 4. Keep the parser usable independently

`eulix_parser` is not just an implementation detail of the main CLI.

Its output is useful as a standalone structured representation of a repository.

---

# Development

Run tests:

```bash
cargo test
```

Check formatting:

```bash
cargo fmt --check
```

Run Clippy:

```bash
cargo clippy
```

Build a release binary:

```bash
cargo build --release
```

---

# Research

Apart from maintaining this codebase i am working on research paper on PRISM, mainly for experience and potential benefit in my masters application.

The central question is:

> **How much useful structural information can we recover from a large repository without paying the cost of full program analysis?**

The current work explores identity-based resolution, scope-aware relationships, class and inheritance information, and language-aware heuristics.

The intent is not to compete with a compiler's type system.

The intent is to make repository-scale code navigation better.

> [!IMPORTANT]
> In future release will be related to better grammar file, support for more languages
> and ability switch between less verbose knowledgebase and the older denser knowledgebase

---

# Related projects

| Project                       | Purpose                                                                  |
| ----------------------------- | ------------------------------------------------------------------------ |
| [eulix_cli](../eulix-cli/)    | Main CLI, query routing, retrieval, context building and LLM integration |
| [eulix_embed](../eulix-embed) | Local embedding generation and embedding storage                         |

---

<div align="center">

**Eulix Parser**

_Fast enough to index the codebase. Structured enough to navigate it._

</div>
