# Eulix Embed
Eulix_Embed is a Rust-based knowledge base embedding generator that processes json created by [eulix_parser](https://github.com/Aelune/eulix/tree/main/eulix-parser) into semantic vector embeddings. It analyzes code structure, creates chunks, generates embeddings using ONNX models, and builds searchable indices for code understanding and retrieval.

## Architecture

### Core Components

1. **Main Pipeline** (`main.rs`)
   - Orchestrates the entire embedding generation workflow
   - Handles CLI argument parsing and execution
   - Provides progress reporting and statistics

2. **ONNX Backend** (`onnx_backend.rs`)
   - Manages ONNX Runtime for embedding generation
   - Supports CUDA (NVIDIA), ROCm (AMD), and CPU execution
   - Handles model downloading from HuggingFace Hub
   - Performs tokenization and mean pooling

3. **Embedder** (`embedder.rs`)
   - High-level embedding generation interface
   - Auto-detects available GPU acceleration
   - Supports batch and parallel processing
   - Includes fallback dummy backend for testing

4. **Chunker** (`chunker.rs`)
   - Converts knowledge base into processable chunks
   - Creates chunks for functions, classes, methods, and files
   - Adds contextual information and metadata
   - Assigns importance scores and tags

## Installation

### Prerequisites

- Rust 1.70 or later
- (Optional) CUDA 11+ or ROCm 5+ for GPU acceleration

### Build from Source

```bash
git clone https://github.com/Aelune/eulix
cd eulix/eulix_embed
# for rocm
cargo build --release --features rocm
# for cpu
cargo build --release
#  for cuda(havent tested it so may not work)
cargo build --release --features cuda
```
## Usage

### Basic Command

```bash
eulix_embed --kb-path knowledge_base.json --output ./embeddings --model sentence-transformers/all-MiniLM-L6-v2
```

### CLI Options

| Option | Short | Description | Default |
|--------|-------|-------------|---------|
| `--kb-path` | `-k` | Path to knowledge base JSON file | `knowledge_base.json` |
| `--output` | `-o` | Output directory for embeddings | `./embeddings` |
| `--model` | `-m` | HuggingFace model name or local path | `sentence-transformers/all-MiniLM-L6-v2` |
| `--help` | `-h` | Show help message | - |
| `--version` | `-v` | Show version | - |

### Supported Models

**Fast (Development/Testing)**
- `sentence-transformers/all-MiniLM-L6-v2` (384d, recommended for testing)

**Better Quality**
- `BAAI/bge-small-en-v1.5` (384d)
- `BAAI/bge-base-en-v1.5` (768d)

**Not Currently Working**
- `sentence-transformers/all-mpnet-base-v2`

## Pipeline Stages

### Stage 1: Load Knowledge Base

Reads the knowledge base JSON file and extracts:
- File structures with functions and classes
- Function signatures, parameters, and return types
- Call graphs and relationships
- Entry points and complexity metrics

### Stage 2: Process Code Chunks

Creates chunks of different types:
- **EntryPoint**: Application entry points (highest priority)
- **Function**: Regular functions with full context
- **Class**: Class overviews with attributes and methods
- **Method**: Class methods with inheritance context
- **File**: File-level summaries

Each chunk includes:
- Source code content
- File path and line numbers
- Language and complexity metrics
- Tags and importance scores

### Stage 3: Generate Embeddings

- Tokenizes text content
- Generates dense vector embeddings using ONNX models
- Applies mean pooling and normalization
- Processes in batches for efficiency

### Stage 4: Build Embedding Index

Creates a searchable index containing:
- Chunk IDs and types
- Original content
- Vector embeddings
- Metadata and relationships

### Stage 5: Create Context Index

Builds additional context structures:
- Tag-based lookups
- Relationship graphs
- Call hierarchies
- Entry point mappings

### Stage 6: Save Outputs

Generates multiple output files:
- `embeddings.json` - Full index in JSON format
- `embeddings.bin` - Compact binary format
- `vectors.bin` - Pure vector data
- `context.json` - Context and relationships

## GPU Acceleration

### Auto-Detection

The system automatically detects available GPU hardware:

```rust
// Automatically selects best backend
let generator = EmbeddingGenerator::new(model_name)?;
```

### Detection Logic

1. **CUDA (NVIDIA)**: Checks for `CUDA_PATH`, `/usr/local/cuda`, or `nvidia-smi`
2. **ROCm (AMD)**: Checks for `ROCM_PATH`, `/opt/rocm`, or `rocm-smi`
3. **CPU Fallback**: Used if no GPU detected

### Manual Backend Selection

You can specify backends programmatically:

```rust
let config = EmbedderConfig {
    backend: EmbeddingBackend::OnnxCuda,  // or OnnxRocm, OnnxCpu
    model_name: model_name.to_string(),
    ..Default::default()
};
let generator = EmbeddingGenerator::with_config(config)?;
```

## Error Handling

### Common Issues

**1. Knowledge Base Not Found**
```
[ERROR] Knowledge base file not found: knowledge_base.json
```
Solution: Provide correct path with `--kb-path`

**2. Model Download Failed**
```
Failed to download ONNX model
```
Solutions:
- Check internet connection
- Set `HF_HOME` environment variable
- Download model manually
- Use CPU backend: `--backend cpu`

**3. GPU Not Detected**
```
No GPU detected - using CPU backend
```
Solutions:
- Install CUDA/ROCm drivers
- Set `CUDA_PATH` or `ROCM_PATH` environment variables
- Verify with `nvidia-smi` or `rocm-smi`

## Performance

### Typical Speeds

- **GPU (CUDA/ROCm)**: 100-500 chunks/sec
- **CPU**: 10-50 chunks/sec

### Memory Usage

- Model size: 50-400 MB (depending on model)
- Embeddings: ~1.5 KB per chunk (384d)
- Total index: Varies by codebase size

### Optimization Tips

1. Use GPU acceleration when available
2. Choose smaller models for faster processing
3. Adjust batch sizes based on available memory
4. Use binary formats for faster loading

## Output Format

### embeddings.json
```json
{
  "model": "sentence-transformers/all-MiniLM-L6-v2",
  "dimension": 384,
  "total_chunks": 1500,
  "entries": [
    {
      "id": "function_id",
      "chunk_type": "function",
      "content": "...",
      "embedding": [0.123, ...],
      "metadata": {...}
    }
  ]
}
```

### context.json
```json
{
  "tags": {
    "async": ["chunk_id1", "chunk_id2"],
    "api": ["chunk_id3"]
  },
  "relationships": [
    {
      "from": "caller_id",
      "to": "callee_id",
      "type": "calls"
    }
  ]
}
```

## Testing

### Dummy Backend

For testing without model download:

```rust
let config = EmbedderConfig {
    backend: EmbeddingBackend::Dummy,
    ..Default::default()
};
```

Generates hash-based embeddings (not semantically meaningful).

## Troubleshooting

### HuggingFace Hub Issues

Set cache directory:
```bash
export HF_HOME=/path/to/cache
```

### ONNX Runtime Errors

Ensure model has ONNX format available:
- Check HuggingFace model page for `onnx/model.onnx`
- Some models require conversion

### Token Limit Exceeded

Chunks automatically truncated to 512 tokens (~2000 chars).

## Contributing

When extending the codebase:

1. Follow Rust naming conventions
2. Add error context with `anyhow::Context`
3. Include progress reporting for long operations
4. Write tests for new backends
5. Update documentation

## License

[LICENSE](../LICENSE)

## Version

Current version: 0.1.2


# Note on Development
This binary was primarily built (approximately 90%) by Claude, due to my limited experience with embeddings, GPU-based computation time to finish the eulix project. I contributed the architecture design, performed basic code fixes, and implemented minor performance optimizations. <br>
Any issues or ideas to improve this bin is appriciated and welcomed
