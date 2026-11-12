# LSP Indexing

This page explains how `eulix-lsp` builds its workspace index: how it scans files, parses them, and commits them into the index that cross-file features read. It also explains why the commit used to slow down quadratically, and how a small change to call-site deduplication made indexing grow linearly with workspace size.

> **Crate / path:** `eulix-lsp/src/backend.rs` (`spawn_scan`), `eulix-lsp/src/scan.rs`, `eulix-lsp/src/index.rs`, `eulix-lsp/src/state.rs`
> **Language:** Rust
> **Status:** the dedupe fix is in the working tree on `fix/lsp-148-sync` and not committed yet
> **Last verified against:** `bb695d1` plus the working-tree changes
> **Benchmark:** `eulix-lsp/bench/bench_lsp.py`, results in `eulix-lsp/bench/results/`

## Overview

When an editor starts the server, it sends `initialized`, and the server indexes the workspace in the background. Until that finishes, cross-file features have nothing to look up: go-to-definition into another file, references, call hierarchy, workspace symbol search, completion and rename. So the time to index is the time the user waits before the server is fully useful.

Indexing has three phases:

| Phase | Where | Threads | What it does |
| --- | --- | --- | --- |
| Walk | `scan::collect_source_files` | the `ignore` crate's walker | Finds supported files. Honours `.gitignore` and `.euignore`, skips `ignoreDirs` and files over `maxFileBytes`, and stops at `maxScanFiles` (default 50,000). |
| Parse | `scan::parse_all` | up to 4 blocking threads | Reads and parses each file into a `ParsedFile`. Files that fail are counted, not fatal. |
| Commit | `ServerState::commit_disk_batch` | 1, holding the index write lock | Adds every `ParsedFile` to the name index and the workspace cache in one pass, so nobody sees a half-built index. |

## The index

`Index` (`index.rs`) stores names, not types. It holds two maps, each with a reverse map so a file can be removed without scanning the whole workspace:

- `defs`: name → path → definitions. A `Def` is a name, kind, path, line range and optional container class.
- `calls`: callee name → list of `CallRef` (path and line). This is what references, incoming calls, rename and the unresolved-call diagnostics read.
- `defined_names` / `called_names`: path → names, used by `remove_file`.

`index_file(path, parsed)` first calls `remove_file(path)`, then adds the file's definitions and call sites. That makes reindexing a file idempotent: indexing the same content twice gives the same index as indexing it once.

## Why the commit was quadratic

Before the fix, each call site was added like this:

```rust
let sites = self.calls.entry(callee.to_string()).or_default();
if !sites.contains(&entry) {   // walks every call to `callee` already recorded
    sites.push(entry);
}
```

`contains` is a linear scan of every call site recorded so far for that name, across the whole workspace. Real code has names that are called from almost everywhere: `len`, `unwrap`, `new` and `get`, and in the kernel `kfree` and `spin_lock`. For a name with *k* call sites, adding them one by one costs about *k²/2* comparisons. Doubling the workspace doubles *k* and quadruples the cost.

The check only exists to drop duplicates within one file, which can appear when call ranges overlap. Duplicates from other files can't happen, because `index_file` has already called `remove_file(path)`, so no entry for this path is left in any list.

The same cost appeared outside the scan too. Every open, reparse after an edit and watcher event runs `index_file`, and the scan holds the index write lock for the whole commit. So every request that needs the index waited for the entire commit.

## The fix

Deduplicate within the file, in O(1), and push without scanning:

```rust
let mut seen: HashSet<(&str, usize)> = HashSet::new();
...
// remove_file ran above, so duplicates can only come from this file.
if callee.is_empty() || !seen.insert((callee, line)) {
    continue;
}
self.calls.entry(callee.to_string()).or_default().push(CallRef { path: path.to_path_buf(), line });
```

The result is the same set of call sites, built in linear time. Two tests guard it: `call_sites_are_deduplicated_and_sorted` (duplicates within one file are dropped) and `same_call_line_in_two_files_is_kept_and_reindex_does_not_duplicate` (the same line in different files is kept, and reindexing a file adds nothing).

## Measured results

Synthetic workspaces from `bench_lsp.py`: Python, Rust and TypeScript files, each defining `helper` and calling it 9 times, so one name has 9 call sites per file.

| Files | Scan before | Scan after | 100-file watcher batch before → after | Open → diagnostics before → after |
| --- | --- | --- | --- | --- |
| 500 | 0.44 s | 0.06 s | 229 → 86 ms | 2.3 → 0.9 ms |
| 1,000 | 1.64 s | 0.11 s | 405 → 107 ms | 3.9 → 1.0 ms |
| 2,000 | 6.43 s | 0.24 s | 753 → 144 ms | 7.5 → 1.5 ms |
| 4,000 | 25.3 s | 0.41 s | 1,479 → 192 ms | 15.0 → 2.2 ms |

Before the fix, each doubling multiplied scan time by 2^1.97: quadratic. After it, by 2^1.0: linear.

On the Linux kernel (50,000 of its 64,531 supported files, under an 8 GB memory cap):

| | Before | After |
| --- | --- | --- |
| Scan | 396 s | 49 s |
| Longest wait of an index request during the scan | 348 s | 2.1 s |
| CPU | 539 s | 190 s |
| Memory after scan | 2.47 GB | 2.48 GB |

Before the fix, about 340 of the 396 seconds ran on one thread inside the commit. After it, the commit takes about 3 seconds and parsing dominates.

The fix changes time, not memory.

## Known limits and next steps

These were measured by the same benchmark and are not fixed yet:

- **Parsing uses at most 4 threads.** `Config` caps `scanThreads` at 4 (`.min(4)`), even when the user sets more. On the kernel, CPU time grew by exactly 4 s per second while parsing, on a 12-core machine.
- **The index doubles memory.** On the kernel, memory was about 1.25 GB after parsing and 2.49 GB after the commit. Each `Def` and `CallRef` stores its own `PathBuf`; interning paths as file ids would shrink this.
- **The memory budget is not enforced.** `scanBudgetMb` (default 500) and `lazyScan` are parsed but never read. The kernel run used five times the budget.
- **`remove_file` still walks the callers of hot names.** `sites.retain(|c| c.path != path)` is linear in the callers of every name the file calls. That is why the watcher batch still grows with workspace size (86 → 192 ms). Storing `calls` as name → path → lines, like `defs`, would make removal proportional to the file.
- **Large results are not capped.** References and incoming calls on `helper` at 4,000 files return about 36,000 results (170 ms and 470 ms), and ten such requests raised peak memory from 122 MB to 434 MB.
- **Shutdown waits for a running commit.** A `shutdown` sent during the commit was answered only after the commit finished (6 s at 2,000 files, before the fix).
- **C coverage.** Structs and macros are not indexed. On the kernel, 11 of 20 opened files had an empty outline, and go-to-definition found nothing on 49 of 60 call sites.

Correctness issues found alongside this work, also open:

- Rename of a name with no workspace definition, such as `len`, edits every call to it in every open file and language. It should refuse.
- Buffers are keyed by the editor's URI string, while the index rebuilds URIs with `Url::from_file_path`. With VS Code's encoded URIs (`c%3A`, `c%2B%2B`), rename skips call sites and go-to-definition selects the whole line. Ignored tests in `eulix-lsp/src/backend/uri_tests.rs` reproduce both.

## Reproducing the numbers

```sh
cd eulix-lsp
bench/build.sh <commit>                          # builds target/bench/eulixlsp-<commit>
python3 bench/bench_lsp.py target/bench/eulixlsp-<commit> --workspace ..
# a very large tree, with the server memory-capped:
systemd-run --user --scope -p MemoryMax=8G -p MemorySwapMax=0 \
  python3 bench/bench_lsp.py <binary> --files '' --workspace /path/to/linux --scan-timeout 900
```

The `before` numbers are in `bench/results/eulixlsp-bb695d1.json` and `eulixlsp-bb695d1-linux.json`.
