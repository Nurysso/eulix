# LSP Benchmark

This page explains the benchmark for `eulix-lsp`: what it measures, how to run it, how to read its output, and the results it gave for commit `bb695d1` and for the call-site dedupe fix.

> **Path:** `eulix-lsp/bench/bench_lsp.py`, `eulix-lsp/bench/build.sh`, results in `eulix-lsp/bench/results/`
> **Language:** Python 3 (standard library only), Bash
> **Platform:** Linux only; memory, CPU, threads and open files are read from `/proc`
> **Last verified against:** `bb695d1` plus the working-tree fixes on `fix/lsp-148-sync` (2026-11-12)

## Overview

The benchmark starts the real server binary and talks to it over stdio, the way an editor does. Each workspace gets a fresh server. It reports latency, memory and health, and saves everything as JSON, so two commits can be compared run by run.

It found the quadratic indexing bug, the `len` rename bug, the unenforced memory budget, and the weak C coverage. See [LSP Indexing](LSP-Indexing.md) and [LSP Known Issues](../Known-Issues/LSP.md).

## What it measures

| Phase    | What happens                                                                                                                                                                                 | What it reports                                                                                                         |
| -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Scan     | Sends `initialized` and waits for the "indexed N files" log. Meanwhile it sends a document-outline request and a workspace search every 50 ms, like an editor in use.                        | Scan time, memory after the scan, and request latency during the scan, which shows how long index requests are blocked. |
| Open     | Opens up to 20 sample files, like an editor restoring tabs.                                                                                                                                  | Time from `didOpen` to diagnostics.                                                                                     |
| Requests | Hover, highlight, references, rename and call hierarchy on declarations; definition, hover and completion on calls into other files; outline, folding, semantic tokens and workspace search. | p50, p95 and max latency per request type, plus errors, empty results and average result size.                          |
| Hot name | The same requests on the most common call name in the open files (`helper` in synthetic workspaces).                                                                                         | The cost of names with many definitions and callers.                                                                    |
| Typing   | 100 edits at the top of the largest open file, sent as fast as possible.                                                                                                                     | Time from the last edit to its diagnostics, and how many diagnostics updates were sent (debounce check).                |
| Churn    | Close, reopen and edit every sample file, 10 rounds by default.                                                                                                                              | Memory, threads and open files per round. Steady growth across identical rounds points to a leak.                       |
| Watcher  | Appends a new function to 100 files and sends one `didChangeWatchedFiles` batch. Synthetic workspaces only, because it writes files.                                                         | Time until all new symbols are searchable.                                                                              |
| Health   | Over the whole run.                                                                                                                                                                          | Panics and errors on stderr, non-protocol bytes on stdout, JSON-RPC errors and timeouts, shutdown time, exit code.      |

Memory is also sampled once a second in a background thread, and recorded after each phase. Peak memory only rises, so the phase that raised it is the one to look at.

## Workspaces

- **Synthetic** (`--files 500,1000,2000,4000`, the default). Generated in a temporary folder and removed afterwards. Python, Rust and TypeScript files in turn, each with a class, 8 functions calling functions in other files, and a `helper` defined in every file, so one name has a definition and 9 call sites per file. Sizes double, so the script can print a growth exponent: about 1 is linear, about 2 is quadratic.
- **Real** (`--workspace PATH`, repeatable). Read-only: the watcher test is skipped.

## How to run it

```sh
cd eulix-lsp
bench/build.sh bb695d1      # builds target/bench/eulixlsp-bb695d1 from that commit, working tree untouched
python3 bench/bench_lsp.py target/bench/eulixlsp-bb695d1 --workspace ..
```

For very large trees, cap the memory so only the server is killed if it runs out, not the machine:

```sh
systemd-run --user --scope -p MemoryMax=8G -p MemorySwapMax=0 \
  python3 bench/bench_lsp.py <binary> --files '' --workspace ~/linux --scan-timeout 900
```

Useful options: `--label` (results name; the default is the binary's file name), `--open` (sample files, 20), `--targets` (positions per request type, 60), `--rounds` (churn rounds, 10), `--watch` (watcher batch size, at most 200), `--scan-timeout` (600 s).

Results go to `bench/results/<label>.json`, with one `<label>-<workspace>.stderr.log` per workspace. If the scan doesn't finish in time, the run stops, records what it has and the shutdown time, and moves on. If the server dies, the run records the last finished phase and the exit code.

## Reading the output

```
== eulixlsp-bb695d1-synthetic-4000  (4000 source files)
  scan 25.342 s   eulixlsp: indexed 4000 files
  memory: idle 6.2 MB, after scan 119.3 MB, peak 434.1 MB, end 158.3 MB   threads 28
  peak rss by phase: scan 122.2, open 122.2, requests 124.6, hot_requests 434.1, ...
  during scan        n   p50 ms   p95 ms    max ms
    workspace/symbol 8     0.08  24986.49  24986.49
```

- A request during the scan whose max is close to the scan time was blocked by the index commit.
- In the request table, `null` counts empty results. Many empty results for a request type that should find something usually point to a gap, such as hover on cross-file calls.
- `[decl]`, `[call]` and `[hot]` show where the request was aimed: a declaration, a call into another file, or the most common name.
- Request times include the benchmark's own JSON parsing, which inflates requests with very large results a little.

## Results

### Synthetic workspaces

| Files | Scan, bb695d1 | Scan, dedupe fix | Watcher batch, before → after | Open → diagnostics, before → after |
| ----- | ------------- | ---------------- | ----------------------------- | ---------------------------------- |
| 500   | 0.44 s        | 0.06 s           | 229 → 86 ms                   | 2.3 → 0.9 ms                       |
| 1,000 | 1.64 s        | 0.11 s           | 405 → 107 ms                  | 3.9 → 1.0 ms                       |
| 2,000 | 6.43 s        | 0.24 s           | 753 → 144 ms                  | 7.5 → 1.5 ms                       |
| 4,000 | 25.3 s        | 0.41 s           | 1,479 → 192 ms                | 15.0 → 2.2 ms                      |

Growth exponent: 1.90, 1.97 and 1.98 before the fix; 0.95, 1.07 and 0.93 after it.

Other numbers at 4,000 files:

- Memory: 119 MB after the scan.
- References on `helper` took 170 ms and incoming calls 470 ms; each returned about 36,000 results.
- Those hot-name requests raised peak memory to 434 MB.
- Typing: diagnostics arrived 220 ms after the last edit, with 200 ms of that the debounce delay. 100 edits produced one diagnostics update.

### Linux kernel

50,000 of its 64,531 supported files (the scan limit), about 800 MB of source, with an 8 GB memory cap:

|                                                  | bb695d1 | Dedupe fix |
| ------------------------------------------------ | ------- | ---------- |
| Scan                                             | 396 s   | 49 s       |
| Longest wait of an index request during the scan | 348 s   | 2.1 s      |
| CPU                                              | 539 s   | 190 s      |
| Memory after scan                                | 2.47 GB | 2.48 GB    |
| Last edit → diagnostics                          | 298 ms  | 255 ms     |

Before the fix, about 340 of the 396 seconds ran on one thread inside the commit. After it, parsing dominates, at 4 CPU-seconds per second on a 12-core machine (see [LSP-7](../Known-Issues/LSP.md#lsp-7-parsing-never-uses-more-than-4-threads)). The dedupe-fix kernel run used a scratch build of the same change, not a committed binary.

### eulix repository (272 files)

- The scan took 0.36 s, and memory after it was 37 MB.
- Churn memory rose for about 35 rounds, then stayed flat at 65.7 MB through round 50. That's the allocator warming up, not a leak.
- The most common name was `len`, and renaming it produced edits across languages ([LSP-1](../Known-Issues/LSP.md#lsp-1-rename-edits-names-that-the-workspace-does-not-define)).

### Health

Every run had no panics, no stray bytes on stdout, no JSON-RPC errors and a clean exit. Threads and open files stayed flat across churn rounds.

## Limits of the benchmark

- The hot name is chosen from the 20 open files, so a real tree may not exercise its most common names. On the kernel it picked `blogic_err`, not `kfree`.
- Synthetic workspaces are a hard case for indexing on purpose: one name is called from every file.
- One machine, one run per number. Repeat runs agreed within a few percent.
- There is no tool yet to compare two result files; compare the JSON by hand or with a short script.
