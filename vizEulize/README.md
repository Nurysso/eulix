# vizEulize

A browser-based call graph explorer for Eulix, launched with `eulix vizEulize`. It loads `.eulix/kb_call_graph.json` and renders it as an interactive, searchable graph. That one file is all it needs.

## Overview

`vizEulize` is two small pieces:

- **`vizeulize.html`**: a single self-contained web page (no build step, no dependencies, no network calls) that renders the call graph on a canvas.
- **`vizeulize` command** (Go, cobra): starts a local HTTP server on `127.0.0.1` with a random free port, serves the page and the call graph, and opens your default browser.

Everything stays on your machine. Nothing is uploaded.

## Quick start

```bash
# 1. Analyze the project to produce .eulix/kb_call_graph.json
eulix analyze

# 2. Open the explorer
eulix vizEulize
```

The page is read from `~/.Eulix/bin/vizEulize.html`, so make sure it is installed there.

By default the command looks for `.eulix/kb_call_graph.json`. The call graph path can be overridden with the command's path flag. The index file (`kb_index.json`) is no longer used by the explorer.

## Server routes

| Route                   | Serves                        |
| ----------------------- | ----------------------------- |
| `/`                     | `~/.Eulix/bin/vizEulize.html` |
| `/data/call_graph.json` | the call graph file           |

The page also tries `/data/kb_call_graph.json` as a fallback.

The command must keep running while you use the explorer. Press `Ctrl+C` to stop the server.

## Using the explorer

**Views**

- The default view shows the most-connected nodes (up to the node limit).
- Double-click a node, or click it in the list, to focus its neighborhood. Choose direction (both / callees / callers) and depth (1 to 4).
- **Back** returns through your focus history. **Reset** clears filters and focus. **Fit** zooms to the visible nodes.

**Filters**

- Search by function name or file path.
- Filter by file category and function tag (read from the call graph file).
- Show only entry points or unused code (no callers), leaf functions (no callees), or hide external nodes.
- Click an edge type in the legend to hide or show it.

**Reading the graph**

- Node color shows kind: function, method, type, file, module, package, external. External nodes are calls to things that are not defined in the analyzed project.
- A red ring marks a node with no callers. Test functions usually show this, since `go test` runs them.
- A dashed edge is a conditional call.
- Click a node for details: category, tags, callees, and callers.
- In the details lists, a **conditional** label next to a name means that one call happens only inside a branch, loop, or similar, and not on every path through the function. The label belongs to the single edge between the selected node and that name.
- Hover a node to see its file and in/out edge counts.

**Controls**

| Action             | Input           |
| ------------------ | --------------- |
| Select node        | Click           |
| Focus neighborhood | Double-click    |
| Move node          | Drag node       |
| Pan                | Drag background |
| Zoom               | Scroll          |
| Search             | `/`             |
| Deselect           | `Esc`           |

**Manual loading.** Without the server (for example, opening the HTML file directly), use **Load JSON files** or drag and drop `kb_call_graph.json` onto the page.

## Data format

Call graph:

```json
{
  "nodes": [
    {
      "id": "func_init::eulix-cli/cmd/eulix/main.go",
      "node_type": "function",
      "file": "eulix-cli/cmd/eulix/main.go"
    }
  ],
  "edges": [{ "from": "<node id>", "to": "<node id>", "edge_type": "calls", "conditional": false }],
  "tags": { "<node id>": ["exported", "testing"] },
  "file_categories": { "eulix-cli/cmd/eulix/main.go": "cli" }
}
```

- `tags` (node id to tags) and `file_categories` (file path to category) are optional. Without them the graph still loads, but the tag and category filters are empty.
- Node ids of the form `kind_Name::path` are parsed for name and file when those fields are missing.
- Nodes that appear only in edges are treated as external.
- A tag whose id matches no node is ignored. If tag filters are empty, check that the tag keys use the same id format as the node ids.
- `source`/`target` and `caller`/`callee` are accepted in place of `from`/`to`.

Graphs generated before tags and categories were added to the call graph file still load. Re-run `eulix analyze` to get tag and category filters.

## Performance notes

- JSON parsing and graph construction run in a Web Worker, so the page stays responsive on large files.
- The graph is stored in compact typed arrays (CSR adjacency), so memory use stays low.
- Only a limited number of nodes are laid out and drawn at a time (80 / 150 / 300 / 600). Use search, filters, and focus to reach the part of the graph you need.
- Because tags and categories live in the call graph file, the large index file is not downloaded or parsed. Only the call graph is loaded.

### Known limits

Tested with a 411 MB `kb_call_graph.json` (loaded together with a 391 MB index, before the index was dropped). The call graph loads, and the default top-connections view stays usable.

- **File size:** the browser cannot build a single string longer than about 512 MB. A call graph file over roughly 500 MB will fail with "Invalid string length", whatever the available memory.
- **Focus on hub nodes:** focusing a node (double-click) is slower on a graph this size. Expanding a neighborhood around a highly connected node can pull in many nodes, and the force layout cost grows with the square of the visible node count. Keep the node limit and focus depth low (limit 150, depth 1 to 2) when exploring hub functions.

## Requirements

- A modern browser (Chrome, Firefox, Safari, Edge) with Web Worker and `ResizeObserver` support.
- Go and `github.com/spf13/cobra` for the command.
