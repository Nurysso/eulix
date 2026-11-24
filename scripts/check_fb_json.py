#!/usr/bin/env python3
# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Check that every --emit-json copy is a 1:1 image of its FlatBuffers file.

For each `<name>.fb` in DIR that has a `<name>.json` next to it, decode the
binary with flatc (the reference decoder) and compare it with the copy the
parser wrote: same keys in the same order, same values. Floats are compared
with a small relative tolerance, because flatc prints them with ~6
significant digits while the copy keeps the exact f32 value.

Usage: scripts/check_fb_json.py DIR      (run eulix_parser with --emit-json first)
Exit status 1 on any mismatch or missing schema.
"""

import json
import math
import os
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCHEMA_DIR = ROOT / "schema"

# Output file stem (without the "kb_" prefix) -> schema file.
SCHEMAS = {
    "entry_points": "entry_points.fbs",
    "external_deps": "external_deps.fbs",
    "metrics": "metrics.fbs",
    "summary": "summary.fbs",
    "patterns": "patterns.fbs",
    "call_graph": "call_graph.fbs",
    "index": "index.fbs",
}

# checksum.fb.zst: a zstd-compressed FlatBuffer; its copy is checksum.json.
CHECKSUM_FILE = "checksum.fb.zst"

# kb.fb is a size-prefixed header plus one FileRecord buffer per file
# (schema/kb_header.fbs), so it is decoded in pieces.
KB_IDENTIFIER = b"EUKB"
KB_ALIGN = 8


def flatc_bin() -> str:
    """$FLATC, else the release binary scripts/gen_flatbuffers.sh caches, else PATH."""
    if os.environ.get("FLATC"):
        return os.environ["FLATC"]
    gen = (ROOT / "scripts" / "gen_flatbuffers.sh").read_text()
    version = gen.split('FLATC_VERSION="', 1)[1].split('"', 1)[0]
    cache = Path(os.environ.get("XDG_CACHE_HOME") or Path.home() / ".cache")
    cached = cache / "eulix" / f"flatc-{version}" / "flatc"
    return str(cached) if cached.is_file() else "flatc"


FLATC = [flatc_bin(), "--json", "--strict-json", "--defaults-json", "--raw-binary"]


def schema_for(fb: Path) -> Path | None:
    stem = fb.stem
    key = stem.split("_", 1)[1] if stem.startswith("kb_") else stem
    name = SCHEMAS.get(key)
    return SCHEMA_DIR / name if name else None


def same(a, b, path="$") -> str | None:
    """Return a description of the first difference, or None."""
    if isinstance(a, dict) and isinstance(b, dict):
        if list(a) != list(b):
            return f"{path}: keys {list(a)} != {list(b)}"
        for k in a:
            d = same(a[k], b[k], f"{path}.{k}")
            if d:
                return d
        return None
    if isinstance(a, list) and isinstance(b, list):
        if len(a) != len(b):
            return f"{path}: length {len(a)} != {len(b)}"
        for i, (x, y) in enumerate(zip(a, b)):
            d = same(x, y, f"{path}[{i}]")
            if d:
                return d
        return None
    if isinstance(a, float) or isinstance(b, float):
        if isinstance(a, (int, float)) and isinstance(b, (int, float)):
            if math.isclose(a, b, rel_tol=1e-6, abs_tol=1e-9):
                return None
    elif type(a) is type(b) and a == b:
        return None
    return f"{path}: {a!r} != {b!r}"


def decompress(data: bytes) -> bytes | None:
    try:
        from compression import zstd  # Python 3.14+

        return zstd.decompress(data)
    except ImportError:
        try:
            return subprocess.run(["zstd", "-dc"], input=data, capture_output=True, check=True).stdout
        except (OSError, subprocess.CalledProcessError):
            return None


def is_kb(fb: Path) -> bool:
    with open(fb, "rb") as f:
        head = f.read(12)
    return len(head) == 12 and head[8:12] == KB_IDENTIFIER


def decode_kb(fb: Path, tmp: Path) -> dict:
    """flatc's decoding of kb.fb, in the shape of its --emit-json copy."""
    data = fb.read_bytes()
    n = int.from_bytes(data[:4], "little")
    (tmp / "header.bin").write_bytes(data[: 4 + n])
    subprocess.run(FLATC + ["--size-prefixed", "-o", str(tmp), str(SCHEMA_DIR / "kb_header.fbs"),
                            "--", str(tmp / "header.bin")], check=True)
    header = json.loads((tmp / "header.json").read_text())
    start = 4 + n + (-(4 + n)) % KB_ALIGN
    files = header.get("files", [])
    names = []
    for i, e in enumerate(files):
        off, length = start + e["offset"], e["length"]
        rec = tmp / f"r{i:07d}.bin"
        rec.write_bytes(data[off : off + length])
        names.append(rec)
    for i in range(0, len(names), 500):  # keep the command line short
        subprocess.run(FLATC + ["-o", str(tmp), str(SCHEMA_DIR / "kb.fbs"), "--",
                                *map(str, names[i : i + 500])], check=True)
    out = {"format_version": header["format_version"], "metadata": header["metadata"], "files": []}
    for e, rec in zip(files, names):
        out["files"].append({**e, "record": json.loads(rec.with_suffix(".json").read_text())})
    return out


def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    out = Path(sys.argv[1])
    checked, failed = 0, 0
    with tempfile.TemporaryDirectory() as tmp:
        for fb in sorted(out.glob("*.fb")):
            copy = fb.with_suffix(".json")
            if not copy.exists():
                continue
            if is_kb(fb):
                kb_tmp = Path(tmp) / "kb"
                kb_tmp.mkdir(exist_ok=True)
                ref = decode_kb(fb, kb_tmp)
                got = json.loads(copy.read_text())
                diff = same(ref, got)
                checked += 1
                if diff:
                    failed += 1
                    print(f"FAIL {fb.name}: {diff}")
                else:
                    print(f"ok   {fb.name} ({len(ref['files'])} records)")
                continue
            schema = schema_for(fb)
            if schema is None:
                print(f"FAIL {fb.name}: no schema mapped in check_fb_json.py")
                failed += 1
                continue
            subprocess.run(
                FLATC + ["-o", tmp, str(schema), "--", str(fb)],
                check=True,
            )
            ref = json.loads((Path(tmp) / copy.name).read_text())
            got = json.loads(copy.read_text())
            diff = same(ref, got)
            checked += 1
            if diff:
                failed += 1
                print(f"FAIL {fb.name}: {diff}")
            else:
                print(f"ok   {fb.name}")
        checksum = out / CHECKSUM_FILE
        copy = out / "checksum.json"
        if checksum.exists() and copy.exists():
            raw = decompress(checksum.read_bytes())
            if raw is None:
                print(f"skip {CHECKSUM_FILE}: needs Python 3.14+ (compression.zstd) or the zstd CLI")
            else:
                (Path(tmp) / "checksum.bin").write_bytes(raw)
                subprocess.run(FLATC + ["-o", tmp, str(SCHEMA_DIR / "checksum.fbs"), "--",
                                        str(Path(tmp) / "checksum.bin")], check=True)
                ref = json.loads((Path(tmp) / "checksum.json").read_text())
                diff = same(ref, json.loads(copy.read_text()))
                checked += 1
                if diff:
                    failed += 1
                    print(f"FAIL {CHECKSUM_FILE}: {diff}")
                else:
                    print(f"ok   {CHECKSUM_FILE}")
        # parse_cache/index.<gen>.fb.zst (the newest generation) and index.json.
        cache = out / "parse_cache"
        indexes = sorted(cache.glob("index.*.fb.zst"), key=lambda p: int(p.name.split(".")[1]))
        copy = cache / "index.json"
        if indexes and copy.exists():
            raw = decompress(indexes[-1].read_bytes())
            if raw is not None:
                (Path(tmp) / "cache_index.bin").write_bytes(raw)
                subprocess.run(FLATC + ["-o", tmp, str(SCHEMA_DIR / "parse_cache.fbs"), "--",
                                        str(Path(tmp) / "cache_index.bin")], check=True)
                ref = json.loads((Path(tmp) / "cache_index.json").read_text())
                diff = same(ref, json.loads(copy.read_text()))
                checked += 1
                if diff:
                    failed += 1
                    print(f"FAIL parse_cache/{indexes[-1].name}: {diff}")
                else:
                    print(f"ok   parse_cache/{indexes[-1].name}")
    if checked == 0:
        print("no .fb files with JSON copies found; run eulix_parser with --emit-json", file=sys.stderr)
        return 1
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
