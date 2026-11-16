#!/usr/bin/env bash
# Record the README demo clips. See "README demo" in docs/scripts.md.
#
# Usage: record.sh <command> [scratch-dir]
#   all          walkthrough scenes, both teaser clips, then the edited video
#   teaser       only the README loop (.github/readme/demo-{dark,light}.webp)
#   walkthrough  only the video (<scratch>/out/eulix-walkthrough.mp4)
#   loop         rebuild the README loops from the takes already recorded plus
#                viz/vizeulize-{dark,light}.mp4 (no new Claude queries)
#   tape <name> [dark|light]   record one tape into <scratch>/<name>-<scheme>
#
# Work goes under the scratch dir (default /tmp/eulix-demo). Claude Code's
# banner shows the working folder, so keep that path neutral. The two README
# loops are written to .github/readme/ (git-ignored: upload them to GitHub and
# link them from the README).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(git -C "$here" rev-parse --show-toplevel)"
# shellcheck source=demo.env
source "$here/demo.env"

cmd="${1:-}"
case "$cmd" in
  tape) scratch="${4:-/tmp/eulix-demo}" ;;
  *) scratch="${2:-/tmp/eulix-demo}" ;;
esac
mkdir -p "$scratch"
scratch="$(cd "$scratch" && pwd)"
work="$scratch/work"

# Teaser timing. The visible part opens with 0.8 s, the typed question at
# 40 ms per character, and 0.6 s before Enter (teaser.tape); everything from
# half a second after Enter until the last 3 s is the wait that gets sped up.
TEASER_SECONDS=12
TEASER_TAIL_FRAMES=150

preflight() {
  local tool
  for tool in vhs ttyd ffmpeg ffprobe magick eulix claude git; do
    command -v "$tool" >/dev/null || { echo "record.sh: $tool is not on PATH" >&2; exit 1; }
  done
  if [[ "$(fc-match -f '%{family}' "$DEMO_FONT")" != "$DEMO_FONT"* ]]; then
    echo "record.sh: font '$DEMO_FONT' is not installed (VHS would fall back silently)" >&2
    exit 1
  fi
}

# Environment for the recorded shell: drop the variables a parent Claude Code
# session sets, or the recorded Claude would run as a child session.
vhs_env() {
  local v
  for v in $(compgen -e | grep -E '^(CLAUDECODE|CLAUDE_CODE_|CLAUDE_PID$|CLAUDE_EFFORT$)' || true); do
    printf -- '-u\n%s\n' "$v"
  done
}

# record_tape <tape-name> <scheme> <run-dir>
# Runs settings.tape + <tape-name>.tape in <run-dir> and composes the text and
# cursor layers into <run-dir>/composed/f%05d.png, padded with the background.
record_tape() {
  local name="$1" scheme="$2" run="$3" body bg
  rm -rf "$run" && mkdir -p "$run/composed"

  body="$(<"$here/$name.tape")"
  body="${body//@Q_CLI@/$Q_CLI}"
  body="${body//@Q_AGENT@/$Q_AGENT}"
  {
    printf 'Set Theme %s\n' "$(tr -d '\n' < "$here/themes/eulix-$scheme.json")"
    printf 'Set FontFamily "%s"\n' "$DEMO_FONT"
    cat "$here/settings.tape"
    printf 'Env REPO "%s"\nEnv WORK "%s"\nEnv CLIP_SCHEME "%s"\n' "$repo" "$work" "$scheme"
    printf '%s\n' "$body"
  } > "$run/run.tape"

  local -a unset_args
  mapfile -t unset_args < <(vhs_env)
  if ! (cd "$run" && env "${unset_args[@]}" REPO="$repo" WORK="$work" vhs run.tape \
        </dev/null >/dev/null 2>"$run/vhs.err"); then
    echo "record.sh: vhs failed on $name ($scheme):" >&2
    grep -v '^\s*$' "$run/vhs.err" | head -5 >&2
    exit 1
  fi

  bg="$(magick "$run/frames/frame-text-00001.png" -format '%[hex:p{0,0}]' info:)"
  ffmpeg -loglevel error -framerate 50 -i "$run/frames/frame-text-%05d.png" \
    -framerate 50 -i "$run/frames/frame-cursor-%05d.png" \
    -filter_complex "[0][1]overlay,pad=iw+80:ih+80:40:40:color=0x$bg" \
    "$run/composed/f%05d.png"
  rm -rf "$run/frames"
  echo "  $name ($scheme): $(find "$run/composed" -name 'f*.png' | wc -l) frames"
}

# A fresh Flask checkout, indexed (scenes 1-2) and registered (scene 4).
walkthrough_scenes() {
  echo "Recording walkthrough scenes in $work"
  "$here/stage.sh" "$work" --no-mcp
  local s
  for s in scene1-init scene2-analyze scene3-query scene4-register; do
    record_tape "$s" dark "$scratch/$s-dark"
  done
}

# The teaser needs an indexed Flask with eulix registered. Reuse the
# walkthrough's checkout when there is one; otherwise set it up off camera.
ensure_work() {
  if [[ ! -f "$work/flask/.eulix/analysis-complete.json" ]]; then
    echo "Preparing an indexed Flask in $work (off camera)"
    "$here/stage.sh" "$work" --no-mcp
    record_tape scene1-init dark "$scratch/prep-init"
    record_tape scene2-analyze dark "$scratch/prep-analyze"
    rm -rf "$scratch/prep-init" "$scratch/prep-analyze"
  fi
  if [[ ! -f "$work/flask/.mcp.json" ]]; then
    (cd "$work/flask" && claude mcp add --scope project eulix -- eulix mcp >/dev/null)
  fi
}

# make_teaser <scheme>: record, speed up the wait with a badge, encode WebP.
make_teaser() {
  local scheme="$1"
  local run="$scratch/teaser-$scheme" out="$repo/.github/readme/demo-$scheme.webp"
  record_tape teaser "$scheme" "$run"

  local -a frames
  mapfile -t frames < <(find "$run/composed" -name 'f*.png' | sort)
  local n=${#frames[@]}
  local head=$(( (800 + ${#Q_AGENT} * 40 + 600 + 500) / 20 ))
  local tail=$TEASER_TAIL_FRAMES
  local span=$(( n - head - tail ))
  local budget=$(( TEASER_SECONDS * 50 - head - tail ))
  local k=1
  (( span > budget && budget > 0 )) && k=$(( (span + budget - 1) / budget ))

  local font fg bg
  font="$(fc-match -f '%{file}' "$DEMO_FONT:style=Bold")"
  if [[ $scheme == dark ]]; then fg='#a78bfa'; bg='#2a3142'; else fg='#6250d6'; bg='#e7e3fb'; fi

  rm -rf "$run/final" && mkdir "$run/final"
  local f i=0 dst
  for (( f = 0; f < n; f++ )); do
    dst="$run/final/f$(printf %05d "$i").png"
    if (( k > 1 && f >= head && f < n - tail )); then
      (( (f - head) % k == 0 )) || continue
      magick "${frames[f]}" -gravity SouthEast -font "$font" -pointsize 22 \
        -fill "$fg" -undercolor "$bg" -annotate +44+12 " ▸▸ ${k}× " "$dst"
    else
      ln -s "${frames[f]}" "$dst"
    fi
    i=$(( i + 1 ))
  done
  echo "  teaser ($scheme): ${k}× wait, $(( i / 50 )) s of terminal"
  encode_teaser "$scheme"
}

# encode_teaser <scheme>: the README loop is the Claude take (teaser-<scheme>/
# final, 50 fps), then, when viz/vizeulize-<scheme>.mp4 and its .steps.json
# from vizeulize-bot.mjs exist, the vizEulize part from VIZ_FROM up to VIZ_TO
# (25 fps). The step times are logged from when wf-recorder started, a little
# before its first frame, hence the small shifts.
VIZ_FROM='Search for any function'
VIZ_TO='And what it calls'
encode_teaser() {
  local scheme="$1"
  local run="$scratch/teaser-$scheme" out="$repo/.github/readme/demo-$scheme.webp"
  local viz="$here/viz/vizeulize-$scheme.mp4"
  [[ -d "$run/final" ]] || { echo "record.sh: no teaser take in $run (run: record.sh teaser)" >&2; exit 1; }
  # ffmpeg streams the frames (ImageMagick holds them all in memory, which
  # runs out with the vizEulize part added); each part keeps its frame rate.
  local -a inputs=(-framerate 50 -start_number 0 -i "$run/final/f%05d.png")
  local graph='[0:v]format=rgba[v]'

  rm -rf "$run/viz"
  if [[ -f "$viz" && -f "$viz.steps.json" ]]; then
    local t0 t1 size pad
    read -r t0 t1 < <(python3 -I - "$viz.steps.json" "$VIZ_FROM" "$VIZ_TO" <<'EOF'
import json, sys
steps = {s["caption"]: s["t"] for s in json.load(open(sys.argv[1]))}
print(f"{max(0, steps[sys.argv[2]] - 0.5):.2f} {steps[sys.argv[3]] - 0.3:.2f}")
EOF
    )
    size="$(magick "$run/final/f00000.png" -format '%wx%h' info:)"
    if [[ $scheme == dark ]]; then pad=1a2030; else pad=f6f8fa; fi
    mkdir "$run/viz"
    ffmpeg -loglevel error -ss "$t0" -to "$t1" -i "$viz" -vf \
      "fps=25,scale=${size/x/:}:force_original_aspect_ratio=decrease:force_divisible_by=2,pad=${size/x/:}:(ow-iw)/2:(oh-ih)/2:color=0x$pad" \
      "$run/viz/f%05d.png"
    inputs+=(-framerate 25 -i "$run/viz/f%05d.png")
    graph='[0:v]format=rgba[a];[1:v]format=rgba[b];[a][b]concat=n=2:v=1:a=0[v]'
    echo "  + vizEulize $t0–$t1 s from $(basename "$viz")"
  else
    echo "  (no $viz with its .steps.json: loop without the vizEulize part)"
  fi

  local -a enc=(-map '[v]' -fps_mode passthrough -c:v libwebp_anim -compression_level 4 -loop 0)
  ffmpeg -loglevel error -y "${inputs[@]}" -filter_complex "$graph" "${enc[@]}" -lossless 1 "$out"
  if (( $(stat -c %s "$out") > 2 * 1024 * 1024 )); then
    ffmpeg -loglevel error -y "${inputs[@]}" -filter_complex "$graph" "${enc[@]}" -lossless 0 -quality 80 "$out"
  fi
  echo "  loop ($scheme): $(( $(stat -c %s "$out") / 1024 )) KB -> ${out#"$repo"/}"
  (( $(stat -c %s "$out") <= 2 * 1024 * 1024 )) || echo "record.sh: $out is over 2 MB" >&2
}

case "$cmd" in
  all)
    preflight
    walkthrough_scenes
    echo "Recording the teaser"
    make_teaser dark
    make_teaser light
    "$here/edit.sh" "$scratch"
    ;;
  teaser)
    preflight
    ensure_work
    echo "Recording the teaser"
    make_teaser dark
    make_teaser light
    ;;
  walkthrough)
    preflight
    walkthrough_scenes
    [[ -d "$scratch/teaser-dark/composed" ]] || make_teaser dark
    "$here/edit.sh" "$scratch"
    ;;
  loop)
    echo "Rebuilding the README loops from $scratch"
    encode_teaser dark
    encode_teaser light
    ;;
  tape)
    name="${2:?usage: record.sh tape <name> [dark|light] [scratch-dir]}"
    preflight
    record_tape "$name" "${3:-dark}" "$scratch/$name-${3:-dark}"
    ;;
  *)
    sed -n '2,15p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
esac
