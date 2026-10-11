# libeulix

> Multi-language static analysis for Rust. Parses C, C++, Go, Java,
> JavaScript, Python, Rust, and TypeScript into a structured knowledge base
> with a project-wide resolved call graph.

`libeulix` is the analysis engine behind the [Eulix](../README.md) toolchain.
It takes source text and turns it into structured code data: files, symbols,
relationships, call graphs, project metrics, entry points, dependencies.
It's designed for large, mixed-language repositories where re-parsing from
scratch for every query is too expensive.

> **Parse once. Navigate many times.**

Built with Rust + Tree-sitter.

---

## What it does

```text
    source text
        ⮟
Tree-sitter syntax parsing
        |
        ├── functions / methods
        ├── classes / structs / traits / interfaces
        ├── imports / dependencies
        ├── docstrings / parameters
        ├── source line ranges
        ⮟
    FileData  (one per file)
        |
        |  merge many FileData
        ⮟
    relationship analysis  (PRISM)
        |
        ├── resolved call graph
        ├── reverse call graph
        ├── inheritance / type relationships
        ├── entry points
        ├── indices
        ├── patterns
        ├── metrics
        ⮟
    KnowledgeBase   (structured, serializable)
```

Tree-sitter is the syntax foundation; `libeulix` adds the repository-level
relationship extraction on top. The result is a value you can query directly,
serialize to JSON, or feed into a retrieval / navigation layer without
reparsing the repository.

`libeulix` is not just an implementation detail of the Eulix CLI. Its output
is useful as a standalone structured representation of a repository.

---

## Install

```shell
cargo add libeulix
```

```toml
# if using in eulix and want latest version and not from cargo
[dependencies]
libeulix = { path = "../libeulix", version = "0.8.1" }
```

Rust stable is required. The tree-sitter grammar crates need a C/C++ compiler at build time.

---

## Quick start

### Parse a single file

```rust
use std::path::Path;
use libeulix::grammar_files;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let path = Path::new("src/main.rs");
    let src = std::fs::read_to_string(path)?;

    let (_rel_path, fd) = grammar_files::parse_source(path, &src)?;

    println!("{}: {} functions, {} classes",
             path.display(), fd.functions.len(), fd.classes.len());

    for f in &fd.functions {
        println!("  fn {} (line {})", f.name, f.line_start);
        for call in &f.calls {
            println!("    → {} at line {}", call.callee, call.line);
        }
    }
    Ok(())
}
```

### Parse many files and build a call graph

```rust
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use libeulix::grammar_files;
use libeulix::analyze::call_graph::prism_v2;
use libeulix::struc::kb_struct::FileData;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let files: Vec<PathBuf> = vec![
        "src/lib.rs".into(),
        "src/main.rs".into(),
    ];

    let mut structure: HashMap<String, FileData> = HashMap::new();
    for path in &files {
        let src = std::fs::read_to_string(path)?;
        let (rel, fd) = grammar_files::parse_source(path, &src)?;
        structure.insert(rel, fd);
    }

    let opts = prism_v2::CallGraphOptions::default();
    let cg = prism_v2::build_with(&structure, &opts);

    println!("{} nodes, {} edges", cg.nodes.len(), cg.edges.len());
    Ok(())
}
```

### Full analysis (all passes)

```rust
use libeulix::analyze::pipeline::analyze_and_build;
use libeulix::struc::kb_struct::{KnowledgeBase, Metadata};
# use std::collections::HashMap;

# fn build_kb() -> KnowledgeBase { todo!() }
let kb = build_kb();
let kb = analyze_and_build(kb, verbose =false, prism =2);

// kb.call_graph, kb.entry_points, kb.patterns, kb.external_dependencies
// are now populated.
```

---

## API overview

Everything is a free function on a module. There is no engine singleton and
no global state — the crate is designed to be embeddable.

| Module                                                         | Purpose                                           |
| -------------------------------------------------------------- | ------------------------------------------------- |
| `grammar_files::parse_source(path, src)`                       | Parse one in-memory source file into a `FileData` |
| `grammar_files::parse_file(path)`                              | Read from disk and parse                          |
| `grammar_files::language::Language::detect(path)`              | Map a path to a language                          |
| `analyze::pipeline::analyze_and_build(kb, verbose, prism)`     | Run every analysis pass over a `KnowledgeBase`    |
| `analyze::call_graph::prism_v1::build(&structure)`             | Fast, name-based call graph                       |
| `analyze::call_graph::prism_v2::build_with(&structure, &opts)` | Scope-aware call graph                            |
| `analyze::indices::generate(&kb)`                              | Fast lookup indices                               |
| `analyze::summary::generate(&kb)`                              | Project summary                                   |
| `analyze::metrics::generate(&kb, top_k)`                       | Complexity / importance metrics                   |
| `analyze::entry_points::find(&kb)`                             | Entry points (main, routes, CLI commands)         |
| `analyze::dependencies::analyze(&kb)`                          | External dependency extraction                    |
| `analyze::patterns::detect(&kb)`                               | Architectural pattern detection                   |
| `struc::kb_struct::*`                                          | The `FileData` / `KnowledgeBase` schema           |

The two top-level types are:

- **`FileData`** — one file's parsed content: functions, classes, methods,
  imports, docstrings, per-function calls with line numbers.
- **`KnowledgeBase`** — a map of relative path → `FileData`, plus metadata
  and everything the analysis passes derive from it.

Both are `Serialize` / `Deserialize`, so they round-trip through JSON.

---

## Supported languages

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

Language support is not just syntax support. Some language features make
relationship analysis substantially harder than parsing alone — dynamic
dispatch, reflection, generated code, macros, runtime metaprogramming. The
parser extracts what's there; PRISM resolves what can reasonably be resolved.

---

## PRISM

`libeulix` includes the **PRISM** relationship engine for large-scale
approximate call-graph resolution.

PRISM exists for one reason:

> **Recover useful code relationships without making whole-program analysis
> prohibitively expensive.**

Depending on the selected version, PRISM uses symbol metadata, scope
information, class information, inheritance relationships, and
language-aware heuristics to connect call sites to likely definitions.

This is **retrieval-oriented analysis**, not a formal compiler or proof
system. That distinction matters. PRISM is designed to give useful
structural context at repository scale, while accepting that dynamic
dispatch, reflection, generated code, and similar features make perfect
resolution impossible.

### PRISM v1

The simpler, high-throughput relationship path. Useful when you want fast
symbol-based resolution and broad call-graph coverage at lower cost.

```rust
use libeulix::analyze::call_graph::prism_v1;
let cg = prism_v1::build(&structure);
```

### PRISM v2

The more scope-aware relationship path. It keeps more information about
modules, classes, methods, inheritance, shadowing, and receiver context,
which lets common ambiguous relationships be resolved more accurately than a
plain global-name lookup.

```rust
use libeulix::analyze::call_graph::{prism_v2, prism_v2::CallGraphOptions};

let opts = CallGraphOptions {
    max_ambiguous: 8,
    strict_imports: true,
};
let cg = prism_v2::build_with(&structure, &opts);
```

Call graph edges are labelled (`call` / `inheritance`) and carry a call-site
line and a conditional flag.

Because PRISM is approximate, graph consumers should treat relationships as
**navigation evidence**, not absolute proof of runtime behaviour.

---

## Design goals

### 1. Parse large repositories quickly

A 30M+ LOC repository should be something the engine can process on consumer
hardware, not a theoretical upper bound that only runs on a threadripper.

### 2. Keep the output useful

The output is consumed by other tools. Stable identifiers and useful source
relationships matter as much as raw parse throughput.

### 3. Prefer cheap approximations when they are good enough

Not every downstream task needs compiler-grade whole-program reasoning. For
code navigation, a fast and useful relationship is often better than an
expensive attempt at perfect resolution.

### 4. Keep the library embeddable

No global state, no required runtime, no required tokio. Every public
function takes its input by reference and returns owned data. Parallelism is
opt-in via the caller's own rayon pool.

---

## Development

```bash
cargo test
cargo fmt --check
cargo clippy
```

---

## Research

The PRISM engine is also the subject of ongoing research on the question:

> **How much useful structural information can we recover from a large
> repository without paying the cost of full program analysis?**

The work explores identity-based resolution, scope-aware relationships,
class and inheritance information, and language-aware heuristics. It is not
trying to compete with a compiler's type system; it is trying to make
repository-scale code navigation better.

---

## Related projects

| Project                          | Purpose                                                                        |
| -------------------------------- | ------------------------------------------------------------------------------ |
| [eulix-parser](../eulix-parser/) | CLI producing JSON knowledge-base artifacts from a repository                  |
| [eulix-lsp](../eulix-lsp/)       | Language server built on `libeulix`                                            |
| [eulix-cli](../eulix-cli/)       | Main CLI, query routing, retrieval, context building, LLM integration          |
| [eulix-embed](../eulix-embed/)   | Local embedding generation and embedding storage that uses knowledge_base.json |

---

<div align="center">

**libeulix**

_The analysis engine inside Eulix._

</div>
