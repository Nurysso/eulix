"""Reference call sites for Python sources, from jedi's goto/infer.

Usage: uv run --no-project --python 3.12 --with jedi==0.19.2 python refcalls.py <root>

Every ast.Call whose callee is a name or an attribute is resolved with
jedi.Script.goto(follow_imports=True), falling back to infer() for attributes.
Definitions under the root are "project"; builtins, stdlib and site-packages are
"external"; nothing found, or a variable that holds a callable, is "unknown".
Calling a class is a "constructor" whose target is the class (and its
__init__ when the project defines one).
"""

import ast
import json
import os
import sys
from pathlib import Path

import jedi

root = Path(sys.argv[1]).resolve()
project = jedi.Project(path=str(root), added_sys_path=[str(root)])
SKIP = {".git", ".venv", "venv", "node_modules", "__pycache__", ".tox", "build", "dist"}


def py_files():
    for dirpath, dirs, files in os.walk(root):
        dirs[:] = [d for d in dirs if d not in SKIP and not d.startswith(".")]
        for f in files:
            if f.endswith(".py"):
                yield Path(dirpath) / f


def rel(p) -> str | None:
    if p is None:
        return None
    try:
        return Path(p).resolve().relative_to(root).as_posix()
    except ValueError:
        return None


def classify(defs):
    """-> (status, kind, targets)"""
    targets, external, kind = [], False, None
    for d in defs:
        if d.in_builtin_module() or d.module_path is None:
            external = True
            continue
        r = rel(d.module_path)
        if r is None:
            external = True
            continue
        if d.type == "class":
            kind = "constructor"
            targets.append({"file": r, "line": d.line, "name": d.name})
            try:
                for n in d.defined_names():
                    if n.name in ("__init__", "__new__") and n.type == "function":
                        targets.append({"file": r, "line": n.line, "name": n.name})
            except Exception:
                pass
        elif d.type == "function":
            p = d.parent()
            kind = kind or ("method" if p is not None and p.type == "class" else "function")
            targets.append({"file": r, "line": d.line, "name": d.name})
    if targets:
        return "project", kind, targets
    return ("external" if external else "unknown"), kind, []


out = sys.stdout
for path in py_files():
    src = path.read_text(encoding="utf-8", errors="replace")
    try:
        tree = ast.parse(src)
    except SyntaxError:
        continue
    file = rel(path)
    script = jedi.Script(src, path=str(path), project=project)
    lines = src.splitlines()

    def chars(line, byte_col):  # ast columns count UTF-8 bytes; jedi wants characters
        text = lines[line - 1] if 0 < line <= len(lines) else ""
        return len(text.encode("utf-8")[:byte_col].decode("utf-8", errors="ignore"))
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        f = node.func
        if isinstance(f, ast.Name):
            line, col, name, kind = f.lineno, chars(f.lineno, f.col_offset), f.id, "function"
        elif isinstance(f, ast.Attribute) and f.end_lineno is not None:
            line, name, kind = f.end_lineno, f.attr, "method"
            col = chars(line, f.end_col_offset) - len(f.attr)
        else:
            continue
        try:
            defs = script.goto(line, col, follow_imports=True)
            if not defs or all(d.type in ("statement", "param", "instance") for d in defs):
                defs = script.infer(line, col) or defs
        except Exception:
            defs = []
        status, k, targets = classify(defs)
        rec = {"file": file, "line": line, "col": col + 1, "lines": [node.lineno], "name": name,
               "kind": k or kind, "status": status, "dispatch": "static", "targets": targets}
        out.write(json.dumps(rec) + "\n")
