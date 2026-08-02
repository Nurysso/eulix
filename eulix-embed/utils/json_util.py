from functools import partial
import json
import warnings
from typing import Any

warnings.filterwarnings("ignore", message="optimum is not installed")

try:
    import orjson as _orjson

    if hasattr(_orjson, "OPT_INDENT_2"):
        json_dumps_no_indent = partial(_orjson.dumps, option=0)
        json_dumps_indent = partial(_orjson.dumps, option=_orjson.OPT_INDENT_2)
    else:  # fallback for older orjson versions
        json_dumps_no_indent = lambda obj: _orjson.dumps(obj).decode()
        json_dumps_indent = lambda obj: _orjson.dumps(
            obj, option=_orjson.OPT_INDENT_2
        ).decode()
    HAS_ORJSON = True
except ImportError:
    HAS_ORJSON = False
    json_dumps_no_indent = partial(json.dumps, indent=None)
    json_dumps_indent = partial(json.dumps, indent=2)

try:
    from ijson.common import ObjectBuilder
except ImportError:
    try:
        from ijson import ObjectBuilder  # type: ignore[no-redef]
    except ImportError:
        raise ImportError(
            'ijson ObjectBuilder not found — if using "uv" use uv pip install ijson'
        )

def json_dumps(obj: Any, *, indent: bool = False) -> str:
    """Fast JSON dumps – uses orjson when available, falls back to json."""
    if indent:
        return json_dumps_indent(obj)
    else:
        return json_dumps_no_indent(obj)
