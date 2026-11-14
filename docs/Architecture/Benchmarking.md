# Version Benchmark

This page explains the benchmark that compares two eulix versions: what it measures, why it is built the way it is, how to run it, and where it keeps its state.

> **Path:** `bench/eulix_bench.py`, `bench/lib/`, suite data in `bench/suite/`, tests in `bench/tests/`
> **Language:** Python 3.11+ (standard library only)
> **Platform:** Linux only; memory is read from `/proc`
> **Last verified against:** `f9006ea` plus the uncommitted `bench/` directory (2026-11-13)

## Overview

`make bench-compare A=v0.8.3 B=HEAD` builds both versions, measures them on the same frozen code, and prints what changed. Results are saved under `~/.Eulix/bench`, keyed by commit, so a version that has been measured once is never measured again. Only the side that has no saved result is built and run.

It measures four things:

| Group | What it runs | What it reports |
| --- | --- | --- |
| Retrieval accuracy | One `query_without_cot` call per question, each on a fresh `eulix mcp` server, for every engine the version has | Hit@1, recall@5, recall@10, MRR, how often the grep fallback answered, questions that skipped retrieval, errors; per question, the rank of the expected file |
| Indexing speed and memory | `eulix analyze` from an empty `.eulix/`, 3 times | Total, parse and embed time, chunks per second, peak memory of the whole process tree and of the parser and embedder, index size on disk |
| Query speed | 3 fresh `eulix mcp` servers, then one server answering every question 3 times | Time to `initialize` and to the first answer, warm p50 and p95, server memory |
| Parser quality | The call graph of the index, plus a second cold parse | Files parsed clean, partial and failed; nodes and edges; share of methods with a known caller; cross-language edges; whether two runs give identical output |

## Commands

| Command | What it does |
| --- | --- |
| `make bench-compare A=<rev> B=<rev> [ARGS=..]` | Measures what is missing for A and B, prints a table, writes a Markdown report to `~/.Eulix/bench/reports/`. Exit 0: no regressions; 1: something was flagged; 2: an error or missing data. |
| `make bench-run REV=<rev> [ARGS=..]` | Measures one version and saves the results. |
| `make bench-list` | Lists saved results: version, group, corpus, engine, device, whether the timing came from this machine. |
| `make bench-clean ARGS='--work'` | Frees disk space (`--work`, `--builds [REV..]`, `--corpora`, `--venvs`, `--logs`, `--all`; `--results --yes` deletes measurements). |
| `make bench-test` | Unit tests of the harness. They need no eulix build. |
| `python3 bench/eulix_bench.py exec <rev> -- <args>` | Runs that version's eulix inside its sandbox, for debugging. |

A revision is a tag, branch, SHA, or `.` for the working tree. A working-tree run is built from a copy of your files and never cached.

Useful options for `run` and `compare`:

- `--corpus NAME` (repeatable) and `--groups retrieval,indexing,query,parser` narrow the run.
- `--repeat N` sets the number of indexing runs. Saved samples are topped up, not thrown away.
- `--rerun` measures again even when a result is saved.
- `--large` adds the Linux kernel (parsing only).
- `--cpu` embeds on CPU instead of the GPU. It is the fallback, used automatically when your eulix venv has no GPU provider; CPU and GPU timings are saved apart.
- `compare --no-run` uses saved results only.

## Design decisions

- **It runs locally and blocks nothing.** Indexing needs the embedding model, which GitHub's free CI runners can't run at a useful speed. The exit code leaves room for a CI check later.
- **Results are keyed by commit; the tag is only a label.** `AppVersion` is hardcoded, so every commit since the last bump reports the same version. Tags can move, and untagged commits need measuring too.
- **The harness, questions and corpora always come from the current checkout.** An old commit only supplies its built binary, so every version takes the same test.
- **Each version runs with its own home folder.** Every eulix binary unpacks its parser into `$HOME/.Eulix` and exits when it finds another build's parser there (`assets.VerifyOrExtract`). The harness points `HOME` at `~/.Eulix/bench/homes/<sha>/`, pre-writes `.Eulix/.initialized` so start-up skips `uv pip install`, and links `.Eulix/.venv` to the benchmark's Python environment. It also checks that your installed `~/.Eulix/bin/eulix_parser` is unchanged after every run.
- **Builds use a detached git worktree.** `eulix-parser/build.rs` runs `git rev-parse` and writes `"unknown"` outside a checkout; with a worktree the right SHA ends up in `kb.json`. The oldest supported version is v0.8.3, the first with `eulix mcp` and `make build-linux-amd`.
- **Speed is measured on the GPU; CPU is the fallback.** Indexing and query timings run with your own eulix Python environment (`~/.Eulix/.venv`), the way real systems run. When that environment has no GPU execution provider, or with `--cpu`, the benchmark uses its own CPU-only environment instead. Runs abort if the embedder used a different device than the one they are labelled with.
- **Accuracy always runs on a CPU-built index.** With ROCm the embedder can produce wrong vectors ([EMB-1](../Known-Issues/Embeddings.md)), so accuracy from a GPU-built index would depend on the machine. The eulix corpus is therefore indexed twice per version: on the GPU for timing, and once, untimed, on CPU for the retrieval questions. The CPU environment holds only the CPU build of onnxruntime, so no GPU is reachable from it.
- **The model runs offline.** The ONNX model is copied into the benchmark's cache once, and every run sets `HF_HUB_OFFLINE=1`, so no network call lands in a timing.
- **Accuracy results are reused on any machine; timings only on the same machine.** Each cache key holds the commit, corpus, generated config, Python environment and device; timing keys also hold a machine fingerprint (CPU model, threads, memory, `/etc/machine-id`, and the GPU's PCI ids for GPU runs). Parser results carry no device, since parsing doesn't embed. Each question's result is saved under its own hash, so adding a question runs only that question.
- **Every version runs on its own retrieval defaults.** The generated `eulix.toml` fixes the model, embedder, parser threads, token budget and cache, and leaves `[retrievalConfig]` out. etiya runs leave out `[project] engine`, which every version reads as etiya; `engine = "ehnas"` is only written for versions that have EHNAS, since v0.8.3 would ignore it silently.
- **Files that quote the questions are deleted from the corpus.** The grep fallback reads files on disk and v0.8.3 ignores `.euignore` there. A preflight fails if any corpus file contains a question's text.
- **Determinism is checked two ways.** `byte_identical` compares every parser output file (`kb.json` without its run-specific metadata). `semantic_identical` compares the call graph's nodes and edges in any order, because v0.8.3 writes part of the call graph from a `HashMap`.

## Flag rules

`compare` matches results cell by cell: (group, corpus, engine). An engine one version lacks is shown as `n/a` and never flagged. It flags:

- a question that A found and B didn't, that B ranks lower, or that B answered with an error or without retrieval;
- a timing or memory figure whose median is more than 10% worse, whose best run is worse than A's worst run, and whose change is above a floor (0.05 s, 5 ms, 10 MB, 64 KiB);
- more files failing to parse, more than 5% fewer call edges, or parser output that stops being identical between runs.

## Corpora and questions

`bench/suite/corpora.toml` pins every corpus to a full commit SHA. Moving a pin invalidates that corpus's saved results.

| Corpus | Commit | Used for |
| --- | --- | --- |
| eulix | `f9006ea` | everything, including accuracy |
| flask 3.1.3, gin v1.12.0, ripgrep 15.2.0, gson 2.15.0 | release tags | indexing, query speed, parser quality |
| Linux v7.3 (`--large`) | release tag | parsing speed and memory, parser quality |

`bench/suite/questions/eulix.toml` holds the retrieval questions, moved from `scripts/eval_mcp.py`. Each one names the files that answer it; a hit is the first result whose path starts with one of them. Questions are phrased "How does/is/are …": shorter "where is X" or "who calls X" questions are answered by the query router directly and never reach retrieval. The outside corpora have 10 questions each with no expected answer; they only time queries.

## State

```
~/.Eulix/bench/
  builds/<sha>/          eulix binary, build.json (capabilities, toolchains), build.log
  homes/<sha12>[-gpu]/   sandboxed HOME per version
  venvs/cpu-<key>/       CPU-only Python env for accuracy and the CPU fallback (eulix-embed requirements + bench/suite/venv-cpu.txt)
  cache/onnx/            seeded embedding model
  corpora/               frozen, read-only corpus snapshots
  work/<sha12>[-gpu]/<corpus>/  per-version copy of a corpus with its index (CPU and GPU builds kept apart)
  results/<group>/<sha12>/<key>.json
  reports/               Markdown reports
  logs/<run>/            raw output of every command the harness ran
```

## First results: v0.8.3 against `f9006ea`

Measured on 2026-11-13 on a Ryzen 5 5600X (12 threads, 15 GiB, governor `powersave`) with an RX 6700 XT (ROCm, onnxruntime-rocm 1.22.2). Timings are the median of 3 runs on the GPU; accuracy uses the CPU-built index. With accuracy already saved, the whole comparison took under 4 minutes; on CPU alone it took about an hour.

- **GPU embedding got slower.** HEAD embeds 20 to 35% fewer chunks per second than v0.8.3 on every corpus (eulix 585 to 389, ripgrep 590 to 380, gin 722 to 493), so indexing takes 18 to 64% longer. On gin and gson the chunk count is identical, so the cost is per chunk. On CPU the two versions embed at the same rate, so the change is in the GPU path. Bisected to `685d011` (bucketing on true token lengths), and it is a correctness fix, not a regression: v0.8.3 guessed a chunk's length as `len(text) // 4` and truncated it at that bucket, but code averages 2.6 characters per token. On this repo's `kb.json`, v0.8.3 cut 67% of chunks short (a median 18% of each, up to 48%), and its vectors for those chunks differ from full-text ones (cosine median 0.992, 5th percentile 0.938, minimum 0.897). HEAD sends 1.47 times as many tokens through the model, which matches the 1.47 times lower throughput (564 to 384 chunks per second). CPU runs don't bucket, so neither version truncates there.
- **The index got bigger.** `index_bytes` is up 16% on eulix and 67% on flask, where HEAD also produces 72% more chunks (901 to 1,552).
- **Retrieval improved on code questions.** etiya ranked the expected file first for 13 of 21 code-vocabulary questions, up from 8. Four questions got worse (`go-config-load`, `go-prf`, `plain-files-changed`, `plain-ann`). EHNAS, new since v0.8.3, ranked 11 of 21.
- **Plain-language questions still fail.** etiya scored 0 of 8 at HEAD (1 of 8 at v0.8.3), even with CPU-built vectors. ROCm (EMB-1) is therefore not the only cause; the chunk text the vectors are built from (EMB-3: names and signatures only) is the likely remaining one.
- **The parser links more methods and fewer cross-language calls.** The share of methods with a known caller went from 0% to 52.8% on eulix (PRISM-2), and cross-language call edges from 239 to 106 (PRISM-4). HEAD's parser output is identical between runs; v0.8.3's has the same content but its bytes vary.
- **Warm etiya queries got slower.** On eulix, etiya's warm p50 went from 21 ms to 31 ms (+50%) and p95 from 61 ms to 69 ms; the first answer after start-up stayed near 1.1 s. EHNAS, which needs no embedder, answers in 18 ms warm and 0.16 s cold, with 89 MB of memory against etiya's 956 MB.
- **Linux v7.3 (`--large`).** Both versions parse the kernel in about 57 s (56.9 s and 57.6 s). HEAD peaks at 3.6 GB against 3.9 GB, and writes a 1.21 GB index against 1.69 GB. Half of the kernel's files (32,491) parse only partially in both versions.
- **The old accuracy baseline was inflated.** The 14 to 15 of 21 recorded in `.eulix/eval/` came from an index that still contained `scripts/eval_mcp.py`, with every question in it.
