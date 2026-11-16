"""bench_callgraph.svg: call-graph precision, PRISM v2 vs PRISM v1.

Numbers: the per-project table in docs/libeulix/Call-Graph-Accuracy.md
(bench/callgraph harness, eulix at 62fdddf), pooled over its eight corpora,
one per language: gin, gson, flask, ripgrep, class-validator, axios, cJSON,
tinyxml2. Every edge is checked against the call sites that language's own
tooling resolves (go/types, javac, jedi, rust-analyzer, the TypeScript
checker, libclang), 10,694 reference caller -> callee pairs in all.

- PRISM v2: 6,268 correct, 87 wrong, 4,426 missed -> precision 98.6%, recall 58.6%
- PRISM v1: 6,093 correct, 5,060 wrong, 4,601 missed -> precision 54.6%, recall 57.0%
"""

from _svg import cli, comparison_chart, save

PRECISION = {"v2": 98.6, "v1": 54.6}
RECALL = {"v2": 58.6, "v1": 57.0}
EDGES = {"v2": (87, 6355), "v1": (5060, 11153)}  # wrong, drawn


def build(theme: str) -> str:
    rows = []
    # The edge counts go under the label: v2's bar leaves no room to its right.
    for version, cls in (("v2", "accent"), ("v1", "context")):
        wrong, drawn = EDGES[version]
        rows.append(
            {
                "label": f"PRISM {version}",
                "sub": f"{wrong:,} of {drawn:,} edges wrong",
                "value": PRECISION[version],
                "display": f"{PRECISION[version]:.1f}%",
                "cls": cls,
            }
        )
    return comparison_chart(
        title="How many call-graph edges are right",
        subtitle="Precision on 8 open-source projects, one per language, checked against each "
        "language's own compiler tooling · higher is better",
        rows=rows,
        axis_max=100,
        ticks=[0, 25, 50, 75, 100],
        tick_label=lambda t: f"{t:g}%",
        footnote=f"Both find about the same share of the real calls (v2 {RECALL['v2']:.1f}%, "
        f"v1 {RECALL['v1']:.1f}%): v2 leaves an edge out rather than guess. 10,694 reference call "
        "pairs from gin, gson, flask, ripgrep, class-validator, axios, cJSON and tinyxml2. "
        "Per-language results in docs/libeulix/Call-Graph-Accuracy.md.",
        desc=(
            f"Bar chart: {PRECISION['v2']:g}% of the call edges PRISM v2 draws are correct, against "
            f"{PRECISION['v1']:g}% for PRISM v1, at similar recall ({RECALL['v2']:g}% and "
            f"{RECALL['v1']:.1f}%), on eight open-source projects checked against each language's "
            "own compiler tooling."
        ),
        theme=theme,
    )


if __name__ == "__main__":
    args = cli("bench_callgraph.svg")
    save(args.out, build(args.theme))
