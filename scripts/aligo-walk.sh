#!/usr/bin/env bash
# Walk every regular file in $PWD through `aligo view`.
# Keys while viewing:
#   Enter → next file  (on the last file: run `aligo check`)
#   Space → re-render the current file
#   ?     → help
#   q     → quit

set -uo pipefail
shopt -s nullglob

# ========== sanity ==========
if ! command -v aligo >/dev/null 2>&1; then
    printf 'error: aligo not found in PATH\n' >&2
    exit 127
fi

# ========== collect files once so we know the total ==========
files=()
for f in *; do
    [[ -f "$f" ]] && files+=("$f")
done

if (( ${#files[@]} == 0 )); then
    printf 'No regular files in %s\n' "$PWD" >&2
    exit 0
fi
total=${#files[@]}

# ========== terminal safety net ==========
_orig_stty=$(stty -g 2>/dev/null) || _orig_stty=
cleanup() { [[ -n "$_orig_stty" ]] && stty "$_orig_stty" 2>/dev/null; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ========== Clear screen AND (best-effort) scrollback for a truly fresh view. ==========
clear_screen() {
    clear 2>/dev/null || printf '\033[H\033[2J'
    printf '\033[3J' 2>/dev/null || true
}

show_help() {
    clear_screen
    cat >&2 <<'EOF'
Keys while viewing:

  Enter   advance to the next file
  Space   re-render the current file
  ?       show this help
  q       quit

EOF
    printf 'Press any key to return...' >&2
    IFS= read -rsn1 _ || true
}

# ========== main loop ==========
n=0
for f in "${files[@]}"; do
    n=$((n + 1))
    while :; do
        clear_screen
        aligo view "$f" || true

        if (( n == total )); then
            prompt="Enter=check   Space=again   ?=help   q=quit"
        else
            prompt="Enter=next    Space=again   ?=help   q=quit"
        fi
        printf '\n  [%d/%d] %s\n  %s  ' "$n" "$total" "$f" "$prompt" >&2

        if ! IFS= read -rsn1 key; then
            printf '\n' >&2
            exit 0                # EOF / Ctrl-D
        fi

        case "$key" in
            '')   break ;;        # Enter → next / check
            ' ')  continue ;;     # Space → re-render
            q|Q)  printf '\n' >&2; exit 0 ;;
            '?')  show_help; continue ;;
            *)    continue ;;     # ignore stray keys
        esac
    done
done

# ========== final check ==========
clear_screen
printf '=== all files viewed ===\n\n' >&2
aligo check . || true
printf '\nPress any key to exit...' >&2
IFS= read -rsn1 _ || true
printf '\n' >&2
