# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Parser output statistics in constant memory (the kernel's call graph is ~360 MB).

kb_call_graph.fb (or kb_call_graph.json from parsers before FlatBuffers) holds
{"nodes":[...],"edges":[...],"tags":{...},"file_categories":{...}}
(nodes: id, node_type, file; edges: from, to, edge_type). The counting mirrors
scripts/known_issues/method_edge_coverage.py and cross_language_edges.py.
"""
import collections
import hashlib
import json
import re
import os

CHUNK = 4 << 20
# JS/TS and C/C++ count as one language each, as in cross_language_edges.py.
LANG = {".py": "py", ".go": "go", ".rs": "rs", ".ts": "js", ".tsx": "js", ".js": "js", ".jsx": "js",
        ".mjs": "js", ".java": "java", ".c": "c", ".h": "c", ".cpp": "c", ".hpp": "c", ".cc": "c",
        ".php": "php"}
METADATA_IGNORED = ("parsed_at", "project_hash", "git_hash")  # as scripts/kb_diff.py


def iter_arrays(path, keys=("nodes", "edges")):
    """Yield (key, object) for each element of the named top-level arrays, in file order.

    A .fb call graph yields the same dicts the JSON file did, so stats and the
    semantic hash compare across the format change."""
    if str(path).endswith(".fb"):
        from . import kbfb
        yield from kbfb.iter_call_graph(path, keys)
        return
    dec = json.JSONDecoder()
    with open(path, encoding="utf-8") as f:
        buf, pos = "", 0

        def fill():
            nonlocal buf, pos
            chunk = f.read(CHUNK)
            buf, pos = buf[pos:] + chunk, 0
            return bool(chunk)

        for key in keys:
            marker = f'"{key}"'
            while True:
                i = buf.find(marker, pos)
                if i >= 0:
                    pos = i + len(marker)
                    break
                pos = max(pos, len(buf) - len(marker))
                if not fill():
                    raise ValueError(f"{path}: no {key!r} array")
            while True:  # skip ':' '[' and whitespace
                while pos < len(buf) and buf[pos] in " \t\r\n:":
                    pos += 1
                if pos < len(buf):
                    break
                if not fill():
                    raise ValueError(f"{path}: truncated after {key!r}")
            if buf[pos] != "[":
                raise ValueError(f"{path}: {key!r} is not an array")
            pos += 1
            while True:
                while pos < len(buf) and buf[pos] in " \t\r\n,":
                    pos += 1
                if pos >= len(buf):
                    if not fill():
                        raise ValueError(f"{path}: truncated inside {key!r}")
                    continue
                if buf[pos] == "]":
                    pos += 1
                    break
                try:
                    obj, end = dec.raw_decode(buf, pos)
                except json.JSONDecodeError:
                    if not fill():
                        raise
                    continue
                yield key, obj
                pos = end


LANGS = sorted(set(LANG.values())) + ["?"]
MASK128 = (1 << 128) - 1


def _lang(file: str) -> str:
    return LANG.get(os.path.splitext(file)[1], "?")


def callgraph_stats(path) -> dict:
    """Counts plus an order-independent hash of nodes and edges (`semantic`).

    Memory stays small on the kernel's 1.5M edges: each node is packed into one
    small int (language, extension, is-method), only call targets that are methods
    are remembered, and the semantic hash is a running sum of per-object digests
    (a multiset hash), so nothing is kept per object.
    """
    node_types = collections.Counter()
    edge_types = collections.Counter()
    exts: dict[str, int] = {}
    ext_names: list[str] = []
    node_code = {}  # hash(id) -> (ext index << 5) | (lang index << 1) | is_method
    method_targets = set()
    cross = collections.Counter()
    acc_sum, acc_xor, count = 0, 0, 0
    for key, obj in iter_arrays(path):
        d = int.from_bytes(hashlib.blake2b(json.dumps(obj, sort_keys=True).encode(), digest_size=16).digest(), "big")
        acc_sum = (acc_sum + d) & MASK128
        acc_xor ^= d
        count += 1
        if key == "nodes":
            node_types[obj.get("node_type", "?")] += 1
            file = obj.get("file", "")
            ext = os.path.splitext(file)[1] or "?"
            if ext not in exts:
                exts[ext] = len(ext_names)
                ext_names.append(ext)
            node_code[hash(obj.get("id"))] = (exts[ext] << 5) | (LANGS.index(_lang(file)) << 1) | \
                (obj.get("node_type") == "method")
        else:
            et = obj.get("edge_type", "?")
            edge_types[et] += 1
            if et == "call":
                to = hash(obj.get("to"))
                tcode = node_code.get(to)
                fcode = node_code.get(hash(obj.get("from")))
                if tcode is not None and tcode & 1:
                    method_targets.add(to)
                a = LANGS[(fcode >> 1) & 15] if fcode is not None else "?"
                b = LANGS[(tcode >> 1) & 15] if tcode is not None else "?"
                if a != b:
                    cross[f"{a}->{b}"] += 1
    per_ext = collections.defaultdict(lambda: [0, 0])  # ext -> [methods, with a caller]
    for h, code in node_code.items():
        if code & 1:
            e = per_ext[ext_names[code >> 5]]
            e[0] += 1
            e[1] += h in method_targets
    return {
        "nodes": sum(node_types.values()),
        "nodes_by_type": dict(node_types),
        "edges": sum(edge_types.values()),
        "edges_by_type": dict(edge_types),
        "call_edges": edge_types.get("call", 0),
        "methods_with_caller": {ext: {"with_caller": hit, "methods": total}
                                for ext, (total, hit) in sorted(per_ext.items())},
        "cross_lang_edges": dict(cross.most_common()),
        "semantic_hash": f"{acc_sum:032x}{acc_xor:032x}{count:x}",
    }


def kb_hash(path) -> str:
    """sha256 of kb.json with the run-specific metadata fields removed.

    kb.json starts with its metadata object, so only the first 64 KiB is decoded;
    the rest (the large `structure` object) is hashed as raw bytes.
    """
    with open(path, "rb") as f:
        head = f.read(1 << 16)
        text = head.decode("utf-8", errors="ignore")
        i = text.find('"metadata"')
        start = text.find("{", i + len('"metadata"'))
        if i < 0 or start < 0:
            raise ValueError(f"{path}: no metadata object at the start")
        meta, end = json.JSONDecoder().raw_decode(text, start)
        for k in METADATA_IGNORED:
            meta.pop(k, None)
        h = hashlib.sha256(json.dumps(meta, sort_keys=True).encode())
        f.seek(len(text[:end].encode("utf-8")))
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def file_hash(path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


_METADATA_HEAD = re.compile(r'\{\s*(?:"format_version"\s*:\s*\d+\s*,\s*)?"metadata"')

# FlatBuffers outputs that embed Metadata (run-specific timestamps and hashes),
# so their bytes differ between identical runs. Their --emit-json copies are
# hashed instead, without those fields.
FB_WITH_METADATA = ("kb.fb", "kb_metrics.fb")


def starts_with_metadata(path) -> bool:
    """True for kb.json-style files and --emit-json copies, which open with a
    metadata object (optionally after "format_version")."""
    with open(path, "rb") as f:
        head = f.read(256).decode("utf-8", errors="ignore").lstrip()
    return bool(_METADATA_HEAD.match(head))


def output_hashes(eulix_dir) -> dict:
    """Hashes of every parser output. Files that open with a metadata block (kb.json,
    kb_metrics.json and its --emit-json copy) are hashed without its run-specific
    fields (parsed_at, ...). Binary .fb outputs are hashed as bytes, except those in
    FB_WITH_METADATA, which their JSON copies cover."""
    out = {}
    for p in sorted(eulix_dir.glob("kb*.json")):
        out[p.name] = kb_hash(p) if starts_with_metadata(p) else file_hash(p)
    for p in sorted(eulix_dir.glob("kb*.fb")):
        if p.name not in FB_WITH_METADATA:
            out[p.name] = file_hash(p)
    return out
