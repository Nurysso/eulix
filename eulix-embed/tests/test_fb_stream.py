# Copyright (c) 2026 Dawood K.
# SPDX-License-Identifier: Apache-2.0
"""kb.fb reader: the fast wire-format decoder must match the generated code
and the --emit-json copy eulix_parser wrote for the same file."""

import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from data_io import fb_stream

# Written by eulix_parser --emit-json over a small fixture (Flask route + Go main).
DATA = ROOT.parent / "eulix-cli" / "internal" / "kbfile" / "testdata"


def _drop_none(v):
    """The JSON copy leaves absent optional fields out; the reader yields None."""
    if isinstance(v, dict):
        return {k: _drop_none(x) for k, x in v.items() if x is not None}
    if isinstance(v, list):
        return [_drop_none(x) for x in v]
    return v


def test_stream_matches_json_copy():
    copy = json.loads((DATA / "kb.json").read_text())
    events = list(fb_stream.stream_kb_fb(DATA / "kb.fb"))
    assert events[0] == ("meta", "metadata", copy["metadata"])
    files = [(p, rec) for ev, p, rec in events[1:]]
    assert [p for p, _ in files] == [f["path"] for f in copy["files"]]
    for (_, rec), want in zip(files, copy["files"]):
        assert _drop_none(rec) == want["record"]
    assert any(rec["functions"] for _, rec in files)


def test_fast_decoder_matches_generated_code():
    data = (DATA / "kb.fb").read_bytes()
    with open(DATA / "kb.fb", "rb") as fh:
        _, files, start = fb_stream.read_kb_header(fh)
    for _, off, length in files:
        rec = data[start + off : start + off + length]
        assert fb_stream._fast_record(rec) == fb_stream._record(rec)


def test_rejects_old_json_and_truncated_files(tmp_path):
    old = tmp_path / "kb.fb"
    old.write_text('{"metadata": {}, "structure": {}}')
    with pytest.raises(fb_stream.KbFormatError):
        list(fb_stream.stream_kb_fb(old))
    cut = tmp_path / "cut.fb"
    cut.write_bytes((DATA / "kb.fb").read_bytes()[:-9])
    with pytest.raises(fb_stream.KbFormatError):
        list(fb_stream.stream_kb_fb(cut))
