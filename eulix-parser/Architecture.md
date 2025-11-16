# Eulix Parser - Architecture Documentation

**deep-dive into the design, and internals of the high-performance code parser.**

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

---

## Overview

Eulix Parser is a static code analysis tool that transforms source code into structured, queryable knowledge bases. It uses tree-sitter for accurate AST parsing and Rayon for parallel processing to achieve 5000+ LOC/s throughput.

### Goals

- **Performance**: Parse large codebases (100K+ LOC) in seconds
- **Accuracy**: Use tree-sitter AST for language-aware parsing
- **Completeness**: Extract all relevant code metadata
- **Scalability**: Handle projects from 1K to 1M+ LOC
- **Extensibility**: Easy addition of new languages

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
  │  Language    │
  │  Detector    │
  └──────┬───────┘
         │
         ↓ (file, language) pairs
  ┌──────────────┐
  │   Parser     │
  │  (Rayon)     │ ← Python, JS, etc.
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
    ┌────┴─────┐
    ↓          ↓
┌─────────┐ ┌──────────┐
│  kb.json│ │Analyzer  │
└─────────┘ └────┬─────┘
               ┌──┴───┐
               ↓      ↓
         ┌─────────┐ ┌─────────┐
         │index.json│ │summary  │
         └─────────┘ │.json    │
                     └─────────┘
```

### example kb.json
``` json
{
  "metadata": {
    "project_name": "string",
    "version": "string",
    "parsed_at": "string (ISO 8601 timestamp)",
    "languages": ["string"],
    "total_files": "number",
    "total_loc": "number",
    "total_functions": "number",
    "total_classes": "number",
    "total_methods": "number"
  },
  "structure": {
    "path/to/file.py": {
      "language": "string",
      "loc": "number",
      "imports": [
        {
          "module": "string",
          "items": ["string"],
          "type": "string (external | internal)"
        }
      ],
      "functions": [
        {
          "id": "string (file:function_name)",
          "name": "string",
          "signature": "string",
          "params": [
            {
              "name": "string",
              "type_annotation": "string",
              "default_value": "string | null"
            }
          ],
          "return_type": "string",
          "docstring": "string",
          "line_start": "number",
          "line_end": "number",
          "calls": [
            {
              "callee": "string",
              "defined_in": "string (file path) | null",
              "line": "number",
              "args": ["string"],
              "is_conditional": "boolean",
              "context": "string (if | else | loop | try | unconditional)"
            }
          ],
          "called_by": [
            {
              "function": "string",
              "file": "string",
              "line": "number"
            }
          ],
          "variables": [
            {
              "name": "string",
              "var_type": "string | null",
              "scope": "string (param | local | global)",
              "defined_at": "number | null",
              "transformations": [
                {
                  "line": "number",
                  "via": "string (function name)",
                  "becomes": "string (new variable name)"
                }
              ],
              "used_in": ["string (function names)"],
              "returned": "boolean"
            }
          ],
          "control_flow": {
            "complexity": "number",
            "branches": [
              {
                "branch_type": "string (if | elif | else | match)",
                "condition": "string",
                "line": "number",
                "true_path": {
                  "calls": ["string"],
                  "returns": "string | null",
                  "raises": "string | null"
                },
                "false_path": {
                  "calls": ["string"],
                  "returns": "string | null",
                  "raises": "string | null"
                } | null
              }
            ],
            "loops": [
              {
                "loop_type": "string (for | while)",
                "condition": "string",
                "line": "number",
                "calls": ["string"]
              }
            ],
            "try_blocks": [
              {
                "line": "number",
                "try_calls": ["string"],
                "except_clauses": [
                  {
                    "exception_type": "string",
                    "line": "number",
                    "calls": ["string"]
                  }
                ],
                "finally_calls": ["string"]
              }
            ]
          },
          "exceptions": {
            "raises": ["string (exception types)"],
            "propagates": ["string (exception types)"],
            "handles": ["string (exception types)"]
          },
          "complexity": "number",
          "is_async": "boolean",
          "decorators": ["string"],
          "tags": ["string"],
          "importance_score": "number (0-1)"
        }
      ],
      "classes": [
        {
          "id": "string (file:class_name)",
          "name": "string",
          "bases": ["string (base class names)"],
          "docstring": "string",
          "line_start": "number",
          "line_end": "number",
          "methods": ["Function objects (same as functions array)"],
          "attributes": [
            {
              "name": "string",
              "type_annotation": "string",
              "value": "string | null"
            }
          ],
          "decorators": ["string"]
        }
      ],
      "global_vars": [
        {
          "name": "string",
          "type_annotation": "string",
          "value": "string | null",
          "line": "number"
        }
      ],
      "todos": [
        {
          "line": "number",
          "text": "string",
          "priority": "string (high | medium | low)"
        }
      ],
      "security_notes": [
        {
          "note_type": "string",
          "line": "number",
          "description": "string"
        }
      ]
    }
  },
  "call_graph": {
    "nodes": [
      {
        "id": "string (file:identifier)",
        "node_type": "string (function | method | class)",
        "file": "string",
        "is_entry_point": "boolean",
        "call_count_estimate": "number"
      }
    ],
    "edges": [
      {
        "from": "string (node id)",
        "to": "string (node id)",
        "edge_type": "string (calls | inherits | uses)",
        "conditional": "boolean",
        "call_site_line": "number"
      }
    ]
  },
  "dependency_graph": {
    "nodes": [
      {
        "id": "string",
        "node_type": "string (file | module | package)",
        "name": "string"
      }
    ],
    "edges": [
      {
        "from": "string (node id)",
        "to": "string (node id)",
        "edge_type": "string (imports | depends_on)"
      }
    ]
  },
  "indices": {
    "functions_by_name": {
      "function_name": ["string (file:line)"]
    },
    "functions_calling": {
      "callee_name": ["string (caller identifiers)"]
    },
    "functions_by_tag": {
      "tag_name": ["string (function identifiers)"]
    },
    "types_by_name": {
      "type_name": ["string (file:line)"]
    },
    "files_by_category": {
      "category_name": ["string (file paths)"]
    }
  },
  "entry_points": [
    {
      "entry_type": "string (api_endpoint | cli_command | main)",
      "path": "string | null",
      "function": "string",
      "handler": "string",
      "file": "string",
      "line": "number",
      "methods": ["string (HTTP methods)"] | null
    }
  ],
  "external_dependencies": [
    {
      "name": "string",
      "version": "string | null",
      "source": "string",
      "used_by": ["string (file paths)"],
      "import_count": "number"
    }
  ],
  "patterns": {
    "naming_convention": "string",
    "structure_type": "string",
    "architecture_style": "string (layered | microservices | mvc) | null"
  }
}
```

### Component Overview

| Component | Input | Output | Purpose |
|-----------|-------|--------|---------|
| **File Walker** | Directory path | File list | Discover source files |
| **Language Detector** | File paths | (file, language) | Detect language per file |
| **Parser** | Source code | Parsed data | Extract AST information |
| **KB Builder** | Parsed data | KnowledgeBase | Build unified structure |
| **Analyzer** | KnowledgeBase | Index/Summary | Extract insights |

---

## Module Architecture

```
eulix-parser/
├── src/
│   ├── main.rs              → CLI entry point
│   │                          - Argument parsing (clap)
│   │                          - Orchestrates pipeline
│   │                          - Progress reporting
│   │
│   ├── parser/              → Parsing logic
│   │   ├── mod.rs            - Module exports
│   │   ├── language.rs       - Language detection & dispatch
│   │   ├── python.rs         - Python AST parser
│   │   └── analyze.rs        - Post-parse analysis
│   │
│   ├── kb/                  → Knowledge base types
│   │   ├── mod.rs            - Module exports
│   │   ├── types.rs          - Core data structures
│   │   └── builder.rs        - KB construction
│   │
│   └── utils/               → Utilities
│       ├── mod.rs            - Module exports
│       ├── file_walker.rs    - File system traversal
│       └── ignore.rs         - .euignore handling
│
└──Cargo.toml               → Dependencies
```

### Module Dependency Graph

```
main.rs
  ├─→ utils::file_walker     → Vec<PathBuf>
  ├─→ utils::ignore          → IgnoreRules
  ├─→ parser::language       → Language detection
  │     └─→ parser::python   → FileData
  ├─→ kb::builder            → KnowledgeBase
  │     └─→ kb::types        → Data structures
  └─→ parser::analyze        → Index + Summary
```

---
### schema
(schema)[schema.txt]

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
const DEFAULT_IGNORES: &[&str] = &[
    ".git/",
    ".eulix/",
    "__pycache__/",
    "*.pyc",
    ".venv/", "venv/", "env/",
    "node_modules/",
    ".pytest_cache/", ".mypy_cache/", ".tox/",
    "dist/", "build/", "*.egg-info/",
];
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

## Memory Management My asumption

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
