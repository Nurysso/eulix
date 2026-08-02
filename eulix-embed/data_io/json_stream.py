from typing import Set, Optional, Dict, List, Generator
from pathlib import Path
from utils.json_util import ObjectBuilder, HAS_ORJSON
import ijson.common

def stream_kb(path: Path) -> Generator:
    """
    Single-pass streaming reader for the (potentially multi-GB) knowledge
    base JSON, replacing what used to be two full passes over the file.

    The KB JSON has two kinds of top-level content:
      - small keys (metadata, entry_points, patterns, external_dependencies)
        that are cheap to hold in memory whole
      - "structure": a huge map of {file_path: file_struct}, which is the
        part that doesn't fit in RAM for large repos

    ijson.parse() emits a flat stream of (prefix, event, value) events as it
    reads the file byte-by-byte, so we never materialize more than one
    "small key" or one "file_struct" at a time. We track two independent
    ObjectBuilder instances (col_builder for small keys, st_builder for the
    current file inside "structure") and yield as soon as each one is
    complete, then discard it.

    Yields:
      ('meta', key, value)              — one per small top-level key
      ('structure', file_path, struct)  — one per file, in file order

    Why this matters: the old two-pass approach read a 4.6GB KB file twice
    (9.2GB of I/O) and fully materialized nested call_graph/dependency_graph
    dicts, causing 10-20x memory blowup relative to the JSON's on-disk size.
    This version reads the file exactly once and never holds more than one
    file_struct in memory.
    """
    # Keys whose entire value is small enough to buffer in RAM.
    COLLECT_KEYS: Set[str] = {"metadata", "structure"}

    top_key: Optional[str] = None

    # State for collecting small top-level values
    col_builder: Optional[ObjectBuilder] = None
    col_depth: int = 0

    #  State for streaming structure entries
    st_fp: Optional[str] = None
    st_builder: Optional[ObjectBuilder] = None
    st_depth: int = 0

    with open(path, "rb") as fh:
        for prefix, event, value in ijson.parse(fh, use_float=True):

            #  top-level key
            if prefix == "" and event == "map_key":
                top_key = value
                # Reset all sub-key trackers
                col_builder = None
                if value == "metadata":
                    col_builder =ObjectBuilder()
                    col_depth = 0
                continue

            #  Buffer small keys
            if col_builder is not None:
                col_builder.event(event, value)
                if event in ("start_map", "start_array"):
                    col_depth += 1
                elif event in ("end_map", "end_array"):
                    col_depth -= 1
                    if col_depth == 0:
                        yield ("meta", top_key, col_builder.value)
                        col_builder = None
                elif col_depth == 0:
                    # Scalar value at the root of this key
                    yield ("meta", top_key, col_builder.value)
                    col_builder = None
                continue

            # A "structure" map_key event means we've hit a new file path.
            # Start a fresh builder for it; the previous file's builder (if
            # any) was already yielded and dropped when its depth hit 0.
            if top_key == "structure":
                # prefix == "structure" + event == "map_key" → new file path
                if prefix == "structure" and event == "map_key":
                    st_fp = value
                    st_builder = ObjectBuilder()
                    st_depth = 0
                elif st_builder is not None:
                    st_builder.event(event, value)
                    if event in ("start_map", "start_array"):
                        st_depth += 1
                    elif event in ("end_map", "end_array"):
                        st_depth -= 1
                        if st_depth == 0:
                            yield ("structure", st_fp, st_builder.value)
                            st_builder = None
                            st_fp = None
                continue
