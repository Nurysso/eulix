"""Reference call sites for a Rust crate or workspace, from rust-analyzer LSIF.

Usage: python3 refcalls.py <root>      (needs rust-analyzer and the rust-src component)

`rust-analyzer lsif <root>` gives, for every name token in the project, the
definition it resolves to. A token is a call site when it is not itself a
definition and the source after it reads `(` or `::<..>(`, and the definition
is a `fn` (tuple structs and enum variants like `Some(x)` are not calls and are
skipped). A definition under the root is "project"; one in the standard
library or a dependency is "external"; no definition is "unknown".

Limitation: a call through a trait bound resolves to the trait's declaration,
not to the impls, so dynamic dispatch has a single target.
"""

import json
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote, urlparse

root = Path(sys.argv[1]).resolve()
FN_RE = re.compile(r"\bfn\s+(r#)?\w+\s*$")

V: dict[int, dict] = {}
nxt: dict[int, int] = {}
defn: dict[int, int] = {}
mon: dict[int, dict] = {}
def_items: dict[int, list[int]] = {}
contains: dict[int, list[int]] = {}
range_doc: dict[int, int] = {}

proc = subprocess.Popen(["rust-analyzer", "lsif", str(root)], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
for line in proc.stdout:
    o = json.loads(line)
    lab = o["label"]
    if o["type"] == "vertex":
        if lab in ("range", "document", "definitionResult", "moniker", "resultSet"):
            V[o["id"]] = o
        continue
    if lab == "next":
        nxt[o["outV"]] = o["inV"]
    elif lab == "textDocument/definition":
        defn[o["outV"]] = o["inV"]
    elif lab == "moniker":
        mon[o["outV"]] = o["inV"]
    elif lab == "contains":
        contains.setdefault(o["outV"], []).extend(o["inVs"])
        for r in o["inVs"]:
            range_doc[r] = o["outV"]
    elif lab == "item" and V.get(o["outV"], {}).get("label") == "definitionResult":
        def_items.setdefault(o["outV"], []).extend(o["inVs"])
        for r in o["inVs"]:
            range_doc.setdefault(r, o["document"])
if proc.wait() != 0:
    sys.exit(f"rust-ref: rust-analyzer lsif exited {proc.returncode}")

doc_path = {k: Path(unquote(urlparse(v["uri"]).path)) for k, v in V.items() if v["label"] == "document"}
text_cache: dict[Path, list[str] | None] = {}


def lines_of(p: Path):
    if p not in text_cache:
        try:
            text_cache[p] = p.read_text(encoding="utf-8", errors="replace").splitlines()
        except OSError:
            text_cache[p] = None
    return text_cache[p]


def rel(p: Path):
    try:
        return p.resolve().relative_to(root).as_posix()
    except ValueError:
        return None


def u16_to_idx(s: str, u16: int) -> int:
    n = 0
    for i, ch in enumerate(s):
        if n >= u16:
            return i
        n += 2 if ord(ch) > 0xFFFF else 1
    return len(s)


def token(doc: int, rng: dict):
    lines = lines_of(doc_path[doc])
    if lines is None:
        return None, None, None
    ln = rng["start"]["line"]
    if ln >= len(lines):
        return None, None, None
    s = lines[ln]
    a, b = u16_to_idx(s, rng["start"]["character"]), u16_to_idx(s, rng["end"]["character"])
    return s[a:b], s[:a], (s[b:], lines[ln + 1:ln + 4])


def is_call(after) -> bool:
    rest, more = after
    text = rest + " " + " ".join(more)
    i = 0
    while i < len(text) and text[i].isspace():
        i += 1
    if text.startswith("::<", i):
        depth, i = 0, i + 2
        while i < len(text):
            if text[i] == "<":
                depth += 1
            elif text[i] == ">":
                depth -= 1
                if depth == 0:
                    i += 1
                    break
            i += 1
        while i < len(text) and text[i].isspace():
            i += 1
    return text.startswith("(", i)


def_ranges = {r for items in def_items.values() for r in items}
project_docs = {d for d, p in doc_path.items() if rel(p) is not None and p.suffix == ".rs"}
out = sys.stdout
for doc in project_docs:
    file = rel(doc_path[doc])
    for r in contains.get(doc, []):
        rv = V.get(r)
        if rv is None or rv["label"] != "range" or r in def_ranges:
            continue
        rs = nxt.get(r)
        name, before, after = token(doc, rv)
        if not name or not (name[0].isalpha() or name[0] == "_") or not is_call(after):
            continue
        if before.rstrip().endswith("fn") or before.rstrip().endswith("!"):
            continue
        targets, status, kind = [], "unknown", "function"
        items = def_items.get(defn.get(rs), [])
        m = V.get(mon.get(rs), {})
        ident = m.get("identifier", "")
        if before.rstrip().endswith(".") or "::impl::" in ident or (before.rstrip().endswith("::") and re.search(r"(\b[A-Z]\w*|Self)(<[^>]*>)?::\s*$", before)):
            kind = "method"
        for d in items:
            dv = V.get(d)
            ddoc = range_doc.get(d)
            if dv is None or ddoc not in doc_path:
                continue
            dname, dbefore, _ = token(ddoc, dv)
            if dbefore is not None and not FN_RE.search(dbefore + (dname or "")):
                break  # tuple struct / enum variant / closure binding: not a fn call
            dfile = rel(doc_path[ddoc])
            if dfile is None:
                status = "external"
            else:
                targets.append({"file": dfile, "line": dv["start"]["line"] + 1, "name": dname or name})
        else:
            if targets:
                status = "project"
            elif not items and m.get("kind") == "import":
                status = "external"
            out.write(json.dumps({"file": file, "line": rv["start"]["line"] + 1, "col": rv["start"]["character"] + 1,
                                  "lines": [], "name": name, "kind": kind, "status": status, "dispatch": "static",
                                  "targets": targets}) + "\n")
