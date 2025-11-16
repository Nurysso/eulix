# Eulix Parser - Python AST Parser

Fast, parallel Python code parser using tree-sitter. Generates structured knowledge bases for code analysis.

## Features

- Fast parallel parsing with Rayon (5000+ LOC/s)
- Tree-sitter AST parsing
- Extracts functions, classes, imports, dependencies
- Detects security patterns and TODOs
- Builds call graphs and dependency graphs
- Calculates cyclomatic complexity
- Resource-friendly (max 4 threads)

## Installation

### Prerequisites

- Rust 1.70+ (install from https://rustup.rs)

### Build

```bash
# Development build (fast compilation)
cargo build

# Release build (optimized, recommended)
cargo build --release
```

Binary location:
- Debug: `target/debug/eulix-parser`
- Release: `target/release/eulix-parser`

## Usage

```bash
# Basic usage
eulix-parser \
  --root /path/to/project \
  --output knowledge_base.json

# With verbose output
eulix-parser \
  --root /path/to/project \
  --output kb.json \
  --verbose

# Custom thread count
eulix-parser \
  --root /path/to/project \
  --output kb.json \
  --threads 2

# With .euignore file
eulix-parser \
  --root /path/to/project \
  --output kb.json \
  --euignore .euignore
```

## Output Format

The parser generates a JSON file with this structure:

```json for parser
{
  "metadata": {
    "project_name": "myproject",
    "version": "1.0",
    "parsed_at": "2025-11-01T10:30:00Z",
    "languages": ["python"],
    "total_files": 47,
    "total_loc": 8432
  },
  "structure": {
    "src/auth/login.py": {
      "language": "python",
      "loc": 234,
      "imports": [...],
      "functions": [...],
      "classes": [...],
      "global_vars": [...],
      "todos": ["Add rate limiting"],
      "security_notes": ["Handles passwords"]
    }
  },
  "dependency_graph": {
    "nodes": [...],
    "edges": [...]
  },
  "entry_points": [...],
  "external_dependencies": [...]
}
```

## Quick Test

Run the provided test script:

```bash
chmod +x ../build_and_test.sh
../build_and_test.sh
```

This will:
1. Build the parser in release mode
2. Create a sample Python project
3. Parse the project
4. Show statistics and output location

## Performance

| Project Size | Parse Time | Throughput |
|-------------|------------|------------|
| 1k LOC      | ~0.2s      | 5000 LOC/s |
| 10k LOC     | ~1.5s      | 6600 LOC/s |
| 100k LOC    | ~15s       | 6600 LOC/s |

## What Gets Parsed

### Functions
- Name, signature, parameters, return type
- Docstrings
- Line numbers (start/end)
- Cyclomatic complexity
- Function calls made
- Decorators
- Async/sync status

### Classes
- Name, base classes
- Docstrings
- Line numbers
- Methods (same as functions)
- Attributes with type annotations

### Imports
- Module names
- Imported items
- Both `import X` and `from X import Y` formats

### Security Patterns
- Password handling
- eval/exec usage
- Dynamic imports
- System commands
- Pickle usage

### Dependencies
- From requirements.txt
- From pyproject.toml
- From setup.py

## Ignored Directories

By default, these are skipped:
- `.git/`
- `.eulix/`
- `__pycache__/`
- `.venv/`, `venv/`, `env/`
- `node_modules/`
- `.pytest_cache/`, `.mypy_cache/`, `.tox/`
- `dist/`, `build/`

Add `.euignore` (gitignore format) for custom exclusions.

## Development

### Project Structure
```
parser/
├── src/
│   ├── main.rs          # CLI entry point
│   ├── parser/
│   │   └── python.rs    # Python AST parser (core)
│   ├── kb/
│   │   ├── types.rs     # Data structures
│   │   └── builder.rs   # KB builder
│   └── utils/
│       └── file_walker.rs  # File discovery
└── Cargo.toml
```

### Run Tests
```bash
cargo test
```

### Format Code
```bash
cargo fmt
```

### Lint
```bash
cargo clippy
```

## Troubleshooting

### "Failed to load Python grammar"
- Make sure tree-sitter-python is in Cargo.toml
- Try `cargo clean && cargo build`

### "No Python files found"
- Check if .gitignore is excluding files
- Use `--verbose` to see what's being scanned
- Create a `.euignore` with custom rules

### Slow parsing
- Reduce threads: `--threads 2`
- Exclude large directories in `.euignore`
- Use release build (10x faster than debug)

### Out of memory
- Parse fewer files at once
- Reduce thread count
- Typical usage: ~5MB per 1k LOC

## Roadmap

- [ ] Incremental parsing (only changed files)
- [ ] Type inference
- [ ] Better docstring extraction
- [ ] JavaScript/TypeScript support
- [ ] More language support
