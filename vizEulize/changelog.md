# Changelog

## Unreleased

### Fixed

- **Memory after loading a large graph in Chromium browsers.** The worker that decodes the file stayed alive and, in V8, kept its heap: about 800 MB after the Linux kernel's graph (measured with the page's worker in Node). Each load now uses a new worker and terminates it when the graph arrives. Downloads are also written into one buffer sized from `Content-Length` instead of being collected and copied. Firefox already released that memory.

### Added

- **Performance metrics in the console.** Each load logs its stage timings (read, decode, build, first view) and memory; `vizEulize.perf()` prints a snapshot with the JS heap (Chrome and Edge), the graph's memory, what is on screen and recent frame times, and `vizEulize.perf.watch(s)` / `vizEulize.perf.stop()` repeat it.

### Changed

- **Reads `kb_call_graph.fb`.** The worker decodes the parser's FlatBuffers call graph itself (`decodeFB`, no runtime library), checking the file identifier and format version; a truncated file is an error, not a partial graph. It fetches `/data/call_graph.fb`, and **Load call graph** and drag and drop take `.fb` files. In Firefox, Linux's graph (985k nodes) reaches the first view in 3.6 s from the 160 MB `.fb` against 5.6 s from the 591 MB JSON, and Chrome and Edge, whose string limit (about 512 MB) rejects that JSON, can now open it.
- The parser's `--emit-json` copy still loads; its `{key, ...}` lists for tags and file categories are read as maps.

## VizEulize 0.2.0 - 2026-11-17

Changes since the first release, which shipped with Eulix v0.8.3.

### Added

- Help overlay with keybinds, light/dark mode toggle, PNG export, sidebar toggle and a version badge (#140).
- Keyboard shortcuts: `1` to `9` pick an edge type, `j`/`k` move through lists (#140).
- Toast notifications (#140).
- Inheritance focus directions (#140).

### Changed

- Edge colors follow the real edge types, with palette colors for unknown types and hollow arrowheads for inheritance (#140).
- Callers, callees, the leaf filters and the details panel count call edges only, so inheritance is kept apart from calls (#140).

### Fixed

- IDs with the `enum_`, `trait_`, `union_`, `record_`, `annotation_`, `ctor_` and `ifacemethod_` prefixes parse to clean labels and the right categories (#140).
- Nested functions are labeled with their own name instead of the full `outer.inner` path (#146).
- The README names the command `vizeulize` and the file `vizeulize.html` (#157).

_The first release is described in the root [changelog](../changelog.md)._
