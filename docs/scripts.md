# Scripts

Audited 2026-11-04 on branch `pre-release-fixes` (`ad744e9`). All scripts were renamed to snake_case on 2026-11-04. The "Former name" column is the old name, for finding it in history.

## Running from any directory

Every sub-directory Makefile (`eulix-cli`, `eulix-embed`, `eulix-parser`) forwards unknown targets to the root `Makefile`, so these work from the root or any of them. Scripts run in the directory you typed `make` in, so relative paths in `ARGS` resolve there.

```
make scripts-help
make repro-prism                       # builds the parser, then runs known_issues/repro_prism.sh
make repro-all                         # every parser repro
make kb-diff ARGS="run1/kb.json run2/kb.json"     # --emit-json copies of two runs
make script SCRIPT=known_issues/cross_language_edges.py ARGS=kb_call_graph.fb
make repro-prism PARSER_BIN=/other/eulix_parser
```

## Defaults and output

Scripts need no arguments when run from the repo: input files default to `.eulix/kb.fb`, `.eulix/kb_call_graph.fb` and `.eulix/embeddings.bin`, the parser defaults to `eulix-parser/target/release/eulix_parser`, and `check_embed_*.py` use the repo's `eulix-embed/` (override with `EULIX_EMBED_DIR`). Anything a script writes goes to `script-test-dump/` at the repo root, which is in `.gitignore` and `.euignore`. `kb_diff.py` has no default because it needs two files. When you pass a path through `make`, use `ARGS=`, for example `make script SCRIPT=call_graph_id_patterns.py ARGS=other.fb`. The FlatBuffers files are read through `bench/lib/kbfb.py` and its vendored runtime, so the scripts still need only the standard library.

## Docs checks

`scripts/check_docs.py` runs the same checks as the Docs workflow (`.github/workflows/Docs.yaml`): dead links with lychee, then spelling with codespell. Run it before pushing a change to `docs/`. `--offline` skips external URLs, and `--links` or `--spelling` runs only one check. It needs `lychee` (`pacman -S lychee`, `brew install lychee` or `cargo install lychee`) and `uv` or `codespell`.

## Bug-repro scripts (run these before a release)

These find and confirm bugs. Each one prints a result that shows whether the bug is present, so a fixed bug that comes back is caught by re-running it. Build the parser first: `cd eulix-parser && cargo build --release`.

| Bug                            | Former name                                                                                                      | Name                                            | What "bug present" looks like                                                                                                                                                                                                                                                                                    |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------- | ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| PRISM-2, 3, 4, 5, 6            | `known-issues/repro-prism.sh`                                                                                    | `known_issues/repro_prism.sh`                   | Edges missing or wrong in the printed v1 / v2 tables (compare with `Known-Issues/Parser-And-PRISM.md`)                                                                                                                                                                                                           |
| PRISM-2                        | `known-issues/method-edge-coverage.py`                                                                           | `known_issues/method_edge_coverage.py`          | `0/N methods have a caller` for a non-PHP extension                                                                                                                                                                                                                                                              |
| PRISM-4                        | `known-issues/cross-language-edges.py`                                                                           | `known_issues/cross_language_edges.py`          | Cross-language count above 0 on a project with no FFI calls                                                                                                                                                                                                                                                      |
| PRISM-5, 6, 7                  | `known-issues/probe/` (Rust crate)                                                                               | `known_issues/probe/`                           | Dumps what the parser records per call and class                                                                                                                                                                                                                                                                 |
| PARSE-2                        | `known-issues/check-tsx.sh`                                                                                      | `known_issues/check_tsx.sh`                     | `Unsupported language` for `.tsx` / `.jsx`                                                                                                                                                                                                                                                                       |
| PRISM-8                        | `known-issues/perf-rust-workspace.sh` (wraps `check-rust-workspace-perf.sh`, now `check_rust_workspace_perf.sh`) | `known_issues/perf_rust_workspace.sh`           | 80x160 workspace takes tens of seconds (42 s measured; dev branch 0.20 s) and yields 0 edges                                                                                                                                                                                                                     |
| #152 review findings           | `check-prism-v2-methods.sh`                                                                                      | `check_prism_v2_methods.sh`                     | `start -> util::run edge: MISSING` or `duplicates: YES`                                                                                                                                                                                                                                                          |
| Retrieval quality (EHNAS)      | `eval_mcp.py`                                                                                                    | `eval_mcp.py` (keep)                            | Expected file prefixes missing from MCP results; compare against `script-test-dump/eval_<engine>_baseline.json`. To compare versions on a frozen corpus, use the [version benchmark](Architecture/Benchmarking.md) (`make bench-compare`), which holds the same questions in `bench/suite/questions/eulix.toml`. |
| Embedding padding / ROCm drift | `padtest.py`, `rocmtest.py`                                                                                      | `check_embed_padding.py`, `check_embed_rocm.py` | Cosine between free and padded (or CPU and ROCm) vectors well below 1.0                                                                                                                                                                                                                                          |

## Diagnostic scripts (inspect output, do not assert a bug)

| Former name                  | Name                                | Use                                                                                                                      |
| ---------------------------- | ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `kb_diff.py`                 | `kb_diff.py` (keep)                 | Compare two parser runs through their `--emit-json` copies (`kb.json`, `kb_index.json`); exit 1 if different. Use it to confirm output stays deterministic. |
| `kbloader.py`                | `kb_id_name_check.py`               | Checks every function/method ID in `kb.fb` against its `name` and file; prints mismatches and exits 1 if any. |
| `ID_Pattern.py`              | `call_graph_id_patterns.py`         | Prints node ID prefix and pattern statistics from `kb_call_graph.fb`.                                                    |
| `vecstats.py`                | `vec_stats.py`                      | Pairwise cosine and int8 usage stats for a quantized vector file.                                                        |
| `kb_usage.py` + `astdump.go` | `kb_field_usage.py` + `ast_dump.go` | Reports which knowledge-base struct fields the Go CLI reads.                                                             |
| `aligo-walk.sh`              | `alignment_go_walk.sh`              | Pages through files with the external `aligo` tool. Not related to Eulix bugs.                                           |
| (new)                        | `lint_ps1.py`                       | Lints `install.ps1`, `build.ps1` and other tracked PowerShell files with PSScriptAnalyzer: `uv run scripts/lint_ps1.py [files] [--severity Error]`. Downloads a pinned, checksum-checked `pwsh` into `script-test-dump/pwsh/` when none is on PATH. |

## README demo

The loop at the top of the README (`.github/readme/demo-dark.webp` and `demo-light.webp`) and the 90-second walkthrough it links to are recorded from scripts in `.github/readme/demo/`. After a change to the CLI's output or to Claude Code, record them again with one command:

```
.github/readme/demo/record.sh all       # walkthrough scenes, both loops, the edited video
.github/readme/demo/record.sh teaser    # only the two README loops
.github/readme/demo/record.sh walkthrough
```

It needs `vhs` (0.12 or later) with `ttyd`, `ffmpeg`, ImageMagick 7, the JetBrainsMono Nerd Font, `eulix` on `PATH` and a logged-in Claude Code. Claude Code is started with its `dark-ansi` or `light-ansi` theme so it draws with the demo's terminal colors; your own theme setting is not touched. Run it from a normal terminal: it removes the variables a parent Claude Code session sets, but the Claude takes count against the logged-in account's usage (two short questions per run). Work goes to `/tmp/eulix-demo` (pass another folder as the second argument); Claude Code shows that folder in its banner, so keep it neutral. The two loops are written to `.github/readme/demo-dark.webp` and `demo-light.webp`, and the video to `/tmp/eulix-demo/out/eulix-walkthrough.mp4`. None of them is committed (the repository's `.gitignore` keeps images and video out of git): drag each into any GitHub comment box, copy its `https://github.com/user-attachments/assets/…` link without posting the comment, and put the links in the README's demo block.

| File | What it does |
| --- | --- |
| `demo.env` | The Flask release, the two questions asked on camera, the font, and the video's palette, step captions, title and install line. |
| `stage.sh` | Clones Flask at that release into the work folder and, unless `--no-mcp`, registers eulix in its `.mcp.json`. |
| `settings.tape`, `scene*.tape`, `teaser.tape` | The VHS scripts. Setup steps run inside `Hide` blocks. |
| `claude.zsh` | Starts Claude Code without personal plugins, hooks or claude.ai connectors, in the default permission mode, with eulix as its only MCP server. |
| `themes/` | Terminal colors from the README palette, dark and light. |
| `record.sh` | Runs the tapes, speeds up the loop's wait and labels it, and encodes the WebP files (2 MB each at most). `raw` and `export` write the uncut clips for a video editor (see below). |
| `edit.sh` | Builds the video: captions, labelled speed-ups, title and end cards, crossfades. |
| `sidebyside.sh`, `sidebyside.tape` | The 30-second side-by-side clip: one task from the agent benchmark, asked without Eulix (left) and with Eulix (right). See below. |

**Side-by-side clip.** `sidebyside.sh <task-id> bench/results/agent/<run-id> [scratch] [dark|light]` copies the benchmark's prepared repos for that task (run `python3 bench/agent/agent_bench.py prepare` first). It records the task's prompt in Claude Code once without Eulix and once with Eulix registered and the benchmark's hint in `CLAUDE.md`. The two takes go side by side, sped up by the same labelled factor to fit 30 s, as `.github/readme/sidebyside-<scheme>.gif` and `.mp4`. The caption under each half is that task's median input tokens, cost and tool calls from the given run, so the clip shows only measured numbers; its font shrinks (17 px down to 11) until the longer caption fits. One task is not the average: the README text next to the clip must name the task and link the full results. `--reuse` rebuilds the clip from the takes already in the scratch folder without asking Claude again, `--raw` also exports the two halves for a video editor, and `EULIX_SIDEBYSIDE_OUT=<path without extension>` writes the clip somewhere other than `.github/readme/`.

**Editing by hand.** `edit.sh` builds the walkthrough in one go. To cut it in a video editor such as Kdenlive instead, export the takes uncut:

```
.github/readme/demo/record.sh raw          # record the scenes and the dark Claude take, then export
.github/readme/demo/record.sh export       # export the takes already recorded (asks Claude nothing)
.github/readme/demo/sidebyside.sh --raw [--reuse] <task-id> bench/results/agent/<run-id>
```

`record.sh walkthrough` and `all` export as well. Everything lands in `/tmp/eulix-demo/raw` (`EULIX_RAW_DIR` overrides it for both scripts):

- `01-init.mp4` … `04b-agent.mp4`: each scene at normal speed, 50 fps, H.264 4:4:4 so coloured text stays sharp. No captions, badges or speed-ups. `05-vizeulize.mp4` (with its `.steps.json`) is copied from `viz/`. `06-sidebyside-plain.mp4` and `06-sidebyside-eulix.mp4` are the two halves.
- `graphics/`: `caption-1.png` … `caption-6.png` (the caption bar for each step, as wide as the terminal clips), `title.png`, `end.png`, `badge-<k>x.png` speed badges with a transparent background, and `stats-plain.png` / `stats-eulix.png`, the benchmark numbers for under each half.
- `clips.tsv`: per clip its length, step and caption, the stretch worth speeding up (`speed_up_from_s` to `speed_up_to_s`: indexing and the Claude waits), the Claude Code version the take ran on, and warnings.

Two checks run on every Claude take: if Claude Code's usage banner ("You've used N% of your weekly limit …") was on screen, the script warns and `clips.tsv` records it, so you can record again later or mask it; and when the Claude takes in one export ran on different Claude Code versions (their banners would disagree), the script says so. Record all the Claude takes in one sitting to keep them on one version. Export the video at 30 fps with no audio track and keep it under GitHub's 10 MB attachment limit.

Why it looks the way it does:

- **Flask, pinned.** Most viewers know Flask and can judge the answers; a fixed release keeps re-recordings comparable. The questions were chosen because eulix answers them well on this release. Questions about a bare Python method name (for example "who calls `full_dispatch_request`") currently retrieve the `Flask` class summary instead of the method ([RET-13](Known-Issues/Retrieval-And-Query.md#ret-13-a-question-naming-a-python-method-gets-the-class-not-the-method)), so avoid them until that is fixed.
- **One Claude take per color scheme.** The walkthrough reuses the dark loop's take, so a full run asks Claude two questions.
- **Speed-ups are labelled.** Waits play faster with a "k×" badge, and the programs' own timings (`analyze` prints how long it took) stay on screen, because the README makes speed claims.
- **Claude's startup is off camera.** The trust prompt prints the full folder path and Claude Code's footer can show account usage. The scripts warn about the usage banner, but check the frames for anything personal before publishing.
- **Captions, no audio.** GitHub plays attached videos muted.
- **README palette and JetBrains Mono**, dark and light, so the loop follows the reader's GitHub theme. The video is dark only.

### vizEulize recording

`.github/readme/demo/viz/vizeulize-bot.mjs` drives vizEulize in a real browser window for a screen recording. It starts `eulix vizeulize` on the README demo's Flask checkout (`/tmp/eulix-demo/work/flask`, created by `record.sh`) and walks through search, callers, callees, clicking through to a caller, back, a class hierarchy and the theme switch, with step captions and a visible pointer. It needs vizEulize 0.2.0 or later installed (`make install-viz`), because it uses the page's keyboard shortcuts.

```
cd .github/readme/demo/viz
npm install && npm run setup                                       # once: Playwright and its Chromium
node vizeulize-bot.mjs --dry-run /tmp/viz-check                    # headless check, a screenshot per step
node vizeulize-bot.mjs --record vizeulize-dark.mp4                 # the bot starts and stops wf-recorder
node vizeulize-bot.mjs --theme light --record vizeulize-light.mp4
```

With `--record`, the bot also writes `<file>.steps.json`, the time each step starts, which `record.sh` uses to cut the README excerpt. On Hyprland it finds its own window with `hyprctl` and records exactly that area; elsewhere it asks you to select the area with `slurp`. Move your own mouse pointer off the window first, because the recording shows it. The browser opens full screen and renders the page at 1.5× (`--zoom`) so its text stays readable once scaled down; `--windowed`, `--pace 1.5` (slower) and `--no-captions` are also available. The launcher's own browser window is suppressed (the bot puts a no-op `xdg-open` first on its `PATH`). The steps name Flask functions as vizEulize displays them (methods keep their class prefix, as in `Flask_full_dispatch_request`); for another repository, edit `STEPS` at the top of the script.

Where the recordings go:

- `record.sh loop` rebuilds both README loops from the Claude takes already recorded, followed by the vizEulize part from "Search for any function" up to "And what it calls" in `viz/vizeulize-dark.mp4` and `viz/vizeulize-light.mp4`. It asks Claude nothing. `record.sh teaser` and `all` do the same after recording new takes.
- `edit.sh` (run by `record.sh walkthrough` and `all`, or directly as `edit.sh /tmp/eulix-demo`) adds `viz/vizeulize-dark.mp4` to the video as step 5, at normal speed.

A missing recording is skipped with a note, and the recordings themselves are not committed.
