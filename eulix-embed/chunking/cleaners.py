# Copyright (C) 2026 Dawood Khan
# SPDX-License-Identifier: Apache-2.0

# Maintainer Dawood (Nurysso) contact - nurysso [at] proton.me

# Chunking module is responsible for chunking files/parts of json file
#
# This file is responsible for stripping docstrings and boilerplate before embedding to remove tokens that dilute
# the semantic signal (license headers, TODOs, dense comments). This is a best‑effort
# regex‑based pass – good enough for embedding input, but never used for correctness‑critical
# operations. Docstring removal is done in‑place on the parsed structure to avoid
# copying large dicts in worker threads.

# USED FOR Experimentation no comments and docstring in embedder and vector
# to see how it effects context window creation,

# yeah this was useless kept it for no reason

# import re


def drop_docstrings(fs: dict) -> None:
    """
    Mutate fs in-place, removing docstring fields before chunking.
    Called in the worker thread inside _submit — fs is exclusively owned
    by that closure so mutation is safe.
    """
    for func in fs.get("functions", []):
        func.pop("docstring", None)
    for cls in fs.get("classes", []):
        cls.pop("docstring", None)
        for method in cls.get("methods", []):
            method.pop("docstring", None)


# def strip_comments(content: str, lang: str) -> str:
#     """
#     Best-effort comment stripper, per language family.
#     Python: removes '# ...' comments with a simple quote-tracking scanner
#         (handles comments after string literals on the same line, but is
#         not a full tokenizer — edge cases like triple-quoted strings
#         containing '#' are not handled).
#     C-like (c/cpp/java/go/javascript/typescript/rust): regex-strips //...
#         and /* ... */ blocks. DOTALL means multi-line block comments are
#         matched greedily; nested block comments are not supported (C-like
#         languages don't nest them anyway).
#     Any other `lang` value: returned unchanged.
#     """
#     if lang == "python":
#         # Remove # comments (but not # inside strings – simple version)
#         lines = []
#         for line in content.splitlines():
#             if "#" in line:
#                 # crude: remove from first # not in quotes
#                 in_string = False
#                 quote_char = None
#                 new_line = []
#                 for i, ch in enumerate(line):
#                     if ch in ('"', "'") and (i == 0 or line[i - 1] != "\\"):
#                         if not in_string:
#                             in_string = True
#                             quote_char = ch
#                         elif ch == quote_char:
#                             in_string = False
#                     elif ch == "#" and not in_string:
#                         break
#                     new_line.append(ch)
#                 lines.append("".join(new_line).rstrip())
#             else:
#                 lines.append(line)
#         return "\n".join(lines)
#     elif lang in ("c", "cpp", "java", "go", "javascript", "typescript", "rust"):
#         # Remove // and /* */ comments
#         # Simple regex (not perfect for strings, but good enough for KB)
#         content = re.sub(r"//.*?$", "", content, flags=re.MULTILINE)
#         content = re.sub(r"/\*.*?\*/", "", content, flags=re.DOTALL)
#         return content
#     return content


# def clean_boilerplate(text: str) -> str:
#     lines = text.splitlines()
#     filtered = []
#     for line in lines:
#         lower = line.lower()
#         if any(
#             x in lower
#             for x in ("license", "copyright", "todo", "fixme", "note:", "author:")
#         ):
#             continue
#         if line.strip().startswith(("// SPDX", "/* SPDX")):
#             continue
#         filtered.append(line)
#     return "\n".join(filtered)


# def clean_content(content: str, lang: str) -> str:
#     """Remove comments and boilerplate from content before truncation."""
#     content = strip_comments(content, lang)  # remove //, /* */, # comments
#     content = clean_boilerplate(content)  # remove license, TODO, etc.
#     return content
