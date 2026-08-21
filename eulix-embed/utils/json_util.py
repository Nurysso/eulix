# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Utils module holds shared code like dynamic imports.

import json
import warnings
from collections.abc import Callable
from functools import partial
from typing import Any

warnings.filterwarnings("ignore", message="optimum is not installed")

try:
    import orjson as _orjson

    # orjson returns bytes, so wrapper functions handle decoding to str
    def _orjson_dumps_no_indent(obj: Any) -> str:
        return _orjson.dumps(obj).decode()

    def _orjson_dumps_indent(obj: Any) -> str:
        return _orjson.dumps(obj, option=_orjson.OPT_INDENT_2).decode()

    json_dumps_no_indent: Callable[[Any], str] = _orjson_dumps_no_indent
    json_dumps_indent: Callable[[Any], str] = _orjson_dumps_indent
    HAS_ORJSON = True

except ImportError:
    HAS_ORJSON = False
    json_dumps_no_indent = partial(json.dumps, indent=None)
    json_dumps_indent = partial(json.dumps, indent=2)
try:
    from ijson.common import ObjectBuilder
except ImportError:
    try:
        from ijson import ObjectBuilder  # noqa: F401
    except ImportError:
        raise ImportError('ijson ObjectBuilder not found — if using "uv" use uv pip install ijson')


def json_dumps(obj: Any, *, indent: bool = False) -> str:
    """Fast JSON dumps – uses orjson when available, falls back to json."""
    if indent:
        return json_dumps_indent(obj)
    else:
        return json_dumps_no_indent(obj)
