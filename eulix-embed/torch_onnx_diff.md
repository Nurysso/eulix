# Eulix Embed: Engine Comparison (ONNX vs. PyTorch)

## Summary

When evaluated under **identical conditions** (same dataset, model, batch size, and dimension), the **ONNX Runtime engine** outperforms native **PyTorch** on AMD ROCm/HIP hardware, delivering a **~7.1% increase in embedding throughput** and reducing total run time by **~6.5%**.

---

## Direct Performance Comparison

> on large and very large corpus difference can be huge

| Metric                  | ONNX Engine (`-e onnx`) | PyTorch Engine (`-e torch`) | Delta / Comparison                         |
| ----------------------- | ----------------------- | --------------------------- | ------------------------------------------ |
| **Model**               | `all-mpnet-base-v2`     | `all-mpnet-base-v2`         | _Identical_                                |
| **Batch Size / Dim**    | 32 / 768d               | 32 / 768d                   | _Identical_                                |
| **Total Chunks**        | 951                     | 951                         | _Identical_                                |
| **KB Scan Time**        | 0.26s                   | **0.23s**                   | PyTorch slightly faster (-0.03s)           |
| **Embedding Time**      | **3.29s**               | 3.52s                       | **ONNX is 0.23s faster**                   |
| **Throughput**          | **289.2 chunks/sec**    | 269.9 chunks/sec            | **ONNX is 7.1% faster (+19.3 chunks/sec)** |
| **Total Pipeline Time** | **3.72s**               | 3.98s                       | **ONNX saves 0.26s overall (-6.5%)**       |

---

## Pros & Cons

### ONNX Engine (`-e onnx`)

- **Pros:**
- **Higher Throughput**: Faster inference engine (289.2 vs 269.9 chunks/sec) due to graph optimizations via `ROCMExecutionProvider`.
- **Lower Memory Footprint**: Smaller runtime startup and lower overall memory allocations.
- **Production Optimized**: Ideal for CLI tools, edge deployments, and high-concurrency environments.

- **Cons:**
- Requires models to be available in or exportable to ONNX format.
- Initialization adds minor setup overhead for cold starts.

---

### PyTorch Engine (`-e torch`)

- **Pros:**
- **Maximum Model Compatibility**: Native support for virtually any Hugging Face model out-of-the-box (no ONNX conversion required).
- **Slightly Faster Cold Scan/Setup**: Minimal initialization latency before inference begins (0.23s vs 0.26s KB scan).
- **Easier Debugging**: Standard Python/PyTorch stack simplifies local development and fine-tuning.

- **Cons:**
- **Slower Inference**: Higher runtime dynamic dispatch overhead leads to lower throughput.
- Larger overall framework dependency overhead.

---

## Quick Recommendation

- **Use ONNX (`-e onnx`)** as the default engine for production indexing, CLI tools, and batch jobs where maximum throughput is needed.
- **Use PyTorch (`-e torch`)** when working with newly released models, custom architecture experimentation, or when ONNX weights are unavailable.
