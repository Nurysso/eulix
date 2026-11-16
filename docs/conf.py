"""Sphinx config used only to CHECK the docs in CI (links, anchors, structure).

Build locally:   uv run --with-requirements requirements.txt sphinx-build -W --keep-going -b html . _build/html
Check URLs:      ... sphinx-build -b linkcheck . _build/linkcheck
"""

project = "Eulix"
author = "Eulix contributors"

extensions = ["myst_parser"]
source_suffix = {".md": "markdown"}
root_doc = "index"
html_theme = "furo"

exclude_patterns = ["_build", "Thumbs.db", ".DS_Store", "requirements.txt"]

# Markdown: make `[text](file.md#heading)` anchors checkable.
myst_heading_anchors = 4
myst_enable_extensions = ["colon_fence"]

# Fail the build on anything Sphinx can detect: broken relative links, bad anchors,
# pages missing from the table of contents.
nitpicky = False
suppress_warnings = []

# External link checking (the `linkcheck` builder).
linkcheck_timeout = 20
linkcheck_retries = 2
linkcheck_anchors = False  # GitHub renders anchors with JavaScript; don't false-alarm
linkcheck_ignore = [
    r"https?://localhost.*",
    r"https?://127\.0\.0\.1.*",
    r"https://github\.com/Nurysso/eulix/(issues|releases).*",  # fine in a browser; rate limited in CI
]
