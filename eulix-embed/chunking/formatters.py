# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Chunking module is responsible for chunking files/parts of json file
#
# This file is responsible for rendering chunks as plain‑text "cards" with contextual information (calls, called_by,
# parameters, exceptions) rather than raw JSON. This format is both human‑readable and
# works well with embedding models because it mimics a natural code explanation.
# Truncation of callers (e.g., "… and N more") keeps card sizes bounded and prevents
# excessive token usage.

import io


# These _fmt_* functions render a parsed function/class/file dict back into
# a plain-text "card" that gets embedded and shown to the retriever. They're
# deliberately comment-like (// File:, // Function:) rather than JSON, since
# that reads naturally to both humans and the embedding model.
def fmt_function_with_context(func: dict, file_path: str) -> str:
    """Render one function as a text card: signature, params, return type,
    up to 10 outgoing calls and 5 incoming callers (truncated with a "...
    and N more" line to keep card size bounded), control-flow complexity,
    and any exceptions raised/handled."""
    buf = io.StringIO()
    w = buf.write
    w(f"// File: {file_path}")
    w(f"// Function: {func['name']}")
    w(f"// Lines: {func.get('line_start', 0)}-{func.get('line_end', 0)}")
    w(f"// Complexity: {func.get('complexity', 0)}")
    w("")
    w(func.get("signature", ""))
    w("")

    params = func.get("params", [])
    if params:
        w("Parameters:")
        for p in params:
            dv = f" = {p['default_value']}" if p.get("default_value") else ""
            w(f"  - {p['name']}: {p.get('type_annotation', '')}{dv}")
        w("")

    if func.get("return_type"):
        w(f"Returns: {func['return_type']}")
        w("")

    calls = func.get("calls", [])
    if calls:
        w("Calls:")
        for c in calls[:10]:
            w(f"  - {c['callee']} (line {c['line']})")
        if len(calls) > 10:
            w(f"  ... and {len(calls) - 10} more")
        w("")

    called_by = func.get("called_by", [])
    if called_by:
        w("Called by:")
        for c in called_by[:5]:
            w(f"  - {c['function']} in {c['file']}")
        if len(called_by) > 5:
            w(f"  ... and {len(called_by) - 5} more")
        w("")

    cf = func.get("control_flow", {})
    if cf.get("complexity", 0) > 0:
        w(
            f"Control flow: {len(cf.get('branches', []))} branches, "
            f"{len(cf.get('loops', []))} loops"
        )

    exc = func.get("exceptions", {})
    if exc.get("raises") or exc.get("handles"):
        w("Exceptions:")
        if exc.get("raises"):
            w(f"  Raises: {', '.join(exc['raises'])}")
        if exc.get("handles"):
            w(f"  Handles: {', '.join(exc['handles'])}")
        w("")

    return buf.getvalue()


def fmt_method_with_class_ctx(method: dict, cls: dict, file_path: str) -> str:
    buf = io.StringIO()
    w = buf.write
    w(f"// File: {file_path}")
    w(f"// Class: {cls['name']}")
    w(f"// Method: {method['name']}")
    w("")
    if cls.get("bases"):
        w(f"// Inherits from: {', '.join(cls['bases'])}")
    w("")
    w(fmt_function_with_context(method, file_path))
    return buf.getvalue()


def fmt_class_overview(cls: dict, file_path: str) -> str:
    buf = io.StringIO()
    w = buf.write
    w(f"// File: {file_path}")
    w(f"// Class: {cls['name']}")
    w(f"// Lines: {cls.get('line_start', 0)}-{cls.get('line_end', 0)}")
    w("")
    if cls.get("bases"):
        w(f"Inherits from: {', '.join(cls['bases'])}")
        w("")
    attrs = cls.get("attributes", [])
    if attrs:
        w("Attributes:")
        for a in attrs:
            w(f"  - {a['name']}: {a.get('type_annotation', '')}")
        w("")
    methods = cls.get("methods", [])
    if methods:
        w(f"Methods ({len(methods)}):")
        for m in methods:
            async_tag = " (async)" if m.get("is_async") else ""
            w(f"  - {m['name']}{async_tag}")
        w("")
    return buf.getvalue()


def fmt_file_summary(file_path: str, fs: dict) -> str:
    buf = io.StringIO()
    w = buf.write
    w(f"File: {file_path}")
    w(f"Language: {fs.get('language', '')}")
    w(f"Lines of code: {fs.get('loc', 0)}")
    w("")
    imports = fs.get("imports", [])
    if imports:
        w("Imports:")
        for imp in imports:
            w(f"  - {imp['module']} ({imp.get('type', '')})")
        w("")
    funcs = fs.get("functions", [])
    if funcs:
        w(f"Functions: {len(funcs)}")
        for f in funcs[:10]:
            w(f"  - {f['name']}")
        if len(funcs) > 10:
            w(f"  ... and {len(funcs) - 10} more")
        w("")
    classes = fs.get("classes", [])
    if classes:
        w(f"Classes: {len(classes)}")
        for c in classes:
            w(f"  - {c['name']}")
        w("")
    return buf.getvalue()
