# LSP Issues

Open problems in `eulix-lsp`, the language server built on `libeulix`.

> **Scope:** `eulix-lsp/`  
> **Last verified against:** `3742a31` (2026-11-10)

`eulix-lsp` is in beta. Three of the five items from [#148](https://github.com/Nurysso/eulix/issues/148) were fixed in [#154](https://github.com/Nurysso/eulix/pull/154) and are listed in [Fixed Issues](Fixed-Issues.md#lsp). The two below remain.

---

## LSP-1: Background scans are not cancelled and overwrite open buffers

**Severity:** Medium · **Status:** Open · **Verification:** Read in code

**Symptom.** Starting a new workspace scan does not stop an older one. A scan also replaces the index entries of open, unsaved buffers with the version on disk, and closing a buffer that was never saved leaves its symbols in the index.

**Cause.** The cancellation flag is set to `true` and right back to `false`, so the older scan never sees it (`eulix-lsp/src/backend.rs`, scan start). The scan writes every file it reads into the index, open or not, and `didClose` does not remove entries for files that do not exist on disk.

**Workaround.** Save open buffers before a rescan, or restart the server.

**Fix direction.** Use a scan generation counter or a fresh cancellation token per scan, skip files that have an open buffer, and drop a closed buffer's entries when the file is not on disk.

---

## LSP-2: The file watcher indexes files outside the workspace root

**Severity:** Medium · **Status:** Open · **Verification:** Read in code

**Symptom.** `workspace/didChangeWatchedFiles` reads and indexes any path the client sends, including paths outside the workspace root.

**Cause.** The handler does not check that a changed path is inside the root (`eulix-lsp/src/backend.rs`, `did_change_watched_files`).

**Workaround.** Configure the client to watch only the workspace folder.

**Fix direction.** Ignore watched-file events whose path is not under the workspace root.

---

## Related Docs

- [Overview](Overview.md): the open list.
- [eulix-lsp README](https://github.com/Nurysso/eulix/blob/main/eulix-lsp/README.md).
