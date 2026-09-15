#!/usr/bin/env python3
"""
[!] AI SLOP VIBE CODE

Exists Just to get how many keys from kb.json are we actually using in eulix_cli
exists cause on large code bases kb.json is too huge naturally this leads to too much memory
being used at retrieval stage.


Cross-references the struct/field schema in a Rust source file (kb_struct.rs)
against a Go codebase, reporting which fields of each struct are ACTUALLY
read, so you know what's safe to cut from kb.json.
Setup:
  - Requires a Go toolchain on PATH (`go version` should work). No other
    dependencies -- astdump.go uses only the Go standard library.

Usage:
  uv run scripts/kb_usage.py --rust ../eulix-parser/src/struc/kb_struct.rs --go-dir ../eulix-cli/internal/query
  uv run scripts/kb_usage.py --rust eulix-parser/src/struc/kb_struct.rs --go-dir eulix-cli/internal/query --only Function Class FileData
"""

import argparse
import glob
import json
import os
import re
import subprocess
import sys
import tempfile
from collections import OrderedDict, defaultdict

NAME_MAP = {
    "Function": "KBFunction",
    "Class": "KBClass",
    "FunctionCall": "KBCall",
}


SEED_HINTS = {
    "kbData": "KnowledgeBase",
    "kbIdx": "Indices",
}

INITIALISMS = {"id", "url", "api", "http", "json", "html", "xml", "kb"}


def snake_to_pascal(field: str) -> str:
    out = []
    for part in field.split("_"):
        out.append(part.upper() if part.lower() in INITIALISMS else part[:1].upper() + part[1:])
    return "".join(out)


def clean_go_type(t: str) -> str:
    t = t.strip().lstrip("*")
    while t.startswith("[]"):
        t = t[2:]
    if "." in t:
        t = t.rsplit(".", 1)[1]
    return t


def unwrap_to_bare(rust_type: str) -> str:
    t = rust_type.strip()
    t = re.sub(r"^&\s*('\w+\s*)?", "", t).strip()
    while True:
        m = re.match(r"^(?:Vec|Option|Box)<(.+)>$", t)
        if m:
            t = m.group(1).strip()
            continue
        m = re.match(r"^HashMap<\s*[^,]+,\s*(.+)>$", t)
        if m:
            t = m.group(1).strip()
            continue
        break
    t = t.lstrip("&").strip()
    return t

def parse_rust_structs(src: str):
    structs = OrderedDict()
    for m in re.finditer(r"pub struct (\w+)(?:<[^>]*>)?\s*\{", src):
        name = m.group(1)
        i = m.end()
        depth = 1
        while depth > 0 and i < len(src):
            if src[i] == "{":
                depth += 1
            elif src[i] == "}":
                depth -= 1
            i += 1
        body = src[m.end():i - 1]
        fields = []
        for line in body.splitlines():
            fm = re.match(r"\s*pub\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*:\s*(.+?),\s*(?://.*)?$", line)
            if fm:
                fields.append((fm.group(1), fm.group(2).strip()))
        structs[name] = fields
    return structs


def build_pascal_index(structs):
    idx = {}
    for name, fields in structs.items():
        idx[name] = {snake_to_pascal(f): (f, t) for f, t in fields}
    return idx


def run_astdump(go_files, go_bin="go", astdump_src=None):
    astdump_src = astdump_src or os.path.join(os.path.dirname(os.path.abspath(__file__)), "astdump.go")
    with tempfile.TemporaryDirectory() as td:
        binpath = os.path.join(td, "astdump")
        build = subprocess.run(
            [go_bin, "build", "-o", binpath, astdump_src],
            capture_output=True, text=True,
        )
        if build.returncode != 0:
            print("Failed to build astdump.go:\n" + build.stderr, file=sys.stderr)
            sys.exit(1)
        run = subprocess.run([binpath, *go_files], capture_output=True, text=True)
        if run.stderr:
            print(run.stderr, file=sys.stderr)
        return json.loads(run.stdout)

REVERSE_NAME_MAP = {}


class Resolver:
    def __init__(self, structs, pascal_idx):
        self.structs = structs
        self.pascal_idx = pascal_idx

    def field_type(self, struct_name, go_field):
        entry = self.pascal_idx.get(struct_name, {}).get(go_field)
        return entry[1] if entry else None

    def resolve(self, expr, types):
        expr = expr.strip()
        m = re.match(r"^(.*)\[[^\[\]]*\]$", expr)
        if m:
            expr = m.group(1).strip()

        if "." in expr:
            base, field = expr.rsplit(".", 1)
            if field in SEED_HINTS:
                return SEED_HINTS[field]
            base_struct = self.resolve(base, types)
            if not base_struct or base_struct not in self.structs:
                return None
            rust_type = self.field_type(base_struct, field)
            if not rust_type:
                return None
            bare = unwrap_to_bare(rust_type)
            return bare if bare in self.structs else None

        return types.get(expr)


def seed_type(go_type_text, resolver):
    t = clean_go_type(go_type_text)
    if t in REVERSE_NAME_MAP:
        return REVERSE_NAME_MAP[t]
    return t if t in resolver.structs else None


def propagate_function(fi, resolver):
    types = {}

    if fi.get("recv_type") and fi.get("recv_name"):
        rust = seed_type(fi["recv_type"], resolver)
        if rust:
            types[fi["recv_name"]] = rust

    for p in fi.get("params", []):
        if not p.get("name"):
            continue
        rust = seed_type(p["type"], resolver)
        if rust:
            types[p["name"]] = rust

    for vd in fi.get("var_decls", []) or []:
        rust = seed_type(vd["type"], resolver)
        if rust:
            types[vd["name"]] = rust

    events = []
    for r in fi.get("ranges", []):
        events.append((r["line"], "range", r))
    for a in fi.get("assigns", []):
        events.append((a["line"], "assign", a))
    events.sort(key=lambda e: e[0])

    for _line, kind, node in events:
        if kind == "range":
            if node["value"] and node["value"] != "_":
                t = resolver.resolve(node["x"], types)
                if t:
                    types[node["value"]] = t
        else:
            if len(node["lhs"]) in (1, 2) and len(node["rhs"]) == 1:
                lhs = node["lhs"][0]
                if re.match(r"^[A-Za-z_]\w*$", lhs):
                    t = resolver.resolve(node["rhs"][0], types)
                    if t:
                        types[lhs] = t

    hits = []
    for sel in fi.get("selectors", []):
        struct_name = resolver.resolve(sel["base"], types)
        if struct_name and sel["field"] in resolver.pascal_idx.get(struct_name, {}):
            hits.append((struct_name, sel["field"], sel["line"]))

    return hits


def main():
    global REVERSE_NAME_MAP

    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--rust", default="kb_struct.rs")
    ap.add_argument("--go-dir", default=".")
    ap.add_argument("--go-bin", default="go")
    ap.add_argument("--astdump", default=None, help="path to astdump.go (default: alongside this script)")
    ap.add_argument("--only", nargs="*", default=None)
    args = ap.parse_args()

    REVERSE_NAME_MAP = {v: k for k, v in NAME_MAP.items()}

    with open(args.rust, "r", encoding="utf-8") as f:
        structs = parse_rust_structs(f.read())
    pascal_idx = build_pascal_index(structs)
    resolver = Resolver(structs, pascal_idx)

    go_files = glob.glob(os.path.join(args.go_dir, "**", "*.go"), recursive=True)
    if not go_files:
        print(f"No .go files found under {args.go_dir}", file=sys.stderr)
        sys.exit(1)

    reports = run_astdump(go_files, go_bin=args.go_bin, astdump_src=args.astdump)

    all_hits = defaultdict(lambda: defaultdict(list))
    for rep in reports:
        for fi in rep["funcs"]:
            for struct_name, pascal_field, line in propagate_function(fi, resolver):
                all_hits[struct_name][pascal_field].append((rep["path"], line))

    names = args.only if args.only else list(structs.keys())

    print("=" * 82)
    print(f"{'Rust struct':<22}{'Go type':<16}{'used/total':<12}")
    print("=" * 82)
    total_fields = total_used = 0
    rows = []
    for name in names:
        if name not in structs:
            print(f"warning: struct '{name}' not found in {args.rust}", file=sys.stderr)
            continue
        fields = structs[name]
        if not fields:
            continue
        used = [f for f, _ in fields if snake_to_pascal(f) in all_hits.get(name, {})]
        rows.append((name, fields, used))
        total_fields += len(fields)
        total_used += len(used)
        go_name = NAME_MAP.get(name, name)
        print(f"{name:<22}{go_name:<16}{f'{len(used)}/{len(fields)}':<12}")

    print()
    for name, fields, used in rows:
        used_set = set(used)
        print("-" * 82)
        print(f"{name}  ({len(used)}/{len(fields)} fields referenced)")
        if used:
            print("  used:")
            for field, _rust_type in fields:
                if field in used_set:
                    pascal = snake_to_pascal(field)
                    locs = all_hits[name][pascal]
                    files = sorted({p for p, _ in locs})
                    print(f"    - {field:<20} -> .{pascal:<20} {len(locs)} hit(s) in {len(files)} file(s)")
        unused = [f for f, _ in fields if f not in used_set]
        if unused:
            print("  candidates to cut / move out of kb.json:")
            for field in unused:
                print(f"    - {field}")

    print("-" * 82)
    if total_fields:
        print(
            f"\nTOTAL: {total_used}/{total_fields} fields referenced "
            f"({total_used/total_fields:.0%}) across {len(go_files)} Go file(s) under {args.go_dir}"
        )


if __name__ == "__main__":
    main()
