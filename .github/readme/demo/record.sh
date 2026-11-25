#!/usr/bin/env bash
# Record the README demo clips. See "README demo" in docs/scripts.md.
#
# Usage: record.sh <command> [scratch-dir]
#   all          walkthrough scenes, both teaser clips, then the edited video
#   teaser       only the README loop (.github/readme/demo-{dark,light}.webp)
#   walkthrough  only the video (<scratch>/out/eulix-walkthrough.mp4)
#   loop         rebuild the README loops from the takes already recorded plus
#                viz/vizeulize-{dark,light}.mp4 (no new Claude queries)
#   raw          walkthrough scenes and the dark Claude take, exported uncut
#                for a video editor (see export); no README loops, no video
#   export       export the takes already recorded for a video editor (no
#                recording, no Claude queries)
#   tape <name> [dark|light]   record one tape into <scratch>/<name>-<scheme>
#
# The export goes to $EULIX_RAW_DIR (default <scratch>/raw): one mp4 per
# scene at 1x, 50 fps, no captions or speed-ups; graphics/ with the caption
# bars, title and end cards as PNGs in the video's palette; and clips.tsv
# with each clip's length, caption, the wait worth speeding up, the Claude
# Code version and any warnings. sidebyside.sh --raw adds its two halves to
# the same folder.
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
raw="${EULIX_RAW_DIR:-$scratch/raw}"

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
    printf 'Output take.txt\n'
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
  check_take "$run" "$name ($scheme)"
  echo "  $name ($scheme): $(find "$run/composed" -name 'f*.png' | wc -l) frames"
}

# check_take <run-dir> <label>: note the Claude Code version a take ran with
# and warn when Claude's usage banner is on screen (VHS writes the screen
# text to take.txt). Both end up in the export's clips.tsv.
check_take() {
  local run="$1" label="$2"
  claude --version 2>/dev/null | awk '{ print $1 }' > "$run/claude-version" || true
  rm -f "$run/usage-banner"
  if [[ -f "$run/take.txt" ]] && grep -Eq "$USAGE_BANNER_RE" "$run/take.txt"; then
    grep -Eom1 "$USAGE_BANNER_RE.*" "$run/take.txt" > "$run/usage-banner"
    echo "record.sh: $label shows Claude's usage banner: re-record later or mask it" >&2
  fi
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

# --- Export for a video editor ------------------------------------------------

# encode_raw <frames-dir> <out.mp4>: every frame at 50 fps, 4:4:4 so coloured
# terminal text stays sharp. Editing input, not for upload.
encode_raw() {
  ffmpeg -loglevel error -y -framerate 50 -i "$1/f%05d.png" -r 50 \
    -c:v libx264 -crf 12 -preset medium -pix_fmt yuv444p "$2"
}
seconds() { ffprobe -v error -show_entries format=duration -of csv=p=0 "$1"; }

# notes_set <clip> <columns...>: write the clip's row in clips.tsv, replacing
# an earlier one; record.sh and sidebyside.sh share the file.
notes_set() {
  local tsv="$raw/clips.tsv" clip="$1"; shift
  local head=$'clip\tseconds\tstep\tcaption\tspeed_up_from_s\tspeed_up_to_s\tclaude\twarning'
  { [[ -f "$tsv" ]] && tail -n +2 "$tsv" | awk -F'\t' -v c="$clip" '$1 != c'
    local IFS=$'\t'; printf '%s\t%s\n' "$clip" "$*"
  } | sort > "$tsv.new"
  { printf '%s\n' "$head"; cat "$tsv.new"; } > "$tsv" && rm "$tsv.new"
}

# render_graphics <width> <terminal-height>: the caption bars (one per step),
# title and end cards, drawn like edit.sh draws them.
render_graphics() {
  local w="$1" h=$(( $2 + CAPTION_BAR )) g="$raw/graphics" i cap font font_bold charw
  font="$(fc-match -f '%{file}' "$DEMO_FONT")"
  font_bold="$(fc-match -f '%{file}' "$DEMO_FONT:style=Bold")"
  charw=$(( 24 * 6 / 10 ))
  mkdir -p "$g"
  for i in 1 2 3 4 5 6; do
    cap="CAP_$i"
    magick -size "${w}x$CAPTION_BAR" "xc:#$BG" -gravity West \
      -font "$font_bold" -fill "#$TEAL" -pointsize 24 -annotate +40+2 "$i ·" \
      -font "$font" -fill "#$FG" -annotate "+$(( 40 + 4 * charw ))+2" "${!cap}" \
      "$g/caption-$i.png"
  done
  magick -size "${w}x$h" "xc:#$BG" -gravity center \
    -font "$font_bold" -fill "#$FG" -pointsize 72 -annotate +0-60 "$TITLE" \
    -font "$font" -fill "#$TEAL" -pointsize 30 -annotate +0+20 "$TAGLINE" \
    -fill "#$DIM" -pointsize 20 -annotate +0+90 "Recorded on Flask $FLASK_TAG · waits sped up and labelled" \
    "$g/title.png"
  magick -size "${w}x$h" "xc:#$BG" -gravity center \
    -font "$font" -fill "#$DIM" -pointsize 22 -annotate +0-90 'Install' \
    -fill "#$FG" -pointsize 22 -annotate +0-40 "$INSTALL" \
    -font "$font_bold" -fill "#$VIOLET" -pointsize 34 -annotate +0+50 'github.com/Nurysso/eulix' \
    "$g/end.png"
  # Speed badges: drop the one matching the speed-up you set on top of a clip.
  for i in 2 3 4 5 6 8 10 12 16; do
    magick -background none -font "$font_bold" -fill "#$VIOLET" -pointsize 22 \
      label:"▸▸ $i× speed" "$g/badge-${i}x.png"
  done
}

# export_raw: every take recorded so far, uncut, plus graphics and clips.tsv.
export_raw() {
  local -a clips=(
    "scene1-init-dark|01-init|1|$CAP_1|-|-"
    "scene2-analyze-dark|02-analyze|2|$CAP_2|1.8|-3"
    "scene3-query-dark|03-query|3|$CAP_3|-|-"
    "scene4-register-dark|04a-register|4|$CAP_4|-|-"
    "teaser-dark|04b-agent|4|$CAP_4|$(typed_head_s "$Q_AGENT")|-3"
  )
  local spec dir name step cap from to run len warn ver size=""
  mkdir -p "$raw"
  echo "Exporting to $raw"
  for spec in "${clips[@]}"; do
    IFS='|' read -r dir name step cap from to <<<"$spec"
    run="$scratch/$dir"
    [[ -d "$run/composed" ]] || { echo "  skip $name: no $run (record it first)"; continue; }
    [[ -n $size ]] || size="$(magick "$run/composed/f00001.png" -format '%w %h' info:)"
    encode_raw "$run/composed" "$raw/$name.mp4"
    len="$(seconds "$raw/$name.mp4")"
    # A negative "to" counts back from the end of the clip; a range that
    # ends before it starts (a short take) is dropped.
    if [[ $to == -[0-9]* ]]; then
      to="$(awk -v l="$len" -v f="$from" -v t="$to" 'BEGIN { e = l + t; if (e <= f + 0) print "-"; else printf "%.2f", e }')"
      [[ $to == - ]] && from=-
    fi
    ver="-" warn="-"
    [[ $dir == teaser-* && -f "$run/claude-version" ]] && ver="$(<"$run/claude-version")"
    [[ -f "$run/usage-banner" ]] && warn="usage banner on screen: $(<"$run/usage-banner")"
    notes_set "$name.mp4" "$len" "$step" "$cap" "$from" "$to" "$ver" "$warn"
    echo "  $name.mp4: $len s"
  done
  local viz="$here/viz/vizeulize-dark.mp4"
  if [[ -f "$viz" ]]; then
    cp "$viz" "$raw/05-vizeulize.mp4"
    [[ -f "$viz.steps.json" ]] && cp "$viz.steps.json" "$raw/05-vizeulize.steps.json"
    notes_set "05-vizeulize.mp4" "$(seconds "$viz")" 5 "$CAP_5" - - - \
      "screen recording: scale to fit the terminal area"
    echo "  05-vizeulize.mp4 (from viz/)"
  else
    echo "  skip 05-vizeulize: no $viz (viz/vizeulize-bot.mjs --record)"
  fi
  if [[ -n $size ]]; then
    render_graphics "${size% *}" "${size#* }"
    echo "  graphics/: caption-1..6, title, end, badge-Nx (${size% *} px wide)"
  fi
  check_versions
  echo "  notes: $raw/clips.tsv"
}

# Warn when the Claude takes in the export ran on different Claude Code
# versions (the banner shows it).
check_versions() {
  [[ -f "$raw/clips.tsv" ]] || return 0
  local n
  n="$(tail -n +2 "$raw/clips.tsv" | awk -F'\t' '$7 != "-" { print $7 }' | sort -u | wc -l)"
  (( n <= 1 )) || echo "record.sh: the Claude takes in $raw ran on $n different Claude Code versions (see clips.tsv)" >&2
}

case "$cmd" in
  all)
    preflight
    walkthrough_scenes
    echo "Recording the teaser"
    make_teaser dark
    make_teaser light
    "$here/edit.sh" "$scratch"
    export_raw
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
    export_raw
    ;;
  raw)
    preflight
    walkthrough_scenes
    echo "Recording the Claude take (dark)"
    record_tape teaser dark "$scratch/teaser-dark"
    export_raw
    ;;
  export)
    export_raw
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
    sed -n '2,28p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
esac
