# Eulix Parser - Architecture Documentation

**Deep-dive into the design, and internals of the high-performance code parser.**

---

## Table of Contents

1. [Overview](#overview)
2. [Design Philosophy](#design-philosophy)
3. [System Architecture](#system-architecture)
4. [Module Architecture](#module-architecture)
5. [Core Data Flow](#core-data-flow)
6. [Parser Implementation](#parser-implementation)
7. [Parallel Processing](#parallel-processing)
8. [Analysis Pipeline](#analysis-pipeline)
9. [Memory Management](#memory-management)
10. [Performance Optimization](#performance-optimization)
11. [Extension Points](#extension-points)
12. [Testing Strategy](#testing-strategy)
13. [Language Support Status](#language-support-status)

---

## Overview

Eulix Parser is a static code analysis tool that transforms source code into structured, queryable knowledge bases. It uses custom AST parsing and Rayon for parallel processing to achieve high throughput for codebase analysis.

### Goals

- **Performance**: Parse large codebases (Millions of LOC) efficiently
- **Accuracy**: Language-aware parsing with custom AST walkers
- **Completeness**: Extract all relevant code metadata (functions, classes, calls, imports)
- **Scalability**: Handle projects from 1K to 1M+ LOC
- **Extensibility**: Easy addition of new languages through modular parser design

### Non-Goals

- Real-time parsing (batch processing only)
- IDE integration (LSP server)
- Code modification or refactoring
- Runtime analysis or profiling

---

## Design Philosophy

### Core Principles

1. **Performance Through Parallelism**
   - Multi-threaded file walking
   - Parallel parsing with Rayon
   - Lock-free data structures where possible

2. **Accuracy Over Speed**
   - Full AST parsing (not regex)
   - Tree-sitter for robust parsing
   - Preserve all source metadata

3. **Separation of Concerns**
   - Parsing separate from analysis
   - File walking independent of parsing
   - KB building decoupled from output

4. **Memory Efficiency**
   - Stream processing where possible
   - Early drops of unused data
   - Configurable thread limits

5. **Extensible Design**
   - Language-agnostic core
   - Plugin-style language parsers
   - Trait-based abstractions

---

## System Architecture

### High-Level Flow

```
┌─────────────────┐
│   Source Code   │
│   (Directory)   │
└────────┬────────┘
         │
         ↓
  ┌──────────────┐
  │ File Walker  │ ← .euignore rules
  │ (parallel)   │
  └──────┬───────┘
         │
         ↓ List of files
  ┌──────────────┐
  │  Language    │ ← calls for specifc parser by checking file extension
  │  Detector    │
  └──────┬───────┘
         │
         ↓ (file, language) pairs
  ┌──────────────┐
  │   Parser     │
  │  (Rayon)     │ ← runs parser based on language detected
  └──────┬───────┘
         │
         ↓ Parsed file data
  ┌──────────────┐
  │ KB Builder   │ ← Build graphs
  └──────┬───────┘
         │
         ↓ KnowledgeBase
  ┌──────────────┐
  │ Serializer   │
  └──────┬───────┘
         │
    ┌────┴─────┐ // optional
    ↓          ↓
┌─────────┐ ┌──────────┐
│  kb.json│ │Analyzer  │
└─────────┘ └────┬─────┘
        ┌────────┴────┬───────────────┐
        ↓             ↓               ↓
     ┌──────────┐  ┌─────────┐  ┌──────────┐
     │index.json│  │summary  │  │Call graph│ //stops at 20k file to save memory and analysis faster
     └──────────┘  │.json    │  └──────────┘
                   └─────────┘
```

### Component Overview

| Component             | Input          | Output           | Purpose                  |
| --------------------- | -------------- | ---------------- | ------------------------ |
| **File Walker**       | Directory path | File list        | Discover source files    |
| **Language Detector** | File paths     | (file, language) | Detect language per file |
| **Parser**            | Source code    | Parsed data      | Extract AST information  |
| **KB Builder**        | Parsed data    | KnowledgeBase    | Build unified structure  |
| **Analyzer**          | KnowledgeBase  | Index/Summary    | Extract insights         |

---

---

## Core Data Flow

### Phase 1: File Discovery

**Module:** `utils/file_walker.rs`

**Purpose:** Discover all source files, respecting ignore rules.

```rust
// Input
root_path: PathBuf
ignore_rules: IgnoreRules
  ↓
// Process
1. Read .euignore file
2. Compile ignore patterns (gitignore format)
3. Walk directory tree (parallel with jwalk)
4. Filter by ignore rules
5. Filter by file extension
  ↓
// Output
Vec<PathBuf> (discovered files)
```

**Ignored by Default:**

```rust
            ".git/",
            ".eulix/",
            "node_modules/",
            "__pycache__/",
            ".venv/",
            "venv/",
            "target/",
            "dist/",
            "build/",
            "*.pyc",
            "*.pyo",
            "*.so",
            "*.dylib",
            "*.exe",
            "*.log",
            ".DS_Store"
```

### Phase 2: Language Detection

**Module:** `parser/language.rs`

**Purpose:** Detect programming language for each file.

```rust
// Input
Vec<PathBuf>
  ↓
// Process
For each file:
  1. Check file extension
  2. If ambiguous, check shebang
  3. If still unknown, peek content
  ↓
// Output
Vec<(PathBuf, Language)>
```

### Phase 3: Parsing

**Module:** `parser/python.rs` (and future language modules)

**Purpose:** Parse source code into structured data using tree-sitter AST.

```rust
// Input
(PathBuf, Language, source_code: String)
  ↓
// Process
1. Initialize tree-sitter parser
2. Parse source to AST
3. Walk AST and extract:
   - Functions (signature, params, docstring, calls)
   - Classes (methods, attributes, inheritance)
   - Imports (modules, items)
   - Global variables
   - TODOs and security patterns
4. Calculate complexity metrics
  ↓
// Output
FileData {
    path, language, loc,
    imports, functions, classes,
    global_vars, todos, security_notes
}
```

**Tree-Sitter AST Walking:**

- calculate_complexity
- Function Call Extraction
- Security Pattern Detection

### Phase 4: KB Building

**Module:** `kb/builder.rs`

**Purpose:** Aggregate parsed file data into unified knowledge base.

```rust
// Input
Vec<FileData>
  ↓
// Process
1. Collect all file data
2. Build dependency graph:
   - Extract function call relationships
   - Build import graph
   - Identify entry points
3. Extract external dependencies:
   - Parse requirements.txt
   - Parse pyproject.toml
   - Parse package.json
4. Build metadata (totals, stats)
  ↓
// Output
KnowledgeBase {
    metadata,
    structure: HashMap<path, FileData>,
    dependency_graph,
    entry_points,
    external_dependencies
}
```

### Phase 5: Analysis

**Module:** `parser/analyze.rs`

**Purpose:** Generate index and summary from knowledge base.

```rust
// Input
KnowledgeBase
  ↓
// Process (Index)
1. Build file index (path → stats)
2. Build function index (name → location)
3. Build class index (name → location)
4. Build import index (module → files)
5. List entry points
  ↓
// Output: Index
IndexData {
    files: HashMap<path, FileStats>,
    functions: HashMap<name, FunctionRef>,
    classes: HashMap<name, ClassRef>,
    imports: HashMap<module, Vec<path>>,
    entry_points: Vec<EntryPointRef>
}

// Process (Summary)
1. Generate overview stats
2. Identify modules/architecture
3. Rank key components by:
   - Call frequency (most called)
   - Complexity (highest cyclomatic)
   - Entry points
4. Aggregate dependencies
5. Calculate complexity stats
6. Collect security notes
7. Collect TODOs
  ↓
// Output: Summary
SummaryData {
    overview,
    architecture,
    key_components,
    dependencies,
    complexity,
    security,
    todos
}
```

---

## Parallel Processing

### Rayon Thread Pool

```rust
use rayon::prelude::*;

// Configure global thread pool
rayon::ThreadPoolBuilder::new()
    .num_threads(args.threads)
    .build_global()?;

// Parallel file parsing
let parsed_files: Vec<FileData> = discovered_files
    .into_par_iter()
    .filter_map(|path| {
        match parse_file(&path) {
            Ok(data) => Some(data),
            Err(e) => {
                eprintln!("Error parsing {}: {}", path.display(), e);
                None
            }
        }
    })
    .collect();
```

### Work Distribution

```
Main Thread
    ↓
Rayon Pool (4 workers)
    ├─→ Worker 1: files[0..12]
    ├─→ Worker 2: files[13..25]
    ├─→ Worker 3: files[26..37]
    └─→ Worker 4: files[38..47]
    ↓
Collect Results (thread-safe)
```

### Load Balancing

**Work Stealing Algorithm:**

- Each worker has a deque of tasks
- Idle workers "steal" from busy workers
- Automatic load balancing

**Optimal Thread Count:**

- Yet to be implemented
- Currently let users define threads when eulix-parser bin is called if not defined then uses 4 threads

```rust
fn optimal_thread_count(file_count: usize) -> usize {
    let cpu_count = num_cpus::get();

    // For small projects, don't over-parallelize
    if file_count < 20 {
        return 1;
    }

    // Cap at 4 to avoid I/O contention
    std::cmp::min(cpu_count, 4)
}
```

---

## Language Support Status

| Language   | Extension                  | Parse | Call Graph | Complexity | Status        |
| ---------- | -------------------------- | ----- | ---------- | ---------- | ------------- |
| Python     | `.py`                      | ✅    | ✅         | ✅         | Implemented   |
| Go         | `.go`                      | ✅    | ✅         | ✅         | Implemented   |
| C          | `.c` `.h`                  | ✅    | ✅         | ✅         | Implemented   |
| C++        | `.cpp` `.hpp` `.cc` `.cxx` | ✅    | ✅         | ✅         | Implemented   |
| Rust       | `.rs`                      | ✅    | ✅         | ✅         | Implemented   |
| TypeScript | `.ts` `.tsx`               | ✅    | ✅         | ✅         | Implemented   |
| JavaScript | `.js` `.jsx`               | ❌    | ❌         | ❌         | Returns error |

> **Note:** JavaScript is detected but not yet implemented. Files of this type cause `parse_file()` to return `Err(...)`, which is silently counted as a parse failure. The file is skipped without user warning unless `--verbose` is passed.

### Language Detection Strategy

The parser uses a multi-strategy language detection system in `parser/language.rs`:

1. **Extension-based detection** (fastest) - Checks file extensions against known patterns
2. **Filename patterns** - Handles special files like `Makefile`, `go.mod`, `Cargo.toml`
3. **Shebang detection** - Reads first line for `#!` interpreter directives
4. **Content analysis** - Falls back to heuristic analysis of first 50 lines for ambiguous files

This layered approach ensures accurate language detection even for files without clear extensions or with unusual naming conventions.

---

## Memory Management

### Memory Layout

```
Heap During Parsing:
┌────────────────────────────────────┐
│ File list (Vec<PathBuf>) ~1MB      │
├────────────────────────────────────┤
│ Source code buffers (temp)         │
│   Per-thread: ~100KB               │
│   Total: ~400KB (4 threads)        │
├────────────────────────────────────┤
│ Parsed FileData (accumulated)      │
│   ~50KB per file                   │
│   Total: ~2.5MB (50 files)         │
├────────────────────────────────────┤
│ KB structure ~10MB                 │
└────────────────────────────────────┘

Peak Memory: ~15-20MB for typical project
```
