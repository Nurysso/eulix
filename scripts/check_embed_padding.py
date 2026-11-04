import sys, os, contextlib, io
# repo copy first, then $EULIX_EMBED_DIR, then the installed one
_repo = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "eulix-embed")
sys.path.insert(0, os.environ.get("EULIX_EMBED_DIR") or (_repo if os.path.isdir(_repo) else os.path.expanduser("~/.Eulix/eulix_embed")))
import numpy as np
from embedders.onnx_embed import EmbeddingGeneratorOnnx
with contextlib.redirect_stdout(io.StringIO()):
    e = EmbeddingGeneratorOnnx("BAAI/bge-base-en-v1.5", device="cpu")
texts = [
    "func resolveAPIKeyFromEnv(provider string) string { return os.Getenv(provider + \"_API_KEY\") }",
    "def chunk_file(path): return [line for line in open(path)]",
    "fn hover(&self, params: HoverParams) -> Option<Hover> { self.lookup(params) }",
    "How does the tool read the secret key for the language model provider",
    "func dotProduct(a, b []float32) float32 { var s float32; for i := range a { s += a[i]*b[i] }; return s }",
    "class CacheManager: stores previous answers keyed by question",
]
free = e._embed_batch(texts)
print("device", e.device, "output", e._target_output_name, e._output_kind, "inputs", e._input_names)
for L in (64, 128, 256, 512):
    pad = e._embed_batch(texts, fixed_len=L)
    same = (free * pad).sum(1)
    print(f"fixed_len={L:3}: cos(free, padded) per text = {np.round(same, 3)}")
def stats(x):
    s = x @ x.T; v = s[np.triu_indices(len(x), 1)]
    return f"min {v.min():.3f} mean {v.mean():.3f}"
print("pairwise free  :", stats(free))
print("pairwise padded:", stats(e._embed_batch(texts, fixed_len=256)))
