#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["ijson>=3.2", "orjson>=3.9"]
# ///
"""
Compare two eulix-parser JSON outputs (kb.json, kb_index.json, ...) from
different runs and report whether they match.

Both files are streamed with ijson's yajl2_c backend, so multi-GB files never
load at once. Every value at --depth (default 2, e.g. structure["src/a.py"] or
metadata.total_loc) is built, serialized with orjson in its original key
order, and hashed. Then per entry:

  only in A / only in B   entry exists in one run only
  changed                 same path, different content (key order counts)
  order                   same entries and content, different sequence

Volatile fields (metadata/parsed_at, project_hash, git_hash) are ignored.

Usage:
  uv run scripts/kb_diff.py run1/kb.json run2/kb.json
  uv run scripts/kb_diff.py run1/kb_index.json run2/kb_index.json --depth 3
  uv run scripts/kb_diff.py a.json b.json --ignore metadata/total_loc --limit 50

Exit code: 0 identical, 1 different, 2 usage/backend error.
"""

import argparse
import sys

import ijson
import orjson

DEFAULT_IGNORE = ["metadata/parsed_at", "metadata/project_hash", "metadata/git_hash"]
START = {"start_map", "start_array"}
END = {"end_map", "end_array"}


def strip(value, rest):
    """Remove an ignored path from inside an already built value."""
    if not rest or not isinstance(value, dict) or rest[0] not in value:
        return
    if len(rest) == 1:
        del value[rest[0]]
    else:
        strip(value[rest[0]], rest[1:])


def entries(path, backend, depth, ignore):
    """Yield (path_tuple, hash) for every value at `depth` (or shallower scalars)."""
    frames = []  # one per open container: [is_array, current key or index]
    builder, nesting, entry_path = None, 0, None

    def emit(p, value):
        for ig in ignore:
            if p[: len(ig)] == ig:
                return None
        for ig in ignore:
            if ig[: len(p)] == p:
                strip(value, ig[len(p):])
        return p, hash(orjson.dumps(value))

    with open(path, "rb") as f:
        for event, value in backend.basic_parse(f, use_float=True):
            if builder is not None:
                builder.event(event, value)
                nesting += event in START
                nesting -= event in END
                if nesting == 0:
                    out = emit(entry_path, builder.value)
                    builder = None
                    if out:
                        yield out
                continue

            if event == "map_key":
                frames[-1][1] = value
                continue
            if event in END:
                frames.pop()
                continue

            if frames and frames[-1][0]:
                frames[-1][1] += 1
            here = tuple(fr[1] for fr in frames)

            if event in START and len(here) < depth:
                frames.append([event == "start_array", -1 if event == "start_array" else None])
            elif event in START:
                builder, nesting, entry_path = ijson.ObjectBuilder(), 1, here
                builder.event(event, value)
            else:
                out = emit(here, value)
                if out:
                    yield out


def load(path, backend, depth, ignore):
    order, hashes = [], {}
    for p, h in entries(path, backend, depth, ignore):
        order.append(p)
        hashes[p] = h
    return order, hashes


def show(p):
    return "/".join(str(k) for k in p) or "<root>"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("a")
    ap.add_argument("b")
    ap.add_argument("--depth", type=int, default=2, help="nesting level compared as one entry (default 2)")
    ap.add_argument("--ignore", action="append", default=[], help="extra '/'-separated path to ignore")
    ap.add_argument("--limit", type=int, default=20, help="max paths printed per section")
    args = ap.parse_args()
    if args.depth < 1:
        ap.error("--depth must be >= 1")

    try:
        backend = ijson.get_backend("yajl2_c")
    except ImportError:
        print("ijson yajl2_c backend is not available (install an ijson wheel with the C extension)", file=sys.stderr)
        return 2

    ignore = [tuple(p.split("/")) for p in DEFAULT_IGNORE + args.ignore]
    order_a, a = load(args.a, backend, args.depth, ignore)
    order_b, b = load(args.b, backend, args.depth, ignore)

    only_a = [p for p in order_a if p not in b]
    only_b = [p for p in order_b if p not in a]
    changed = [p for p in order_a if p in b and a[p] != b[p]]

    print(f"A: {args.a} ({len(order_a)} entries)")
    print(f"B: {args.b} ({len(order_b)} entries)")
    for title, paths in (("only in A", only_a), ("only in B", only_b), ("changed", changed)):
        if paths:
            print(f"\n{title}: {len(paths)}")
            for p in paths[: args.limit]:
                print(f"  {show(p)}")
            if len(paths) > args.limit:
                print(f"  ... {len(paths) - args.limit} more")

    reordered = None
    if not (only_a or only_b or changed) and order_a != order_b:
        reordered = next(i for i, (x, y) in enumerate(zip(order_a, order_b)) if x != y)
        print(f"\norder: same entries, sequence diverges at #{reordered}: {show(order_a[reordered])} vs {show(order_b[reordered])}")

    if only_a or only_b or changed or reordered is not None:
        print("\nRESULT: DIFFERENT")
        return 1
    print("\nRESULT: IDENTICAL")
    return 0


if __name__ == "__main__":
    sys.exit(main())
