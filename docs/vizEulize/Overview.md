# vizEulize

vizEulize is a browser-based explorer for the call graph that `eulix_parser` produces. It turns `kb_call_graph.json` into an interactive, searchable graph that you can click through.

> **Crate / path:** `vizEulize/vizeulize.html`, launched by `eulix-cli/internal/cli/commands.go`  
> **Language:** HTML and JavaScript (the launcher is Go)  
> **Status:** stable, version 0.2.0  
> **Last verified against:** `ad744e9`

## Overview

A call graph for a real repository can have hundreds of thousands of nodes, and reading it as JSON is not practical. vizEulize lets you look at it instead. You can search for a function, double-click it to see what it calls and what calls it, and follow the edges outward one step at a time.

It has two parts. The first is **one HTML file**, `vizeulize.html`, with no build step, no dependencies and no network calls. The second is a **launcher command** in `eulix-cli`, which starts a small web server on your own machine, serves the page and the call graph file, and opens your browser. Nothing leaves your computer.

After reading this page you will know how to start the explorer, what it needs, and where to read more. [Using the Explorer](Using-The-Explorer.md) covers day-to-day use, and [Internals](Internals.md) explains how it works.

## Why It Exists

The query side of Eulix answers questions in text, but sometimes you want to see the shape of the code: which functions are hubs, which are never called, and how two modules connect. A graph on screen answers those questions much faster than a list.

The page is a single file on purpose. It can be shipped next to the binary, opened on a machine with no tooling, and read or changed without a build. The cost of that choice is a style of code that is dense and carefully ordered; a note at the top of the file asks contributors to be careful with the CSS and JavaScript blocks.

## How It Works

When you run the launcher, it does four things:

1. It looks for the page at `~/.Eulix/bin/vizeulize.html`.
2. It checks that the call graph file exists. By default this is `.eulix/kb_call_graph.json` in the current project.
3. It starts an HTTP server on `127.0.0.1` with a random free port, and opens that address in your default browser.
4. It keeps running until you press `Ctrl+C`.

In the browser, the page downloads the call graph from the server, hands it to a background worker that parses it into a compact structure, and then draws the most-connected nodes. Everything after that happens in the page itself.

### Main Components

| Component | Location | Responsibility |
| --- | --- | --- |
| `vizeulize.html` | `vizEulize/` | The whole interface: layout, worker, graph logic, drawing, shortcuts. |
| `vizEulizeCMD` | `eulix-cli/internal/cli/commands.go` | The command that serves the page and the data and opens the browser. |
| `pid.test.mjs` | `vizEulize/` | A Node test for the code that turns node ids into readable names. |

### Data In, Data Out

The only input is the call graph file written by `eulix_parser`. The launcher serves two routes:

| Route | Serves |
| --- | --- |
| `/` | `~/.Eulix/bin/vizeulize.html` (only the exact path `/`; anything else returns 404) |
| `/data/call_graph.json` | The call graph file you chose |

The page also tries `/data/kb_call_graph.json` as a second location. The built-in server does not serve that route, so it matters only if you host the files some other way.

The explorer has no output other than what it draws, and an optional PNG of the current view.

## Key Algorithms

The explorer does not use a published graph-layout library. It has three small pieces of its own, all described in [Internals](Internals.md): a compact graph representation built in a background worker, a force-directed layout for the nodes on screen, and a "nearest connections first" view builder. In plain words: it never tries to draw the whole graph. It draws a limited number of nodes at a time, starting with the best-connected, and lets you move through the graph by focusing on one node's neighbourhood.

## Configuration

| Setting | Where | Default | What it does and when to change it |
| --- | --- | --- | --- |
| `--graph <path>` | launcher flag | `.eulix/kb_call_graph.json` | Use a call graph from a different location, such as one made with a custom `--output` name. |
| `--index <path>` | launcher flag | `.eulix/kb_index.json` | Accepted but unused. The explorer no longer reads the index file, because tags and categories now come from the call graph file. |
| Node limit | in the page | 150 | How many nodes are laid out and drawn: 80, 150, 300 or 600. |
| Theme | in the page | system | Light or dark. Your choice is remembered in the browser. |

## Usage Examples

```bash
# 1. Produce the call graph (writes .eulix/kb_call_graph.json)
eulix analyze

# 2. Open the explorer
eulix vizeulize
```

Expected result: the terminal prints `Launched vizEulize at http://127.0.0.1:<port>/` and `Serving call graph. Press Ctrl+C to stop.`, and your browser opens on the graph.

To open a graph from another location:

```bash
eulix vizeulize --graph out/kb_call_graph.json
```

If you cannot use the launcher, open `vizeulize.html` directly in a browser and use **Load JSON files**, or drag `kb_call_graph.json` onto the page. A page opened from a file cannot download the graph on its own, so it shows a message asking you to load one.

> **A note on names.** The subsystem is called vizEulize, but the command and the HTML file are lowercase: `eulix vizeulize` and `vizeulize.html`. Command names in this CLI framework are case-sensitive, so use the lowercase form. I could not run the command here to confirm, because the installed `eulix` stopped at its parser integrity check first.

## Performance Notes

The graph is parsed off the main thread, so the page stays responsive while a large file loads. It is then stored in typed arrays rather than objects, which keeps memory low. The README reports testing with a 411 MB call graph, which loaded and gave a usable default view. I did not repeat that test.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| A call graph larger than about 500 MB fails with "Invalid string length". | The browser cannot build a single string longer than about 512 MB, and the file is decoded to one string. | Analyze a smaller part of the project, using `.euignore`. |
| Focusing on a very connected function can be slow on huge graphs. | The layout compares every drawn node with every other one, so cost grows with the square of the node count. | Keep the node limit near 150 and the depth at 1 or 2 when exploring hubs. |
| If you load several graph files at once, only the last one is kept. | Each file replaces the previous graph. | Load one call graph at a time. |
| The page needs a modern browser. | It uses Web Workers and `ResizeObserver`. | Use a current Chrome, Firefox, Safari or Edge. |
| "External" nodes are unlikely with Eulix's own graphs. | Both PRISM versions only create edges between nodes that exist. External nodes appear when an edge names a node that is missing from the node list, which other tools may produce. | None needed. |

## Related Docs

- [Using the Explorer](Using-The-Explorer.md): views, filters, shortcuts.
- [Internals](Internals.md): how the page works and how to change it.
- [Output Files](../eulix-parser/Output-Files.md): where `kb_call_graph.json` comes from.
- [PRISM v2](../libeulix/PRISM-V2.md): how the edges in the graph are decided, and why they are approximate.
