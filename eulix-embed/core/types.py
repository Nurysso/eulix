# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me
# Types for eulix_embed

from enum import Enum
from dataclasses import dataclass
from typing import Optional, List

from core.constants import DC_KW
# The unit of retrieval: one Chunk == one embeddable "thing" extracted from
# source code (a function body, a class overview, a method, or a whole-file
# summary). ChunkType.entry_point exists in the enum for parity with the
# Rust side but isn't currently emitted by chunk_one_file().
class ChunkType(str, Enum):
    function = "function"
    class_ = "class"
    method = "method"
    file = "file"
    entry_point = "entrypoint"
    other = "other"

@dataclass(**DC_KW)
class ChunkMetadata:
    file_path: Optional[str] = None
    language: Optional[str] = None
    line_start: Optional[int] = None
    line_end: Optional[int] = None
    name: str = ""
    complexity: Optional[int] = None


# __slots__ (via DC_KW) matters here: a large codebase can produce hundreds
# of thousands of Chunk objects, and __slots__ removes the per-instance
# __dict__, cutting memory roughly in half for objects this simple.
@dataclass(**DC_KW)
class Chunk:
    id: str
    chunk_type: ChunkType
    content: str
    metadata: ChunkMetadata
    tags: List[str]
    importance_score: float
