#!/usr/bin/env python3
"""How many method nodes have at least one incoming call edge (PRISM-2).

Usage: method_edge_coverage.py [path/to/kb_call_graph.fb]   (default: <repo>/.eulix/kb_call_graph.fb)
Before the receiver-aware fix, this was 0 for every non-PHP language.
"""
import collections
import os
import sys
from pathlib import Path

def load_call_graph(path):
    """kb_call_graph.fb, read through bench/lib/kbfb.py."""
    import importlib.util

    kbfb_path = Path(__file__).resolve().parents[2] / "bench" / "lib" / "kbfb.py"
    spec = importlib.util.spec_from_file_location("eulix_bench_kbfb", kbfb_path)
    kbfb = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(kbfb)
    return kbfb.load_call_graph(Path(path))


DEFAULT = Path(__file__).resolve().parents[2] / ".eulix" / "kb_call_graph.fb"
cg = load_call_graph(sys.argv[1] if len(sys.argv) > 1 else DEFAULT)
targets = {e["to"] for e in cg["edges"] if e["edge_type"] == "call"}
per_ext = collections.defaultdict(lambda: [0, 0])  # ext -> [methods, with callers]
for n in cg["nodes"]:
    if n["node_type"] != "method":
        continue
    ext = os.path.splitext(n["file"])[1] or "?"
    per_ext[ext][0] += 1
    per_ext[ext][1] += n["id"] in targets
for ext, (total, hit) in sorted(per_ext.items()):
    print(f"{ext:8} {hit:5}/{total:<5} methods have a caller")
