#!/usr/bin/env bash
# Record the README's 30-second side-by-side clip: the same benchmark question to
# Claude Code, without Eulix (left) and with Eulix (right).
#
# Usage: sidebyside.sh [--raw] [--reuse] <task-id> <bench-run-dir> [scratch-dir] [dark|light]
#   --raw          also export each half uncut (1x, no caption) for a video editor,
#                  with its stats strip as a PNG, into $EULIX_RAW_DIR (default
#                  /tmp/eulix-demo/raw, where record.sh export writes)
#   --reuse        use the takes already in the scratch dir: no new Claude queries,
#                  only the stacking and the export run again
#   task-id        a task from bench/agent/tasks.toml (its prompt is asked on camera)
#   bench-run-dir  bench/results/agent/<run-id>: the captions under each half are that
#                  task's medians from this run, so the clip never shows a number the
#                  benchmark did not measure
#
# Output: .github/readme/sidebyside-<scheme>.gif and .mp4 (git-ignored; upload to
# GitHub and link from the README, like the teaser). EULIX_SIDEBYSIDE_OUT=<path
# without extension> writes them elsewhere.
#
# Setup matches the benchmark's eulix-hint arm and uses copies of the benchmark's
# own prepared repos (run `agent_bench.py prepare` first): the right-hand copy has
# the index, eulix registered in .mcp.json and the one-line hint in CLAUDE.md; the
# left-hand copy has none of them. Both run through claude.zsh (no personal plugins or settings).
# The takes are sped up to fit 30 s, both by the same factor, and the badge says so.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(git -C "$here" rev-parse --show-toplevel)"
# shellcheck source=demo.env
source "$here/demo.env"

usage='usage: sidebyside.sh [--raw] [--reuse] <task-id> <bench-run-dir> [scratch-dir] [dark|light]'
export_raw=0 reuse=0 args=()
for a in "$@"; do
  case "$a" in
    --raw) export_raw=1 ;;
    --reuse) reuse=1 ;;
    -*) echo "$usage" >&2; exit 2 ;;
    *) args+=("$a") ;;
  esac
done
task="${args[0]:?$usage}"
run_dir="${args[1]:?$usage}"
scratch="${args[2]:-/tmp/eulix-sidebyside}"
scheme="${args[3]:-dark}"
raw="${EULIX_RAW_DIR:-/tmp/eulix-demo/raw}"
CLIP_SECONDS=30
STRIP=56   # stats strip under each half, px

for tool in vhs ttyd ffmpeg ffprobe magick eulix claude git python3; do
  command -v "$tool" >/dev/null || { echo "sidebyside.sh: $tool is not on PATH" >&2; exit 1; }
done
mkdir -p "$scratch"
scratch="$(cd "$scratch" && pwd)"
work="$scratch/work"

# The prompt and the captions, both straight from the benchmark files.
read_bench() {
  python3 - "$repo/bench/agent/tasks.toml" "$run_dir/runs.jsonl" "$task" "$1" <<'EOF'
import json, statistics, sys, tomllib
tasks, runs, task, what = sys.argv[1:]
t = next((t for t in tomllib.load(open(tasks, "rb"))["task"] if t["id"] == task), None)
if t is None:
    sys.exit(f"sidebyside.sh: no task {task} in tasks.toml")
if what in ("prompt", "corpus"):
    print(t[what]); sys.exit()
arm = {"plain": "plain", "eulix": "eulix-hint"}[what]
rs = [json.loads(l) for l in open(runs) if l.strip()]
rs = [r for r in rs if r["task"] == task and r["arm"] == arm and r.get("ok")]
if not rs:
    sys.exit(f"sidebyside.sh: no {arm} runs of {task} in {runs}")
med = lambda k: statistics.median(r[k] for r in rs)
label = "without Eulix" if what == "plain" else "with Eulix"
print(f"{label}: {med('input_tokens'):,.0f} input tokens, ${med('cost_usd'):.3f}, "
      f"{med('tool_calls'):g} tool call{'' if med('tool_calls') == 1 else 's'} (median of {len(rs)} benchmark runs)")
EOF
}
question="$(read_bench prompt)"
cap_plain="$(read_bench plain)"
cap_eulix="$(read_bench eulix)"

# Copies of the benchmark's two prepared repos, under a neutral folder name
# (Claude Code's banner shows the path): plain, and indexed + registered + hinted.
corpus="$(read_bench corpus)"
bench_work="${EULIX_BENCH_HOME:-$HOME/.Eulix/bench}/agent/work"
hint="$(python3 -c "import sys; sys.path.insert(0, '$repo/bench/agent'); from agent_bench import HINT; print(HINT)")"
prepare_copies() {
local side src
for side in plain eulix; do
  src="$bench_work/$corpus-$side"
  [[ -f "$src/.agent-bench" ]] || { echo "sidebyside.sh: run 'python3 bench/agent/agent_bench.py prepare' first" >&2; exit 1; }
  rm -rf "${work:?}/$side" && mkdir -p "$work/$side"
  cp -r "$src" "$work/$side/$corpus"
  rm -f "$work/$side/$corpus/.agent-bench" "$work/$side/$corpus/.agent-index-seconds"
done
echo '{"mcpServers":{}}' > "$work/plain/$corpus/.mcp.json"
(cd "$work/eulix/$corpus" && claude mcp add --scope project eulix -- eulix mcp >/dev/null && printf '%s\n' "$hint" > CLAUDE.md)
}

# Same recipe as record.sh's record_tape: theme + font + settings + tape, then
# compose the text and cursor layers.
record_side() {
  local side="$1" body bg v
  local run="$scratch/$side-$scheme"  # own line: $side here must be the local, not the stale global from the prepare loop
  rm -rf "$run" && mkdir -p "$run/composed"
  body="$(<"$here/sidebyside.tape")"
  body="${body//@DIR@/$side/$corpus}"
  body="${body//@Q_SIDE@/$question}"
  {
    printf 'Set Theme %s\n' "$(tr -d '\n' < "$here/themes/eulix-$scheme.json")"
    printf 'Set FontFamily "%s"\n' "$DEMO_FONT"
    sed 's/^Set Width .*/Set Width 1000/' "$here/settings.tape"
    printf 'Output take.txt\n'
    printf 'Env REPO "%s"\nEnv WORK "%s"\nEnv CLIP_SCHEME "%s"\n' "$repo" "$work" "$scheme"
    printf '%s\n' "$body"
  } > "$run/run.tape"
  local -a unset_args=()
  for v in $(compgen -e | grep -E '^(CLAUDECODE|CLAUDE_CODE_|CLAUDE_PID$|CLAUDE_EFFORT$)' || true); do
    unset_args+=(-u "$v")
  done
  (cd "$run" && env "${unset_args[@]}" REPO="$repo" WORK="$work" vhs run.tape </dev/null >/dev/null 2>"$run/vhs.err") \
    || { echo "sidebyside.sh: vhs failed ($side):" >&2; head -5 "$run/vhs.err" >&2; exit 1; }
  bg="$(magick "$run/frames/frame-text-00001.png" -format '%[hex:p{0,0}]' info:)"
  ffmpeg -loglevel error -framerate 50 -i "$run/frames/frame-text-%05d.png" \
    -framerate 50 -i "$run/frames/frame-cursor-%05d.png" \
    -filter_complex "[0][1]overlay,pad=iw+40:ih+40:20:20:color=0x$bg" -c:v libx264 -pix_fmt yuv444p -crf 12 \
    "$run/take.mp4"
  rm -rf "$run/frames"
  claude --version 2>/dev/null | awk '{ print $1 }' > "$run/claude-version" || true
  rm -f "$run/usage-banner"
  if grep -Eq "$USAGE_BANNER_RE" "$run/take.txt" 2>/dev/null; then
    grep -Eom1 "$USAGE_BANNER_RE.*" "$run/take.txt" > "$run/usage-banner"
    echo "sidebyside.sh: the $side take shows Claude's usage banner: re-record later or mask it" >&2
  fi
}
if (( reuse )); then
  for side in plain eulix; do
    [[ -f "$scratch/$side-$scheme/take.mp4" ]] || { echo "sidebyside.sh: --reuse: no $scratch/$side-$scheme/take.mp4" >&2; exit 1; }
  done
  echo "Reusing the takes in $scratch (no new Claude queries)"
else
  prepare_copies
  echo "Recording: $question"
  record_side plain
  record_side eulix
fi

# One speed-up for both halves, chosen so the longer take fits CLIP_SECONDS;
# the shorter one holds its last frame.
dur() { ffprobe -v error -show_entries format=duration -of csv=p=0 "$1"; }
dp="$(dur "$scratch/plain-$scheme/take.mp4")"
de="$(dur "$scratch/eulix-$scheme/take.mp4")"
k="$(python3 -c "import math; print(max(1, math.ceil(max($dp, $de) / $CLIP_SECONDS)))")"
font="$(fc-match -f '%{file}' "$DEMO_FONT:style=Bold")"
if [[ $scheme == dark ]]; then fg='a78bfa'; bar='1a2030'; else fg='6250d6'; bar='e7e3fb'; fi
out="${EULIX_SIDEBYSIDE_OUT:-$repo/.github/readme/sidebyside-$scheme}"

# The stats strips, as images: one font size for both halves, the largest
# (17 px at most) at which the longer caption fits the half's width.
half_w="$(ffprobe -v error -select_streams v -show_entries stream=width -of csv=p=0 "$scratch/plain-$scheme/take.mp4")"
pt=17
while (( pt > 10 )); do
  wmax=0
  for c in "$cap_plain" "$cap_eulix"; do
    w="$(magick -font "$font" -pointsize "$pt" label:"$c" -format '%w' info:)"
    if (( w > wmax )); then wmax=$w; fi
  done
  if (( wmax <= half_w - 40 )); then break; fi
  pt=$(( pt - 1 ))
done
for side in plain eulix; do
  c="cap_$side"
  magick -size "${half_w}x$STRIP" "xc:#$bar" -gravity West -font "$font" -pointsize "$pt" \
    -fill "#$fg" -annotate +20+0 "${!c}" "$scratch/strip-$side-$scheme.png"
done

ffmpeg -y -loglevel error \
  -i "$scratch/plain-$scheme/take.mp4" -i "$scratch/eulix-$scheme/take.mp4" \
  -i "$scratch/strip-plain-$scheme.png" -i "$scratch/strip-eulix-$scheme.png" \
  -filter_complex "
    [0]setpts=PTS/$k,tpad=stop_mode=clone:stop_duration=$CLIP_SECONDS,trim=0:$CLIP_SECONDS,
       pad=iw:ih+$STRIP:0:0:color=0x${bar}[l0];
    [l0][2]overlay=0:H-${STRIP}[l];
    [1]setpts=PTS/$k,tpad=stop_mode=clone:stop_duration=$CLIP_SECONDS,trim=0:$CLIP_SECONDS,
       pad=iw:ih+$STRIP:0:0:color=0x${bar}[r0];
    [r0][3]overlay=0:H-${STRIP}[r];
    [l][r]hstack,drawtext=fontfile='$font':text='▸▸ ${k}×':fontcolor=0x$fg:fontsize=20:x=w-90:y=12[v]" \
  -map "[v]" -r 25 -c:v libx264 -pix_fmt yuv420p -crf 20 "$out.mp4"
ffmpeg -y -loglevel error -i "$out.mp4" \
  -vf "fps=12,scale=1600:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=bayer" \
  "$out.gif"
echo "Wrote $out.mp4 and $out.gif (${k}× speed-up)"
echo "  left : $cap_plain"
echo "  right: $cap_eulix"

# --raw: each half uncut for a video editor, next to record.sh's export.
if (( export_raw )); then
  mkdir -p "$raw/graphics"
  head_s="$(typed_head_s "$question")"
  tsv="$raw/clips.tsv"
  hdr=$'clip\tseconds\tstep\tcaption\tspeed_up_from_s\tspeed_up_to_s\tclaude\twarning'
  rows=""
  for side in plain eulix; do
    run="$scratch/$side-$scheme"
    clip="06-sidebyside-$side.mp4"
    cp "$run/take.mp4" "$raw/$clip"
    cp "$scratch/strip-$side-$scheme.png" "$raw/graphics/stats-$side.png"
    len="$(dur "$raw/$clip")"
    ver="$(cat "$run/claude-version" 2>/dev/null || echo -)"
    warn="-"; [[ -f "$run/usage-banner" ]] && warn="usage banner on screen: $(<"$run/usage-banner")"
    read -r from to < <(awk -v l="$len" -v f="$head_s" 'BEGIN { e = l - 4; if (e <= f) print "- -"; else printf "%s %.2f\n", f, e }')
    rows+="$clip"$'\t'"$len"$'\t6\t'"$CAP_6"$'\t'"$from"$'\t'"$to"$'\t'"$ver"$'\t'"$warn"$'\n'
    echo "  raw: $raw/$clip ($len s)"
  done
  { [[ -f "$tsv" ]] && tail -n +2 "$tsv" | grep -v '^06-sidebyside-'; printf '%s' "$rows"; } | sort > "$tsv.new"
  { printf '%s\n' "$hdr"; cat "$tsv.new"; } > "$tsv" && rm "$tsv.new"
  echo "  raw: graphics/stats-{plain,eulix}.png, rows in $tsv"
  n="$(tail -n +2 "$tsv" | awk -F'\t' '$7 != "-" { print $7 }' | sort -u | wc -l)"
  (( n <= 1 )) || echo "sidebyside.sh: the Claude takes in $raw ran on $n different Claude Code versions (see clips.tsv)" >&2
fi
