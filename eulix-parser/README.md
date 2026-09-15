# Eulix Parser

`eulix_parser` is the Rust analysis engine behind Eulix.

It takes a source tree and turns it into structured code data: files, symbols, relationships, call graphs, project metrics, entry points, dependencies, and other metadata used by Eulix's retrieval and navigation layers.

Built with Rust + Tree-sitter, it is designed for large, mixed-language repositories where repeatedly walking the source tree is expensive.

> **Parse the repository once. Navigate it many times.**

---

## What it does

```text
source tree
    |
    v
file discovery
    |
    v
Tree-sitter parsing
    |
    +--> functions / methods
    +--> classes / structs / traits / interfaces
    +--> imports / dependencies
    +--> symbols and source locations
    |
    v
relationship analysis
    |
    +--> call graph
    +--> reverse call graph
    +--> inheritance / type relationships
    +--> entry points
    |
    v
indexes + metrics + project metadata
    |
    v
structured JSON knowledge base
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

JavaScript / JSX and Java support exists but are in active development.

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

See the PRISM research and parser architecture documentation for implementation details.

---

## Performance

The parser is built to scale across files using parallel workers.

Recent repository-scale measurements:

**Command:**

```bash
eulix_parser --root . --output out/kb.json --threads 12 --verbose --prism 2
```

### Linux kernel

Local benchmark:

```text
Files:                33,743
Failed Files:         0
Lines of code:        25,200,532
Functions:            612,250
Classes:              206,142
Methods:              3324

Graph nodes:          732,364
Graph edges:          1,301,640

Parser time:          37.83s
Analysis time:        6.10s
complete run:         51.39s
Peak RSS:             ~7.98 GB
Threads:              12
PRISM:                v2
Output Written:       ~5.41 GB JSON

Elapsed (Wall):       0:55.10 (55.10 sec)
User CPU Time:        349.69 sec
Sys CPU Time:         17.20 sec
CPU Usage:            665%

Voluntary Switches:   52,301
Involuntary Switches: 59,368

Major (I/O) Faults:   9
Minor Faults:         1,238,154
File Inputs:          769,664
File Outputs:         5,674,576
```

### OpenStack

Local benchmark:

```text
Files processed:      29,623
Failed Files:         0
Lines of code:        6,936,415
Functions:            46,361
Classes:              51,520
Methods:              208,382

Graph nodes:          306,165
Graph edges:          757,025

Parser time:          7.93s
Analysis time:        1.91s
complete run:         12.59s
Peak RSS:             ~3.16GB
Threads:              12
PRISM:                v2
Output Written:       ~1.2 GB

Elapsed (Wall):       0:13.59 (13.59 sec)
User CPU Time:        84.98 sec
Sys CPU Time:         2.97 sec
CPU Usage:            646%

Voluntary Switches:   22,292
Involuntary Switches: 11,677

Major (I/O) Faults:   3
Minor Faults:         85,476
File Inputs:          330,376
File Outputs:         2,439,776
```

These are perf are ran on my pc(amd 5600x cpu) and not universal performance guarantees. Hardware, storage, repository layout, thread count, parser version, and euignore configuration all these factors affects results.

---

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
| JavaScript / JSX | `.js`, `.mjs`, `.cjs`, `.jsx`         | In development |
| Java             | `.java`                               | In development |

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

```text
                    Repository
                         |
                         v
                 +---------------+
                 | File Walker   |
                 +-------+-------+
                         |
                         v
                 +---------------+
                 | Tree-sitter   |
                 | AST parsing   |
                 +-------+-------+
                         |
             +-----------+-----------+
             |           |           |
             v           v           v
          Symbols     Metadata    Source info
             |           |           |
             +-----------+-----------+
                         |
                         v
                 +---------------+
                 |    PRISM      |
                 | relationships |
                 +-------+-------+
                         |
             +-----------+-----------+
             |           |           |
             v           v           v
        Call Graph     Indexes     Metrics
             |           |           |
             +-----------+-----------+
                         |
                         v
                  JSON artifacts
```

The downstream Eulix query engine can then use those artifacts without reparsing the repository for every question.

---

# Security and pattern analysis

The parser can identify selected code and architectural signals that are useful to downstream analysis.

Depending on the language and available metadata, this can include:

- security-sensitive patterns
- TODOs
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
> In future releases i will be focusing on smaller kb.json file cause its too huge and not every struct is used in retrieval, the plan is to have 2 modes detailed(current implementation) for static analysis? maybe, and a less verbose version for retrieval in [eulix_cli](../eulix-cli/)

---

# Related projects

| Project        | Purpose                                                                  |
| -------------- | ------------------------------------------------------------------------ |
| `eulix`        | Main CLI, query routing, retrieval, context building and LLM integration |
| `eulix_parser` | Repository parsing and structural analysis                               |
| `eulix_embed`  | Local embedding generation and embedding storage                         |

---

<div align="center">

**Eulix Parser**

_Fast enough to index the codebase. Structured enough to navigate it._

</div>
