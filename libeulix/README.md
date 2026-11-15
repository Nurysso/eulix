# libeulix

> The analysis engine inside Eulix: parses nine languages into a structured knowledge base with a project-wide call graph.

| Language | Version | Status | Docs |
| :-- | :-- | :-- | :-- |
| Rust | 0.9.0 | Stable, on [crates.io](https://crates.io/crates/libeulix) | [docs/libeulix](../docs/libeulix/Overview.md) |

`libeulix` turns source text into structured code data: files, symbols, relationships, call graphs, project metrics, entry points and dependencies. It is built on Tree-sitter for large, mixed-language repositories, where re-parsing from scratch for every query is too expensive. Parse once, then navigate many times.

It powers [`eulix-parser`](../eulix-parser/) and [`eulix-lsp`](../eulix-lsp/), and works just as well as a standalone library: its output is a useful structured representation of a repository on its own.

## Install

```bash
cargo add libeulix
```

To use the copy in this repository instead of the crates.io release:

```toml
[dependencies]
libeulix = { path = "../libeulix", version = "0.9.0" }
```

Requires Rust stable. The Tree-sitter grammar crates need a C/C++ compiler at build time.

## Usage

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
use std::collections::BTreeMap;
use std::path::PathBuf;
use libeulix::grammar_files;
use libeulix::analyze::call_graph::prism_v2;
use libeulix::struc::kb_struct::FileData;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let files: Vec<PathBuf> = vec![
        "src/lib.rs".into(),
        "src/main.rs".into(),
    ];

    let mut structure: BTreeMap<String, FileData> = BTreeMap::new();
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

### Run the analysis passes

`analyze_and_build` still works, but it is **deprecated since 0.9.0** and will be removed in 1.0.0. Call the passes directly instead:

```rust
use libeulix::analyze::call_graph::prism_v2;
use libeulix::analyze::{dependencies, entry_points, patterns};

// `kb` is a KnowledgeBase whose `structure` is already filled in.
kb.call_graph = prism_v2::build_call_graph_v2(&kb.structure);
prism_v2::populate_called_by_from_graph(&mut kb);
kb.patterns = patterns::detect_patterns(&kb);
kb.entry_points = entry_points::find_entry_points(&kb);
kb.external_dependencies = dependencies::analyze_external_deps(&kb);
```

## How it works

```text
source text
    │
    ▼
Tree-sitter syntax parsing
    ├── functions / methods
    ├── classes / structs / traits / interfaces
    ├── imports / dependencies
    ├── docstrings / parameters
    └── source line ranges
    │
    ▼
FileData (one per file)
    │  merge many FileData
    ▼
relationship analysis (PRISM)
    ├── resolved call graph
    ├── reverse call graph
    ├── inheritance / type relationships
    ├── entry points
    ├── indices
    ├── patterns
    └── metrics
    │
    ▼
KnowledgeBase (structured, serializable)
```

Tree-sitter is the syntax foundation; `libeulix` adds repository-level relationship extraction on top. The result is a value you can query directly, serialize to JSON, or feed into a retrieval or navigation layer without reparsing the repository.

## API overview

Everything is a free function on a module. There is no engine singleton and no global state: the crate is designed to be embedded.

| Function | Purpose |
| :-- | :-- |
| `grammar_files::parse_source(path, src)` | Parse one in-memory source file into a `FileData` |
| `grammar_files::language::Language::detect(path)` | Map a path to a language |
| `analyze::call_graph::prism_v1::build_call_graph(&structure)` | Fast, name-based call graph (run `resolve_call_locations` first) |
| `analyze::call_graph::prism_v2::build_with(&structure, &opts)` | Scope-aware call graph |
| `analyze::entry_points::find_entry_points(&kb)` | Entry points (`main`, routes, CLI commands) |
| `analyze::dependencies::analyze_external_deps(&kb)` | External dependency extraction |
| `analyze::patterns::detect_patterns(&kb)` | Naming, layout and architecture hints |
| `analyze::indices::generate_indices_view(&kb)` | Fast lookup indices |
| `analyze::metrics::generate_metrics(&kb, k)` | Top-`k` functions by complexity |
| `analyze::summary::generate_summary(&kb)` | Project summary |
| `analyze::pipeline::analyze_and_build(kb, verbose, prism)` | Runs the call graph, patterns, entry points and dependencies. Deprecated. |
| `struc::kb_struct::*` | The `FileData` / `KnowledgeBase` schema |

`structure`, the argument to the call-graph builders, is a `BTreeMap<String, FileData>`. The two top-level types are:

- **`FileData`**: one file's parsed content: functions, classes, methods, imports, docstrings, and per-function calls with line numbers.
- **`KnowledgeBase`**: a map of relative path to `FileData`, plus metadata and everything the analysis passes derive from it.

Both are `Serialize` / `Deserialize`, so they round-trip through JSON. Full API: [Public API](../docs/libeulix/Public-API.md).

## Supported languages

| Language | Extensions | Status |
| :-- | :-- | :-- |
| C | `.c`, `.h` | Stable |
| C++ | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hxx` | Stable |
| Go | `.go` | Stable |
| Python | `.py`, `.pyw`, `.pyi` | Stable |
| Rust | `.rs` | Stable |
| TypeScript | `.ts`, `.mts`, `.cts` | Stable |
| JavaScript | `.js`, `.mjs`, `.cjs` | Needs testing |
| Java | `.java` | Needs testing |
| PHP | `.php`, `.phtml`, `.inc` | Needs testing |

`.jsx` and `.tsx` files are detected as languages but currently rejected by `parse_source`. Ruby is not supported.

Language support is more than syntax support. Some features make relationship analysis much harder than parsing: dynamic dispatch, reflection, generated code, macros and runtime metaprogramming. The parser extracts what is there; PRISM resolves what can reasonably be resolved.

## PRISM

PRISM is the relationship engine for large-scale, approximate call-graph resolution. It exists to recover useful code relationships without making whole-program analysis prohibitively expensive. Depending on the version, it uses symbol metadata, scope information, class information, inheritance relationships and language-aware heuristics to connect call sites to their likely definitions.

This is retrieval-oriented analysis, not a compiler or a proof system. PRISM gives useful structural context at repository scale and accepts that dynamic dispatch, reflection and generated code make perfect resolution impossible. Treat its edges as navigation evidence, not proof of runtime behaviour.

### PRISM v1

The simpler, high-throughput path: fast symbol-based resolution with broad call-graph coverage at low cost.

```rust
use libeulix::analyze::call_graph::prism_v1;
// Rewrites each call's callee to the id of the first definition with that
// name. Without it, bare call names don't resolve.
prism_v1::resolve_call_locations(&mut kb);
let cg = prism_v1::build_call_graph(&kb.structure);
```

### PRISM v2

The scope-aware path. It keeps more information about modules, classes, methods, inheritance, shadowing and receiver context, so common ambiguous calls resolve more accurately than with a global name lookup. See [PRISM v2](../docs/libeulix/PRISM-V2.md).

```rust
use libeulix::analyze::call_graph::{prism_v2, prism_v2::CallGraphOptions};

let opts = CallGraphOptions {
    max_ambiguous: 8,
    strict_imports: true,
};
let cg = prism_v2::build_with(&structure, &opts);
```

Call-graph edges are labelled (`call` or `inheritance`) and carry a call-site line and a conditional flag.

## Design goals

1. **Parse large repositories quickly.** A 30M+ line repository should run on consumer hardware, not only on a workstation.
2. **Keep the output useful.** Other tools consume it, so stable identifiers and useful relationships matter as much as raw throughput.
3. **Prefer cheap approximations when they are good enough.** For code navigation, a fast and useful relationship beats an expensive attempt at perfect resolution.
4. **Stay embeddable.** No global state, no required runtime, no required tokio. Every public function takes its input by reference and returns owned data. Parallelism is opt-in through the caller's own rayon pool.

## Research

PRISM is also the subject of ongoing research into one question: how much useful structural information can be recovered from a large repository without paying for full program analysis? The work covers identity-based resolution, scope-aware relationships, class and inheritance information, and language-aware heuristics. It does not compete with a compiler's type system; it aims to make repository-scale navigation better. See [PRISM v2 methodology](../docs/libeulix/Research/PRISM-V2-Methodology.md).

## Development

```bash
cargo test
cargo fmt --check
cargo clippy
```

## Related components

| Component | Language | Role |
| :-- | :-- | :-- |
| [libeulix](../libeulix/) | Rust | Analysis engine: parsing, PRISM call graphs, indexes |
| [eulix-parser](../eulix-parser/) | Rust | CLI that runs libeulix over a repository and writes `.eulix/` |
| [eulix-embed](../eulix-embed/) | Python | Embedding pipeline for the ETIYA engine |
| [eulix-cli](../eulix-cli/) | Go | The `eulix` binary: retrieval engines, MCP server, chat |
| [eulix-lsp](../eulix-lsp/) | Rust | Language server built on libeulix (beta) |
| [vizEulize](../vizEulize/) | HTML/JS | Call-graph explorer in your browser |

## License

Apache 2.0, like every Eulix component. See [LICENSE](../LICENSE).
