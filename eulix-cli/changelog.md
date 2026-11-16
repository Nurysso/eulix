# Changelog

## Eulix CLI 0.9.0 - 2026-11-17

Changes since v0.8.3.

### Added

- **EHNAS retrieval engine** (Exact · Hierarchical · Node · Structure), alongside etiya (#153). It does symbol hygiene, explicit anchor localization, multi-strategy scoring (`kb_exact`, `grep`, `exact`, `partial`, `keyword`, `semantic`, `monte_carlo`), subsystem and scope demotion, and context packing. Retrieval engines now have engine-specific configuration.
- `[retrievalConfig.etiya] lexical_exclude`: globs (matched against the repo-relative path or the file name) and `dir/` prefixes that the MCP grep fallback skips while etiya is the active engine, so eval scripts and docs that quote benchmark questions can't outrank real code (#153).
- **PHP in retrieval:**
  - comment stripping for `//`, `#` and `/* */`, leaving inline HTML, strings (with `{$a["k"]}` interpolation), heredoc/nowdoc bodies and `#[...]` attributes alone. A `.inc` file is stripped as PHP only when it has a PHP open tag, since PHP's `#` comments would eat C's `#include` lines (#85, `cc27f09`);
  - a PHP signature reader that keeps attributes, modifiers, parameters with defaults and the return type, and stops at the first top-level `{` or `;`. PHP used to fall to the Go reader, which ran on into the next declaration after an abstract or interface method (#90);
  - `.phtml` and `.inc` are recognized as source paths in a query ("explain store.inc"), next to `.php` (#90).

### Changed

- A repository's `.env` is no longer exported into the process environment. It is read only for the provider key, after the real environment, with the same precedence as before (#116).
- `eulix setup` locks `.env` to the owner before writing secrets (a protected DACL on Windows, `0600` elsewhere) and aborts if it can't. Values godotenv would misread are quoted, values it couldn't read back are refused, and line breaks are rejected (#161, #164).
- Under `eulix mcp`, logs stay off stdout (#157).
- Logs are opened inside `.eulix` through `os.Root`. A symlinked or hostile `.eulix` directory is refused before the router, context builder, analyze or viewer use it, a `history.db` that isn't a regular file is refused, and the subsystem debug log is written to the project's `.eulix` (#163).
- With `engine = "ehnas"`, `eulix analyze` skips the embedding stage, since EHNAS doesn't read `embeddings.bin` or `vectors.bin`. The completion marker records the retrieval engine, so changing the embedding model no longer re-analyzes an EHNAS project (`5ecd16d`).
- Change detection hashes size and mtime, matching the parser's FileWalker (#147).
- `kb.json` is streamed through a buffer instead of being mmapped, which takes peak memory from 212 MB to 33 MB on a 178 MB file. `MAP_POPULATE` is dropped, and the chunk slice is sized from `kb.json` metadata instead of a fixed 320k (`f9006ea`).
- Better memory alignment across the CLI (`42300d8`), and a simpler `KBStruct` (#157).

### Fixed

- **Vector search paired vectors with the wrong chunks.** `embeddings.bin` also has a file-summary row per file, but search matched `embeddings[i]` to `chunks[i]`, so from the second file on every vector was off. Rows are now matched through the `vectors.bin` id map in both the flat and the IVF search, and with no map loaded, vector search returns nothing instead of guessing (#110).
- **Unfinished analyses** (#107): when embedding failed, the next `eulix analyze` found the sources unchanged, printed "No changes detected" and left the project without `embeddings.bin`. `analyze` now writes `.eulix/analysis-complete.json` only after parsing and embedding both succeed, recording the embedding model, engine and PRISM version, and the size and mtime of the checksum, `kb.json`, `kb_index.json`, `kb_call_graph.json`, `embeddings.bin` and `vectors.bin`. A run is skipped only when the sources are unchanged and the marker still matches, and the marker is removed before each new run. A project analysed by an older version runs one full analysis.
- **New languages:** adding the first file of a language the last analysis didn't have (`main.go` in a Python-only project) was not seen as a change, so the next analysis was skipped (#113).
- **`.euignore`:** `**` and anchored `/` patterns match correctly, and unanchored patterns like `.*` no longer match the `..` path when walking from outside the project (#139). The file is read fully: lines over 64 KiB, CRLF, nested `.euignore` files (#157).
- **Query embedding engine:** the query server always used ONNX, so query vectors could come from a different backend than the index; on ROCm this made etiya's semantic search return noise. It now gets the configured engine (#153).
- **Embedding lifecycle** (#112): a long-lived MCP server leaked a full embedding matrix on every reload, because the anonymous mapping was never unmapped. `Close` now releases it (after the background IVF build), closes the query embedder's Python process, and is safe to call twice. An `embeddings.bin` with zero vectors no longer panics.
- **Router** (#115): file-structure and TODO queries always answered "Full KB (kb.json) not loaded"; they now load `kb.json` on first use. "list dependencies" reads `.eulix/kb_external_deps.json` of the repository being queried, instead of a file in the current directory that stayed cached for the whole process.
- **Provider keys** (#116): `ANTHROPIC_API_KEY` and `GEMINI_API_KEY` were never picked up. In one process (the MCP server switching repositories), the second repository kept the first one's key. Choosing Remote in the setup wizard left `base_url` on the local Ollama default.
- **Nested functions:** IDs such as `func_outer.inner::file.rs`, with or without an `@line` suffix, resolve to bare names in lookups and call-graph nodes (#146).
- **Source hydration** (#137): the skeleton fallback read through child ranges, and skeleton pointers could outlive evicted children. CRLF and BOM are normalized where files are read.
- **EHNAS ranking** (#153, #158):
  - chunks over 120 lines are demoted unless the query names them, so long orchestrators stop outranking the function that answers the query;
  - file anchors gate results only when some chunk path contains them, so naming a file like `eulix.toml` no longer drops every result;
  - BM25, keyword and strategy-cap ties break on stable IDs, and overloads that share an ID are told apart by source range, so results are deterministic;
  - camelCase identifiers are split and terms are stemmed in the shared index key path;
  - call-layer endpoints resolve by chunk ID before symbol names (on requests, 0 → 1,326 layer edges);
  - twin diversity takes effect, with snake_case and camelCase twins grouped, and the whole novelty candidate list is weighted before sorting.
- **IDs and index:** stale index entries are filtered (#159). Qualified IDs keep their qualifier when the file part is stripped, so `Foo::bar` stays `Foo::bar` (#163).
- **Leaks:** debug-log flush goroutines leaked, and builders closed the router's logger (`f9006ea`). `DebugLogger.Close` and `Flush` are nil-safe and idempotent (#161, #164).
- **mmap:** the package didn't compile outside Unix and Windows; there it now falls back to a buffered reader (#164). Windows and macOS flags are corrected (`f9006ea`). On Windows the file opens with `FILE_SHARE_READ` and the cleanup is idempotent (#161).
- **Windows** (#161, #164):
  - the parser installs through a temp file and a rename, moving a running binary aside instead of failing to overwrite it;
  - zip entries with `\` (PowerShell 5.1 `Compress-Archive`) are accepted when unpacking eulix_embed;
  - files open through `rundll32` with a percent-encoded, UNC-aware `file://` URL instead of `cmd /c start`, which re-parsed `& ^ | %`;
  - the embedding server's liveness is checked with `WaitForSingleObject`, since signal 0 is unsupported;
  - `Router.Close` closes the debug log, so `.eulix` can be removed;
  - `version` finds the parser through `ParserPath()`;
  - MCP handles `file://%3F/C%3A/repo` roots, and `~/.Eulix` no longer triggers project detection on case-insensitive filesystems (#163).
- **Embedded parser check:** builds now embed the parser hash, so a binary whose parser doesn't match its build manifest fails at startup with "does not match build manifest". The check used to be skipped silently (#92, #94).

### Security

- Context reads stay inside the project (`d809dbe`). The MCP lexical fallback read every regular file, hidden ones like `.env` and `.euignore`d ones included, and source hydration followed `..` paths and symlinks out of the project. The fallback now skips hidden and ignored files, and hydration reads through `os.Root`.
- NTFS-hidden files and directories are skipped by the lexical fallback (`3f83afa`), and by the checksum scan and `ServableMatcher` (#164).
- `sourceRoot` symlinks are resolved before the containment check (#137).
- Usage queries can't read files outside the repository, `.euignore`d and hidden files are kept out of MCP and the KB, and symlinked, Windows and racing MCP roots are fixed (#157).
- Symlinked usage sources are rejected, lexical hits are rendered from the scanned bytes, repository roots are pinned to canonical paths, and UNC hosts in file URIs are kept (#159).
- Lexical search reads are pinned to the root, so a swapped-in symlink isn't followed. Reading source for answers refuses symlinks, and hidden and ignored files are dropped from the router's and retrieval's call graphs (#163).
- `.eulix` symlinks can't escape the repository (see Changed). MCP remembers the folder it is bound to, refuses queries if that folder is replaced or removed, and rebuilds on rebind. Hidden and ignored files no longer show up in entry points, metrics, dependencies or the viewer's default call graph (#163).
- Leftover `.old-*` parsers are cleaned up with `ReadDir` instead of `Glob`, so glob characters in the install path can't delete files outside it (#164).

### Performance

- MCP lexical search trims hits during the walk, which takes peak heap from 578 MiB to 17 MiB on a 469 MiB repository. `ServableMatcher` answers are cached per path, and the root is resolved once instead of on every search (#163).

### Tests

- New tests for cache, checksum, cli, config, embeddings, llm, mcp and utils (#157); hidden-file and `.euignore` coverage for the lexical fallback (`3f83afa`); source and mmap tests, with the debugger writing to memory instead of the filesystem (#93); determinism tests for retrieval (#153, #158); `winissues`-tagged tests for open Windows issues (#164). `go test -count=3 ./...` passes (`f9006ea`).

### Known issues

- Windows: CLI-9 (a bound folder stays locked) and CLI-10 (junction roots) are open; see `docs/eulix-cli/windows-issues.md` (#164).

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
