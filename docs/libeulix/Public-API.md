# libeulix Public API

This page lists the functions and types that `libeulix` exposes, shows how they fit together, and gives runnable examples.

> **Crate / path:** `libeulix/src/`  
> **Language:** Rust  
> **Status:** stable  
> **Last verified against:** `923a577`

## Overview

Everything in `libeulix` is a plain function that takes its input by reference and returns owned data. There is no engine object to create, no global state, and no async runtime to set up. That makes the library easy to embed: you call a function, you get a value back.

The public surface is split across four modules. `grammar_files` parses files, `analyze` computes relationships, `struc` holds the data types, and `utils` offers a file walker and a parse-error checker. This page walks through them in the order you will usually need them.

> **A note on older docs.** The previous `libeulix/README.md` lists a few names that do not exist in the code, such as `grammar_files::parse_file`, `entry_points::find`, `dependencies::analyze`, `indices::generate`, `summary::generate` and `metrics::generate`. The names below are the real ones, taken from the source.

## Why It Exists

A small, predictable API is what lets four different tools share one engine. Because the functions are pure (same input, same output, no side effects), the parser binary can run them in parallel, the language server can run them on a single edited file, and tests can call them directly with hand-built data.

## How It Works

A typical program uses the API in three steps: parse, collect, analyze. Parsing is done per file, so you can run it on as many threads as you like. Collecting just means putting each `FileData` into a map keyed by relative path. Analysis then takes that map, or a full `KnowledgeBase`, and adds relationships to it.

### Main Components

| Function | Module | What it does |
| --- | --- | --- |
| `parse_source(path, src)` | `grammar_files` | Parses one in-memory source file into a `FileData`. Returns `Result<(String, FileData), String>`, where the string is the file's relative path. |
| `Language::detect(path)` | `grammar_files::language` | Maps a path to a `Language`, using the extension first and then well-known file names. No file is read. |
| `Language::extensions()` | `grammar_files::language` | Lists the extensions registered for a language. |
| `analyze_and_build(kb, verbose, prism)` | `analyze::pipeline` | Runs every pass in one call. **Deprecated since 0.9.0** and scheduled for removal in 1.0.0. |
| `prism_v1::resolve_call_locations(&mut kb)` | `analyze::call_graph` | v1 pre-pass. Rewrites each call's callee to a definition id. Must run before `build_call_graph`. |
| `prism_v1::build_call_graph(&structure)` | `analyze::call_graph` | Builds the v1 call graph. |
| `prism_v1::populate_called_by(&mut kb)` | `analyze::call_graph` | Fills each function's `called_by` list for v1. |
| `prism_v2::build_call_graph_v2(&structure)` | `analyze::call_graph` | Builds the v2 call graph with default options. |
| `prism_v2::build_with(&structure, &opts)` | `analyze::call_graph` | Builds the v2 call graph with your own `CallGraphOptions`. |
| `prism_v2::populate_called_by_from_graph(&mut kb)` | `analyze::call_graph` | Fills each function's `called_by` list from a finished call graph. |
| `find_entry_points(&kb)` | `analyze::entry_points` | Finds `main`-style functions, HTTP routes and CLI commands. |
| `analyze_external_deps(&kb)` | `analyze::dependencies` | Lists third-party and standard-library imports with usage counts. |
| `detect_patterns(&kb)` | `analyze::patterns` | Guesses naming convention, project layout and architecture style. |
| `generate_indices_view(&kb)` | `analyze::indices` | Builds borrowed lookup indices (by name, by tag, by category, callers). |
| `generate_metrics(&kb, k)` | `analyze::metrics` | Returns the top `k` functions ranked by complexity. |
| `generate_summary(&kb)` | `analyze::summary` | Builds a `ProjectSummary` of the whole project. |
| `FileWalker` | `utils::file_walker` | Walks a directory honouring `.euignore`, and can compute a project hash. |
| `syntax::inspect(&tree)` | `utils::syntax` | Counts Tree-sitter `ERROR` and `MISSING` nodes in a parsed tree. |

The input to the call-graph functions is `&BTreeMap<String, FileData>`, which is the type of `KnowledgeBase::structure`. Both PRISM builders take that map, not a whole knowledge base.

### Data In, Data Out

`parse_source` returns a `String` error, not a typed error. The most common reasons are an unsupported language (`"Unsupported language: ..."`) and, for `.inc` files, a missing PHP opening tag (`"not PHP: a .inc file without a PHP opening tag"`).

The call-graph builders return a `CallGraph` with `nodes` and `edges`. Nodes carry an id, a type (`function`, `method` or `class`), the file, an entry-point flag and a call-count estimate. Edges carry the caller id, the callee id, an `edge_type` (`call` or `inheritance`), a `conditional` flag and the line of the call site. See [Knowledge Base Schema](Knowledge-Base-Schema.md).

## Key Algorithms

### Choosing a language

**The problem.** A file name is the only hint about the language, but `.h` could be C or C++, and `.inc` could be PHP, C or assembler.

**The approach.** A single table maps extensions and a few special file names to a `Language`. The lookup tries the exact extension first, then a lowercase version, then the file name. `.h` is treated as C. For `.inc`, `parse_source` goes one step further and accepts the file as PHP only if it contains a PHP opening tag.

**Why this way.** Keeping one table as the single source of truth means the detector and `Language::extensions()` can never disagree.

### Walking a project and hashing it

**The problem.** Callers need a list of source files that respects ignore rules, and a cheap way to tell whether a project has changed since the last run.

**The approach.** `FileWalker` uses the `ignore` crate, reads `.euignore` files (or a path set with `with_euignore`) and deliberately turns off `.gitignore` handling. The project hash is built from each file's relative path, size and modification time, sorted by path, so it changes when files are added, removed or touched, without ever reading file contents.

**Why this way.** Hashing metadata instead of contents keeps change detection fast on very large repositories.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `FileWalker::with_threads(n)` | builder method | available parallelism, or 4 | Sets how many threads the walk uses. |
| `FileWalker::with_euignore(path)` | builder method | `<root>/.euignore` | Adds an extra ignore file on top of the per-directory `.euignore` files. |
| `CallGraphOptions` | struct | see [Overview](Overview.md) | Controls PRISM v2 strictness. |

## Usage Examples

### Parse many files and build a call graph

```rust
use std::collections::BTreeMap;
use std::path::PathBuf;
use libeulix::analyze::call_graph::prism_v2::{self, CallGraphOptions};
use libeulix::grammar_files;
use libeulix::struc::kb_struct::FileData;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let files: Vec<PathBuf> = vec!["src/lib.rs".into(), "src/main.rs".into()];

    let mut structure: BTreeMap<String, FileData> = BTreeMap::new();
    for path in &files {
        let src = std::fs::read_to_string(path)?;
        let (rel, data) = grammar_files::parse_source(path, &src)?;
        structure.insert(rel, data);
    }

    let graph = prism_v2::build_with(&structure, &CallGraphOptions::default());
    println!("{} nodes, {} edges", graph.nodes.len(), graph.edges.len());
    Ok(())
}
```

Expected result: a single line with the node and edge counts. Note that the map must be a `BTreeMap`, not a `HashMap`, because that is what the schema and both builders use.

### Use PRISM v1 correctly

```rust
use libeulix::analyze::call_graph::prism_v1;

// `kb` is a KnowledgeBase whose `structure` is already filled in.
prism_v1::resolve_call_locations(&mut kb);          // 1. must come first
kb.call_graph = prism_v1::build_call_graph(&kb.structure); // 2. build
prism_v1::populate_called_by(&mut kb);              // 3. reverse edges
```

The order matters. Without the first step, v1 cannot resolve bare call names, as explained in [PRISM v1](PRISM-V1.md).

### Run the remaining passes by hand

Because `analyze_and_build` is deprecated, new code should call the passes directly:

```rust
use libeulix::analyze::{dependencies, entry_points, patterns};

kb.patterns = patterns::detect_patterns(&kb);
kb.entry_points = entry_points::find_entry_points(&kb);
kb.external_dependencies = dependencies::analyze_external_deps(&kb);
```

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| The library is not completely free of side effects. | `FileWalker::walk_and_project_hash` writes a checksum file to the directory you pass in. | Use `walk_paths` or `project_hash` if you want no writes. |
| `analyze_and_build` prints to stdout when `verbose` is true. | It uses `println!` directly. | Pass `false`, or call the passes yourself. |
| `analyze_and_build` skips the call graph above 100,000 files. | A memory guard. | Call the PRISM builders directly. |
| Two PRISM runs leave `kb.structure` in different states. | v1's pre-pass rewrites each call's `callee`; v2 does not. | Decide on one version before reading `calls`. |

## Related Docs

- [Overview](Overview.md): how the pieces fit together.
- [Knowledge Base Schema](Knowledge-Base-Schema.md): the types these functions return.
- [Analysis Passes](Analysis-Passes.md): what each `analyze` function computes.
