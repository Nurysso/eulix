# vizEulize Internals

This page explains how `vizeulize.html` works inside, for anyone who needs to fix a bug or add a feature.

> **Crate / path:** `vizEulize/vizeulize.html`, `vizEulize/pid.test.mjs`  
> **Language:** HTML, CSS and JavaScript (no dependencies)  
> **Status:** stable, version 0.2.0  
> **Last verified against:** `ad744e9`

## Overview

The whole explorer is one HTML file of roughly 630 lines. It contains the styles, the page, a background worker (stored as inline text), and the main script. There is no build step: what you edit is what runs. The version number lives in a single `<meta name="version">` tag, and the page, the help dialog and the browser console all read it from there.

The design has one central idea: **keep a compact copy of the entire graph in memory, but only ever lay out and draw a small slice of it.** The compact copy is built once, in a worker. The slice is rebuilt whenever you search, filter or focus.

## Why It Exists

A call graph file can be hundreds of megabytes. Parsing that on the main thread would freeze the page, and storing it as one JavaScript object per node and edge would use far more memory than the file itself. The structure below avoids both problems.

## How It Works

### Loading

`auto()` runs when the page starts. If the page was opened from a file (`file:`), it cannot fetch anything, so it asks you to load a file. Otherwise it tries `/data/call_graph.json` and then `/data/kb_call_graph.json`, streaming the download so that it can show a progress bar. You can also load files with the file chooser or by dropping them on the page.

The downloaded bytes are transferred (not copied) to a **Web Worker**. The worker is created from the text of a `<script type="text/plain">` block turned into a Blob URL, which is how a single file can contain a worker. It decodes the bytes to a string, parses the JSON, builds the graph structure, and transfers the resulting typed arrays back. If the JSON has neither `nodes` nor `edges`, the page reports an unrecognized file.

### The compact graph

The worker builds the graph as arrays of numbers indexed by node, not as objects:

| Data | Form | Purpose |
| --- | --- | --- |
| `ids`, `names`, `files` | string arrays | The node id, its display name and its file. |
| `cat` | `Uint8Array` | Display category: function, method, type, file, module, package or external. |
| `eF`, `eT`, `eY`, `eC` | typed arrays | For every edge: source node, target node, edge type, and conditional flag. |
| `oS`, `oE`, `iS`, `iE` | `Int32Array` | **CSR adjacency**: for each node, where its outgoing and incoming edges start, and which edges they are. |
| `ci`, `co` | `Int32Array` | Incoming and outgoing counts of call-like edges only. |
| `tk` | `Uint8Array` | Per edge type: `1` if it is inheritance, `0` otherwise. |
| `tg`, `fc` | arrays | Tags per node, and the category index of each node's file. |

CSR (compressed sparse row) is a standard way to store a graph. All edges of node *n* sit next to each other in one array, between `oS[n]` and `oS[n+1]`, so walking a node's neighbours is a simple loop with no lookups and no allocation.

**Names from ids.** Nodes may arrive with only an id such as `func_helper::app/models.py`. The worker splits ids of the form `kind_name::path` using a list of known kinds, and builds the display name and file from them. Nested functions, whose ids look like `func_outer.inner::a.rs`, are labelled by their own name (`inner`), and a trailing `@42` marker is removed. Method ids keep the class in the label (`Router_handle`). The function that does this is `pid`, and `pid.test.mjs` covers it.

**External nodes.** A node that appears only in an edge, and not in the node list, is created on the fly and treated as external.

**Edge types.** Edge types are not hard-coded. Each distinct `edge_type` string gets an index, so a call graph with extra types still loads. Types matching `inheritance`, `inherits`, `extends` or `implements` are flagged as inheritance, and everything else counts as a call. The page also accepts `source`/`target` or `caller`/`callee` in place of `from`/`to`, and `type` in place of `edge_type`.

### Building the view

On the main thread, `setGraph` computes each node's total degree and sorts all nodes by it, once. After that, `rebuild()` decides which nodes to show:

- **No focus:** walk the sorted list and take the first nodes that pass the current filters, up to the limit.
- **Focus:** start at the focused node and expand breadth-first through the chosen direction, one depth level at a time, until the depth or the limit is reached.

Each chosen node gets a start position on a rough circle with some random jitter, and the layout takes over.

### Layout

The layout is a small force simulation, run once per animation frame:

- Every pair of nodes repels with a force proportional to `3500 / distance²`.
- Every edge acts like a spring with a rest length of 110 pixels.
- A weak pull toward the centre keeps the group together.
- Velocity is damped (multiplied by 0.6) each step, and a "temperature" value `alpha` shrinks by 1.5% per frame.

When `alpha` falls below 0.3 after a rebuild, the view is fitted to the screen. When it falls below 0.01, the simulation stops and the loop only redraws when something changes. Dragging a node, or pressing `Space`, reheats it. Because every pair of nodes is compared, the cost per frame grows with the square of the node count, which is why the limits go no higher than 600.

### Drawing

Drawing uses a single 2D canvas, scaled for high-density screens. Edges are drawn first (dashed if conditional, with a filled arrowhead for calls and a hollow one for inheritance), then nodes. When a node is selected, nodes and edges not connected to it are drawn faint. Labels appear only when zoomed in past 0.7, when fewer than 80 nodes are shown, or for the selected node's neighbours and the hovered node, so a crowded view stays readable.

### Main Components

| Part | What it does |
| --- | --- |
| `#wk` script block | The worker source: `pid`, `graph()` and the message handler. |
| `ingest`, `setGraph` | Hand files to the worker and store the result. |
| `nbrs` | Walks a node's neighbours in a chosen direction, using the CSR arrays. |
| `mk` | Builds the filter function from the search box and dropdowns. |
| `rebuild`, `link` | Choose the visible nodes and edges. |
| `tick`, `draw` | Layout step and canvas drawing. |
| `showDet` | Fills the details panel. |

### Data In, Data Out

In: a call graph JSON with `nodes`, `edges`, and optional `tags` and `file_categories` maps. Out: pixels, and the optional PNG.

## Key Algorithms

### Why filters fade nodes in focus mode

Without a focus, a filter decides *which* nodes enter the view. With a focus, the neighbourhood is already chosen, and removing nodes would break the paths you are looking at. So in focus mode the filter only changes how strongly a node is drawn (non-matching nodes are drawn at 40% strength).

### "No callers" counts calls only

A node's red ring, the "no callers" filter and the "leaf" filter use `ci` and `co`, which leave out inheritance edges. A class that is only subclassed would otherwise look as if something called it.

## Configuration

| Setting | Where | Value | Meaning |
| --- | --- | --- | --- |
| Node limits | `#lim` dropdown | 80 / 150 / 300 / 600 | The most nodes laid out and drawn. |
| Depth | `#depth` dropdown | 1 to 4 | Focus depth. |
| Search delay | `qt` timer | 200 ms | Wait after typing before rebuilding. |
| Zoom range | `zoomBy` | 0.1 to 6 | Allowed zoom factors. |
| Theme key | `localStorage` | `vizeulize.theme` | Remembers light or dark. Reading and writing it are wrapped in `try`, so the page works when storage is blocked. |

## Usage Examples

Run the test for the id parser:

```bash
node --test vizEulize/pid.test.mjs
```

Expected result: `tests 2`, `pass 2`, `fail 0`. I ran it while writing this page and it passed.

The test does not import the page. It reads `vizeulize.html`, keeps only the lines that start with `const K=`, `const A=`, `const nest=` or `const pid=`, and evaluates them. This works only while each of those four definitions stays on its own line starting in the first column, so keep that layout if you edit them.

## Performance Notes

Typed arrays and CSR keep memory close to the size of the data. Parsing happens off the main thread, and the buffer is transferred rather than copied. The biggest remaining costs are decoding the whole file to one string (which sets the roughly 500 MB limit) and the all-pairs layout step.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Maximum file size of about 500 MB. | One string must hold the whole JSON. | A streaming parser would be needed; none is used today. |
| Quadratic layout. | Every pair of visible nodes is compared. | A spatial index or a faster layout would be the fix. |
| Loading several graphs keeps only the last. | `setGraph` replaces the current graph for each file. | Load one at a time. |
| The test depends on source layout. | It extracts lines from the HTML by prefix. | Keep the four definitions on separate lines at column 0, or move them to a module. |

## Related Docs

- [Overview](Overview.md): starting the explorer.
- [Using the Explorer](Using-The-Explorer.md): the user's view of these features.
- [Output Files](../eulix-parser/Output-Files.md): the call graph file format.
- [Knowledge Base Schema](../libeulix/Knowledge-Base-Schema.md): node and edge fields.
