# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Read eulix_parser's FlatBuffers outputs with the standard library alone.

Uses the vendored pure-Python runtime (bench/vendor/flatbuffers) and the code
generated from schema/*.fbs (eulix-embed/fb). Readers return plain dicts
with the same keys the old JSON files had, so code written for the JSON
outputs (and hashes over them) work on both formats. scripts/ imports this
module too.
"""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FORMAT_VERSION = 1  # libeulix/src/fb_io.rs FORMAT_VERSION

for p in (ROOT / "bench" / "vendor", ROOT / "eulix-embed"):
    if str(p) not in sys.path:
        sys.path.insert(0, str(p))

from fb.CallGraph import CallGraph  # noqa: E402


def _s(b) -> str:
    return b.decode("utf-8") if b is not None else ""


def _open(path, root_cls, identifier: bytes, kind: str):
    data = Path(path).read_bytes()
    if len(data) < 8 or data[4:8] != identifier:
        raise ValueError(f"{path}: not a eulix {kind} file (identifier {data[4:8]!r})")
    root = getattr(root_cls, "GetRootAs")(data, 0)
    if root.FormatVersion() != FORMAT_VERSION:
        raise ValueError(f"{path}: {kind} format version {root.FormatVersion()}, expected {FORMAT_VERSION}")
    return root


def kb_path(eulix_dir) -> Path:
    """kb.fb when present (current parsers), else kb.json."""
    d = Path(eulix_dir)
    fb = d / "kb.fb"
    return fb if fb.exists() else d / "kb.json"


def _embed_fb_stream():
    """eulix-embed's kb.fb reader (data_io/fb_stream.py), loaded by path: it has
    the fast wire-format decoder, and bench must not import eulix-embed as a
    package."""
    import importlib.util
    path = ROOT / "eulix-embed" / "data_io" / "fb_stream.py"
    spec = importlib.util.spec_from_file_location("eulix_embed_fb_stream", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def iter_kb(path):
    """Yield (file path, record dict) for every file in kb.fb, in path order.
    Records have the keys kb.json's structure entries had."""
    for event, *payload in _embed_fb_stream().stream_kb_fb(Path(path)):
        if event == "structure":
            yield payload[0], payload[1]


def load_kb(path) -> dict:
    """The whole knowledge base in the old kb.json shape: {"metadata", "structure"}."""
    out = {"metadata": {}, "structure": {}}
    for event, *payload in _embed_fb_stream().stream_kb_fb(Path(path)):
        if event == "meta":
            out["metadata"] = payload[1]
        else:
            out["structure"][payload[0]] = payload[1]
    return out


def call_graph_path(eulix_dir) -> Path:
    """kb_call_graph.fb when present (current parsers), else kb_call_graph.json."""
    d = Path(eulix_dir)
    fb = d / "kb_call_graph.fb"
    return fb if fb.exists() else d / "kb_call_graph.json"


def iter_call_graph(path, keys=("nodes", "edges")):
    """Yield (key, dict) for each node and edge of kb_call_graph.fb, in file order."""
    g = _open(path, CallGraph, b"EUCG", "call graph")
    for key in keys:
        if key == "nodes":
            for i in range(g.NodesLength()):
                n = g.Nodes(i)
                yield key, {
                    "id": _s(n.Id()),
                    "node_type": _s(n.NodeType()),
                    "file": _s(n.File()),
                    "is_entry_point": n.IsEntryPoint(),
                    "call_count_estimate": n.CallCountEstimate(),
                }
        elif key == "edges":
            for i in range(g.EdgesLength()):
                e = g.Edges(i)
                yield key, {
                    "from": _s(e.From()),
                    "to": _s(e.To()),
                    "edge_type": _s(e.EdgeType()),
                    "conditional": e.Conditional(),
                    "call_site_line": e.CallSiteLine(),
                }
        else:
            raise ValueError(f"unknown call graph array {key!r}")


def load_call_graph(path) -> dict:
    """The whole call graph as the old JSON shape: nodes, edges, tags, file_categories."""
    g = _open(path, CallGraph, b"EUCG", "call graph")
    out = {"nodes": [], "edges": []}
    for key, obj in iter_call_graph(path):
        out[key].append(obj)
    tags = {}
    for i in range(g.TagsLength()):
        t = g.Tags(i)
        tags[_s(t.Key())] = [_s(t.Values(j)) for j in range(t.ValuesLength())]
    cats = {}
    for i in range(g.FileCategoriesLength()):
        c = g.FileCategories(i)
        cats[_s(c.Key())] = _s(c.Category())
    if tags:
        out["tags"] = tags
    if cats:
        out["file_categories"] = cats
    return out
