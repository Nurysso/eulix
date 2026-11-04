# libeulix

`libeulix` is the Rust library that turns source code into a structured, queryable description of a whole repository.

> **Crate / path:** `libeulix/`  
> **Language:** Rust (Tree-sitter for parsing)  
> **Status:** stable for C, C++, Go, Python, Rust and TypeScript; JavaScript, Java and PHP are newer and need more testing  
> **Last verified against:** `923a577`

## Overview

`libeulix` is the engine underneath every other part of Eulix. It reads source files, extracts the functions, classes, imports and calls inside each one, and then stitches those per-file results together into a single `KnowledgeBase`. That knowledge base includes a project-wide call graph, which tells you which function calls which, even across files.

The other Eulix tools sit on top of this crate. `eulix-parser` is the binary that runs it over a repository and writes the result to JSON. `eulix-cli` reads that JSON to answer questions. `eulix-lsp` uses the library directly to power editor features. If you are new to the project, `libeulix` is the first thing to understand, because every other subsystem consumes the data it produces.

After reading this page you will know what the crate contains, how data moves through it, and which of the other pages in this folder to read next.

## Why It Exists

Answering a question about a large codebase ("who calls this function?", "what does this module depend on?") by re-reading and re-parsing the source every time is far too slow. The idea behind `libeulix` is to **parse once and navigate many times**: do the expensive work a single time, keep the result as plain data, and let every later query be a cheap lookup.

It also exists as a separate library, rather than as code inside the parser binary, so that several tools can share one implementation. The crate follows a strict rule that keeps it easy to embed: it performs no printing, no global state, and no hidden I/O in its core. Anything that talks to the terminal or decides where files are written belongs in the binary crates. (There is one small, documented exception, the file walker, described in [Public API](Public-API.md).)

## How It Works

The whole crate is a pipeline with three stages, and each stage hands plain data to the next.

First, **parsing** takes one source file and produces one `FileData`. The right Tree-sitter grammar is chosen from the file extension, and a language-specific parser walks the syntax tree to pull out functions, methods, classes, imports, docstrings, TODO comments and, importantly, every call made inside every function. Files are independent of each other at this stage, which is why they can be parsed in parallel.

Second, the `FileData` values are collected into a `KnowledgeBase`, keyed by each file's relative path. At this point the knowledge base knows what every file contains, but it does not yet know how the files relate to each other. A call to `save()` is only a name; nothing says which `save` is meant.

Third, **analysis** fills in the relationships. The most important pass is **PRISM**, which resolves each call name to the definition it most likely refers to, producing the call graph and the "called by" lists. Smaller passes then detect entry points, external dependencies, naming conventions and architecture style, and they build lookup indices and summary reports.

```text
source files
    │  grammar_files::parse_source        (one file at a time, parallel)
    ▼
FileData  ×  N files
    │  collected into KnowledgeBase.structure
    ▼
analyze::call_graph  (PRISM v1 or v2)   → call_graph, called_by
analyze::entry_points                   → entry_points
analyze::dependencies                   → external_dependencies
analyze::patterns                       → patterns
analyze::indices / metrics / summary    → derived reports
    ▼
KnowledgeBase  (serializable to JSON)
```

### Main Components

| Component | Location | Responsibility |
| --- | --- | --- |
| `grammar_files` | `src/grammar_files/` | Detects the language of a file and parses it into a `FileData`. One parser per language. |
| `struc` | `src/struc/kb_struct.rs` | Defines every data type: `FileData`, `Function`, `Class`, `CallGraph`, `KnowledgeBase`, and so on. |
| `analyze` | `src/analyze/` | Turns parsed files into relationships: call graphs, entry points, dependencies, patterns, indices, metrics, summaries. |
| `utils` | `src/utils/` | A gitignore-style file walker with project hashing, and a syntax-health checker for parse errors. |

### Data In, Data Out

The input is source text plus a path (the path decides the language). The output is a `KnowledgeBase`, which derives `Serialize` and `Deserialize`, so it round-trips through JSON without any extra code. The full shape is described in [Knowledge Base Schema](Knowledge-Base-Schema.md).

## Key Algorithms

`libeulix` has one algorithm that is genuinely its own, and it is the reason the crate exists as more than a thin wrapper around Tree-sitter.

### PRISM (call resolution)

**The problem.** After parsing, a call site is only text, such as `save()` or `self.validate()`. A real repository may have dozens of functions called `save`. To build a useful call graph, something has to decide which definition each call refers to, and it has to do that for hundreds of thousands of calls without taking minutes.

**The approach.** PRISM, short for *Polyglot Resolution via Inverted Symbol Map*, builds a lookup table from names to definitions and then resolves every call against it. It comes in two versions: [PRISM v1](PRISM-V1.md) is a fast, name-only lookup, and [PRISM v2](PRISM-V2.md) is a scope-aware lookup that searches from the nearest scope outward.

**Why this way.** A compiler-grade analysis would need full type information for nine languages, which is out of reach and too slow for repository scale. PRISM aims for something more modest and more useful: relationships that are right most of the time and cheap to compute. Treat its output as navigation evidence, not as proof of what happens at runtime.

## Configuration

`libeulix` has no configuration file of its own. Behaviour is chosen by the caller, through function arguments and one options struct.

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `CallGraphOptions::max_ambiguous` | argument to `prism_v2::build_with` | `8` | A name shared by more than this many definitions gets no edge when nothing nearer settles it. Raise it for more edges, lower it for fewer wrong ones. |
| `CallGraphOptions::strict_imports` | argument to `prism_v2::build_with` | `true` | Stops names imported from outside the project from linking to a project function of the same name. Turn it off if your TypeScript uses path aliases such as `@app/...`. |
| `prism` | argument to `analyze_and_build` | none (you choose) | `1` selects PRISM v1, `2` selects PRISM v2. |
| Cargo features | `Cargo.toml` | `num_cpus` on | `num_cpus`, `secure`, `simd`, `system` and `dev` are declared. `simd` forwards to the file walker's `ignore` dependency and `dev` forwards to `serde_json`; I found no `#[cfg(feature = ...)]` in the library source itself. |

## Usage Examples

The smallest useful example parses one file and prints what it found.

```rust
use std::path::Path;
use libeulix::grammar_files;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let path = Path::new("src/main.rs");
    let src = std::fs::read_to_string(path)?;

    let (rel_path, file) = grammar_files::parse_source(path, &src)?;
    println!("{rel_path}: {} functions, {} classes", file.functions.len(), file.classes.len());
    Ok(())
}
```

Expected result: one line naming the file with its function and class counts. More examples, including building a call graph, are in [Public API](Public-API.md).

## Performance Notes

The PRISM v2 source documents a target profile of about 1.4 seconds and 3.1 GB of peak memory for a roughly 64,000-file polyglot repository, producing about 773,000 nodes and 1.5 million edges. That figure comes from a comment in the source, and I did not re-measure it. Parallelism comes from `rayon`, and the library uses whatever thread pool the caller has set up.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| `.jsx` and `.tsx` files are rejected by `parse_source`. | The language detector recognizes them (`Language::Jsx`, `Language::Tsx`), but the dispatch `match` in `grammar_files/mod.rs` has no arm for them, so they fall through to "Unsupported language". I confirmed it by parsing a `.tsx` file: `Unsupported language: Tsx`. | Treat as a bug: [PARSE-2](../Known-Issues/Parser-And-PRISM.md#parse-2-jsx-and-tsx-files-are-rejected). |
| Ruby is not supported. | The old README lists it as "in development", but there is no Ruby grammar or parser in the crate. | None yet. |
| Dynamic behaviour is invisible. | Reflection, `getattr`, generated code and macros cannot be resolved from syntax alone. | None. This is inherent to the approach. |
| The built-in call graph is skipped for very large repositories. | `analyze_and_build` skips it above 100,000 files to bound memory. | Call the PRISM functions directly instead of using `analyze_and_build`. |
| Resolution is approximate. | PRISM guesses from names and scopes, not from types. | Treat edges as evidence, not proof. |

## Related Docs

- [Public API](Public-API.md): the functions you call and how to use them.
- [Knowledge Base Schema](Knowledge-Base-Schema.md): every field of the output.
- [Analysis Passes](Analysis-Passes.md): what each pass in `analyze/` computes.
- [PRISM v1](PRISM-V1.md) and [PRISM v2](PRISM-V2.md): the two call-resolution algorithms.
- [Parser And PRISM issues](../Known-Issues/Parser-And-PRISM.md): current problems.
