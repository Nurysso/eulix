"""Shared theme and drawing helpers for the README images.

Every image draws its own card and swaps colors with an embedded
`prefers-color-scheme` media query, so it reads on GitHub's light and dark
themes. Standard library only.

Regenerate everything from the repository root:

    python3 .github/readme/architecture.py
    python3 .github/readme/bench_parse.py
    python3 .github/readme/bench_memory.py
    python3 .github/readme/bench_callgraph.py

Pass `--theme light` or `--theme dark` with `--out <file>` to preview one mode.
"""

from __future__ import annotations

import argparse
from html import escape
from pathlib import Path

HERE = Path(__file__).resolve().parent

SANS = (
    '-apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans", '
    "Helvetica, Arial, sans-serif"
)
MONO = (
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, '
    '"Liberation Mono", monospace'
)

# role: (light, dark). Surfaces and inks follow GitHub's own palette. The
# accent is the brand violet and the only hue in any image: it marks the
# story (the fast path); everything else stays neutral. Contrast on the
# surface: accent 5.4:1 light / 5.5:1 dark, context gray 3.2:1 / 3.3:1,
# secondary ink 5.7:1 / 5.9:1.
THEME = {
    "surface": ("#f6f8fa", "#161b22"),
    "node": ("#ffffff", "#0d1117"),
    "border": ("#d1d9e0", "#3d444d"),
    "grid": ("#e1e6eb", "#262c36"),
    "axis": ("#afb8c1", "#545d68"),
    "ink": ("#1f2328", "#f0f6fc"),
    "ink2": ("#59636e", "#9198a1"),
    "accent": ("#6250d6", "#9085e9"),
    "context": ("#818b98", "#656c76"),
}

# class name: declaration, filled from THEME per mode.
RULES = {
    "bg": "fill:{surface};stroke:{border}",
    "node": "fill:{node};stroke:{border}",
    "chip": "fill:{surface};stroke:{border}",
    "grid": "stroke:{grid}",
    "axis": "stroke:{axis}",
    "ink": "fill:{ink}",
    "ink2": "fill:{ink2}",
    "accent": "fill:{accent}",
    "trace": "fill:none;stroke:{accent}",
    "context": "fill:{context}",
    "flow": "fill:none;stroke:{ink2}",
    "head": "fill:{ink2}",
}

# Layout estimates run wide on purpose: previews render with DejaVu Sans,
# which is wider than the fonts GitHub's readers see.
SAFETY = 1.1


def n(v: float) -> str:
    """Compact number for SVG attributes."""
    return f"{round(v, 1):g}"


def _rules(mode: int) -> str:
    colors = {role: pair[mode] for role, pair in THEME.items()}
    return "".join(f".{name}{{{decl.format(**colors)}}}" for name, decl in RULES.items())


def style(theme: str) -> str:
    fonts = (
        f".sans{{font-family:{SANS}}}"
        f".mono{{font-family:{MONO}}}"
        ".num{font-variant-numeric:tabular-nums}"
    )
    if theme == "light":
        colors = _rules(0)
    elif theme == "dark":
        colors = _rules(1)
    else:
        colors = _rules(0) + "@media (prefers-color-scheme:dark){" + _rules(1) + "}"
    return f"<style>{fonts}{colors}</style>"


def text_width(s: str, size: float, mono: bool = False) -> float:
    """Rough rendered width of `s`, for layout and wrapping only."""
    if mono:
        return len(s) * 0.6 * size
    em = 0.0
    for ch in s:
        if ch == " ":
            em += 0.28
        elif ch in "il.,:;'|!·":
            em += 0.26
        elif ch in "fjrt()[]{}-/":
            em += 0.36
        elif ch in "mwMW":
            em += 0.86
        elif ch.isupper() or ch in "%&@×<>":
            em += 0.66
        elif ch.isdigit():
            em += 0.56
        else:
            em += 0.54
    return em * size


def wrap(s: str, size: float, max_w: float) -> list[str]:
    lines: list[str] = []
    cur = ""
    for word in s.split():
        trial = f"{cur} {word}".strip()
        if cur and text_width(trial, size) * SAFETY > max_w:
            lines.append(cur)
            cur = word
        else:
            cur = trial
    if cur:
        lines.append(cur)
    return lines


def text(
    x: float,
    y: float,
    s: str,
    *,
    size: float = 13,
    weight: int = 400,
    cls: str = "ink",
    family: str = "sans",
    anchor: str = "start",
) -> str:
    attrs = f'x="{n(x)}" y="{n(y)}" class="{family} {cls}" font-size="{n(size)}"'
    if weight != 400:
        attrs += f' font-weight="{weight}"'
    if anchor != "start":
        attrs += f' text-anchor="{anchor}"'
    return f"<text {attrs}>{escape(s, quote=False)}</text>"


def line(x1: float, y1: float, x2: float, y2: float, cls: str, width: float = 1) -> str:
    return (
        f'<line class="{cls}" x1="{n(x1)}" y1="{n(y1)}" x2="{n(x2)}" y2="{n(y2)}" '
        f'stroke-width="{n(width)}"/>'
    )


def hbar(x: float, y: float, w: float, h: float, cls: str, r: float = 4) -> str:
    """Horizontal bar: square at the baseline, rounded at the data end."""
    r = min(r, w, h / 2)
    return (
        f'<path class="{cls}" d="M{n(x)},{n(y)}H{n(x + w - r)}'
        f"A{n(r)},{n(r)} 0 0 1 {n(x + w)},{n(y + r)}V{n(y + h - r)}"
        f'A{n(r)},{n(r)} 0 0 1 {n(x + w - r)},{n(y + h)}H{n(x)}Z"/>'
    )


def arrow_right(x1: float, x2: float, y: float, cls: str = "flow", head: str = "head") -> str:
    return line(x1, y, x2 - 6, y, cls, 1.5) + (
        f'<path class="{head}" d="M{n(x2)},{n(y)}L{n(x2 - 8)},{n(y - 4.5)}'
        f'L{n(x2 - 8)},{n(y + 4.5)}Z"/>'
    )


def document(width: int, height: float, title: str, desc: str, body: list[str], theme: str) -> str:
    height = int(round(height))
    head = (
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
        f'viewBox="0 0 {width} {height}" role="img" aria-labelledby="title desc">\n'
        f'<title id="title">{escape(title)}</title>\n'
        f'<desc id="desc">{escape(desc)}</desc>\n'
        f"{style(theme)}\n"
        f'<rect class="bg" x="0.5" y="0.5" width="{width - 1}" height="{height - 1}" rx="12"/>\n'
    )
    return head + "\n".join(body) + "\n</svg>\n"


def comparison_chart(
    *,
    title: str,
    subtitle: str,
    rows: list[dict],
    axis_max: float,
    ticks: list[float],
    tick_label,
    footnote: str,
    desc: str,
    theme: str,
    width: int = 800,
) -> str:
    """Two or more horizontal bars on one baseline, one row per measurement.

    Each row: label, sub, value, display, cls ("accent" or "context"), note.
    """
    pad, gutter, right = 28, 196, 76
    x0, x1 = pad + gutter, width - pad - right
    scale = (x1 - x0) / axis_max
    row_h, bar_h = 58, 24

    body = [text(pad, 44, title, size=18, weight=600)]
    y = 68.0
    for part in wrap(subtitle, 13, width - 2 * pad):
        body.append(text(pad, y, part, cls="ink2"))
        y += 18
    top = y + 12
    bottom = top + row_h * len(rows)

    for t in ticks:
        x = x0 + t * scale
        body.append(line(x, top, x, bottom, "axis" if t == 0 else "grid"))
        body.append(text(x, bottom + 20, tick_label(t), size=12, cls="ink2 num", anchor="middle"))

    for i, row in enumerate(rows):
        cy = top + row_h * i + row_h / 2
        body.append(text(x0 - 14, cy - 2, row["label"], size=14, weight=600, anchor="end"))
        body.append(text(x0 - 14, cy + 15, row["sub"], size=12, cls="ink2", anchor="end"))
        w = row["value"] * scale
        body.append(hbar(x0, cy - bar_h / 2, w, bar_h, row["cls"]))
        vx = x0 + w + 10
        body.append(text(vx, cy + 5, row["display"], size=14, weight=600))
        if row.get("note"):
            nx = vx + text_width(row["display"], 14) * SAFETY + 10
            body.append(text(nx, cy + 5, row["note"], cls="ink2"))

    y = bottom + 52
    for part in wrap(footnote, 12, width - 2 * pad):
        body.append(text(pad, y, part, size=12, cls="ink2"))
        y += 17
    return document(width, y + 8, title, desc, body, theme)


def cli(default_name: str) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=f"Write {default_name} for the README.")
    parser.add_argument(
        "--theme",
        choices=("auto", "light", "dark"),
        default="auto",
        help="auto follows the viewer's color scheme; light or dark forces one (for previews)",
    )
    parser.add_argument("--out", type=Path, default=HERE / default_name, help="output path")
    return parser.parse_args()


def save(path: Path, svg: str) -> None:
    path.write_text(svg, encoding="utf-8")
    print(f"wrote {path}")
