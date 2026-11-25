# One half of the README side-by-side clip. sidebyside.sh records it twice,
# once in a plain repo copy and once in a copy with eulix registered, and
# puts the two takes next to each other. @DIR@ (<side>/<corpus>) and
# @Q_SIDE@ (the benchmark task's prompt) are filled in by sidebyside.sh.
#
# Startup stays off camera (see teaser.tape for the trust-prompt keys).
Hide
Type 'source "$REPO/.github/readme/demo/claude.zsh" && cd "$WORK/@DIR@" && clear'
Enter
Type "claude"
Enter
Sleep 8s
Down
Sleep 300ms
Enter
Wait+Screen@60s /Claude Code v/
Sleep 3s
Show

Sleep 800ms
Type "@Q_SIDE@"
Sleep 600ms
Enter
Wait+Screen@300s / for \d+(m \d+)?s · done/
Sleep 4s

Hide
Ctrl+C
Sleep 500ms
Ctrl+C
Sleep 1s
