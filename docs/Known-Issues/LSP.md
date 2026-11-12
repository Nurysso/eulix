# LSP Issues

Known problems in the `eulix-lsp` language server: wrong edits or locations, slow paths, and memory use.

> **Scope:** `eulix-lsp` (`src/backend.rs`, `src/index.rs`, `src/state.rs`, `src/scan.rs`, `src/config.rs`)
> **Last verified against:** `bb695d1` plus the working-tree fixes on `fix/lsp-148-sync` (2026-11-12)

Most entries were found with the benchmark in `eulix-lsp/bench/` (see [LSP Indexing](../Architecture/LSP-Indexing.md)), on synthetic workspaces of 500 to 4,000 files, the eulix repository, and the Linux kernel. Read [Overview](Overview.md) for the legend. Fixed LSP problems are listed in [Fixed Issues](Fixed-Issues.md#lsp).

---

## LSP-1: Rename edits names that the workspace does not define

**Severity:** High · **Status:** Open · **Verification:** Reproduced

**Symptom.** Renaming a call to a built-in or library function rewrites every call to that name in every open file, in every language. On the eulix repository, renaming Go's `len` returned 85 edits across 5 files: Go `len(...)`, Python `len(...)` and Rust `pb.len()`. Applying it breaks all of them.

**Cause.** `rename` (`backend.rs`) refuses only when `definition_count(name) > 1`. A name with no definition passes, and every call site of that name is edited, because the index matches by name only.

**Workaround.** Don't rename built-ins or library names; check the edit preview.

**Fix direction.** Refuse when the name has no definition in the workspace (`existing == 0`). Consider also limiting edits to the language of the definition.

---

## LSP-2: Rename and go-to-definition misbehave with editor-encoded URIs

**Severity:** High (VS Code on Windows) · **Status:** Open · **Verification:** Reproduced

**Symptom.** When the editor spells a file URI differently from the `url` crate, rename changes the declaration but none of its call sites, which leaves the code broken. Go-to-definition into another file selects the whole line instead of the name. VS Code always encodes the drive colon on Windows (`file:///c%3A/...`), and on every system it encodes characters such as `+` (`c%2B%2B`).

**Cause.** Open buffers are stored under the URI exactly as the editor sent it. Rename (`backend.rs`, the call-site loop) and `location_for` rebuild a URI from an index path with `Url::from_file_path` and look the buffer up with it. The strings differ, so the buffer is not found.

**Workaround.** None in VS Code on Windows. Elsewhere, avoid such characters in paths.

**Fix direction.** Normalize each incoming URI once where requests enter the server (`Url::from_file_path(uri.to_file_path())`), so every map key is spelled the same way. The same mismatch in the disk-sync code was fixed by comparing paths; see [Fixed Issues](Fixed-Issues.md#lsp). Two ignored tests in `src/backend/uri_tests.rs` reproduce both symptoms; remove `#[ignore]` when this is fixed.

---

## LSP-3: Watcher and reparse cost still grows with the callers of common names

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced

**Symptom.** A batch of 100 changed files takes 86 ms at 500 files and 192 ms at 4,000 files, after the quadratic-commit fix.

**Cause.** `Index::remove_file` runs `sites.retain(|c| c.path != path)` for every name the file calls. For a name called from every file, that walks all its call sites on each removal.

**Fix direction.** Store call sites as name → path → lines, like `defs`, so removing a file touches only its own entries.

---

## LSP-4: References and incoming calls on common names are slow and memory-heavy

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced

**Symptom.** At 4,000 files, references on `helper` (36,000 call sites) take 170 ms, and incoming calls take 470 ms. Ten such requests raised the server's peak memory from 122 MB to 434 MB, and about 35 MB was not returned.

**Cause.** Results are not capped, and every call site becomes a full LSP location with its own URI string.

**Fix direction.** Cap or page large results, and build each file's URI once per response.

---

## LSP-5: The index takes as much memory as the parsed files

**Severity:** Medium · **Status:** Open · **Verification:** Reproduced

**Symptom.** On the Linux kernel (50,000 files), memory was 1.25 GB after parsing and 2.49 GB after the commit into the index. That's about 3 times the source size.

**Cause.** Each `Def` and `CallRef` stores its own copy of the file path (`PathBuf`), across millions of call sites.

**Fix direction.** Intern paths as file ids (`u32` or `Arc<Path>`).

---

## LSP-6: The scan memory budget and lazy scan options do nothing

**Severity:** Low · **Status:** Open · **Verification:** Read in code

**Symptom.** `scanBudgetMb` (default 500) and `lazyScan` are accepted but have no effect. The kernel run used about 2.5 GB, five times the budget.

**Cause.** `Config` parses both fields, and nothing reads them.

**Fix direction.** Enforce the budget in the scan (stop or skip once it is reached), or remove both options.

---

## LSP-7: Parsing never uses more than 4 threads

**Severity:** Low · **Status:** Open · **Verification:** Reproduced

**Symptom.** On a 12-core machine, the kernel's parse phase ran at exactly 4 CPU-seconds per second, and about 46 of the 49 seconds were parsing.

**Cause.** `Config` caps `scan_threads` at 4 (`.min(4)`), both for the default and for the user's `scanThreads`.

**Fix direction.** Raise the cap or let `scanThreads` go past it, then re-measure with the benchmark.

---

## LSP-8: Shutdown waits for a running index commit

**Severity:** Low · **Status:** Open · **Verification:** Reproduced

**Symptom.** A `shutdown` request sent while the scan was committing was answered only after the commit finished: 6 s at 2,000 files, before the quadratic-commit fix.

**Cause.** The shutdown handler takes locks (`scan`, `documents`) that the commit holds.

**Fix direction.** Set the cancel flag and answer before taking those locks; the 2-second exit timer then ends the process.

---

## LSP-9: C structs and macros are not indexed

**Severity:** Medium · **Status:** Limitation · **Verification:** Reproduced

**Symptom.** On the Linux kernel, 11 of 20 opened files had an empty outline. They were headers with only macros, structs and prototypes; `pci_clp.h` has 16 structs and showed none. Go-to-definition found nothing on 49 of 60 call sites, many of them macros such as `blogic_err`.

**Cause.** The parser extracts functions and classes, not C structs, enums or `#define` macros.

**Fix direction.** Extract structs, enums and function-like macros in `libeulix` for C and C++. See also [PARSE-3](Parser-And-PRISM.md#parse-3-c-and-c-files-parse-only-partly).

---

## LSP-10: Hover shows nothing on calls into other files

**Severity:** Low · **Status:** Limitation · **Verification:** Reproduced

**Symptom.** Hover on a call to a function defined in another file returns nothing. All 60 such hovers in each benchmark run were empty.

**Cause.** `hover` looks only at the current file's parse result.

**Fix direction.** Resolve the name through the index, as go-to-definition does, and show that definition.
