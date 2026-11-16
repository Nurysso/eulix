"""Check that every function/method ID in kb.json agrees with its `name` and file.

IDs look like `func_{name}::{path}` or `method_{Class}_{name}::{path}`
(nested Python names use `.`: `method_C_f._inner::{path}`). A node passes when:
  - the path after `::` equals the file it is listed under, and
  - the body is `{name}` (func_, ifacemethod_; nested funcs end in `.{name}`), or ends with `_{name}` / `.{name}` (method_),
    and for methods listed under a class, equals `{class}_{name}` or ends with `.{name}`.

Usage: kb_id_name_check.py [path/to/kb.json] [--show N]
       (default: <repo>/.eulix/kb.json, 5 examples per bad group)
Exit code: 0 all consistent, 1 mismatches found.
"""
import collections
import json
import sys
from pathlib import Path

args = [a for a in sys.argv[1:] if not a.startswith("--")]
show = int(sys.argv[sys.argv.index("--show") + 1]) if "--show" in sys.argv else 5
if "--show" in sys.argv:
    args = [a for a in args if a != sys.argv[sys.argv.index("--show") + 1]]
kb_path = args[0] if args else Path(__file__).resolve().parent.parent / ".eulix" / "kb.json"
st = json.load(open(kb_path))
st = st.get("structure", st)

PREFIXES = ("func_", "method_", "ifacemethod_")


def split(node_id):
    body, _, path = node_id.rpartition("::")
    for p in PREFIXES:
        if body.startswith(p):
            return p, body[len(p):], path
    return "", body, path


def problem(node, file, cls=None):
    prefix, body, path = split(node["id"])
    name = node["name"]
    if path != file:
        return "path"
    if not prefix:
        return "prefix"
    if prefix in ("func_", "ifacemethod_"):  # ifacemethod ids carry no class
        return None if body == name or body.endswith("." + name) else "name"
    if cls is not None and body != f"{cls}_{name}" and not body.endswith("." + name):
        return "name"
    if not (body == name or body.endswith("_" + name) or body.endswith("." + name)):
        return "name"
    return None


total = 0
bad = collections.defaultdict(list)
for file, fd in st.items():
    ext = file.rsplit(".", 1)[-1]
    nodes = [(f, None) for f in fd.get("functions", [])]
    for cl in fd.get("classes", []):
        nodes += [(m, cl.get("name")) for m in cl.get("methods", [])]
    for node, cls in nodes:
        total += 1
        why = problem(node, file, cls)
        if why:
            bad[(ext, why)].append((node["id"], node["name"], cls))

n_bad = sum(len(v) for v in bad.values())
print(f"{total} functions/methods checked, {n_bad} inconsistent")
for (ext, why), items in sorted(bad.items()):
    print(f"\n[{ext}] {why}: {len(items)}")
    for i in items[:show]:
        print("   id=%s name=%s class=%s" % i)
sys.exit(1 if n_bad else 0)
