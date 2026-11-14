# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Result documents: one JSON file per measured cell, found again by its cache key.

A key is the sha256 of the fields that decide whether an old result still
answers the same question: commit, corpus, config, Python env, device, and for
timing groups the machine. Working-tree runs are kept apart and never reused.
"""
from datetime import datetime, timezone

from . import paths
from .common import H, read_json, write_json

SCHEMA = 1


def key(fields: dict) -> str:
    return H({"schema": SCHEMA, **fields})


def doc_path(group: str, sha12: str, k: str):
    return paths.sub("results", group, sha12, f"{k[:16]}.json")


def load(side, group: str, fields: dict) -> dict | None:
    k = key(fields)
    if side.rev.worktree:
        return side.worktree_docs.get(k)
    p = doc_path(group, side.rev.sha12, k)
    return read_json(p) if p.exists() else None


def save(side, doc: dict) -> None:
    doc["created"] = datetime.now(timezone.utc).isoformat(timespec="seconds")
    if side.rev.worktree:
        side.worktree_docs[doc["key"]] = doc
        write_json(paths.sub("results", "_worktree", side.run_id, f"{doc['group']}-{doc['key'][:16]}.json"), doc)
    else:
        write_json(doc_path(doc["group"], side.rev.sha12, doc["key"]), doc)


def new_doc(side, group: str, method_version: int, fields: dict, corpus, engine: str | None) -> dict:
    return {
        "schema": SCHEMA,
        "group": group,
        "method_version": method_version,
        "key": key(fields),
        "key_fields": fields,
        "sha": side.rev.sha,
        "label": side.rev.label,
        "corpus": {"name": corpus.name, "commit": corpus.commit, "tag": corpus.tag, "id": corpus.id},
        "engine": engine,
        "device": side.device,
        "machine": side.machine,
        "env": side.env_info,
        "build": {k: side.meta.get(k) for k in ("binary_sha256", "app_version", "go_version", "rustc_version",
                                                 "lock_drift")},
        "harness": side.harness,
        "metrics": [],
        "details": {},
    }


def metric(name, unit, better, kind, samples=None, value=None) -> dict:
    """better: lower | higher | none. kind: time | memory | size | accuracy | count | ratio | bool."""
    m = {"name": name, "unit": unit, "better": better, "kind": kind}
    if samples is not None:
        m["samples"] = samples
    if value is not None or samples is None:
        m["value"] = value
    return m


def scan():
    root = paths.sub("results")
    if not root.exists():
        return
    for p in sorted(root.glob("*/*/*.json")):
        if p.parts[-3] == "_worktree":
            continue
        try:
            yield read_json(p)
        except ValueError:
            continue
