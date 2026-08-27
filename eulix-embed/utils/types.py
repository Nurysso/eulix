# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
# Types for eulix_embed

from dataclasses import dataclass
from enum import Enum

from .constants import DC_KW


# The unit of retrieval: one Chunk == one embeddable "thing" extracted from
# source code (a function body, a class overview, a method, or a whole-file
# summary). ChunkType.entry_point exists in the enum for parity with the
# Rust side but isn't currently emitted by chunk_one_file().
class ChunkType(str, Enum):
    function = "function"
    class_ = "class"
    method = "method"
    file = "file"
    # entry_point = "entrypoint"
    # other = "other"


@dataclass(**DC_KW)
class ChunkMetadata:
    file_path: str | None = None
    name: str = ""
    language: str | None = None
    line_start: int | None = None
    line_end: int | None = None
    complexity: int | None = None


# __slots__ (via DC_KW) matters here: a large codebase can produce hundreds
# of thousands of Chunk objects, and __slots__ removes the per-instance
# __dict__, cutting memory roughly in half for objects this simple.
@dataclass(**DC_KW)
class Chunk:
    id: str
    chunk_type: ChunkType
    content: str
    tags: list[str]
    metadata: ChunkMetadata
    importance_score: float
