# Changelog

## Eulix_embed v0.4.2 - 2026-11-16

Changes since v0.4.1, the version that shipped with Eulix v0.8.3.

### Changed

- The default model is `BAAI/bge-base-en-v1.5` (#143).
- The ONNX and Torch embedding pipelines share one base class (#143).
- `--help` is corrected: the engine default is `onnx`, `rocm` is listed for `-d`, and the server's default model is right. The README no longer lists the nonexistent `--no-comments`/`--save-json` and describes the binary format with the quantized byte and scales (#157).

### Fixed

- **Throughput:** token lengths were measured with padding and truncation on, so every chunk reported its batch's maximum. 88% of chunks landed in the 512 bucket, which forced the smallest batch size and the most padding: 78 chunks/s against 550 before. Lengths are now measured without padding or truncation (#143).
- **Memory:** `generate_vectors_streaming` buffered every vector before the first yield, so peak RAM grew with the corpus. It yields per batch again, in both the ONNX and the Torch pipeline (#143).
- **Torch on CPU:** the Torch backend didn't start on CPU-only Torch builds ("Torch not compiled with CUDA enabled"). The CUDA memory fraction is now set only when CUDA is available (#119).
- **Query protocol:** the ONNX embedder printed "Using ONNX-RUNTIME" to stdout, where `query` writes its JSON and binary responses, so they couldn't be parsed. The banner goes to stderr now (#119).

### Security

- Model loading blocks `trust_remote_code`, closing a remote code execution path (#157).

_Earlier releases are in the root [changelog](../changelog.md)._
