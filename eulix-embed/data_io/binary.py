import os
import io
from typing import List, Tuple, Optional
from pathlib import Path
import struct as _struct
from core.constants import BINARY_MAGIC, BINARY_VERSION
from utils.req import require_numpy
from .serialization import sq8_decode, sq8_encode

def _write_str(fh, s: str) -> None:
    b = s.encode("utf-8")
    fh.write(_struct.pack("<I", len(b)))
    fh.write(b)


def _read_str(fh) -> str:
    (n,) = _struct.unpack("<I", fh.read(4))
    return fh.read(n).decode("utf-8")


def save_embeddings_bin(
    path: "Path",
    model_name: str,
    dimension: int,
    entries,  # Iterable[Tuple[str, np.ndarray]]
    count: "Optional[int]" = None,
    quantize: bool = False,
) -> None:
    """
    Stream-write embeddings.bin without ever holding all vectors in memory
    at once — `entries` is consumed lazily as an iterator, so this can be
    fed directly from a generator (see EmbeddingPipeline.generate_vectors_streaming).

    v4 format adds a single quantization-flag byte after `dimension` so
    readers can distinguish float32 vs SQ8 payloads; v3 files (no flag byte)
    are still readable by load_embeddings_bin for backwards compatibility.
    v5 changed name of script and magicByte

    File Layout (Little-Endian):
    ─────────────────────────────────────────────────────────
    4  bytes  magic          "EULX"
    4  bytes  version        4
    4+n bytes model_name     uint32 len + UTF-8
    4  bytes  count          number of embeddings
    4  bytes  dimension      vector length (floats)
    1  byte   quantized      0 = float32, 1 = SQ8 int8
    ─── per embedding ───────────────────────────────────────
    4  bytes  id_len
    id_len    id             UTF-8
    if quantized:
      4  bytes  scale        float32 (little-endian)
      dim bytes quantized    int8 * dim
    else:
      dim*4    vector        float32 * dim
    ─────────────────────────────────────────────────────────
    """
    np = require_numpy()
    if count is None:
        entries = list(entries)
        count = len(entries)

    quant_flag = b"\x01" if quantize else b"\x00"

    with open(path, "wb") as raw:
        fh = io.BufferedWriter(raw, buffer_size=4 * 1024 * 1024)
        fh.write(BINARY_MAGIC)
        fh.write(_struct.pack("<I", BINARY_VERSION))
        _write_str(fh, model_name)
        fh.write(_struct.pack("<II", count, dimension))
        fh.write(quant_flag)

        for eid, vec in entries:
            eid_bytes = eid.encode("utf-8")
            if len(eid_bytes) > 0xFFFF:
                raise ValueError(f"Chunk ID too long: {eid[:80]}...")
            fh.write(_struct.pack("<I", len(eid_bytes)))
            fh.write(eid_bytes)

            arr = np.asarray(vec, dtype=np.float32)
            if arr.shape != (dimension,):
                raise ValueError(
                    f"Shape mismatch for {eid}: {arr.shape} vs ({dimension},)"
                )
            if quantize:
                q, scale = sq8_encode(arr)
                fh.write(_struct.pack("<f", scale))  # 4 bytes
                fh.write(q.tobytes())  # dim bytes (int8)
            else:
                fh.write(arr.tobytes())  # dim*4 bytes
            fh.flush()


def load_embeddings_bin(path: "Path", dequantize: bool = True):
    """
    Load embeddings.bin, transparently handling both v3 (always float32)
    and v4 (float32 or SQ8, flagged) files.

    On a corrupt/truncated entry, logs the error and stops reading rather
    than raising — callers get back whatever was successfully parsed plus
    a `count` mismatch they can detect by comparing len(entries) to the
    header's declared count.

    dequantize=True (default): always returns (id, float32_vector) pairs,
        transparently converting SQ8 data back to float32.
    dequantize=False: for SQ8 files, returns (id, int8_vector, scale)
        instead — useful if the caller wants to do quantized-domain math
        (e.g. int8 dot products) without paying the conversion cost.
    """
    np = require_numpy()
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic: {magic!r}")
        (version,) = _struct.unpack("<I", fh.read(4))
        model_name = _read_str(fh)
        count, dim = _struct.unpack("<II", fh.read(8))

        quantized = False
        if version == 4:
            quantized = fh.read(1) == b"\x01"
        elif version != 3:
            raise ValueError(f"Unsupported embeddings.bin version: {version}")

        print(f"  magic={magic}, version={version}, model={model_name}")
        print(f"  count={count}, dim={dim}, quantized={quantized}")

        entries = []
        for idx in range(count):
            pos = fh.tell()
            try:
                eid = _read_str(fh)
                if quantized:
                    (scale,) = _struct.unpack("<f", fh.read(4))
                    raw = fh.read(dim)
                    q = np.frombuffer(raw, dtype=np.int8).copy()
                    if dequantize:
                        entries.append((eid, sq8_decode(q, scale)))
                    else:
                        entries.append((eid, q, scale))
                else:
                    raw = fh.read(dim * 4)
                    if len(raw) != dim * 4:
                        print(f"  [ERROR] entry {idx} @{pos}: short read")
                        break
                    vec = np.frombuffer(raw, dtype=np.float32).copy()
                    entries.append((eid, vec))
            except Exception as e:
                print(f"  [ERROR] entry {idx} @{pos}: {e}")
                break

        print(f"  successfully read {len(entries)} / {count} entries")
        return model_name, entries


def save_vectors_bin(
    path: Path,
    model_name: str,
    ids: List[str],  # ordered; position == vector index in embeddings.bin
) -> None:
    """
    Save a vector ID index (no float data).

    File Format (Little-Endian):
    =============================
    Offset   | Size     | Field       | Description
    ---------|----------|-------------|-----------------------------
    0        | 4        | magic       | "EULX" file signature
    4        | 4        | version     | Format version (currently 1)
    8        | variable | model_name  | 4-byte len + UTF-8 bytes
    8+mlen   | 4        | count       | Number of IDs
    12+mlen  | variable | id_data     | Repeated for each ID:
             |          |   4 bytes   | id_len (uint32 LE)
             |          |   id_len    | UTF-8 chunk ID
    """
    with open(path, "wb") as fh:
        fh.write(BINARY_MAGIC)
        fh.write(_struct.pack("<I", BINARY_VERSION))
        _write_str(fh, model_name)
        fh.write(_struct.pack("<I", len(ids)))
        for eid in ids:
            eid_bytes = eid.encode("utf-8")
            if len(eid_bytes) > 0xFFFF:
                raise ValueError(
                    f"Chunk ID too long ({len(eid_bytes)} bytes): {eid[:80]}..."
                )
            fh.write(_struct.pack("<I", len(eid_bytes)))
            fh.write(eid_bytes)


def load_vectors_bin(path: Path) -> Tuple[str, List[str]]:
    """
    Load a vector ID index.

    Returns:
        (model_name, ids)  — ids[i] is the chunk ID at vector index i.
    """
    with open(path, "rb") as fh:
        magic = fh.read(4)
        if magic != BINARY_MAGIC:
            raise ValueError(f"Bad magic: {magic!r} (expected {BINARY_VERSION!r})")
        (version,) = _struct.unpack("<I", fh.read(4))
        if version != BINARY_VERSION:
            raise ValueError(f"Unsupported vectors.bin version: {version}")
        model_name = _read_str(fh)
        (count,) = _struct.unpack("<I", fh.read(4))
        ids: List[str] = [_read_str(fh) for _ in range(count)]
    return model_name, ids
