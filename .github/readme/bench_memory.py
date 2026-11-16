"""bench_memory.svg: MCP server memory, EHNAS vs ETIYA.

Numbers: memory of the whole `eulix mcp` process tree (ETIYA's includes its
Python embedding helper), eulix v0.9.0 at f9006ea, from the "Query speed"
table in bench/results/v0.8.3-vs-f9006ea.md. Each bar is drawn at the
midpoint of the engine's range across the five repos.
"""

from _svg import cli, comparison_chart, save

MEMORY_MB = {
    "ETIYA": {"eulix": 956, "flask": 943, "gin": 930, "ripgrep": 949, "gson": 964},
    "EHNAS": {"eulix": 89, "flask": 81, "gin": 83, "ripgrep": 84, "gson": 83},
}


def summary(engine: str) -> tuple[int, int, float]:
    values = MEMORY_MB[engine].values()
    lo, hi = min(values), max(values)
    return lo, hi, (lo + hi) / 2


def build(theme: str) -> str:
    e_lo, e_hi, etiya = summary("ETIYA")  # 930, 964 -> 947
    h_lo, h_hi, ehnas = summary("EHNAS")  # 81, 89 -> 85
    ratio = round(etiya / ehnas)  # 11
    return comparison_chart(
        title="MCP server memory by retrieval engine",
        subtitle=(
            "Whole server process tree · eulix v0.9.0 on five repos "
            "(eulix, flask, gin, ripgrep, gson) · MB, lower is better"
        ),
        rows=[
            {
                "label": "ETIYA",
                "sub": f"range {e_lo}–{e_hi} MB",
                "value": etiya,
                "display": f"{etiya:g} MB",
                "cls": "context",
            },
            {
                "label": "EHNAS",
                "sub": f"range {h_lo}–{h_hi} MB",
                "value": ehnas,
                "display": f"{ehnas:g} MB",
                "cls": "accent",
                "note": f"about {ratio}× less memory",
            },
        ],
        axis_max=1000,
        ticks=[0, 250, 500, 750, 1000],
        tick_label=lambda t: "1,000 MB" if t == 1000 else f"{t:g}",
        footnote=(
            "Bars mark the midpoint of each engine's range across the five repos. "
            "ETIYA's figure includes its Python embedding helper; EHNAS loads no model at all."
        ),
        desc=(
            f"Horizontal bar chart. ETIYA uses {etiya:g} MB (range {e_lo} to {e_hi}). "
            f"EHNAS uses {ehnas:g} MB (range {h_lo} to {h_hi}), about {ratio} times less memory."
        ),
        theme=theme,
    )


if __name__ == "__main__":
    args = cli("bench_memory.svg")
    save(args.out, build(args.theme))
