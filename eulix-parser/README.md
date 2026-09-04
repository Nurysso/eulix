# Eulix Parser (`eulix_parser`)

Fast, parallel, multi-language code intelligence and AST parser built in Rust using Tree-sitter. It parses multi-million LOC codebases, extracts rich semantic AST metadata, constructs cross-file call graphs via the **PRISM** engine, computes code complexity metrics, and generates structured knowledge bases for AI agents, RAG pipelines, and static analysis tools.

---

## Key Features

- **Multi-Language Tree-Sitter AST Parsing**:
  - Full native support for **C, C++, Go, Python, Rust, and TypeScript/TSX**.
  - Extracts functions, methods, classes, interfaces, structs, traits, imports, and variables.
  - Captures language-specific constructs (e.g., Go goroutines/channels, Rust traits/lifetimes, C++ templates/virtual tables, Python decorators/routes).
- **PRISM Call Graph Engine**:
  - Cross-file call resolution linking call sites to definition IDs.
  - **PRISMv1**: High-throughput $O(1)$ symbol-indexed call resolution.
  - **PRISMv2**: Scope-aware and class-aware call resolution resolving method overrides, inheritance, and shadowing.
  - Computes call count estimates, conditional execution branches, and reverse call graphs (`called_by`).
- **High-Performance Architecture**:
  - Multi-threaded processing powered by **Rayon** with dynamic work-chunking.
  - Custom memory allocation via **mimalloc** with secure features enabled.
  - SIMD acceleration and zero-copy / high-speed JSON serialization with **sonic-rs**.
  - OS-level I/O optimizations (`readahead`, `posix_fadvise`, sequential access hints, and automatic memory-mapping via `memmap2` for files > 10MB).
  - Benchmarked at **26M+ LOC in ~46s** parsing and **~5s** analysis on a 12-thread machine.
- **Security & Pattern Analysis**:
  - Detects language-specific security risks (e.g., unsafe memory operations, command injection, weak RNG, unsanitized inputs, XSS sinks).
  - Automatically identifies TODOs and architectural patterns (Layered, MVC, Microservices).
  - Discovers application entry points (HTTP routes, CLI commands, `main` functions).
- **Flexible Exclusions**:
  - Independent ignore engine using `.euignore` (GitIgnore syntax).
  - Built-in automatic filtering of build artifacts, caches, and virtual environments.

---

## Supported Languages

| Language | File Extensions | Extracted Constructs & Specific Metadata |
|---|---|---|
| **C** | `.c`, `.h` | Functions, structs, unions, enums, `#include`, `#define` macros, typedefs, inline assembly, POSIX threads, syscalls, `malloc`/`free` tracking, security patterns. |
| **C++** | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hxx` | Classes, structs, methods, constructors/destructors, operator overloads, templates, concepts, virtual/override/final methods, access specifiers, exception safety. |
| **Go** | `.go` | Packages, imports, functions, methods (pointer/value receivers), structs, interfaces, embedded types, goroutines, channels, `select`, `defer`, build tags, cgo, directives (`//go:embed`). |
| **Python** | `.py`, `.pyw`, `.pyi` | Functions, classes, async functions, decorators, dataclasses, class/static methods, properties, Flask/API routes, exception blocks, docstrings. |
| **Rust** | `.rs` | Functions, structs, enums, unions, traits, `impl` blocks (inherent & trait), generics, lifetimes, where-clauses, macros (`macro_rules!` and invocations), `unsafe` blocks, derives, `?` operator. |
| **TypeScript / TSX** | `.ts`, `.tsx` | Classes, interfaces, types, functions, arrow functions, methods, decorators, generic parameters, abstract classes, optional properties, DOM/XSS security flags. |

---

## Installation & Build

### Prerequisites

- **Rust 1.70+** with Cargo installed (via [rustup](https://rustup.rs))
- C/C++ build toolchain (for building Tree-sitter grammars)

### Building the Binary

```bash
cd eulix-parser

# Standard release build (optimized with LTO)
cargo build --release
```

The compiled binary will be located at:
- **`target/release/eulix_parser`**

---

## CLI Usage

```text
eulix_parser [OPTIONS] --root <ROOT> --prism <PRISM>
```

### Options & Flags

| Flag / Option | Short | Description | Default |
|---|---|---|---|
| `--root <ROOT>` | `-r` | Path to the root directory of the project to parse. | *(Required)* |
| `--prism <1\|2>` | `-p` | PRISM algorithm version: `1` (fast, direct) or `2` (precise, scope-aware). | *(Required)* |
| `--output <OUTPUT>` | `-o` | Base output path for the knowledge base JSON artifact. | `knowledge_base.json` |
| `--threads <N>` | `-t` | Number of worker threads for parallel parsing (`0` = auto-detect CPU cores). | `4` |
| `--languages <LANGS>`| `-l` | Comma-separated list of languages (`c`, `cpp`, `go`, `python`, `rust`, `typescript`) or `all`. | `all` |
| `--no-analyze` | | Skip the analysis phase (skips call graph, metrics, indices; only writes raw AST KB). | `false` |
| `--euignore <PATH>` | | Custom path to `.euignore` file (defaults to `<root>/.euignore`). | `None` |
| `--verbose` | `-v` | Enable detailed progress logging and phase metrics. | `false` |

### Examples

#### 1. Standard Analysis with PRISMv1 (Fast)
```bash
./target/release/eulix_parser \
  --root /path/to/project \
  --prism 1 \
  --output .eulix/kb.json \
  --threads 8 \
  --verbose
```

#### 2. Deep Analysis with PRISMv2 (Precise Call Graph)
```bash
./target/release/eulix_parser \
  --root /path/to/project \
  --prism 2 \
  --output .eulix/kb.json
```

#### 3. Targeted Language Parsing
```bash
./target/release/eulix_parser \
  --root /path/to/project \
  --languages rust,go \
  --prism 1 \
  --output out/kb.json
```

#### 4. High-Speed Raw Parsing (Skip Graph Analysis)
```bash
./target/release/eulix_parser \
  --root /path/to/huge_repo \
  --prism 1 \
  --no-analyze \
  --output out/kb.json
```

---

## Output Artifacts

When analysis completes, `eulix_parser` writes **eight synchronized JSON artifacts** into the directory containing `--output`:

| Artifact File | Description |
|---|---|
| `<base>.json` | **Knowledge Base**: Contains project metadata and the complete per-file AST breakdown (`functions`, `classes`, `imports`, `variables`, `todos`, `security_notes`, line ranges, and language-specific metadata). |
| `<base>_call_graph.json` | **Call Graph**: Complete serialized node array (`id`, `node_type`, `file`, `is_entry_point`) and edge array (`from`, `to`, `edge_type`, `conditional`, `call_site_line`). |
| `<base>_index.json` | **Fast Lookup Indices**: Inverted indices for `functions_by_name`, `functions_calling` (caller index), `functions_by_tag`, `types_by_name`, and `files_by_category`. |
| `<base>_summary.json` | **Project Summary**: Human-readable overview with language distribution, LOC metrics, detected architectural style, and key components. |
| `<base>_metrics.json` | **Complexity Metrics**: Summary statistics alongside the top-$K$ most complex functions ranked by cyclomatic complexity and importance. |
| `<base>_entry_points.json` | **Entry Points**: List of all discovered entry points, HTTP route handlers, and CLI commands. |
| `<base>_external_deps.json` | **External Dependencies**: External third-party packages, import frequencies, and the files consuming them. |
| `<base>_patterns.json` | **Patterns**: High-level structural conventions, naming schemes, and architectural patterns. |

> **Note**: If `--no-analyze` is passed, only `<base>.json` is written.

---

## Call Graph & The PRISM Engine

The parser includes a dedicated call-graph engine called **PRISM**. Detailed design documentation is available in [Call Graph Architecture](docs/callgraph.md).

### PRISMv1 (Direct / High-Throughput)
- Built for extreme parsing speed on massive repositories.
- Employs a parallel chunked node/edge extractor with Rayon.
- Uses an $O(1)$ global symbol-table lookup for short-name call resolution.
- Ideal when call relationships are needed quickly for high-level retrieval.

### PRISMv2 (Precise / Scope-Aware)
- Preserves full module, class, and method scoping.
- Resolves polymorphic method calls, class inheritance chains, and receiver types.
- Disambiguates common method names (e.g. `run()`, `init()`, `handle()`) across different types.

### Large-Repo Protection
- If a project exceeds **100,000 files**, call-graph generation is gracefully skipped to avoid memory exhaustion (OOM), ensuring the parser completes reliably.

---

## File Exclusion & `.euignore`

`eulix_parser` does **not** depend on `.gitignore` to avoid skipping files that developers want analyzed (such as vendored code or local build scripts). Instead, it uses an explicit exclusion system:

1. **Automatic Directory Exclusions**:
   - Version Control: `.git`
   - Eulix Internal: `.eulix`
   - Python: `__pycache__`, `.venv`, `venv`, `env`, `.env`, `.pytest_cache`, `.mypy_cache`, `.tox`, `*.egg-info`, `.eggs`
   - Node / Web: `node_modules`
   - Build / Output: `dist`, `build`, `target`, `.ipynb_checkpoints`

2. **Custom `.euignore`**:
   Place a `.euignore` file in your project root (or pass `--euignore <path>`). It uses standard GitIgnore glob syntax:
   ```gitignore
   # Ignore test fixtures and generated assets
   fixtures/
   generated/*.go
   vendor/
   ```

---

## Project Structure

```text
eulix-parser/
├── Cargo.toml               # Dependencies, release profiles (LTO, mimalloc, sonic-rs)
├── .euignore                # Default parser-level ignore rules
├── docs/
│   └── callgraph.md         # In-depth PRISM call graph documentation
└── src/
    ├── main.rs              # CLI entry point, thread pool setup, multi-file orchestration
    ├── struc/
    │   ├── mod.rs
    │   └── kb_struct.rs     # Core data structures & language-specific AST metadata definitions
    ├── parser/
    │   ├── mod.rs
    │   ├── language.rs      # Language detection (extensions, shebang, heuristics)
    │   ├── analyze.rs       # PRISM engine, call graph builder, indexer, pattern detector
    │   ├── c.rs             # C parser (Tree-sitter)
    │   ├── cpp.rs           # C++ parser (Tree-sitter)
    │   ├── go.rs            # Go parser (Tree-sitter)
    │   ├── python.rs        # Python parser (Tree-sitter)
    │   ├── rust.rs          # Rust parser (Tree-sitter)
    │   ├── typescript.rs    # TypeScript & TSX parser (Tree-sitter)
    │   └── parser_test.rs   # Parser integration tests
    └── utils/
        ├── mod.rs
        └── file_walker.rs   # Fast directory walker respecting .euignore & hardcoded filters
```

---

## Development & Verification

### Running Tests
```bash
cargo test
```

### Checking Lints & Formatting
```bash
cargo fmt --check
cargo clippy
```
