# vizEulize

> A browser-based call-graph explorer: interactive, searchable, and entirely local.

| Language | Version | Status | Docs |
| :-- | :-- | :-- | :-- |
| HTML/JS (page), Go (command) | 0.2.0 | Stable | [docs/vizEulize](../docs/vizEulize/Overview.md) |

`eulix vizeulize` loads `.eulix/kb_call_graph.fb` and renders it as an interactive graph in your browser. That one file is all it needs. It comes in two small pieces:

- **`vizeulize.html`**: a single self-contained web page (no build step, no dependencies, no network calls) that draws the call graph on a canvas.
- **`vizeulize` command** (Go, cobra): starts a local HTTP server on `127.0.0.1` with a random free port, serves the page and the call graph, and opens your default browser.

Everything stays on your machine. Nothing is uploaded.

## Install

The install script places the page at `~/.Eulix/bin/vizeulize.html`; the command is part of [`eulix`](../eulix-cli/). If you build from source, copy `vizeulize.html` there yourself.

## Usage

```bash
eulix analyze     # produces .eulix/kb_call_graph.fb
eulix vizeulize   # opens the explorer
```

By default the command reads `.eulix/kb_call_graph.fb` and leaves out nodes from hidden or `.euignore`d files; override the file with `--graph <path>`, which is shown whole. The index file (`kb_index.json`) is no longer used; `--index` is still accepted but has no effect.

The command must keep running while you use the explorer. Press `Ctrl+C` to stop the server.

### Server routes

| Route | Serves |
| :-- | :-- |
| `/` | `~/.Eulix/bin/vizeulize.html` |
| `/data/call_graph.fb` | The call graph (FlatBuffers) |

The page also tries `/data/kb_call_graph.fb` as a fallback.

### Manual loading

Without the server (for example, opening the HTML file directly), use **Load call graph** or drag and drop `kb_call_graph.fb` onto the page. The parser's `--emit-json` copy, `kb_call_graph.json`, also loads.

## Using the explorer

**Views**

- The default view shows the most-connected nodes, up to the node limit.
- Double-click a node, or click it in the list, to focus its neighborhood. Choose the direction (both, callees or callers) and the depth (1 to 4).
- **Back** returns through your focus history. **Reset** clears filters and focus. **Fit** zooms to the visible nodes.

**Filters**

- Search by function name or file path.
- Filter by file category and function tag (read from the call graph file).
- Show only entry points or unused code (no callers), leaf functions (no callees), or hide external nodes.
- Click an edge type in the legend to hide or show it.

**Reading the graph**

- Node color shows the kind: function, method, type, file, module, package or external. External nodes are calls to things not defined in the analyzed project.
- A red ring marks a node with no callers. Test functions usually show this, since the test runner calls them.
- A dashed edge is a conditional call.
- Click a node for details: category, tags, callees and callers.
- In the details lists, a **conditional** label next to a name means that one call happens only inside a branch, loop or similar, not on every path through the function. The label belongs to the single edge between the selected node and that name.
- Hover a node to see its file and in/out edge counts.

**Controls**

| Action | Input |
| :-- | :-- |
| Select node | Click |
| Focus neighborhood | Double-click |
| Move node | Drag node |
| Pan | Drag background |
| Zoom | Scroll |
| Search | `/` |
| Deselect | `Esc` |

The page also has a help overlay with every keybind, a light/dark toggle, PNG export and a sidebar toggle.

## Data format

```json
{
  "nodes": [
    {
      "id": "func_init::eulix-cli/cmd/eulix/main.go",
      "node_type": "function",
      "file": "eulix-cli/cmd/eulix/main.go"
    }
  ],
  "edges": [{ "from": "<node id>", "to": "<node id>", "edge_type": "call", "conditional": false }],
  "tags": { "<node id>": ["exported", "testing"] },
  "file_categories": { "eulix-cli/cmd/eulix/main.go": "cli" }
}
```

- `tags` (node id to tags) and `file_categories` (file path to category) are optional. Without them the graph still loads, but the tag and category filters are empty.
- Node ids of the form `kind_Name::path` are parsed for name and file when those fields are missing.
- Nodes that appear only in edges are treated as external.
- A tag whose id matches no node is ignored. If the tag filters are empty, check that the tag keys use the same id format as the node ids.
- `source`/`target` and `caller`/`callee` are accepted in place of `from`/`to`.

Graphs generated before tags and categories were added to the call graph file still load. Re-run `eulix analyze` to get the tag and category filters.

## Performance

- FlatBuffers decoding and graph construction run in a Web Worker, so the page stays responsive on large files.
- The graph is stored in compact typed arrays (CSR adjacency), so memory use stays low.
- Only a limited number of nodes are laid out and drawn at a time (80, 150, 300 or 600). Use search, filters and focus to reach the part of the graph you need.
- Tags and categories live in the call graph file, so the large index file is never downloaded or parsed.

Measured in Firefox: the Linux kernel graph (985k nodes, 2.2M edges) reaches the first view in 3.6 s from its 160 MB `.fb` and 5.6 s from its 591 MB JSON copy; the Kubernetes graph (119k nodes) in 0.65 s from 22 MB. Chrome loads the Linux `.fb` with about 273 MB of JS heap in use; Firefox's `about:memory` shows about 550 MB for the whole tab. Earlier, a 411 MB `kb_call_graph.json` was tested in a browser and the default top-connections view stayed usable.

In the browser console, `vizEulize.perf()` prints memory and timings and `vizEulize.perf.watch(2)` repeats it every 2 seconds (`vizEulize.perf.stop()` ends it); each load also logs its stage timings. See [Overview](../docs/vizEulize/Overview.md#performance-metrics-in-the-console).

- **JSON size in Chrome and Edge:** their JavaScript engine cannot build a string longer than about 512 MB, so a JSON copy over roughly 500 MB (Linux's is 591 MB) fails with "Invalid string length", whatever the available memory. Firefox allows about 1 GB and opens it, in 5.6 s against 3.6 s for the `.fb`. The `.fb` file has no such limit.
- **Focus on hub nodes:** focusing a node is slower on a graph this size. Expanding the neighborhood of a highly connected node can pull in many nodes, and the force layout cost grows with the square of the visible node count. Keep the node limit and focus depth low (limit 150, depth 1 to 2) when exploring hub functions.

## Requirements

- A modern browser (Chrome, Firefox, Safari, Edge) with Web Worker and `ResizeObserver` support.
- Go and `github.com/spf13/cobra` to build the command.

## Related components

| Component | Language | Role |
| :-- | :-- | :-- |
| [libeulix](../libeulix/) | Rust | Analysis engine: parsing, PRISM call graphs, indexes |
| [eulix-parser](../eulix-parser/) | Rust | CLI that runs libeulix over a repository and writes `.eulix/` |
| [eulix-embed](../eulix-embed/) | Python | Embedding pipeline for the ETIYA engine |
| [eulix-cli](../eulix-cli/) | Go | The `eulix` binary: retrieval engines, MCP server, chat |
| [eulix-lsp](../eulix-lsp/) | Rust | Language server built on libeulix (beta) |
| [vizEulize](../vizEulize/) | HTML/JS | Call-graph explorer in your browser |

## License

Apache 2.0, like every Eulix component. See [LICENSE](../LICENSE).
