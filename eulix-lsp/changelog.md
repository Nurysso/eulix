# Changelog

## eulix-lsp 0.2.0 - Unreleased (Alpha)

> eulix-lsp has not been released yet. To try it, clone the repo and run `make install lsp`.

Changes since 0.1.2 (never released). Now requires libeulix 0.9.0 (#150).

### Added

- **PHP:** `.php` and `.phtml` files are served and `PhpInfo` is mapped. `.inc` stays out, since C, C++ and assembler share the extension (#82).
- **PHP references and call hierarchy** (#89). PHP files go through their own index, which keeps qualified identities and resolves calls at query time, so results don't depend on scan order and an edited parent class is picked up. References, `prepareCallHierarchy` and incoming/outgoing calls use it; other languages keep the existing resolver.
  - `$this`, `self` and `static` resolve on the enclosing class, `parent` on its `extends` class, and methods are looked up along the `extends` chain.
  - `Class::m` and `new Class` match through namespaces and `use` aliases, case-insensitively, as PHP does.
  - `$x->m()` on an unknown receiver resolves only when one method in the workspace has that name, the rule PRISM v2 uses for PHP.
  - Namespaced functions fall back to the global function.

### Fixed

- **Rename** skipped the call sites in the declaring file and left a second call on the same line unchanged. It now edits every identifier-bounded match in every open file (#106).
- **Positions:** `find_name_on_line` mixed byte and character offsets, so a multi-byte character earlier on the line shifted the range to the right, and stepping one byte could land inside a character and panic (#105). EOF positions stay inclusive, and oversized columns are clamped before the line terminator (#154).
- **Symbols:** workspace symbols keep their real names, document symbol ranges include the full declaration body, and Rust test functions are no longer marked deprecated (#154).
- **Call hierarchy and diagnostics:** outgoing calls skip repeated name-and-line pairs, and optional diagnostics are deduplicated by name and line (#152).
- **Open buffers and disk sync** (#162):
  - each scan has its own cancellation token, and buffer session and edit revisions plus per-path disk tickets reject stale parses;
  - scans and file-watcher updates no longer replace an open buffer's cache or index, also while its reparse is pending, and a buffer owns its diagnostics, so a deletion on disk can't clear them;
  - symlinks below the workspace root and canonical aliases are ignored, closed external files and files with no workspace root are forgotten, and empty scans stay quiet.
- Call-site indexing was quadratic per file and is linear now (#165).
- Open buffers are matched by path instead of a rebuilt URI, which broke on `+` and drive colons (#165).
- `did_close` and watched-file handling read files with `from_utf8_lossy` (#165).
- Go and Java call sites take their qualifier from `FunctionCall::receiver`, where libeulix 0.9.0 now stores it, so ranking still sees `fmt` in `fmt.Println()` (#167).

### Tests

- Tests build URIs from the system temp directory instead of `/workspace/...`, which has no drive letter on Windows and made 16 tests panic there (`b37015f`).
- `make test-rust` runs the eulix-lsp and libeulix tests, as CI does (#156).

_Changes before 0.2.0 were not tracked._
