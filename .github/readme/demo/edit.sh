#!/usr/bin/env bash
# Edit the recorded walkthrough scenes into one captioned video.
#
# Usage: edit.sh <scratch-dir>
# Reads <scratch>/{scene1-init,scene2-analyze,scene3-query,scene4-register,
# teaser}-dark/composed/ (written by record.sh), plus viz/vizeulize-dark.mp4
# when it exists (viz/vizeulize-bot.mjs), and writes
# <scratch>/out/eulix-walkthrough.mp4. The video is uploaded to GitHub by
# hand, not committed.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=demo.env
source "$here/demo.env"
scratch="$(cd "${1:?usage: edit.sh <scratch-dir>}" && pwd)"
out="$scratch/out"
tmp="$scratch/edit"
rm -rf "$tmp" && mkdir -p "$tmp" "$out"

# Eulix dark palette (themes/eulix-dark.json).
BG=1a2030 FG=e7e9ee DIM=8b93a7 VIOLET=a78bfa TEAL=5eead4
BAR=64        # caption bar above the terminal, px
XFADE=0.3     # crossfade between segments, s
FONT="$(fc-match -f '%{file}' "$DEMO_FONT")"
FONT_BOLD="$(fc-match -f '%{file}' "$DEMO_FONT:style=Bold")"
INSTALL='bash <(curl -fsSL https://raw.githubusercontent.com/Nurysso/eulix/main/install.sh)'

# Segments in order. Fields: run dir | step | caption | head s | tail s |
# target s for the sped-up middle (0 = play at 1x). The head and tail play at
# 1x; the middle keeps every k-th frame so it lasts about the target, and
# shows a "k×" badge.
teaser_head="$(awk -v n=${#Q_AGENT} 'BEGIN { printf "%.2f", (800 + n * 40 + 600 + 500) / 1000 }')"
SEGMENTS=(
  "scene1-init-dark|1|Set up eulix in a repo|0|0|0"
  "scene2-analyze-dark|2|Index it: parse, call graph, embeddings|1.8|3|16"
  "scene3-query-dark|3|Ask from the CLI: ranked, token-budgeted context|0|0|0"
  "scene4-register-dark|4|Your agent asks eulix over MCP|0|0|0"
  "teaser-dark|4|Your agent asks eulix over MCP|$teaser_head|3|14"
)

size="$(magick "$scratch/scene1-init-dark/composed/f00001.png" -format '%wx%h' info:)"
W="${size%x*}" H=$(( ${size#*x} + BAR ))
charw=$(( 24 * 6 / 10 ))   # monospace advance at 24 px

encode_png_seq() {  # <frames-dir> <filter> <out.mp4>
  ffmpeg -loglevel error -y -framerate 50 -i "$1/f%05d.png" -vf "$2" -r 50 \
    -c:v libx264 -crf 12 -preset veryfast -pix_fmt yuv420p "$3"
}

segs=()
idx=0
for spec in "${SEGMENTS[@]}"; do
  IFS='|' read -r dir step caption head_s tail_s target_s <<<"$spec"
  frames="$scratch/$dir/composed"
  [[ -d "$frames" ]] || { echo "edit.sh: missing $frames (run record.sh first)" >&2; exit 1; }
  n="$(find "$frames" -name 'f*.png' | wc -l)"

  printf '%s ·' "$step" > "$tmp/step$idx.txt"
  printf '%s' "$caption" > "$tmp/caption$idx.txt"
  vf=""
  badge=""
  read -r hf tf tgt < <(awk -v h="$head_s" -v t="$tail_s" -v g="$target_s" \
    'BEGIN { printf "%d %d %d\n", h * 50, t * 50, g * 50 }')
  span=$(( n - hf - tf ))
  if (( tgt > 0 && span > tgt )); then
    k=$(( (span + tgt - 1) / tgt ))
    end=$(( n - tf ))
    kept=$(( (span + k - 1) / k ))
    vf="select='lt(n,$hf)+gte(n,$end)+between(n,$hf,$end-1)*not(mod(n-$hf,$k))',"
    printf '▸▸ %s× speed' "$k" > "$tmp/badge$idx.txt"
    badge=",drawtext=fontfile=$FONT_BOLD:textfile=$tmp/badge$idx.txt:fontsize=22:fontcolor=0x$VIOLET:x=w-tw-40:y=($BAR-th)/2+4:enable='between(n,$hf,$(( hf + kept - 1 )))'"
  fi
  vf+="setpts=N/50/TB,pad=iw:ih+$BAR:0:$BAR:color=0x$BG"
  vf+=",drawtext=fontfile=$FONT_BOLD:textfile=$tmp/step$idx.txt:fontsize=24:fontcolor=0x$TEAL:x=40:y=($BAR-th)/2+4"
  vf+=",drawtext=fontfile=$FONT:textfile=$tmp/caption$idx.txt:fontsize=24:fontcolor=0x$FG:x=$(( 40 + 4 * charw )):y=($BAR-th)/2+4"
  vf+="$badge"

  encode_png_seq "$frames" "$vf" "$tmp/seg$idx.mp4"
  segs+=("$tmp/seg$idx.mp4")
  echo "  segment $idx ($dir): $(ffprobe -v error -show_entries format=duration -of csv=p=0 "$tmp/seg$idx.mp4") s"
  idx=$(( idx + 1 ))
done

# Step 5: the vizEulize screen recording made with viz/vizeulize-bot.mjs
# (`--record viz/vizeulize-dark.mp4`). It plays at 1x, scaled to fit the
# terminal area, with the same caption bar. Skipped when it hasn't been made.
VIZ="${VIZ_DARK:-$here/viz/vizeulize-dark.mp4}"
if [[ -f "$VIZ" ]]; then
  printf '5 ·' > "$tmp/step_viz.txt"
  printf '%s' 'See the call graph in your browser: eulix vizeulize' > "$tmp/caption_viz.txt"
  ih=$(( H - BAR ))
  vf="fps=50,scale=$W:$ih:force_original_aspect_ratio=decrease:force_divisible_by=2"
  vf+=",pad=$W:$ih:(ow-iw)/2:(oh-ih)/2:color=0x$BG,setsar=1,pad=iw:ih+$BAR:0:$BAR:color=0x$BG"
  vf+=",drawtext=fontfile=$FONT_BOLD:textfile=$tmp/step_viz.txt:fontsize=24:fontcolor=0x$TEAL:x=40:y=($BAR-th)/2+4"
  vf+=",drawtext=fontfile=$FONT:textfile=$tmp/caption_viz.txt:fontsize=24:fontcolor=0x$FG:x=$(( 40 + 4 * charw )):y=($BAR-th)/2+4"
  ffmpeg -loglevel error -y -i "$VIZ" -vf "$vf" -r 50 -an \
    -c:v libx264 -crf 12 -preset veryfast -pix_fmt yuv420p "$tmp/seg_viz.mp4"
  segs+=("$tmp/seg_viz.mp4")
  echo "  segment viz ($(basename "$VIZ")): $(ffprobe -v error -show_entries format=duration -of csv=p=0 "$tmp/seg_viz.mp4") s"
else
  echo "  no $VIZ: the video ends without the vizEulize part"
fi

# Title and end cards, same size as the segments.
card() {  # <out.png> then magick draw args
  local dst="$1"; shift
  magick -size "${W}x${H}" "xc:#$BG" "$@" "$dst"
}
card "$tmp/title.png" \
  -gravity center -font "$FONT_BOLD" -fill "#$FG" -pointsize 72 -annotate +0-60 'Eulix' \
  -font "$FONT" -fill "#$TEAL" -pointsize 30 -annotate +0+20 'ranked context for coding agents, over MCP' \
  -fill "#$DIM" -pointsize 20 -annotate +0+90 "Recorded on Flask $FLASK_TAG · waits sped up and labelled"
card "$tmp/end.png" \
  -gravity center -font "$FONT" -fill "#$DIM" -pointsize 22 -annotate +0-90 'Install' \
  -fill "#$FG" -pointsize 22 -annotate +0-40 "$INSTALL" \
  -font "$FONT_BOLD" -fill "#$VIOLET" -pointsize 34 -annotate +0+50 'github.com/Nurysso/eulix'
for c in title:3 end:5; do
  ffmpeg -loglevel error -y -loop 1 -framerate 50 -t "${c#*:}" -i "$tmp/${c%%:*}.png" -r 50 \
    -c:v libx264 -crf 12 -preset veryfast -pix_fmt yuv420p "$tmp/${c%%:*}.mp4"
done
parts=("$tmp/title.mp4" "${segs[@]}" "$tmp/end.mp4")

# Crossfade everything into one stream: offset_k = length so far - XFADE.
# CRF 24 keeps the ~2 min video under GitHub's 10 MB attachment limit.
inputs=() filter="" len="" prev="[0:v]"
for (( i = 0; i < ${#parts[@]}; i++ )); do
  inputs+=(-i "${parts[i]}")
  d="$(ffprobe -v error -show_entries format=duration -of csv=p=0 "${parts[i]}")"
  if (( i == 0 )); then len="$d"; continue; fi
  offset="$(awk -v l="$len" -v x="$XFADE" 'BEGIN { printf "%.3f", l - x }')"
  filter+="${prev}[$i:v]xfade=transition=fade:duration=$XFADE:offset=$offset[v$i];"
  prev="[v$i]"
  len="$(awk -v l="$len" -v d="$d" -v x="$XFADE" 'BEGIN { printf "%.3f", l + d - x }')"
done
ffmpeg -loglevel error -y "${inputs[@]}" -filter_complex "${filter%;}" -map "$prev" -r 30 \
  -c:v libx264 -crf 24 -preset slow -pix_fmt yuv420p -movflags +faststart \
  "$out/eulix-walkthrough.mp4"

final="$out/eulix-walkthrough.mp4"
echo "Walkthrough: $(ffprobe -v error -show_entries format=duration -of csv=p=0 "$final") s," \
  "$(( $(stat -c %s "$final") / 1024 )) KB -> $final"
