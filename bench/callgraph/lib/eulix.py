"""Run eulix_parser on a corpus and load its nodes and call edges."""

from __future__ import annotations

import json
import shutil
import subprocess
import time
from pathlib import Path

from .score import Node

KIND_OF_PREFIX = {"func": "function", "method": "method", "ctor": "method", "ifacemethod": "method"}


def run_parser(parser: Path, root: Path, out: Path, prism: int, threads: int) -> dict:
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)
    cmd = [str(parser), "--root", str(root), "--output", str(out / "kb.json"),
           "--prism", str(prism), "--threads", str(threads)]
    t0 = time.perf_counter()
    p = subprocess.run(cmd, capture_output=True, text=True)
    secs = time.perf_counter() - t0
    (out / "parser.log").write_text(p.stdout + p.stderr)
    if p.returncode != 0:
        raise RuntimeError(f"eulix_parser exited {p.returncode}; see {out / 'parser.log'}")
    return {"seconds": round(secs, 3), "cmd": cmd}


def _walk(obj, file: str, out: dict[str, Node]):
    if isinstance(obj, dict):
        nid = obj.get("id")
        if isinstance(nid, str) and "line_start" in obj:
            prefix = nid.split("_", 1)[0]
            kind = KIND_OF_PREFIX.get(prefix, "class")
            a, b = int(obj["line_start"]), int(obj.get("line_end") or obj["line_start"])
            if nid in out:  # overloads / same name twice: eulix gives them one id
                out[nid].more.append((file, a, b))
            else:
                out[nid] = Node(nid, obj.get("name", ""), kind, file, a, b)
        for v in obj.values():
            if isinstance(v, (dict, list)):
                _walk(v, file, out)
    elif isinstance(obj, list):
        for v in obj:
            _walk(v, file, out)


def load(out: Path) -> tuple[dict[str, Node], list[tuple[str, str, int]]]:
    kb = json.loads((out / "kb.json").read_text())
    nodes: dict[str, Node] = {}
    for file, entry in kb.get("structure", {}).items():
        _walk(entry, file, nodes)
    cg = json.loads((out / "kb_call_graph.json").read_text())
    edges = [(e["from"], e["to"], int(e.get("call_site_line") or 0))
             for e in cg.get("edges", []) if e.get("edge_type") in ("call", "calls")]
    return nodes, edges
