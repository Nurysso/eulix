"""Reference call sites for C / C++ sources, from libclang.

Usage: python3 refcalls.py <root> --lang c|c++ [--std c11] [-- extra clang args]

Uses the system clang Python bindings (python-clang) and libclang.so; set
LIBCLANG to point at another library. Every .c/.cc/.cpp/.cxx file is a
translation unit, compiled with -I for every project directory that holds a
header. Headers are scanned through the units that include them; a call seen
from several units is reported once.

A call names the declaration clang resolved. Its definition and declarations
inside the root are the targets. A virtual member call also lists every project
method that overrides it. Calls through function pointers are "unknown";
everything else outside the root is "external". Implicit constructor calls
(`S s;`, copies) are skipped; written ones (`S(1)`, `new S`) are kept.
"""

import argparse
import json
import os
import sys
from collections import defaultdict
from pathlib import Path

import clang.cindex as ci

ci.Config.set_library_file(os.environ.get("LIBCLANG", "/usr/lib/libclang.so"))
CK = ci.CursorKind

ap = argparse.ArgumentParser()
ap.add_argument("root")
ap.add_argument("--lang", choices=["c", "c++"], required=True)
ap.add_argument("--std")
ap.add_argument("extra", nargs="*")
a = ap.parse_args()
root = Path(a.root).resolve()
SKIP = {".git", "build", "node_modules"}
TU_EXT = {".c"} if a.lang == "c" else {".c", ".cc", ".cpp", ".cxx", ".c++"}
HDR_EXT = {".h", ".hh", ".hpp", ".hxx", ".inl"}

tus, inc = [], set()
for dirpath, dirs, files in os.walk(root):
    dirs[:] = [d for d in dirs if d not in SKIP and not d.startswith(".")]
    for f in files:
        ext = os.path.splitext(f)[1].lower()
        if ext in TU_EXT:
            tus.append(os.path.join(dirpath, f))
        if ext in HDR_EXT:
            inc.add(dirpath)
std = a.std or ("c11" if a.lang == "c" else "c++17")
try:  # libclang does not find its builtin headers (stddef.h) on its own
    import subprocess
    _rd = subprocess.run(["clang", "-print-resource-dir"], capture_output=True, text=True).stdout.strip()
except OSError:
    _rd = ""
args = [*(["-resource-dir", _rd] if _rd else []), "-x", a.lang, f"-std={std}", "-ferror-limit=0", *[f"-I{d}" for d in sorted(inc)], *a.extra]

relcache = {}


def rel(path):
    if path is None:
        return None
    if path not in relcache:
        try:
            relcache[path] = Path(path).resolve().relative_to(root).as_posix()
        except ValueError:
            relcache[path] = None
    return relcache[path]


def loc_of(c):
    f = c.location.file
    return (rel(f.name) if f else None), c.location.line


FUNC_KINDS = {CK.FUNCTION_DECL, CK.CXX_METHOD, CK.CONSTRUCTOR, CK.DESTRUCTOR, CK.FUNCTION_TEMPLATE, CK.CONVERSION_FUNCTION}
methods_by_name = defaultdict(list)  # name -> [method cursor]   (for overriders)
index = ci.Index.create()
seen_sites = set()
records = []
errors = 0

units = []
for tu_path in tus:
    try:
        tu = index.parse(tu_path, args=args)
    except ci.TranslationUnitLoadError:
        print(f"clang-ref: cannot parse {tu_path}", file=sys.stderr)
        continue
    errs = [d for d in tu.diagnostics if d.severity >= ci.Diagnostic.Error]
    errors += len(errs)
    if os.environ.get("CLANG_REF_DEBUG"):
        for d in errs[:20]:
            print(f"clang-ref: {d}", file=sys.stderr)
    units.append(tu)

def walk(cur, visit):
    for ch in cur.get_children():
        f = ch.location.file
        if f is not None and rel(f.name) is None:
            continue  # system / outside headers
        visit(ch)
        walk(ch, visit)

defs_by_usr = {}  # a call in one unit often sees only the prototype; the body is in another


def collect_defs(c):
    if c.kind in FUNC_KINDS and c.is_definition():
        f, line = loc_of(c)
        if f:
            defs_by_usr.setdefault(c.get_usr(), {"file": f, "line": line, "name": c.spelling})
        if c.kind == CK.CXX_METHOD:
            methods_by_name[c.spelling].append(c)


for tu in units:
    walk(tu.cursor, collect_defs)

def derives(cls, base_usr, depth=0):
    """cls (a class cursor) has base_usr among its (transitive) bases."""
    if cls is None or depth > 30:
        return False
    for ch in cls.get_children():
        if ch.kind == CK.CXX_BASE_SPECIFIER:
            b = ch.type.get_declaration()
            b = b.get_definition() or b
            if b.get_usr() == base_usr or derives(b, base_usr, depth + 1):
                return True
    return False


def overrides(m, base):
    """m (a method definition) overrides virtual method `base`."""
    return (m.type.spelling == base.type.spelling
            and derives(m.semantic_parent, (base.semantic_parent.get_definition() or base.semantic_parent).get_usr()))


def targets_of(ref):
    out, seen = [], set()
    d = defs_by_usr.get(ref.get_usr())
    if d:
        seen.add((d["file"], d["line"]))
        out.append(d)
    for c in (ref.get_definition(), ref, ref.canonical):
        if c is None:
            continue
        f, line = loc_of(c)
        if f and (f, line) not in seen:
            seen.add((f, line))
            out.append({"file": f, "line": line, "name": c.spelling})
    return out

def tokens_after(c):
    toks = list(c.get_tokens())
    return [t.spelling for t in toks[:3]]

def visit_call(c):
    if c.kind != CK.CALL_EXPR:
        return
    f, line = loc_of(c)
    if f is None:
        return
    key = (f, line, c.location.column, c.spelling)
    if key in seen_sites:
        return
    seen_sites.add(key)
    ref = c.referenced
    rec = {"file": f, "line": line, "col": c.location.column, "lines": [c.extent.start.line], "name": c.spelling,
           "kind": "function", "status": "unknown", "dispatch": "static", "targets": []}
    if ref is None or ref.kind not in FUNC_KINDS:
        if ref is None and not c.spelling:
            return  # call through an expression clang could not name
        records.append(rec)
        return
    if ref.kind == CK.CONSTRUCTOR:
        toks = tokens_after(c)
        if not toks or (toks[0] != "new" and toks[0] != c.spelling and "(" not in toks and "{" not in toks):
            return  # implicit construction
        if toks and toks[0] not in ("new", c.spelling):
            return
        rec["kind"] = "constructor"
    elif ref.kind in (CK.CXX_METHOD, CK.DESTRUCTOR, CK.CONVERSION_FUNCTION):
        rec["kind"] = "function" if ref.is_static_method() else "method"
    if ref.spelling.startswith("__builtin"):
        rec["status"] = "external"
        records.append(rec)
        return
    if ref.get_definition() is None and ref.get_usr() not in defs_by_usr:
        decl_file, decl_line = loc_of(ref)
        # Never defined, and declared only inside a source file: an implicit
        # declaration at first use (its header is missing), so unknown.
        # (A declaration outside the project, such as libc's, stays external.)
        if decl_file is not None and (
            (decl_file, decl_line) == (f, line) or Path(decl_file).suffix.lower() in TU_EXT
        ):
            records.append(rec)
            return
    tg = targets_of(ref)
    if ref.kind == CK.CXX_METHOD and ref.is_virtual_method():
        usr = ref.get_usr()
        for m in methods_by_name.get(ref.spelling, []):
            if m.get_usr() != usr and overrides(m, ref):
                ff, ll = loc_of(m)
                if ff:
                    tg.append({"file": ff, "line": ll, "name": m.spelling})
                    rec["dispatch"] = "virtual"
    if tg:
        rec["status"], rec["targets"] = "project", tg
    else:
        rec["status"] = "external"
    records.append(rec)

for tu in units:
    walk(tu.cursor, visit_call)
if errors:
    print(f"clang-ref: {errors} compile errors across {len(units)} units", file=sys.stderr)
for r in records:
    sys.stdout.write(json.dumps(r) + "\n")
