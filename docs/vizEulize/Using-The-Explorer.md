# Using the Explorer

This page is a guide to using vizEulize once it is open: how to find things, how to read what you see, and which keys and mouse actions to use.

> **Crate / path:** `vizEulize/vizeulize.html`  
> **Language:** HTML and JavaScript  
> **Status:** stable, version 0.2.0  
> **Last verified against:** `ad744e9`

## Overview

The explorer shows a small part of a big graph at a time. You decide which part by searching, filtering, or focusing on one function. This page walks through those tools in the order you will usually reach for them. If you have not started the explorer yet, read the [Overview](Overview.md) first.

## Why It Exists

Showing every function and call of a large repository at once would be an unreadable cloud. The tools described here exist so that you can always reduce the view to a size your eyes can follow, and move from one function to the next without losing your place.

## How It Works

### The default view

When the graph loads, you see the most-connected functions, up to the node limit (150 by default). "Most connected" means the highest number of edges in or out, so hubs appear first. The sidebar on the left lists matching functions in the same order, up to 200 at a time, and says how many more there are.

The status bar at the bottom of the graph tells you how many nodes and edges the whole graph has, how many are on screen, and whether you have hit the limit.

### Focusing on one function

Double-click a node, or click its name in the sidebar, to **focus** on it. The view is rebuilt around that function: the node itself, then its neighbours, then their neighbours, up to the depth you choose, and stopping when the node limit is reached.

Three controls shape a focus:

| Control | Choices | Meaning |
| --- | --- | --- |
| Direction | all edges, callees, callers, calls both ways, inheritance out, inheritance in, inheritance both | Which edges to follow. *Callees* are the functions this one calls; *callers* are the functions that call it. |
| Depth | 1 to 4 | How many steps to follow. |
| Limit | 80, 150, 300, 600 | The most nodes to draw. |

**Back** returns through your focus history. **Reset** clears all filters and the focus. **Fit** zooms so every visible node is on screen.

### Searching and filtering

The search box matches function names and file paths. Other filters narrow the view further:

- **Category** and **tag** filters use the labels stored in the call graph file. Categories describe a file's role (such as tests or utilities) and tags are short labels on functions.
- **Show** can limit the view to nodes with no callers (entry points or unused code), nodes with no callees (leaf functions), or hide external nodes.
- Clicking an **edge type** in the legend hides or shows that type.

When you are focused on a function, a filter does not remove nodes. It fades the nodes that do not match, so that the shape of the neighbourhood stays intact.

### Reading the graph

| What you see | What it means |
| --- | --- |
| Node colour | The kind: function, method, type, file, module, package or external. |
| Node size | Grows with the number of connections. |
| Red ring | The node has outgoing calls but no callers. Test functions often look like this, because a test runner calls them, not other code. |
| Dashed edge | A conditional call: it happens only inside a branch or loop, not on every path. |
| Solid arrowhead | A call. The arrow points at the callee. |
| Hollow arrowhead | Inheritance. |
| Dimmed nodes and edges | When a node is selected, everything not directly connected to it fades. |

Click a node to open its **details panel**. It shows the kind and file, the category and tags, and lists of callees and callers (each limited to the first 40). A small **conditional** label next to a name means that one call is conditional. Click any name in the lists to focus on it. Hover over a node to see its file and the number of incoming and outgoing edges in the bottom bar.

> **Remember what the edges are.** The graph comes from PRISM, which is an approximation. A missing edge does not prove that no call happens, and an edge is strong evidence but not proof. See [PRISM v2](../libeulix/PRISM-V2.md).

### Main Components

The screen has a top bar (load files, PNG, theme, help), a left sidebar (search, filters, controls, legend, list) and the graph area with a details panel and a status bar.

### Data In, Data Out

You can export what you see as a PNG with the **PNG** button or the `s` key. The file is named `vizeulize-<date>-<time>.png`.

## Key Algorithms

None are needed for using the explorer. How the view is built is described in [Internals](Internals.md).

## Configuration

### Mouse

| Action | Input |
| --- | --- |
| Select a node | Click |
| Focus its neighbourhood | Double-click |
| Move a node | Drag the node |
| Pan | Drag the background |
| Zoom | Scroll |

### Keyboard

| Key | Action |
| --- | --- |
| `/` or `Ctrl+K` | Focus the search box. `Enter` there focuses the first match. |
| `?` | Show or hide the shortcut list. |
| `Esc` | Close help, leave a field, or clear the selection. |
| `j` or `↓`, `k` or `↑` | Select the next or previous node in the list. |
| `Enter` | Focus the selected node. |
| `Backspace` or `Alt+←` | Back. |
| `f` | Fit the graph to the view. |
| `+`, `-` | Zoom in or out. |
| `Space` | Run the layout again. |
| `[`, `]` | Decrease or increase focus depth. |
| `d` | Cycle focus direction. |
| `1` to `9` | Show or hide the edge type with that number in the legend. |
| `r` | Reset filters and view. |
| `b` | Show or hide the sidebar. |
| `t` | Switch between light and dark themes. |
| `s` | Save the view as a PNG. |

## Usage Examples

**Find who calls a function.** Press `/`, type the name, press `Enter` to focus the first match, set the direction to *← callers*, and raise the depth to 2 or 3. Click a caller to read its details.

**Find code that nothing calls.** Choose *No callers (entry / unused)* in **Show**. Remember that entry points, tests and code reached by reflection will appear here too.

**See a class hierarchy.** Focus a class and set the direction to *inheritance ↔*.

Expected result in each case: the graph rebuilds around your choice and the bottom bar shows the new node and edge counts.

## Performance Notes

If the graph feels slow after focusing on a hub function, lower the limit to 150 and the depth to 1 or 2. The layout cost rises quickly with the number of drawn nodes.

## Limitations

| Limitation | Why it happens | Workaround |
| --- | --- | --- |
| Lists show at most 200 matches, and details lists at most 40 names. | Keeps the page fast. | Refine the search. |
| The sidebar list is sorted by connections, not alphabetically. | Hubs are usually what you want first. | Search by name. |
| Red rings can mark legitimate entry points. | The page only knows there are no callers in the graph. | Read the function before assuming it is dead code. |
| A tag or category filter can be empty. | The call graph file was made by an older parser without tags and categories, or the tag keys do not match the node ids. | Run `eulix analyze` again with a current parser. |

## Related Docs

- [Overview](Overview.md): starting the explorer.
- [Internals](Internals.md): how the view and layout are built.
