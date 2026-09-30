# Changelog

## Eulix CLI 0.8.2 - 2026-09-29

### Features (`6c5f9d4`)
- **hydration**: Updated the hydration algorithm within retrieval for improved performance and data processing.
- **checksum**: Updated `checksum` package to read directly from `eulix_parser` output instead of generating `checksum.json.zst`.
- **config**: Moved project-wide configuration constants into `utils/constants`.

### Performance & Parser Improvements
- **retrieval / parser** (`81294c30`): Updated parser to output smaller files for retrieval. This resulted in:
  - Faster retrieval speed without any loss in accuracy.
  -  Reduced peak RAM/RSS usage when loading files into memory.
- **query**(`acef602`): Skipped semantic search optimizations for `callers` and `callees` intent queries.

### Refactoring & Cleanups
- **query**(`087a0f5`): Broken down monolithic query package into smaller, maintainable modules.
- **query**(`1e9a67f`): Rewrote MMR, added anchor pinning, and performed symbol cleanup.
- **kbstruct**(`63f6164`): Synced `kbstruct.go` to be a direct 1:1 match with the `eulix_parser` struct.
- **deprecations**(`6c5f9d4`): Deprecated `glados` and `aspirine` modules.

### Tests
- **query**(`63f6164`): Added test suites for classifier and router components.
