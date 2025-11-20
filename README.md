# Eulix

Eulix is a semantic code-intelligence system designed to analyze large codebases quickly and generate rich, structured program knowledge. It provides high-performance code parsing, semantic extraction, and vector embeddings suitable for search, navigation, and automated reasoning.

## Overview

Eulix consists of three binaries:

* **CLI (Go):** User-facing command-line interface for running analyses and querying results.
* **Parser (Rust):** High-throughput static analyzer capable of processing ~9 million lines of code in under 40 seconds.
* **Embedder (Rust):** Vector-embedding generator built on *candle* and Hugging Face models.

## Features

### Parsing and Semantic Extraction

The parser produces detailed semantic data, including:

* **Indexing:** Function, class, and symbol locations.
* **Call Graphs:** Incoming and outgoing call relationships.
* **Summaries:** Extracted docstrings and synthesized descriptions.
* **Knowledge Base:** Fine-grained semantic details such as control-flow structures, try/except blocks, and cyclomatic complexity.

### Embedding Generation

The embedder supports multiple transformer models:

* `sentence-transformers/all-MiniLM-L6-v2` (384d, fast)
* `BAAI/bge-small-en-v1.5` (384d, strong performance)
* `BAAI/bge-base-en-v1.5` (768d, higher quality)
* `sentence-transformers/all-mpnet-base-v2` (768d, high quality)

Embeddings are generated using candle and can be integrated into search pipelines, ranking systems, or downstream ML tasks.

## Current Limitations

Due to couple error in setting cudarc, only CPU builds are currently supported for the embedder. GPU acceleration will be enabled once upstream support stabilizes.
