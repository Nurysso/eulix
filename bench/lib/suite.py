# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Question sets. Each question is cached by its own hash, so adding one runs only that one."""
import tomllib
from dataclasses import dataclass
from pathlib import Path

from .common import SUITE, H, BenchError

STYLES = {"code", "plain"}


@dataclass(frozen=True)
class Question:
    id: str
    text: str
    expect: tuple   # path prefixes; empty means latency-only
    style: str = ""
    component: str = ""

    def qhash(self, corpus: str) -> str:
        return H({"corpus": corpus, "text": self.text, "expect": sorted(self.expect)})[:16]


def load(rel: str | None) -> list[Question]:
    if not rel:
        return []
    path = SUITE / rel
    if not path.exists():
        raise BenchError(f"question file {path} does not exist")
    data = tomllib.loads(path.read_text())
    if data.get("schema") != 1:
        raise BenchError(f"{path}: unsupported schema {data.get('schema')}")
    out, ids, texts = [], set(), set()
    for q in data.get("q", []):
        qid, text = q.get("id"), q.get("text")
        if not qid or not text:
            raise BenchError(f"{path}: every question needs id and text")
        if qid in ids:
            raise BenchError(f"{path}: duplicate id {qid}")
        if text in texts:
            raise BenchError(f"{path}: duplicate text {text!r}")
        ids.add(qid)
        texts.add(text)
        expect = tuple(q.get("expect", ()))
        style = q.get("style", "")
        if expect and style not in STYLES:
            raise BenchError(f"{path}: {qid}: style must be one of {sorted(STYLES)}")
        out.append(Question(id=qid, text=text, expect=expect, style=style, component=q.get("component", "")))
    return out


def graded(questions: list[Question]) -> list[Question]:
    return [q for q in questions if q.expect]


def latency_set_hash(corpus: str, questions: list[Question]) -> str:
    return H({"corpus": corpus, "texts": sorted(q.text for q in questions)})[:16]


def path_of(rel: str) -> Path:
    return SUITE / rel
