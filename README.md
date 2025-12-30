# Eulix

Eulix is a high-performance semantic code-intelligence system designed to analyze large codebases and generate rich, structured program knowledge. It provides fast code parsing, semantic extraction, and vector embeddings for search, navigation, and automated reasoning.

## Overview

Eulix consists of three specialized binaries:

* **CLI (Go):** User-facing command-line interface for running analyses, querying results, and managing configurations.
* **Parser (Rust):** High-throughput static analyzer capable of processing ~9 million lines of code in under 40 seconds on a single thread.
* **Embedder (Rust):** Vector-embedding generator built on *candle* and Hugging Face models, supporting both CUDA and ROCm acceleration.

## Features

### Parsing and Semantic Extraction

The parser produces detailed semantic data including:

* **Indexing:** Function, class, and symbol locations
* **Call Graphs:** Incoming and outgoing call relationships
* **Summaries:** Extracted docstrings and synthesized descriptions
* **Knowledge Base:** Fine-grained semantic details such as control-flow structures, try/except blocks, and cyclomatic complexity
* **Languages supported:** Python, Go, C

### Embedding Generation

The embedder supports multiple transformer models:

* `sentence-transformers/all-MiniLM-L6-v2` (384d, fast)
* `BAAI/bge-small-en-v1.5` (384d, strong performance)
* `BAAI/bge-base-en-v1.5` (768d, higher quality)
* `sentence-transformers/all-mpnet-base-v2` (768d, high quality)

Embeddings are generated using ONNX and downloaded via Hugging Face. Hardware acceleration is available for both CUDA and ROCm, with a dummy backend for testing and installation.

## Command Reference

### Main CLI (`eulix`)

```
Available Commands:
  analyze     Analyze codebase and generate knowledge base
  aspirine    Fix corrupted embeddings.bin and kb (testing utility)
  cache       Manage cache entries
  chat        Start interactive chat interface
  config      Manage eulix configuration
  glados      Validate knowledge base and embeddings integrity
  history     View query history interactively
  init        Initialize eulix in current directory
```

### Parser (`eulix_parser`)

Fast multi-language code parser with parallel processing support. Key options:

* `-r, --root <ROOT>`: Project root directory (required)
* `-o, --output <OUTPUT>`: Output file for knowledge base
* `-t, --threads <THREADS>`: Number of parsing threads (default: 4)
* `-l, --languages <LANGUAGES>`: Languages to parse (comma-separated or "all")
* `--no-analyze`: Skip analysis phase for faster parsing
* `--euignore <PATH>`: Custom ignore file path

### Embedder (`eulix_embed`)

Generate and query vector embeddings:

**Commands:**
* `embed`: Generate embeddings for knowledge base (default)
* `query`: Generate embedding for a query string

**Common Options:**
* `-m, --model <NAME>`: HuggingFace model name or local path
* `-f, --format <FORMAT>`: Output format (json or binary)

## Current Limitations

* Context window creation performs well in most cases but may struggle with certain function name patterns

## Documentation and Benchmarks

Comprehensive documentation and performance benchmarks will be added soon. The system is currently in active testing.
