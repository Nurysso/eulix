#!/usr/bin/env python3
"""Count call edges whose two ends are in different languages (PRISM-4).

Usage: cross_language_edges.py [path/to/kb_call_graph.json]   (default: <repo>/.eulix/kb_call_graph.json)
JS/TS and C/C++ count as one language each. A project with no foreign-function
calls between its languages should report 0.
"""
import collections
import json
import os
import sys
from pathlib import Path

LANG = {".py": "py", ".go": "go", ".rs": "rs", ".ts": "js", ".tsx": "js",
        ".js": "js", ".jsx": "js", ".mjs": "js", ".java": "java", ".c": "c",
        ".h": "c", ".cpp": "c", ".hpp": "c", ".cc": "c", ".php": "php"}

DEFAULT = Path(__file__).resolve().parents[2] / ".eulix" / "kb_call_graph.json"
cg = json.load(open(sys.argv[1] if len(sys.argv) > 1 else DEFAULT))
files = {n["id"]: n["file"] for n in cg["nodes"]}
lang_of = lambda node: LANG.get(os.path.splitext(files.get(node, ""))[1], "?")
calls = [e for e in cg["edges"] if e["edge_type"] == "call"]
cross = collections.Counter((lang_of(e["from"]), lang_of(e["to"]))
                            for e in calls if lang_of(e["from"]) != lang_of(e["to"]))
print(len(calls), "call edges,", sum(cross.values()), "cross-language")
for (a, b), n in cross.most_common():
    print(f"  {a} -> {b}: {n}")
