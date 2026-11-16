"""bench_parse.svg: full vs incremental parse of the Linux kernel.

Numbers:
- Full parse: 53.9 s (median of 3 cold runs: 52.2, 53.9, 54.7 s) for 37,322,695
  lines in 64,460 source files, 12 threads, PRISM v2, eulix-parser v0.9.0,
  measured with scripts/parser_perf.sh (the parser benchmark in the root README).
- Incremental re-parse of the same tree: under 10 s (earlier measurement). Unchanged files come from
  .eulix/parse_cache/; most of the remaining time is rewriting the JSON output.
"""

from _svg import cli, comparison_chart, save

FULL_PARSE_S = 53.9
INCREMENTAL_MAX_S = 10.0  # an upper bound, drawn at the bound and labelled "< 10 s"


def build(theme: str) -> str:
    speedup = int(FULL_PARSE_S / INCREMENTAL_MAX_S * 10) / 10  # 5.39 -> 5.3, rounded down
    return comparison_chart(
        title="Parsing the Linux kernel",
        subtitle="37.3M lines · 64,460 source files · 12 threads · PRISM v2 · seconds, lower is better",
        rows=[
            {
                "label": "Full parse",
                "sub": "every file, cold",
                "value": FULL_PARSE_S,
                "display": f"{FULL_PARSE_S:g} s",
                "cls": "context",
            },
            {
                "label": "Incremental re-parse",
                "sub": "unchanged files reused",
                "value": INCREMENTAL_MAX_S,
                "display": f"< {INCREMENTAL_MAX_S:g} s",
                "cls": "accent",
                "note": f"over {speedup:g}× faster",
            },
        ],
        axis_max=60,
        ticks=[0, 10, 20, 30, 40, 50, 60],
        tick_label=lambda t: f"{t:g} s" if t == 60 else f"{t:g}",
        footnote=(
            "Incremental runs reuse unchanged files from .eulix/parse_cache/ and still rebuild "
            "the call graph from scratch, so the output matches a full parse byte for byte. "
            "Most of the time left is spent rewriting the JSON output."
        ),
        desc=(
            "Horizontal bar chart. A full parse of the Linux kernel takes 53.9 seconds. "
            f"An incremental re-parse takes under 10 seconds, over {speedup:g} times faster."
        ),
        theme=theme,
    )


if __name__ == "__main__":
    args = cli("bench_parse.svg")
    save(args.out, build(args.theme))
