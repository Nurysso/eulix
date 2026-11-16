# Changelog

## VizEulize 0.2.0 - 2026-11-16

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
