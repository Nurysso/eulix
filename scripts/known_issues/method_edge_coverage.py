#!/usr/bin/env python3
"""How many method nodes have at least one incoming call edge (PRISM-2).

Usage: method_edge_coverage.py [path/to/kb_call_graph.json]   (default: <repo>/.eulix/kb_call_graph.json)
Before the receiver-aware fix, this was 0 for every non-PHP language.
"""
import collections
import json
import os
import sys
from pathlib import Path

DEFAULT = Path(__file__).resolve().parents[2] / ".eulix" / "kb_call_graph.json"
cg = json.load(open(sys.argv[1] if len(sys.argv) > 1 else DEFAULT))
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
