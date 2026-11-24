# Copyright (c) 2026 Dawood K.
# SPDX-License-Identifier: Apache-2.0

# Maintainer: Dawood (Nurysso) <nurysso@proton.me>
#
# Streaming reader for kb.fb, the FlatBuffers knowledge base written by
# eulix_parser (layout: schema/kb_header.fbs). Yields the same events, with
# the same dict shapes the old JSON reader (stream_kb) yielded for kb.json, so
# the chunker was unchanged by the move to FlatBuffers.

from __future__ import annotations

from collections.abc import Generator
from pathlib import Path
from typing import Any, BinaryIO

# fb/ is generated from schema/*.fbs by scripts/gen_flatbuffers.sh.
from fb.FileRecord import FileRecord
from fb.Function import Function
from fb.KbHeader import KbHeader
from fb.Metadata import Metadata

FORMAT_VERSION = 1  # libeulix/src/fb_io.rs FORMAT_VERSION
RECORD_ALIGN = 8  # libeulix/src/fb_io.rs KB_RECORD_ALIGN


class KbFormatError(ValueError):
    """kb.fb is missing, from another eulix version, or corrupt."""


def _s(b: bytes | None) -> str:
    return b.decode("utf-8") if b is not None else ""


def _opt(b: bytes | None) -> str | None:
    return b.decode("utf-8") if b is not None else None


def _function(f: Function) -> dict[str, Any]:
    params = []
    for i in range(f.ParamsLength()):
        p = f.Params(i)
        params.append(
            {
                "name": _s(p.Name()),
                "type_annotation": _s(p.TypeAnnotation()),
                "default_value": _opt(p.DefaultValue()),
            }
        )
    return {
        "id": _s(f.Id()),
        "name": _s(f.Name()),
        "signature": _s(f.Signature()),
        "params": params,
        "return_type": _s(f.ReturnType()),
        "docstring": _s(f.Docstring()),
        "line_start": f.LineStart(),
        "line_end": f.LineEnd(),
    }


def _record(buf: bytes) -> dict[str, Any]:
    r = FileRecord.GetRootAs(buf, 0)
    classes = []
    for i in range(r.ClassesLength()):
        c = r.Classes(i)
        attrs = []
        for j in range(c.AttributesLength()):
            a = c.Attributes(j)
            attrs.append(
                {
                    "name": _s(a.Name()),
                    "type_annotation": _s(a.TypeAnnotation()),
                    "value": _opt(a.Value()),
                }
            )
        classes.append(
            {
                "id": _s(c.Id()),
                "name": _s(c.Name()),
                "bases": [_s(c.Bases(j)) for j in range(c.BasesLength())],
                "docstring": _s(c.Docstring()),
                "line_start": c.LineStart(),
                "line_end": c.LineEnd(),
                "methods": [_function(c.Methods(j)) for j in range(c.MethodsLength())],
                "attributes": attrs,
                "decorators": [_s(c.Decorators(j)) for j in range(c.DecoratorsLength())],
            }
        )
    todos = []
    for i in range(r.TodosLength()):
        t = r.Todos(i)
        todos.append({"line": t.Line(), "text": _s(t.Text()), "priority": _s(t.Priority())})
    return {
        "language": _s(r.Language()),
        "loc": r.Loc(),
        "functions": [_function(r.Functions(i)) for i in range(r.FunctionsLength())],
        "classes": classes,
        "todos": todos,
    }


def _metadata(m: Metadata | None) -> dict[str, Any]:
    if m is None:
        return {}
    return {
        "version": _s(m.Version()),
        "git_hash": _s(m.GitHash()),
        "project_name": _s(m.ProjectName()),
        "project_hash": _s(m.ProjectHash()),
        "parsed_at": _s(m.ParsedAt()),
        "languages": [_s(m.Languages(i)) for i in range(m.LanguagesLength())],
        "total_files": m.TotalFiles(),
        "total_loc": m.TotalLoc(),
        "total_functions": m.TotalFunctions(),
        "total_classes": m.TotalClasses(),
        "total_methods": m.TotalMethods(),
    }


def read_kb_header(fh: BinaryIO) -> tuple[dict[str, Any], list[tuple[str, int, int]], int]:
    """Read kb.fb's header from the start of an open binary file.

    Returns (metadata, [(path, offset, length)], records_start). Leaves fh
    positioned at the end of the header.
    """
    prefix = fh.read(4)
    if len(prefix) < 4:
        raise KbFormatError("kb.fb is too short")
    n = int.from_bytes(prefix, "little")
    head = fh.read(n)
    if len(head) != n or n < 8 or head[4:8] != b"EUKB":
        raise KbFormatError("not a eulix kb.fb file (re-run `eulix analyze`)")
    h = KbHeader.GetRootAs(head, 0)
    if h.FormatVersion() != FORMAT_VERSION:
        raise KbFormatError(f"kb.fb has format version {h.FormatVersion()}, expected {FORMAT_VERSION}")
    files = []
    for i in range(h.FilesLength()):
        e = h.Files(i)
        files.append((_s(e.Path()), e.Offset(), e.Length()))
    end = 4 + n
    return _metadata(h.Metadata()), files, end + (-end) % RECORD_ALIGN


# ---------- fast record decoder ----------
#
# The generated accessors cost several Python calls and a vtable lookup per
# field, which made kb.fb ~2.7x slower to read than kb.json through ijson's C
# backend. This decoder reads the FlatBuffers wire format directly: a table
# at T starts with an int32 back-offset to its vtable; vtable[4 + 2*i] is the
# offset of field i from T (0 = absent); a string or vector field holds a
# uint32 forward offset to a uint32 length followed by the data. Field indexes
# follow declaration order in schema/kb.fbs. tests/test_fb_stream.py checks it
# against the generated code (`_record`).

import struct as _struct

_u16 = _struct.Struct("<H").unpack_from
_u32 = _struct.Struct("<I").unpack_from
_i32 = _struct.Struct("<i").unpack_from
_u64 = _struct.Struct("<Q").unpack_from


def _fields(buf: bytes, t: int) -> tuple[int, ...]:
    """Field offsets (relative to table t) from the table's vtable."""
    vt = t - _i32(buf, t)[0]
    vsize = _u16(buf, vt)[0]
    n = (vsize - 4) // 2
    return _struct.unpack_from(f"<{n}H", buf, vt + 4) if n else ()


def _str(buf: bytes, t: int, off: tuple[int, ...], i: int) -> str | None:
    if i >= len(off) or not off[i]:
        return None
    p = t + off[i]
    p += _u32(buf, p)[0]
    n = _u32(buf, p)[0]
    return buf[p + 4 : p + 4 + n].decode("utf-8")


def _u64f(buf: bytes, t: int, off: tuple[int, ...], i: int) -> int:
    return _u64(buf, t + off[i])[0] if i < len(off) and off[i] else 0


def _vec(buf: bytes, t: int, off: tuple[int, ...], i: int) -> list[int]:
    """Absolute positions of a vector's elements (each a uoffset to a table/string)."""
    if i >= len(off) or not off[i]:
        return []
    p = t + off[i]
    p += _u32(buf, p)[0]
    n = _u32(buf, p)[0]
    base = p + 4
    rel = _struct.unpack_from(f"<{n}I", buf, base) if n else ()
    return [base + 4 * k + r for k, r in enumerate(rel)]


def _strs(buf: bytes, t: int, off: tuple[int, ...], i: int) -> list[str]:
    out = []
    for s in _vec(buf, t, off, i):
        n = _u32(buf, s)[0]
        out.append(buf[s + 4 : s + 4 + n].decode("utf-8"))
    return out


def _fast_function(buf: bytes, t: int) -> dict[str, Any]:
    o = _fields(buf, t)
    params = []
    for p in _vec(buf, t, o, 3):
        po = _fields(buf, p)
        params.append(
            {
                "name": _str(buf, p, po, 0) or "",
                "type_annotation": _str(buf, p, po, 1) or "",
                "default_value": _str(buf, p, po, 2),
            }
        )
    return {
        "id": _str(buf, t, o, 0) or "",
        "name": _str(buf, t, o, 1) or "",
        "signature": _str(buf, t, o, 2) or "",
        "params": params,
        "return_type": _str(buf, t, o, 4) or "",
        "docstring": _str(buf, t, o, 5) or "",
        "line_start": _u64f(buf, t, o, 6),
        "line_end": _u64f(buf, t, o, 7),
    }


def _fast_record(buf: bytes) -> dict[str, Any]:
    t = _u32(buf, 0)[0]
    o = _fields(buf, t)
    classes = []
    for c in _vec(buf, t, o, 3):
        co = _fields(buf, c)
        attrs = []
        for a in _vec(buf, c, co, 7):
            ao = _fields(buf, a)
            attrs.append(
                {
                    "name": _str(buf, a, ao, 0) or "",
                    "type_annotation": _str(buf, a, ao, 1) or "",
                    "value": _str(buf, a, ao, 2),
                }
            )
        classes.append(
            {
                "id": _str(buf, c, co, 0) or "",
                "name": _str(buf, c, co, 1) or "",
                "bases": _strs(buf, c, co, 2),
                "docstring": _str(buf, c, co, 3) or "",
                "line_start": _u64f(buf, c, co, 4),
                "line_end": _u64f(buf, c, co, 5),
                "methods": [_fast_function(buf, m) for m in _vec(buf, c, co, 6)],
                "attributes": attrs,
                "decorators": _strs(buf, c, co, 8),
            }
        )
    todos = []
    for d in _vec(buf, t, o, 4):
        do = _fields(buf, d)
        todos.append(
            {
                "line": _u64f(buf, d, do, 0),
                "text": _str(buf, d, do, 1) or "",
                "priority": _str(buf, d, do, 2) or "",
            }
        )
    return {
        "language": _str(buf, t, o, 0) or "",
        "loc": _u64f(buf, t, o, 1),
        "functions": [_fast_function(buf, f) for f in _vec(buf, t, o, 2)],
        "classes": classes,
        "todos": todos,
    }


def stream_kb_fb(path: Path) -> Generator:
    """Yield ('meta', 'metadata', dict), then ('structure', path, dict) for
    every file in path order, reading kb.fb front to back with one record in
    memory at a time."""
    with open(path, "rb") as fh:
        meta, files, start = read_kb_header(fh)
        yield ("meta", "metadata", meta)
        pos = fh.tell()
        for file_path, offset, length in files:
            at = start + offset
            if at < pos:
                raise KbFormatError(f"kb.fb record for {file_path} is out of place")
            if at > pos:
                fh.seek(at)
            buf = fh.read(length)
            if len(buf) != length or buf[4:8] != b"EUFR":
                raise KbFormatError(f"kb.fb record for {file_path} is truncated or corrupt")
            pos = at + length
            yield ("structure", file_path, _fast_record(buf))
