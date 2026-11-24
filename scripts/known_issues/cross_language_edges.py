#!/usr/bin/env python3
"""Count call edges whose two ends are in different languages (PRISM-4).

Usage: cross_language_edges.py [path/to/kb_call_graph.fb]   (default: <repo>/.eulix/kb_call_graph.fb)
JS/TS and C/C++ count as one language each. A project with no foreign-function
calls between its languages should report 0.
"""
import collections
import os
import sys
from pathlib import Path

LANG = {".py": "py", ".go": "go", ".rs": "rs", ".ts": "js", ".tsx": "js",
        ".js": "js", ".jsx": "js", ".mjs": "js", ".java": "java", ".c": "c",
        ".h": "c", ".cpp": "c", ".hpp": "c", ".cc": "c", ".php": "php"}

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
files = {n["id"]: n["file"] for n in cg["nodes"]}
lang_of = lambda node: LANG.get(os.path.splitext(files.get(node, ""))[1], "?")
calls = [e for e in cg["edges"] if e["edge_type"] == "call"]
cross = collections.Counter((lang_of(e["from"]), lang_of(e["to"]))
                            for e in calls if lang_of(e["from"]) != lang_of(e["to"]))
print(len(calls), "call edges,", sum(cross.values()), "cross-language")
for (a, b), n in cross.most_common():
    print(f"  {a} -> {b}: {n}")
