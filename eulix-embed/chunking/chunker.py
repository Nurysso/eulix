# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Chunking module is responsible for chunking files/parts of json file
#
# This file is responsible for splitting code into fine‑grained chunks (functions, methods, classes, file summary)
# to improve retrieval precision. Deduping via seen_ids prevents duplicate IDs from malformed
# input. Truncation keeps chunk sizes bounded for embedding models. We use formatters to
# generate human‑readable cards that include call context, which helps semantic search.

from typing import Any

from utils.types import Chunk, ChunkMetadata, ChunkType

from .formatters import (
    fmt_class_overview,
    fmt_file_summary,
    fmt_function_with_context,
    fmt_method_with_class_ctx,
)


def chunk_one_file(
    file_path: str,
    fs: dict[str, Any],
    max_size: int,
    seen_ids: set[str],
) -> list[Chunk]:
    """
    Turn one parsed file_struct into a flat list of Chunks: one per
    function, one per class (overview card) + one per method inside it,
    and one file-level summary card if the file has any content worth
    summarizing.

    `seen_ids` dedupes within this call (methods can't collide with
    functions since IDs are assigned upstream by the parser, but this guards
    against any accidental duplicate emission from malformed input).
    Docstrings must already be stripped from `fs` by the caller
    (_drop_docstrings) before this runs — this function doesn't do it
    itself so it can be called from a worker thread without re-touching
    fields other threads might be reading.
    """
    chunks: list[Chunk] = []
    lang = fs.get("language", "")

    # Functions
    for func in fs.get("functions", []):
        fid = func["id"]
        before = len(seen_ids)
        seen_ids.add(fid)
        if len(seen_ids) == before:
            continue

        chunks.append(
            Chunk(
                id=fid,
                chunk_type=ChunkType.function,
                content=_truncate_content(
                    fmt_function_with_context(func, file_path),
                    max_size,
                ),
                metadata=ChunkMetadata(
                    file_path=file_path,
                    language=lang,
                    line_start=func.get("line_start"),
                    line_end=func.get("line_end"),
                    name=func["name"],
                    complexity=func.get("complexity"),
                ),
                tags=_generate_tags(func, "function"),
                importance_score=func.get("importance_score", 0.5),
            )
        )

    # Classes + methods
    for cls in fs.get("classes", []):
        cid = cls["id"]
        if cid in seen_ids:
            continue
        seen_ids.add(cid)

        chunks.append(
            Chunk(
                id=cid,
                chunk_type=ChunkType.class_,
                content=_truncate_content(
                    fmt_class_overview(cls, file_path),
                    max_size,
                ),
                metadata=ChunkMetadata(
                    file_path=file_path,
                    language=lang,
                    line_start=cls.get("line_start"),
                    line_end=cls.get("line_end"),
                    name=cls["name"],
                    complexity=None,
                ),
                tags=["class", lang],
                importance_score=0.7,
            )
        )

        for method in cls.get("methods", []):
            mid = method["id"]
            if mid in seen_ids:
                continue
            seen_ids.add(mid)

            chunks.append(
                Chunk(
                    id=mid,
                    chunk_type=ChunkType.method,
                    content=_truncate_content(
                        fmt_method_with_class_ctx(method, cls, file_path),
                        max_size,
                    ),
                    metadata=ChunkMetadata(
                        file_path=file_path,
                        language=lang,
                        line_start=method.get("line_start"),
                        line_end=method.get("line_end"),
                        name=f"{cls['name']}.{method['name']}",
                        complexity=method.get("complexity"),
                    ),
                    tags=_generate_tags(method, "method"),
                    importance_score=method.get("importance_score", 0.5),
                )
            )

    summary = fmt_file_summary(file_path, fs)
    if summary.strip():
        fid = f"file:{file_path}"
        if fid not in seen_ids:
            seen_ids.add(fid)
            chunks.append(
                Chunk(
                    id=fid,
                    chunk_type=ChunkType.file,
                    content=_truncate_content(summary, max_size),
                    metadata=ChunkMetadata(
                        file_path=file_path,
                        language=lang,
                        line_start=1,
                        line_end=fs.get("loc"),
                        name=file_path,
                        complexity=None,
                    ),
                    tags=["file", lang],
                    importance_score=0.5,
                )
            )

    return chunks


def _truncate_content(content: str, max_size: int) -> str:
    safe_max = min(max_size, 2000)
    if len(content) <= safe_max:
        return content
    trunc_at = max(safe_max - 3, 0)
    nl = content[:trunc_at].rfind("\n")
    cut = nl if nl != -1 else trunc_at
    return content[:cut] + "..."


def _generate_tags(func: dict[str, Any], base_tag: str) -> list[str]:
    tags = [base_tag]
    if func.get("is_async"):
        tags.append("async")
    tags.extend(func.get("tags", []))
    if func.get("complexity", 0) > 10:
        tags.append("complex")
    for dec in func.get("decorators", []):
        if "api" in dec or "route" in dec:
            tags.append("api")
        if "test" in dec:
            tags.append("test")
    return sorted(set(tags))
