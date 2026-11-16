"""architecture.svg: the Eulix pipeline, eulix-parser -> eulix-embed -> eulix-cli -> MCP.

The violet trace is the EHNAS path, which skips the embedding stage. PRISM v2
and EHNAS, the two engines Eulix is built around, are named in violet too.
"""

from html import escape

from _svg import arrow_right, cli, document, line, n, save, text, text_width

WIDTH = 1000

NODES = [
    {
        "name": "eulix-parser",
        "chips": ["Rust"],
        "lines": ["Tree-sitter parsing", [("PRISM v2", "accent"), (" call graph", "")], "Incremental cache"],
        "out": ".eulix/kb*.json",
    },
    {
        "name": "eulix-embed",
        "chips": ["Python", "ETIYA only"],
        "lines": ["ONNX or PyTorch", "GPU or CPU", "SQ8 vectors"],
        "out": ".eulix/embeddings.bin",
    },
    {
        "name": "eulix-cli",
        "chips": ["Go"],
        "lines": ["Query router", [("EHNAS", "accent"), (" · ETIYA engines", "")], "Budgeted MMR"],
        "out": "reads .eulix/",
    },
    {
        "name": "MCP server",
        "chips": ["stdio"],
        "lines": ["4 tools, 2 prompts", "Ranked context", "Session memory"],
        "out": "→ your AI agent",
    },
]

NODE_W, NODE_H, GAP = 206, 168, 42
TOP = 150


def node_x(i: int) -> float:
    total = len(NODES) * NODE_W + (len(NODES) - 1) * GAP
    return (WIDTH - total) / 2 + i * (NODE_W + GAP)


def draw_node(i: int, node: dict) -> list[str]:
    x = node_x(i)
    out = [
        f'<rect class="node" x="{x}" y="{TOP}" width="{NODE_W}" height="{NODE_H}" rx="10"/>',
        text(x + 16, TOP + 32, node["name"], size=15, weight=600, family="mono"),
    ]
    cx = x + 16
    for chip in node["chips"]:
        w = text_width(chip, 11.5) * 1.1 + 16
        out.append(f'<rect class="chip" x="{cx}" y="{TOP + 44}" width="{w:.1f}" height="20" rx="10"/>')
        out.append(text(cx + w / 2, TOP + 58, chip, size=11.5, cls="ink2", anchor="middle"))
        cx += w + 6
    out.append(line(x + 16, TOP + 78, x + NODE_W - 16, TOP + 78, "grid"))
    for j, body in enumerate(node["lines"]):
        y = TOP + 102 + j * 22
        if isinstance(body, str):
            out.append(text(x + 16, y, body, cls="ink2"))
        else:  # segments: (text, class); an accent segment is a highlighted engine name
            spans = "".join(
                f'<tspan class="{cls}" font-weight="600">{escape(t, quote=False)}</tspan>' if cls
                else escape(t, quote=False)
                for t, cls in body
            )
            out.append(f'<text x="{n(x + 16)}" y="{n(y)}" class="sans ink2" font-size="13">{spans}</text>')
    out.append(
        text(x + NODE_W / 2, TOP + NODE_H + 26, node["out"], size=12, cls="ink2", family="mono", anchor="middle")
    )
    return out


def build(theme: str) -> str:
    body = [
        text(28, 44, "From repository to agent context", size=18, weight=600),
        text(
            28,
            68,
            "One Go binary ships the Rust parser and the Python embedder. Everything runs on your machine.",
            cls="ink2",
        ),
    ]

    # The EHNAS trace: parser straight to the CLI, arching over the embedder.
    px = node_x(0) + NODE_W / 2
    cx = node_x(2) + NODE_W / 2
    lift = 58
    body.append(
        f'<path class="trace" stroke-width="2" stroke-linecap="round" '
        f'd="M{px},{TOP - 2}C{px},{TOP - lift} {cx},{TOP - lift} {cx},{TOP - 9}"/>'
    )
    body.append(f'<path class="accent" d="M{cx},{TOP - 1}L{cx - 5},{TOP - 10}L{cx + 5},{TOP - 10}Z"/>')
    label = "EHNAS skips embedding"
    apex_y = TOP - 0.75 * lift
    lw = text_width(label, 13) * 1.1 + 40
    mid = (px + cx) / 2
    body.append(
        f'<rect class="chip" x="{mid - lw / 2:.1f}" y="{apex_y - 13:.1f}" width="{lw:.1f}" height="26" rx="13"/>'
    )
    body.append(f'<circle class="accent" cx="{mid - lw / 2 + 16:.1f}" cy="{apex_y:.1f}" r="4"/>')
    body.append(text(mid - lw / 2 + 28, apex_y + 4.5, label))

    for i, node in enumerate(NODES):
        body += draw_node(i, node)
        if i < len(NODES) - 1:
            body.append(arrow_right(node_x(i) + NODE_W + 4, node_x(i + 1) - 4, TOP + NODE_H / 2))

    foot_y = TOP + NODE_H + 70
    body.append(
        text(
            28,
            foot_y,
            "eulix-cli also serves eulix chat (terminal Q&A) and eulix vizeulize (call-graph explorer) "
            "from the same index.",
            size=12,
            cls="ink2",
        )
    )
    desc = (
        "Pipeline diagram. eulix-parser (Rust) parses the repository with Tree-sitter, builds the "
        "PRISM v2 call graph and writes .eulix/kb*.json. eulix-embed (Python, ETIYA only) embeds "
        "chunks with ONNX Runtime or PyTorch into .eulix/embeddings.bin. eulix-cli (Go) routes "
        "queries through the ETIYA or EHNAS engine. The MCP server hands ranked context to your "
        "AI agent. EHNAS skips the embedding stage."
    )
    return document(WIDTH, foot_y + 28, "Eulix architecture", desc, body, theme)


if __name__ == "__main__":
    args = cli("architecture.svg")
    save(args.out, build(args.theme))
