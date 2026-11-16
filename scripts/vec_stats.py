"""Stats for a quantized vector file. Usage: vec_stats.py [path/to/embeddings.bin]  (default: <repo>/.eulix/embeddings.bin)"""
import struct, sys, numpy as np
from pathlib import Path
path = sys.argv[1] if len(sys.argv) > 1 else Path(__file__).resolve().parent.parent / ".eulix" / "embeddings.bin"
b = open(path, 'rb').read(); o = 8
n = struct.unpack_from('<I', b, o)[0]; o += 4; model = b[o:o+n].decode(); o += n
cnt, dim = struct.unpack_from('<II', b, o); o += 8; q = b[o]; o += 1
rec = np.frombuffer(b, dtype=[('s', '<f4'), ('q', 'i1', (dim,))], count=cnt, offset=o)
x = rec['q'].astype(np.float32) * rec['s'][:, None]
x /= np.linalg.norm(x, axis=1, keepdims=True)
print(model, cnt, dim, 'quantized', q, 'scale range', rec['s'].min(), rec['s'].max())
s = x @ x.T; iu = np.triu_indices(cnt, 1); v = s[iu]
print('pairwise cos: mean %.3f  p5 %.3f  p50 %.3f  p95 %.3f  negative %.1f%%' % (v.mean(), *np.percentile(v, [5, 50, 95]), 100 * (v < 0).mean()))
m = x.mean(0); print('norm of mean vector %.3f (near 1 = all rows point the same way)' % np.linalg.norm(m))
print('int8 usage: min %d max %d, share of |q|=127: %.3f' % (rec['q'].min(), rec['q'].max(), (np.abs(rec['q']) == 127).mean()))
