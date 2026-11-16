import sys, os, contextlib, io
# repo copy first, then $EULIX_EMBED_DIR, then the installed one
_repo = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "eulix-embed")
sys.path.insert(0, os.environ.get("EULIX_EMBED_DIR") or (_repo if os.path.isdir(_repo) else os.path.expanduser("~/.Eulix/eulix_embed")))
import numpy as np
from embedders.onnx_embed import EmbeddingGeneratorOnnx
texts = [
    "func resolveAPIKeyFromEnv(provider string) string { return os.Getenv(provider + \"_API_KEY\") }",
    "def chunk_file(path): return [line for line in open(path)]",
    "fn hover(&self, params: HoverParams) -> Option<Hover> { self.lookup(params) }",
    "How does the tool read the secret key for the language model provider",
    "func dotProduct(a, b []float32) float32 { var s float32; for i := range a { s += a[i]*b[i] }; return s }",
    "class CacheManager: stores previous answers keyed by question",
] * 11  # 66 texts: more than one GPU batch of 64
out = {}
for dev in ("cpu", "rocm"):
    with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
        e = EmbeddingGeneratorOnnx("BAAI/bge-base-en-v1.5", device=dev)
    out[dev, None] = np.concatenate([e._embed_batch(texts[i:i+e.batch_size]) for i in range(0, len(texts), e.batch_size)])
    out[dev, 128] = np.concatenate([e._embed_batch(texts[i:i+e.batch_size], fixed_len=128) for i in range(0, len(texts), e.batch_size)])
    print(dev, "providers", e.session.get_providers(), "batch", e.batch_size, "bucketing", e.use_bucketing)
ref = out["cpu", None]
for k, v in out.items():
    c = (ref * v).sum(1)
    s = v @ v.T; p = s[np.triu_indices(len(v), 1)]
    print(f"{k[0]:4} fixed_len={k[1]!s:4}: cos vs CPU min {c.min():.3f} mean {c.mean():.3f} | pairwise min {p.min():.3f} | norm {np.linalg.norm(v,axis=1).mean():.3f}")
